package sandbox

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
	"gitlab.inoio.de/inoio/opencode-msb/internal/termio"
)

// configFiles holds the merged configuration and parsed structures for comparison.
type configFiles struct {
	files  map[string][]byte
	parsed map[string]map[string]any
	keys   []string // sorted file names for VM comparison
}

const autoFlag = "--auto"

// loadConfigFiles builds the merged opencode configuration from the user's
// config directory, any project-specific config in .opencode-msb/opencode,
// and the embedded provider config. Returns the marshaled files, parsed
// structures, and sorted file keys.
func loadConfigFiles(userConfigDir string) (*configFiles, error) {
	providerCfg, err := config.LoadProviderConfig()
	if err != nil {
		return nil, fmt.Errorf("load provider config: %w", err)
	}
	projectConfigDir := ""
	if _, statErr := os.Stat(GetConfigPaths().projectOpencodeConfigDir()); statErr == nil {
		projectConfigDir = GetConfigPaths().projectOpencodeConfigDir()
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
	return &configFiles{
		files:  files,
		parsed: parsed,
		keys:   fileKeys,
	}, nil
}

// readVMFiles reads all files from the given directory on the VM.
//
//nolint:unparam // kept general: dir param passed from decideReconfig only at "/home/dev/.config/opencode"
func readVMFiles(
	ctx context.Context,
	sb Sandbox,
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

// configEqual compares Go-side parsed config against VM-side files.
func configEqual(goSide map[string]map[string]any, keys []string, vmData map[string][]byte) bool {
	return equalJSONFiles(goSide, keys, vmData)
}

// equalJSONFiles compares JSON files semantically (key order, number types) and
// compares non-JSON contents as raw bytes.
func equalJSONFiles(goSide map[string]map[string]any, keys []string, vmData map[string][]byte) bool {
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

// buildEnvMap reads environment variables from a file.
func buildEnvMap(filename string) map[string]string {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil
	}
	env := make(map[string]string)
	for line := range strings.SplitSeq(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.Contains(line, "=") {
			parts := strings.SplitN(line, "=", envKeyValueParts)
			if len(parts) == envKeyValueParts {
				env[parts[0]] = parts[1]
			}
		}
	}
	return env
}

// mergeEnvMaps merges multiple environment maps into one.
func mergeEnvMaps(mapsToMerge ...map[string]string) map[string]string {
	result := make(map[string]string)
	for _, m := range mapsToMerge {
		maps.Copy(result, m)
	}
	return result
}

// envContentHash returns a SHA-256 hex digest of the env map contents.
// Keys are sorted and hashed as "K=V" lines for order-independence.
func envContentHash(env map[string]string) string {
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

// secretsContentHash returns a SHA-256 hex digest of the secret entries.
// Entries are sorted by EnvVar and hashed as "ENVVAR=VALUE" lines.
func secretsContentHash(entries []msbSdk.SecretEntry) string {
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

// envChanged reports whether the applied env state differs from the desired
// env map, comparing content hashes for a stable order-independent result.
// A zero-value appliedEnv indicates "no persisted state yet" (first run),
// which is considered "changed" only when the desired map is non-empty.
func envChanged(applied EnvState, desired map[string]string) bool {
	if applied.Hash == "" {
		return len(desired) > 0
	}
	return applied.Hash != envContentHash(desired)
}

// secretsChanged reports whether the applied secret state differs from the
// desired secret entries, comparing content hashes.
// A zero-value appliedSecrets indicates "no persisted state yet" (first run),
// which is only considered "changed" when the desired slice is non-empty.
func secretsChanged(applied SecretState, desired []msbSdk.SecretEntry) bool {
	if applied.Hash == "" {
		if desired == nil {
			return false
		}
		return len(desired) > 0
	}
	return applied.Hash != secretsContentHash(desired)
}

// buildEnvState computes the content hash and sorted name list for the env map.
func buildEnvState(desired map[string]string) EnvState {
	names := make([]string, 0, len(desired))
	for k := range desired {
		names = append(names, k)
	}
	sort.Strings(names)
	return EnvState{
		Hash:  envContentHash(desired),
		Names: names,
	}
}

// buildSecretState computes the content hash and sorted name list for the secret entries.
func buildSecretState(desired []msbSdk.SecretEntry) SecretState {
	byEnv := make(map[string]msbSdk.SecretEntry, len(desired))
	for _, e := range desired {
		byEnv[e.EnvVar] = e
	}
	names := make([]string, 0, len(byEnv))
	for k := range byEnv {
		names = append(names, k)
	}
	sort.Strings(names)
	return SecretState{
		Hash:  secretsContentHash(desired),
		Names: names,
	}
}
