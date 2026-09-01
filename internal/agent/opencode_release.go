package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// opencodeGitHubLatestURL is a var (not const) so the tests can point it at an httptest
// server via overrideLatestURL.
//
//nolint:gochecknoglobals // test hook for the otherwise unmockable endpoint URL
var opencodeGitHubLatestURL = "https://api.github.com/repos/anomalyco/opencode/releases/latest"

type githubRelease struct {
	TagName string `json:"tag_name"`
}

// latestOpenCodeVersion returns the newest stable opencode release string (leading "v"
// stripped) by querying the GitHub releases/latest endpoint.
func latestOpenCodeVersion(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, opencodeGitHubLatestURL, nil)
	if err != nil {
		return "", fmt.Errorf("build latest-version request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "opencode-sandbox")
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("latest opencode release: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("latest opencode release: unexpected status %d", resp.StatusCode)
	}
	var release githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return "", fmt.Errorf("decode latest opencode release: %w", err)
	}
	return strings.TrimPrefix(release.TagName, "v"), nil
}

// newerOpenCodeThan reports whether a is a strictly newer semantic version than b,
// ignoring a leading "v" on either string.
func newerOpenCodeThan(a, b string) (bool, error) {
	return newerVersionThan(a, b)
}
