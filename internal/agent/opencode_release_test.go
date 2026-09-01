package agent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// overrideLatestURL swaps opencodeGitHubLatestURL for the duration of a test. It returns
// a restore func.
func overrideLatestURL(url string) func() {
	orig := opencodeGitHubLatestURL
	opencodeGitHubLatestURL = url
	return func() { opencodeGitHubLatestURL = orig }
}

func TestLatestVersionReadsGitHubRelease(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
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

	if _, err := latestOpenCodeVersion(context.Background()); err == nil {
		t.Fatal("expected error for unreachable endpoint")
	}
}

func TestLatestVersionNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	restore := overrideLatestURL(srv.URL)
	t.Cleanup(restore)

	if _, err := latestOpenCodeVersion(context.Background()); err == nil {
		t.Fatal("expected error on non-200 response")
	}
}

func TestLatestVersionDecodeError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("not json"))
	}))
	t.Cleanup(srv.Close)
	restore := overrideLatestURL(srv.URL)
	t.Cleanup(restore)

	if _, err := latestOpenCodeVersion(context.Background()); err == nil {
		t.Fatal("expected error on undecodable response")
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
	if _, err := newerOpenCodeThan("not-a-version", "1.0.0"); err == nil {
		t.Fatal("expected error for invalid first version")
	}
	if _, err := newerOpenCodeThan("1.0.0", "not-a-version"); err == nil {
		t.Fatal("expected error for invalid second version")
	}
}
