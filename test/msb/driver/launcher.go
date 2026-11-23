package driver

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"time"
)

const (
	// TuiReadyTimeout bounds how long a harness may take to reach its
	// full-screen TUI. The image prebuild pays the one-off microsandbox
	// runtime install, so this only has to absorb the VM start; the budget
	// stays generous for a cold host.
	TuiReadyTimeout = 180 * time.Second
	// prepareTimeout bounds the one-off launcher build and the per-harness
	// runner-image prebuild every subtest shares.
	prepareTimeout = 30 * time.Minute
	// prebuildConcurrency bounds how many harness images the suite builds and
	// loads at once. Each prebuild is dominated by the microsandbox image
	// transfer, which contends on disk, so the cap is intentionally small.
	prebuildConcurrency = 2
	// probeTimeout bounds a preflight or cleanup subprocess so a wedged docker
	// socket cannot hang the suite.
	probeTimeout = 30 * time.Second

	// kvmDevice is the Linux virtualization device microsandbox needs.
	kvmDevice = "/dev/kvm"
	// launcherPackage is the command the suite compiles and drives.
	launcherPackage = "github.com/inoio/agents-sandbox/cmd/agents-sandbox"
)

// The suite creates one shared root, builds the launcher into it once, and
// prebuilds every harness image once. Each phase is guarded by its own
// sync.Once; the stored error is reported by every caller so a failed phase
// fails each harness instead of being silently retried.
var (
	prepareOnce     sync.Once
	prebuildOnce    sync.Once
	smokeRoot       string
	smokeRootReused bool
	// smokeRootLockRelease releases the exclusive lock a reused root is held
	// under; nil for a fresh root. Prepare registers its release so it runs after
	// the sandbox sweep that shares the root.
	smokeRootLockRelease func()
	smokeBinary          string
	errSmokePrepare      error
	errPrebuild          error
)

// Preflight skips the suite when the host cannot run it at all: an unsupported
// platform or Linux without KVM. It never fails, needs no root, and runs before
// anything is built, so a host that cannot run the suite pays nothing.
func Preflight(tb TB) {
	tb.Helper()

	switch runtime.GOOS {
	case "linux":
		if _, err := os.Stat(kvmDevice); err != nil {
			tb.Skipf("harness smoke tests need %s: %v", kvmDevice, err)
		}
	case "darwin":
		// Apple Silicon virtualization needs no host device.
	default:
		tb.Skipf("harness smoke tests need linux or darwin, not %s", runtime.GOOS)
	}
}

// PreflightDocker skips the suite when no docker daemon is reachable. The probe
// runs under the same isolated environment the child gets, so it can never
// certify a daemon the child cannot reach — a docker context the child does not
// inherit, or a HOME the child does not share, would otherwise pass here and
// fail every harness. It never fails.
func PreflightDocker(tb TB, root string) {
	tb.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	defer cancel()

	//testenv:allow the docker preflight probes the real daemon the child will use
	info := exec.CommandContext(ctx, "docker", "info")
	//testenv:allow the docker preflight must see the daemon the child will use
	info.Env = IsolatedEnv(root, root, os.Environ())
	if out, err := info.CombinedOutput(); err != nil {
		tb.Skipf("harness smoke tests need a reachable docker daemon: %v\n%s", err, out)
	}
}

// Prepare creates the shared root and compiles the launcher into it once for the
// whole suite, and returns both. A freshly created root is registered for
// removal as soon as it exists, so a skip (the docker preflight) or a failed
// build cannot leave the root and its compiled binary behind; a reused root
// (SmokeRootReuseEnv) is kept. Destroying the sandboxes inside the root stays
// separate: that needs the binary and a reachable docker daemon, so it runs only
// after the docker preflight.
func Prepare(tb TB) (root, binary string) {
	tb.Helper()

	prepareOnce.Do(CreateRootAndBuildBinary)
	// Release the reuse lock last: cleanups run LIFO, and the sandbox sweep
	// registered after Prepare must run while the lock is still held.
	if smokeRootLockRelease != nil {
		releaseLock := smokeRootLockRelease
		tb.Cleanup(func() { releaseLock() })
	}
	if smokeRoot != "" && !smokeRootReused {
		created := smokeRoot
		tb.Cleanup(func() { removeTempRoot(tb, created) })
	}
	if errSmokePrepare != nil {
		tb.Fatalf("prepare harness smoke suite: %v", errSmokePrepare)
	}
	return smokeRoot, smokeBinary
}

