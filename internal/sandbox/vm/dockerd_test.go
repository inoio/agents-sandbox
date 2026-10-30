package vm

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// countingSandbox is an msb.MockSandbox that records how many detached jobs it
// launched and models dockerd becoming ready after a number of readiness
// probes, so tests can assert which branches startDockerdIfPresent took.
type countingSandbox struct {
	*msb.MockSandbox

	execCalls               int
	execErr                 error
	cleanupErr              error
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
	case dockerdCleanupCmd:
		if s.cleanupErr != nil {
			return msb.NewTestResult(false, 1, "", "", nil), s.cleanupErr
		}
	}
	return msb.NewTestResult(true, 0, "", "", nil), nil
}

func (s *countingSandbox) ExecDetached(
	ctx context.Context,
	cmd string,
	args []string,
	opts ...msbSdk.JobExecOption,
) (msb.Job, error) {
	s.execCalls++
	if s.execErr != nil {
		return nil, s.execErr
	}
	return s.MockSandbox.ExecDetached(ctx, cmd, args, opts...)
}

func newCountingSandbox(readyCallsBeforeHealthy int) *countingSandbox {
	return &countingSandbox{
		MockSandbox:             &msb.MockSandbox{},
		readyCallsBeforeHealthy: readyCallsBeforeHealthy,
	}
}

func TestStartDockerdIfPresentNoDockerdBinary(t *testing.T) {
	ui := termio.NewTestMock(t)
	sb := msb.NewMockSandbox(msb.SandboxOpts{
		ShellOut: map[string]msb.ShellResult{
			dockerdBinaryCheckCmd: msb.NewTestResult(false, 1, "", "", nil),
		},
	})

	if err := startDockerdIfPresent(context.Background(), sb, &ui); err != nil {
		t.Fatalf("expected no-op when dockerd absent, got: %v", err)
	}
	if !slices.Contains(ui.VerboseCalls, "/usr/bin/dockerd not present, skipping Docker startup") {
		t.Errorf("expected the absent-dockerd verbose note, got %v", ui.VerboseCalls)
	}
}

func TestStartDockerdIfPresentAlreadyRunning(t *testing.T) {
	ui := termio.NewTestMock(t)
	sb := newCountingSandbox(0)

	if err := startDockerdIfPresent(context.Background(), sb, &ui); err != nil {
		t.Fatalf("startDockerdIfPresent (already running): %v", err)
	}
	if sb.execCalls != 0 {
		t.Errorf("expected no detach when already running, got %d", sb.execCalls)
	}
	if !slices.Contains(ui.VerboseCalls, "using already running dockerd") {
		t.Errorf("expected the already-running verbose note, got %v", ui.VerboseCalls)
	}
}

func TestStartDockerdIfPresentStartsAndBecomesReady(t *testing.T) {
	ui := termio.NewTestMock(t)
	sb := newCountingSandbox(1)

	if err := startDockerdIfPresent(context.Background(), sb, &ui); err != nil {
		t.Fatalf("startDockerdIfPresent should detach and become ready, got: %v", err)
	}
	if sb.execCalls != 1 {
		t.Errorf("expected one detach attempt, got %d", sb.execCalls)
	}
	if !slices.Contains(ui.VerboseCalls, "starting dockerd with vfs storage driver") {
		t.Errorf("expected a verbose start note, got %v", ui.VerboseCalls)
	}
	if !slices.Contains(ui.VerboseCalls, "dockerd is ready") {
		t.Errorf("expected a verbose ready note, got %v", ui.VerboseCalls)
	}
}

func TestStartDockerdIfPresentExecError(t *testing.T) {
	ui := termio.NewTestMock(t)
	sb := newCountingSandbox(1 << 30)
	wantErr := errors.New("connection lost")
	sb.execErr = wantErr

	err := startDockerdIfPresent(context.Background(), sb, &ui)
	if err == nil {
		t.Fatal("expected error when ExecDetached fails")
	}
	if !errors.Is(err, wantErr) {
		t.Fatalf("startDockerdIfPresent() = %v, want it to wrap %v", err, wantErr)
	}
	if sb.execCalls != 1 {
		t.Errorf("expected 1 detach attempt, got %d", sb.execCalls)
	}
}

func TestStartDockerdIfPresentNeverReady(t *testing.T) {
	ui := termio.NewTestMock(t)
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
	if sb.execCalls != 1 {
		t.Errorf("expected 1 detach attempt before timeout, got %d", sb.execCalls)
	}
}

func TestStartDockerdIfPresentProbesBeforeWaiting(t *testing.T) {
	ui := termio.NewTestMock(t)
	// The pre-detach check fails once; the first post-detach probe is healthy.
	// A long poll interval proves that probe happens immediately, not after the
	// interval.
	sb := newCountingSandbox(1)

	origTimeout, origInterval := dockerdReadyTimeout, dockerdPollInterval
	t.Cleanup(func() {
		dockerdReadyTimeout = origTimeout
		dockerdPollInterval = origInterval
	})
	dockerdReadyTimeout = 2 * time.Second
	dockerdPollInterval = 2 * time.Second

	start := time.Now()
	if err := startDockerdIfPresent(context.Background(), sb, &ui); err != nil {
		t.Fatalf("startDockerdIfPresent: %v", err)
	}
	if elapsed := time.Since(start); elapsed >= 500*time.Millisecond {
		t.Fatalf("startDockerdIfPresent waited %v before its first readiness probe, want immediate", elapsed)
	}
}

