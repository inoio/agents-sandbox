package reprovision

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	config "gitlab.inoio.de/inoio/opencode-msb/internal/opencodeconfig"
	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox/msb"
	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox/state"
	"gitlab.inoio.de/inoio/opencode-msb/internal/termio"

	cp "gitlab.inoio.de/inoio/opencode-msb/internal/configpaths"
)

// EnvKeyValueParts is the number of parts strings.SplitN should produce for
// key=value lines.
const EnvKeyValueParts = 2

// parseKeyValueLines splits data into trimmed, non-blank, non-comment
// "key=value" lines and hands each split pair to onLine. The key and value are
// passed exactly as SplitN produced them (not re-trimmed); callers that need
// trimmed keys trim them in their post-processing.
func parseKeyValueLines(data string, onLine func(key, value string) error) error {
	for line := range strings.SplitSeq(data, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", EnvKeyValueParts)
		if len(parts) != EnvKeyValueParts {
			continue
		}
		if err := onLine(parts[0], parts[1]); err != nil {
			return err
		}
	}
	return nil
}

// ConfigFiles holds the merged configuration and parsed structures for comparison.
type ConfigFiles struct {
	Files  map[string][]byte
	Parsed map[string]map[string]any
	Keys   []string // sorted file names for VM comparison
}

// LoadConfigFiles builds the merged opencode configuration from the user's
// config directory, any project-specific config in .opencode-msb/opencode,
// and the embedded provider config. Returns the marshaled files, parsed
// structures, and sorted file keys.
func LoadConfigFiles(userConfigDir string) (*ConfigFiles, error) {
	providerCfg, err := config.LoadProviderConfig()
	if err != nil {
		return nil, fmt.Errorf("load provider config: %w", err)
	}
	projectConfigDir := ""
	if _, statErr := os.Stat(cp.GetConfigPaths().ProjectOpencodeConfigDir()); statErr == nil {
		projectConfigDir = cp.GetConfigPaths().ProjectOpencodeConfigDir()
	}
	files, err := config.BuildMergedConfig(userConfigDir, projectConfigDir, providerCfg)
	if err != nil {
		return nil, fmt.Errorf("merge config: %w", err)
	}

	parsed := make(map[string]map[string]any)
	for name, data := range files {
		var cfg map[string]any
		if err := json.Unmarshal(data, &cfg); err == nil {
			parsed[name] = cfg
		}
	}

	fileKeys := make([]string, 0, len(files))
	for k := range files {
		fileKeys = append(fileKeys, k)
	}
	sort.Strings(fileKeys)
	return &ConfigFiles{
		Files:  files,
		Parsed: parsed,
		Keys:   fileKeys,
	}, nil
}

// tmpMountPath is the mount point used for the sandbox tmpfs.
const tmpMountPath = "/tmp"

// ReadVMFiles reads all files from a sandbox directory.
func ReadVMFiles(
	ctx context.Context,
	sb msb.Sandbox,
	dir string,
	ui termio.UI,
) map[string][]byte {
	l, err := sb.FS().List(ctx, dir)
	if err != nil {
		ui.Verbosef("  list failed: %v", err)
		return nil
	}
	if len(l) == 0 {
		ui.Verbosef("  directory %q is empty or does not exist", dir)
		return nil
	}
	ui.Verbosef("  found %d entries in %s", len(l), dir)
	result := make(map[string][]byte)
	for _, e := range l {
		if e.Kind != msbSdk.FsEntryKindFile {
			ui.Verbosef("    skipping %s (kind=%s)", e.Path, e.Kind)
			continue
		}
		data, err := sb.FS().Read(ctx, e.Path)
		if err != nil {
			ui.Verbosef("    read %s failed: %v", e.Path, err)
			continue
		}
		result[filepath.Base(e.Path)] = data
		ui.Verbosef("    OK: %s (%d bytes)", e.Path, len(data))
	}
	return result
}

// ConfigEqual compares Go-side parsed config against VM-side files.
func ConfigEqual(goSide map[string]map[string]any, keys []string, vmData map[string][]byte) bool {
	return EqualJSONFiles(goSide, keys, vmData)
}

