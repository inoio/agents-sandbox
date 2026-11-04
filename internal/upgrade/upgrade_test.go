package upgrade

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/termio"
)

func TestParseMode(t *testing.T) {
	tests := []struct {
		in      string
		want    Mode
		wantErr bool
	}{
		{in: "prompt", want: ModePrompt},
		{in: "notify", want: ModeNotify},
		{in: "auto", want: ModeAuto},
		{in: "auto-exit", want: ModeAutoExit},
		{in: "PROMPT", want: ModePrompt},
		{in: " bogus ", wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			got, err := ParseMode(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestCheckSkipsDevAndEmpty(t *testing.T) {
	serveLatest(t, "1.0.0", nil)
	for _, v := range []string{"", "dev"} {
		res, err := Check(context.Background(), mockOptions(t, v, ModeNotify))
		if err != nil {
			t.Fatalf("version %q: unexpected error: %v", v, err)
		}
		if res.HasUpdate {
			t.Fatalf("version %q: expected no update, got %+v", v, res)
		}
	}
}

func TestCheckSkipsHomebrewInstall(t *testing.T) {
	serveLatest(t, "2.0.0", nil)
	origHomebrew := InstalledViaHomebrew
	InstalledViaHomebrew = func() bool { return true }
	t.Cleanup(func() { InstalledViaHomebrew = origHomebrew })

	opts := mockOptions(t, "1.0.0", ModeAuto)
	opts.UpdateFunc = func(context.Context, string) error {
		t.Fatal("must not replace the binary of a Homebrew-managed install")
		return nil
	}
	res, err := Check(context.Background(), opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.HasUpdate || res.Updated || res.Exit {
		t.Fatalf("expected a complete no-op for a Homebrew install, got %+v", res)
	}
}

func TestCheckThrottled(t *testing.T) {
	// A failing server proves no network request happens when throttled.
	serveLatest(t, "9.9.9", nil)
	latestURL = "http://127.0.0.1:1/releases/latest" // force failure if hit
	ui := termio.Mock{}
	opts := mockOptions(t, "1.0.0", ModeNotify)
	opts.UI = &ui
	opts.Store = &fakeStore{state: LauncherState{LastChecked: time.Now()}}

	res, err := Check(context.Background(), opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.HasUpdate {
		t.Fatalf("expected no update, got %+v", res)
	}
	if len(ui.InfoCalls) != 0 {
		t.Fatalf("expected no notifications, got %v", ui.InfoCalls)
	}
}

func TestCheckNoUpdateSavesState(t *testing.T) {
	serveLatest(t, "1.0.0", nil)
	store := &fakeStore{}
	opts := mockOptions(t, "1.5.0", ModeNotify)
	opts.Store = store
	res, err := Check(context.Background(), opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.HasUpdate {
		t.Fatalf("expected no update, got %+v", res)
	}
	if store.state.LastChecked.IsZero() {
		t.Fatalf("expected the check time persisted, got %+v", store.state)
	}
}

func TestCheckStoreLoadErrorDefers(t *testing.T) {
	// A contended store means the state is unreachable; the check defers
	// silently instead of failing startup or clobbering state.
	serveLatest(t, "2.0.0", nil)
	ui := termio.Mock{}
	opts := mockOptions(t, "1.0.0", ModeNotify)
	opts.UI = &ui
	opts.Store = &fakeStore{err: errors.New("state locked")}

	res, err := Check(context.Background(), opts)
	if err != nil {
		t.Fatalf("expected the store failure to be tolerated, got %v", err)
	}
	if res.HasUpdate || res.Updated || res.Exit {
		t.Fatalf("expected an empty result, got %+v", res)
	}
	if len(ui.InfoCalls) != 0 {
		t.Fatalf("expected no notification, got %v", ui.InfoCalls)
	}
	if len(ui.VerboseCalls) == 0 {
		t.Fatal("expected a verbose note about the unusable store")
	}
}

func TestCheckStoreWriteErrorIsWarnedNotReturned(t *testing.T) {
	serveLatest(t, "2.0.0", nil)
	ui := termio.Mock{}
	opts := mockOptions(t, "1.0.0", ModeNotify)
	opts.UI = &ui
	store := &fakeStore{}
	opts.Store = &writeFailingStore{inner: store}

	res, err := Check(context.Background(), opts)
	if err != nil {
		t.Fatalf("expected the write failure to be swallowed, got %v", err)
	}
	if !res.HasUpdate {
		t.Fatalf("expected the notification to still happen, got %+v", res)
	}
	if len(ui.InfoCalls) == 0 {
		t.Fatal("expected a notification")
	}
	if len(ui.VerboseCalls) == 0 {
		t.Fatal("expected a verbose note about the failed write")
	}
}

func TestCheckNetworkErrorIsIgnored(t *testing.T) {
	latestURL = "http://127.0.0.1:1/releases/latest"
	orig := latestURL
	defer func() { latestURL = orig }()
	opts := mockOptions(t, "1.0.0", ModeNotify)
	res, err := Check(context.Background(), opts)
	if err != nil {
		t.Fatalf("expected no error on network failure, got %v", err)
	}
	if res.HasUpdate {
		t.Fatalf("expected no update, got %+v", res)
	}
}

func TestCheckNotify(t *testing.T) {
	serveLatest(t, "2.0.0", nil)
	ui := termio.Mock{}
	opts := mockOptions(t, "1.0.0", ModeNotify)
	opts.UI = &ui
	res, err := Check(context.Background(), opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.HasUpdate || res.Updated || res.Exit {
		t.Fatalf("unexpected result: %+v", res)
	}
	if len(ui.InfoCalls) == 0 {
		t.Fatal("expected a notification")
	}
}

func TestCheckCanaryOffersPrerelease(t *testing.T) {
	serveReleases(t, `[{"tag_name":"v1.9.0-canary.1","draft":false}]`)
	opts := mockOptions(t, "1.8.0", ModeNotify)
	opts.Channel = ChannelCanary
	res, err := Check(context.Background(), opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.HasUpdate || res.Latest != "1.9.0-canary.1" {
		t.Fatalf("expected a canary offer, got %+v", res)
	}
}

func TestCheckStableNeverOffersPrerelease(t *testing.T) {
	serveLatest(t, "1.8.0", nil)
	serveReleases(t, `[{"tag_name":"v1.9.0-canary.1","draft":false}]`)
	opts := mockOptions(t, "1.7.0", ModeNotify)
	opts.Channel = ChannelStable
	res, err := Check(context.Background(), opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.HasUpdate || res.Latest != "1.8.0" {
		t.Fatalf("stable must resolve only the latest release, got %+v", res)
	}
}

func TestCheckAutoUpgrade(t *testing.T) {
	serveLatest(t, "2.0.0", nil)
	ui := termio.Mock{}
	opts := mockOptions(t, "1.0.0", ModeAuto)
	opts.UI = &ui
	var updated string
	opts.UpdateFunc = func(_ context.Context, latest string) error {
		updated = latest
		return nil
	}
	res, err := Check(context.Background(), opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if updated != "2.0.0" || !res.Updated || res.Exit {
		t.Fatalf("unexpected result: %+v updated=%q", res, updated)
	}
}

func TestCheckAutoUpgradeExit(t *testing.T) {
	serveLatest(t, "2.0.0", nil)
	opts := mockOptions(t, "1.0.0", ModeAutoExit)
	opts.UpdateFunc = func(_ context.Context, _ string) error { return nil }
	res, err := Check(context.Background(), opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.Updated || !res.Exit {
		t.Fatalf("expected updated+exit, got %+v", res)
	}
}

func TestCheckPromptNonInteractiveFallsBackToNotify(t *testing.T) {
	serveLatest(t, "2.0.0", nil)
	ui := termio.Mock{}
	opts := mockOptions(t, "1.0.0", ModePrompt)
	opts.UI = &ui
	res, err := Check(context.Background(), opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Updated || res.Exit {
		t.Fatalf("expected no install, got %+v", res)
	}
	if len(ui.InfoCalls) == 0 {
		t.Fatal("expected fallback notification")
	}
}

func TestCheckPromptSelection(t *testing.T) {
	tests := []struct {
		name       string
		key        string
		wantUpdate bool
		wantExit   bool
	}{
		{name: "continue", key: "c"},
		{name: "upgrade", key: "u", wantUpdate: true},
		{name: "upgrade-exit", key: "x", wantUpdate: true, wantExit: true},
		{name: "skip", key: "s"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			serveLatest(t, "2.0.0", nil)
			ui := termio.Mock{IsInteractiveResult: true}
			ui.SelectFn = func(_ string, _ []termio.Choice, _ string) (string, error) {
				return tc.key, nil
			}
			opts := mockOptions(t, "1.0.0", ModePrompt)
			opts.UI = &ui
			opts.UpdateFunc = func(_ context.Context, _ string) error { return nil }
			res, err := Check(context.Background(), opts)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if res.Updated != tc.wantUpdate || res.Exit != tc.wantExit {
				t.Fatalf("key %q: got %+v, want update=%v exit=%v", tc.key, res, tc.wantUpdate, tc.wantExit)
			}
		})
	}
}

func TestCheckDismissedVersion(t *testing.T) {
	serveLatest(t, "2.0.0", nil)
	ui := termio.Mock{}
	opts := mockOptions(t, "1.0.0", ModeNotify)
	opts.UI = &ui
	opts.Store = &fakeStore{state: LauncherState{DismissedVersions: []string{"2.0.0"}}}
	res, err := Check(context.Background(), opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.HasUpdate {
		t.Fatalf("dismissed version should not notify, got %+v", res)
	}
	if len(ui.InfoCalls) != 0 {
		t.Fatalf("expected no notification, got %v", ui.InfoCalls)
	}
}

func TestCheckSubMinIntervalFails(t *testing.T) {
	opts := mockOptions(t, "1.0.0", ModeNotify)
	opts.Interval = time.Minute
	res, err := Check(context.Background(), opts)
	if err == nil {
		t.Fatalf("expected error for sub-minimum interval, got %+v", res)
	}
}

// A fold that cannot proceed is deferred, not stepped over: the process's view
// of the launcher state is incomplete, so checking now could offer a version the
// legacy file still dismisses. The check resumes once the fold succeeds.
func TestCheckFoldFailureIsDeferred(t *testing.T) {
	serveLatest(t, "2.0.0", nil)
	ui := termio.Mock{}
	opts := mockOptions(t, "1.0.0", ModeNotify)
	opts.UI = &ui

	// A directory at the legacy path makes the fold's read fail.
	if err := os.Mkdir(legacyStatePath(configpaths.Get()), 0o700); err != nil {
		t.Fatal(err)
	}

	res, err := Check(context.Background(), opts)
	if err != nil {
		t.Fatalf("a folded-state failure must not fail the check: %v", err)
	}
	if res.HasUpdate || res.Updated || res.Exit {
		t.Fatalf("expected the check to be deferred, got %+v", res)
	}
	if len(ui.InfoCalls) != 0 {
		t.Fatalf("expected no notification, got %v", ui.InfoCalls)
	}
	if len(ui.VerboseCalls) == 0 {
		t.Fatal("expected a verbose log for the deferred migration")
	}
}

func TestCheckUnparsableVersionIgnored(t *testing.T) {
	serveLatest(t, "2.0.0", nil)
	ui := termio.Mock{}
	opts := mockOptions(t, "not-a-version", ModeNotify)
	opts.UI = &ui
	res, err := Check(context.Background(), opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.HasUpdate {
		t.Fatalf("expected no update, got %+v", res)
	}
	if len(ui.VerboseCalls) == 0 {
		t.Fatal("expected a verbose log for the parse failure")
	}
}

func TestCheckAutoInstallFailureNotifies(t *testing.T) {
	serveLatest(t, "2.0.0", nil)
	ui := termio.Mock{}
	opts := mockOptions(t, "1.0.0", ModeAuto)
	opts.UI = &ui
	opts.UpdateFunc = func(context.Context, string) error { return errors.New("boom") }
	res, err := Check(context.Background(), opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Updated || res.Exit {
		t.Fatalf("expected no update on install failure, got %+v", res)
	}
	if len(ui.InfoCalls) == 0 {
		t.Fatal("expected fallback notification on install failure")
	}
}

func TestCheckPromptInstallFailure(t *testing.T) {
	serveLatest(t, "2.0.0", nil)
	ui := termio.Mock{IsInteractiveResult: true}
	ui.SelectFn = func(_ string, _ []termio.Choice, _ string) (string, error) { return "u", nil }
	opts := mockOptions(t, "1.0.0", ModePrompt)
	opts.UI = &ui
	opts.UpdateFunc = func(context.Context, string) error { return errors.New("boom") }
	res, err := Check(context.Background(), opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Updated || res.Exit {
		t.Fatalf("expected no update on install failure, got %+v", res)
	}
}

func TestCheckDevLeverOffersRelease(t *testing.T) {
	serveLatest(t, "2.0.0", nil)
	opts := mockOptions(t, "dev", ModeNotify)
	opts.AllowAnyVersion = true
	res, err := Check(context.Background(), opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.HasUpdate || res.Latest != "2.0.0" {
		t.Fatalf("expected the dev lever to offer 2.0.0, got %+v", res)
	}
}

func TestCheckDevLeverShortensInterval(t *testing.T) {
	serveLatest(t, "2.0.0", nil)
	opts := mockOptions(t, "dev", ModeNotify)
	opts.AllowAnyVersion = true
	opts.Interval = DefaultInterval
	// Checked two hours ago: within the default window, outside the capped one.
	opts.Store = &fakeStore{state: LauncherState{LastChecked: time.Now().Add(-2 * time.Hour)}}
	res, err := Check(context.Background(), opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.HasUpdate {
		t.Fatalf("the dev lever must cap the interval at MinInterval, got %+v", res)
	}

	// Negative baseline: the same stored timestamp with the lever off sits
	// inside DefaultInterval, so no update is offered. A parseable current
	// version keeps this off the dev-build no-op path, so it is the throttle
	// (not the absence of a newer release) that suppresses the update.
	opts.AllowAnyVersion = false
	opts.CurrentVersion = "1.0.0"
	opts.Store = &fakeStore{state: LauncherState{LastChecked: time.Now().Add(-2 * time.Hour)}}
	baseline, err := Check(context.Background(), opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if baseline.HasUpdate {
		t.Fatalf("without the dev lever the default interval must throttle the check, got %+v", baseline)
	}
}

func TestCheckDefaultsIntervalAndStore(t *testing.T) {
	// A zero Interval and a nil Store exercise the default-resolution branches
	// of Check: the interval falls back to DefaultInterval and the check runs
	// against the in-memory store, writing no host state.
	configpaths.WithMockConfigPaths(t)
	serveLatest(t, "2.0.0", nil)
	ui := termio.Mock{}
	opts := Options{CurrentVersion: "1.0.0", Mode: ModeNotify, UI: &ui}
	res, err := Check(context.Background(), opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.HasUpdate {
		t.Fatalf("expected an update notification, got %+v", res)
	}
}

// A nil Store keeps the check in memory, so Check must not run the legacy-file
// fold: the fold would save into the ephemeral memory store and then delete the
// real upgrade.json, losing the dismissals and cadence for good. Paths is a real
// resolver so the nil Store, not a nil Paths, is what suppresses the fold.
func TestCheckNilStoreLeavesLegacyFileInPlace(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	writeLegacy(t, `{"dismissed_versions":["1.5.2"]}`)
	serveLatest(t, "2.0.0", nil)
	ui := termio.Mock{}
	opts := Options{
		CurrentVersion: "1.0.0",
		Mode:           ModeNotify,
		Interval:       time.Hour,
		Paths:          configpaths.Get(),
		UI:             &ui,
	}
	if _, err := Check(context.Background(), opts); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := os.Stat(legacyStatePath(configpaths.Get())); err != nil {
		t.Fatalf("nil Store deleted the legacy upgrade.json: %v", err)
	}
}

// A non-nil Store with a nil Paths resolver must skip the fold, not call
// FoldLegacyState: the resolver is what locates upgrade.json, and Options
// documents that a nil resolver folds nothing rather than crashing. The recent
// LastChecked throttles the check, so no network is touched.
func TestCheckNilPathsSkipsLegacyFold(t *testing.T) {
	ui := termio.Mock{}
	opts := Options{
		CurrentVersion: "1.0.0",
		Mode:           ModeNotify,
		Interval:       time.Hour,
		Store:          &fakeStore{state: LauncherState{LastChecked: time.Now()}},
		UI:             &ui,
	}
	if _, err := Check(context.Background(), opts); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// A dismissal recorded by a concurrent launcher must survive this process's
// state write: persisting unions into whatever the store holds at write time
// instead of replacing it with the snapshot this process loaded.
func TestPersistBestEffortUnionsConcurrentDismissals(t *testing.T) {
	store := &fakeStore{state: LauncherState{DismissedVersions: []string{"9.9.9"}}}
	persistBestEffort(store, LauncherState{
		LastChecked:       time.Now(),
		DismissedVersions: []string{"1.0.0"},
	}, &termio.Mock{})
	if !slices.Contains(store.state.DismissedVersions, "9.9.9") {
		t.Fatalf("persist dropped a concurrent dismissal: %+v", store.state.DismissedVersions)
	}
	if !slices.Contains(store.state.DismissedVersions, "1.0.0") {
		t.Fatalf("persist dropped the local dismissal: %+v", store.state.DismissedVersions)
	}
}

func TestIsNewer(t *testing.T) {
	tests := []struct {
		current, latest string
		want            bool
	}{
		{current: "1.0.0", latest: "1.1.0", want: true},
		{current: "1.10.0", latest: "1.9.0", want: false},
		{current: "2.0.0", latest: "2.0.0", want: false},
		{current: "v1.0.0", latest: "1.0.1", want: true},
		{current: "1.0.0", latest: "1.0.0-beta.1", want: false},
	}
	for _, tc := range tests {
		got, err := isNewer(tc.current, tc.latest)
		if err != nil {
			t.Fatalf("isNewer(%q,%q): %v", tc.current, tc.latest, err)
		}
		if got != tc.want {
			t.Errorf("isNewer(%q,%q) = %v, want %v", tc.current, tc.latest, got, tc.want)
		}
	}
}

func TestIsNewerInvalidVersion(t *testing.T) {
	if _, err := isNewer("dev", "1.0.0"); err == nil {
		t.Fatal("expected error for invalid version")
	}
}

func TestIsNewerInvalidLatest(t *testing.T) {
	if _, err := isNewer("1.0.0", "not-a-version"); err == nil {
		t.Fatal("expected error for invalid latest version")
	}
}

func TestNewerThan(t *testing.T) {
	tests := []struct {
		name     string
		current  string
		latest   string
		allowAny bool
		want     bool
		wantErr  bool
	}{
		{name: "normal newer", current: "1.0.0", latest: "1.1.0", want: true},
		{name: "normal not newer", current: "1.1.0", latest: "1.1.0"},
		{name: "dev ignored without the lever", current: "dev", latest: "1.1.0", wantErr: true},
		{name: "empty ignored without the lever", current: "", latest: "1.1.0", wantErr: true},
		{name: "dev offered with the lever", current: "dev", latest: "1.1.0", allowAny: true, want: true},
		{name: "empty offered with the lever", current: "", latest: "1.1.0", allowAny: true, want: true},
		{name: "garbage offered with the lever", current: "not-a-version", latest: "1.1.0", allowAny: true, want: true},
		{name: "unparsable latest still errors", current: "dev", latest: "nope", allowAny: true, wantErr: true},
		{name: "parseable current is unaffected by the lever", current: "1.1.0", latest: "1.1.0", allowAny: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := newerThan(tc.current, tc.latest, tc.allowAny)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("newerThan(%q,%q,%v) = %v, want %v", tc.current, tc.latest, tc.allowAny, got, tc.want)
			}
		})
	}
}

func TestPromptSelectErrorContinues(t *testing.T) {
	ui := termio.Mock{IsInteractiveResult: true}
	ui.SelectFn = func(_ string, _ []termio.Choice, _ string) (string, error) {
		return "", errors.New("boom")
	}
	if got := prompt(&ui, "1.0.0", "2.0.0"); got != actionContinue {
		t.Fatalf("prompt on select error = %v, want actionContinue", got)
	}
}

func TestUpgradeSwitchesFromCanaryToStable(t *testing.T) {
	installed := stubRelease(t, "0.5.3")
	ui := termio.Mock{}

	err := Upgrade(context.Background(), &ui, UpgradeOptions{
		CurrentVersion:     "0.6.0-canary.1",
		Channel:            ChannelStable,
		AllowChannelSwitch: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if *installed != "0.5.3" {
		t.Fatalf("installed = %q, want the stable 0.5.3", *installed)
	}
	if !strings.Contains(strings.Join(ui.InfoCalls, "\n"), "switching agents-sandbox 0.6.0-canary.1 -> 0.5.3") {
		t.Fatalf("expected a channel switch message, got %v", ui.InfoCalls)
	}
}

func TestUpgradeSwitchTreatsZeroChannelAsStable(t *testing.T) {
	installed := stubRelease(t, "0.5.3")

	err := Upgrade(context.Background(), &termio.Mock{}, UpgradeOptions{
		CurrentVersion:     "0.6.0-canary.1",
		AllowChannelSwitch: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if *installed != "0.5.3" {
		t.Fatalf("installed = %q, want the stable 0.5.3", *installed)
	}
}

func TestUpgradeWithoutChannelSwitchKeepsCanaryAndHints(t *testing.T) {
	installed := stubRelease(t, "0.5.3")
	ui := termio.Mock{}

	err := Upgrade(context.Background(), &ui, UpgradeOptions{
		CurrentVersion: "0.6.0-canary.1",
		Channel:        ChannelStable,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if *installed != "" {
		t.Fatalf("installed = %q, want no install without an explicit channel switch", *installed)
	}
	info := strings.Join(ui.InfoCalls, "\n")
	if !strings.Contains(info, "up to date (0.6.0-canary.1)") {
		t.Fatalf("expected the up-to-date message, got %v", ui.InfoCalls)
	}
	if !strings.Contains(info, "agents-sandbox upgrade --channel stable") || !strings.Contains(info, "0.5.3") {
		t.Fatalf("expected a hint to switch to the stable 0.5.3, got %v", ui.InfoCalls)
	}
}

func TestUpgradeCanaryChannelNeverSwitches(t *testing.T) {
	installed := stubRelease(t, "0.6.0-canary.1")
	ui := termio.Mock{}

	err := Upgrade(context.Background(), &ui, UpgradeOptions{
		CurrentVersion:     "0.6.0-canary.1",
		Channel:            ChannelCanary,
		AllowChannelSwitch: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if *installed != "" {
		t.Fatalf("installed = %q, want nothing on an up-to-date canary", *installed)
	}
	if strings.Contains(strings.Join(ui.InfoCalls, "\n"), "--channel stable") {
		t.Fatalf("a canary-channel run must not hint at stable, got %v", ui.InfoCalls)
	}
}

func TestUpgradeStableReleaseIsNotSwitched(t *testing.T) {
	installed := stubRelease(t, "0.5.3")
	ui := termio.Mock{}

	err := Upgrade(context.Background(), &ui, UpgradeOptions{
		CurrentVersion:     "0.5.3",
		Channel:            ChannelStable,
		AllowChannelSwitch: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if *installed != "" {
		t.Fatalf("installed = %q, want nothing on an up-to-date stable release", *installed)
	}
	if len(ui.InfoCalls) != 1 || !strings.Contains(ui.InfoCalls[0], "up to date") {
		t.Fatalf("expected only the up-to-date message, got %v", ui.InfoCalls)
	}
}

func TestUpgradeNeverDowngradesAStableRelease(t *testing.T) {
	installed := stubRelease(t, "0.5.3")

	err := Upgrade(context.Background(), &termio.Mock{}, UpgradeOptions{
		CurrentVersion:     "0.6.0",
		Channel:            ChannelStable,
		AllowChannelSwitch: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if *installed != "" {
		t.Fatalf("installed = %q, want no downgrade of a stable release", *installed)
	}
}

func TestUpgradeChannelSwitchReportsInstallFailure(t *testing.T) {
	stubRelease(t, "0.5.3")
	Update = func(context.Context, string) error { return errBoom }

	err := Upgrade(context.Background(), &termio.Mock{}, UpgradeOptions{
		CurrentVersion:     "0.6.0-canary.1",
		Channel:            ChannelStable,
		AllowChannelSwitch: true,
	})
	if err == nil {
		t.Fatal("expected the install failure to be returned")
	}
}

func TestUpgradeAnnouncesANewerRelease(t *testing.T) {
	stubRelease(t, "0.6.0")
	ui := termio.Mock{}

	if err := Upgrade(context.Background(), &ui, UpgradeOptions{CurrentVersion: "0.5.3"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ui.InfoCalls) == 0 || ui.InfoCalls[0] != "upgrading agents-sandbox 0.5.3 -> 0.6.0" {
		t.Fatalf("expected the upgrade announcement first, got %v", ui.InfoCalls)
	}
}

func TestUpgrade(t *testing.T) {
	t.Run("dev build rejected", func(t *testing.T) {
		if err := Upgrade(context.Background(), &termio.Mock{}, UpgradeOptions{CurrentVersion: "dev"}); err == nil {
			t.Fatal("expected error for dev build")
		}
	})

	t.Run("up to date", func(t *testing.T) {
		orig := LatestVersion
		LatestVersion = func(context.Context, Channel) (string, error) { return "1.0.0", nil }
		t.Cleanup(func() { LatestVersion = orig })
		ui := termio.Mock{}
		if err := Upgrade(context.Background(), &ui, UpgradeOptions{CurrentVersion: "1.0.0"}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(ui.InfoCalls) == 0 || !strings.Contains(ui.InfoCalls[0], "up to date") {
			t.Fatalf("expected up-to-date info, got %v", ui.InfoCalls)
		}
	})

	t.Run("upgrades", func(t *testing.T) {
		origLatest, origUpdate := LatestVersion, Update
		LatestVersion = func(context.Context, Channel) (string, error) { return "2.0.0", nil }
		var installed string
		Update = func(_ context.Context, latest string) error { installed = latest; return nil }
		t.Cleanup(func() { LatestVersion, Update = origLatest, origUpdate })
		ui := termio.Mock{}
		if err := Upgrade(context.Background(), &ui, UpgradeOptions{CurrentVersion: "1.0.0"}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if installed != "2.0.0" {
			t.Fatalf("installed = %q, want 2.0.0", installed)
		}
	})

	t.Run("homebrew install defers to brew", func(t *testing.T) {
		origHomebrew := InstalledViaHomebrew
		InstalledViaHomebrew = func() bool { return true }
		t.Cleanup(func() { InstalledViaHomebrew = origHomebrew })
		origLatest, origUpdate := LatestVersion, Update
		LatestVersion = func(context.Context, Channel) (string, error) {
			t.Fatal("LatestVersion must not be consulted for a Homebrew install")
			return "", nil
		}
		Update = func(context.Context, string) error {
			t.Fatal("Update must not replace the binary of a Homebrew install")
			return nil
		}
		t.Cleanup(func() { LatestVersion, Update = origLatest, origUpdate })

		ui := termio.Mock{}
		if err := Upgrade(context.Background(), &ui, UpgradeOptions{CurrentVersion: "1.0.0"}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(ui.InfoCalls) == 0 || !strings.Contains(ui.InfoCalls[0], "brew") {
			t.Fatalf("expected brew upgrade guidance, got %v", ui.InfoCalls)
		}
	})

	t.Run("latest lookup failure", func(t *testing.T) {
		orig := LatestVersion
		LatestVersion = func(context.Context, Channel) (string, error) { return "", errors.New("boom") }
		t.Cleanup(func() { LatestVersion = orig })
		if err := Upgrade(context.Background(), &termio.Mock{}, UpgradeOptions{CurrentVersion: "1.0.0"}); err == nil {
			t.Fatal("expected error on lookup failure")
		}
	})
}

func TestUpgradeDevLeverAllowed(t *testing.T) {
	origLatest, origUpdate := LatestVersion, Update
	LatestVersion = func(context.Context, Channel) (string, error) { return "2.0.0", nil }
	var installed string
	Update = func(_ context.Context, latest string) error { installed = latest; return nil }
	t.Cleanup(func() { LatestVersion, Update = origLatest, origUpdate })

	if err := Upgrade(
		context.Background(),
		&termio.Mock{},
		UpgradeOptions{CurrentVersion: "dev", AllowAnyVersion: true},
	); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if installed != "2.0.0" {
		t.Fatalf("installed = %q, want 2.0.0", installed)
	}
}

func TestUpgradeIsNewerError(t *testing.T) {
	orig := LatestVersion
	LatestVersion = func(context.Context, Channel) (string, error) { return "not-a-version", nil }
	t.Cleanup(func() { LatestVersion = orig })
	if err := Upgrade(context.Background(), &termio.Mock{}, UpgradeOptions{CurrentVersion: "1.0.0"}); err == nil {
		t.Fatal("expected error when the latest version is unparsable")
	}
}

func TestUpgradeUpdateError(t *testing.T) {
	origLatest, origUpdate := LatestVersion, Update
	LatestVersion = func(context.Context, Channel) (string, error) { return "2.0.0", nil }
	Update = func(context.Context, string) error { return errors.New("boom") }
	t.Cleanup(func() { LatestVersion, Update = origLatest, origUpdate })
	if err := Upgrade(context.Background(), &termio.Mock{}, UpgradeOptions{CurrentVersion: "1.0.0"}); err == nil {
		t.Fatal("expected error when the install fails")
	}
}

func TestLeavesPrerelease(t *testing.T) {
	tests := []struct {
		name    string
		current string
		channel Channel
		want    bool
	}{
		{name: "canary on stable", current: "0.6.0-canary.1", channel: ChannelStable, want: true},
		{name: "canary on zero channel", current: "v0.6.0-canary.1", channel: "", want: true},
		{name: "canary on canary", current: "0.6.0-canary.1", channel: ChannelCanary, want: false},
		{name: "release on stable", current: "0.5.3", channel: ChannelStable, want: false},
		{name: "unparseable on stable", current: "dev", channel: ChannelStable, want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := leavesPrerelease(test.current, test.channel); got != test.want {
				t.Fatalf("leavesPrerelease(%q, %q) = %v, want %v", test.current, test.channel, got, test.want)
			}
		})
	}
}

func TestLatestRelease(t *testing.T) {
	serveLatest(t, "3.2.1", nil)
	got, err := latestRelease(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "3.2.1" {
		t.Fatalf("got %q, want 3.2.1", got)
	}
}

func TestLatestReleaseNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	orig := latestURL
	latestURL = srv.URL + "/releases/latest"
	t.Cleanup(func() { latestURL = orig })
	if _, err := latestRelease(context.Background()); err == nil {
		t.Fatal("expected error on non-200 response")
	}
}

func TestLatestReleaseDecodeError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("not json"))
	}))
	t.Cleanup(srv.Close)
	orig := latestURL
	latestURL = srv.URL + "/releases/latest"
	t.Cleanup(func() { latestURL = orig })
	if _, err := latestRelease(context.Background()); err == nil {
		t.Fatal("expected error on undecodable response")
	}
}

// TestUpdateExecutable exercises updateExecutable end to end in a child
// process, since it atomically replaces the running executable. The child
// re-runs a copy of this test binary with a helper marker and replaces its own
// binary with the served asset.
func TestUpdateExecutable(t *testing.T) {
	if os.Getenv("UPGRADE_HELPER_PROCESS") == "1" {
		downloadBase = os.Getenv("UPGRADE_DOWNLOAD_BASE")
		if err := updateExecutable(context.Background(), "2.0.0"); err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}

	asset := []byte("#!/bin/sh\necho new\n")
	digest := sha256.Sum256(asset)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v2.0.0/"+checksumsAsset {
			_, _ = fmt.Fprintf(w, "%s  %s\n", hex.EncodeToString(digest[:]), assetName())
			return
		}
		_, _ = w.Write(asset)
	}))
	t.Cleanup(srv.Close)

	childBinary := copyTestBinary(t)
	cmd := exec.Command(childBinary, "-test.run=^TestUpdateExecutable$")
	cmd.Env = append(os.Environ(),
		"UPGRADE_HELPER_PROCESS=1",
		"UPGRADE_DOWNLOAD_BASE="+srv.URL,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("helper failed: %v\n%s", err, out)
	}

	installed, err := os.ReadFile(childBinary)
	if err != nil {
		t.Fatalf("read child binary: %v", err)
	}
	if !bytes.Equal(installed, asset) {
		t.Fatalf("child binary content = %q, want %q", installed, asset)
	}
}

func TestUpdateExecutableDownloadFailure(t *testing.T) {
	if os.Getenv("UPGRADE_HELPER_PROCESS") == "1" {
		downloadBase = "http://127.0.0.1:1"
		if err := updateExecutable(context.Background(), "2.0.0"); err == nil {
			os.Exit(1)
		}
		os.Exit(0)
	}

	cmd := exec.Command(copyTestBinary(t), "-test.run=^TestUpdateExecutableDownloadFailure$")
	cmd.Env = append(os.Environ(), "UPGRADE_HELPER_PROCESS=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("helper failed: %v\n%s", err, out)
	}
}

// TestInstallReleaseIsFailClosed drives the whole download -> verify -> replace
// path: only a matching published checksum may touch the installed binary.
func TestInstallReleaseIsFailClosed(t *testing.T) {
	asset := []byte("#!/bin/sh\necho installed\n")
	digest := sha256.Sum256(asset)

	tests := []struct {
		name      string
		checksums string
		wantErr   bool
	}{
		{name: "matching checksum installs", checksums: checksumLine(digest[:], assetName())},
		{
			name:      "mismatched checksum aborts",
			checksums: checksumLine(bytes.Repeat([]byte{0}, sha256.Size), assetName()),
			wantErr:   true,
		},
		{name: "missing checksums file aborts", checksums: "", wantErr: true},
		{
			name:      "missing entry aborts",
			checksums: checksumLine(digest[:], "agents-sandbox-other-arch"),
			wantErr:   true,
		},
		{
			name:      "malformed entry aborts",
			checksums: "not-a-hex-digest  " + assetName() + "\n",
			wantErr:   true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			releaseServer(t, "2.0.0", asset, tc.checksums)
			dir := t.TempDir()
			exe := filepath.Join(dir, "agents-sandbox")
			original := []byte("old-binary")
			if err := os.WriteFile(exe, original, 0o755); err != nil {
				t.Fatal(err)
			}

			err := installRelease(context.Background(), "2.0.0", exe)

			if tc.wantErr {
				if err == nil {
					t.Fatal("expected the install to abort")
				}
			} else if err != nil {
				t.Fatalf("expected the install to proceed, got %v", err)
			}

			data, readErr := os.ReadFile(exe)
			if readErr != nil {
				t.Fatalf("read installed binary: %v", readErr)
			}
			if tc.wantErr {
				if !bytes.Equal(data, original) {
					t.Fatalf("an aborted install modified the binary: %q", data)
				}
				if leftovers := updateTempFiles(t, dir); len(leftovers) != 0 {
					t.Fatalf("an aborted install left temp files: %v", leftovers)
				}
				return
			}
			if !bytes.Equal(data, asset) {
				t.Fatalf("installed content = %q, want %q", data, asset)
			}
			if leftovers := updateTempFiles(t, dir); len(leftovers) != 0 {
				t.Fatalf("a successful install left temp files: %v", leftovers)
			}
		})
	}
}

func TestInstallReleaseDownloadFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	orig := downloadBase
	downloadBase = srv.URL
	t.Cleanup(func() { downloadBase = orig })

	dir := t.TempDir()
	exe := filepath.Join(dir, "agents-sandbox")
	if err := os.WriteFile(exe, []byte("old-binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := installRelease(context.Background(), "2.0.0", exe); err == nil {
		t.Fatal("expected error when the asset download fails")
	}
	data, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "old-binary" {
		t.Fatalf("a failed download modified the binary: %q", data)
	}
}

func TestInstallReleaseChecksumsReadFailure(t *testing.T) {
	asset := []byte("release-binary")
	// The asset downloads, but the checksums body is truncated: verification
	// fails on the read, and the wrapping must preserve the cause.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v2.0.0/"+checksumsAsset {
			w.Header().Set("Content-Length", "1024")
			_, _ = w.Write([]byte("short"))
			return
		}
		_, _ = w.Write(asset)
	}))
	t.Cleanup(srv.Close)
	pointDownloadBase(t, srv.URL)

	dir := t.TempDir()
	exe := filepath.Join(dir, "agents-sandbox")
	if err := os.WriteFile(exe, []byte("old-binary"), 0o755); err != nil {
		t.Fatal(err)
	}

	err := installRelease(context.Background(), "2.0.0", exe)
	if err == nil {
		t.Fatal("expected error when the checksums read fails")
	}
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("expected the read error to be preserved, got %v", err)
	}
	data, readErr := os.ReadFile(exe)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(data) != "old-binary" {
		t.Fatalf("a failed verification modified the binary: %q", data)
	}
}

func TestInstallReleaseReplaceFailure(t *testing.T) {
	asset := []byte("binary")
	digest := sha256.Sum256(asset)
	releaseServer(t, "2.0.0", asset, checksumLine(digest[:], assetName()))

	dir := t.TempDir()
	exe := filepath.Join(dir, "agents-sandbox")
	if err := os.Mkdir(exe, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := installRelease(context.Background(), "2.0.0", exe); err == nil {
		t.Fatal("expected error when the replacement target is a directory")
	}
	if leftovers := updateTempFiles(t, dir); len(leftovers) != 0 {
		t.Fatalf("a failed replace left temp files: %v", leftovers)
	}
}

func TestDownloadAssetCreateTempFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("data"))
	}))
	t.Cleanup(srv.Close)
	orig := downloadBase
	downloadBase = srv.URL
	t.Cleanup(func() { downloadBase = orig })

	dir := filepath.Join(t.TempDir(), "does-not-exist")
	if _, err := downloadAssetToDir(context.Background(), dir, "2.0.0"); err == nil {
		t.Fatal("expected error when the destination directory does not exist")
	}
}

func TestDownloadAssetCopyFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "100")
		_, _ = w.Write([]byte("short"))
	}))
	t.Cleanup(srv.Close)
	orig := downloadBase
	downloadBase = srv.URL
	t.Cleanup(func() { downloadBase = orig })

	if _, err := downloadAssetToDir(context.Background(), t.TempDir(), "2.0.0"); err == nil {
		t.Fatal("expected error when the response body is truncated")
	}
}

func TestDownloadAndReplace(t *testing.T) {
	serveLatest(t, "2.0.0", []byte("#!/bin/sh\necho new\n"))
	dir := t.TempDir()
	target := filepath.Join(dir, "agents-sandbox")
	_ = os.WriteFile(target, []byte("old"), 0o750)

	asset, err := downloadAssetToDir(context.Background(), dir, "2.0.0")
	if err != nil {
		t.Fatalf("download: %v", err)
	}
	defer os.Remove(asset)
	if err := replaceExecutable(asset, target); err != nil {
		t.Fatalf("replace: %v", err)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read replaced file: %v", err)
	}
	if string(data) != "#!/bin/sh\necho new\n" {
		t.Fatalf("unexpected replaced content: %q", string(data))
	}
	info, _ := os.Stat(target)
	if info.Mode().Perm() != 0o750 {
		t.Fatalf("replaced binary mode = %o, want 750 (preserve original permissions)", info.Mode().Perm())
	}
}

func TestDownloadAssetUsesTaggedURL(t *testing.T) {
	asset := []byte("binary")
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write(asset)
	}))
	t.Cleanup(srv.Close)
	orig := downloadBase
	downloadBase = srv.URL
	t.Cleanup(func() { downloadBase = orig })

	if _, err := downloadAssetToDir(context.Background(), t.TempDir(), "1.9.0-canary.3"); err != nil {
		t.Fatalf("download: %v", err)
	}
	want := fmt.Sprintf("/v1.9.0-canary.3/agents-sandbox-%s-%s", runtime.GOOS, runtime.GOARCH)
	if gotPath != want {
		t.Fatalf("download path = %q, want %q", gotPath, want)
	}
}

func TestDownloadAssetNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)
	orig := downloadBase
	downloadBase = srv.URL
	t.Cleanup(func() { downloadBase = orig })
	if _, err := downloadAssetToDir(context.Background(), t.TempDir(), "2.0.0"); err == nil {
		t.Fatal("expected error on non-200 download")
	}
}

func TestDownloadAssetNetworkError(t *testing.T) {
	orig := downloadBase
	downloadBase = "http://127.0.0.1:1"
	t.Cleanup(func() { downloadBase = orig })
	if _, err := downloadAssetToDir(context.Background(), t.TempDir(), "2.0.0"); err == nil {
		t.Fatal("expected error on unreachable download endpoint")
	}
}

func TestDownloadAssetBadURL(t *testing.T) {
	orig := downloadBase
	downloadBase = "://missing-scheme"
	t.Cleanup(func() { downloadBase = orig })
	_, err := downloadAssetToDir(context.Background(), t.TempDir(), "2.0.0")
	if err == nil {
		t.Fatal("expected error for an unbuildable download request")
	}
	if _, ok := errors.AsType[*url.Error](err); !ok {
		t.Fatalf("expected the request error to be preserved, got %v", err)
	}
}

func TestExecutablePath(t *testing.T) {
	path, err := executablePath()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if path == "" {
		t.Fatal("expected a non-empty executable path")
	}
}

func TestReplaceExecutableChmodFails(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "agents-sandbox")
	_ = os.WriteFile(target, []byte("old"), 0o755)

	asset := filepath.Join(dir, "missing-asset")
	if err := replaceExecutable(asset, target); err == nil {
		t.Fatal("expected error when the asset cannot be chmod'd")
	}
}

func TestReplaceExecutableRenameFails(t *testing.T) {
	dir := t.TempDir()
	asset := filepath.Join(dir, "asset")
	_ = os.WriteFile(asset, []byte("new"), 0o600)

	exePath := filepath.Join(dir, "agents-sandbox")
	_ = os.MkdirAll(exePath, 0o755)

	if err := replaceExecutable(asset, exePath); err == nil {
		t.Fatal("expected error when the rename target is a directory")
	}
}

func TestReplaceExecutableDefaultsToExecutable(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "agents-sandbox") // does not exist
	asset := filepath.Join(dir, "asset")
	_ = os.WriteFile(asset, []byte("new"), 0o600)

	if err := replaceExecutable(asset, target); err != nil {
		t.Fatalf("replace: %v", err)
	}
	info, _ := os.Stat(target)
	if info.Mode().Perm()&0o111 == 0 {
		t.Fatal("replaced binary should be executable when target missing")
	}
}

// stubRelease makes LatestVersion report latest and records what Update
// installs, restoring both when the test ends.
func stubRelease(t *testing.T, latest string) *string {
	t.Helper()
	origLatest, origUpdate := LatestVersion, Update
	t.Cleanup(func() { LatestVersion, Update = origLatest, origUpdate })
	LatestVersion = func(context.Context, Channel) (string, error) { return latest, nil }
	installed := new(string)
	Update = func(_ context.Context, version string) error {
		*installed = version
		return nil
	}
	return installed
}

// updateTempFiles lists the updater's temp files left in dir.
func updateTempFiles(t *testing.T, dir string) []string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, ".agents-sandbox-update-*"))
	if err != nil {
		t.Fatalf("glob temp files: %v", err)
	}
	return matches
}

