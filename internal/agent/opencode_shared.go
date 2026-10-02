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

// opencodeAutoupdateDisabled is the AgentEnv value both opencode profiles set on
// OPENCODE_DISABLE_AUTOUPDATE to disable the agent's own auto-update checks
// inside the sandbox.
const opencodeAutoupdateDisabled = "true"

func (opencodeConfig) ConfigDirName() string { return opencodeName }

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

// MigrationSpec describes the OpenCode files that can be migrated without
// copying native credentials into the VM. Both OpenCode profiles use the same
// auth format and paths.
//
//nolint:gosec // these are public provider hostnames, not credentials
func (opencodeConfig) MigrationSpec() ConfigMigrationSpec {
	const wellKnownAuthType = "wellknown"
	const tokenField = "token"
	return ConfigMigrationSpec{
		NativeConfigDir:    ".config/opencode",
		NativeConfigEnv:    "XDG_CONFIG_HOME",
		NativeConfigSubdir: opencodeName,
		NativeCredential:   ".local/share/opencode/auth.json",
		NativeDataEnv:      "XDG_DATA_HOME",
		NativeDataSubdir:   opencodeName,
		CredentialTarget:   ".local/share/opencode/auth.json",
		ManagedCredential:  "auth.json",
		ManagedSnippet:     "opencode-migrated.jsonc",
		NativeConfigFiles: []string{
			"config.json",
			"opencode.json",
			"opencode.jsonc",
			"opencode.json5",
			"opencode.yaml",
			"opencode.yml",
		},
		KnownProviderHosts: map[string]string{
			"anthropic":      "api.anthropic.com",
			"github-copilot": "api.githubcopilot.com",
			"openai":         "api.openai.com",
			"openrouter":     "openrouter.ai",
		},
		Auth: AuthMigrationSpec{
			SecretPrefix: "OPENCODE",
			AuthFields: map[string][]string{
				"oauth":           {"access", "refresh"},
				"api":             {"key"},
				wellKnownAuthType: {tokenField},
			},
			SensitiveFields: []string{
				"access", "accesskey", "accesstoken", "api_key", "apikey", "apitoken", "authorization",
				"bearertoken", "clientsecret", "cookie", "credential", "credentials", "idtoken", "key",
				"password", "privatekey", "privatetoken", "refresh", "refreshtoken", "secret", "session",
				"sessiontoken", "token", "xapikey",
			},
			SafeFields: []string{
				"accountid", "enterpriseurl", "expires", "scopes", "type",
			},
			ConfigSensitiveFields: []string{
				"accesskey", "accesstoken", "apikey", "apitoken", "authorization", "bearertoken", "clientsecret",
				"credential", "credentials", "idtoken", "oauthaccess", "oauthrefresh", "password", "privatekey",
				"privatetoken", "refreshtoken", "secret", "secretaccesskey", "session", "sessiontoken", "token",
				"xapikey",
			},
			EndpointFields:          []string{"baseURL", "endpoint", "enterpriseUrl", "url"},
			AuthPlaceholderPrefix:   "$MSB_",
			ConfigPlaceholderPrefix: "{env:",
			ConfigPlaceholderSuffix: "}",
		},
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
