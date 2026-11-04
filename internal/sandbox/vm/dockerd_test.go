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

func TestDockerdArgsDefaultsToVFSFlag(t *testing.T) {
	args, driver, err := dockerdArgs(context.Background(), daemonJSONSandbox(""))
	if err != nil {
		t.Fatalf("dockerdArgs: %v", err)
	}
	if driver != vfsStorageDriver {
		t.Errorf("driver = %q, want %q when fuse-overlayfs is unavailable", driver, vfsStorageDriver)
	}
	if !slices.Contains(args, "--storage-driver="+vfsStorageDriver) {
		t.Errorf("expected the vfs flag when daemon.json is absent; got %v", args)
	}
}

// TestDockerdArgsUsesFuseOverlayfsWhenAvailable guards the #489 default: when
// the image's daemon.json sets no driver and the guest provides the
// fuse-overlayfs binary and a usable /dev/fuse, dockerd is asked for
// fuse-overlayfs, whose copy-on-write layers beat vfs's full copy per layer.
func TestDockerdArgsUsesFuseOverlayfsWhenAvailable(t *testing.T) {
	args, driver, err := dockerdArgs(context.Background(), daemonJSONSandboxWithFuse("", true))
	if err != nil {
		t.Fatalf("dockerdArgs: %v", err)
	}
	if driver != fuseOverlayfsStorageDriver {
		t.Errorf("driver = %q, want %q when fuse-overlayfs is available", driver, fuseOverlayfsStorageDriver)
	}
	if !slices.Contains(args, "--storage-driver="+fuseOverlayfsStorageDriver) {
		t.Errorf("expected the fuse-overlayfs flag; got %v", args)
	}
}

// TestDockerdArgsAcceptsFuseOverlayfsFromDaemonJSON guards the #287 rule for the
// new driver: a daemon.json that already forces fuse-overlayfs is honored with
// no --storage-driver flag (dockerd rejects a directive set both ways).
func TestDockerdArgsAcceptsFuseOverlayfsFromDaemonJSON(t *testing.T) {
	args, driver, err := dockerdArgs(
		context.Background(),
		daemonJSONSandboxWithFuse(`{"storage-driver":"fuse-overlayfs"}`, false),
	)
	if err != nil {
		t.Fatalf("dockerdArgs: %v", err)
	}
	if driver != fuseOverlayfsStorageDriver {
		t.Errorf("driver = %q, want %q", driver, fuseOverlayfsStorageDriver)
	}
	for _, arg := range args {
		if strings.HasPrefix(arg, "--storage-driver") {
			t.Errorf("must not pass the flag when daemon.json already sets the driver; got %v", args)
		}
	}
}

// TestDockerdArgsFallsBackToVFSOnProbeError guards the failure mode: a probe
// transport error must not fail the VM start; selection falls back to vfs.
func TestDockerdArgsFallsBackToVFSOnProbeError(t *testing.T) {
	sb := &msb.MockSandbox{
		FSValue_: msb.NewTestFS(nil, nil),
		ShellErr: errors.New("probe boom"),
	}
	args, driver, err := dockerdArgs(context.Background(), sb)
	if err != nil {
		t.Fatalf("dockerdArgs: %v", err)
	}
	if driver != vfsStorageDriver {
		t.Errorf("driver = %q, want %q after a probe error", driver, vfsStorageDriver)
	}
	if !slices.Contains(args, "--storage-driver="+vfsStorageDriver) {
		t.Errorf("expected the vfs fallback flag; got %v", args)
	}
}

func TestDockerdArgsKeepsFlagForUnrelatedKeys(t *testing.T) {
	args, _, err := dockerdArgs(
		context.Background(),
		daemonJSONSandbox(`{"registry-mirrors":["https://mirror.example"]}`),
	)
	if err != nil {
		t.Fatalf("dockerdArgs: %v", err)
	}
	if !slices.Contains(args, "--storage-driver="+vfsStorageDriver) {
		t.Errorf("expected the vfs flag when daemon.json sets no storage driver; got %v", args)
	}
}

