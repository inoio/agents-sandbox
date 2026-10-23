package agent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func overrideHermesLatestURL(url string) func() {
	orig := hermesGitHubLatestURL
	hermesGitHubLatestURL = url
	return func() { hermesGitHubLatestURL = orig }
}

func TestHermesLatestVersionStripsTagPrefix(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"tag_name":"v0.22.5","name":"Hermes Agent v0.22.5"}`))
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(overrideHermesLatestURL(srv.URL))

	got, err := latestHermesVersion(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "0.22.5" {
		t.Errorf("latestHermesVersion() = %q, want 0.22.5", got)
	}
}

func TestHermesLatestVersionEmptyTag(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"tag_name":""}`))
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(overrideHermesLatestURL(srv.URL))

	if _, err := latestHermesVersion(context.Background()); err == nil {
		t.Fatal("expected error for an empty tag")
	}
}

func TestHermesLatestVersionNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(overrideHermesLatestURL(srv.URL))

	if _, err := latestHermesVersion(context.Background()); err == nil {
		t.Fatal("expected error on non-200 response")
	}
}

func TestHermesLatestVersionDecodeError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("not json"))
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(overrideHermesLatestURL(srv.URL))

	if _, err := latestHermesVersion(context.Background()); err == nil {
		t.Fatal("expected error on undecodable response")
	}
}
