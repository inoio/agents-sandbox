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
	"strconv"
	"strings"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/opencode-sandbox/internal/agent"
	config "github.com/inoio/opencode-sandbox/internal/opencodeconfig"
	"github.com/inoio/opencode-sandbox/internal/sandbox/msb"
	"github.com/inoio/opencode-sandbox/internal/sandbox/network"
	"github.com/inoio/opencode-sandbox/internal/sandbox/state"
	"github.com/inoio/opencode-sandbox/internal/termio"

	cp "github.com/inoio/opencode-sandbox/internal/configpaths"
	"github.com/inoio/opencode-sandbox/internal/homeconfig"
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

// VMHomeDir is the sandbox home mount point. It is fixed by the project VM
// layout regardless of the configured runtime user.
const VMHomeDir = "/home/dev"

// OpenCodeConfigPath returns the VM path where the merged opencode config is
// provisioned.
func OpenCodeConfigPath(home string) string {
	return filepath.Join(home, ".config", "opencode", "opencode.json")
}

// ConfigFiles holds the merged agent config, the set of home files to
// provision into the VM, and the default drop-in copy from the host.
type ConfigFiles struct {
	HasSnippets bool                  // whether any agent snippet existed
	OpenCode    []byte                // merged agent config content
	HomeFiles   map[string][]byte     // VM absolute path -> content (home.yaml)
	Provisioned map[string][]byte     // VM absolute path -> content (drop-in copy)
	Hooks       []homeconfig.HookSpec // startup hooks to run at setUpSandbox
	Keys        []string              // sorted VM paths for comparison
}

// LoadConfigFiles builds the desired VM state for the given agent using the
// real host home. userConfigDir is accepted for call compatibility with older
// opencode-specific callers; the agent determines its own config directories.
func LoadConfigFiles(a agent.Agent, _ string, ui termio.UI) (*ConfigFiles, error) {
	hostHome, _ := os.UserHomeDir()
	return LoadConfigFilesForHost(a, hostHome, VMHomeDir, ui)
}

// LoadConfigFilesForHost builds the desired VM state for the given agent with
// explicit host and VM home directories: the merged agent config, the home
// files (from the home.yaml manifests), and the default drop-in copy of the
// agent's host config (per its provision rules). It warns about any home.yaml
// source that does not exist on the host and about malformed provision rules.
// Home files and the merged config override provisioned defaults for the same
// VM path.
func LoadConfigFilesForHost(a agent.Agent, hostHome, vmHome string, ui termio.UI) (*ConfigFiles, error) {
	mergedPath, opencodeJSON, hasSnippets, err := buildMergedConfig(a, vmHome)
	if err != nil {
		return nil, err
	}
	userConfigDir := filepath.Dir(cp.Get().UserAgentConfigDir(a))
	homeFiles, missing, _, err := homeconfig.BuildHomeFiles(
		userConfigDir, // user home.yaml lives one level above the agent subdir
		cp.Get().ProjectConfigDir(),
		vmHome,
	)
	if err != nil {
		return nil, fmt.Errorf("build home files: %w", err)
	}
	for _, src := range missing {
		ui.Warnf("home.yaml source %q does not exist on the host; skipping", src)
	}
	hooks, err := homeconfig.BuildHooks(
		userConfigDir, // user home.yaml lives one level above the agent subdir
		cp.Get().ProjectConfigDir(),
		vmHome,
	)
	if err != nil {
		return nil, fmt.Errorf("build hooks: %w", err)
	}
	provisioned := make(map[string][]byte)
	if p, ok := agent.AsProvisioner(a); ok {
		for _, w := range agent.ValidateProvisionRules(p.ProvisionRules()) {
			ui.Warnf("provision rule: %s", w)
		}
		onCopy := func(dst string, data []byte) error {
			provisioned[dst] = data
			return nil
		}
		if _, err := agent.EvalProvisionRules(p.ProvisionRules(), hostHome, vmHome, onCopy); err != nil {
			return nil, fmt.Errorf("eval provision rules: %w", err)
		}
	}
	// Precedence: home files always override provisioned defaults, and the
	// merged agent config overrides the provisioned config at the same path when
	// snippets exist (no merged config means the drop-in default is provisioned).
	for p := range homeFiles {
		delete(provisioned, p)
	}
	if hasSnippets {
		delete(provisioned, mergedPath)
	}
	keys := make([]string, 0, len(homeFiles)+len(provisioned)+1)
	if hasSnippets {
		keys = append(keys, mergedPath)
	}
	for p := range homeFiles {
		keys = append(keys, p)
	}
	for p := range provisioned {
		keys = append(keys, p)
	}
	sort.Strings(keys)
	return &ConfigFiles{
		HasSnippets: hasSnippets,
		OpenCode:    opencodeJSON,
		HomeFiles:   homeFiles,
		Provisioned: provisioned,
		Hooks:       hooks,
		Keys:        keys,
	}, nil
}

