package agent

import "context"

// qwenCodeNpmLatestURL is the npm registry's "latest" dist-tag of the Qwen Code
// CLI. The GitHub repository also publishes its TypeScript SDK and desktop app,
// one of which is what releases/latest returns, so the npm package that shares
// the CLI release's version is the authoritative resolver.
var qwenCodeNpmLatestURL = "https://registry.npmjs.org/@qwen-code/qwen-code/latest"

// latestQwenCodeVersion returns the newest Qwen Code CLI release.
func latestQwenCodeVersion(ctx context.Context) (string, error) {
	return latestVersionFromJSON(ctx, qwenCodeNpmLatestURL, qwenCodeName)
}
