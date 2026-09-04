package vm

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/termio"
)

func TestResolveBuildVersionSkipsCheckForUserAgentSource(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	if err := saveUpgradeState(upgradeState{AgentSource: agentSourceUser, CurrentVersion: "9.9.9"}); err != nil {
		t.Fatal(err)
	}
	var latestCalled bool
	origLatest := agentLatestVersion
	agentLatestVersion = func(_ context.Context, _ agent.Agent) (string, error) {
		latestCalled = true
		return "10.0.0", nil
	}
	t.Cleanup(func() { agentLatestVersion = origLatest })

	got, upgraded, err := resolveBuildVersion(
		context.Background(),
		opencodeAgent(t),
		&termio.Mock{},
		options.RunOptions{},
	)
	if err != nil {
		t.Fatalf("resolveBuildVersion: %v", err)
	}
	if upgraded {
		t.Error("expected upgraded=false for a user-provided agent")
	}
	if got != "9.9.9" {
		t.Errorf("resolveBuildVersion = %q, want recorded 9.9.9", got)
	}
	if latestCalled {
		t.Error("agentLatestVersion must not be called for a user-provided agent")
	}
}

func TestResolveBuildVersionUserAgentEmptyVersionUsesSentinel(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	if err := saveUpgradeState(upgradeState{AgentSource: agentSourceUser}); err != nil {
		t.Fatal(err)
	}
	var latestCalled bool
	origLatest := agentLatestVersion
	agentLatestVersion = func(_ context.Context, _ agent.Agent) (string, error) {
		latestCalled = true
		return "10.0.0", nil
	}
	t.Cleanup(func() { agentLatestVersion = origLatest })

	got, upgraded, err := resolveBuildVersion(
		context.Background(),
		opencodeAgent(t),
		&termio.Mock{},
		options.RunOptions{},
	)
	if err != nil {
		t.Fatalf("resolveBuildVersion: %v", err)
	}
	if upgraded {
		t.Error("expected upgraded=false for a user-provided agent")
	}
	if got != userProvidedAgentVersion {
		t.Errorf("resolveBuildVersion = %q, want sentinel %q", got, userProvidedAgentVersion)
	}
	if latestCalled {
		t.Error("agentLatestVersion must not be called for a user-provided agent")
	}
}