// SmokeRootReused reports whether Prepare reused an existing suite root
// (SmokeRootReuseEnv) rather than creating a fresh one. A reused root keeps its
// microsandbox image cache, so the suite skips removing the runner images it
// shares with it.
func SmokeRootReused() bool {
	return smokeRootReused
}

// removeTempRoot deletes the suite's shared temp root. It runs in cleanup, so a
// failure is logged, never fatal.
func removeTempRoot(tb TB, root string) {
	tb.Helper()

	if err := os.RemoveAll(root); err != nil {
		tb.Logf("cleanup: remove temp root %s: %v", root, err)
	}
}

// CreateRootAndBuildBinary resolves the suite root and compiles the launcher
// into it.
func CreateRootAndBuildBinary() {
	root, reused, release, err := ResolveSuiteRoot()
	if err != nil {
		errSmokePrepare = err
		return
	}
	smokeRoot = root
	smokeRootReused = reused
	smokeRootLockRelease = release
	smokeBinary = filepath.Join(root, "agents-sandbox")
	if err := os.MkdirAll(filepath.Join(root, "tmp"), 0o750); err != nil {
		errSmokePrepare = fmt.Errorf("create temp dir: %w", err)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), prepareTimeout)
	defer cancel()

	build := exec.CommandContext(ctx, "go", "build", "-o", smokeBinary, launcherPackage)
	if out, err := build.CombinedOutput(); err != nil {
		errSmokePrepare = fmt.Errorf("build launcher: %w\n%s", err, out)
	}
}

// PrebuildImages builds every image in SuiteProjectAgents once, so each
// subtest's budget only has to absorb its VM start rather than a cold image
// build. It is the first docker-dependent step, so it runs only after
// PreflightDocker has confirmed the child can reach a daemon.
func PrebuildImages(tb TB, root string) {
	tb.Helper()

	prebuildOnce.Do(func() { errPrebuild = BuildHarnessImages(tb, root) })
	if errPrebuild != nil {
		tb.Fatalf("prebuild harness images: %v", errPrebuild)
	}
}

// BuildHarnessImages builds every image in SuiteProjectAgents in the same
// throwaway project directory its subtest will run from, so the project slug
// and agent identity match. The builds run up to prebuildConcurrency at a time;
// the isolated home pays for the microsandbox runtime install here, outside any
// harness's TUI budget. The env carries the environment's GitHub token when one
// is set, so its release resolutions authenticate.
func BuildHarnessImages(tb TB, root string) error {
	tb.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), prepareTimeout)
	defer cancel()

	//testenv:allow the prebuild runs under the host environment it authenticates from
	inherited := os.Environ()
	env := WithGitHubToken(IsolatedEnv(root, root, inherited), inherited)
	pairs := SuiteProjectAgents()
	for _, pair := range pairs {
		if err := os.MkdirAll(ProjectDir(root, pair.Project), 0o750); err != nil {
			return fmt.Errorf("create project dir for %s: %w", pair.Project, err)
		}
	}

	buildErrors := make([]error, len(pairs))
	semaphore := make(chan struct{}, prebuildConcurrency)
	var builds sync.WaitGroup
	for index, pair := range pairs {
		builds.Go(func() {
			semaphore <- struct{}{}
			defer func() { <-semaphore }()

			args := ImageBuildArgs(pair.Agent, pair.Docker)
			build := exec.CommandContext(ctx, smokeBinary, args...)

			build.Dir = ProjectDir(root, pair.Project)
			build.Env = env
			started := time.Now()
			out, err := build.CombinedOutput()
			// The prebuild is the suite's dominant one-off cost (a docker build
			// plus an `EnsureLoaded` transfer of a multi-GB runner image into
			// the microsandbox cache); the per-image duration separates build
			// from transfer when -v is in use.
			tb.Logf(
				"prebuilt %s image for project %s in %s",
				pair.Agent,
				pair.Project,
				time.Since(started).Round(time.Millisecond),
			)
			if err != nil {
				buildErrors[index] = fmt.Errorf(
					"prebuild image for %s (project %s): %w\n%s",
					pair.Agent,
					pair.Project,
					err,
					out,
				)
			}
		})
	}
	builds.Wait()
	return errors.Join(buildErrors...)
}

