package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
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

// fetchReleaseJSON decodes the JSON document at url into target, labeling
// errors with the given agent name.
func fetchReleaseJSON(ctx context.Context, url, agentName string, target any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("build latest-version request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "agents-sandbox")
	// gomutants:disable-next-line INTEGER_INCREMENT,INTEGER_DECREMENT,ARITHMETIC_BASE reason="a defensive request timeout is unobservable against a local test server that answers immediately"
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("latest %s release: %w", agentName, err)
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
