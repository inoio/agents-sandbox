package agent_test

import (
	"testing"

	"github.com/inoio/agents-sandbox/internal/agent"
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

func TestOnlyOpencodeImplementsEventStreamProvider(t *testing.T) {
	for _, name := range agent.Names() {
		a, _ := agent.Lookup(name)
		_, ok := agent.AsEventStreamProvider(a)
		if name == "opencode" {
			if !ok {
				t.Errorf("opencode should implement EventStreamProvider")
			}
			continue
		}
		if ok {
			t.Errorf("%s should not implement EventStreamProvider", name)
		}
	}
}
