package agent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func serveGooseRelease(t *testing.T, handler http.HandlerFunc) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	orig := gooseGitHubLatestURL
	gooseGitHubLatestURL = srv.URL
	t.Cleanup(func() { gooseGitHubLatestURL = orig })
}

func TestGooseLatestVersionStripsTagPrefix(t *testing.T) {
	serveGooseRelease(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"tag_name":"v1.60.1"}`))
	})
	got, err := latestGooseVersion(context.Background())
	if err != nil || got != "1.60.1" {
		t.Errorf("latestGooseVersion() = %q, %v, want 1.60.1", got, err)
	}
}

func TestGooseLatestVersionRejectsUnexpectedTag(t *testing.T) {
	for _, tag := range []string{"", "1.60.1", "v"} {
		serveGooseRelease(t, func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"tag_name":"` + tag + `"}`))
		})
		if got, err := latestGooseVersion(context.Background()); err == nil {
			t.Errorf("tag %q: latestGooseVersion() = %q, want error", tag, got)
		}
	}
}

func TestGooseLatestVersionNon200(t *testing.T) {
	serveGooseRelease(t, func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "rate limited", http.StatusForbidden)
	})
	_, err := latestGooseVersion(context.Background())
	if err == nil || !strings.Contains(err.Error(), "unexpected status 403") {
		t.Fatalf("err = %v, want the fetch error reporting status 403", err)
	}
}
