package vm

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/inoio/opencode-sandbox/internal/sandbox/msb"
	"github.com/inoio/opencode-sandbox/internal/termio"
)

func TestParseHealthResponseHealthy(t *testing.T) {
	healthy, err := parseHealthResponse(`{"healthy":true,"version":"1.18.5"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !healthy {
		t.Error("expected healthy=true")
	}
}

func TestParseHealthResponseUnhealthy(t *testing.T) {
	healthy, err := parseHealthResponse(`{"healthy":false}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if healthy {
		t.Error("expected healthy=false")
	}
}

func TestParseHealthResponseInvalidJSON(t *testing.T) {
	_, err := parseHealthResponse("not json")
	if err == nil {
		t.Error("expected error for invalid JSON")
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

	err := ensureDaemon(context.Background(), false, nil, &testUI)
	if err != nil {
		t.Fatalf("EnsureDaemon failed: %v", err)
	}
}

// TestDaemonStartCommandServeOnly verifies that serve-only mode binds the
// opencode daemon to 0.0.0.0 so microsandbox's published-port forwarder (which
// dials the guest's external interface address) can reach it. Loopback-only
// (127.0.0.1) would make host clients get an empty reply.
func TestDaemonStartCommandServeOnly(t *testing.T) {
	got := daemonStartCommand(true)
	if !strings.Contains(got, "--hostname 0.0.0.0") {
		t.Errorf("serve-only daemon start command missing --hostname 0.0.0.0, got %q", got)
	}
	if strings.Contains(got, "127.0.0.1") {
		t.Errorf("serve-only daemon start command must not bind loopback, got %q", got)
	}
}

// TestDaemonStartCommandAttach verifies the non-serve-only (attach) path keeps
// the loopback binding for the in-VM TUI client.
func TestDaemonStartCommandAttach(t *testing.T) {
	got := daemonStartCommand(false)
	if !strings.Contains(got, "--hostname 127.0.0.1") {
		t.Errorf("attach daemon start command must keep loopback binding, got %q", got)
	}
}

// TestEnsureDaemonStartsServeOnlyOnExternalInterface verifies that serve-only
// mode starts the opencode daemon bound to 0.0.0.0. Microsandbox's published-port
// forwarder dials the guest's external interface address, so a loopback-only
// (127.0.0.1) binding would make host clients get an empty reply.
func TestEnsureDaemonStartsServeOnlyOnExternalInterface(t *testing.T) {
	testUI := termio.NewTestMock(t)
	var startCmd string
	healthChecks := 0
	prev := SetDaemonShellFunc(func(_ context.Context, _ msb.Sandbox, command string) (string, int, error) {
		if strings.Contains(command, "opencode serve") && !strings.Contains(command, "pkill") {
			startCmd = command
		}
		if command == "curl -sfm2 "+daemonHealthURL {
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

	err := ensureDaemon(context.Background(), true, nil, &testUI)
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

	err := ensureDaemon(context.Background(), false, nil, &testUI)
	if err == nil {
		t.Fatal("expected error after timeout")
	}
	if !strings.Contains(err.Error(), "did not become healthy") {
		t.Errorf("expected timeout error, got: %v", err)
	}
}
