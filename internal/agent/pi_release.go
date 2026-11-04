package agent

import "context"

// piDevLatestURL returns the latest pi release.
var piDevLatestURL = "https://pi.dev/api/latest-version"

// latestPIVersion returns the newest stable pi release string by querying
// pi.dev's latest-version endpoint.
func latestPIVersion(ctx context.Context) (string, error) {
	return latestVersionFromJSON(ctx, piDevLatestURL, "pi")
}
