package upgrade

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// serveLatest starts an httptest server that serves the latest-release API
// and a release asset download, wiring the package endpoint overrides to it.
func serveLatest(t *testing.T, latest string, asset []byte) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/releases/latest":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"tag_name":"v` + latest + `"}`))
		default:
			_, _ = w.Write(asset)
		}
	}))
	t.Cleanup(srv.Close)

	origLatest := latestURL
	origDownload := downloadBase
	latestURL = srv.URL + "/releases/latest"
	downloadBase = srv.URL
	t.Cleanup(func() {
		latestURL = origLatest
		downloadBase = origDownload
	})
}

// fakeStore is the in-memory Store injected by mockOptions.
type fakeStore struct {
	state LauncherState
	err   error
}

func (f *fakeStore) Load() (LauncherState, error) { return f.state, f.err }

func (f *fakeStore) Update(mutate func(*LauncherState)) error {
	if f.err != nil {
		return f.err
	}
	mutate(&f.state)
	return nil
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
		UI:             &termio.Mock{},
	}
}

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

// writeFailingStore loads from its inner store but always fails to write.
type writeFailingStore struct{ inner Store }

func (s *writeFailingStore) Load() (LauncherState, error) { return s.inner.Load() }

func (s *writeFailingStore) Update(func(*LauncherState)) error { return errors.New("state locked") }

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

func TestStateRoundTrip(t *testing.T) {
	store := &fakeStore{}
	want := LauncherState{
		LastChecked:       time.Now().Truncate(time.Second),
		DismissedVersions: []string{"1.1.0"},
	}
	if err := store.Update(func(st *LauncherState) { *st = want }); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, err := store.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !got.LastChecked.Equal(want.LastChecked) || len(got.DismissedVersions) != 1 ||
		got.DismissedVersions[0] != "1.1.0" {
		t.Fatalf("got %+v, want %+v", got, want)
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
	if err := os.Mkdir(legacyStatePath(), 0o700); err != nil {
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
// real upgrade.json, losing the dismissals and cadence for good.
func TestCheckNilStoreLeavesLegacyFileInPlace(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	writeLegacy(t, `{"dismissed_versions":["1.5.2"]}`)
	serveLatest(t, "2.0.0", nil)
	ui := termio.Mock{}
	opts := Options{CurrentVersion: "1.0.0", Mode: ModeNotify, Interval: time.Hour, UI: &ui}
	if _, err := Check(context.Background(), opts); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := os.Stat(legacyStatePath()); err != nil {
		t.Fatalf("nil Store deleted the legacy upgrade.json: %v", err)
	}
}

func TestIsNewerInvalidLatest(t *testing.T) {
	if _, err := isNewer("1.0.0", "not-a-version"); err == nil {
		t.Fatal("expected error for invalid latest version")
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
