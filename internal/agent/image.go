package agent

import "strings"

// ImageSpec carries the structured bits needed to bake an agent into the runner
// image: the version build ARG, env vars to set in the image (e.g., to disable
// runtime auto-update), the install command, whether the agent needs Node.js
// present at install and runtime (true only for npm-installed node bundles), and
// the tool-tree paths the runner hands to its dev user.
type ImageSpec struct {
	VersionArg     string
	AgentEnv       map[string]string
	InstallCommand string
	RequiresNode   bool
	// OwnedToolPaths marks that the agent writes into its own installation at
	// runtime (Hermes keeps a dependency ledger and bytecode markers in its
	// checkout and rejects a git checkout owned by another user), so the tool
	// tree must belong to the runtime user. A non-empty slice makes the
	// renderer's tool-tree COPY carry --chown=dev:dev, handing the whole copied
	// tree to dev at copy time; the listed paths are the declaration, not the
	// chown scope (a granular chown would record a second full copy of the
	// tree in its layer diff). A nil or empty slice leaves the tree root-owned.
	OwnedToolPaths []string
	// RuntimeLibs are shared libraries the agent's provisioned runtime links at
	// run time. The runner copies only the agent's tool tree into the project's
	// base, so a system library the runtime links is absent unless the base
	// provides it (Hermes's managed Node.js links libatomic.so.1 on glibc). The
	// renderer verifies each in the final stage and fails the build naming it, so
	// a custom base that lacks one fails at build time instead of when the agent
	// launches. The check is glibc-only; musl bases are skipped. Agents that need
	// none leave it nil.
	RuntimeLibs []string
}

// versionArgFor derives the Docker build ARG name for an agent from its name,
// e.g. "opencode" -> "OPENCODE_VERSION", "claude-code" -> "CLAUDE_CODE_VERSION".
func versionArgFor(name string) string {
	return strings.ToUpper(strings.ReplaceAll(name, "-", "_")) + "_VERSION"
}
