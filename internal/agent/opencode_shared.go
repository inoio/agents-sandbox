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
		{Dir: ".local/share/opencode", Patterns: []string{authFileName}},
	}
}

// MigrationSpec describes the OpenCode files that can be migrated without
// copying native credentials into the VM. Both OpenCode profiles use the same
// auth format and paths.
//
//nolint:gosec // these are public provider hostnames, not credentials
func (opencodeConfig) MigrationSpec() ConfigMigrationSpec {
	const wellKnownAuthType = "wellknown"
	const tokenField = authTokenField
	return ConfigMigrationSpec{
		NativeConfigDir:       ".config/opencode",
		NativeConfigEnv:       "XDG_CONFIG_HOME",
		NativeConfigEnvIsPath: false,
		NativeConfigSubdir:    opencodeName,
		NativeCredential:      ".local/share/opencode/auth.json",
		NativeDataEnv:         "XDG_DATA_HOME",
		NativeDataEnvIsPath:   false,
		NativeDataSubdir:      opencodeName,
		CredentialTarget:      ".local/share/opencode/auth.json",
		ManagedCredential:     authFileName,
		ManagedSnippet:        "opencode-migrated.jsonc",
		NativeConfigFiles: []string{
			"config.json",
			"opencode.json",
			"opencode.jsonc",
			"opencode.json5",
			"opencode.yaml",
			"opencode.yml",
		},
		NativeConfigStrictJSON:    false,
		RequiredNetworkHosts:      nil,
		NativeSupplementalFiles:   nil,
		ManagedSupplementalFiles:  nil,
		ProvisioningExcludedFiles: nil,
		KnownProviderHosts: map[string]string{
			authAnthropicProvider: anthropicAPIHost,
			"github-copilot":      "api.githubcopilot.com",
			"openai":              "api.openai.com",
			"openrouter":          "openrouter.ai",
		},
		Auth: AuthMigrationSpec{
			SecretPrefix: "OPENCODE",
			AuthFields: map[string][]string{
				"oauth":           {authAccessField, authRefreshField},
				"api":             {authKeyField},
				wellKnownAuthType: {tokenField},
			},
			SensitiveFields: []string{
				authAccessField,
				authAccessKeyField,
				authAccessTokenField,
				authAPIKeyType,
				authAPIKeyField,
				authAPITokenField,
				authAuthorizationField,
				authBearerTokenField,
				authClientSecretField,
				authCookieField,
				authCredentialField,
				authCredentialsField,
				authIDTokenField,
				authKeyField,
				authPasswordField,
				authPrivateKeyField,
				authPrivateTokenField,
				authRefreshField,
				authRefreshTokenField,
				authSecretField,
				authSessionField,
				authSessionTokenField,
				authTokenField,
				authXAPIKeyField,
			},
			SafeFields: []string{
				"accountid", "enterpriseurl", "expires", "scopes", authTypeField,
			},
			ConfigSensitiveFields: []string{
				authAccessKeyField,
				authAccessTokenField,
				authAPIKeyField,
				authAPITokenField,
				authAuthorizationField,
				authBearerTokenField,
				authClientSecretField,
				authCredentialField,
				authCredentialsField,
				authIDTokenField,
				authOAuthAccessField,
				authOAuthRefreshField,
				authPasswordField,
				authPrivateKeyField,
				authPrivateTokenField,
				authRefreshTokenField,
				authSecretField,
				authSecretAccessKeyField,
				authSessionField,
				authSessionTokenField,
				authTokenField,
				authXAPIKeyField,
			},
			EndpointFields:          []string{authBaseURLField, authEndpointField, "enterpriseUrl", authURLField},
			AuthPlaceholderPrefix:   migrationPlaceholderPrefix,
			ConfigPlaceholderPrefix: "{env:",
			ConfigPlaceholderSuffix: "}",
			RejectUnresolvedValues:  false,
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
