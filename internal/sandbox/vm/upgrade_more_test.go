package vm

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/inoio/opencode-sandbox/internal/configpaths"
	"github.com/inoio/opencode-sandbox/internal/sandbox/image"
	"github.com/inoio/opencode-sandbox/internal/termio"
)

// TestMaybePromptInteractiveSelectError covers the Select failure branch of
// maybePromptOpenCodeUpgrade: an interactive prompt whose selection errors is
// propagated.
func TestMaybePromptInteractiveSelectError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	origLatest := openCodeUpgradeInfo
	defer func() { openCodeUpgradeInfo = origLatest }()
	openCodeUpgradeInfo = func(_ context.Context) (string, error) { return "2.0.0", nil }

	info := image.ImageInfo{Tag: "opencode-sandbox:run", OpenCodeVersion: "1.0.0"}
	ui := &termio.Mock{
		IsInteractiveResult: true,
		SelectFn: func(_ string, _ []termio.Choice, _ string) (string, error) {
			return "", errors.New("selection failed")
		},
	}

	_, _, err := maybePromptOpenCodeUpgrade(context.Background(), ui, false, "", info)
	if err == nil {
		t.Fatal("expected error when the interactive selection fails")
	}
}

// TestMaybePromptInteractiveRebuildError covers the rebuild-failure branch in
// the interactive path: the user chooses Rebuild but the rebuild fails.
func TestMaybePromptInteractiveRebuildError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	origLatest := openCodeUpgradeInfo
	origRebuild := rebuildImageForUpgrade
	defer func() {
		openCodeUpgradeInfo = origLatest
		rebuildImageForUpgrade = origRebuild
	}()
	openCodeUpgradeInfo = func(_ context.Context) (string, error) { return "2.0.0", nil }
	rebuildImageForUpgrade = func(_ context.Context, _ termio.UI, _ string) (image.ImageInfo, error) {
		return image.ImageInfo{}, errors.New("rebuild failed")
	}

	info := image.ImageInfo{Tag: "opencode-sandbox:run", OpenCodeVersion: "1.0.0"}
	ui := &termio.Mock{
		IsInteractiveResult: true,
		SelectFn: func(_ string, _ []termio.Choice, _ string) (string, error) {
			return "r", nil
		},
	}

	_, _, err := maybePromptOpenCodeUpgrade(context.Background(), ui, false, "", info)
	if err == nil {
		t.Fatal("expected error when the interactive rebuild fails")
	}
}

// TestMaybePromptInteractiveDefaultChoice covers the default/unknown-choice
// branch of maybePromptOpenCodeUpgrade: an unrecognized selection keeps the
// current image.
func TestMaybePromptInteractiveDefaultChoice(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	origLatest := openCodeUpgradeInfo
	defer func() { openCodeUpgradeInfo = origLatest }()
	openCodeUpgradeInfo = func(_ context.Context) (string, error) { return "2.0.0", nil }

	info := image.ImageInfo{Tag: "opencode-sandbox:run", OpenCodeVersion: "1.0.0"}
	ui := &termio.Mock{
		IsInteractiveResult: true,
		SelectFn: func(_ string, _ []termio.Choice, _ string) (string, error) {
			return "unknown", nil
		},
	}

	gotInfo, gotAction, err := maybePromptOpenCodeUpgrade(context.Background(), ui, false, "", info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotAction != upgradeActionNone {
		t.Errorf("expected upgradeActionNone for an unknown choice, got %d", gotAction)
	}
	if gotInfo.OpenCodeVersion != info.OpenCodeVersion {
		t.Errorf("expected unchanged info for an unknown choice")
	}
}

// TestMaybePromptInteractiveKeep covers the "keep" choice branch of
// maybePromptOpenCodeUpgrade: the current image is kept.
func TestMaybePromptInteractiveKeep(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	origLatest := openCodeUpgradeInfo
	defer func() { openCodeUpgradeInfo = origLatest }()
	openCodeUpgradeInfo = func(_ context.Context) (string, error) { return "2.0.0", nil }

	info := image.ImageInfo{Tag: "opencode-sandbox:run", OpenCodeVersion: "1.0.0"}
	ui := &termio.Mock{
		IsInteractiveResult: true,
		SelectFn: func(_ string, _ []termio.Choice, _ string) (string, error) {
			return "k", nil
		},
	}

	gotInfo, gotAction, err := maybePromptOpenCodeUpgrade(context.Background(), ui, false, "", info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotAction != upgradeActionNone {
		t.Errorf("expected upgradeActionNone for the keep choice, got %d", gotAction)
	}
	if gotInfo.OpenCodeVersion != info.OpenCodeVersion {
		t.Errorf("expected unchanged info for the keep choice")
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
