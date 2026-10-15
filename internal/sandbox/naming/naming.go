package naming

import "strings"

// Prefix is the canonical base name for all agents-sandbox naming
// conventions. Changing this value renames the tool across all namespaces,
// annotations, VM names, image references, sandbox names, and volume names.
const Prefix = "agents-sandbox"

// Sandbox and image name prefixes derived from Prefix.
const (
	VMPrefix    = Prefix + "-vm-"
	HomePrefix  = Prefix + "-home-"
	TaskPrefix  = Prefix + "-task-"
	ImagePrefix = Prefix + "/runner-"
)

// IsRunnerImageRef reports whether ref names a runner image, tolerating an
// optional registry authority prefix (the embedded registry's host:port) that a
// registry-pull reference carries.
func IsRunnerImageRef(ref string) bool {
	return strings.HasPrefix(ref, ImagePrefix) || strings.Contains(ref, "/"+ImagePrefix)
}

// runnerImageSuffix returns the "<ImagePrefix>...:tag" portion of ref, dropping
// any registry authority the embedded registry prepends.
func runnerImageSuffix(ref string) string {
	if i := strings.Index(ref, ImagePrefix); i >= 0 {
		return ref[i:]
	}
	return ref
}
