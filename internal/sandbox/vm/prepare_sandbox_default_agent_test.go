package vm

import (
	"context"
	"slices"
	"testing"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/git"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// TestPrepareSandboxRunsDefaultAgentDaemon covers the default agent's daemon
// path end to end through PrepareSandbox: a run with the default agent must
// resolve to a daemon profile and drive that profile's own start and health
// commands. The identity, migration, lifecycle, and preview tests pin the v1
// agent explicitly (their subject is the project, not the agent), so this is
// the test that keeps whichever agent is the default wired through
// orchestration.
func TestPrepareSandboxRunsDefaultAgentDaemon(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	defaultAgent, ok := agent.Lookup(agent.DefaultAgentName)
	if !ok {
		t.Fatalf("default agent %q is not registered", agent.DefaultAgentName)
	}
	provider, ok := agent.AsDaemonProvider(defaultAgent)
	if !ok {
		t.Fatalf("default agent %q is not a daemon provider", agent.DefaultAgentName)
	}

	installPrepareSandboxDockerMock(t)
	mock := installPrepareSandboxMSBMock(t)
	created, ok := mock.CreatedSandbox.(*msb.MockSandbox)
	if !ok {
		t.Fatalf("created sandbox is %T, want *msb.MockSandbox", mock.CreatedSandbox)
	}
	detached := &[]msb.MockDetachedCall{}
	created.ExecDetachedCalls = detached
	seedMatchingHomeState(t, state.Key{Slug: git.MustCheckoutSlug(), Agent: agent.DefaultAgentName})

	// The first probe reports not-ready so the run takes the start-then-poll
	// path, exercising both the start and health commands; later probes report
	// healthy so the run finishes. The body carries both agents' healthy
	// markers, keeping the mock agnostic to which profile is the default.
	var commands []string
	healthProbes := 0
	origDaemon := SetDaemonHealthFunc(func(_ context.Context, _ msb.Sandbox, command string) (string, int, error) {
		commands = append(commands, command)
		if command == provider.DaemonHealthCmd() {
			healthProbes++
			if healthProbes == 1 {
				return "", 0, nil
			}
			return `{"healthy":true,"version":"test"}`, 0, nil
		}
		return "", 0, nil
	})
	t.Cleanup(func() { SetDaemonHealthFunc(origDaemon) })

	ui := termio.NewTestMock(t)
	sess, err := PrepareSandbox(context.Background(), options.RunOptions{}, false, &ui)
	if err != nil {
		t.Fatalf("PrepareSandbox(default %q): %v", agent.DefaultAgentName, err)
	}
	if sess == nil {
		t.Fatal("expected a non-nil session")
	}
	defer sess.Cleanup()

	assertDetachedHelperLaunch(t, *detached, "agent-daemon", provider.DaemonCommand(false).Binary)
	if !slices.Contains(commands, provider.DaemonHealthCmd()) {
		t.Errorf("default agent %q daemon health was not probed; commands = %v", agent.DefaultAgentName, commands)
	}
}
