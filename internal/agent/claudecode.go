package agent

import (
	"context"
	"path/filepath"
	"strings"
)

//nolint:gochecknoinits // built-in agent self-registration
func init() { Register(claudeCodeProfile{}) }

// claudeCodeName is the canonical registry name of the claude-code agent.
const claudeCodeName = "claude-code"

type claudeCodeProfile struct{}

func (claudeCodeProfile) Name() string          { return claudeCodeName }
func (claudeCodeProfile) ConfigDirName() string { return "claude" }

// MigrationSpec describes the portable Claude Code settings file. Claude Code
// stores login credentials separately in .credentials.json or the OS keychain;
// those credentials are intentionally not migration inputs.
func (claudeCodeProfile) MigrationSpec() ConfigMigrationSpec {
	return ConfigMigrationSpec{
		NativeConfigDir:           ".claude",
		NativeConfigEnv:           "CLAUDE_CONFIG_DIR",
		NativeConfigEnvIsPath:     true,
		NativeConfigSubdir:        "",
		NativeCredential:          "",
		NativeDataEnv:             "",
		NativeDataEnvIsPath:       false,
		NativeDataSubdir:          "",
		CredentialTarget:          "",
		ManagedCredential:         "",
		ManagedSnippet:            "settings-migrated.json",
		NativeConfigFiles:         []string{"settings.json"},
		NativeConfigStrictJSON:    true,
		RequiredNetworkHosts:      []string{anthropicAPIHost, "platform.claude.com", "claude.ai", "claude.com"},
		NativeSupplementalFiles:   nil,
		ManagedSupplementalFiles:  nil,
		ProvisioningExcludedFiles: nil,
		KnownProviderHosts: map[string]string{
			authAnthropicProvider: anthropicAPIHost,
		},
		Auth: AuthMigrationSpec{
			SecretPrefix: "CLAUDE",
			AuthFields: map[string][]string{
				authAPIKeyType: {authKeyField},
			},
			SensitiveFields: []string{
				"access",
				"accesskey",
				"accesstoken",
				"api_key",
				"apikey",
				"apitoken",
				"authorization",
				authBearerTokenField,
				authClientSecretField,
				authCookieField,
				authCredentialField,
				authCredentialsField,
				authIDTokenField,
				authKeyField,
				"password",
				"privatekey",
				"privatetoken",
				"refresh",
				"refreshtoken",
				"secret",
				"session",
				"sessiontoken",
				"token",
				"xapikey",
			},
			SafeFields: []string{
				"model", "permissions", "sandbox", "theme", authTypeField,
			},
			ConfigSensitiveFields: []string{
				"accesskey",
				"accesstoken",
				"apikey",
				"apitoken",
				"authorization",
				"bearertoken",
				"clientsecret",
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
				"xapikey",
			},
			EndpointFields:          []string{authBaseURLField, authBaseURLLowerField, authEndpointField, authURLField},
			AuthPlaceholderPrefix:   migrationPlaceholderPrefix,
			ConfigPlaceholderPrefix: migrationPlaceholderPrefix,
			ConfigPlaceholderSuffix: "",
			RejectUnresolvedValues:  true,
		},
	}
}

func (claudeCodeProfile) ImageSpec() ImageSpec {
	return ImageSpec{
		VersionArg: versionArgFor(claudeCodeName),
		// claude-code self-updates at startup; the sandbox pins the baked
		// version and resolves upgrades itself, so disable the in-agent
		// auto-updater.
		AgentEnv:       map[string]string{"DISABLE_AUTOUPDATER": "1"},
		InstallCommand: "npm install -g @anthropic-ai/claude-code@$CLAUDE_CODE_VERSION",
	}
}

func (claudeCodeProfile) LatestVersion(ctx context.Context) (string, error) {
	return latestClaudeCodeVersion(ctx)
}
func (claudeCodeProfile) NewerThan(a, b string) (bool, error) { return newerVersionThan(a, b) }

func (claudeCodeProfile) SnippetPattern() string { return "settings*.json*" }
func (claudeCodeProfile) VMConfigPath(home string) string {
	return filepath.Join(home, ".claude", settingsFileName)
}
func (claudeCodeProfile) ConfigFileNames() []string { return []string{settingsFileName} }

func (claudeCodeProfile) ProvisionRules() []ProvisionRule {
	return []ProvisionRule{
		{Dir: ".claude", Patterns: []string{settingsFileName}},
	}
}

func (claudeCodeProfile) AttachCommand(_ string, args []string) string {
	parts := []string{"claude"}
	parts = append(parts, args...)
	return strings.Join(parts, " ")
}

func (claudeCodeProfile) VersionCmd() string { return "claude --version" }
func (claudeCodeProfile) ParseVersion(stdout string) (string, error) {
	return extractSemverFromOutput(stdout)
}
