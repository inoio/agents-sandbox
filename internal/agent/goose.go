package agent

import (
	"context"
	"path/filepath"
	"strings"
)

// gooseName is the canonical registry name of the goose agent; its binary is
// goose too.
const gooseName = "goose"

// gooseConfigDir is goose's home-relative config dir: settings, permissions,
// context files, prompts, recipes, custom providers, and (without a system
// keyring) its secrets file.
const gooseConfigDir = ".config/goose"

// gooseConfigFileName is goose's config file under its config dir.
const gooseConfigFileName = "config.yaml"

type gooseProfile struct{}

func (gooseProfile) Name() string          { return gooseName }
func (gooseProfile) ConfigDirName() string { return gooseName }
func (gooseProfile) Experimental() bool    { return true }

// ImageSpec installs the static musl release build, which needs no shared
// library. Its asset names use `uname -m` architectures. The release publishes
// no checksums, so, like codex, the asset is fetched over TLS from the pinned
// release tag; the build runs the binary once so a broken download never ships.
// goose checks for updates only on an explicit `goose update`, so no switch is
// needed.
func (gooseProfile) ImageSpec() ImageSpec {
	return ImageSpec{
		VersionArg: versionArgFor(gooseName),
		AgentEnv:   nil,
		InstallCommand: `curl -fsSL "https://github.com/aaif-goose/goose/releases/download/v${GOOSE_VERSION}/goose-$(uname -m)-unknown-linux-musl.tar.gz" | ` +
			`tar -xz -C /opt/agents-sandbox/bin ./goose && ` +
			`/opt/agents-sandbox/bin/goose --version`,
		RequiresNode: false,
		// goose keeps its sessions and logs under ~/.local on the home volume, so
		// the tool tree stays root-owned.
		OwnsToolTree:  false,
		RuntimeLibs:   nil,
		RunnerCommand: "",
	}
}

func (gooseProfile) LatestVersion(ctx context.Context) (string, error) {
	return latestGooseVersion(ctx)
}
func (gooseProfile) NewerThan(a, b string) (bool, error) { return newerVersionThan(a, b) }

// SnippetPattern matches YAML snippets under either extension; they merge into
// config.yaml as YAML.
func (gooseProfile) SnippetPattern() string { return "config*.{yml,yaml}" }

func (gooseProfile) VMConfigPath(home string) string {
	return filepath.Join(home, gooseConfigDir, gooseConfigFileName)
}
func (gooseProfile) ConfigFileNames() []string { return []string{gooseConfigFileName} }

// ProvisionRules copies only goose's credential-free, user-authored files: the
// tool permission levels, the global context files (.goosehints and AGENTS.md),
// and the prompt and recipe trees. config.yaml is not copied, because an
// extension's envs and headers can hold tokens beside the settings; nor are the
// custom provider definitions, which can carry an inline api_key and headers.
// secrets.yaml holds plain-text secrets when there is no system keyring; keys
// reach the VM only as msb secrets, which goose reads from the environment
// before any secret store.
func (gooseProfile) ProvisionRules() []ProvisionRule {
	return []ProvisionRule{
		{Dir: gooseConfigDir, Patterns: []string{
			"/permission.yaml", "/.goosehints", agentsRulesPattern, promptsDirPattern, "/recipes/",
		}},
	}
}

// ReadBackPaths declares nothing yet. Neither config file is classifiable:
// config.yaml can carry tokens in an extension's envs and headers, and
// secrets.yaml is a YAML secret store the reader does not parse, so an inline
// value in either would reach the managed-config write-back verbatim. They are
// declared once the reader classifies those shapes.
func (gooseProfile) ReadBackPaths() []string { return nil }

func (gooseProfile) CredentialProbes() []string { return nil }

// AttachCommand forwards every argument shell-quoted, because the command runs
// through `bash -c`. Without arguments goose starts its default interactive
// session.
func (gooseProfile) AttachCommand(_ string, args []string) string {
	parts := append([]string{"goose"}, shellQuoteArgs(args)...)
	return strings.Join(parts, " ")
}

func (gooseProfile) VersionCmd() string { return "goose --version" }
func (gooseProfile) ParseVersion(stdout string) (string, error) {
	return extractSemverFromOutput(stdout)
}
