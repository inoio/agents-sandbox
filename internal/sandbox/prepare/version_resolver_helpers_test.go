package prepare

import (
	"context"
	"testing"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/sandbox/image"
)

// withMockBuildVersionResolver overrides the release resolver PrepareSandbox
// injects into the image build, so a test can observe the requested version.
func withMockBuildVersionResolver(t *testing.T, resolve image.AgentVersionResolver) {
	t.Helper()
	orig := buildVersionResolver
	buildVersionResolver = resolve
	t.Cleanup(func() { buildVersionResolver = orig })
}

// withMockAgentLatestVersion pins the shared agent-latest-version lookup, which
// the build resolver and the upgrade check both consult.
func withMockAgentLatestVersion(t *testing.T, v string) {
	t.Helper()
	orig := agentLatestVersion
	agentLatestVersion = func(_ context.Context, _ agent.Agent) (string, error) { return v, nil }
	t.Cleanup(func() { agentLatestVersion = orig })
}
