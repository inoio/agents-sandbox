package vm

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// TestDockerdArgsSelectsTheStorageDriver pins the storage driver dockerd is
// started with. Without a driver in the image's daemon.json it is
// fuse-overlayfs when the guest supports it (its copy-on-write layers beat
// vfs's full copy per layer, #489) and vfs otherwise, passed as a flag; a probe
// that cannot run falls back to vfs. A driver daemon.json sets is honored
// without the flag, since dockerd refuses a directive set both ways (#287). Any
// other driver, a non-string value, or an unreadable daemon.json fails.
func TestDockerdArgsSelectsTheStorageDriver(t *testing.T) {
	readErr := errors.New("read boom")
	cases := []struct {
		name       string
		sb         func() *msb.MockSandbox
		wantDriver string
		wantFlag   bool
		wantErr    func(error) bool
	}{
		{
			name:       "no daemon.json, no fuse",
			sb:         func() *msb.MockSandbox { return daemonJSONSandbox("") },
			wantDriver: vfsStorageDriver,
			wantFlag:   true,
		},
		{
			name:       "no daemon.json, fuse available",
			sb:         func() *msb.MockSandbox { return daemonJSONSandboxWithFuse("", true) },
			wantDriver: fuseOverlayfsStorageDriver,
			wantFlag:   true,
		},
		{
			name: "fuse probe fails",
			sb: func() *msb.MockSandbox {
				return &msb.MockSandbox{FSValue_: msb.NewTestFS(nil), ShellErr: errors.New("probe boom")}
			},
			wantDriver: vfsStorageDriver,
			wantFlag:   true,
		},
		{
			name:       "unrelated keys",
			sb:         func() *msb.MockSandbox { return daemonJSONSandbox(`{"registry-mirrors":["https://mirror.example"]}`) },
			wantDriver: vfsStorageDriver,
			wantFlag:   true,
		},
		{
			name:       "malformed daemon.json",
			sb:         func() *msb.MockSandbox { return daemonJSONSandbox(`{"storage-driver":`) },
			wantDriver: vfsStorageDriver,
			wantFlag:   true,
		},
		{
			name:       "null driver",
			sb:         func() *msb.MockSandbox { return daemonJSONSandbox(`{"storage-driver":null}`) },
			wantDriver: vfsStorageDriver,
			wantFlag:   true,
		},
		{
			name:       "daemon.json sets vfs",
			sb:         func() *msb.MockSandbox { return daemonJSONSandbox(`{"storage-driver":"vfs"}`) },
			wantDriver: vfsStorageDriver,
		},
		{
			name: "daemon.json sets fuse-overlayfs",
			sb: func() *msb.MockSandbox {
				return daemonJSONSandboxWithFuse(`{"storage-driver":"fuse-overlayfs"}`, false)
			},
			wantDriver: fuseOverlayfsStorageDriver,
		},
		{
			name: "daemon.json sets another driver",
			sb:   func() *msb.MockSandbox { return daemonJSONSandbox(`{"storage-driver":"overlay2"}`) },
			wantErr: func(err error) bool {
				return strings.Contains(err.Error(), "overlay2") &&
					strings.Contains(err.Error(), vfsStorageDriver) &&
					strings.Contains(err.Error(), fuseOverlayfsStorageDriver)
			},
		},
		{
			name:    "non-string driver",
			sb:      func() *msb.MockSandbox { return daemonJSONSandbox(`{"storage-driver":42}`) },
			wantErr: func(err error) bool { return strings.Contains(err.Error(), "storage-driver") },
		},
		{
			name: "unreadable daemon.json",
			sb: func() *msb.MockSandbox {
				fs := msb.NewTestFS(map[string][]byte{dockerdDaemonConfigPath: []byte(`{}`)})
				fs.ReadErr = readErr
				return &msb.MockSandbox{FSValue_: fs}
			},
			wantErr: func(err error) bool { return errors.Is(err, readErr) },
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			args, driver, err := dockerdArgs(context.Background(), tc.sb())
			if tc.wantErr != nil {
				if err == nil || !tc.wantErr(err) {
					t.Fatalf("dockerdArgs error = %v, want the driver rejected", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("dockerdArgs: %v", err)
			}
			if driver != tc.wantDriver {
				t.Errorf("driver = %q, want %q", driver, tc.wantDriver)
			}
			hasFlag := slices.ContainsFunc(
				args,
				func(arg string) bool { return strings.HasPrefix(arg, "--storage-driver") },
			)
			if hasFlag != tc.wantFlag || (tc.wantFlag && !slices.Contains(args, "--storage-driver="+tc.wantDriver)) {
				t.Errorf("args = %v, want the --storage-driver=%s flag: %v", args, tc.wantDriver, tc.wantFlag)
			}
			if !slices.Contains(args, dockerdSocket) {
				t.Errorf("args = %v, want dockerd started on its socket", args)
			}
		})
	}
}

// TestGuestStorageDriverExistsErrorWrapsCause pins that daemon.json that cannot
// be checked for aborts with its cause, rather than being treated as absent.
func TestGuestStorageDriverExistsErrorWrapsCause(t *testing.T) {
	sentinel := errors.New("exists boom")
	sb := &msb.MockSandbox{FSValue_: existsErrorFS{TestFS: msb.NewTestFS(nil), err: sentinel}}

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

// TestStartDockerdTreatsAMissingProbeResultAsNotReady pins that a readiness
// probe returning no result counts as not ready: startup times out rather than
// panicking.
func TestStartDockerdTreatsAMissingProbeResultAsNotReady(t *testing.T) {
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
	if err == nil || !strings.Contains(err.Error(), "did not become ready") {
		t.Fatalf("error = %v, want dockerd reported as not ready", err)
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

// TestStartDockerdStopsWhenCancelled pins that a cancelled context ends the
// dockerd readiness wait with the cancellation.
func TestStartDockerdStopsWhenCancelled(t *testing.T) {
	ui := termio.NewTestMock(t)
	sb := &msb.MockSandbox{
		ShellOut: map[string]msb.ShellResult{
			dockerdBinaryCheckCmd: msb.NewTestResult(true, 0, "", "", nil),
			dockerdReadyCmd:       msb.NewTestResult(false, 1, "", "", nil),
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := startDockerdIfPresent(ctx, sb, &ui); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want the cancellation", err)
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
	// countingSandbox reports fuse-overlayfs as available (#489).
	if sb.execArgs == nil || !slices.Contains(*sb.execArgs, "--storage-driver="+fuseOverlayfsStorageDriver) {
		t.Errorf("dockerd launched with %v, want the fuse-overlayfs driver", sb.execArgs)
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
	if err == nil || !strings.Contains(err.Error(), "did not become ready") {
		t.Fatalf("error = %v, want dockerd reported as not ready", err)
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
	stale := &msb.MockJob{}
	running := &msb.MockJob{}
	sb := newCountingSandbox(1)
	// The stale job was launched by a release whose dockerd arguments differ
	// from this one's (no storage-driver flag), which is exactly the case the
	// old binary+args matcher missed (#343).
	sb.Jobs = []msb.JobInfo{
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
	// dockerd runs as root, so its detached launch carries the root exec
	// user; a helper that dropped it would leave dockerd as the
	// default user.
	if (*calls)[0].Spec.User != RootUser {
		t.Errorf("dockerd launch exec user = %q, want %q", (*calls)[0].Spec.User, RootUser)
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
	sb.Job_ = &msb.MockJob{CloseErr: errors.New("close boom")}

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
		Logs_: []msb.JobLogEntry{{Data: []byte(sentinel)}},
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
		FSValue_: msb.NewTestFS(files),
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
		FSValue_:          msb.NewTestFS(files),
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
