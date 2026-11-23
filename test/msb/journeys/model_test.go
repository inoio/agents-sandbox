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

func TestKeywordWrappersSetTheKeywordAndKeepTheStep(t *testing.T) {
	step := Step{Name: "the harness starts", Run: func(*Context) error { return nil }}
	for _, tc := range []struct {
		wrap func(Step) Step
		want Keyword
	}{{Given, KeywordGiven}, {When, KeywordWhen}, {Then, KeywordThen}, {And, KeywordAnd}} {
		wrapped := tc.wrap(step)
		if wrapped.Keyword != tc.want || wrapped.Name != step.Name || wrapped.Run == nil || wrapped.Kind != Action {
			t.Errorf("wrapped = %+v, want keyword %q on the unchanged action", wrapped, tc.want)
		}
	}
}

func TestAsCheckMakesAStepNonFatal(t *testing.T) {
	if got := AsCheck(Step{Name: "x"}).Kind; got != Check {
		t.Errorf("AsCheck kind = %v, want Check", got)
	}
}

func TestStepTextIsTheGherkinLine(t *testing.T) {
	if got := Then(Step{Name: "the reply is shown"}).Text(); got != "Then the reply is shown" {
		t.Errorf("Text() = %q", got)
	}
}
