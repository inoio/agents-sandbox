package agent

import "strings"

// ImageSpec carries the structured bits needed to bake an agent into the runner
// image: the version build ARG, the Docker label recording the baked version,
// env vars to set in the image (e.g. to disable runtime auto-update), and the
// install command.
type ImageSpec struct {
	VersionArg     string
	VersionLabel   string
	AgentEnv       map[string]string
	InstallCommand string
}

// versionArgFor derives the Docker build ARG name for an agent from its name,
// e.g. "opencode" -> "OPENCODE_VERSION", "claude-code" -> "CLAUDE_CODE_VERSION".
func versionArgFor(name string) string {
	return strings.ToUpper(strings.ReplaceAll(name, "-", "_")) + "_VERSION"
}

// versionLabelFor derives the Docker version label for an agent from its name,
// e.g. "opencode" -> "org.opencode-sandbox.opencode-version".
func versionLabelFor(name string) string {
	return "org.opencode-sandbox." + name + "-version"
}
