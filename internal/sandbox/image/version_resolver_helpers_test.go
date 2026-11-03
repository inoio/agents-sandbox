package image

import (
	"context"
	"testing"

	"github.com/inoio/agents-sandbox/internal/agent"
)

// withMockAgentVersion pins the package's default agent-version resolver to v
// for the duration of the test, restoring it when the test ends. It is the
// in-package replacement for the retired exported WithMockAgentVersion hook:
// production callers inject a resolver through BuildOptions.ResolveVersion, and
// image's own tests override the package default.
func withMockAgentVersion(t *testing.T, v string) {
	t.Helper()
	withMockAgentVersionResolver(t, func(_ context.Context, _ agent.Agent, _ string) (string, error) {
		return v, nil
	})
}

// withMockAgentVersionResolver installs a custom default resolver for the test.
func withMockAgentVersionResolver(t *testing.T, resolve AgentVersionResolver) {
	t.Helper()
	orig := defaultAgentVersionResolver
	defaultAgentVersionResolver = resolve
	t.Cleanup(func() { defaultAgentVersionResolver = orig })
}
