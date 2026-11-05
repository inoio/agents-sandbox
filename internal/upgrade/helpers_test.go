package upgrade

import (
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/inoio/agents-sandbox/internal/configpaths"
)

// serveReleases serves the release-list API from a JSON body.
func serveReleases(t *testing.T, body string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	orig := releasesURL
	releasesURL = srv.URL + "/releases"
	t.Cleanup(func() { releasesURL = orig })
}

// releaseServer serves the current platform's release asset and (when
// checksums is non-empty) the release's checksums.txt at the tagged URLs the
// updater builds, pointing downloadBase at itself.
func releaseServer(t *testing.T, version string, asset []byte, checksums string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case fmt.Sprintf("/v%s/%s", version, assetName()):
			_, _ = w.Write(asset)
		case fmt.Sprintf("/v%s/%s", version, checksumsAsset):
			if checksums == "" {
				http.NotFound(w, r)
				return
			}
			_, _ = io.WriteString(w, checksums)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	orig := downloadBase
	downloadBase = srv.URL
	t.Cleanup(func() { downloadBase = orig })
}

// pointDownloadBase redirects downloadBase at base for the duration of the test.
func pointDownloadBase(t *testing.T, base string) {
	t.Helper()
	orig := downloadBase
	downloadBase = base
	t.Cleanup(func() { downloadBase = orig })
}

// checksumLine formats a sha256sum entry for name.
func checksumLine(digest []byte, name string) string {
	return fmt.Sprintf("%s  %s\n", hex.EncodeToString(digest), name)
}

// errBoom marks the failure cases exercised in TestUnderCellar.
var errBoom = errors.New("boom")

// writeLegacy writes the retired upgrade.json at the legacy path, creating the
// user state directory first.
func writeLegacy(t *testing.T, body string) {
	t.Helper()
	if err := os.MkdirAll(configpaths.Get().UserStateDir(), 0o700); err != nil {
		t.Fatalf("create user state dir: %v", err)
	}
	if err := os.WriteFile(legacyStatePath(configpaths.Get()), []byte(body), 0o600); err != nil {
		t.Fatalf("write legacy state: %v", err)
	}
}

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

// writeFailingStore loads from its inner store but always fails to write.
type writeFailingStore struct{ inner Store }

func (s *writeFailingStore) Load() (LauncherState, error) { return s.inner.Load() }

func (s *writeFailingStore) Update(func(*LauncherState)) error { return errors.New("state locked") }