// TestDockerdArgsDropsFlagWhenDaemonJSONSetsVFS guards #287: when the image
// already forces vfs in daemon.json, passing --storage-driver=vfs as well makes
// dockerd refuse to start ("specified both as a flag and in the configuration
// file").
func TestDockerdArgsDropsFlagWhenDaemonJSONSetsVFS(t *testing.T) {
	args, _, err := dockerdArgs(
		context.Background(),
		daemonJSONSandbox(`{"storage-driver":"vfs"}`),
	)
	if err != nil {
		t.Fatalf("dockerdArgs: %v", err)
	}
	for _, arg := range args {
		if strings.HasPrefix(arg, "--storage-driver") {
			t.Errorf("must not pass the flag when daemon.json already sets vfs; got %v", args)
		}
	}
	if !slices.Contains(args, dockerdSocket) {
		t.Errorf("expected dockerd to still be started on its socket; got %v", args)
	}
}

func TestDockerdArgsFailsOnOtherStorageDriver(t *testing.T) {
	_, _, err := dockerdArgs(
		context.Background(),
		daemonJSONSandbox(`{"storage-driver":"overlay2"}`),
	)
	if err == nil {
		t.Fatal("expected an error when daemon.json requests a non-vfs driver")
	}
	if !strings.Contains(err.Error(), "overlay2") ||
		!strings.Contains(err.Error(), vfsStorageDriver) ||
		!strings.Contains(err.Error(), fuseOverlayfsStorageDriver) {
		t.Errorf("error should name the offending and both accepted drivers, got: %v", err)
	}
}

func TestDockerdArgsMalformedDaemonJSONFallsBackToFlag(t *testing.T) {
	args, _, err := dockerdArgs(
		context.Background(),
		daemonJSONSandbox(`{"storage-driver":`),
	)
	if err != nil {
		t.Fatalf("dockerdArgs: %v", err)
	}
	if !slices.Contains(args, "--storage-driver="+vfsStorageDriver) {
		t.Errorf("expected the vfs flag when daemon.json cannot be parsed; got %v", args)
	}
}

func TestDockerdArgsNullStorageDriverFallsBackToFlag(t *testing.T) {
	args, _, err := dockerdArgs(
		context.Background(),
		daemonJSONSandbox(`{"storage-driver":null}`),
	)
	if err != nil {
		t.Fatalf("dockerdArgs: %v", err)
	}
	if !slices.Contains(args, "--storage-driver="+vfsStorageDriver) {
		t.Errorf("expected the vfs flag when storage-driver is null; got %v", args)
	}
}

func TestDockerdArgsNonStringStorageDriverFails(t *testing.T) {
	_, _, err := dockerdArgs(
		context.Background(),
		daemonJSONSandbox(`{"storage-driver":42}`),
	)
	if err == nil {
		t.Fatal("expected an error for a non-string storage-driver value")
	}
}

func TestDockerdArgsReadErrorFails(t *testing.T) {
	fs := msb.NewTestFS(map[string][]byte{dockerdDaemonConfigPath: []byte(`{}`)}, nil)
	readErr := errors.New("read boom")
	fs.ReadErr = readErr
	sb := &msb.MockSandbox{FSValue_: fs}
	_, _, err := dockerdArgs(context.Background(), sb)
	if err == nil {
		t.Fatal("expected an error when daemon.json cannot be read")
	}
	if !errors.Is(err, readErr) {
		t.Errorf("error = %v, want it to wrap the read cause", err)
	}
}

