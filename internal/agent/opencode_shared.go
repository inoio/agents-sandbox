package agent

import (
	"encoding/json"
	"fmt"
	"path/filepath"
)

// opencodeCmdName is the opencode (v1) shell command the launcher invokes in
// the VM (serve, attach, --version).
const opencodeCmdName = "opencode"

// opencodeConfigDirName is the opencode (v1) launcher config directory name,
// which opencode2 also falls back to as its legacy alias.
const opencodeConfigDirName = "opencode"

// opencodeConfig carries opencode (v1)'s config behavior: the snippet pattern,
// merged config path, config file names, and provisioning rules. opencode2
// embeds it for those guest-side concerns (it still reads v1's ~/.config/opencode
// files and auth.json) while overriding ConfigDirName for its own launcher
// config namespace.
type opencodeConfig struct{}

// opencodeAutoupdateDisabled is the AgentEnv value both opencode profiles set on
// OPENCODE_DISABLE_AUTOUPDATE to disable the agent's own auto-update checks
// inside the sandbox.
const opencodeAutoupdateDisabled = "true"

// daemonGuestTools are the in-VM tools the launcher uses to reach and control
// an opencode daemon: curl for the health probe, session and worktree calls,
// and the notify event stream. The daemon is launched and stopped as a
// runtime-owned job, so no guest process control tool is needed. Both daemon
// profiles (opencode, opencode2) declare them, and the renderer checks them at
// image build, so a custom base missing one fails the build naming the tool
// instead of failing at runtime.
var daemonGuestTools = []string{"curl"}

func (opencodeConfig) ConfigDirName() string { return opencodeConfigDirName }

func (opencodeConfig) SnippetPattern() string { return "opencode*.json*" }

func (opencodeConfig) VMConfigPath(home string) string {
	return filepath.Join(home, ".config", "opencode", "opencode.jsonc")
}

// ConfigFileNames opencodeConfigFileNames are the config files opencode reads from its global
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

// opencodeReadBackPaths are the home-relative config and credential paths both
// opencode profiles read back: the shared global config dir and the auth file.
var opencodeReadBackPaths = []string{".config/opencode/*", ".local/share/opencode/auth.json"}

func (opencodeConfig) ReadBackPaths() []string { return opencodeReadBackPaths }

// CredentialProbes is empty for v1: its credential is file-based. opencode2
// overrides it with its SQLite probe.
func (opencodeConfig) CredentialProbes() []string { return nil }

// parseDaemonHealth decodes opencode (v1)'s {"healthy": bool} health response.
// opencode2 (v2) uses its own ServerInfo parser.
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
