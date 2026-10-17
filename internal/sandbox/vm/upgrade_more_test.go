package vm

import (
	"context"
	"errors"
	"testing"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// TestResolveOpenCodeVersionInteractiveSelectError covers the Select failure
// branch of resolveBuildVersion: an interactive prompt whose selection
// errors is propagated.
func TestResolveOpenCodeVersionInteractiveSelectError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := state.Key{Slug: "proj", Agent: "opencode"}
	seedAgentState(t, k, "1.0.0", agentSourceTool)
	origLatest := agentLatestVersion
	defer func() { agentLatestVersion = origLatest }()
	agentLatestVersion = func(_ context.Context, _ agent.Agent) (string, error) { return "2.0.0", nil }

	ui := &termio.Mock{
		IsInteractiveResult: true,
		SelectFn: func(_ string, _ []termio.Choice, _ string) (string, error) {
			return "", errors.New("selection failed")
		},
	}

	_, _, err := resolveBuildVersion(context.Background(), k, opencodeAgent(t), ui, options.RunOptions{})
	if err == nil {
		t.Fatal("expected error when the interactive selection fails")
	}
}

// TestResolveOpenCodeVersionInteractiveDefaultChoice covers the
// default/unknown-choice branch: an unrecognized selection keeps the current
// version.
func TestResolveOpenCodeVersionInteractiveDefaultChoice(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := state.Key{Slug: "proj", Agent: "opencode"}
	seedAgentState(t, k, "1.0.0", agentSourceTool)
	origLatest := agentLatestVersion
	defer func() { agentLatestVersion = origLatest }()
	agentLatestVersion = func(_ context.Context, _ agent.Agent) (string, error) { return "2.0.0", nil }

	ui := &termio.Mock{
		IsInteractiveResult: true,
		SelectFn: func(_ string, _ []termio.Choice, _ string) (string, error) {
			return "unknown", nil
		},
	}

	got, upgraded, err := resolveBuildVersion(context.Background(), k, opencodeAgent(t), ui, options.RunOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if upgraded {
		t.Error("expected upgraded=false for an unknown choice")
	}
	if got != "1.0.0" {
		t.Errorf("expected current version for an unknown choice, got %q", got)
	}
}

// TestResolveOpenCodeVersionInteractiveKeep covers the "keep" choice branch:
// the current version is kept.
func TestResolveOpenCodeVersionInteractiveKeep(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := state.Key{Slug: "proj", Agent: "opencode"}
	seedAgentState(t, k, "1.0.0", agentSourceTool)
	origLatest := agentLatestVersion
	defer func() { agentLatestVersion = origLatest }()
	agentLatestVersion = func(_ context.Context, _ agent.Agent) (string, error) { return "2.0.0", nil }

	ui := &termio.Mock{
		IsInteractiveResult: true,
		SelectFn: func(_ string, _ []termio.Choice, _ string) (string, error) {
			return "k", nil
		},
	}

	got, upgraded, err := resolveBuildVersion(context.Background(), k, opencodeAgent(t), ui, options.RunOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if upgraded {
		t.Error("expected upgraded=false for the keep choice")
	}
	if got != "1.0.0" {
		t.Errorf("expected current version for the keep choice, got %q", got)
	}
}

// TestFindWorktreeDirInvalidJSON covers the invalid-JSON branch of
// findWorktreeDir.
func TestFindWorktreeDirInvalidJSON(t *testing.T) {
	if _, ok := findWorktreeDir("not valid json", "slug"); ok {
		t.Error("expected no match for invalid JSON")
	}
}

// TestFindWorktreeDirMalformedEntry covers the malformed-entry branches of
// findWorktreeDir (entries that cannot be unmarshalled).
func TestFindWorktreeDirMalformedEntry(t *testing.T) {
	// A string entry that is a malformed JSON string.
	if _, ok := findWorktreeDir(`["unterminated`, "slug"); ok {
		t.Error("expected no match for malformed string entry")
	}
	// An object entry that is malformed.
	if _, ok := findWorktreeDir(`[{"directory":`, "slug"); ok {
		t.Error("expected no match for malformed object entry")
	}
}

// TestResolveBuildVersionWithoutUpgradeChecker covers the fallback branch of
// resolveBuildVersion: an agent with no upgrade checker reuses the recorded
// version without prompting.
func TestResolveBuildVersionWithoutUpgradeChecker(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := state.Key{Slug: "proj", Agent: "fake"}
	seedAgentState(t, k, "1.5.0", agentSourceTool)
	got, upgraded, err := resolveBuildVersion(
		context.Background(),
		k,
		&fakeAgent{},
		&termio.Mock{},
		options.RunOptions{},
	)
	if err != nil {
		t.Fatalf("resolveBuildVersion: %v", err)
	}
	if got != "1.5.0" {
		t.Errorf("resolveBuildVersion = %q, want recorded version 1.5.0", got)
	}
	if upgraded {
		t.Error("expected upgraded=false for an agent without an upgrade checker")
	}
}

// TestPendingUpgradeWithoutUpgradeChecker covers the no-checker branch of
// pendingUpgrade: it never offers an upgrade for an agent lacking a checker.
func TestPendingUpgradeWithoutUpgradeChecker(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := state.Key{Slug: "proj", Agent: "fake"}
	latest, offer := pendingUpgrade(context.Background(), &fakeAgent{}, k, &termio.Mock{}, "1.0.0")
	if offer {
		t.Errorf("expected no upgrade offer for an agent without a checker, got latest=%q", latest)
	}
}
