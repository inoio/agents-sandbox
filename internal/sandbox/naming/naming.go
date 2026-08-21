package naming

// Prefix is the canonical base name for all opencode-sandbox naming
// conventions. Changing this value renames the tool across all namespaces,
// annotations, VM names, image references, sandbox names, and volume names.
const Prefix = "opencode-sandbox"

const BaseSlug = "base"
const BaseDindSlug = "base-dind"

// Sandbox and image name prefixes derived from Prefix.
const (
	VmPrefix            = Prefix + "-vm-" //nolint:staticcheck,revive // follows existing brief naming convention (ST1003, var-naming)
	HomePrefix          = Prefix + "-home-"
	TaskPrefix          = Prefix + "-task-"
	ImagePrefix         = Prefix + "/runner-"
	BaseImagePrefix     = Prefix + "/runner-base"
	BaseDindImagePrefix = Prefix + "/runner-base-dind"
)

// Fully-qualified image references derived from Prefix.
const (
	BaseTag     = BaseImagePrefix + ":latest"
	DindBaseTag = BaseDindImagePrefix + ":latest"
)
