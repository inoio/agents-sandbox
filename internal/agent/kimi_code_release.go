package agent

import "context"

// kimiCodeNpmLatestURL is the npm registry's "latest" dist-tag of the Kimi Code
// CLI. The GitHub repository also publishes the desktop app, whose release is
// what releases/latest returns, so the npm package that shares the CLI release's
// version is the authoritative resolver.
var kimiCodeNpmLatestURL = "https://registry.npmjs.org/@moonshot-ai/kimi-code/latest"

// latestKimiCodeVersion returns the newest Kimi Code CLI release.
func latestKimiCodeVersion(ctx context.Context) (string, error) {
	return latestVersionFromJSON(ctx, kimiCodeNpmLatestURL, kimiCodeName)
}
