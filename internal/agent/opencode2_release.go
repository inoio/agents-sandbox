package agent

import (
	"context"
	"strconv"
	"strings"
)

// opencode2UpdateAPIURL is the opencode update API's "latest" channel for the
// CLI, the same endpoint the v2 installer (`https://opencode.ai/v2/install`)
// queries when it resolves an unpinned version. Resolving through it honors the
// update API's channel/active gating instead of reading an npm dist-tag, and it
// returns the canonical release the installer would fetch (currently
// @opencode/cli 2.x). It is a var (not const) so the tests can point it at an
// httptest server.
var opencode2UpdateAPIURL = "https://opencode.ai/update/api/latest/cli/npm"

// latestOpenCode2Version returns the newest opencode 2 release string by
// querying the update API's "latest" channel.
func latestOpenCode2Version(ctx context.Context) (string, error) {
	return latestVersionFromJSON(ctx, opencode2UpdateAPIURL, "opencode2")
}

// opencode2BetaPrefix is the version shape of the opencode 2 beta snapshots
// published during v2's pre-GA period: 0.0.0-beta-<build>. Resolution no longer
// yields this shape (the update API's latest channel returns 2.x), but a
// project's previously recorded version — or a cached latest in RootState — can
// still be a beta, so the comparison below stays.
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
// across digit-length changes (e.g., 0.0.0-beta-9999 vs 0.0.0-beta-10000), so
// the build number is compared numerically when both versions match the beta
// shape. Any other version falls back to plain semver comparison. The beta path
// is legacy-only: resolution returns 2.x, so it fires only when a recorded or
// cached version is still a pre-GA snapshot.
func newerOpenCode2Than(a, b string) (bool, error) {
	an, aBeta := opencode2BetaBuildNumber(a)
	bn, bBeta := opencode2BetaBuildNumber(b)
	if aBeta && bBeta {
		return an > bn, nil
	}
	return newerVersionThan(a, b)
}
