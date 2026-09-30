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

func TestBaseTag(t *testing.T) {
	a := agentOpencode(t)
	if got := baseTag(a, false); got != "agents-sandbox/runner-base:opencode-latest" {
		t.Errorf("baseTag(opencode) = %q", got)
	}
	if got := baseTag(a, true); got != "agents-sandbox/runner-base:opencode-latest-dind" {
		t.Errorf("baseTag(opencode, dind) = %q", got)
	}
}
