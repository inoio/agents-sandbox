package agent

import (
	"context"
	"path/filepath"
	"strings"
)

// hermesName is the canonical registry name of the Hermes Agent profile.
const hermesName = "hermes"

// hermesConfigFileName is the YAML settings file Hermes reads from its home.
const hermesConfigFileName = "config.yaml"

// hermesBuildHome is the home the installer provisions at build time. It holds
// what has to survive outside the sandbox home volume: the managed Python and
// Node tool store, the uv cache, and the dependency environments under
// installs/.
const hermesBuildHome = "/opt/agents-sandbox/hermes-home"

// hermesGuestHome is the home Hermes runs with inside the sandbox: the same
// path as mounts.VMHomeDir, so Hermes' config, sessions, memories, and skills
// live on the persistent home volume the launcher provisions into.
const hermesGuestHome = "/home/dev/.hermes"

// hermesLauncherShim replaces the launcher the installer publishes. It keeps
// user state in $HERMES_HOME (the home volume) and links the baked tool store,
// uv cache, and dependency environments into it, so a fresh home volume starts
// Hermes without reinstalling its dependencies — which the installer would
// otherwise do, because Hermes keys dependency state per home.
const hermesLauncherShim = `printf '%s\n' ` +
	`'#!/bin/sh' ` +
	`'home="${HERMES_HOME:-$HOME/.hermes}"' ` +
	`'mkdir -p "$home"' ` +
	`'for d in tools cache installs; do [ -e "$home/$d" ] || ln -s "` + hermesBuildHome + `/$d" "$home/$d"; done' ` +
	`'exec /opt/agents-sandbox/hermes/.hermes/bin/hermes "$@"' ` +
	`> /opt/agents-sandbox/bin/hermes && chmod 755 /opt/agents-sandbox/bin/hermes`

type hermesProfile struct{}

func (hermesProfile) Name() string          { return hermesName }
func (hermesProfile) ConfigDirName() string { return "hermes" }

func (hermesProfile) ImageSpec() ImageSpec {
	return ImageSpec{
		VersionArg: versionArgFor(hermesName),
		// Hermes publishes no binary: its official installer clones the pinned
		// release tag and provisions a Python/Node tool store, then writes a
		// launcher carrying absolute interpreter paths. Checkout and store
		// therefore live under toolDir, outside /home/dev, which the sandbox
		// home volume covers at runtime; RuntimeOwnedPaths hands them to the
		// runner's dev user afterwards, because Hermes keeps its dependency
		// ledger and bytecode markers there. --non-interactive skips the setup
		// wizard and the gateway service install, and the browser/computer-use
		// tools are dropped because the runner image carries no desktop stack for
		// them. libatomic1 is pre-installed: the installer's package manager
		// would otherwise reach for apt through sudo, which the tool base does
		// not have.
		AgentEnv: map[string]string{"HERMES_HOME": hermesGuestHome},
		InstallCommand: `apt-get update && apt-get install -y --no-install-recommends git libatomic1 && rm -rf /var/lib/apt/lists/* && ` +
			`curl -fsSL https://hermes-agent.nousresearch.com/install.sh -o /tmp/hermes-install.sh && ` +
			`bash /tmp/hermes-install.sh --commit "v$HERMES_VERSION" --dir /opt/agents-sandbox/hermes ` +
			`--hermes-home ` + hermesBuildHome + ` --non-interactive --skip-browser --skip-computer-use && ` +
			hermesLauncherShim,
		// The agent stage installs no node: Hermes' installer provisions the
		// node build its TUI and web products need into its own tool store.
		RequiresNode: false,
	}
}

func (hermesProfile) LatestVersion(ctx context.Context) (string, error) {
	return latestHermesVersion(ctx)
}
func (hermesProfile) NewerThan(a, b string) (bool, error) { return newerVersionThan(a, b) }

func (hermesProfile) SnippetPattern() string { return "config*.yaml" }
func (hermesProfile) VMConfigPath(home string) string {
	return filepath.Join(home, ".hermes", hermesConfigFileName)
}
func (hermesProfile) ConfigFileNames() []string { return []string{hermesConfigFileName} }

func (hermesProfile) ProvisionRules() []ProvisionRule {
	return []ProvisionRule{
		// Settings only: credentials live in ~/.hermes/.env and the rest of the
		// home is runtime state (state.db, sessions, memories), which belongs to
		// the sandbox, not to the host.
		{Dir: ".hermes", Patterns: []string{hermesConfigFileName}},
	}
}

func (hermesProfile) AttachCommand(_ string, args []string) string {
	parts := []string{"hermes"}
	parts = append(parts, shellQuoteArgs(args)...)
	return strings.Join(parts, " ")
}

// RuntimeOwnedPaths hands the checkout and the tool store to the runner's dev
// user: Hermes writes its dependency ledger, bytecode markers, and product
// caches there at runtime, and git rejects a checkout owned by another user.
func (hermesProfile) RuntimeOwnedPaths() []string {
	return []string{"/opt/agents-sandbox/hermes", hermesBuildHome}
}

func (hermesProfile) VersionCmd() string { return "hermes --version" }
func (hermesProfile) ParseVersion(stdout string) (string, error) {
	return extractSemverFromOutput(stdout)
}