func TestResolveOpenCodeVersionPinnedSkipsUpdateCheck(t *testing.T) {
	origLatest := agentLatestVersion
	defer func() { agentLatestVersion = origLatest }()
	latestCalled := false
	agentLatestVersion = func(_ context.Context, _ agent.Agent) (string, error) {
		latestCalled = true
		return "2.0.0", nil
	}

	configpaths.WithMockConfigPaths(t)
	opts := options.RunOptions{AgentVersion: "2.0.0"}
	ui := &termio.Mock{}

	got, upgraded, err := resolveBuildVersion(context.Background(), opencodeAgent(t), ui, opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "2.0.0" {
		t.Errorf("expected pinned version 2.0.0, got %q", got)
	}
	if upgraded {
		t.Error("expected upgraded=false for a pinned version")
	}
	if latestCalled {
		t.Error("agentLatestVersion should NOT have been called when the version is pinned")
	}
}

func TestResolveOpenCodeVersionRebuildSkipsUpdateCheck(t *testing.T) {
	origLatest := agentLatestVersion
	defer func() { agentLatestVersion = origLatest }()
	latestCalled := false
	agentLatestVersion = func(_ context.Context, _ agent.Agent) (string, error) {
		latestCalled = true
		return "2.0.0", nil
	}

	configpaths.WithMockConfigPaths(t)
	if err := saveUpgradeState(upgradeState{CurrentVersion: "1.0.0"}); err != nil {
		t.Fatalf("saveUpgradeState: %v", err)
	}
	opts := options.RunOptions{Rebuild: true}
	ui := &termio.Mock{}

	got, upgraded, err := resolveBuildVersion(context.Background(), opencodeAgent(t), ui, opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "1.0.0" {
		t.Errorf("expected current version 1.0.0, got %q", got)
	}
	if upgraded {
		t.Error("expected upgraded=false when Rebuild is set")
	}
	if latestCalled {
		t.Error("agentLatestVersion should NOT have been called when Rebuild is set")
	}
}

func TestResolveOpenCodeVersionNoBaseline(t *testing.T) {
	origLatest := agentLatestVersion
	defer func() { agentLatestVersion = origLatest }()
	latestCalled := false
	agentLatestVersion = func(_ context.Context, _ agent.Agent) (string, error) {
		latestCalled = true
		return "2.0.0", nil
	}

	configpaths.WithMockConfigPaths(t)
	opts := options.RunOptions{}
	ui := &termio.Mock{}

	got, upgraded, err := resolveBuildVersion(context.Background(), opencodeAgent(t), ui, opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "" {
		t.Errorf("expected empty version (no baseline), got %q", got)
	}
	if upgraded {
		t.Error("expected upgraded=false without a baseline")
	}
	if latestCalled {
		t.Error("agentLatestVersion should NOT have been called without a baseline")
	}
}

func TestResolveOpenCodeVersionNoNewerVersion(t *testing.T) {
	origLatest := agentLatestVersion
	defer func() { agentLatestVersion = origLatest }()
	agentLatestVersion = func(_ context.Context, _ agent.Agent) (string, error) { return "2.0.0", nil }

	configpaths.WithMockConfigPaths(t)
	if err := saveUpgradeState(upgradeState{CurrentVersion: "2.0.0"}); err != nil {
		t.Fatalf("saveUpgradeState: %v", err)
	}
	opts := options.RunOptions{}
	ui := &termio.Mock{}

	got, upgraded, err := resolveBuildVersion(context.Background(), opencodeAgent(t), ui, opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "2.0.0" {
		t.Errorf("expected current version 2.0.0, got %q", got)
	}
	if upgraded {
		t.Error("expected upgraded=false when no newer version exists")
	}
}

func TestResolveOpenCodeVersionNonInteractiveLogsUpgradeAvailable(t *testing.T) {
	origLatest := agentLatestVersion
	defer func() { agentLatestVersion = origLatest }()
	agentLatestVersion = func(_ context.Context, _ agent.Agent) (string, error) { return "2.0.0", nil }

	configpaths.WithMockConfigPaths(t)
	if err := saveUpgradeState(upgradeState{CurrentVersion: "1.0.0"}); err != nil {
		t.Fatalf("saveUpgradeState: %v", err)
	}
	opts := options.RunOptions{}
	ui := &termio.Mock{IsInteractiveResult: false}

	got, upgraded, err := resolveBuildVersion(context.Background(), opencodeAgent(t), ui, opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "1.0.0" {
		t.Errorf("expected current version 1.0.0, got %q", got)
	}
	if upgraded {
		t.Error("expected upgraded=false in a non-interactive session")
	}
	wantInfo := "opencode 2.0.0 available (image has 1.0.0); run 'agents-sandbox build' to upgrade"
	if !slices.Contains(ui.InfoCalls, wantInfo) {
		t.Errorf("expected info %q in output, got %v", wantInfo, ui.InfoCalls)
	}
}

func TestResolveOpenCodeVersionInteractiveRebuild(t *testing.T) {
	origLatest := agentLatestVersion
	defer func() { agentLatestVersion = origLatest }()
	agentLatestVersion = func(_ context.Context, _ agent.Agent) (string, error) { return "2.0.0", nil }

	configpaths.WithMockConfigPaths(t)
	if err := saveUpgradeState(upgradeState{CurrentVersion: "1.0.0"}); err != nil {
		t.Fatalf("saveUpgradeState: %v", err)
	}
	opts := options.RunOptions{}
	ui := &termio.Mock{
		IsInteractiveResult: true,
		SelectFn: func(_ string, _ []termio.Choice, _ string) (string, error) {
			return "r", nil
		},
	}

	got, upgraded, err := resolveBuildVersion(context.Background(), opencodeAgent(t), ui, opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "2.0.0" {
		t.Errorf("expected new version 2.0.0, got %q", got)
	}
	if !upgraded {
		t.Error("expected upgraded=true after choosing to rebuild")
	}
}

func TestResolveOpenCodeVersionSkipsCheckWhenCheckedToday(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	if err := saveUpgradeState(upgradeState{CurrentVersion: "1.0.0", LastChecked: now()}); err != nil {
		t.Fatalf("saveUpgradeState: %v", err)
	}

	origLatest := agentLatestVersion
	defer func() { agentLatestVersion = origLatest }()
	latestCalled := false
	agentLatestVersion = func(_ context.Context, _ agent.Agent) (string, error) {
		latestCalled = true
		return "2.0.0", nil
	}

	opts := options.RunOptions{}
	ui := &termio.Mock{IsInteractiveResult: true}

	got, upgraded, err := resolveBuildVersion(context.Background(), opencodeAgent(t), ui, opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "1.0.0" {
		t.Errorf("expected current version 1.0.0, got %q", got)
	}
	if upgraded {
		t.Error("expected upgraded=false when already checked today")
	}
	if latestCalled {
		t.Error("agentLatestVersion should NOT have been called when already checked today")
	}
}

func TestResolveOpenCodeVersionDoesNotReOfferVersion(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	if err := saveUpgradeState(upgradeState{
		CurrentVersion:  "1.0.0",
		LastChecked:     now().Add(-25 * time.Hour),
		OfferedVersions: []string{"2.0.0"},
	}); err != nil {
		t.Fatalf("saveUpgradeState: %v", err)
	}

	origLatest := agentLatestVersion
	defer func() { agentLatestVersion = origLatest }()
	agentLatestVersion = func(_ context.Context, _ agent.Agent) (string, error) { return "2.0.0", nil }

	opts := options.RunOptions{}
	ui := &termio.Mock{IsInteractiveResult: true}

	got, upgraded, err := resolveBuildVersion(context.Background(), opencodeAgent(t), ui, opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "1.0.0" {
		t.Errorf("expected current version 1.0.0, got %q", got)
	}
	if upgraded {
		t.Error("expected upgraded=false for an already-offered version")
	}
}

func TestResolveOpenCodeVersionRecordsOfferedBeforePrompt(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	if err := saveUpgradeState(upgradeState{CurrentVersion: "1.0.0"}); err != nil {
		t.Fatalf("saveUpgradeState: %v", err)
	}

	origLatest := agentLatestVersion
	defer func() { agentLatestVersion = origLatest }()
	agentLatestVersion = func(_ context.Context, _ agent.Agent) (string, error) { return "2.0.0", nil }

	opts := options.RunOptions{}
	ui := &termio.Mock{IsInteractiveResult: true}

	if _, _, err := resolveBuildVersion(context.Background(), opencodeAgent(t), ui, opts); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The prompt was shown (mock default returns "r" → Rebuild), so 2.0.0 must
	// now be recorded as offered, and the timestamp updated.
	st, err := loadUpgradeState()
	if err != nil {
		t.Fatalf("loadUpgradeState: %v", err)
	}
	oc := st.Agents["opencode"]
	if !oc.offered("2.0.0") {
		t.Error("expected 2.0.0 to be recorded as offered after the prompt")
	}
	if oc.LastChecked.IsZero() {
		t.Error("expected LastChecked to be refreshed after a successful check")
	}
}

func TestResolveOpenCodeVersionUnparsableLatestKeepsCurrent(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	if err := saveUpgradeState(upgradeState{CurrentVersion: "1.0.0"}); err != nil {
		t.Fatalf("saveUpgradeState: %v", err)
	}

	origLatest := agentLatestVersion
	defer func() { agentLatestVersion = origLatest }()
	// An unparsable latest version must never fail the session or offer an upgrade.
	agentLatestVersion = func(_ context.Context, _ agent.Agent) (string, error) { return "not-a-version", nil }

	opts := options.RunOptions{}
	ui := &termio.Mock{IsInteractiveResult: true}

	got, upgraded, err := resolveBuildVersion(context.Background(), opencodeAgent(t), ui, opts)
	if err != nil {
		t.Fatalf("unparsable latest version must not fail the session, got: %v", err)
	}
	if got != "1.0.0" {
		t.Errorf("expected current version 1.0.0, got %q", got)
	}
	if upgraded {
		t.Error("expected upgraded=false when the latest version is unparsable")
	}
}

func TestResolveOpenCodeVersionOfflineDoesNotUpdateLastChecked(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	if err := saveUpgradeState(upgradeState{CurrentVersion: "1.0.0"}); err != nil {
		t.Fatalf("saveUpgradeState: %v", err)
	}

	origLatest := agentLatestVersion
	defer func() { agentLatestVersion = origLatest }()
	agentLatestVersion = func(_ context.Context, _ agent.Agent) (string, error) {
		return "", errors.New("network unreachable")
	}

	opts := options.RunOptions{}
	ui := &termio.Mock{IsInteractiveResult: true}

	got, upgraded, err := resolveBuildVersion(context.Background(), opencodeAgent(t), ui, opts)
	if err != nil {
		t.Fatalf("offline check must not fail the session, got: %v", err)
	}
	if got != "1.0.0" {
		t.Errorf("expected current version 1.0.0 when offline, got %q", got)
	}
	if upgraded {
		t.Error("expected upgraded=false when offline")
	}
	if len(ui.WarnCalls) == 0 {
		t.Error("expected a warning about the failed update check")
	}

	// The failed (offline) check must NOT refresh LastChecked, so the next
	// online run within the same day still retries.
	st, err := loadUpgradeState()
	if err != nil {
		t.Fatalf("loadUpgradeState: %v", err)
	}
	if !st.Agents["opencode"].LastChecked.IsZero() {
		t.Errorf("expected LastChecked to remain zero after offline check, got %v", st.Agents["opencode"].LastChecked)
	}
}

func TestResolveOpenCodeVersionInteractiveQuit(t *testing.T) {
	origLatest := agentLatestVersion
	defer func() { agentLatestVersion = origLatest }()
	agentLatestVersion = func(_ context.Context, _ agent.Agent) (string, error) { return "2.0.0", nil }

	configpaths.WithMockConfigPaths(t)
	if err := saveUpgradeState(upgradeState{CurrentVersion: "1.0.0"}); err != nil {
		t.Fatalf("saveUpgradeState: %v", err)
	}
	opts := options.RunOptions{}
	ui := &termio.Mock{
		IsInteractiveResult: true,
		SelectFn: func(_ string, _ []termio.Choice, _ string) (string, error) {
			return "q", nil
		},
	}

	_, _, err := resolveBuildVersion(context.Background(), opencodeAgent(t), ui, opts)
	if !errors.Is(err, errUpgradeQuit) {
		t.Fatalf("expected errUpgradeQuit, got %v", err)
	}
}
