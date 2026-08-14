package session

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/msb"
	"gitlab.inoio.de/inoio/opencode-sandbox/internal/testutil"
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
	restartShellErr         error
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
		if s.restartShellErr != nil {
			return nil, s.restartShellErr
		}
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
		t.Errorf("expected restart attempt, got %d", sb.restartCalls)
	}
}

func TestStartDockerdIfPresentShellError(t *testing.T) {
	ui := testutil.TermUIMock(t)
	sb := newCountingSandbox(1 << 30)
	sb.restartShellErr = errors.New("connection lost")

	err := startDockerdIfPresent(context.Background(), sb, &ui)
	if err == nil {
		t.Fatal("expected error when the restart Shell call fails")
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

	origTimeout, origInterval := dockerdReadyTimeout, dockerdPollInterval
	t.Cleanup(func() {
		dockerdReadyTimeout = origTimeout
		dockerdPollInterval = origInterval
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

func TestDockerdRestartCmdTearsDownContainerd(t *testing.T) {
	for _, want := range []string{
		"pkill dockerd",
		"pkill containerd",
		"containerd",
		".sock",
		"dockerd -H unix:///var/run/docker.sock",
	} {
		if !strings.Contains(dockerdRestartCmd, want) {
			t.Errorf("dockerdRestartCmd missing %q:\n%s", want, dockerdRestartCmd)
		}
	}
	if !strings.HasSuffix(dockerdRestartCmd, "&") {
		t.Errorf("dockerdRestartCmd should start dockerd in background:\n%s", dockerdRestartCmd)
	}
	if strings.Contains(dockerdRestartCmd, "sleep 1") {
		t.Errorf("dockerdRestartCmd should not use a fixed sleep:\n%s", dockerdRestartCmd)
	}
}

func TestDockerdReadyTimeoutAllowsFailOver(t *testing.T) {
	if dockerdReadyTimeout < 25*time.Second {
		t.Fatalf("dockerdReadyTimeout=%v must exceed dockerd's 15s containerd start timeout", dockerdReadyTimeout)
	}
}

func TestTimeoutErrorIncludesDockerdLog(t *testing.T) {
	origTimeout, origInterval := dockerdReadyTimeout, dockerdPollInterval
	t.Cleanup(func() {
		dockerdReadyTimeout = origTimeout
		dockerdPollInterval = origInterval
	})
	dockerdReadyTimeout = 50 * time.Millisecond
	dockerdPollInterval = time.Millisecond

	const sentinel = "failed to start containerd: timeout waiting for containerd to start"
	sb := &countingSandbox{
		Sandbox: msb.NewMockSandbox(msb.SandboxOpts{
			FSValue: msb.NewTestFS(map[string][]byte{
				"/var/log/dockerd.log": []byte(sentinel),
			}, nil),
		}),
		readyCallsBeforeHealthy: 1 << 30,
	}
	ui := testutil.TermUIMock(t)

	err := startDockerdIfPresent(context.Background(), sb, &ui)
	if err == nil {
		t.Fatal("expected error when dockerd never becomes ready")
	}
	if !strings.Contains(err.Error(), sentinel) {
		t.Errorf("timeout error should embed the dockerd log, got: %v", err)
	}
}
