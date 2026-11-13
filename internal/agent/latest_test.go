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

	if !authorizeGitHubRequest(req) {
		t.Error("authorizeGitHubRequest() = false, want true with a token for the GitHub host")
	}
	if got := req.Header.Get("Authorization"); got != "Bearer ghp_token" {
		t.Errorf("Authorization = %q, want %q", got, "Bearer ghp_token")
	}
}

func TestAuthorizeGitHubRequestLeavesOtherHostsUnauthorized(t *testing.T) {
	t.Setenv("GH_TOKEN", "ghp_token")

	req := authorizeTestRequest(t, "https://registry.npmjs.org/@anthropic-ai/claude-code/latest")

	if authorizeGitHubRequest(req) {
		t.Error("authorizeGitHubRequest() = true, want false for a non-GitHub host")
	}
	if got := req.Header.Get("Authorization"); got != "" {
		t.Errorf("Authorization = %q, want none for a non-GitHub host", got)
	}
}

func TestAuthorizeGitHubRequestWithoutTokenSetsNoHeader(t *testing.T) {
	t.Setenv("GH_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")

	req := authorizeTestRequest(t, "https://api.github.com/repos/inoio/agents-sandbox/releases/latest")

	if authorizeGitHubRequest(req) {
		t.Error("authorizeGitHubRequest() = true, want false without a token")
	}
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

	requests := 0
	var authorization string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		authorization = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"version":"1.2.3"}`))
	}))
	t.Cleanup(srv.Close)
	overrideGitHubAPIHost(t, srv.URL)

	if _, err := latestVersionFromJSON(context.Background(), srv.URL, "test"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if authorization != "Bearer ghp_token" {
		t.Errorf("Authorization = %q, want %q", authorization, "Bearer ghp_token")
	}
	if requests != 1 {
		t.Errorf("requests = %d, want 1: a successful request is not retried", requests)
	}
}

// overrideGitHubAPIHost points githubAPIHost at serverURL's host for the rest of
// the test, so a request to the local server takes the GitHub-host path.
func overrideGitHubAPIHost(t *testing.T, serverURL string) {
	t.Helper()
	parsed, err := url.Parse(serverURL)
	if err != nil {
		t.Fatalf("parse test server URL: %v", err)
	}
	original := githubAPIHost
	githubAPIHost = parsed.Host
	t.Cleanup(func() { githubAPIHost = original })
}

func TestFetchReleaseJSONRetriesWithoutTokenWhenAuthenticatedRequestFails(t *testing.T) {
	t.Setenv("GH_TOKEN", "ghp_expired")
	t.Setenv("GITHUB_TOKEN", "")

	var authorizations []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorizations = append(authorizations, r.Header.Get("Authorization"))
		if len(authorizations) == 1 {
			http.Error(w, "bad credentials", http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"version":"1.2.3"}`))
	}))
	t.Cleanup(srv.Close)
	overrideGitHubAPIHost(t, srv.URL)

	got, err := latestVersionFromJSON(context.Background(), srv.URL, "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "1.2.3" {
		t.Errorf("version = %q, want %q", got, "1.2.3")
	}
	if len(authorizations) != 2 || authorizations[0] != "Bearer ghp_expired" || authorizations[1] != "" {
		t.Errorf("Authorization headers = %q, want [%q %q]", authorizations, "Bearer ghp_expired", "")
	}
}

func TestFetchReleaseJSONDoesNotRetryAnAnonymousRequest(t *testing.T) {
	t.Setenv("GH_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")

	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		http.Error(w, "nope", http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	_, err := latestVersionFromJSON(context.Background(), srv.URL, "test")
	if err == nil || !strings.Contains(err.Error(), "unexpected status") {
		t.Fatalf("error = %v, want an unexpected-status error", err)
	}
	if requests != 1 {
		t.Errorf("requests = %d, want 1: a request with no token has nothing to fall back from", requests)
	}
}

func TestFetchReleaseJSONReportsTheRetryStatus(t *testing.T) {
	t.Setenv("GH_TOKEN", "ghp_expired")
	t.Setenv("GITHUB_TOKEN", "")

	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		if requests == 1 {
			http.Error(w, "bad credentials", http.StatusUnauthorized)
			return
		}
		http.Error(w, "not found", http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)
	overrideGitHubAPIHost(t, srv.URL)

	_, err := latestVersionFromJSON(context.Background(), srv.URL, "test")
	if err == nil || !strings.Contains(err.Error(), "unexpected status 404") {
		t.Fatalf("error = %v, want the unauthenticated retry's status 404", err)
	}
	if requests != 2 {
		t.Errorf("requests = %d, want 2", requests)
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
