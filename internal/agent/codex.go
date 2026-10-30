package agent

import (
	"context"
	"path/filepath"
	"strings"
)

// codexName is the canonical registry name of the Codex CLI agent.
const codexName = "codex"

// codexConfigFileName is the Codex CLI's native config file under ~/.codex.
const codexConfigFileName = "config.toml"

type codexProfile struct{}

func (codexProfile) Name() string          { return codexName }
func (codexProfile) ConfigDirName() string { return "codex" }

// ImageSpec installs the codex-package release asset, the layout Codex's own
// installer uses: bin/codex plus the helpers it resolves next to its real path
// (bin/codex-code-mode-host runs commands, codex-path/rg, codex-resources/).
// The bare codex binary asset lacks those helpers. The package lands in
// toolDir/codex and the tool bin links to its bin/codex; the release asset
// names use the same architecture names as `uname -m`.
func (codexProfile) ImageSpec() ImageSpec {
	return ImageSpec{
		VersionArg: versionArgFor(codexName),
		AgentEnv:   nil,
		InstallCommand: `mkdir -p /opt/agents-sandbox/codex && ` +
			`curl -fsSL "https://github.com/openai/codex/releases/download/rust-v${CODEX_VERSION}/codex-package-$(uname -m)-unknown-linux-musl.tar.gz" | ` +
			`tar -xz -C /opt/agents-sandbox/codex && ` +
			`ln -s ../codex/bin/codex /opt/agents-sandbox/bin/codex`,
		RequiresNode:   false,
		OwnedToolPaths: nil,
		RuntimeLibs:    nil,
	}
}

func (codexProfile) LatestVersion(ctx context.Context) (string, error) {
	return latestCodexVersion(ctx)
}
func (codexProfile) NewerThan(a, b string) (bool, error) { return newerVersionThan(a, b) }

// SnippetPattern matches TOML snippets, which merge into config.toml as TOML.
func (codexProfile) SnippetPattern() string { return "config*.toml" }
func (codexProfile) VMConfigPath(home string) string {
	return filepath.Join(home, ".codex", codexConfigFileName)
}
func (codexProfile) ConfigFileNames() []string { return []string{codexConfigFileName} }

// ProvisionRules copies only config.toml. auth.json is left out: a ChatGPT
// login holds rotating refresh tokens that a copy would invalidate on the host.
func (codexProfile) ProvisionRules() []ProvisionRule {
	return []ProvisionRule{
		{Dir: ".codex", Patterns: []string{codexConfigFileName}},
	}
}

// AttachCommand disables the startup update check (the sandbox pins the baked
// version and offers upgrades itself) and turns off codex's own command
// sandbox: the VM is the isolation boundary. A forwarded --sandbox flag still
// overrides it.
// Every forwarded argument is shell-quoted, because the command runs through
// `bash -c`.
func (codexProfile) AttachCommand(_ string, args []string) string {
	parts := []string{
		"codex",
		"-c", "check_for_update_on_startup=false",
		"-c", shellQuote(`sandbox_mode="danger-full-access"`),
	}
	parts = append(parts, shellQuoteArgs(args)...)
	return strings.Join(parts, " ")
}

func (codexProfile) VersionCmd() string { return "codex --version" }
func (codexProfile) ParseVersion(stdout string) (string, error) {
	return extractSemverFromOutput(stdout)
}
