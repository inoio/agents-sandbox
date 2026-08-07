package sandbox

import (
	"context"
	"testing"
	"time"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox/msb"
	"gitlab.inoio.de/inoio/opencode-msb/internal/testutil"
)

// countingSandbox is an msb.Sandbox stub that records how many times it ran
// each of the dockerd start/ready commands, so tests can assert which branches
// startDockerdIfPresent took. The ready command becomes healthy after exceeding
// readyCallsBeforeHealthy, which models dockerd becoming ready some time after
// the restart.
type countingSandbox struct {
	msb.Sandbox

	restartCalls            int
	readyCalls              int
	readyCallsBeforeHealthy int
}

func (s *countingSandbox) Shell(_ context.Context, command string, _ ...msbSdk.ExecOption) (msb.ShellResult, error) {
	switch command {
	case dockerdBinaryCheckCmd:
		return msb.NewTestResult(true, 0, "", "", nil), nil
	case dockerdReadyCmd:
		s.readyCalls++
		if s.readyCalls > s.readyCallsBeforeHealthy {
			return msb.NewTestResult(true, 0, "", "", nil), nil
		}
		return msb.NewTestResult(false, 1, "", "", nil), nil
	case dockerdRestartCmd:
		s.restartCalls++
		return msb.NewTestResult(true, 0, "", "", nil), nil
	}
	return msb.NewTestResult(true, 0, "", "", nil), nil
}

func newCountingSandbox(readyCallsBeforeHealthy int) *countingSandbox {
	return &countingSandbox{
		Sandbox:                 msb.NewMockSandbox(msb.SandboxOpts{}),
		readyCallsBeforeHealthy: readyCallsBeforeHealthy,
	}
}

func TestStartDockerdIfPresentNoDockerdBinary(t *testing.T) {
	ui := testutil.TermUIMock(t)
	sb := msb.NewMockSandbox(msb.SandboxOpts{
		ShellOut: map[string]msb.ShellResult{
			dockerdBinaryCheckCmd: msb.NewTestResult(false, 1, "", "", nil),
		},
	})

	if err := startDockerdIfPresent(context.Background(), sb, &ui); err != nil {
		t.Fatalf("expected no-op when dockerd absent, got: %v", err)
	}
}

func TestStartDockerdIfPresentAlreadyRunning(t *testing.T) {
	ui := testutil.TermUIMock(t)
	sb := newCountingSandbox(0)

	if err := startDockerdIfPresent(context.Background(), sb, &ui); err != nil {
		t.Fatalf("startDockerdIfPresent (already running): %v", err)
	}
	if sb.restartCalls != 0 {
		t.Errorf("expected no dockerd restart when already running, got %d", sb.restartCalls)
	}
}

func TestStartDockerdIfPresentStartsAndBecomesReady(t *testing.T) {
	ui := testutil.TermUIMock(t)
	sb := newCountingSandbox(1)

	if err := startDockerdIfPresent(context.Background(), sb, &ui); err != nil {
		t.Fatalf("startDockerdIfPresent should restart and become ready, got: %v", err)
	}
	if sb.restartCalls != 1 {
		t.Errorf("expected 1 restart attempt, got %d", sb.restartCalls)
	}
}

func TestStartDockerdIfPresentNeverReady(t *testing.T) {
	ui := testutil.TermUIMock(t)
	// A huge threshold means the ready check can never become healthy within
	// the (shortened) poll window, forcing the timeout path.
	sb := newCountingSandbox(1 << 30)

	t.Cleanup(func() {
		dockerdReadyTimeout = 10 * time.Second
		dockerdPollInterval = time.Second
	})
	dockerdReadyTimeout = 100 * time.Millisecond
	dockerdPollInterval = time.Millisecond

	err := startDockerdIfPresent(context.Background(), sb, &ui)
	if err == nil {
		t.Fatal("expected error when dockerd never becomes ready")
	}
	if sb.restartCalls != 1 {
		t.Errorf("expected 1 restart attempt before timeout, got %d", sb.restartCalls)
	}
}
