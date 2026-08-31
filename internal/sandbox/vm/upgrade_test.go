package vm

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/inoio/opencode-sandbox/internal/configpaths"
	"github.com/inoio/opencode-sandbox/internal/sandbox/options"
	"github.com/inoio/opencode-sandbox/internal/termio"
)

func TestResolveOpenCodeVersionPinnedSkipsUpdateCheck(t *testing.T) {
	origLatest := openCodeUpgradeInfo
	defer func() { openCodeUpgradeInfo = origLatest }()
	latestCalled := false
	openCodeUpgradeInfo = func(_ context.Context) (string, error) {
		latestCalled = true
		return "2.0.0", nil
	}

	configpaths.WithMockConfigPaths(t)
	opts := options.RunOptions{OpenCodeVersion: "2.0.0"}
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
		t.Error("openCodeUpgradeInfo should NOT have been called when the version is pinned")
	}
}

func TestResolveOpenCodeVersionRebuildSkipsUpdateCheck(t *testing.T) {
	origLatest := openCodeUpgradeInfo
	defer func() { openCodeUpgradeInfo = origLatest }()
	latestCalled := false
	openCodeUpgradeInfo = func(_ context.Context) (string, error) {
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
		t.Error("openCodeUpgradeInfo should NOT have been called when Rebuild is set")
	}
}

func TestResolveOpenCodeVersionNoBaseline(t *testing.T) {
	origLatest := openCodeUpgradeInfo
	defer func() { openCodeUpgradeInfo = origLatest }()
	latestCalled := false
	openCodeUpgradeInfo = func(_ context.Context) (string, error) {
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
		t.Error("openCodeUpgradeInfo should NOT have been called without a baseline")
	}
}

func TestResolveOpenCodeVersionNoNewerVersion(t *testing.T) {
	origLatest := openCodeUpgradeInfo
	defer func() { openCodeUpgradeInfo = origLatest }()
	openCodeUpgradeInfo = func(_ context.Context) (string, error) { return "2.0.0", nil }

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
	origLatest := openCodeUpgradeInfo
	defer func() { openCodeUpgradeInfo = origLatest }()
	openCodeUpgradeInfo = func(_ context.Context) (string, error) { return "2.0.0", nil }

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
	wantInfo := "opencode 2.0.0 available (image has 1.0.0); run 'opencode-sandbox build' to upgrade"
	if !slices.Contains(ui.InfoCalls, wantInfo) {
		t.Errorf("expected info %q in output, got %v", wantInfo, ui.InfoCalls)
	}
}

func TestResolveOpenCodeVersionInteractiveRebuild(t *testing.T) {
	origLatest := openCodeUpgradeInfo
	defer func() { openCodeUpgradeInfo = origLatest }()
	openCodeUpgradeInfo = func(_ context.Context) (string, error) { return "2.0.0", nil }

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

	origLatest := openCodeUpgradeInfo
	defer func() { openCodeUpgradeInfo = origLatest }()
	latestCalled := false
	openCodeUpgradeInfo = func(_ context.Context) (string, error) {
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
		t.Error("openCodeUpgradeInfo should NOT have been called when already checked today")
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

	origLatest := openCodeUpgradeInfo
	defer func() { openCodeUpgradeInfo = origLatest }()
	openCodeUpgradeInfo = func(_ context.Context) (string, error) { return "2.0.0", nil }

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

	origLatest := openCodeUpgradeInfo
	defer func() { openCodeUpgradeInfo = origLatest }()
	openCodeUpgradeInfo = func(_ context.Context) (string, error) { return "2.0.0", nil }

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
	if !st.offered("2.0.0") {
		t.Error("expected 2.0.0 to be recorded as offered after the prompt")
	}
	if st.LastChecked.IsZero() {
		t.Error("expected LastChecked to be refreshed after a successful check")
	}
}

func TestResolveOpenCodeVersionOfflineDoesNotUpdateLastChecked(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	if err := saveUpgradeState(upgradeState{CurrentVersion: "1.0.0"}); err != nil {
		t.Fatalf("saveUpgradeState: %v", err)
	}

	origLatest := openCodeUpgradeInfo
	defer func() { openCodeUpgradeInfo = origLatest }()
	openCodeUpgradeInfo = func(_ context.Context) (string, error) {
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
	if !st.LastChecked.IsZero() {
		t.Errorf("expected LastChecked to remain zero after offline check, got %v", st.LastChecked)
	}
}

func TestResolveOpenCodeVersionInteractiveQuit(t *testing.T) {
	origLatest := openCodeUpgradeInfo
	defer func() { openCodeUpgradeInfo = origLatest }()
	openCodeUpgradeInfo = func(_ context.Context) (string, error) { return "2.0.0", nil }

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
