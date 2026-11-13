package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

// latestVersionFromJSON returns the "version" field from the JSON document at
// url, labeling errors with the given agent name. Agents whose release
// endpoint returns a {"version": "..."} shape (pi.dev, the npm registry, and
// the opencode update API) share this fetcher.
func latestVersionFromJSON(ctx context.Context, url, agentName string) (string, error) {
	var release struct {
		Version string `json:"version"`
	}
	if err := fetchReleaseJSON(ctx, url, agentName, &release); err != nil {
		return "", err
	}
	if release.Version == "" {
		return "", fmt.Errorf("latest %s release: empty version", agentName)
	}
	return release.Version, nil
}

// latestGitHubReleaseTag returns the tag_name of the GitHub releases/latest
// document at url, unmodified.
func latestGitHubReleaseTag(ctx context.Context, url, agentName string) (string, error) {
	var release struct {
		TagName string `json:"tag_name"`
	}
	if err := fetchReleaseJSON(ctx, url, agentName, &release); err != nil {
		return "", err
	}
	return release.TagName, nil
}

// githubTokenEnvVars holds the environment variables a GitHub API token is read
// from, in precedence order (GH_TOKEN wins, matching gh).
var githubTokenEnvVars = []string{"GH_TOKEN", "GITHUB_TOKEN"}

// githubToken returns the first non-empty, whitespace-trimmed GitHub API token
// from the environment, or "" when none is set.
func githubToken() string {
	for _, name := range githubTokenEnvVars {
		if token := strings.TrimSpace(os.Getenv(name)); token != "" {
			return token
		}
	}
	return ""
}

// githubAPIHost is the only host a resolved GitHub token is sent to. It is a var
// so a test can point it at a local server.
var githubAPIHost = "api.github.com"

// authorizeGitHubRequest authenticates req with the environment's GitHub token
// when req targets the GitHub API, reporting whether it did. Scoping to the host
// keeps the token off the other agents' release endpoints (npm, pi.dev,
// opencode.ai).
func authorizeGitHubRequest(req *http.Request) bool {
	if req.URL.Host != githubAPIHost {
		return false
	}
	token := githubToken()
	if token == "" {
		return false
	}
	req.Header.Set("Authorization", "Bearer "+token)
	return true
}

// fetchReleaseJSON decodes the JSON document at url into target, labeling
// errors with the given agent name.
//
// It authenticates GitHub requests with the environment's token, then retries
// once without it when the authenticated request fails: an expired token, a
// fine-grained token without access to the repository, or an org IP allow list
// can make GitHub reject a request that anonymous access would serve, so a bad
// token degrades to the anonymous read rather than failing the lookup.
func fetchReleaseJSON(ctx context.Context, url, agentName string, target any) error {
	resp, authenticated, err := sendReleaseRequest(ctx, url, agentName, true)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK && authenticated {
		_ = resp.Body.Close()
		resp, _, err = sendReleaseRequest(ctx, url, agentName, false)
		if err != nil {
			return err
		}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("latest %s release: unexpected status %d", agentName, resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(target); err != nil {
		return fmt.Errorf("decode latest %s release: %w", agentName, err)
	}
	return nil
}

// sendReleaseRequest builds and sends the latest-release request. When withToken
// is true and a GitHub token is available for a GitHub host, the request is
// authenticated; the boolean reports whether it was.
func sendReleaseRequest(ctx context.Context, url, agentName string, withToken bool) (*http.Response, bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, false, fmt.Errorf("build latest-version request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "agents-sandbox")
	authenticated := withToken && authorizeGitHubRequest(req)
	// gomutants:disable-next-line INTEGER_INCREMENT,INTEGER_DECREMENT,ARITHMETIC_BASE reason="a defensive request timeout is unobservable against a local test server that answers immediately"
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, authenticated, fmt.Errorf("latest %s release: %w", agentName, err)
	}
	return resp, authenticated, nil
}
