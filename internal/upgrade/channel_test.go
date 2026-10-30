package upgrade

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// serveReleases serves the release-list API from a JSON body.
func serveReleases(t *testing.T, body string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	orig := releasesURL
	releasesURL = srv.URL + "/releases"
	t.Cleanup(func() { releasesURL = orig })
}

func TestParseChannel(t *testing.T) {
	tests := []struct {
		in      string
		want    Channel
		wantErr bool
	}{
		{in: "stable", want: ChannelStable},
		{in: "canary", want: ChannelCanary},
		{in: "CANARY", want: ChannelCanary},
		{in: " canary ", want: ChannelCanary},
		{in: "bogus", wantErr: true},
		{in: "", wantErr: true},
	}
	for _, tc := range tests {
		got, err := ParseChannel(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Fatalf("ParseChannel(%q): expected error, got %q", tc.in, got)
			}
			continue
		}
		if err != nil {
			t.Fatalf("ParseChannel(%q): unexpected error: %v", tc.in, err)
		}
		if got != tc.want {
			t.Fatalf("ParseChannel(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestCanaryVersion(t *testing.T) {
	tests := map[string]struct {
		eligible bool
		version  string
	}{
		"v1.9.0":           {eligible: true, version: "1.9.0"},
		"1.9.0":            {eligible: true, version: "1.9.0"},
		"v1.9.0-canary.1":  {eligible: true, version: "1.9.0-canary.1"},
		"v1.9.0-canary.10": {eligible: true, version: "1.9.0-canary.10"},
		"v2.0.0-alpha.1":   {},
		"v1.9.0-rc.1":      {},
		"v1.9.0-beta":      {},
		"nightly":          {},
		"v":                {},
	}
	for tag, want := range tests {
		v, eligible := canaryVersion(tag)
		if eligible != want.eligible {
			t.Errorf("canaryVersion(%q) eligible = %v, want %v", tag, eligible, want.eligible)
			continue
		}
		if !want.eligible {
			continue
		}
		if v == nil {
			t.Errorf("canaryVersion(%q) reported eligible with a nil version", tag)
			continue
		}
		if got := v.String(); got != want.version {
			t.Errorf("canaryVersion(%q) version = %q, want %q", tag, got, want.version)
		}
	}
}

func TestLatestCanaryReleaseNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	orig := releasesURL
	releasesURL = srv.URL + "/releases"
	t.Cleanup(func() { releasesURL = orig })
	if _, err := latestCanaryRelease(context.Background()); err == nil {
		t.Fatal("expected error on non-200 response")
	}
}

func TestLatestCanaryReleaseDecodeError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("not json"))
	}))
	t.Cleanup(srv.Close)
	orig := releasesURL
	releasesURL = srv.URL + "/releases"
	t.Cleanup(func() { releasesURL = orig })
	if _, err := latestCanaryRelease(context.Background()); err == nil {
		t.Fatal("expected error on undecodable response")
	}
}

func TestLatestCanaryReleaseNetworkError(t *testing.T) {
	orig := releasesURL
	releasesURL = "http://127.0.0.1:1"
	t.Cleanup(func() { releasesURL = orig })
	if _, err := latestCanaryRelease(context.Background()); err == nil {
		t.Fatal("expected error on unreachable releases endpoint")
	}
}

func TestLatestCanaryReleaseBadURL(t *testing.T) {
	orig := releasesURL
	releasesURL = "://bad"
	t.Cleanup(func() { releasesURL = orig })
	if _, err := latestCanaryRelease(context.Background()); err == nil {
		t.Fatal("expected error on unparsable releases URL")
	}
}

func TestLatestForChannelCanaryPicksNewest(t *testing.T) {
	serveReleases(t, `[
		{"tag_name":"v2.0.0-alpha.1","draft":false},
		{"tag_name":"v1.9.0-canary.2","draft":false},
		{"tag_name":"v1.9.0-canary.10","draft":false},
		{"tag_name":"v1.8.0","draft":false},
		{"tag_name":"v1.9.0-canary.11","draft":true}
	]`)
	got, err := latestForChannel(context.Background(), ChannelCanary)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "1.9.0-canary.10" {
		t.Fatalf("latestForChannel(canary) = %q, want 1.9.0-canary.10", got)
	}
}

func TestLatestForChannelCanarySkipsUnusable(t *testing.T) {
	serveReleases(t, `[
		{"tag_name":"not-a-version","draft":false},
		{"tag_name":"v1.9.0-canary.1","draft":false}
	]`)
	got, err := latestForChannel(context.Background(), ChannelCanary)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "1.9.0-canary.1" {
		t.Fatalf("got %q, want 1.9.0-canary.1", got)
	}
}

func TestLatestForChannelCanaryNoEligible(t *testing.T) {
	serveReleases(t, `[{"tag_name":"v2.0.0-alpha.1","draft":false}]`)
	if _, err := latestForChannel(context.Background(), ChannelCanary); err == nil {
		t.Fatal("expected an error when no release is canary-eligible")
	}
}

func TestLatestForChannelCanaryRollsForwardToRelease(t *testing.T) {
	// Once the real release ships it outranks its own canaries, so the canary
	// pool moves onto it with no special rule.
	serveReleases(t, `[
		{"tag_name":"v1.9.0-canary.3","draft":false},
		{"tag_name":"v1.9.0","draft":false}
	]`)
	got, err := latestForChannel(context.Background(), ChannelCanary)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "1.9.0" {
		t.Fatalf("got %q, want 1.9.0", got)
	}
}

func TestLatestForChannelStableUsesLatest(t *testing.T) {
	serveLatest(t, "1.8.0", nil)
	serveReleases(t, `[{"tag_name":"v1.9.0-canary.1","draft":false}]`)
	for _, ch := range []Channel{ChannelStable, ""} {
		got, err := latestForChannel(context.Background(), ch)
		if err != nil {
			t.Fatalf("channel %q: unexpected error: %v", ch, err)
		}
		if got != "1.8.0" {
			t.Fatalf("channel %q = %q, want 1.8.0 (the stable release)", ch, got)
		}
	}
}
