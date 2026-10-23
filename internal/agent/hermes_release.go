package agent

import "context"

// hermesGitHubLatestURL is a var (not const) so the tests can point it at an
// httptest server.
var hermesGitHubLatestURL = "https://api.github.com/repos/NousResearch/hermes-agent/releases/latest"

// latestHermesVersion returns the newest stable Hermes release string (leading
// "v" stripped) by querying the GitHub releases/latest endpoint. Hermes tags
// releases vX.Y.Z and its own installer resolves the stable channel from the
// same endpoint.
func latestHermesVersion(ctx context.Context) (string, error) {
	return latestGitHubReleaseVersion(ctx, hermesGitHubLatestURL, hermesName)
}
