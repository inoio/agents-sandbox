package agent

import "context"

// grokBuildNpmLatestURL is the npm registry's "latest" dist-tag of Grok Build's
// launcher package, which tracks the CLI's stable channel. The release binaries
// are hosted only by xAI (there is no GitHub release), so the npm package that
// shares their version is the resolver.
var grokBuildNpmLatestURL = "https://registry.npmjs.org/@xai-official/grok/latest"

// latestGrokBuildVersion returns the newest stable Grok Build release.
func latestGrokBuildVersion(ctx context.Context) (string, error) {
	return latestVersionFromJSON(ctx, grokBuildNpmLatestURL, grokBuildName)
}
