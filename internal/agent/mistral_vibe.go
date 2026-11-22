package agent

import (
	"context"
	"path/filepath"
	"strings"
)

// mistralVibeName is the canonical registry name of Mistral's Vibe CLI. Its
// binary is vibe.
const mistralVibeName = "mistral-vibe"

// mistralVibeDataDir is vibe's home-relative data root (VIBE_HOME): config,
// the API key's .env, user instructions, agents, prompts, skills, and runtime
// state live under it.
const mistralVibeDataDir = ".vibe"

// mistralVibeConfigFileName is vibe's user config file under its data root.
const mistralVibeConfigFileName = "config.toml"

// mistralVibeInstallDir holds the uv-managed Python and the tool environment
// vibe is installed into; the launchers uv links into the bin dir point here.
const mistralVibeInstallDir = "/opt/agents-sandbox/mistral-vibe"

// mistralVibeResolveUV sets uv_arch to uv's release architecture for the build
// machine and uv_sha to the pinned digest of that archive, and fails the build
// on any other architecture. The uv version and digests are the ones Vibe's
// own installer pins; uv only runs at build time and is not shipped.
const mistralVibeResolveUV = `case "$(uname -m)" in ` +
	`x86_64|amd64) uv_arch=x86_64 uv_sha=6426a73c3837e6e2483ee344cbc00f36394d179afcba6183cb77437e67db4af0 ;; ` +
	`aarch64|arm64) uv_arch=aarch64 uv_sha=befa1a59c91e96eb601b0fd9a97c03dd666f17baba644b2b4db9c59a767e387e ;; ` +
	`*) echo "mistral-vibe: unsupported architecture $(uname -m)" >&2; exit 1 ;; ` +
	`esac`

// mistralVibeUVEnvironment places everything uv installs under the tool tree,
// the only path the runner image copies from the agent stage: the managed
// Python (never a system one), the tool environment, and the launchers.
// Bytecode is compiled at build time because the tree is root-owned at run
// time, and uv trusts the system store so a build behind a TLS-intercepting
// proxy works, as Vibe's own installer does.
const mistralVibeUVEnvironment = `UV_TOOL_DIR=` + mistralVibeInstallDir + `/tools ` +
	`UV_TOOL_BIN_DIR=/opt/agents-sandbox/bin ` +
	`UV_PYTHON_INSTALL_DIR=` + mistralVibeInstallDir + `/python ` +
	`UV_PYTHON_PREFERENCE=only-managed ` +
	`UV_CACHE_DIR="$vibe_dir/cache" ` +
	`UV_COMPILE_BYTECODE=1 ` +
	`UV_SYSTEM_CERTS=true`

// mistralVibeLockedConstraints writes $vibe_dir/constraints.txt from the
// uv.lock of the release's tag, so the dependencies resolve to the versions
// Mistral locked for that release instead of whatever PyPI serves at build
// time. A tag without a lock fails the build.
const mistralVibeLockedConstraints = `mkdir -p "$vibe_dir/lock" && ` +
	`vibe_tag="https://raw.githubusercontent.com/mistralai/mistral-vibe/v${MISTRAL_VIBE_VERSION}" && ` +
	`curl -fsSL "$vibe_tag/pyproject.toml" -o "$vibe_dir/lock/pyproject.toml" && ` +
	`curl -fsSL "$vibe_tag/uv.lock" -o "$vibe_dir/lock/uv.lock" && ` +
	`(cd "$vibe_dir/lock" && ` + mistralVibeUVEnvironment + ` "$vibe_dir/uv" export --frozen --no-dev --no-emit-project ` +
	`--no-hashes --quiet -o "$vibe_dir/constraints.txt")`

type mistralVibeProfile struct{}

func (mistralVibeProfile) Name() string          { return mistralVibeName }
func (mistralVibeProfile) ConfigDirName() string { return mistralVibeName }
func (mistralVibeProfile) Experimental() bool    { return true }

