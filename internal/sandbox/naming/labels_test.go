package naming

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLabelConstants(t *testing.T) {
	if LabelProject != "org.agents-sandbox.project" {
		t.Errorf("LabelProject = %q, want org.agents-sandbox.project", LabelProject)
	}
	if LabelImage != "org.agents-sandbox.image" {
		t.Errorf("LabelImage = %q, want org.agents-sandbox.image", LabelImage)
	}
	if LabelTask != "org.agents-sandbox.task" {
		t.Errorf("LabelTask = %q, want org.agents-sandbox.task", LabelTask)
	}
}

func TestProjectSandboxLabels(t *testing.T) {
	require.Equal(t, map[string]string{
		LabelType:    SandboxTypeProject,
		LabelSlug:    "proj-1mjusbm3wikhb0",
		LabelAgent:   "opencode",
		LabelProject: "repo-1mjusbm3wikhb0",
		LabelImage:   "proj-1mjusbm3wikhb0:opencode-latest",
	}, ProjectSandboxLabels(
		"proj-1mjusbm3wikhb0",
		"opencode",
		"repo-1mjusbm3wikhb0",
		"proj-1mjusbm3wikhb0:opencode-latest",
	))
}

func TestTaskSandboxLabels(t *testing.T) {
	require.Equal(t, map[string]string{
		LabelType:  SandboxTypeTask,
		LabelSlug:  "proj-1mjusbm3wikhb0",
		LabelAgent: "opencode",
	}, TaskSandboxLabels("proj-1mjusbm3wikhb0", "opencode"))
}

// TestSandboxIdentityPrefersLabels pins that the labels win over a name that
// would otherwise parse as an unrelated artifact.
func TestSandboxIdentityPrefersLabels(t *testing.T) {
	require.Equal(t, ArtifactInfo{Slug: "proj-1mjusbm3wikhb0", Agent: "opencode", Task: true},
		SandboxIdentity("unrelated-name", map[string]string{
			LabelType:  SandboxTypeTask,
			LabelSlug:  "proj-1mjusbm3wikhb0",
			LabelAgent: "opencode",
		}))
}

// TestSandboxIdentityTaskWithoutSlug pins the task marker without a slug label:
// the name still supplies the key, the label supplies the kind.
func TestSandboxIdentityTaskWithoutSlug(t *testing.T) {
	require.True(t, SandboxIdentity("proj-1mjusbm3wikhb0-opencode",
		map[string]string{LabelType: SandboxTypeTask}).Task)
	require.True(t, SandboxIdentity("proj-1mjusbm3wikhb0-opencode",
		map[string]string{LabelTask: "1"}).Task)
}

// TestSandboxIdentityNameFallback pins the pre-label path: a nil labels map
// falls back to the name heuristic alone.
func TestSandboxIdentityNameFallback(t *testing.T) {
	require.Equal(t, ArtifactInfo{Slug: "proj-1mjusbm3wikhb0", Agent: "opencode"},
		SandboxIdentity("proj-1mjusbm3wikhb0-opencode", nil))
}

func TestSandboxIdentityClassifiesBothSchemesAndLabels(t *testing.T) {
	require.True(t, SandboxIdentity("myproj-1mjusbm3wikhb0-opencode-1729000000000000000", nil).Task)
	require.True(t, SandboxIdentity("agents-sandbox-task-x-1729", nil).Task)
	require.True(t, SandboxIdentity("proj-aBc1234D", map[string]string{LabelTask: "1"}).Task)
	require.True(
		t,
		SandboxIdentity("proj-aBc1234D", map[string]string{LabelType: SandboxTypeTask}).Task,
	)
	require.False(t, SandboxIdentity("myproj-1mjusbm3wikhb0-opencode", nil).Task)

	// The label is authoritative: a prefix-free project VM whose folder begins
	// with the legacy task prefix is not a task when labelled as a project.
	project := map[string]string{
		LabelType:  SandboxTypeProject,
		LabelSlug:  "agents-sandbox-task-1mjusbm3wikhb0",
		LabelAgent: "opencode",
	}
	got := SandboxIdentity("agents-sandbox-task-1mjusbm3wikhb0-opencode", project)
	require.False(t, got.Task)
	require.Equal(t, "agents-sandbox-task-1mjusbm3wikhb0", got.Slug)
	require.Equal(t, "opencode", got.Agent)

	// The label carries the full slug even when the name was truncated.
	truncated := map[string]string{
		LabelType:  SandboxTypeProject,
		LabelSlug:  "a-very-long-folder-name-1mjusbm3wikhb0",
		LabelAgent: "opencode",
	}
	require.Equal(t, "a-very-long-folder-name-1mjusbm3wikhb0",
		SandboxIdentity("agents-sandbox-vm-", truncated).Slug)
}
