package agent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func overrideKimiCodeLatestURL(t *testing.T, url string) {
	t.Helper()
	original := kimiCodeNpmLatestURL
	kimiCodeNpmLatestURL = url
	t.Cleanup(func() { kimiCodeNpmLatestURL = original })
}

func serveKimiCodeLatest(t *testing.T, handler http.HandlerFunc) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	overrideKimiCodeLatestURL(t, server.URL)
}

func TestKimiCodeLatestVersionReadsNpm(t *testing.T) {
	serveKimiCodeLatest(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"name":"@moonshot-ai/kimi-code","version":"2.7.0"}`))
	})

	got, err := latestKimiCodeVersion(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "2.7.0" {
		t.Errorf("latestKimiCodeVersion() = %q, want 2.7.0", got)
	}
}

func TestKimiCodeLatestVersionRejectsBadResponses(t *testing.T) {
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
			serveKimiCodeLatest(t, handler)
			if version, err := latestKimiCodeVersion(context.Background()); err == nil {
				t.Fatalf("latestKimiCodeVersion() = %q, want an error", version)
			}
		})
	}
}
