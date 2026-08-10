package sandbox

// Prefix is the canonical base name for all opencode-msb naming conventions.
// Changing this value renames the tool across all namespaces, annotations,
// VM names, image references, sandbox names, and volume names.
// Filesystem paths are exempt, they are defined separately in paths.go.
const Prefix = "opencode-msb"

const baseSlug = "base"

// Sandbox and image name prefixes derived from Prefix.
const (
	sbPrefix        = Prefix + "-"
	vmPrefix        = Prefix + "-vm-"
	homePrefix      = Prefix + "-home-"
	clonePrefix     = Prefix + "-clone-"
	taskPrefix      = Prefix + "-task-"
	imagePrefix     = Prefix + "/runner-"
	baseImagePrefix = Prefix + "/runner-base"
)

// Fully-qualified image references derived from Prefix.
const (
	baseTag     = baseImagePrefix + ":latest"
	dindBaseTag = baseImagePrefix + "-dind:latest"
)
