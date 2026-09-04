package image

import "testing"

func TestRunnerTagIsPerAgent(t *testing.T) {
	if got := runnerTag("myproject", "opencode"); got != "agents-sandbox/runner-myproject:opencode-latest" {
		t.Errorf("runnerTag(opencode) = %q", got)
	}
	if got := runnerTag("myproject", "pi"); got != "agents-sandbox/runner-myproject:pi-latest" {
		t.Errorf("runnerTag(pi) = %q", got)
	}
}
