package agent_test

import (
	"testing"

	"github.com/inoio/opencode-sandbox/internal/agent"
)

// opencode must expose a distinct WorktreeProvider capability, separate from
// DaemonProvider.
func TestOpencodeImplementsWorktreeProvider(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	if _, ok := agent.AsWorktreeProvider(a); !ok {
		t.Fatal("opencode should implement WorktreeProvider")
	}
	if _, ok := agent.AsDaemonProvider(a); !ok {
		t.Fatal("opencode should implement DaemonProvider")
	}
}
