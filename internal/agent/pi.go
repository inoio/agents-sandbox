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

// piLoginFileName is pi's login store under its agent dir: saved API keys and
// OAuth credentials.
const piLoginFileName = "auth.json"

type piProfile struct{}

func (piProfile) Name() string          { return piName }
func (piProfile) ConfigDirName() string { return "pi" }
func (piProfile) Experimental() bool    { return false }

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
		RequiresNode:  true,
		OwnsToolTree:  false,
		RuntimeLibs:   nil,
		RunnerCommand: "",
	}
}

func (piProfile) LatestVersion(ctx context.Context) (string, error) {
	return latestPIVersion(ctx)
}
func (piProfile) NewerThan(a, b string) (bool, error) { return newerVersionThan(a, b) }

func (piProfile) SnippetPattern() string { return settingsSnippetPattern }
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
			"/extensions/", skillsDirPattern, promptsDirPattern, "/themes/",
		}},
	}
}

// ReadBackPaths declares pi's settings and its login store. models.json and
// mcp.json are withheld: a provider's or MCP server's headers can carry a token
// the reader does not classify yet, which would reach the managed-config
// write-back verbatim.
func (piProfile) ReadBackPaths() []string {
	return []string{
		piAgentDir + "/" + settingsFileName,
		piAgentDir + "/" + piLoginFileName,
	}
}

func (piProfile) CredentialProbes() []string { return nil }

func (piProfile) AttachCommand(_ string, args []string) string {
	parts := []string{"pi"}
	parts = append(parts, shellQuoteArgs(args)...)
	return strings.Join(parts, " ")
}

func (piProfile) VersionCmd() string { return "pi --version" }
func (piProfile) ParseVersion(stdout string) (string, error) {
	return extractSemverFromOutput(stdout)
}

// InstructionPaths declares the credential-free files the host seed copies:
// the instruction and system-prompt files and the prompt and skill trees. The
// extension tree holds code and is left out.
func (piProfile) InstructionPaths() []string {
	return []string{
		agentsRulesFileName, "AGENTS.MD", "AGENTS.override.md", claudeRulesFileName, "CLAUDE.MD",
		"SYSTEM.md", "APPEND_SYSTEM.md", promptsDirName, skillsDirName,
	}
}
