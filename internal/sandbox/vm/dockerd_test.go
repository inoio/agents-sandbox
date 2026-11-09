package vm

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb/msbtest"
	"github.com/inoio/agents-sandbox/internal/termio/termiotest"
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
		sb         func() *msbtest.MockSandbox
		wantDriver string
		wantFlag   bool
		wantErr    func(error) bool
	}{
		{
			name:       "no daemon.json, no fuse",
			sb:         func() *msbtest.MockSandbox { return daemonJSONSandbox("") },
			wantDriver: vfsStorageDriver,
			wantFlag:   true,
		},
		{
			name:       "no daemon.json, fuse available",
			sb:         func() *msbtest.MockSandbox { return daemonJSONSandboxWithFuse("", true) },
			wantDriver: fuseOverlayfsStorageDriver,
			wantFlag:   true,
		},
		{
			name: "fuse probe fails",
			sb: func() *msbtest.MockSandbox {
				return &msbtest.MockSandbox{FSValue_: msbtest.NewTestFS(nil), ShellErr: errors.New("probe boom")}
			},
			wantDriver: vfsStorageDriver,
			wantFlag:   true,
		},
		{
			name: "unrelated keys",
			sb: func() *msbtest.MockSandbox {
				return daemonJSONSandbox(`{"registry-mirrors":["https://mirror.example"]}`)
			},
			wantDriver: vfsStorageDriver,
			wantFlag:   true,
		},
		{
			name:       "malformed daemon.json",
			sb:         func() *msbtest.MockSandbox { return daemonJSONSandbox(`{"storage-driver":`) },
			wantDriver: vfsStorageDriver,
			wantFlag:   true,
		},
		{
			name:       "null driver",
			sb:         func() *msbtest.MockSandbox { return daemonJSONSandbox(`{"storage-driver":null}`) },
			wantDriver: vfsStorageDriver,
			wantFlag:   true,
		},
		{
			name:       "daemon.json sets vfs",
			sb:         func() *msbtest.MockSandbox { return daemonJSONSandbox(`{"storage-driver":"vfs"}`) },
			wantDriver: vfsStorageDriver,
		},
		{
			name: "daemon.json sets fuse-overlayfs",
			sb: func() *msbtest.MockSandbox {
				return daemonJSONSandboxWithFuse(`{"storage-driver":"fuse-overlayfs"}`, false)
			},
			wantDriver: fuseOverlayfsStorageDriver,
		},
		{
			name: "daemon.json sets another driver",
			sb:   func() *msbtest.MockSandbox { return daemonJSONSandbox(`{"storage-driver":"overlay2"}`) },
			wantErr: func(err error) bool {
				return strings.Contains(err.Error(), "overlay2") &&
					strings.Contains(err.Error(), vfsStorageDriver) &&
					strings.Contains(err.Error(), fuseOverlayfsStorageDriver)
			},
		},
		{
			name:    "non-string driver",
			sb:      func() *msbtest.MockSandbox { return daemonJSONSandbox(`{"storage-driver":42}`) },
			wantErr: func(err error) bool { return strings.Contains(err.Error(), "storage-driver") },
		},
		{
			name: "unreadable daemon.json",
			sb: func() *msbtest.MockSandbox {
				fs := msbtest.NewTestFS(map[string][]byte{dockerdDaemonConfigPath: []byte(`{}`)})
				fs.ReadErr = readErr
				return &msbtest.MockSandbox{FSValue_: fs}
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
			if !slices.Contains(args, "--data-root="+dockerdDataRoot) {
				t.Errorf("args = %v, want the engine data on the VM root disk", args)
			}
			if slices.Contains(args, "-H") {
				t.Errorf("args = %v, want the rootless default socket, not a -H override", args)
			}
		})
	}
}

