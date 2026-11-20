package agent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func overrideGrokBuildLatestURL(t *testing.T, url string) {
	t.Helper()
	original := grokBuildNpmLatestURL
	grokBuildNpmLatestURL = url
	t.Cleanup(func() { grokBuildNpmLatestURL = original })
}

func serveGrokBuildLatest(t *testing.T, handler http.HandlerFunc) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	overrideGrokBuildLatestURL(t, server.URL)
}

func TestGrokBuildLatestVersionReadsNpm(t *testing.T) {
	serveGrokBuildLatest(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"name":"@xai-official/grok","version":"1.0.89"}`))
	})

	got, err := latestGrokBuildVersion(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "1.0.89" {
		t.Errorf("latestGrokBuildVersion() = %q, want 1.0.89", got)
	}
}

func TestGrokBuildLatestVersionRejectsBadResponses(t *testing.T) {
	for name, handler := range map[string]http.HandlerFunc{
		"empty version": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"version":""}`))
		},
		"non-200": func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "nope", http.StatusInternalServerError)
		},
		"undecodable": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("not json"))
		},
	} {
		t.Run(name, func(t *testing.T) {
			serveGrokBuildLatest(t, handler)
			if version, err := latestGrokBuildVersion(context.Background()); err == nil {
				t.Fatalf("latestGrokBuildVersion() = %q, want an error", version)
			}
		})
	}
}
