package journeys

import (
	"testing"

	"github.com/inoio/agents-sandbox/test/msb/driver"
)

func TestContextProjectDirDefaultsToTheAgentsSharedProject(t *testing.T) {
	ctx := &Context{Root: "/suite", Agent: "opencode2"}
	if got, want := ctx.ProjectDir(), driver.ProjectDir("/suite", "opencode2"); got != want {
		t.Errorf("ProjectDir() = %q, want the shared %q", got, want)
	}
	ctx.Project = "/suite/projects/opencode2-turn"
	if got := ctx.ProjectDir(); got != ctx.Project {
		t.Errorf("ProjectDir() = %q, want the journey's own %q", got, ctx.Project)
	}
}