// TestDockerdArgsKeepsTheImageDaemonJSON pins that the image's daemon.json stays
// the engine's configuration: rootless dockerd reads ~/.config/docker by
// default, so the launcher names /etc/docker/daemon.json explicitly when it
// exists, and drops a launcher flag the file already sets, since dockerd
// refuses a directive set both ways (#287).
func TestDockerdArgsKeepsTheImageDaemonJSON(t *testing.T) {
	cases := []struct {
		name           string
		content        string
		wantConfigFile bool
		wantDataRoot   bool
	}{
		{name: "no daemon.json", content: "", wantConfigFile: false, wantDataRoot: true},
		{name: "unrelated keys", content: `{"registry-mirrors":[]}`, wantConfigFile: true, wantDataRoot: true},
		{name: "daemon.json sets data-root", content: `{"data-root":"/srv/docker"}`, wantConfigFile: true},
		{name: "malformed daemon.json", content: `{"data-root":`, wantConfigFile: true, wantDataRoot: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			args, _, err := dockerdArgs(context.Background(), daemonJSONSandbox(tc.content))
			if err != nil {
				t.Fatalf("dockerdArgs: %v", err)
			}
			configFileAt := slices.Index(args, "--config-file")
			hasConfigFile := configFileAt >= 0 && configFileAt+1 < len(args) &&
				args[configFileAt+1] == dockerdDaemonConfigPath
			if hasConfigFile != tc.wantConfigFile {
				t.Errorf("args = %v, want --config-file %s: %v", args, dockerdDaemonConfigPath, tc.wantConfigFile)
			}
			hasDataRoot := slices.ContainsFunc(
				args,
				func(arg string) bool { return strings.HasPrefix(arg, "--data-root") },
			)
			if hasDataRoot != tc.wantDataRoot {
				t.Errorf("args = %v, want the --data-root flag: %v", args, tc.wantDataRoot)
			}
		})
	}
}

// TestDockerdArgsRejectsANonStringDataRoot pins that a data-root dockerd could
// not read as a path fails before the launch, like a non-string driver.
func TestDockerdArgsRejectsANonStringDataRoot(t *testing.T) {
	_, _, err := dockerdArgs(context.Background(), daemonJSONSandbox(`{"data-root":42}`))
	if err == nil || !strings.Contains(err.Error(), "data-root") {
		t.Fatalf("dockerdArgs error = %v, want the non-string data-root rejected", err)
	}
}

