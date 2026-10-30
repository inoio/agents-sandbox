package agent

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/Masterminds/semver/v3"
)

// overrideLatestURL swaps opencodeGitHubLatestURL for the duration of a test. It returns
// a restore func.
func overrideLatestURL(url string) func() {
	orig := opencodeGitHubLatestURL
	opencodeGitHubLatestURL = url
	return func() { opencodeGitHubLatestURL = orig }
}

func TestLatestVersionReadsGitHubRelease(t *testing.T) {
	var accept, userAgent string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		accept = r.Header.Get("Accept")
		userAgent = r.Header.Get("User-Agent")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"tag_name":"v1.2.3"}`))
	}))
	t.Cleanup(srv.Close)

	restore := overrideLatestURL(srv.URL)
	t.Cleanup(restore)

	got, err := latestOpenCodeVersion(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "1.2.3" {
		t.Errorf("LatestVersion() = %q, want %q", got, "1.2.3")
	}
	if accept != "application/json" {
		t.Errorf("Accept header = %q, want application/json", accept)
	}
	if userAgent != "agents-sandbox" {
		t.Errorf("User-Agent header = %q, want agents-sandbox", userAgent)
	}
}

func TestLatestVersionStripsVBeta(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"tag_name":"v4.0.0-beta.1"}`))
	}))
	t.Cleanup(srv.Close)
	restore := overrideLatestURL(srv.URL)
	t.Cleanup(restore)

	got, err := latestOpenCodeVersion(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "4.0.0-beta.1" {
		t.Errorf("LatestVersion() = %q, want %q", got, "4.0.0-beta.1")
	}
}

func TestLatestVersionPropagatesRequestError(t *testing.T) {
	restore := overrideLatestURL("http://127.0.0.1:1")
	t.Cleanup(restore)

	_, err := latestOpenCodeVersion(context.Background())
	if _, ok := errors.AsType[*url.Error](err); !ok {
		t.Fatalf("error %v does not wrap the transport cause", err)
	}
}

func TestLatestVersionBuildRequestError(t *testing.T) {
	restore := overrideLatestURL("://bad")
	t.Cleanup(restore)

	_, err := latestOpenCodeVersion(context.Background())
	if _, ok := errors.AsType[*url.Error](err); !ok {
		t.Fatalf("error %v does not wrap the request-building cause", err)
	}
}

func TestLatestVersionNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	restore := overrideLatestURL(srv.URL)
	t.Cleanup(restore)

	_, err := latestOpenCodeVersion(context.Background())
	if err == nil || !strings.Contains(err.Error(), "unexpected status") {
		t.Fatalf("error = %v, want an unexpected-status error", err)
	}
}

func TestLatestVersionDecodeError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("not json"))
	}))
	t.Cleanup(srv.Close)
	restore := overrideLatestURL(srv.URL)
	t.Cleanup(restore)

	_, err := latestOpenCodeVersion(context.Background())
	if _, ok := errors.AsType[*json.SyntaxError](err); !ok {
		t.Fatalf("error %v does not wrap the *json.SyntaxError cause", err)
	}
}

func TestNewerThan(t *testing.T) {
	tests := []struct {
		name string
		a, b string
		want bool
	}{
		{name: "newer", a: "1.1.0", b: "1.0.0", want: true},
		{name: "older", a: "1.0.0", b: "1.1.0", want: false},
		{name: "equal", a: "1.0.0", b: "1.0.0", want: false},
		{name: "v-prefix a", a: "v1.1.0", b: "1.0.0", want: true},
		{name: "v-prefix b", a: "1.1.0", b: "v1.0.0", want: true},
		{name: "prerelease not newer", a: "1.0.0-beta.1", b: "1.0.0", want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := newerOpenCodeThan(tc.a, tc.b)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("NewerThan(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
			}
		})
	}
}

func TestNewerThanInvalidVersion(t *testing.T) {
	for _, pair := range [][2]string{{"not-a-version", "1.0.0"}, {"1.0.0", "not-a-version"}} {
		newer, err := newerOpenCodeThan(pair[0], pair[1])
		if newer {
			t.Errorf("newerOpenCodeThan(%q, %q) = true on a parse error, want false", pair[0], pair[1])
		}
		if !errors.Is(err, semver.ErrInvalidSemVer) {
			t.Fatalf("error %v does not wrap semver.ErrInvalidSemVer", err)
		}
	}
}
