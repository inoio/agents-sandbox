package image

import (
	"context"
	"testing"

	"github.com/inoio/opencode-sandbox/internal/agent"
)

// plainAgent is an Agent that implements no optional capabilities, so
// resolveAgentVersion takes the fallback branch.
type plainAgent struct{}

func (plainAgent) Name() string          { return "plain" }
func (plainAgent) ConfigDirName() string { return "plain" }
func (plainAgent) ImageSpec() agent.ImageSpec {
	return agent.ImageSpec{InstallCommand: "true"}
}

func TestResolveAgentVersionWithoutUpgradeChecker(t *testing.T) {
	got, err := resolveAgentVersion(context.Background(), plainAgent{}, "1.2.3")
	if err != nil {
		t.Fatalf("resolveAgentVersion error: %v", err)
	}
	if got != "1.2.3" {
		t.Errorf("resolveAgentVersion = %q, want requested version 1.2.3", got)
	}
}

func TestResolveOpenCodeVersionRequestsLatest(t *testing.T) {
	WithMockOpenCodeVersion(t, "9.9.9")
	got, err := resolveOpenCodeVersion(context.Background(), "")
	if err != nil {
		t.Fatalf("resolveOpenCodeVersion error: %v", err)
	}
	if got != "9.9.9" {
		t.Errorf("resolveOpenCodeVersion = %q, want 9.9.9", got)
	}
}
