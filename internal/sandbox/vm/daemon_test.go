package vm

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// fakeAgent implements only the core Agent interface with no capabilities, so
// the daemon/worktree paths must no-op for it.
type fakeAgent struct{}

func (fakeAgent) Name() string          { return "fake" }
func (fakeAgent) ConfigDirName() string { return "fake" }
func (fakeAgent) ImageSpec() agent.ImageSpec {
	return agent.ImageSpec{}
}

func TestEnsureDaemonNoDaemonProvider(t *testing.T) {
	ui := &termio.Mock{}
	sb := &msb.MockSandbox{ShellCalls: &[]string{}}
	a := &fakeAgent{}
	if err := ensureDaemon(context.Background(), a, false, sb, ui); err != nil {
		t.Fatalf("expected nil for an agent without a DaemonProvider, got %v", err)
	}
	if len(*sb.ShellCalls) != 0 {
		t.Errorf("expected no shell calls for an agent without a DaemonProvider, got %v", *sb.ShellCalls)
	}
}

// mockDaemonShell overrides daemonShellFunc for testutil. It returns queued
// (stdout, exitCode) pairs.
type mockDaemonShell struct {
	responses []mockShellResp
	calls     int
}

type mockShellResp struct {
	stdout   string
	exitCode int
	err      error
}

func (m *mockDaemonShell) run(_ context.Context, _ msb.Sandbox, _ string) (string, int, error) {
	if m.calls >= len(m.responses) {
		return "", 0, nil
	}
	r := m.responses[m.calls]
	m.calls++
	return r.stdout, r.exitCode, r.err
}

func TestEnsureDaemonStartsWhenUnhealthy(t *testing.T) {
	testUI := termio.NewTestMock(t)
	mock := &mockDaemonShell{
		responses: []mockShellResp{
			// First healthcheck: unhealthy (daemon not running).
			{stdout: "", exitCode: 1},
			// Kill stale daemon (response ignored).
			{stdout: "", exitCode: 0},
			// Start command (response ignored).
			{stdout: "", exitCode: 0},
			// Poll: still starting.
			{stdout: "", exitCode: 1},
			// Poll again: healthy.
			{stdout: `{"healthy":true}`, exitCode: 0},
		},
	}
	prev := daemonShellFunc
	t.Cleanup(func() { daemonShellFunc = prev })
	daemonShellFunc = mock.run

	t.Cleanup(func() { daemonPollInterval = 2 * time.Second })
	daemonPollInterval = 10 * time.Millisecond

	err := ensureDaemon(context.Background(), opencodeAgent(t), false, nil, &testUI)
	if err != nil {
		t.Fatalf("EnsureDaemon failed: %v", err)
	}
}

// TestEnsureDaemonStartsServeOnlyOnExternalInterface verifies that serve-only
// mode starts the opencode daemon bound to 0.0.0.0. Microsandbox's published-port
// forwarder dials the guest's external interface address, so a loopback-only
// (127.0.0.1) binding would make host clients get an empty reply.
func TestEnsureDaemonStartsServeOnlyOnExternalInterface(t *testing.T) {
	testUI := termio.NewTestMock(t)
	provider := opencodeProvider(t)
	var startCmd string
	healthChecks := 0
	prev := SetDaemonShellFunc(func(_ context.Context, _ msb.Sandbox, command string) (string, int, error) {
		if strings.Contains(command, "opencode serve") && !strings.Contains(command, "pkill") {
			startCmd = command
		}
		if command == provider.DaemonHealthCmd() {
			healthChecks++
			if healthChecks == 1 {
				return "", 1, nil
			}
			return `{"healthy":true}`, 0, nil
		}
		return "", 0, nil
	})
	defer SetDaemonShellFunc(prev)

	t.Cleanup(func() { daemonPollInterval = 2 * time.Second })
	daemonPollInterval = 10 * time.Millisecond

	err := ensureDaemon(context.Background(), opencodeAgent(t), true, nil, &testUI)
	if err != nil {
		t.Fatalf("EnsureDaemon failed: %v", err)
	}
	if startCmd == "" || !strings.Contains(startCmd, "--hostname 0.0.0.0") {
		t.Errorf("serve-only daemon was not started with external-interface binding, got %q", startCmd)
	}
}

func TestEnsureDaemonFailsAfterTimeout(t *testing.T) {
	testUI := termio.NewTestMock(t)
	mock := &mockDaemonShell{
		responses: []mockShellResp{
			// Always unhealthy.
			{stdout: "", exitCode: 1},
			{stdout: "", exitCode: 0},
			{stdout: "", exitCode: 0},
			{stdout: "", exitCode: 1},
			{stdout: "", exitCode: 1},
			{stdout: "", exitCode: 1},
			{stdout: "", exitCode: 1},
			{stdout: "", exitCode: 1},
		},
	}
	prev := daemonShellFunc
	t.Cleanup(func() { daemonShellFunc = prev })
	daemonShellFunc = mock.run

	t.Cleanup(func() { daemonReadyTimeout = 60 * time.Second })
	daemonReadyTimeout = 10 * time.Millisecond

	t.Cleanup(func() { daemonPollInterval = 2 * time.Second })
	daemonPollInterval = 1 * time.Millisecond

	err := ensureDaemon(context.Background(), opencodeAgent(t), false, nil, &testUI)
	if err == nil {
		t.Fatal("expected error after timeout")
	}
	if !strings.Contains(err.Error(), "did not become healthy") {
		t.Errorf("expected timeout error, got: %v", err)
	}
}
