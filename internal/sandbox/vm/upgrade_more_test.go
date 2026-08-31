package vm

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/inoio/opencode-sandbox/internal/configpaths"
	"github.com/inoio/opencode-sandbox/internal/sandbox/options"
	"github.com/inoio/opencode-sandbox/internal/termio"
)

// TestResolveOpenCodeVersionInteractiveSelectError covers the Select failure
// branch of resolveBuildVersion: an interactive prompt whose selection
// errors is propagated.
func TestResolveOpenCodeVersionInteractiveSelectError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	if err := saveUpgradeState(upgradeState{CurrentVersion: "1.0.0"}); err != nil {
		t.Fatalf("saveUpgradeState: %v", err)
	}
	origLatest := openCodeUpgradeInfo
	defer func() { openCodeUpgradeInfo = origLatest }()
	openCodeUpgradeInfo = func(_ context.Context) (string, error) { return "2.0.0", nil }

	ui := &termio.Mock{
		IsInteractiveResult: true,
		SelectFn: func(_ string, _ []termio.Choice, _ string) (string, error) {
			return "", errors.New("selection failed")
		},
	}

	_, _, err := resolveBuildVersion(context.Background(), opencodeAgent(t), ui, options.RunOptions{})
	if err == nil {
		t.Fatal("expected error when the interactive selection fails")
	}
}

// TestResolveOpenCodeVersionInteractiveDefaultChoice covers the
// default/unknown-choice branch: an unrecognized selection keeps the current
// version.
func TestResolveOpenCodeVersionInteractiveDefaultChoice(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	if err := saveUpgradeState(upgradeState{CurrentVersion: "1.0.0"}); err != nil {
		t.Fatalf("saveUpgradeState: %v", err)
	}
	origLatest := openCodeUpgradeInfo
	defer func() { openCodeUpgradeInfo = origLatest }()
	openCodeUpgradeInfo = func(_ context.Context) (string, error) { return "2.0.0", nil }

	ui := &termio.Mock{
		IsInteractiveResult: true,
		SelectFn: func(_ string, _ []termio.Choice, _ string) (string, error) {
			return "unknown", nil
		},
	}

	got, upgraded, err := resolveBuildVersion(context.Background(), opencodeAgent(t), ui, options.RunOptions{})
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
	if err := saveUpgradeState(upgradeState{CurrentVersion: "1.0.0"}); err != nil {
		t.Fatalf("saveUpgradeState: %v", err)
	}
	origLatest := openCodeUpgradeInfo
	defer func() { openCodeUpgradeInfo = origLatest }()
	openCodeUpgradeInfo = func(_ context.Context) (string, error) { return "2.0.0", nil }

	ui := &termio.Mock{
		IsInteractiveResult: true,
		SelectFn: func(_ string, _ []termio.Choice, _ string) (string, error) {
			return "k", nil
		},
	}

	got, upgraded, err := resolveBuildVersion(context.Background(), opencodeAgent(t), ui, options.RunOptions{})
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

// TestSaveUpgradeStateWriteError covers the WriteFile failure branch of
// saveUpgradeState. The state directory is made read-only so creating the
// temp file fails after MkdirAll succeeds.
func TestSaveUpgradeStateWriteError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	dir := configpaths.Get().UserStateDir()
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Skipf("cannot chmod state dir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	if err := saveUpgradeState(upgradeState{CurrentVersion: "1.0.0"}); err == nil {
		t.Error("expected error when writing the updater state file fails")
	}
}
