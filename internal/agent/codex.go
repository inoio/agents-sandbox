package agent

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/inoio/agents-sandbox/internal/sandbox/mounts"
)

// codexName is the canonical registry name of the Codex CLI agent.
const codexName = "codex"

// codexConfigFileName is the Codex CLI's native config file under ~/.codex.
const codexConfigFileName = "config.toml"

// codexSystemConfigLines are the launcher's codex defaults, baked into the
// system config /etc/codex/config.toml, codex's lowest config layer: the user's
// config.toml and a forwarded flag such as --sandbox still override them. The
// update check is off because the sandbox pins the baked version and offers
// upgrades itself; codex's own command sandbox is off because the VM is the
// isolation boundary; the workspace is trusted because the user chose it when
// launching the sandbox.
var codexSystemConfigLines = []string{
	"check_for_update_on_startup = false",
	`sandbox_mode = "danger-full-access"`,
	"",
	`[projects."` + mounts.WorkspaceMountPath + `"]`,
	`trust_level = "trusted"`,
}

// codexWriteSystemConfig writes codexSystemConfigLines in the runner stage, the
// only stage whose files reach the final image. Settings passed as -c instead
// would force codex into its embedded mode, which it warns about on every start.
var codexWriteSystemConfig = "mkdir -p /etc/codex && printf '%s\\n' " +
	strings.Join(shellQuoteArgs(codexSystemConfigLines), " ") +
	" > /etc/codex/config.toml"

type codexProfile struct{}

func (codexProfile) Name() string          { return codexName }
func (codexProfile) ConfigDirName() string { return "codex" }
func (codexProfile) Experimental() bool    { return true }

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
		RequiresNode:  false,
		OwnsToolTree:  false,
		RuntimeLibs:   nil,
		RunnerCommand: codexWriteSystemConfig,
	}
}

func (codexProfile) LatestVersion(ctx context.Context) (string, error) {
	return latestCodexVersion(ctx)
}
func (codexProfile) NewerThan(a, b string) (bool, error) { return newerVersionThan(a, b) }

// SnippetPattern matches TOML snippets, which merge into config.toml as TOML.
func (codexProfile) SnippetPattern() string { return configTOMLSnippetPattern }

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

// ReadBackPaths declares codex's login file only: config.toml is covered by the
// config merger and carries no credential.
func (codexProfile) ReadBackPaths() []string { return []string{".codex/auth.json"} }

func (codexProfile) CredentialProbes() []string { return nil }

// AttachCommand runs codex without its shared background server: by default
// codex installs and starts its own daemon binary under ~/.codex, beside the
// version the sandbox pins. The launcher's defaults come from the baked system
// config, so the command carries no -c override.
// Every forwarded argument is shell-quoted, because the command runs through
// `bash -c`.
func (codexProfile) AttachCommand(_ string, args []string) string {
	parts := []string{"codex", "--no-daemon"}
	parts = append(parts, shellQuoteArgs(args)...)
	return strings.Join(parts, " ")
}

func (codexProfile) VersionCmd() string { return "codex --version" }
func (codexProfile) ParseVersion(stdout string) (string, error) {
	return extractSemverFromOutput(stdout)
}

// InstructionPaths declares the credential-free files the host seed copies: the
// global AGENTS.md (and its override) and the skill tree, the instruction files
// codex reads from CODEX_HOME.
func (codexProfile) InstructionPaths() []string {
	return []string{agentsRulesFileName, "AGENTS.override.md", skillsDirName}
}
