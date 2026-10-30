package agent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func serveOhMyPiRelease(t *testing.T, handler http.HandlerFunc) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	orig := ohMyPiGitHubLatestURL
	ohMyPiGitHubLatestURL = srv.URL
	t.Cleanup(func() { ohMyPiGitHubLatestURL = orig })
}

func TestOhMyPiLatestVersionStripsTagPrefix(t *testing.T) {
	serveOhMyPiRelease(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"tag_name":"v18.14.0"}`))
	})
	got, err := latestOhMyPiVersion(context.Background())
	if err != nil || got != "18.14.0" {
		t.Errorf("latestOhMyPiVersion() = %q, %v, want 18.14.0", got, err)
	}
}

func TestOhMyPiLatestVersionRejectsUnexpectedTag(t *testing.T) {
	for _, tag := range []string{"", "18.14.0", "v"} {
		serveOhMyPiRelease(t, func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"tag_name":"` + tag + `"}`))
		})
		if got, err := latestOhMyPiVersion(context.Background()); err == nil {
			t.Errorf("tag %q: latestOhMyPiVersion() = %q, want error", tag, got)
		}
	}
}

func TestOhMyPiLatestVersionNon200(t *testing.T) {
	serveOhMyPiRelease(t, func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "rate limited", http.StatusForbidden)
	})
	_, err := latestOhMyPiVersion(context.Background())
	if err == nil || !strings.Contains(err.Error(), "unexpected status 403") {
		t.Fatalf("err = %v, want the fetch error reporting status 403", err)
	}
}
