package agent

import (
	"context"
	"path/filepath"
	"strings"
)

// piName is the canonical registry name of the pi agent.
const piName = "pi"

// piAgentDir is pi's home-relative agent dir, which holds its user config,
// logins, and runtime state.
const piAgentDir = ".pi/agent"

type piProfile struct{}

func (piProfile) Name() string          { return piName }
func (piProfile) ConfigDirName() string { return "pi" }
func (piProfile) Experimental() bool    { return true }

func (piProfile) ImageSpec() ImageSpec {
	return ImageSpec{
		VersionArg: versionArgFor(piName),
		// pi checks pi.dev for updates at startup; the sandbox pins the baked
		// version and resolves upgrades itself, so disable the in-agent check.
		AgentEnv: map[string]string{"PI_SKIP_VERSION_CHECK": "1"},
		// --ignore-scripts avoids running the package's postinstall (which may
		// phone home); the version is pinned so the baked release is exact.
		InstallCommand: "npm install -g --ignore-scripts --prefix /opt/agents-sandbox @earendil-works/pi-coding-agent@$PI_VERSION",
		// The published package is a #!/usr/bin/env node bundle, so pi needs
		// node at install and runtime; it is the only agent that does.
		RequiresNode: true,
		OwnsToolTree: false,
		RuntimeLibs:  nil,
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

// ProvisionRules copies the agent dir's user-authored files that carry no
// credential: settings, keybindings, instructions, system prompts, and the
// extension, skill, prompt, and theme trees. auth.json holds logins and saved
// keys, models.json and mcp.json can hold literal keys and headers, and
// mcp-auth.json holds MCP tokens; keys reach the VM only as msb secrets, and
// the rest of the agent dir is runtime state.
func (piProfile) ProvisionRules() []ProvisionRule {
	return []ProvisionRule{
		{Dir: piAgentDir, Patterns: []string{
			"/" + settingsFileName,
			"/keybindings.json",
			"/AGENTS.override.md", "/AGENTS.md", "/AGENTS.MD", "/CLAUDE.md", "/CLAUDE.MD",
			"/SYSTEM.md", "/APPEND_SYSTEM.md",
			"/extensions/", "/skills/", "/prompts/", "/themes/",
		}},
	}
}

func (piProfile) AttachCommand(_ string, args []string) string {
	parts := []string{"pi"}
	parts = append(parts, shellQuoteArgs(args)...)
	return strings.Join(parts, " ")
}

func (piProfile) VersionCmd() string { return "pi --version" }
func (piProfile) ParseVersion(stdout string) (string, error) {
	return extractSemverFromOutput(stdout)
}