// TestReadGuestDaemonConfigExistsErrorWrapsCause pins that daemon.json that
// cannot be checked for aborts with its cause, rather than being treated as
// absent.
func TestReadGuestDaemonConfigExistsErrorWrapsCause(t *testing.T) {
	sentinel := errors.New("exists boom")
	sb := &msbtest.MockSandbox{FSValue_: existsErrorFS{TestFS: msbtest.NewTestFS(nil), err: sentinel}}

	config, err := readGuestDaemonConfig(context.Background(), sb)
	if err == nil {
		t.Fatalf("expected an Exists failure to abort, got %+v", config)
	}
	if config != (guestDaemonConfig{}) {
		t.Errorf("config = %+v, want empty on error", config)
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

	ui := termiotest.NewTestMock(t)
	sb := &nilResultDockerd{Sandbox: msbtest.NewMockSandbox(msbtest.SandboxOpts{})}

	err := startDockerdIfPresent(context.Background(), sb, &ui)
	if err == nil || !strings.Contains(err.Error(), "did not become ready") {
		t.Fatalf("error = %v, want dockerd reported as not ready", err)
	}
}

func TestStartDockerdUsesConfigDrivenArgsWhenDaemonJSONSetsVFS(t *testing.T) {
	ui := termiotest.NewTestMock(t)
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
	ui := termiotest.NewTestMock(t)
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
	ui := termiotest.NewTestMock(t)
	sb := &msbtest.MockSandbox{
		ShellOut: map[string]msb.ShellResult{
			dockerdBinaryCheckCmd: msbtest.NewTestResult(true, 0, "", "", nil),
			dockerdReadyCmd:       msbtest.NewTestResult(false, 1, "", "", nil),
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
	ui := termiotest.NewTestMock(t)
	sentinel := errors.New("shell boom")
	sb := msbtest.NewMockSandbox(msbtest.SandboxOpts{ShellErr: sentinel})

	err := startDockerdIfPresent(context.Background(), sb, &ui)
	if err == nil {
		t.Fatal("expected a binary-check Shell failure to abort")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("error = %v, want it to wrap the Shell cause", err)
	}
}

func TestStartDockerdIfPresentNoDockerdBinary(t *testing.T) {
	ui := termiotest.NewTestMock(t)
	sb := msbtest.NewMockSandbox(msbtest.SandboxOpts{
		ShellOut: map[string]msb.ShellResult{
			dockerdBinaryCheckCmd: msbtest.NewTestResult(false, 1, "", "", nil),
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
	ui := termiotest.NewTestMock(t)
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
	ui := termiotest.NewTestMock(t)
	sb := newCountingSandbox(1)

	if err := startDockerdIfPresent(context.Background(), sb, &ui); err != nil {
		t.Fatalf("startDockerdIfPresent should detach and become ready, got: %v", err)
	}
	if sb.execCalls != 1 {
		t.Errorf("expected one detach attempt, got %d", sb.execCalls)
	}
	if !slices.Contains(ui.VerboseCalls, "starting rootless dockerd with fuse-overlayfs storage driver") {
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
	ui := termiotest.NewTestMock(t)
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
	ui := termiotest.NewTestMock(t)
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
	ui := termiotest.NewTestMock(t)
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
	ui := termiotest.NewTestMock(t)
	stale := &msbtest.MockJob{}
	running := &msbtest.MockJob{}
	sb := newCountingSandbox(1)
	// The stale job is the rootful dockerd an older release launched under the
	// same marker: its binary and arguments differ from this release's, which is
	// exactly the case a binary+args matcher missed (#343).
	sb.Jobs = []msb.JobInfo{{
		ID:      "stale-dockerd",
		Command: []string{"env", "AGENTS_SANDBOX_DETACHED_JOB=dockerd", "dockerd", "-H", "unix:///var/run/docker.sock"},
	}}
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
	ui := termiotest.NewTestMock(t)
	sb := newCountingSandbox(1)
	calls := &[]msbtest.MockDetachedCall{}
	sb.ExecDetachedCalls = calls

	if err := startDockerdIfPresent(context.Background(), sb, &ui); err != nil {
		t.Fatalf("startDockerdIfPresent: %v", err)
	}
	assertDetachedHelperLaunch(t, *calls, "dockerd", "dockerd-rootless.sh")
	// The engine runs rootless as dev, never as root: the launch carries dev
	// explicitly and the runtime directory that holds its socket and state.
	spec := (*calls)[0].Spec
	if spec.User != "dev" {
		t.Errorf("dockerd launch exec user = %q, want %q", spec.User, "dev")
	}
	if got := spec.Env["XDG_RUNTIME_DIR"]; got != "/run/agents-sandbox/docker" {
		t.Errorf("dockerd launch XDG_RUNTIME_DIR = %q, want %q", got, "/run/agents-sandbox/docker")
	}
}

// TestStartDockerdPreparesTheRootlessRuntimeAsRoot pins the root-side setup the
// rootless engine needs on every launch, in order: it runs as root before the
// fuse-overlayfs probe (which runs as dev and needs the opened /dev/fuse), and
// it makes the devices the guest recreates 0600 each boot usable by dev, creates
// the dev-only runtime and data directories, and links the default socket.
func TestStartDockerdPreparesTheRootlessRuntimeAsRoot(t *testing.T) {
	ui := termiotest.NewTestMock(t)
	sb := &userRecordingSandbox{MockSandbox: &msbtest.MockSandbox{
		ShellOut: map[string]msb.ShellResult{dockerdReadyCmd: msbtest.NewTestResult(false, 1, "", "", nil)},
	}}
	origTimeout, origInterval := dockerdReadyTimeout, dockerdPollInterval
	t.Cleanup(func() { dockerdReadyTimeout, dockerdPollInterval = origTimeout, origInterval })
	dockerdReadyTimeout, dockerdPollInterval = time.Millisecond, time.Millisecond

	_ = startDockerdIfPresent(context.Background(), sb, &ui)

	prepareAt := slices.Index(sb.commands, dockerdRootlessPrepareCmd)
	probeAt := slices.Index(sb.commands, fuseOverlayfsProbeCmd)
	if prepareAt < 0 || probeAt < 0 || prepareAt > probeAt {
		t.Fatalf("commands = %v, want the prepare step before the fuse-overlayfs probe", sb.commands)
	}
	if sb.users[prepareAt] != RootUser {
		t.Errorf("prepare ran as %q, want %q", sb.users[prepareAt], RootUser)
	}
	if sb.users[probeAt] != "dev" {
		t.Errorf("fuse-overlayfs probe ran as %q, want %q (the engine's user)", sb.users[probeAt], "dev")
	}
	for _, want := range []string{
		"set -e",
		"chmod 0666 /dev/net/tun",
		"chmod 0666 /dev/fuse",
		"mkdir -p /run/agents-sandbox/docker /var/lib/agents-sandbox/docker",
		"chown dev:dev /run/agents-sandbox/docker /var/lib/agents-sandbox/docker",
		"chmod 0700 /run/agents-sandbox/docker",
		"ln -sfn /run/agents-sandbox/docker/docker.sock /var/run/docker.sock",
	} {
		if !strings.Contains(dockerdRootlessPrepareCmd, want) {
			t.Errorf("dockerdRootlessPrepareCmd missing %q:\n%s", want, dockerdRootlessPrepareCmd)
		}
	}
}

// TestStartDockerdFailsWhenThePrepareStepFails pins that a rootless runtime the
// launcher cannot set up stops before the launch with the cause, instead of
// surfacing as the readiness timeout.
func TestStartDockerdFailsWhenThePrepareStepFails(t *testing.T) {
	ui := termiotest.NewTestMock(t)
	detached := &[]msbtest.MockDetachedCall{}
	sb := &msbtest.MockSandbox{
		ExecDetachedCalls: detached,
		ShellOut: map[string]msb.ShellResult{
			dockerdReadyCmd:           msbtest.NewTestResult(false, 1, "", "", nil),
			dockerdRootlessPrepareCmd: msbtest.NewTestResult(false, 1, "", "chmod: /dev/net/tun: No such file", nil),
		},
	}

	err := startDockerdIfPresent(context.Background(), sb, &ui)
	if err == nil || !strings.Contains(err.Error(), "/dev/net/tun") {
		t.Fatalf("error = %v, want the prepare failure with its output", err)
	}
	if len(*detached) != 0 {
		t.Errorf("the engine must not launch after a failed prepare, got %+v", *detached)
	}
}

// TestStartDockerdWrapsARootlessSetupTransportError pins that a shell transport
// failure while checking for or preparing the rootless engine aborts with its
// cause and launches nothing, rather than reading as a missing entrypoint or a
// readiness timeout.
func TestStartDockerdWrapsARootlessSetupTransportError(t *testing.T) {
	for _, failing := range []string{dockerdRootlessCheckCmd, dockerdRootlessPrepareCmd} {
		t.Run(failing, func(t *testing.T) {
			ui := termiotest.NewTestMock(t)
			sentinel := errors.New("transport boom")
			detached := &[]msbtest.MockDetachedCall{}
			sb := &commandErrorSandbox{
				MockSandbox: &msbtest.MockSandbox{
					ExecDetachedCalls: detached,
					ShellOut: map[string]msb.ShellResult{
						dockerdReadyCmd: msbtest.NewTestResult(false, 1, "", "", nil),
					},
				},
				failing: failing,
				err:     sentinel,
			}

			err := startDockerdIfPresent(context.Background(), sb, &ui)
			if !errors.Is(err, sentinel) {
				t.Fatalf("error = %v, want it to wrap the transport cause", err)
			}
			if len(*detached) != 0 {
				t.Errorf("the engine must not launch after a setup failure, got %+v", *detached)
			}
		})
	}
}

// TestStartDockerdRequiresTheRootlessEntrypoint pins the stale-image case: an
// image that has dockerd but predates the rootless engine fails with a rebuild
// hint rather than starting a root-equivalent engine.
func TestStartDockerdRequiresTheRootlessEntrypoint(t *testing.T) {
	ui := termiotest.NewTestMock(t)
	detached := &[]msbtest.MockDetachedCall{}
	sb := &msbtest.MockSandbox{
		ExecDetachedCalls: detached,
		ShellOut: map[string]msb.ShellResult{
			dockerdReadyCmd:         msbtest.NewTestResult(false, 1, "", "", nil),
			dockerdRootlessCheckCmd: msbtest.NewTestResult(false, 1, "", "", nil),
		},
	}

	err := startDockerdIfPresent(context.Background(), sb, &ui)
	if err == nil || !strings.Contains(err.Error(), "dockerd-rootless.sh") ||
		!strings.Contains(err.Error(), "--rebuild") {
		t.Fatalf("error = %v, want the missing entrypoint named with a --rebuild hint", err)
	}
	if len(*detached) != 0 {
		t.Errorf("no engine may start without the rootless entrypoint, got %+v", *detached)
	}
}

func TestStartDockerdIfPresentContinuesAfterCleanupError(t *testing.T) {
	ui := termiotest.NewTestMock(t)
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
	ui := termiotest.NewTestMock(t)
	sb := newCountingSandbox(1)
	sb.Job_ = &msbtest.MockJob{CloseErr: errors.New("close boom")}

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
	sb.Job_ = &msbtest.MockJob{
		Logs_: []msb.JobLogEntry{{Data: []byte(sentinel)}},
	}
	ui := termiotest.NewTestMock(t)

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

// TestDockerdCleanupCmdRemovesRootlessResidue pins the residue a killed engine
// job may leave: the job kill owns the engine itself, while helper processes
// and the runtime directory's sockets, pid files, and state are cleared so the
// fresh launch starts clean.
func TestDockerdCleanupCmdRemovesRootlessResidue(t *testing.T) {
	for _, want := range []string{
		"pkill -KILL -x containerd",
		"pkill -KILL -x slirp4netns",
		"find /run/agents-sandbox/docker -mindepth 1 -delete",
	} {
		if !strings.Contains(dockerdCleanupCmd, want) {
			t.Errorf("dockerdCleanupCmd missing %q:\n%s", want, dockerdCleanupCmd)
		}
	}
	if strings.Contains(dockerdCleanupCmd, "-x dockerd") || strings.Contains(dockerdCleanupCmd, "-x rootlesskit") {
		t.Errorf("dockerdCleanupCmd must not kill dockerd (job kill owns that):\n%s", dockerdCleanupCmd)
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
func daemonJSONSandbox(content string) *msbtest.MockSandbox {
	return daemonJSONSandboxWithFuse(content, false)
}

// daemonJSONSandboxWithFuse returns a MockSandbox with the given daemon.json
// contents whose guest probe reports the fuse-overlayfs binary and /dev/fuse as
// available or not.
func daemonJSONSandboxWithFuse(content string, fuseAvailable bool) *msbtest.MockSandbox {
	files := map[string][]byte{}
	if content != "" {
		files[dockerdDaemonConfigPath] = []byte(content)
	}
	return &msbtest.MockSandbox{
		FSValue_: msbtest.NewTestFS(files),
		ShellOut: map[string]msb.ShellResult{
			fuseOverlayfsProbeCmd: msbtest.NewTestResult(fuseAvailable, 0, "", "", nil),
		},
	}
}

func newDockerdStartSandbox(content string) (*dockerdStartSandbox, *[]msbtest.MockDetachedCall) {
	files := map[string][]byte{}
	if content != "" {
		files[dockerdDaemonConfigPath] = []byte(content)
	}
	detachedCalls := &[]msbtest.MockDetachedCall{}
	mock := &msbtest.MockSandbox{
		FSValue_:          msbtest.NewTestFS(files),
		ExecDetachedCalls: detachedCalls,
	}
	return &dockerdStartSandbox{Sandbox: mock}, detachedCalls
}

func newCountingSandbox(readyCallsBeforeHealthy int) *countingSandbox {
	return &countingSandbox{
		MockSandbox:             &msbtest.MockSandbox{},
		readyCallsBeforeHealthy: readyCallsBeforeHealthy,
	}
}
