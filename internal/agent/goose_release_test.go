package agent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func serveGooseReleases(t *testing.T, body string) {
	t.Helper()
	serveGooseReleasesWith(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) })
}

func serveGooseReleasesWith(t *testing.T, handler http.HandlerFunc) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	orig := gooseGitHubReleasesURL
	gooseGitHubReleasesURL = srv.URL
	t.Cleanup(func() { gooseGitHubReleasesURL = orig })
}

// gooseCompleteAssetsJSON is the asset list of a complete goose release: both Linux
// musl CLI archives beside the other platforms.
const gooseCompleteAssetsJSON = `[{"name":"goose-x86_64-unknown-linux-musl.tar.gz"},` +
	`{"name":"goose-aarch64-unknown-linux-musl.tar.gz"},{"name":"goose-acp-x86_64-unknown-linux-gnu.tar.gz"}]`

func TestGooseLatestVersionStripsTagPrefix(t *testing.T) {
	serveGooseReleases(t, `[{"tag_name":"v1.60.1","assets":`+gooseCompleteAssetsJSON+`}]`)
	got, err := latestGooseVersion(context.Background())
	if err != nil || got != "1.60.1" {
		t.Errorf("latestGooseVersion() = %q, %v, want 1.60.1", got, err)
	}
}

// TestGooseLatestVersionSkipsReleasesWithoutTheCLIAssets pins that a release
// published before its Linux CLI archives are uploaded (goose v1.60.2 shipped
// only its acp assets for hours) is passed over for the newest complete one,
// so an unpinned build never resolves a version it cannot download.
func TestGooseLatestVersionSkipsReleasesWithoutTheCLIAssets(t *testing.T) {
	serveGooseReleases(t, `[`+
		`{"tag_name":"v1.60.2","assets":[{"name":"goose-acp-x86_64-unknown-linux-gnu.tar.gz"}]},`+
		`{"tag_name":"v1.60.1","assets":`+gooseCompleteAssetsJSON+`}]`)
	got, err := latestGooseVersion(context.Background())
	if err != nil || got != "1.60.1" {
		t.Errorf("latestGooseVersion() = %q, %v, want 1.60.1", got, err)
	}
}

func TestGooseLatestVersionNeedsBothArchitectures(t *testing.T) {
	serveGooseReleases(t, `[`+
		`{"tag_name":"v1.60.2","assets":[{"name":"goose-x86_64-unknown-linux-musl.tar.gz"}]},`+
		`{"tag_name":"v1.60.1","assets":`+gooseCompleteAssetsJSON+`}]`)
	got, err := latestGooseVersion(context.Background())
	if err != nil || got != "1.60.1" {
		t.Errorf("latestGooseVersion() = %q, %v, want 1.60.1 (v1.60.2 lacks the aarch64 asset)", got, err)
	}
}

func TestGooseLatestVersionSkipsDraftsAndPrereleases(t *testing.T) {
	serveGooseReleases(t, `[`+
		`{"tag_name":"v1.61.0","draft":true,"assets":`+gooseCompleteAssetsJSON+`},`+
		`{"tag_name":"v1.61.0-rc.1","prerelease":true,"assets":`+gooseCompleteAssetsJSON+`},`+
		`{"tag_name":"v1.60.1","assets":`+gooseCompleteAssetsJSON+`}]`)
	got, err := latestGooseVersion(context.Background())
	if err != nil || got != "1.60.1" {
		t.Errorf("latestGooseVersion() = %q, %v, want 1.60.1", got, err)
	}
}

func TestGooseLatestVersionReportsNoCompleteRelease(t *testing.T) {
	serveGooseReleases(t, `[{"tag_name":"v1.60.2","assets":[{"name":"goose-acp-x86_64-unknown-linux-gnu.tar.gz"}]}]`)
	_, err := latestGooseVersion(context.Background())
	if err == nil || !strings.Contains(err.Error(), "no goose release lists goose-x86_64-unknown-linux-musl.tar.gz") {
		t.Fatalf("err = %v, want a no-complete-release error naming the missing asset", err)
	}
}

func TestGooseLatestVersionRejectsUnexpectedTag(t *testing.T) {
	for _, tag := range []string{"", "1.60.1", "v"} {
		serveGooseReleases(t, `[{"tag_name":"`+tag+`","assets":`+gooseCompleteAssetsJSON+`}]`)
		_, err := latestGooseVersion(context.Background())
		if err == nil || !strings.Contains(err.Error(), "unexpected tag") {
			t.Errorf("tag %q: err = %v, want an unexpected-tag error", tag, err)
		}
	}
}

func TestGooseLatestVersionNon200(t *testing.T) {
	serveGooseReleasesWith(t, func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "rate limited", http.StatusForbidden)
	})
	_, err := latestGooseVersion(context.Background())
	if err == nil || !strings.Contains(err.Error(), "unexpected status 403") {
		t.Fatalf("err = %v, want the fetch error reporting status 403", err)
	}
}
