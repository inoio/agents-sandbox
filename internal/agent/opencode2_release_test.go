package agent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// overrideOpenCode2NpmURL swaps opencode2NpmBetaURL for the duration of a test.
// It returns a restore func.
func overrideOpenCode2NpmURL(url string) func() {
	orig := opencode2NpmBetaURL
	opencode2NpmBetaURL = url
	return func() { opencode2NpmBetaURL = orig }
}

func TestOpencode2LatestVersionReadsNpmBeta(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"version":"0.0.0-beta-18866","name":"@opencode-ai/cli"}`))
	}))
	t.Cleanup(srv.Close)
	restore := overrideOpenCode2NpmURL(srv.URL)
	t.Cleanup(restore)

	got, err := latestOpenCode2Version(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "0.0.0-beta-18866" {
		t.Errorf("latestOpenCode2Version() = %q, want 0.0.0-beta-18866", got)
	}
}

func TestOpencode2LatestVersionNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	restore := overrideOpenCode2NpmURL(srv.URL)
	t.Cleanup(restore)

	if _, err := latestOpenCode2Version(context.Background()); err == nil {
		t.Fatal("expected error on non-200 response")
	}
}

func TestOpencode2LatestVersionDecodeError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("not json"))
	}))
	t.Cleanup(srv.Close)
	restore := overrideOpenCode2NpmURL(srv.URL)
	t.Cleanup(restore)

	if _, err := latestOpenCode2Version(context.Background()); err == nil {
		t.Fatal("expected error on undecodable response")
	}
}

func TestOpencode2LatestVersionEmptyVersion(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"version":""}`))
	}))
	t.Cleanup(srv.Close)
	restore := overrideOpenCode2NpmURL(srv.URL)
	t.Cleanup(restore)

	if _, err := latestOpenCode2Version(context.Background()); err == nil {
		t.Fatal("expected error for empty version")
	}
}

func TestOpencode2NewerThanBetaBuilds(t *testing.T) {
	tests := []struct {
		name string
		a, b string
		want bool
	}{
		{name: "newer build", a: "0.0.0-beta-18866", b: "0.0.0-beta-17823", want: true},
		{name: "older build", a: "0.0.0-beta-17823", b: "0.0.0-beta-18866", want: false},
		{name: "equal build", a: "0.0.0-beta-18866", b: "0.0.0-beta-18866", want: false},
		{name: "cross digit length newer", a: "0.0.0-beta-10000", b: "0.0.0-beta-9999", want: true},
		{name: "cross digit length older", a: "0.0.0-beta-9999", b: "0.0.0-beta-10000", want: false},
		{name: "v-prefixed beta", a: "v0.0.0-beta-18866", b: "0.0.0-beta-17823", want: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := newerOpenCode2Than(tc.a, tc.b)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("newerOpenCode2Than(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
			}
		})
	}
}

func TestOpencode2NewerThanFallsBackToSemver(t *testing.T) {
	got, err := newerOpenCode2Than("1.2.0", "1.1.0")
	if err != nil || !got {
		t.Errorf("newerOpenCode2Than(1.2.0, 1.1.0) = %v, %v, want true, nil", got, err)
	}
}

func TestOpencode2NewerThanInvalidVersion(t *testing.T) {
	if _, err := newerOpenCode2Than("not-a-version", "0.0.0-beta-18866"); err == nil {
		t.Fatal("expected error for invalid first version")
	}
	if _, err := newerOpenCode2Than("0.0.0-beta-18866", "not-a-version"); err == nil {
		t.Fatal("expected error for invalid second version")
	}
}
