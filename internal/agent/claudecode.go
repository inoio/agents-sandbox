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

func (claudeCodeProfile) ImageSpec() ImageSpec {
	return ImageSpec{
		VersionArg:   versionArgFor(claudeCodeName),
		VersionLabel: versionLabelFor(claudeCodeName),
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
