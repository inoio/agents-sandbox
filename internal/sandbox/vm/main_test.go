package vm

import (
	"testing"
	"time"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	mocks "github.com/inoio/agents-sandbox/internal/testutil/mocks"
)

// capturedDaemonReadyTimeout and capturedDaemonPollInterval hold the values the
// production declarations assign, captured before TestMain overwrites the vars
// for fast unit tests. TestMain's override makes the declared defaults
// unreachable from a test otherwise, so this capture is how a test can pin them.
var (
	capturedDaemonReadyTimeout = daemonReadyTimeout
	capturedDaemonPollInterval = daemonPollInterval
)

func TestMain(m *testing.M) {
	// A mock that does not answer a readiness probe must fail fast, not poll out
	// the production budget: these are mocked unit tests, and the first probe is
	// healthy in every passing case. A test that needs the production window, or
	// a deliberately expired one, saves and restores the var itself.
	daemonReadyTimeout = 200 * time.Millisecond
	daemonPollInterval = 10 * time.Millisecond
	dockerdReadyTimeout = 200 * time.Millisecond
	dockerdPollInterval = 10 * time.Millisecond
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

// opencodeResolved returns the default opencode profile with its capabilities
// resolved, for the vm consumers that take an agent.Resolved.
func opencodeResolved(t *testing.T) agent.Resolved {
	t.Helper()
	return agent.Resolve(opencodeAgent(t))
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