// TestGuestStorageDriverExistsErrorWrapsCause pins the inspect-failure branch:
// an FS.Exists error aborts with the daemon.json path named and its cause
// preserved, rather than being treated as "absent".
func TestGuestStorageDriverExistsErrorWrapsCause(t *testing.T) {
	sentinel := errors.New("exists boom")
	sb := &msb.MockSandbox{FSValue_: existsErrorFS{TestFS: msb.NewTestFS(nil, nil), err: sentinel}}

	driver, err := guestStorageDriver(context.Background(), sb)
	if err == nil {
		t.Fatalf("expected an Exists failure to abort, got driver %q", driver)
	}
	if driver != "" {
		t.Errorf("driver = %q, want empty on error", driver)
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("error = %v, want it to wrap the Exists cause", err)
	}
}

// TestStartDockerdNilProbeResult covers the branch where the dockerd readiness
// probe returns a nil result during polling: the probe is treated as unhealthy
// and startup times out rather than panicking.
func TestStartDockerdNilProbeResult(t *testing.T) {
	origTimeout, origInterval := dockerdReadyTimeout, dockerdPollInterval
	t.Cleanup(func() {
		dockerdReadyTimeout = origTimeout
		dockerdPollInterval = origInterval
	})
	dockerdReadyTimeout = 50 * time.Millisecond
	dockerdPollInterval = time.Millisecond

	ui := termio.NewTestMock(t)
	sb := &nilResultDockerd{Sandbox: msb.NewMockSandbox(msb.SandboxOpts{})}

	err := startDockerdIfPresent(context.Background(), sb, &ui)
	if err == nil {
		t.Fatal("expected error when dockerd never becomes ready")
	}
}

func TestStartDockerdUsesConfigDrivenArgsWhenDaemonJSONSetsVFS(t *testing.T) {
	ui := termio.NewTestMock(t)
	sb, detached := newDockerdStartSandbox(`{"storage-driver":"vfs"}`)

	if err := startDockerdIfPresent(context.Background(), sb, &ui); err != nil {
		t.Fatalf("startDockerdIfPresent: %v", err)
	}
	if len(*detached) != 1 {
		t.Fatalf("ExecDetached calls = %+v, want one dockerd launch", *detached)
	}
	for _, arg := range (*detached)[0].Args {
		if strings.HasPrefix(arg, "--storage-driver") {
			t.Errorf("must not pass --storage-driver when daemon.json sets vfs; got %v", (*detached)[0].Args)
		}
	}
}

func TestStartDockerdFailsOnOtherStorageDriver(t *testing.T) {
	ui := termio.NewTestMock(t)
	sb, detached := newDockerdStartSandbox(`{"storage-driver":"overlay2"}`)

	err := startDockerdIfPresent(context.Background(), sb, &ui)
	if err == nil {
		t.Fatal("expected startDockerdIfPresent to fail for a non-vfs driver")
	}
	if !strings.Contains(err.Error(), "overlay2") {
		t.Errorf("error should name the offending driver, got: %v", err)
	}
	if len(*detached) != 0 {
		t.Errorf("dockerd must not be started when the driver conflicts, got %+v", *detached)
	}
}

