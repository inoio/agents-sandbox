package agent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func overrideQwenCodeLatestURL(t *testing.T, url string) {
	t.Helper()
	original := qwenCodeNpmLatestURL
	qwenCodeNpmLatestURL = url
	t.Cleanup(func() { qwenCodeNpmLatestURL = original })
}

func serveQwenCodeLatest(t *testing.T, handler http.HandlerFunc) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	overrideQwenCodeLatestURL(t, server.URL)
}

func TestQwenCodeLatestVersionReadsNpm(t *testing.T) {
	serveQwenCodeLatest(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"name":"@qwen-code/qwen-code","version":"0.28.0"}`))
	})

	got, err := latestQwenCodeVersion(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "0.28.0" {
		t.Errorf("latestQwenCodeVersion() = %q, want 0.28.0", got)
	}
}

func TestQwenCodeLatestVersionRejectsBadResponses(t *testing.T) {
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
			serveQwenCodeLatest(t, handler)
			if version, err := latestQwenCodeVersion(context.Background()); err == nil {
				t.Fatalf("latestQwenCodeVersion() = %q, want an error", version)
			}
		})
	}
}
