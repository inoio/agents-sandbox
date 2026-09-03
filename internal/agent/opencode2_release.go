package agent

import (
	"context"
	"strconv"
	"strings"
)

// opencode2NpmBetaURL resolves the newest opencode 2 beta release from the npm
// registry's "beta" dist-tag (the version `npm install -g @opencode-ai/cli@beta`
// would fetch). It is a var (not const) so the tests can point it at an
// httptest server.
//
//nolint:gochecknoglobals // test hook for the otherwise unmockable endpoint URL
var opencode2NpmBetaURL = "https://registry.npmjs.org/@opencode-ai/cli/beta"

// latestOpenCode2Version returns the newest opencode 2 beta release string by
// querying the npm registry's "beta" dist-tag endpoint.
func latestOpenCode2Version(ctx context.Context) (string, error) {
	return latestVersionFromJSON(ctx, opencode2NpmBetaURL, "opencode2")
}

// opencode2BetaPrefix is the version shape npm publishes for the opencode 2
// beta: 0.0.0-beta-<build>.
const opencode2BetaPrefix = "0.0.0-beta-"

// opencode2BetaBuildNumber extracts the trailing build number from a
// "0.0.0-beta-<build>" version, optionally v-prefixed. ok is false when v does
// not match the beta shape.
func opencode2BetaBuildNumber(v string) (int, bool) {
	v = strings.TrimPrefix(v, "v")
	if !strings.HasPrefix(v, opencode2BetaPrefix) {
		return 0, false
	}
	build := strings.TrimPrefix(v, opencode2BetaPrefix)
	n, err := strconv.Atoi(build)
	if err != nil {
		return 0, false
	}
	return n, true
}

// newerOpenCode2Than reports whether a is a strictly newer opencode 2 release
// than b. Beta versions are "0.0.0-beta-<build>" where <build> is a
// monotonically increasing integer; comparing them as plain semver breaks
// across digit-length changes (e.g. 0.0.0-beta-9999 vs 0.0.0-beta-10000), so
// the build number is compared numerically when both versions match the beta
// shape. Any other version falls back to plain semver comparison.
func newerOpenCode2Than(a, b string) (bool, error) {
	an, aBeta := opencode2BetaBuildNumber(a)
	bn, bBeta := opencode2BetaBuildNumber(b)
	if aBeta && bBeta {
		return an > bn, nil
	}
	return newerVersionThan(a, b)
}
