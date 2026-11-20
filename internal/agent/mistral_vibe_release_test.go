package agent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func serveMistralVibeLatest(t *testing.T, handler http.HandlerFunc) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	original := mistralVibePyPIURL
	mistralVibePyPIURL = server.URL
	t.Cleanup(func() { mistralVibePyPIURL = original })
}

func TestMistralVibeLatestVersionReadsPyPI(t *testing.T) {
	serveMistralVibeLatest(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"info":{"name":"mistral-vibe","version":"3.1.3"},"urls":[]}`))
	})

	got, err := latestMistralVibeVersion(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "3.1.3" {
		t.Errorf("latestMistralVibeVersion() = %q, want 3.1.3", got)
	}
}

// TestMistralVibeLatestVersionRejectsBadResponses pins each failure to its own
// error, so a fetch failure cannot pass as a document without a version.
func TestMistralVibeLatestVersionRejectsBadResponses(t *testing.T) {
	for name, tc := range map[string]struct {
		handler http.HandlerFunc
		wantErr string
	}{
		"empty version": {
			handler: func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"info":{"version":""}}`)) },
			wantErr: "empty version",
		},
		"top-level version only": {
			handler: func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"version":"3.1.3"}`)) },
			wantErr: "empty version",
		},
		"non-200": {
			handler: func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "nope", http.StatusInternalServerError) },
			wantErr: "unexpected status 500",
		},
		"undecodable": {
			handler: func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("not json")) },
			wantErr: "decode latest mistral-vibe release",
		},
	} {
		t.Run(name, func(t *testing.T) {
			serveMistralVibeLatest(t, tc.handler)
			version, err := latestMistralVibeVersion(context.Background())
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("latestMistralVibeVersion() = %q, %v; want an error containing %q", version, err, tc.wantErr)
			}
		})
	}
}
