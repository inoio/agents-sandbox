package agent

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/inoio/agents-sandbox/internal/sandbox/mounts"
)

// claudeCodeName is the canonical registry name of the claude-code agent.
const claudeCodeName = "claude-code"

type claudeCodeProfile struct{}

func (claudeCodeProfile) Name() string          { return claudeCodeName }
func (claudeCodeProfile) ConfigDirName() string { return "claude" }
func (claudeCodeProfile) Experimental() bool    { return true }

func (claudeCodeProfile) ImageSpec() ImageSpec {
	return ImageSpec{
		VersionArg: versionArgFor(claudeCodeName),
		// claude-code self-updates at startup; the sandbox pins the baked
		// version and resolves upgrades itself, so disable the in-agent
		// auto-updater.
		AgentEnv:       map[string]string{"DISABLE_AUTOUPDATER": "1"},
		InstallCommand: `curl -fsSL https://claude.ai/install.sh | bash -s -- "$CLAUDE_CODE_VERSION" && cp -L /root/.local/bin/claude /opt/agents-sandbox/bin/claude`,
		RequiresNode:   false,
		OwnsToolTree:   false,
		RuntimeLibs:    nil,
		RunnerCommand:  "",
	}
}

func (claudeCodeProfile) LatestVersion(ctx context.Context) (string, error) {
	return latestClaudeCodeVersion(ctx)
}
func (claudeCodeProfile) NewerThan(a, b string) (bool, error) { return newerVersionThan(a, b) }

func (claudeCodeProfile) SnippetPattern() string { return settingsSnippetPattern }
func (claudeCodeProfile) VMConfigPath(home string) string {
	return filepath.Join(home, ".claude", settingsFileName)
}
func (claudeCodeProfile) ConfigFileNames() []string { return []string{settingsFileName} }

func (claudeCodeProfile) ProvisionRules() []ProvisionRule {
	return []ProvisionRule{
		{Dir: ".claude", Patterns: []string{settingsFileName}},
	}
}

// ReadBackPaths declares claude-code's OAuth login store, .credentials.json
// (recorded, never copied). settings.json is withheld: it can hold credentials
// in shapes the read-back does not classify, so it is neither read back nor
// copied into managed config.
func (claudeCodeProfile) ReadBackPaths() []string {
	return []string{".claude/.credentials.json"}
}

func (claudeCodeProfile) CredentialProbes() []string { return nil }

// StateDefaults pre-accepts Claude Code's folder-trust question for the
// workspace in ~/.claude.json, its state file: the user chose the directory on
// the host, and the VM is the isolation boundary.
func (claudeCodeProfile) StateDefaults() []StateDefault {
	return []StateDefault{{
		Path: ".claude.json",
		Keys: map[string]any{"projects": map[string]any{
			mounts.WorkspaceMountPath: map[string]any{"hasTrustDialogAccepted": true},
		}},
	}}
}

func (claudeCodeProfile) AttachCommand(_ string, args []string) string {
	parts := []string{"claude"}
	parts = append(parts, shellQuoteArgs(args)...)
	return strings.Join(parts, " ")
}

func (claudeCodeProfile) VersionCmd() string { return "claude --version" }
func (claudeCodeProfile) ParseVersion(stdout string) (string, error) {
	return extractSemverFromOutput(stdout)
}
