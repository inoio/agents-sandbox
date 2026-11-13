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
)

func TestLatestVersionFromJSONBuildRequestError(t *testing.T) {
	_, err := latestVersionFromJSON(context.Background(), "://bad", "test")
	if _, ok := errors.AsType[*url.Error](err); !ok {
		t.Fatalf("error %v does not wrap the request-building cause", err)
	}
}

func TestLatestVersionFromJSONSendsJSONHeaders(t *testing.T) {
	var accept, userAgent string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		accept = r.Header.Get("Accept")
		userAgent = r.Header.Get("User-Agent")
		_, _ = w.Write([]byte(`{"version":"1.2.3"}`))
	}))
	t.Cleanup(srv.Close)

	got, err := latestVersionFromJSON(context.Background(), srv.URL, "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "1.2.3" {
		t.Errorf("latestVersionFromJSON() = %q, want 1.2.3", got)
	}
	if accept != "application/json" {
		t.Errorf("Accept header = %q, want application/json", accept)
	}
	if userAgent != "agents-sandbox" {
		t.Errorf("User-Agent header = %q, want agents-sandbox", userAgent)
	}
}

func TestLatestVersionFromJSONPropagatesRequestError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := latestVersionFromJSON(ctx, "http://127.0.0.1:1", "test"); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want it to wrap context.Canceled", err)
	}
}

func TestLatestVersionFromJSONNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	_, err := latestVersionFromJSON(context.Background(), srv.URL, "test")
	if err == nil || !strings.Contains(err.Error(), "unexpected status") {
		t.Fatalf("error = %v, want an unexpected-status error", err)
	}
}

func TestLatestVersionFromJSONDecodeError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("not json"))
	}))
	t.Cleanup(srv.Close)

	_, err := latestVersionFromJSON(context.Background(), srv.URL, "test")
	if err == nil || !strings.Contains(err.Error(), "decode latest") {
		t.Fatalf("error = %v, want a decode error, not the empty-version fallthrough", err)
	}
	if _, ok := errors.AsType[*json.SyntaxError](err); !ok {
		t.Fatalf("error %v does not wrap the *json.SyntaxError cause", err)
	}
}

func authorizeTestRequest(t *testing.T, target string) *http.Request {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	return req
}

func TestAuthorizeGitHubRequestSetsBearerToken(t *testing.T) {
	t.Setenv("GH_TOKEN", "ghp_token")
	t.Setenv("GITHUB_TOKEN", "")

	req := authorizeTestRequest(t, "https://api.github.com/repos/inoio/agents-sandbox/releases/latest")
	authorizeGitHubRequest(req)

	if got := req.Header.Get("Authorization"); got != "Bearer ghp_token" {
		t.Errorf("Authorization = %q, want %q", got, "Bearer ghp_token")
	}
}

func TestAuthorizeGitHubRequestLeavesOtherHostsUnauthorized(t *testing.T) {
	t.Setenv("GH_TOKEN", "ghp_token")

	req := authorizeTestRequest(t, "https://registry.npmjs.org/@anthropic-ai/claude-code/latest")
	authorizeGitHubRequest(req)

	if got := req.Header.Get("Authorization"); got != "" {
		t.Errorf("Authorization = %q, want none for a non-GitHub host", got)
	}
}

func TestAuthorizeGitHubRequestWithoutTokenSetsNoHeader(t *testing.T) {
	t.Setenv("GH_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")

	req := authorizeTestRequest(t, "https://api.github.com/repos/inoio/agents-sandbox/releases/latest")
	authorizeGitHubRequest(req)

	if got := req.Header.Get("Authorization"); got != "" {
		t.Errorf("Authorization = %q, want none without a token", got)
	}
}

func TestAuthorizeGitHubRequestPrefersGHToken(t *testing.T) {
	t.Setenv("GH_TOKEN", "ghp_gh")
	t.Setenv("GITHUB_TOKEN", "ghp_actions")

	req := authorizeTestRequest(t, "https://api.github.com/repos/inoio/agents-sandbox/releases/latest")
	authorizeGitHubRequest(req)

	if got := req.Header.Get("Authorization"); got != "Bearer ghp_gh" {
		t.Errorf("Authorization = %q, want %q", got, "Bearer ghp_gh")
	}
}

func TestAuthorizeGitHubRequestTrimsTokenWhitespace(t *testing.T) {
	t.Setenv("GH_TOKEN", "  ghp_token\n")
	t.Setenv("GITHUB_TOKEN", "")

	req := authorizeTestRequest(t, "https://api.github.com/repos/inoio/agents-sandbox/releases/latest")
	authorizeGitHubRequest(req)

	if got := req.Header.Get("Authorization"); got != "Bearer ghp_token" {
		t.Errorf("Authorization = %q, want the token trimmed to %q", got, "Bearer ghp_token")
	}
}

func TestFetchReleaseJSONAuthorizesGitHubRequests(t *testing.T) {
	t.Setenv("GH_TOKEN", "ghp_token")

	var authorization string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorization = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"version":"1.2.3"}`))
	}))
	t.Cleanup(srv.Close)

	srvURL, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("parse test server URL: %v", err)
	}
	origHost := githubAPIHost
	githubAPIHost = srvURL.Host
	t.Cleanup(func() { githubAPIHost = origHost })

	if _, err := latestVersionFromJSON(context.Background(), srv.URL, "test"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if authorization != "Bearer ghp_token" {
		t.Errorf("Authorization = %q, want %q", authorization, "Bearer ghp_token")
	}
}

func TestLatestVersionFromJSONDoesNotSendTokenToOtherHosts(t *testing.T) {
	t.Setenv("GH_TOKEN", "ghp_token")

	authorization := "unset"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorization = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"version":"1.2.3"}`))
	}))
	t.Cleanup(srv.Close)

	if _, err := latestVersionFromJSON(context.Background(), srv.URL, "test"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if authorization != "" {
		t.Errorf("Authorization = %q, want none for a non-GitHub host", authorization)
	}
}
