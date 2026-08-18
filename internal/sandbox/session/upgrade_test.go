package session

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"
	"time"

	"gitlab.inoio.de/inoio/opencode-sandbox/internal/configpaths"
	"gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/image"
	"gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/options"
	"gitlab.inoio.de/inoio/opencode-sandbox/internal/termio"
)

func TestMaybePromptSkipsWhenRebuildFlagSet(t *testing.T) {
	origRebuild := rebuildImageForUpgrade
	origLatest := openCodeUpgradeInfo
	defer func() {
		rebuildImageForUpgrade = origRebuild
		openCodeUpgradeInfo = origLatest
	}()

	rebuildCalled := false
	rebuildImageForUpgrade = func(_ context.Context, _ termio.UI, _ options.RunOptions) (image.ImageInfo, error) {
		rebuildCalled = true
		return image.ImageInfo{}, nil
	}

	latestCalled := false
	openCodeUpgradeInfo = func(_ context.Context) (string, error) {
		latestCalled = true
		return "2.0.0", nil
	}

	configpaths.WithMockConfigPaths(t)

	info := image.ImageInfo{Tag: "opencode-sandbox:run", OpenCodeVersion: "1.0.0"}
	opts := options.RunOptions{Rebuild: true}
	ui := &termio.Mock{}

	gotInfo, gotAction, err := maybePromptOpenCodeUpgrade(context.Background(), ui, opts, info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !reflect.DeepEqual(gotInfo, info) {
		t.Errorf("expected unchanged info, got %+v", gotInfo)
	}
	if gotAction != upgradeActionNone {
		t.Errorf("expected upgradeActionNone, got %d", gotAction)
	}
	if rebuildCalled {
		t.Error("rebuildImageForUpgrade should NOT have been called when Rebuild is set")
	}
	if latestCalled {
		t.Error("openCodeUpgradeInfo should NOT have been called when Rebuild is set")
	}
}

func TestMaybePromptForcesRebuildWhenNoVersionLabel(t *testing.T) {
	origRebuild := rebuildImageForUpgrade
	defer func() { rebuildImageForUpgrade = origRebuild }()

	expectedRebuilt := image.ImageInfo{
		Tag:             "opencode-sandbox:rebuilt",
		Digest:          "sha256:abc",
		OpenCodeVersion: "9.9.9",
		Env:             map[string]string{"FOO": "bar"},
	}
	rebuildImageForUpgrade = func(_ context.Context, _ termio.UI, _ options.RunOptions) (image.ImageInfo, error) {
		return expectedRebuilt, nil
	}

	configpaths.WithMockConfigPaths(t)

	info := image.ImageInfo{Tag: "opencode-sandbox:old", Digest: "sha256:old", OpenCodeVersion: ""}
	opts := options.RunOptions{}
	ui := &termio.Mock{}

	gotInfo, gotAction, err := maybePromptOpenCodeUpgrade(context.Background(), ui, opts, info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotInfo.OpenCodeVersion != "9.9.9" {
		t.Errorf("expected OpenCodeVersion 9.9.9, got %q", gotInfo.OpenCodeVersion)
	}
	if gotInfo.Tag != "opencode-sandbox:rebuilt" {
		t.Errorf("expected Tag opencode-sandbox:rebuilt, got %q", gotInfo.Tag)
	}
	if gotAction != upgradeActionRebuild {
		t.Errorf("expected upgradeActionRebuild, got %d", gotAction)
	}
}

func TestMaybePromptWarnsAndKeepsWhenForceRebuildFails(t *testing.T) {
	origRebuild := rebuildImageForUpgrade
	defer func() { rebuildImageForUpgrade = origRebuild }()

	rebuildImageForUpgrade = func(_ context.Context, _ termio.UI, _ options.RunOptions) (image.ImageInfo, error) {
		return image.ImageInfo{}, errors.New("build failed")
	}

	configpaths.WithMockConfigPaths(t)

	info := image.ImageInfo{Tag: "opencode-sandbox:old", OpenCodeVersion: ""}
	opts := options.RunOptions{}
	ui := &termio.Mock{}

	gotInfo, gotAction, err := maybePromptOpenCodeUpgrade(context.Background(), ui, opts, info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotAction != upgradeActionNone {
		t.Errorf("expected upgradeActionNone on rebuild failure, got %d", gotAction)
	}
	if !reflect.DeepEqual(gotInfo, info) {
		t.Errorf("expected unchanged info on rebuild failure, got %+v", gotInfo)
	}
	if len(ui.WarnCalls) == 0 {
		t.Error("expected a warning about failed rebuild, got no warnings")
	}
}

func TestMaybePromptKeepsWhenNoNewerVersion(t *testing.T) {
	origLatest := openCodeUpgradeInfo
	defer func() { openCodeUpgradeInfo = origLatest }()

	openCodeUpgradeInfo = func(_ context.Context) (string, error) {
		return "2.0.0", nil
	}

	configpaths.WithMockConfigPaths(t)

	info := image.ImageInfo{Tag: "opencode-sandbox:run", OpenCodeVersion: "2.0.0"}
	opts := options.RunOptions{}
	ui := &termio.Mock{}

	gotInfo, gotAction, err := maybePromptOpenCodeUpgrade(context.Background(), ui, opts, info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotAction != upgradeActionNone {
		t.Errorf("expected upgradeActionNone, got %d", gotAction)
	}
	if !reflect.DeepEqual(gotInfo, info) {
		t.Errorf("expected unchanged info, got %+v", gotInfo)
	}
}

func TestMaybePromptNonInteractiveLogsUpgradeAvailable(t *testing.T) {
	origLatest := openCodeUpgradeInfo
	defer func() { openCodeUpgradeInfo = origLatest }()

	openCodeUpgradeInfo = func(_ context.Context) (string, error) {
		return "2.0.0", nil
	}

	configpaths.WithMockConfigPaths(t)

	info := image.ImageInfo{Tag: "opencode-sandbox:run", OpenCodeVersion: "1.0.0"}
	opts := options.RunOptions{}
	ui := &termio.Mock{IsInteractiveResult: false}

	gotInfo, gotAction, err := maybePromptOpenCodeUpgrade(context.Background(), ui, opts, info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotAction != upgradeActionNone {
		t.Errorf("expected upgradeActionNone, got %d", gotAction)
	}
	if !reflect.DeepEqual(gotInfo, info) {
		t.Errorf("expected unchanged info, got %+v", gotInfo)
	}
	wantInfo := "opencode 2.0.0 available (image has 1.0.0); run 'opencode-sandbox build' to upgrade"
	if !slices.Contains(ui.InfoCalls, wantInfo) {
		t.Errorf("expected info %q in output, got %v", wantInfo, ui.InfoCalls)
	}
}

func TestMaybePromptInteractiveRebuild(t *testing.T) {
	origRebuild := rebuildImageForUpgrade
	defer func() { rebuildImageForUpgrade = origRebuild }()

	configpaths.WithMockConfigPaths(t)

	rebuilt := image.ImageInfo{Tag: "opencode-sandbox:new", OpenCodeVersion: "2.0.0"}
	rebuildImageForUpgrade = func(_ context.Context, _ termio.UI, _ options.RunOptions) (image.ImageInfo, error) {
		return rebuilt, nil
	}

	openCodeUpgradeInfo = func(_ context.Context) (string, error) {
		return "2.0.0", nil
	}

	info := image.ImageInfo{Tag: "opencode-sandbox:run", OpenCodeVersion: "1.0.0"}
	opts := options.RunOptions{}
	ui := &termio.Mock{
		IsInteractiveResult: true,
		SelectFn: func(_ string, _ []termio.Choice, _ string) (string, error) {
			return "r", nil
		},
	}

	gotInfo, gotAction, err := maybePromptOpenCodeUpgrade(context.Background(), ui, opts, info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotAction != upgradeActionRebuild {
		t.Errorf("expected upgradeActionRebuild, got %d", gotAction)
	}
	if gotInfo.OpenCodeVersion != "2.0.0" {
		t.Errorf("expected OpenCodeVersion 2.0.0, got %q", gotInfo.OpenCodeVersion)
	}
}

func TestMaybePromptSkipsCheckWhenCheckedToday(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	origLatest := openCodeUpgradeInfo
	defer func() { openCodeUpgradeInfo = origLatest }()
	latestCalled := false
	openCodeUpgradeInfo = func(_ context.Context) (string, error) {
		latestCalled = true
		return "2.0.0", nil
	}

	// A successful check happened just now, so the once-per-day gate is closed.
	if err := saveUpgradeState(upgradeState{LastChecked: now()}); err != nil {
		t.Fatalf("saveUpgradeState: %v", err)
	}

	info := image.ImageInfo{Tag: "opencode-sandbox:run", OpenCodeVersion: "1.0.0"}
	opts := options.RunOptions{}
	ui := &termio.Mock{IsInteractiveResult: true}

	gotInfo, gotAction, err := maybePromptOpenCodeUpgrade(context.Background(), ui, opts, info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotAction != upgradeActionNone {
		t.Errorf("expected upgradeActionNone, got %d", gotAction)
	}
	if !reflect.DeepEqual(gotInfo, info) {
		t.Errorf("expected unchanged info, got %+v", gotInfo)
	}
	if latestCalled {
		t.Error("openCodeUpgradeInfo should NOT have been called when already checked today")
	}
}

func TestMaybePromptDoesNotReOfferVersion(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	origLatest := openCodeUpgradeInfo
	defer func() { openCodeUpgradeInfo = origLatest }()
	openCodeUpgradeInfo = func(_ context.Context) (string, error) {
		return "2.0.0", nil
	}

	// The same version was already offered (and its prompt shown) before.
	if err := saveUpgradeState(upgradeState{
		LastChecked:     now().Add(-25 * time.Hour),
		OfferedVersions: []string{"2.0.0"},
	}); err != nil {
		t.Fatalf("saveUpgradeState: %v", err)
	}

	info := image.ImageInfo{Tag: "opencode-sandbox:run", OpenCodeVersion: "1.0.0"}
	opts := options.RunOptions{}
	ui := &termio.Mock{IsInteractiveResult: true}

	gotInfo, gotAction, err := maybePromptOpenCodeUpgrade(context.Background(), ui, opts, info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotAction != upgradeActionNone {
		t.Errorf("expected upgradeActionNone for already-offered version, got %d", gotAction)
	}
	if !reflect.DeepEqual(gotInfo, info) {
		t.Errorf("expected unchanged info, got %+v", gotInfo)
	}
}

func TestMaybePromptRecordsOfferedVersionBeforePrompt(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	origLatest := openCodeUpgradeInfo
	origRebuild := rebuildImageForUpgrade
	defer func() {
		openCodeUpgradeInfo = origLatest
		rebuildImageForUpgrade = origRebuild
	}()
	openCodeUpgradeInfo = func(_ context.Context) (string, error) {
		return "2.0.0", nil
	}
	rebuildImageForUpgrade = func(_ context.Context, _ termio.UI, _ options.RunOptions) (image.ImageInfo, error) {
		return image.ImageInfo{OpenCodeVersion: "2.0.0"}, nil
	}

	info := image.ImageInfo{Tag: "opencode-sandbox:run", OpenCodeVersion: "1.0.0"}
	opts := options.RunOptions{}
	ui := &termio.Mock{IsInteractiveResult: true}

	_, _, err := maybePromptOpenCodeUpgrade(context.Background(), ui, opts, info)
	if err != nil {
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

func TestMaybePromptOfflineDoesNotUpdateLastChecked(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	origLatest := openCodeUpgradeInfo
	defer func() { openCodeUpgradeInfo = origLatest }()
	openCodeUpgradeInfo = func(_ context.Context) (string, error) {
		return "", errors.New("network unreachable")
	}

	info := image.ImageInfo{Tag: "opencode-sandbox:run", OpenCodeVersion: "1.0.0"}
	opts := options.RunOptions{}
	ui := &termio.Mock{IsInteractiveResult: true}

	gotInfo, gotAction, err := maybePromptOpenCodeUpgrade(context.Background(), ui, opts, info)
	if err != nil {
		t.Fatalf("offline check must not fail the session, got: %v", err)
	}
	if gotAction != upgradeActionNone {
		t.Errorf("expected upgradeActionNone when offline, got %d", gotAction)
	}
	if !reflect.DeepEqual(gotInfo, info) {
		t.Errorf("expected unchanged info when offline, got %+v", gotInfo)
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

func TestMaybePromptInteractiveQuit(t *testing.T) {
	origRebuild := rebuildImageForUpgrade
	origLatest := openCodeUpgradeInfo
	defer func() {
		rebuildImageForUpgrade = origRebuild
		openCodeUpgradeInfo = origLatest
	}()

	configpaths.WithMockConfigPaths(t)

	rebuildImageForUpgrade = func(_ context.Context, _ termio.UI, _ options.RunOptions) (image.ImageInfo, error) {
		return image.ImageInfo{}, nil
	}
	openCodeUpgradeInfo = func(_ context.Context) (string, error) {
		return "2.0.0", nil
	}

	info := image.ImageInfo{Tag: "opencode-sandbox:run", OpenCodeVersion: "1.0.0"}
	opts := options.RunOptions{}
	ui := &termio.Mock{
		IsInteractiveResult: true,
		SelectFn: func(_ string, _ []termio.Choice, _ string) (string, error) {
			return "q", nil
		},
	}

	_, _, err := maybePromptOpenCodeUpgrade(context.Background(), ui, opts, info)
	if !errors.Is(err, errUpgradeQuit) {
		t.Fatalf("expected errUpgradeQuit, got %v", err)
	}
}
