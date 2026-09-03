package agent

import (
	"encoding/json"
	"fmt"
	"path/filepath"
)

// opencodeConfig carries the config-dir behavior shared by the opencode and
// opencode2 profiles: v2 reads the same ~/.config/opencode files as v1, so both
// agents share the snippet pattern, merged config path, config file names, and
// provisioning rules.
type opencodeConfig struct{}

func (opencodeConfig) ConfigDirName() string { return "opencode" }

func (opencodeConfig) SnippetPattern() string { return "opencode-*.json*" }

func (opencodeConfig) VMConfigPath(home string) string {
	return filepath.Join(home, ".config", "opencode", "opencode.jsonc")
}

// opencodeConfigFileNames are the config files opencode reads from its global
// config directory (config.json < opencode.json < opencode.jsonc), plus the
// opencode.* variants it may gain support for. The merged config is written to
// the last-loaded filename so it wins over the others.
func (opencodeConfig) ConfigFileNames() []string {
	return []string{"config.json", "opencode.json", "opencode.jsonc", "opencode.json5", "opencode.yaml", "opencode.yml"}
}

func (opencodeConfig) ProvisionRules() []ProvisionRule {
	return []ProvisionRule{
		{Dir: ".config/opencode", Patterns: []string{"**", "!node_modules/", "!package*.json", "!.gitignore"}},
		{Dir: ".local/share/opencode", Patterns: []string{"auth.json"}},
	}
}

// parseDaemonHealth decodes the shared {"healthy": bool} health response used
// by the opencode and opencode2 daemon agents.
func parseDaemonHealth(stdout string) (bool, error) {
	var resp struct {
		Healthy bool `json:"healthy"`
	}
	if err := json.Unmarshal([]byte(stdout), &resp); err != nil {
		return false, fmt.Errorf("parse health response: %w", err)
	}
	return resp.Healthy, nil
}

// parseWorktreeDirectory decodes the shared {"directory": string} worktree
// response used by the opencode and opencode2 daemon agents.
func parseWorktreeDirectory(stdout string) (string, bool) {
	var resp struct {
		Directory string `json:"directory"`
	}
	if err := json.Unmarshal([]byte(stdout), &resp); err != nil {
		return "", false
	}
	return resp.Directory, resp.Directory != ""
}