// ImageBuildArgs is the launcher invocation that builds agentName's runner
// image in the current project dir, docker-enabled when docker is set.
func ImageBuildArgs(agentName string, docker bool) []string {
	args := []string{"build", agentFlag, agentName}
	if docker {
		args = append(args, "--docker")
	}
	return args
}

// BuildProjectImage builds agentName's runner image for projectDir, creating the
// dir, so a `run --no-rebuild` there finds its image. A journey that runs on a
// project of its own calls it; the docker layers the prebuild cached make it
// cheap.
func BuildProjectImage(root, binary, projectDir, agentName string) error {
	if err := os.MkdirAll(projectDir, 0o750); err != nil {
		return fmt.Errorf("create project dir %s: %w", projectDir, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), prepareTimeout)
	defer cancel()
	//testenv:allow the image build runs under the host environment it authenticates from
	inherited := os.Environ()
	args := ImageBuildArgs(agentName, false)
	build := exec.CommandContext(ctx, binary, args...)

	build.Dir = projectDir
	build.Env = WithGitHubToken(IsolatedEnv(root, root, inherited), inherited)
	if out, err := build.CombinedOutput(); err != nil {
		return fmt.Errorf("build %s image for %s: %w\n%s", agentName, projectDir, err, out)
	}
	return nil
}

// SweepSandboxes best-effort destroys any sandbox left in the isolated home. It
// runs at suite start — so a reused root never carries a previous run's
// stopped-but-persisted VM into this run's subtests — and again at teardown, so
// a failed harness cannot leak a VM pointing at a home the suite is about to
// delete. when names the phase in its log lines. The root's own removal is
// registered by Prepare and runs after this. It logs failures and never fails
// the test.
func SweepSandboxes(tb TB, root, binary, when string) {
	tb.Helper()

	list, cancelList := BoundedLauncher(root, binary, "sandbox", "list", "-q")
	out, err := list.Output()
	cancelList()
	if err != nil {
		tb.Logf("%s: list sandboxes: %v", when, err)
	}
	for _, name := range sandboxNames(string(out)) {
		// --yes is required: without it the non-interactive remove blocks on
		// its confirmation prompt and fails.
		remove, cancelRemove := BoundedLauncher(root, binary, "sandbox", "remove", "--force", "--yes", name)
		removeOut, removeErr := remove.CombinedOutput()
		cancelRemove()
		if removeErr != nil {
			tb.Logf("%s: remove sandbox %s: %v\n%s", when, name, removeErr, removeOut)
		}
	}
}

// BoundedLauncher builds a launcher subcommand under the isolated environment
// with a bounded timeout, so a wedged socket cannot hang teardown. The caller
// must invoke the returned cancel once the command completes.
func BoundedLauncher(root, binary string, args ...string) (*exec.Cmd, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	cmd := exec.CommandContext(ctx, binary, args...)
	//testenv:allow the cleanup subprocess runs under the suite's isolated home
	cmd.Env = IsolatedEnv(root, root, os.Environ())
	return cmd, cancel
}

// The quit loop taps the harness's key in pairs until the process exits (see
// Quit). quitPressInterval keeps the two presses of a pair within the window a
// repeated-key exit's "press again" prompt allows — qwen-code's was measured
// under 1.2s, and a slower cadence stops the exit chaining — while
// quitRetryInterval spaces the pairs out, so a harness that has caught the quit
// can finish tearing down (and emit its alternate-screen leave) before the loop
// writes another press. QuitTimeout bounds the whole sequence.
const (
	QuitPressInterval = 400 * time.Millisecond
	quitRetryInterval = 2 * time.Second
	QuitTimeout       = 15 * time.Second
)

// ExitSaveTimeout bounds how long the launcher may take to exit after its learn
// exit prompt is answered. Answering "Save all" runs the after-run analysis and
// save path before the process exits, which can outlast the quit budget on a
// cold host — so the post-answer wait gets its own, larger bound rather than
// failing a run that is still working.
const ExitSaveTimeout = TuiReadyTimeout

// quitIntervalAfterPress returns the wait before the next quit press: a pair's
// two presses are one QuitPressInterval apart, and the even press ends the pair,
// after which the loop waits quitRetryInterval for the harness to exit or to
// signal its teardown.
func quitIntervalAfterPress(presses int) time.Duration {
	if presses%2 == 0 {
		return quitRetryInterval
	}
	return QuitPressInterval
}

