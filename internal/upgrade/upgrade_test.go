package upgrade

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/inoio/opencode-sandbox/internal/configpaths"
	"github.com/inoio/opencode-sandbox/internal/termio"
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

func mockOptions(t testing.TB, version string, mode Mode) Options {
	t.Helper()
	return Options{
		CurrentVersion: version,
		Mode:           mode,
		Interval:       time.Hour,
		StatePath:      filepath.Join(t.TempDir(), "state.json"),
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

func TestCheckThrottled(t *testing.T) {
	// A failing server proves no network request happens when throttled.
	serveLatest(t, "9.9.9", nil)
	latestURL = "http://127.0.0.1:1/releases/latest" // force failure if hit
	ui := termio.Mock{}
	opts := mockOptions(t, "1.0.0", ModeNotify)
	opts.UI = &ui
	_ = os.MkdirAll(filepath.Dir(opts.StatePath), 0o700)
	_ = os.WriteFile(opts.StatePath, []byte(`{"last_check":"`+time.Now().Format(time.RFC3339)+`"}`), 0o600)

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
	opts := mockOptions(t, "1.5.0", ModeNotify)
	res, err := Check(context.Background(), opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.HasUpdate {
		t.Fatalf("expected no update, got %+v", res)
	}
	data, err := os.ReadFile(opts.StatePath)
	if err != nil {
		t.Fatalf("expected state file written: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("state file empty")
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
	_ = os.MkdirAll(filepath.Dir(opts.StatePath), 0o700)
	_ = os.WriteFile(opts.StatePath, []byte(`{"dismissed_versions":["2.0.0"]}`), 0o600)
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
	target := filepath.Join(dir, "opencode-sandbox")
	_ = os.WriteFile(target, []byte("old"), 0o750)

	asset, err := downloadAssetToDir(context.Background(), dir)
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

func TestReplaceExecutableDefaultsToExecutable(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "opencode-sandbox") // does not exist
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
	path := filepath.Join(t.TempDir(), "state.json")
	want := state{LastCheck: time.Now().Truncate(time.Second), DismissedVersions: []string{"1.1.0"}}
	if err := saveState(path, want); err != nil {
		t.Fatalf("save: %v", err)
	}
	got := loadState(path)
	if !got.LastCheck.Equal(want.LastCheck) || len(got.DismissedVersions) != 1 || got.DismissedVersions[0] != "1.1.0" {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestLoadStateMissingFile(t *testing.T) {
	s := loadState(filepath.Join(t.TempDir(), "nope.json"))
	if !s.LastCheck.IsZero() || len(s.DismissedVersions) != 0 {
		t.Fatalf("expected empty state, got %+v", s)
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
		if err := Upgrade(context.Background(), &termio.Mock{}, "dev"); err == nil {
			t.Fatal("expected error for dev build")
		}
	})

	t.Run("up to date", func(t *testing.T) {
		orig := LatestVersion
		LatestVersion = func(context.Context) (string, error) { return "1.0.0", nil }
		t.Cleanup(func() { LatestVersion = orig })
		ui := termio.Mock{}
		if err := Upgrade(context.Background(), &ui, "1.0.0"); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(ui.InfoCalls) == 0 || !strings.Contains(ui.InfoCalls[0], "up to date") {
			t.Fatalf("expected up-to-date info, got %v", ui.InfoCalls)
		}
	})

	t.Run("upgrades", func(t *testing.T) {
		origLatest, origUpdate := LatestVersion, Update
		LatestVersion = func(context.Context) (string, error) { return "2.0.0", nil }
		var installed string
		Update = func(_ context.Context, latest string) error { installed = latest; return nil }
		t.Cleanup(func() { LatestVersion, Update = origLatest, origUpdate })
		ui := termio.Mock{}
		if err := Upgrade(context.Background(), &ui, "1.0.0"); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if installed != "2.0.0" {
			t.Fatalf("installed = %q, want 2.0.0", installed)
		}
	})

	t.Run("latest lookup failure", func(t *testing.T) {
		orig := LatestVersion
		LatestVersion = func(context.Context) (string, error) { return "", errors.New("boom") }
		t.Cleanup(func() { LatestVersion = orig })
		if err := Upgrade(context.Background(), &termio.Mock{}, "1.0.0"); err == nil {
			t.Fatal("expected error on lookup failure")
		}
	})
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
	if _, err := downloadAssetToDir(context.Background(), t.TempDir()); err == nil {
		t.Fatal("expected error on non-200 download")
	}
}

func TestCheckDefaultsIntervalAndStatePath(t *testing.T) {
	// A zero Interval and empty StatePath exercise the default-resolution
	// branches of Check, writing state to the mocked user state dir.
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
	LatestVersion = func(context.Context) (string, error) { return "not-a-version", nil }
	t.Cleanup(func() { LatestVersion = orig })
	if err := Upgrade(context.Background(), &termio.Mock{}, "1.0.0"); err == nil {
		t.Fatal("expected error when the latest version is unparsable")
	}
}

func TestUpgradeUpdateError(t *testing.T) {
	origLatest, origUpdate := LatestVersion, Update
	LatestVersion = func(context.Context) (string, error) { return "2.0.0", nil }
	Update = func(context.Context, string) error { return errors.New("boom") }
	t.Cleanup(func() { LatestVersion, Update = origLatest, origUpdate })
	if err := Upgrade(context.Background(), &termio.Mock{}, "1.0.0"); err == nil {
		t.Fatal("expected error when the install fails")
	}
}

func TestDownloadAssetNetworkError(t *testing.T) {
	orig := downloadBase
	downloadBase = "http://127.0.0.1:1"
	t.Cleanup(func() { downloadBase = orig })
	if _, err := downloadAssetToDir(context.Background(), t.TempDir()); err == nil {
		t.Fatal("expected error on unreachable download endpoint")
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
