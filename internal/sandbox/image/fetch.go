package image

import (
	"context"
	"testing"

	"github.com/inoio/opencode-sandbox/internal/agent"
	"github.com/inoio/opencode-sandbox/internal/opencode"
)

// LatestOpenCodeVersion returns the newest opencode release string as resolved
// by the opencode package (GitHub releases/latest).
func LatestOpenCodeVersion(ctx context.Context) (string, error) {
	return opencode.LatestVersion(ctx)
}

// resolveOpenCodeVersion returns the requested version when non-empty,
// otherwise it resolves the latest opencode release. Tests stub this var.
var resolveOpenCodeVersion = func(ctx context.Context, requested string) (string, error) { //nolint:gochecknoglobals // test seam
	if requested != "" {
		return requested, nil
	}
	return LatestOpenCodeVersion(ctx)
}

// resolveAgentVersion returns the requested version when non-empty, otherwise
// resolves the latest release for the agent. Agents without an UpgradeChecker
// fall back to the requested (or empty) version.
func resolveAgentVersion(ctx context.Context, a agent.Agent, requested string) (string, error) {
	if _, ok := agent.AsUpgradeChecker(a); ok {
		return resolveOpenCodeVersion(ctx, requested)
	}
	return requested, nil
}

// WithMockOpenCodeVersion redirects the image package's opencode version
// resolver to return v for any request, restoring the real resolver (which may
// hit the GitHub releases endpoint) when the test ends.
func WithMockOpenCodeVersion(t *testing.T, v string) {
	t.Helper()
	WithMockOpenCodeVersionResolver(t, func(_ context.Context, _ string) (string, error) { return v, nil })
}

// WithMockOpenCodeVersionResolver installs a custom opencode version resolver
// for a test, restoring the real one (which may hit the GitHub releases
// endpoint) when the test ends.
func WithMockOpenCodeVersionResolver(
	t *testing.T,
	resolve func(ctx context.Context, requested string) (string, error),
) {
	t.Helper()
	orig := resolveOpenCodeVersion
	resolveOpenCodeVersion = resolve
	t.Cleanup(func() { resolveOpenCodeVersion = orig })
}
