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

// renderBytes renders a runner Dockerfile for tests, failing on a render error.
func renderBytes(t *testing.T, a agent.Agent, project []byte, docker bool) []byte {
	t.Helper()
	out, err := RenderDockerfile(a, project, docker)
	if err != nil {
		t.Fatalf("RenderDockerfile: %v", err)
	}
	return out
}
