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
	// OwnedToolPaths are the paths inside the runner image that must belong to
	// the runtime user. They exist for agents that write into their own
	// installation at runtime (Hermes keeps a dependency ledger and bytecode
	// markers in its checkout and rejects a git checkout owned by another
	// user), so a tree left owned by the build's root is unusable for dev. The
	// renderer chowns them to `dev` after copying the tool tree in and before
	// it switches to `USER dev`. Agents that never write into their
	// installation leave it nil.
	OwnedToolPaths []string
}

// versionArgFor derives the Docker build ARG name for an agent from its name,
// e.g. "opencode" -> "OPENCODE_VERSION", "claude-code" -> "CLAUDE_CODE_VERSION".
func versionArgFor(name string) string {
	return strings.ToUpper(strings.ReplaceAll(name, "-", "_")) + "_VERSION"
}
