package naming

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
