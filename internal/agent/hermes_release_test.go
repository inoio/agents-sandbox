package agent

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
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

// TestHermesLatestVersionRejectsMalformedTags covers every tag the version
// parser must refuse: an empty tag, a tag without the "v" prefix, and a bare
// "v" carrying no digits. A tag that is found but strips to an empty version
// exercises the guard's second operand, while one that is not found exercises
// the first, so neither operand can be dropped undetected.
func TestHermesLatestVersionRejectsMalformedTags(t *testing.T) {
	cases := []struct {
		name string
		tag  string
	}{
		{"empty tag", ""},
		{"no v prefix", "0.22.5"},
		{"bare v", "v"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = fmt.Fprintf(w, `{"tag_name":%q}`, c.tag)
			}))
			t.Cleanup(srv.Close)
			t.Cleanup(overrideHermesLatestURL(srv.URL))

			_, err := latestHermesVersion(context.Background())
			if err == nil {
				t.Fatalf("expected error for tag %q", c.tag)
			}
			if !strings.Contains(err.Error(), "unexpected tag") {
				t.Errorf("error = %v, want it to name the unexpected tag", err)
			}
		})
	}
}

func TestHermesLatestVersionNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(overrideHermesLatestURL(srv.URL))

	_, err := latestHermesVersion(context.Background())
	if err == nil {
		t.Fatal("expected error on non-200 response")
	}
	// The transport error must propagate unchanged, not be replaced by the
	// tag-validation error a dropped guard would fall through to.
	if !strings.Contains(err.Error(), "unexpected status") {
		t.Errorf("error = %v, want the non-200 transport error", err)
	}
}

func TestHermesLatestVersionDecodeError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("not json"))
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(overrideHermesLatestURL(srv.URL))

	_, err := latestHermesVersion(context.Background())
	if err == nil {
		t.Fatal("expected error on undecodable response")
	}
	if !strings.Contains(err.Error(), "decode latest hermes release") {
		t.Errorf("error = %v, want the decode transport error", err)
	}
}
