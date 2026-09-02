package agent

import "strings"

// ImageSpec carries the structured bits needed to bake an agent into the runner
// image: the version build ARG, env vars to set in the image (e.g. to disable
// runtime auto-update), and the install command.
type ImageSpec struct {
	VersionArg     string
	AgentEnv       map[string]string
	InstallCommand string
}

// versionArgFor derives the Docker build ARG name for an agent from its name,
// e.g. "opencode" -> "OPENCODE_VERSION", "claude-code" -> "CLAUDE_CODE_VERSION".
func versionArgFor(name string) string {
	return strings.ToUpper(strings.ReplaceAll(name, "-", "_")) + "_VERSION"
}
