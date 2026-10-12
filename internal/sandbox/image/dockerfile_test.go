package image

import (
	"testing"

	"github.com/inoio/agents-sandbox/internal/agent"
)

func agentOpencode(t *testing.T) agent.Agent {
	t.Helper()
	a, ok := agent.Lookup("opencode")
	if !ok {
		t.Fatal("opencode agent not registered")
	}
	return a
}

// testAgentVersion is the agent version most render tests use; it matches the
// version most tests install via WithMockAgentVersion so identity assertions
// stay consistent with the rendered content.
const testAgentVersion = "1.2.3"

// renderBytes renders a runner Dockerfile for tests with the default test
// version, failing on a render error.
func renderBytes(t *testing.T, a agent.Agent, project []byte, docker bool) []byte {
	t.Helper()
	return renderBytesVersion(t, a, testAgentVersion, project, docker)
}

// renderBytesVersion renders a runner Dockerfile for a pinned agent version,
// failing on a render error.
func renderBytesVersion(t *testing.T, a agent.Agent, version string, project []byte, docker bool) []byte {
	t.Helper()
	out, err := RenderDockerfile(a, version, project, docker)
	if err != nil {
		t.Fatalf("RenderDockerfile: %v", err)
	}
	return out
}
