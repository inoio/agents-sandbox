package agent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func overrideClaudeCodeLatestURL(url string) func() {
	orig := claudeCodeNpmLatestURL
	claudeCodeNpmLatestURL = url
	return func() { claudeCodeNpmLatestURL = orig }
}

func TestClaudeCodeLatestVersionReadsNpm(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"name":"@anthropic-ai/claude-code","version":"2.1.252"}`))
	}))
	t.Cleanup(srv.Close)
	restore := overrideClaudeCodeLatestURL(srv.URL)
	t.Cleanup(restore)

	got, err := latestClaudeCodeVersion(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "2.1.252" {
		t.Errorf("latestClaudeCodeVersion() = %q, want 2.1.252", got)
	}
}

func TestClaudeCodeLatestVersionEmptyVersion(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"version":""}`))
	}))
	t.Cleanup(srv.Close)
	restore := overrideClaudeCodeLatestURL(srv.URL)
	t.Cleanup(restore)

	if _, err := latestClaudeCodeVersion(context.Background()); err == nil {
		t.Fatal("expected error for empty version")
	}
}

func TestClaudeCodeLatestVersionNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	restore := overrideClaudeCodeLatestURL(srv.URL)
	t.Cleanup(restore)

	if _, err := latestClaudeCodeVersion(context.Background()); err == nil {
		t.Fatal("expected error on non-200 response")
	}
}

func TestClaudeCodeLatestVersionDecodeError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("not json"))
	}))
	t.Cleanup(srv.Close)
	restore := overrideClaudeCodeLatestURL(srv.URL)
	t.Cleanup(restore)

	if _, err := latestClaudeCodeVersion(context.Background()); err == nil {
		t.Fatal("expected error on undecodable response")
	}
}