func TestStartDockerdIfPresentKillsStaleJobWithChangedArgs(t *testing.T) {
	ui := termio.NewTestMock(t)
	stale := &msb.MockJob{ID_: "stale-dockerd"}
	running := &msb.MockJob{ID_: "new-dockerd"}
	sb := newCountingSandbox(1)
	// The stale job was launched by a release whose dockerd arguments differ
	// from this one's (no storage-driver flag), which is exactly the case the
	// old binary+args matcher missed (#343).
	sb.Jobs = []msbSdk.JobInfo{
		{ID: "stale-dockerd", Command: []string{dockerdBinary, "-H", "unix:///var/run/docker.sock"}},
	}
	sb.GetJobFn = func(context.Context, string) (msb.Job, error) { return stale, nil }
	sb.Job_ = running

	if err := startDockerdIfPresent(context.Background(), sb, &ui); err != nil {
		t.Fatalf("startDockerdIfPresent: %v", err)
	}
	if !stale.DidKill {
		t.Error("stale dockerd job was not killed before restart")
	}
}

// TestStartDockerdIfPresentLaunchesMarkedJob pins the launch shape: the job is
// started under dockerd's identity marker, so a later release can target it
// whatever arguments it uses.
func TestStartDockerdIfPresentLaunchesMarkedJob(t *testing.T) {
	ui := termio.NewTestMock(t)
	sb := newCountingSandbox(1)
	calls := &[]msb.MockDetachedCall{}
	sb.ExecDetachedCalls = calls

	if err := startDockerdIfPresent(context.Background(), sb, &ui); err != nil {
		t.Fatalf("startDockerdIfPresent: %v", err)
	}
	assertDetachedHelperLaunch(t, *calls, "dockerd", dockerdBinary)
}

func TestStartDockerdIfPresentContinuesAfterCleanupError(t *testing.T) {
	ui := termio.NewTestMock(t)
	sb := newCountingSandbox(1)
	sb.cleanupErr = errors.New("residue boom")

	if err := startDockerdIfPresent(context.Background(), sb, &ui); err != nil {
		t.Fatalf("residue cleanup failure must be non-fatal, got: %v", err)
	}
	if !strings.Contains(strings.Join(ui.VerboseCalls, "\n"), "residue cleanup failed") {
		t.Errorf("expected a verbose cleanup failure, got %v", ui.VerboseCalls)
	}
}

func TestStartDockerdIfPresentReportsCloseFailure(t *testing.T) {
	ui := termio.NewTestMock(t)
	sb := newCountingSandbox(1)
	sb.Job_ = &msb.MockJob{ID_: "new-dockerd", CloseErr: errors.New("close boom")}

	if err := startDockerdIfPresent(context.Background(), sb, &ui); err != nil {
		t.Fatalf("startDockerdIfPresent: %v", err)
	}
	if !strings.Contains(strings.Join(ui.VerboseCalls, "\n"), "closing dockerd job handle") {
		t.Errorf("expected a verbose close failure, got %v", ui.VerboseCalls)
	}
}

func TestDockerdCleanupCmdRemovesContainerdResidue(t *testing.T) {
	for _, want := range []string{
		"pkill containerd",
		".sock",
		".pid",
	} {
		if !strings.Contains(dockerdCleanupCmd, want) {
			t.Errorf("dockerdCleanupCmd missing %q:\n%s", want, dockerdCleanupCmd)
		}
	}
	if strings.Contains(dockerdCleanupCmd, "pkill dockerd") {
		t.Errorf("dockerdCleanupCmd must not pkill dockerd (job kill owns that):\n%s", dockerdCleanupCmd)
	}
	if strings.HasSuffix(dockerdCleanupCmd, "&") {
		t.Errorf("dockerdCleanupCmd must not background anything:\n%s", dockerdCleanupCmd)
	}
}

func TestDockerdReadyTimeoutAllowsFailOver(t *testing.T) {
	// The invariant is on the production default: TestMain shortens the var, so
	// assert the default the var is initialised from.
	if dockerdReadyTimeoutDefault < 25*time.Second {
		t.Fatalf(
			"dockerdReadyTimeoutDefault=%v must exceed dockerd's 15s containerd start timeout",
			dockerdReadyTimeoutDefault,
		)
	}
	// Pin the exact value too: the "< 25s" floor alone lets a decrement or
	// increment of the literal through, and the readiness window is a contract.
	if dockerdReadyTimeoutDefault != 30*time.Second {
		t.Errorf("dockerdReadyTimeoutDefault = %s, want 30s", dockerdReadyTimeoutDefault)
	}
}

func TestTimeoutErrorIncludesJobLog(t *testing.T) {
	origTimeout, origInterval := dockerdReadyTimeout, dockerdPollInterval
	t.Cleanup(func() {
		dockerdReadyTimeout = origTimeout
		dockerdPollInterval = origInterval
	})
	dockerdReadyTimeout = 50 * time.Millisecond
	dockerdPollInterval = time.Millisecond

	const sentinel = "failed to start containerd: timeout waiting for containerd to start"
	sb := newCountingSandbox(1 << 30)
	sb.Job_ = &msb.MockJob{
		Logs_: []msbSdk.JobLogEntry{{Data: []byte(sentinel)}},
	}
	ui := termio.NewTestMock(t)

	err := startDockerdIfPresent(context.Background(), sb, &ui)
	if err == nil {
		t.Fatal("expected error when dockerd never becomes ready")
	}
	if !strings.Contains(err.Error(), sentinel) {
		t.Errorf("timeout error should embed the job log, got: %v", err)
	}
	if !strings.Contains(strings.Join(ui.VerboseCalls, "\n"), "dockerd not ready:") {
		t.Errorf("expected a verbose not-ready probe detail, got %v", ui.VerboseCalls)
	}
}
