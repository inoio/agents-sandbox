package agent

import (
	"context"
	"path/filepath"
	"strings"
)

// grokBuildName is the canonical registry name of xAI's Grok Build CLI. Its
// binary is grok.
const grokBuildName = "grok-build"

// grokBuildDataDir is grok's home-relative data root: config, login, sessions,
// trust stores, and the cloud-synced policy cache live under it.
const grokBuildDataDir = ".grok"

// grokBuildConfigFileName is grok's user config file under its data root.
const grokBuildConfigFileName = "config.toml"

// grokBuildDisableAutoUpdater suppresses grok's launch-time auto-updater for
// the process.
const grokBuildDisableAutoUpdater = "GROK_DISABLE_AUTOUPDATER"

// grokBuildResolveAsset sets grok_arch to the release's architecture name for
// the build machine and fails the build on any other architecture, so a
// wrong-arch binary is never installed.
const grokBuildResolveAsset = `case "$(uname -m)" in ` +
	`x86_64|amd64) grok_arch=x86_64 ;; ` +
	`aarch64|arm64) grok_arch=aarch64 ;; ` +
	`*) echo "grok-build: unsupported architecture $(uname -m)" >&2; exit 1 ;; ` +
	`esac`

// grokBuildDownload fetches the compressed release binary from x.ai and, when
// that download fails, from the storage bucket xAI's installer falls back to.
const grokBuildDownload = `grok_asset="grok-${GROK_BUILD_VERSION}-linux-$grok_arch.gz" && ` +
	`{ curl -fsSL "https://x.ai/cli/$grok_asset" -o "$grok_dir/grok.gz" || ` +
	`curl -fsSL "https://storage.googleapis.com/grok-build-public-artifacts/cli/$grok_asset" -o "$grok_dir/grok.gz"; }`

// grokBuildPinNoAutoUpdate pins the auto-updater off in the system
// requirements file, the highest-priority config layer, so no user or project
// setting turns it back on: the sandbox pins the baked version and offers
// upgrades itself. It runs in the runner stage, which the file must be part of:
// the agent stage is not in the runner's lineage, so a write there is discarded.
const grokBuildPinNoAutoUpdate = `mkdir -p /etc/grok && ` +
	`printf '%s\n' '[cli]' 'auto_update = false' > /etc/grok/requirements.toml`

type grokBuildProfile struct{}

func (grokBuildProfile) Name() string          { return grokBuildName }
func (grokBuildProfile) ConfigDirName() string { return grokBuildName }
func (grokBuildProfile) Experimental() bool    { return true }

// ImageSpec installs grok's static release binary, which needs no shared
// library. xAI publishes no checksums for the binaries it hosts, so, like codex and goose, it is
// fetched over TLS by its pinned version; the build runs the binary once so a
// broken download never ships.
func (grokBuildProfile) ImageSpec() ImageSpec {
	return ImageSpec{
		VersionArg: versionArgFor(grokBuildName),
		AgentEnv:   map[string]string{grokBuildDisableAutoUpdater: "1"},
		InstallCommand: grokBuildResolveAsset + ` && ` +
			`grok_dir=/tmp/grok-build && mkdir -p "$grok_dir" && ` +
			grokBuildDownload + ` && ` +
			`gzip -dc "$grok_dir/grok.gz" > "$grok_dir/grok" && ` +
			`install -m 0755 "$grok_dir/grok" /opt/agents-sandbox/bin/grok && rm -rf "$grok_dir" && ` +
			grokBuildDisableAutoUpdater + `=1 /opt/agents-sandbox/bin/grok --version`,
		RequiresNode: false,
		// grok keeps its sessions and caches under ~/.grok on the home volume, so
		// the tool tree stays root-owned.
		OwnsToolTree:  false,
		RuntimeLibs:   nil,
		RunnerCommand: grokBuildPinNoAutoUpdate,
	}
}

func (grokBuildProfile) LatestVersion(ctx context.Context) (string, error) {
	return latestGrokBuildVersion(ctx)
}
func (grokBuildProfile) NewerThan(a, b string) (bool, error) { return newerVersionThan(a, b) }

// SnippetPattern matches TOML snippets, which merge into config.toml as TOML.
func (grokBuildProfile) SnippetPattern() string { return configTOMLSnippetPattern }

func (grokBuildProfile) VMConfigPath(home string) string {
	return filepath.Join(home, grokBuildDataDir, grokBuildConfigFileName)
}
func (grokBuildProfile) ConfigFileNames() []string { return []string{grokBuildConfigFileName} }

// ProvisionRules copies only the user-authored files that carry no credential:
// the global rules files, skills, sandbox profiles, and workflows. config.toml
// can hold a model's api_key and MCP headers beside its settings, and auth.json
// holds the login; keys reach the VM only as msb secrets. Hooks run host
// commands and plugins can bundle MCP configs, so neither is copied. The policy
// cache and trust stores are per-machine state, and the rest of the data root
// is runtime state.
func (grokBuildProfile) ProvisionRules() []ProvisionRule {
	return []ProvisionRule{
		{Dir: grokBuildDataDir, Patterns: []string{
			agentsRulesPattern, "/Agents.md", "/AGENT.md", "/CLAUDE.md", "/Claude.md",
			skillsDirPattern, "/sandbox.toml", "/workflows/",
		}},
	}
}

// ReadBackPaths declares grok's login store only. config.toml is withheld: it
// can carry a static key inline (`[model.<id>] api_key`), which the read-back
// does not classify yet, so a learned write-back would copy the raw key into
// managed config. It is declared once the inline key is classified, the way
// kimi-code's config.toml is.
func (grokBuildProfile) ReadBackPaths() []string {
	return []string{
		grokBuildDataDir + "/auth.json",
	}
}

func (grokBuildProfile) CredentialProbes() []string { return nil }

// AttachCommand forwards every argument shell-quoted, because the command runs
// through `bash -c`.
func (grokBuildProfile) AttachCommand(_ string, args []string) string {
	parts := append([]string{"grok"}, shellQuoteArgs(args)...)
	return strings.Join(parts, " ")
}

func (grokBuildProfile) VersionCmd() string { return "grok --version" }
func (grokBuildProfile) ParseVersion(stdout string) (string, error) {
	return extractSemverFromOutput(stdout)
}
