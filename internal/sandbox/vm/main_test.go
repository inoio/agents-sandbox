package vm

import (
	"testing"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"

	termio "github.com/inoio/agents-sandbox/internal/testutil/mocks"
)

func TestMain(m *testing.M) {
	termio.InitFailFastMocks(m)
}

// opencodeAgent returns the default opencode profile for tests that need a real
// agent but don't otherwise care which one it is.
func opencodeAgent(t *testing.T) agent.Agent {
	t.Helper()
	a, ok := agent.Lookup("opencode")
	if !ok {
		t.Fatal("opencode agent not registered")
	}
	return a
}

// opencodeProvider returns the opencode profile's DaemonProvider so tests can
// build the exact commands the production path shells out with.
func opencodeProvider(t *testing.T) agent.DaemonProvider {
	t.Helper()
	p, ok := agent.AsDaemonProvider(opencodeAgent(t))
	if !ok {
		t.Fatal("opencode agent does not implement DaemonProvider")
	}
	return p
}

// testVMKey returns a stable project/agent key for unit tests that exercise
// the VM lifecycle without needing a real git project slug.
func testVMKey() state.Key {
	return state.Key{Slug: "testproj", Agent: "opencode"}
}
