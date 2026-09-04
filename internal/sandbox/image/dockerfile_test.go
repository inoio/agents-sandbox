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
