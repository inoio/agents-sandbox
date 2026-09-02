package naming

// Labels the launcher attaches to the project sandbox VM at creation. The
// org.opencode-sandbox. prefix is shared with the runner-image labels.
const (
	// LabelProject identifies the project the sandbox belongs to.
	LabelProject = "org.opencode-sandbox.project"
	// LabelImage records the runner image reference the sandbox was created with.
	LabelImage = "org.opencode-sandbox.image"
)
