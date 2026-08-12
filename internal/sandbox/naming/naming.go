package naming

// Prefix is the canonical base name for all opencode-msb naming conventions.
// Changing this value renames the tool across all namespaces, annotations,
// VM names, image references, sandbox names, and volume names.
// Filesystem paths are exempt, they are defined separately in config_paths.go.
const Prefix = "opencode-msb"

const BaseSlug = "base"

// Sandbox and image name prefixes derived from Prefix.
const (
	SbPrefix        = Prefix + "-"
	VmPrefix        = Prefix + "-vm-" //nolint:staticcheck,revive // follows existing brief naming convention (ST1003, var-naming)
	HomePrefix      = Prefix + "-home-"
	ClonePrefix     = Prefix + "-clone-"
	TaskPrefix      = Prefix + "-task-"
	ImagePrefix     = Prefix + "/runner-"
	BaseImagePrefix = Prefix + "/runner-base"
)

// Fully-qualified image references derived from Prefix.
const (
	BaseTag     = BaseImagePrefix + ":latest"
	DindBaseTag = BaseImagePrefix + "-dind:latest"
)

type ArtifactInfo struct {
	Slug   string
	Digest string
}
