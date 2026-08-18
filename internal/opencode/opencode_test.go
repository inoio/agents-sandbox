package opencode

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// overrideLatestURL swaps gitHubLatestURL for the duration of a test. It returns
// a restore func.
func overrideLatestURL(url string) func() {
	orig := gitHubLatestURL
	gitHubLatestURL = url
	return func() { gitHubLatestURL = orig }
}

func TestLatestVersionReadsGitHubRelease(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"tag_name":"v1.2.3"}`))
	}))
	t.Cleanup(srv.Close)

	restore := overrideLatestURL(srv.URL)
	t.Cleanup(restore)

	got, err := LatestVersion(context.Background())
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

	got, err := LatestVersion(context.Background())
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

	if _, err := LatestVersion(context.Background()); err == nil {
		t.Fatal("expected error for unreachable endpoint")
	}
}

func TestVersionCompareNumerical(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.9.0", "1.10.0", -1},
		{"1.10.0", "1.9.0", 1},
		{"1.2.3", "1.2.3", 0},
		{"1.2.3", "1.2.4", -1},
		{"2.0.0", "1.99.99", 1},
		{"v1.2.3", "1.2.4", -1},
		{"1.2", "1.2.0", 0},
	}
	for _, tc := range cases {
		got := VersionCompare(tc.a, tc.b)
		switch {
		case tc.want < 0 && got >= 0:
			t.Errorf("VersionCompare(%q,%q) = %d, want < 0", tc.a, tc.b, got)
		case tc.want > 0 && got <= 0:
			t.Errorf("VersionCompare(%q,%q) = %d, want > 0", tc.a, tc.b, got)
		case tc.want == 0 && got != 0:
			t.Errorf("VersionCompare(%q,%q) = %d, want 0", tc.a, tc.b, got)
		}
	}
}

func TestVersionCompareReturnsBakedDirection(t *testing.T) {
	// Convenience directional tests matching how the reconcile hook uses it.
	if VersionCompare("1.0.0", "1.1.0") >= 0 {
		t.Error("expected older < newer")
	}
	if VersionCompare("1.2.0", "1.1.0") <= 0 {
		t.Error("expected newer > older")
	}
}
