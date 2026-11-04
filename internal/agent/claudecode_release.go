package agent

import "context"

// claudeCodeNpmLatestURL returns the latest claude-code release from the npm
// registry's "latest" dist-tag (the version `npm install -g` would fetch).
var claudeCodeNpmLatestURL = "https://registry.npmjs.org/@anthropic-ai/claude-code/latest"

// latestClaudeCodeVersion returns the newest claude-code release string by
// querying the npm registry's "latest" dist-tag endpoint.
func latestClaudeCodeVersion(ctx context.Context) (string, error) {
	return latestVersionFromJSON(ctx, claudeCodeNpmLatestURL, "claude-code")
}