// Quit taps the harness's key in pairs until the process exits and returns its
// exit code. Tapping on is what a user does, and it is what the harnesses need:
// the press that lands can be ignored because the TUI painted its screen before
// it accepted input (opencode draws its footer while it is still loading), and a
// harness whose exit is a repeated key — claude-code announces "Press Ctrl-C
// again to exit" — needs the taps to keep coming.
//
// Two measured facts shape the cadence. A repeated-key exit must chain: the
// arm and fire presses have to stay within the window its "press again" prompt
// allows, and qwen-code's was measured at under 1.2s (a 0.8s gap still chained,
// a 1.2s gap did not). And a harness that has caught the quit restores its
// terminal as it tears down, so a press written while it does is no longer a
// keystroke but the SIGINT a cooked terminal makes of it — qwen-code's slow
// "Agent powering down" teardown is killed this way, its clean exit reported as
// -1 (which os.Exit(-1) turns into the launcher's 255). A slow teardown can
// outlast one press interval, so a fixed cadence writes the next press into
// that window.
//
// So presses come in pairs: the two presses of a pair are kept one
// QuitPressInterval (400ms) apart, well inside the chaining window, and every
// pair is followed by quitRetryInterval (2s) before the next pair. The pair
// itself delivers an arm and a fire when the harness needs two presses; a slow
// teardown gets the 2s to finish, and its alternate-screen leave to appear, so
// the loop stops — see TeardownStarted — before it writes another press.
//
// The loop stops tapping once either teardown signal appears and only waits for
// the exit:
//
//   - the alternate screen came back (TuiLeft): the harness's own, guest-side
//     teardown signal, emitted while it restores its terminal. It is the only
//     signal visible before the guest exits, which is when qwen-code's slow
//     teardown gets tapped past.
//   - the test pty is back in cooked mode (Session's inRawMode): the launcher's
//     msb attach holds the pty raw for the session and restores it on the way
//     out, which covers a normal-buffer harness (claude-code) that emits no
//     alternate-screen sequence. It cannot stand in for the guest's restore,
//     though: the attach owns the host pty and only restores it after the guest
//     process has already exited, so it never observes the guest's earlier
//     restore.
func Quit(ses *Session, spec Spec) (int, error) {
	budget := ScaleTimeout(QuitTimeout, SmokeTimeoutFactor())
	deadline := time.Now().Add(budget)
	interval := QuitPressInterval
	for presses := 1; ; presses++ {
		if TeardownStarted(ses, presses) {
			return waitExitWithin(ses, deadline, budget, presses, spec)
		}
		if err := ses.Write(spec.QuitKey); err != nil {
			return 0, fmt.Errorf("press %#x: %w", spec.QuitKey, err)
		}
		code, err := ses.WaitExit(interval)
		if err == nil {
			return code, nil
		}
		if !time.Now().Before(deadline) {
			return 0, quitTimeoutError(presses, spec, budget, err)
		}
		interval = quitIntervalAfterPress(presses)
	}
}

// waitExitWithin waits out the remaining quit budget for a process that has
// begun tearing down, returning its exit code or the same timeout error the
// tapping loop reports.
func waitExitWithin(
	ses *Session,
	deadline time.Time,
	budget time.Duration,
	presses int,
	spec Spec,
) (int, error) {
	code, err := ses.WaitExit(time.Until(deadline))
	if err == nil {
		return code, nil
	}
	return 0, quitTimeoutError(presses, spec, budget, err)
}

// quitTimeoutError reports that a harness did not exit within budget after
// presses taps of its key.
func quitTimeoutError(presses int, spec Spec, budget time.Duration, err error) error {
	return fmt.Errorf("%d presses of %#x within %s did not exit it: %w",
		presses, spec.QuitKey, budget, err)
}

// WaitReady blocks until the harness can be driven with its quit key. A harness
// with a readiness marker announces its UI as soon as that line is printed; one
// that takes over the alternate screen announces it with the screen; one that
// keeps the terminal's normal buffer and has neither marker is given the
// launcher's start-up time instead.
func WaitReady(ses *Session, spec Spec) error {
	factor := SmokeTimeoutFactor()
	if spec.ReadyMarker != "" {
		return ses.WaitForMarker(spec.ReadyMarker, ScaleTimeout(spec.ReadyTimeout(), factor))
	}
	if spec.KeepsNormalBuffer {
		return ses.WaitSettled(ScaleTimeout(spec.ReadyTimeout(), factor))
	}
	return ses.WaitForTUI(ScaleTimeout(TuiReadyTimeout, factor))
}