// TestStartDockerdCtxDone covers the context-cancellation branch inside the
// dockerd readiness poll loop: when the context is cancelled, the poll aborts
// with the context error.
func TestStartDockerdCtxDone(t *testing.T) {
	ui := termio.NewTestMock(t)
	sb := &msb.MockSandbox{
		ShellOut: map[string]msb.ShellResult{
			dockerdBinaryCheckCmd: msb.NewTestResult(true, 0, "", "", nil),
			dockerdReadyCmd:       msb.NewTestResult(false, 1, "", "", nil),
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := startDockerdIfPresent(ctx, sb, &ui); err == nil {
		t.Fatal("expected an error when the context is cancelled during dockerd startup")
	}
	if err := ctx.Err(); err == nil {
		t.Fatal("expected the context to be cancelled")
	}
}

// TestStartDockerdIfPresentBinaryCheckErrorWrapsCause pins the binary-probe
// failure: a Shell error checking for dockerd aborts with its cause preserved,
// rather than being read as "not present".
func TestStartDockerdIfPresentBinaryCheckErrorWrapsCause(t *testing.T) {
	ui := termio.NewTestMock(t)
	sentinel := errors.New("shell boom")
	sb := msb.NewMockSandbox(msb.SandboxOpts{ShellErr: sentinel})

	err := startDockerdIfPresent(context.Background(), sb, &ui)
	if err == nil {
		t.Fatal("expected a binary-check Shell failure to abort")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("error = %v, want it to wrap the Shell cause", err)
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
	if !slices.Contains(ui.VerboseCalls, "starting dockerd with fuse-overlayfs storage driver") {
		t.Errorf("expected a verbose start note naming the selected driver, got %v", ui.VerboseCalls)
	}
	if !slices.Contains(ui.VerboseCalls, "dockerd is ready") {
		t.Errorf("expected a verbose ready note, got %v", ui.VerboseCalls)
	}
}

// TestStartDockerdIfPresentUsesFuseOverlayfsDefault guards #489 end to end from
// the start path: countingSandbox reports the fuse-overlayfs probe as available,
// so the detached dockerd job must be launched with --storage-driver=fuse-overlayfs.
func TestStartDockerdIfPresentUsesFuseOverlayfsDefault(t *testing.T) {
	ui := termio.NewTestMock(t)
	sb := newCountingSandbox(1)

	if err := startDockerdIfPresent(context.Background(), sb, &ui); err != nil {
		t.Fatalf("startDockerdIfPresent: %v", err)
	}
	if sb.execArgs == nil || !slices.Contains(*sb.execArgs, "--storage-driver="+fuseOverlayfsStorageDriver) {
		t.Fatalf("expected dockerd launched with the fuse-overlayfs driver; got %v", sb.execArgs)
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
	dockerdReadyTimeout = time.Second
	dockerdPollInterval = time.Second

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
	// dockerd runs as root, so its detached launch carries exactly one
	// exec-user option; a helper that dropped it would leave dockerd as the
	// default user.
	if len((*calls)[0].Opts) != 1 {
		t.Errorf("dockerd launch exec options = %d, want 1 (root)", len((*calls)[0].Opts))
	}
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

// daemonJSONSandbox returns a MockSandbox whose guest has the given
// /etc/docker/daemon.json contents (omitted when content is empty) and reports
// fuse-overlayfs as unavailable, exercising the vfs fallback.
func daemonJSONSandbox(content string) *msb.MockSandbox {
	return daemonJSONSandboxWithFuse(content, false)
}

// daemonJSONSandboxWithFuse returns a MockSandbox with the given daemon.json
// contents whose guest probe reports the fuse-overlayfs binary and /dev/fuse as
// available or not.
func daemonJSONSandboxWithFuse(content string, fuseAvailable bool) *msb.MockSandbox {
	files := map[string][]byte{}
	if content != "" {
		files[dockerdDaemonConfigPath] = []byte(content)
	}
	return &msb.MockSandbox{
		FSValue_: msb.NewTestFS(files, nil),
		ShellOut: map[string]msb.ShellResult{
			fuseOverlayfsProbeCmd: msb.NewTestResult(fuseAvailable, 0, "", "", nil),
		},
	}
}

func newDockerdStartSandbox(content string) (*dockerdStartSandbox, *[]msb.MockDetachedCall) {
	files := map[string][]byte{}
	if content != "" {
		files[dockerdDaemonConfigPath] = []byte(content)
	}
	detachedCalls := &[]msb.MockDetachedCall{}
	mock := &msb.MockSandbox{
		FSValue_:          msb.NewTestFS(files, nil),
		ExecDetachedCalls: detachedCalls,
	}
	return &dockerdStartSandbox{Sandbox: mock}, detachedCalls
}

func newCountingSandbox(readyCallsBeforeHealthy int) *countingSandbox {
	return &countingSandbox{
		MockSandbox:             &msb.MockSandbox{},
		readyCallsBeforeHealthy: readyCallsBeforeHealthy,
	}
}
