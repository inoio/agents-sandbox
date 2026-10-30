package agent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func serveCodexRelease(t *testing.T, handler http.HandlerFunc) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	orig := codexGitHubLatestURL
	codexGitHubLatestURL = srv.URL
	t.Cleanup(func() { codexGitHubLatestURL = orig })
}

func TestCodexLatestVersionStripsTagPrefix(t *testing.T) {
	serveCodexRelease(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"tag_name":"rust-v0.164.0"}`))
	})
	got, err := latestCodexVersion(context.Background())
	if err != nil || got != "0.164.0" {
		t.Errorf("latestCodexVersion() = %q, %v, want 0.164.0", got, err)
	}
}

func TestCodexLatestVersionRejectsUnexpectedTag(t *testing.T) {
	for _, tag := range []string{"", "v0.164.0", "rust-v"} {
		serveCodexRelease(t, func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"tag_name":"` + tag + `"}`))
		})
		if got, err := latestCodexVersion(context.Background()); err == nil {
			t.Errorf("tag %q: latestCodexVersion() = %q, want error", tag, got)
		}
	}
}

func TestCodexLatestVersionNon200(t *testing.T) {
	serveCodexRelease(t, func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "rate limited", http.StatusForbidden)
	})
	_, err := latestCodexVersion(context.Background())
	if err == nil || !strings.Contains(err.Error(), "unexpected status 403") {
		t.Fatalf("err = %v, want the fetch error reporting status 403", err)
	}
}