// EqualJSONFiles compares JSON files semantically (key order, number types) and
// compares non-JSON contents as raw bytes.
func EqualJSONFiles(goSide map[string]map[string]any, keys []string, vmData map[string][]byte) bool {
	for _, name := range keys {
		goVal, hasGoVal := goSide[name]
		vmBytes, hasVM := vmData[name]
		if !hasVM {
			return false
		}
		if !hasGoVal || goVal == nil {
			continue
		}
		goJSON, _ := json.Marshal(goVal)
		va, err := parseJSON(goJSON)
		if err != nil {
			return false
		}
		vb, err := parseJSON(vmBytes)
		if err != nil {
			return false
		}
		if !reflect.DeepEqual(va, vb) {
			return false
		}
	}
	return true
}

// parseJSON unmarshals data into map[string]any for deep equality comparison.
func parseJSON(data []byte) (any, error) {
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, err
	}
	return v, nil
}

// BuildEnvMap reads environment variables from a file.
func BuildEnvMap(filename string) map[string]string {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil
	}
	env := make(map[string]string)
	_ = parseKeyValueLines(string(data), func(key, value string) error {
		env[key] = value
		return nil
	})
	return env
}

// MergeEnvMaps merges multiple environment maps into one.
func MergeEnvMaps(mapsToMerge ...map[string]string) map[string]string {
	result := make(map[string]string)
	for _, m := range mapsToMerge {
		maps.Copy(result, m)
	}
	return result
}

// EnvContentHash returns a SHA-256 hex digest of the env map contents.
// Keys are sorted and hashed as "K=V" lines for order-independence.
func EnvContentHash(env map[string]string) string {
	if env == nil {
		env = map[string]string{}
	}
	lines := make([]string, 0, len(env))
	for k, v := range env {
		lines = append(lines, k+"="+v)
	}
	sort.Strings(lines)
	h := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return hex.EncodeToString(h[:])
}

// SecretsContentHash returns a SHA-256 hex digest of the secret entries.
// Entries are sorted by EnvVar and hashed as "ENVVAR=VALUE" lines.
func SecretsContentHash(entries []msbSdk.SecretEntry) string {
	if entries == nil {
		entries = []msbSdk.SecretEntry{}
	}
	byEnv := make(map[string]msbSdk.SecretEntry, len(entries))
	for _, e := range entries {
		byEnv[e.EnvVar] = e
	}
	envVars := make([]string, 0, len(byEnv))
	for k := range byEnv {
		envVars = append(envVars, k)
	}
	sort.Strings(envVars)
	var b strings.Builder
	for _, k := range envVars {
		fmt.Fprintf(&b, "%s=%s\n", k, byEnv[k].Value)
	}
	h := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(h[:])
}

// EnvChanged reports whether the applied env state differs from the desired
// env map, comparing content hashes for a stable order-independent result.
// A zero-value appliedEnv indicates "no persisted state yet" (first run),
// which is considered "changed" only when the desired map is non-empty.
func EnvChanged(applied state.EnvState, desired map[string]string) bool {
	if applied.Hash == "" {
		return len(desired) > 0
	}
	return applied.Hash != EnvContentHash(desired)
}

// SecretsChanged reports whether the applied secret state differs from the
// desired secret entries, comparing content hashes.
// A zero-value appliedSecrets indicates "no persisted state yet" (first run),
// which is only considered "changed" when the desired slice is non-empty.
func SecretsChanged(applied state.SecretState, desired []msbSdk.SecretEntry) bool {
	if applied.Hash == "" {
		if desired == nil {
			return false
		}
		return len(desired) > 0
	}
	return applied.Hash != SecretsContentHash(desired)
}

// BuildEnvState computes the content hash and sorted name list for the env map.
func BuildEnvState(desired map[string]string) state.EnvState {
	names := make([]string, 0, len(desired))
	for k := range desired {
		names = append(names, k)
	}
	sort.Strings(names)
	return state.EnvState{
		Hash:  EnvContentHash(desired),
		Names: names,
	}
}

// BuildSecretState computes the content hash and sorted name list for the secret entries.
func BuildSecretState(desired []msbSdk.SecretEntry) state.SecretState {
	byEnv := make(map[string]msbSdk.SecretEntry, len(desired))
	for _, e := range desired {
		byEnv[e.EnvVar] = e
	}
	names := make([]string, 0, len(byEnv))
	for k := range byEnv {
		names = append(names, k)
	}
	sort.Strings(names)
	return state.SecretState{
		Hash:  SecretsContentHash(desired),
		Names: names,
	}
}
