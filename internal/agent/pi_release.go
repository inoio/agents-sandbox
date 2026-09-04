package agent

import "context"

// piDevLatestURL returns the latest pi release. It is a var (not const) so the
// tests can point it at an httptest server.
//
//nolint:gochecknoglobals // test hook for the otherwise unmockable endpoint URL
var piDevLatestURL = "https://pi.dev/api/latest-version"

// latestPIVersion returns the newest stable pi release string by querying
// pi.dev's latest-version endpoint.
func latestPIVersion(ctx context.Context) (string, error) {
	return latestVersionFromJSON(ctx, piDevLatestURL, "pi")
}
