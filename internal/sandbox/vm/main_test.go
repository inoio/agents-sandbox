package vm

import (
	"context"
	"testing"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio"

	mocks "github.com/inoio/agents-sandbox/internal/testutil/mocks"
)

func TestMain(m *testing.M) {
	// Seal PrepareSandbox off from the developer's real legacy home: the default
	// offer is a no-op, and the tests that exercise it opt in explicitly.
	SetRuntimeHomeMigrationOffer(func(context.Context, msb.Client, termio.UI) error { return nil })
	// No existing runner image by default; recovery tests opt in explicitly.
	existingAgentVersion = func(context.Context, string, string) (string, bool) { return "", false }
	mocks.InitFailFastMocks(m)
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
