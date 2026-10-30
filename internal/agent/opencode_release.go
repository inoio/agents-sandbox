package agent

import (
	"context"
	"strings"
)

// opencodeGitHubLatestURL is a var (not const) so the tests can point it at an httptest
// server via overrideLatestURL.
var opencodeGitHubLatestURL = "https://api.github.com/repos/anomalyco/opencode/releases/latest"

// latestOpenCodeVersion returns the newest stable opencode release string (leading "v"
// stripped) by querying the GitHub releases/latest endpoint.
func latestOpenCodeVersion(ctx context.Context) (string, error) {
	tag, err := latestGitHubReleaseTag(ctx, opencodeGitHubLatestURL, "opencode")
	if err != nil {
		return "", err
	}
	return strings.TrimPrefix(tag, "v"), nil
}

// newerOpenCodeThan reports whether a is a strictly newer semantic version than b,
// ignoring a leading "v" on either string.
func newerOpenCodeThan(a, b string) (bool, error) {
	return newerVersionThan(a, b)
}
