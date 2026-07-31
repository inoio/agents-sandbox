package sandbox

import (
	"context"
	"strings"
	"testing"

	msb "github.com/superradcompany/microsandbox/sdk/go"
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

// mockDaemonShell overrides daemonShellFunc for testing. It returns queued
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

func (m *mockDaemonShell) run(_ context.Context, _ *msb.Sandbox, _ string) (string, int, error) {
	if m.calls >= len(m.responses) {
		return "", 0, nil
	}
	r := m.responses[m.calls]
	m.calls++
	return r.stdout, r.exitCode, r.err
}

func TestEnsureDaemonStartsWhenUnhealthy(t *testing.T) {
	ui := newTestio(t)
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

	err := EnsureDaemon(context.Background(), nil, io)
	if err != nil {
		t.Fatalf("EnsureDaemon failed: %v", err)
	}
}

func TestEnsureDaemonFailsAfterTimeout(t *testing.T) {
	ui := newTestio(t)
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

	err := EnsureDaemon(context.Background(), nil, io)
	if err == nil {
		t.Fatal("expected error after timeout")
	}
	if !strings.Contains(err.Error(), "did not become healthy") {
		t.Errorf("expected timeout error, got: %v", err)
	}
}
