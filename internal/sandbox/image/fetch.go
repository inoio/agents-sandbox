package image

import (
	"context"
	"testing"

	"github.com/inoio/agents-sandbox/internal/agent"
)

// resolveAgentVersion returns the requested version when non-empty, otherwise
// resolves the latest release for the agent via agent.LatestVersion, the one
// resolution shared with the upgrade check. Agents without an UpgradeChecker
// fall back to the requested (or empty) version.
var resolveAgentVersion = func(ctx context.Context, a agent.Agent, requested string) (string, error) {
	if requested != "" {
		return requested, nil
	}
	return agent.LatestVersion(ctx, a)
}

// WithMockAgentVersion redirects the image package's agent version resolver to
// return v for any request, restoring the real resolver when the test ends.
//
//deadcode:ignore reason="test seam establishing a pinned agent version"
func WithMockAgentVersion(t *testing.T, v string) {
	t.Helper()
	WithMockAgentVersionResolver(t, func(_ context.Context, _ agent.Agent, _ string) (string, error) { return v, nil })
}

// WithMockAgentVersionResolver installs a custom agent version resolver for a
// test, restoring the real one (which may hit the GitHub releases endpoint for
// agents that implement an UpgradeChecker) when the test ends.
//
//deadcode:ignore reason="test seam establishing a custom agent version resolver"
func WithMockAgentVersionResolver(
	t *testing.T,
	resolve func(ctx context.Context, a agent.Agent, requested string) (string, error),
) {
	t.Helper()
	orig := resolveAgentVersion
	resolveAgentVersion = resolve
	t.Cleanup(func() { resolveAgentVersion = orig })
}
