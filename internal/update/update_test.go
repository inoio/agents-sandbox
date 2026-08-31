package update

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

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
		{in: "auto-upgrade", want: ModeAutoUpgrade},
		{in: "auto-upgrade-exit", want: ModeAutoUpgradeExit},
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
	opts := mockOptions(t, "1.0.0", ModeAutoUpgrade)
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
	opts := mockOptions(t, "1.0.0", ModeAutoUpgradeExit)
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
	_ = os.WriteFile(target, []byte("old"), 0o755)

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
	if info.Mode().Perm()&0o111 == 0 {
		t.Fatal("replaced binary is not executable")
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
