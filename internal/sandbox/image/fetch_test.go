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

// fakeUpgradeAgent implements Agent + UpgradeChecker with a controllable latest.
type fakeUpgradeAgent struct{ latest string }

func (fakeUpgradeAgent) Name() string          { return "fake-upgrade" }
func (fakeUpgradeAgent) ConfigDirName() string { return "fake-upgrade" }
func (fakeUpgradeAgent) ImageSpec() agent.ImageSpec {
	return agent.ImageSpec{InstallCommand: "true"}
}
func (f fakeUpgradeAgent) LatestVersion(_ context.Context) (string, error) { return f.latest, nil }
func (fakeUpgradeAgent) NewerThan(_, _ string) (bool, error)               { return false, nil }

// The agent's own UpgradeChecker must drive the resolved version, not opencode's.
func TestResolveAgentVersionUsesAgentsOwnChecker(t *testing.T) {
	got, err := resolveAgentVersion(context.Background(), fakeUpgradeAgent{latest: "7.7.7"}, "")
	if err != nil {
		t.Fatalf("resolveAgentVersion error: %v", err)
	}
	if got != "7.7.7" {
		t.Errorf("resolveAgentVersion = %q, want the fake agent's latest 7.7.7", got)
	}
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
	WithMockAgentVersion(t, "9.9.9")
	got, err := resolveAgentVersion(context.Background(), plainAgent{}, "")
	if err != nil {
		t.Fatalf("resolveAgentVersion error: %v", err)
	}
	if got != "9.9.9" {
		t.Errorf("resolveAgentVersion = %q, want 9.9.9", got)
	}
}
