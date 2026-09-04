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
		{Dir: ".pi/agent", Patterns: []string{"**"}},
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