// ImageSpec installs the mistral-vibe package from PyPI with a checksum-pinned
// uv into a uv-managed CPython 3.14, the GIL build Vibe's installer selects,
// with its dependencies held to the release's lock. The build runs vibe once
// so a broken install never ships.
func (mistralVibeProfile) ImageSpec() ImageSpec {
	return ImageSpec{
		VersionArg: versionArgFor(mistralVibeName),
		// Vibe reads VIBE_* variables as config, above the user's config.toml.
		// Its TUI gates the update check and prompt on enable_update_checks; the
		// sandbox pins the baked version and offers upgrades itself. The system
		// trust store, which carries the sandbox's TLS-interception CA, replaces
		// the Rust TUI's built-in roots and the Python side's strict certifi
		// context, so intercepted HTTPS (and with it secret substitution) works.
		AgentEnv: map[string]string{
			"VIBE_ENABLE_UPDATE_CHECKS":      "false",
			"VIBE_ENABLE_SYSTEM_TRUST_STORE": "true",
		},
		InstallCommand: mistralVibeResolveUV + ` && ` +
			`vibe_dir=/tmp/mistral-vibe && mkdir -p "$vibe_dir" && ` +
			`curl -fsSL "https://github.com/astral-sh/uv/releases/download/0.11.26/uv-$uv_arch-unknown-linux-gnu.tar.gz" ` +
			`-o "$vibe_dir/uv.tar.gz" && ` +
			`echo "$uv_sha  $vibe_dir/uv.tar.gz" | sha256sum --check --strict --quiet && ` +
			`tar -xzf "$vibe_dir/uv.tar.gz" -C "$vibe_dir" --strip-components=1 && ` +
			mistralVibeUVEnvironment + ` "$vibe_dir/uv" python install cpython-3.14 && ` +
			mistralVibeLockedConstraints + ` && ` +
			mistralVibeUVEnvironment + ` "$vibe_dir/uv" tool install --python 3.14 --constraints "$vibe_dir/constraints.txt" ` +
			`"mistral-vibe==${MISTRAL_VIBE_VERSION}" && ` +
			`rm -rf "$vibe_dir" && ` +
			`/opt/agents-sandbox/bin/vibe --version`,
		RequiresNode: false,
		// vibe keeps its sessions, logs, and caches under ~/.vibe on the home
		// volume, so the tool tree stays root-owned.
		OwnsToolTree: false,
		// The managed CPython and vibe's bundled TUI binary link only glibc.
		RuntimeLibs:   nil,
		RunnerCommand: "",
	}
}

func (mistralVibeProfile) LatestVersion(ctx context.Context) (string, error) {
	return latestMistralVibeVersion(ctx)
}
func (mistralVibeProfile) NewerThan(a, b string) (bool, error) { return newerVersionThan(a, b) }

// SnippetPattern matches TOML snippets, which merge into config.toml as TOML.
func (mistralVibeProfile) SnippetPattern() string { return configTOMLSnippetPattern }

func (mistralVibeProfile) VMConfigPath(home string) string {
	return filepath.Join(home, mistralVibeDataDir, mistralVibeConfigFileName)
}
func (mistralVibeProfile) ConfigFileNames() []string { return []string{mistralVibeConfigFileName} }

// ProvisionRules copies only the user-authored files that carry no credential:
// the user instructions and the agent, prompt, and skill trees. config.toml can
// hold MCP and provider headers and an MCP server's env beside its settings,
// .env is the API key store, and hooks.toml runs host commands; keys reach the
// VM only as msb secrets. The trust store and the rest of the data root are
// per-machine or runtime state.
func (mistralVibeProfile) ProvisionRules() []ProvisionRule {
	return []ProvisionRule{
		{
			Dir:      mistralVibeDataDir,
			Patterns: []string{agentsRulesPattern, "/agents/", promptsDirPattern, skillsDirPattern},
		},
	}
}

// ReadBackPaths declares vibe's .env, where /login stores an API key and which
// the reader classifies as a dotenv. config.toml is withheld: its MCP headers
// and env are shapes the reader does not classify yet.
func (mistralVibeProfile) ReadBackPaths() []string {
	return []string{mistralVibeDataDir + "/.env"}
}

func (mistralVibeProfile) CredentialProbes() []string { return nil }

// AttachCommand forwards every argument shell-quoted, because the command runs
// through `bash -c`.
func (mistralVibeProfile) AttachCommand(_ string, args []string) string {
	parts := append([]string{"vibe"}, shellQuoteArgs(args)...)
	return strings.Join(parts, " ")
}

func (mistralVibeProfile) VersionCmd() string { return "vibe --version" }
func (mistralVibeProfile) ParseVersion(stdout string) (string, error) {
	return extractSemverFromOutput(stdout)
}

// InstructionPaths declares the credential-free files the host seed copies:
// the user instructions and the agent, prompt, and skill trees.
func (mistralVibeProfile) InstructionPaths() []string {
	return []string{agentsRulesFileName, agentsDirName, promptsDirName, skillsDirName}
}
