package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// githubRelease is the subset of GitHub's releases/latest payload the agent
// profiles read: the release tag ("v0.22.5").
type githubRelease struct {
	TagName string `json:"tag_name"`
}

// latestGitHubReleaseVersion returns the newest release tag (leading "v"
// stripped) from the GitHub releases/latest endpoint at url, labeling errors
// with agentName. Agents published as GitHub releases (opencode, hermes) share
// it.
func latestGitHubReleaseVersion(ctx context.Context, url, agentName string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("build latest-version request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "agents-sandbox")
	// gomutants:disable-next-line INTEGER_INCREMENT,INTEGER_DECREMENT,ARITHMETIC_BASE reason="a defensive request timeout is unobservable against a local test server that answers immediately"
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("latest %s release: %w", agentName, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("latest %s release: unexpected status %d", agentName, resp.StatusCode)
	}
	var release githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return "", fmt.Errorf("decode latest %s release: %w", agentName, err)
	}
	version := strings.TrimPrefix(release.TagName, "v")
	if version == "" {
		return "", fmt.Errorf("latest %s release: empty tag", agentName)
	}
	return version, nil
}

// latestVersionFromJSON returns the "version" field from the JSON document at
// url, labeling errors with the given agent name. Agents whose release
// endpoint returns a {"version": "..."} shape (pi.dev, the npm registry, and
// the opencode update API) share this fetcher.
func latestVersionFromJSON(ctx context.Context, url, agentName string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("build latest-version request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "agents-sandbox")
	// gomutants:disable-next-line INTEGER_INCREMENT,INTEGER_DECREMENT,ARITHMETIC_BASE reason="a defensive request timeout is unobservable against a local test server that answers immediately"
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("latest %s release: %w", agentName, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("latest %s release: unexpected status %d", agentName, resp.StatusCode)
	}
	var release struct {
		Version string `json:"version"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return "", fmt.Errorf("decode latest %s release: %w", agentName, err)
	}
	if release.Version == "" {
		return "", fmt.Errorf("latest %s release: empty version", agentName)
	}
	return release.Version, nil
}