// copyTestBinary copies the running test binary into a fresh temp directory and
// returns the copy's path. A child-process test replaces the executable it runs,
// so each must run its own copy: replacing the shared test binary would make
// every later child exec the replacement instead of the test.
func copyTestBinary(t *testing.T) string {
	t.Helper()
	source, err := os.Open(os.Args[0])
	if err != nil {
		t.Fatalf("open test binary: %v", err)
	}
	defer source.Close()

	target := filepath.Join(t.TempDir(), "upgrade.test")
	dest, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		t.Fatalf("create test binary copy: %v", err)
	}
	if _, err := io.Copy(dest, source); err != nil {
		_ = dest.Close()
		t.Fatalf("copy test binary: %v", err)
	}
	if err := dest.Close(); err != nil {
		t.Fatalf("close test binary copy: %v", err)
	}
	return target
}

// mockOptions builds Options for a Check test. It isolates host config paths
// because Check folds the retired upgrade.json from the user state directory.
func mockOptions(t *testing.T, version string, mode Mode) Options {
	t.Helper()
	configpaths.WithMockConfigPaths(t)
	return Options{
		CurrentVersion: version,
		Mode:           mode,
		Interval:       time.Hour,
		Store:          &fakeStore{},
		Paths:          configpaths.Get(),
		UI:             &termio.Mock{},
	}
}
