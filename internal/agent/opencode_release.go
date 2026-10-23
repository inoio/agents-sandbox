package agent

import "context"

// opencodeGitHubLatestURL is a var (not const) so the tests can point it at an httptest
// server via overrideLatestURL.
var opencodeGitHubLatestURL = "https://api.github.com/repos/anomalyco/opencode/releases/latest"

// latestOpenCodeVersion returns the newest stable opencode release string (leading "v"
// stripped) by querying the GitHub releases/latest endpoint.
func latestOpenCodeVersion(ctx context.Context) (string, error) {
	return latestGitHubReleaseVersion(ctx, opencodeGitHubLatestURL, opencodeName)
}

// newerOpenCodeThan reports whether a is a strictly newer semantic version than b,
// ignoring a leading "v" on either string.
func newerOpenCodeThan(a, b string) (bool, error) {
	return newerVersionThan(a, b)
}
