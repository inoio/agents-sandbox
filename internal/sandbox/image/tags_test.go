package image

import (
	"testing"
)

func TestImageTag(t *testing.T) {
	got := runnerTag("myproj-aBc1234D", "opencode")
	expected := "agents-sandbox/runner-myproj-aBc1234D:opencode-latest"
	if got != expected {
		t.Errorf("expected %q, got %q", expected, got)
	}
}

func TestRunnerTagIsPerAgent(t *testing.T) {
	if got := runnerTag("myproject", "opencode"); got != "agents-sandbox/runner-myproject:opencode-latest" {
		t.Errorf("runnerTag(opencode) = %q", got)
	}
	if got := runnerTag("myproject", "pi"); got != "agents-sandbox/runner-myproject:pi-latest" {
		t.Errorf("runnerTag(pi) = %q", got)
	}
}
