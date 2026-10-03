package agent

import (
	"context"
	"path/filepath"
	"strings"
)

//nolint:gochecknoinits // built-in agent self-registration
func init() { Register(piProfile{}) }

// piName is the canonical registry name of the pi agent.
const piName = "pi"

type piProfile struct{}

func (piProfile) Name() string          { return piName }
func (piProfile) ConfigDirName() string { return "pi" }

// MigrationSpec describes Pi's user settings and credential files. Pi resolves
// $NAME references in auth.json from the process environment, so migrated
// credentials can use the microsandbox secret mechanism.
//
//nolint:gosec // these are public provider hostnames, not credentials
func (piProfile) MigrationSpec() ConfigMigrationSpec { //nolint:funlen // migration metadata is the agent-specific contract
	return ConfigMigrationSpec{
		NativeConfigDir:           ".pi/agent",
		NativeConfigEnv:           "PI_CODING_AGENT_DIR",
		NativeConfigEnvIsPath:     true,
		NativeConfigSubdir:        "agent",
		NativeCredential:          ".pi/agent/auth.json",
		NativeDataEnv:             "PI_CODING_AGENT_DIR",
		NativeDataEnvIsPath:       true,
		NativeDataSubdir:          "agent",
		CredentialTarget:          ".pi/agent/auth.json",
		ManagedCredential:         authFileName,
		ManagedSnippet:            "settings-migrated.json",
		NativeConfigFiles:         []string{"settings.json"},
		NativeConfigStrictJSON:    false,
		RequiredNetworkHosts:      nil,
		NativeSupplementalFiles:   []string{modelsFileName},
		ManagedSupplementalFiles:  []string{modelsFileName},
		ProvisioningExcludedFiles: []string{authFileName, modelsFileName},
		KnownProviderHosts: map[string]string{
			authAnthropicProvider: anthropicAPIHost,
			"cerebras":            "api.cerebras.ai",
			"deepseek":            "api.deepseek.com",
			"fireworks":           "api.fireworks.ai",
			"github-copilot":      "api.githubcopilot.com",
			"google":              "generativelanguage.googleapis.com",
			"google-vertex":       "aiplatform.googleapis.com",
			"groq":                "api.groq.com",
			"huggingface":         "router.huggingface.co",
			"mistral":             "api.mistral.ai",
			"openai":              "api.openai.com",
			"openrouter":          "openrouter.ai",
			"together":            "api.together.xyz",
			"xai":                 "api.x.ai",
			"zai":                 "api.z.ai",
			"zai-coding-cn":       "open.bigmodel.cn",
			"opencode":            "opencode.ai",
			"opencode-go":         "opencode.ai",
		},
		Auth: AuthMigrationSpec{
			SecretPrefix: "PI",
			AuthFields: map[string][]string{
				authAPIKeyType: {authKeyField},
				"oauth":        {authAccessField, authRefreshField},
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
				"accountid", "clientid", "expires", "ratelimittier", "scopes", "subscriptiontype", authTypeField,
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
			EndpointFields:          []string{authBaseURLField, authBaseURLLowerField, authEndpointField, authURLField},
			AuthPlaceholderPrefix:   migrationPlaceholderPrefix,
			ConfigPlaceholderPrefix: migrationPlaceholderPrefix,
			ConfigPlaceholderSuffix: "",
			RejectUnresolvedValues:  true,
		},
	}
}

func (piProfile) ImageSpec() ImageSpec {
	return ImageSpec{
		VersionArg: versionArgFor(piName),
		// pi checks pi.dev for updates at startup; the sandbox pins the baked
		// version and resolves upgrades itself, so disable the in-agent check.
		AgentEnv: map[string]string{"PI_SKIP_VERSION_CHECK": "1"},
		// --ignore-scripts avoids running the package's postinstall (which may
		// phone home); the version is pinned so the baked release is exact.
		InstallCommand: "npm install -g --ignore-scripts @earendil-works/pi-coding-agent@$PI_VERSION",
	}
}

func (piProfile) LatestVersion(ctx context.Context) (string, error) {
	return latestPIVersion(ctx)
}
func (piProfile) NewerThan(a, b string) (bool, error) { return newerVersionThan(a, b) }

func (piProfile) SnippetPattern() string { return "settings*.json*" }
func (piProfile) VMConfigPath(home string) string {
	return filepath.Join(home, ".pi", "agent", settingsFileName)
}
func (piProfile) ConfigFileNames() []string { return []string{settingsFileName} }

func (piProfile) ProvisionRules() []ProvisionRule {
	return []ProvisionRule{
		{Dir: ".pi/agent", Patterns: []string{"**", "!" + authFileName, "!models.json"}},
	}
}

func (piProfile) AttachCommand(_ string, args []string) string {
	parts := []string{"pi"}
	parts = append(parts, args...)
	return strings.Join(parts, " ")
}

func (piProfile) VersionCmd() string { return "pi --version" }
func (piProfile) ParseVersion(stdout string) (string, error) {
	return extractSemverFromOutput(stdout)
}
