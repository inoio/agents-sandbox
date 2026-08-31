package opencode

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// overrideLatestURL swaps gitHubLatestURL for the duration of a test. It returns
// a restore func.
func overrideLatestURL(url string) func() {
	orig := gitHubLatestURL
	gitHubLatestURL = url
	return func() { gitHubLatestURL = orig }
}

func TestLatestVersionReadsGitHubRelease(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"tag_name":"v1.2.3"}`))
	}))
	t.Cleanup(srv.Close)

	restore := overrideLatestURL(srv.URL)
	t.Cleanup(restore)

	got, err := LatestVersion(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "1.2.3" {
		t.Errorf("LatestVersion() = %q, want %q", got, "1.2.3")
	}
}

func TestLatestVersionStripsVBeta(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"tag_name":"v4.0.0-beta.1"}`))
	}))
	t.Cleanup(srv.Close)
	restore := overrideLatestURL(srv.URL)
	t.Cleanup(restore)

	got, err := LatestVersion(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "4.0.0-beta.1" {
		t.Errorf("LatestVersion() = %q, want %q", got, "4.0.0-beta.1")
	}
}

func TestLatestVersionPropagatesRequestError(t *testing.T) {
	restore := overrideLatestURL("http://127.0.0.1:1")
	t.Cleanup(restore)

	if _, err := LatestVersion(context.Background()); err == nil {
		t.Fatal("expected error for unreachable endpoint")
	}
}
