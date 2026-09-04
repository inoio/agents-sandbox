package agent

import "context"

// claudeCodeNpmLatestURL returns the latest claude-code release from the npm
// registry's "latest" dist-tag (the version `npm install -g` would fetch). It
// is a var (not const) so the tests can point it at an httptest server.
//
//nolint:gochecknoglobals // test hook for the otherwise unmockable endpoint URL
var claudeCodeNpmLatestURL = "https://registry.npmjs.org/@anthropic-ai/claude-code/latest"

// latestClaudeCodeVersion returns the newest claude-code release string by
// querying the npm registry's "latest" dist-tag endpoint.
func latestClaudeCodeVersion(ctx context.Context) (string, error) {
	return latestVersionFromJSON(ctx, claudeCodeNpmLatestURL, "claude-code")
}
