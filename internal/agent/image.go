package agent

import "strings"

// ImageSpec carries the structured bits needed to bake an agent into the runner
// image: the version build ARG, env vars to set in the image (e.g., to disable
// runtime auto-update), the install command, whether the agent needs Node.js
// present at install and runtime (true only for npm-installed node bundles), and
// whether the runner hands the tool tree to its dev user.
type ImageSpec struct {
	VersionArg     string
	AgentEnv       map[string]string
	InstallCommand string
	RequiresNode   bool
	// OwnsToolTree marks that the agent writes into its own installation at
	// runtime (Hermes keeps a dependency ledger and bytecode markers in its
	// checkout and rejects a git checkout owned by another user), so the tool
	// tree must belong to the runtime user. When true, the renderer's tool-tree
	// COPY carries --chown=dev:dev and hands the whole copied tree to dev at
	// copy time; ownership is set in the COPY rather than a follow-up chown,
	// which would record a second full copy of the tree in its layer diff.
	// False leaves the tree root-owned.
	OwnsToolTree bool
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
