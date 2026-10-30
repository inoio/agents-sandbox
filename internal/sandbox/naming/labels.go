package naming

// Labels the launcher attaches to the sandboxes it creates. New artifacts carry
// their identity and kind here rather than only in the name, because the name
// shape cannot distinguish a project whose folder itself begins with a legacy
// prefix (e.g. a folder named "agents-sandbox-vm"). The org.agents-sandbox.
// prefix is shared with the runner-image labels.
const (
	// LabelType is the artifact kind: SandboxTypeProject or SandboxTypeTask.
	LabelType = "org.agents-sandbox.type"
	// LabelSlug is the project checkout slug the artifact belongs to. It is the
	// full slug even when the name was truncated to the sandbox-name limit.
	LabelSlug = "org.agents-sandbox.slug"
	// LabelAgent is the agent the artifact belongs to.
	LabelAgent = "org.agents-sandbox.agent"

	// LabelProject identifies the repo the sandbox's project belongs to.
	LabelProject = "org.agents-sandbox.project"
	// LabelImage records the runner image reference the sandbox was created with.
	LabelImage = "org.agents-sandbox.image"

	// LabelTask is the pre-LabelType task marker. New task sandboxes carry
	// LabelType=SandboxTypeTask; the launcher still reads LabelTask so a task
	// sandbox created by an earlier version stays classified.
	LabelTask = "org.agents-sandbox.task"

	// SandboxTypeProject and SandboxTypeTask are the LabelType values.
	SandboxTypeProject = "project"
	SandboxTypeTask    = "task"
)

// ProjectSandboxLabels returns the labels for a project VM.
func ProjectSandboxLabels(slug, agent, repoSlug, imageRef string) map[string]string {
	return map[string]string{
		LabelType:    SandboxTypeProject,
		LabelSlug:    slug,
		LabelAgent:   agent,
		LabelProject: repoSlug,
		LabelImage:   imageRef,
	}
}

// TaskSandboxLabels returns the labels for a transient task sandbox.
func TaskSandboxLabels(slug, agent string) map[string]string {
	return map[string]string{
		LabelType:  SandboxTypeTask,
		LabelSlug:  slug,
		LabelAgent: agent,
	}
}