// buildMergedConfig merges the agent's snippet files into a single config and
// returns its destination VM path, content, whether any snippet existed, and an
// error. Agents that implement ConfigMerger use their own snippet pattern and
// VM config path; other agents fall back to the opencode snippet behavior.
func buildMergedConfig(a agent.Agent, vmHome string) (string, []byte, bool, error) {
	if cm, ok := agent.AsConfigMerger(a); ok {
		merged, _, hasSnippets, err := config.BuildMerged(
			cm.SnippetPattern(),
			cp.Get().UserAgentConfigDir(a),
			cp.Get().ProjectAgentConfigDir(a),
		)
		if err != nil {
			return "", nil, false, fmt.Errorf("merge agent config: %w", err)
		}
		return cm.VMConfigPath(vmHome), merged, hasSnippets, nil
	}
	merged, _, hasSnippets, err := config.BuildOpenCodeJSON(
		cp.Get().UserOpencodeConfigDir(),
		cp.Get().ProjectOpencodeConfigDir(),
	)
	if err != nil {
		return "", nil, false, fmt.Errorf("merge opencode config: %w", err)
	}
	return OpenCodeConfigPath(vmHome), merged, hasSnippets, nil
}

// tmpMountPath is the mount point used for the sandbox tmpfs.
const tmpMountPath = "/tmp"

// workspaceMountPath is the mount point used for the host bind mount.
const workspaceMountPath = "/workspace"

// ReadVMConfig reads the given absolute VM paths, returning a map of path to
// content for files that exist.
func ReadVMConfig(ctx context.Context, sb msb.Sandbox, paths []string, ui termio.UI) map[string][]byte {
	result := make(map[string][]byte)
	for _, p := range paths {
		data, err := sb.FS().Read(ctx, p)
		if err != nil {
			ui.Verbosef("read %s failed: %v", p, err)
			continue
		}
		result[p] = data
		ui.Verbosef("OK: %s (%d bytes)", p, len(data))
	}
	return result
}

// OpenCodeConfigEqual reports whether the merged opencode config matches the
// VM state. Home files are intentionally ignored: they are provisioned on every
// startup and do not require a daemon restart to take effect.
func OpenCodeConfigEqual(cf *ConfigFiles, vmData map[string][]byte) bool {
	if !cf.HasSnippets {
		return true
	}
	vm, ok := vmData[OpenCodeConfigPath(VMHomeDir)]
	if !ok {
		return false
	}
	return jsonEqual(cf.OpenCode, vm)
}

func jsonEqual(a, b []byte) bool {
	va, err1 := parseJSON(a)
	vb, err2 := parseJSON(b)
	if err1 != nil || err2 != nil {
		return false
	}
	return reflect.DeepEqual(va, vb)
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
// Entries are sorted by EnvVar and hashed as "ENVVAR=VALUE|HOSTS|PATTERNS|
// PLACEHOLDER|TLS|VIOLATION" lines. Every field that microsandbox bakes into
// the VM at creation is included so a change to any of them (not just the
// value) triggers the VM recreate that applies it.
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
		e := byEnv[k]
		tls := ""
		if e.RequireTLS != nil {
			tls = strconv.FormatBool(*e.RequireTLS)
		}
		fmt.Fprintf(&b, "%s=%s|%s|%s|%s|%s|%s\n",
			k,
			e.Value,
			strings.Join(e.AllowHosts, ","),
			strings.Join(e.AllowHostPatterns, ","),
			e.Placeholder,
			tls,
			string(e.OnViolation),
		)
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

// NetworkChanged reports whether the applied network state differs from the
// desired network policy, comparing content fingerprints. A zero-value applied
// state indicates "no persisted state yet" (first run or a VM created before
// network policies existed); it is only considered "changed" when the desired
// policy is non-empty.
func NetworkChanged(applied state.NetworkState, desired network.Policy) bool {
	if applied.Hash == "" {
		return !desired.Empty()
	}
	return applied.Hash != desired.Fingerprint()
}

// BuildNetworkState computes the fingerprint for the desired network policy.
func BuildNetworkState(desired network.Policy) state.NetworkState {
	return state.NetworkState{
		Hash:  desired.Fingerprint(),
		Names: []string{string(desired.Profile)},
	}
}
