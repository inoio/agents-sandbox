package sandbox

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"gitlab.inoio.de/inoio/opencode-msb/internal/config"
	"gitlab.inoio.de/inoio/opencode-msb/internal/termio"
)

// configFiles holds the merged configuration and parsed structures for comparison.
type configFiles struct {
	files  map[string][]byte
	parsed map[string]map[string]any
	keys   []string // sorted file names for VM comparison
}

const autoFlag = "--auto"

// promptConfigChange asks the user whether to restart the daemon after a
// provider config change.
func promptConfigChange(ui termio.UI) (string, error) {
	selection, err := ui.Select(
		"opencode provider config has changed. Restart the daemon to apply the new config?",
		[]termio.Choice{
			{
				Label: "Proceed without changes (keep current config)", Key: "p",
				Description: "Daemon continues with the existing config",
			},
			{
				Label: "Restart opencode serve (apply new config)", Key: "r",
				Description: "Daemon restarts with new config; active clients disconnect",
			},
		},
		"p",
	)
	if err != nil {
		return "", fmt.Errorf("prompt config change: %w", err)
	}
	return selection, nil
}

// daemonIsHealthy returns true when the opencode serve daemon reports healthy.
func daemonIsHealthy(ctx context.Context, sb Sandbox) bool {
	out, err := sb.Shell(ctx, "curl -sf "+daemonHealthURL)
	if err != nil || out == nil || !out.Success() {
		return false
	}
	h, _ := parseHealthResponse(out.Stdout())
	return h
}

// loadConfigFiles builds the merged opencode configuration from the user's
// config directory, any project-specific config in .opencode-msb/opencode,
// and the embedded provider config. Returns the marshaled files, parsed
// structures, and sorted file keys.
func loadConfigFiles(userConfigDir string) (*configFiles, error) {
	providerCfg, err := config.LoadProviderConfig(config.EmbeddedProviderConfig)
	if err != nil {
		return nil, fmt.Errorf("load provider config: %w", err)
	}
	projectConfigDir := ""
	if _, statErr := os.Stat(ProjectOpencodeConfigDir()); statErr == nil {
		projectConfigDir = ProjectOpencodeConfigDir()
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
//nolint:unparam // general utility
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

// envOrSecretsChanged reports whether desired env/secrets differ from the VM's
// current config, using the same diff rules as reconcileEnvAndSecrets.
func envOrSecretsChanged( //nolint:unused // called by runner at production call-site
	cfg *msbSdk.SandboxConfig,
	desiredEnv map[string]string,
	desiredSecrets []msbSdk.SecretEntry,
) (bool, bool) {
	var envChanged, secretsChanged bool
	if cfg == nil {
		return desiredEnv != nil || len(desiredSecrets) > 0, len(desiredSecrets) > 0
	}
	if !envMapsEqual(cfg.Env, desiredEnv) {
		envChanged = true
	}
	if !secretsNameSetEqual(cfg.Secrets, parseSecretEntries(desiredSecrets)) {
		secretsChanged = true
	}
	return envChanged, secretsChanged
}
