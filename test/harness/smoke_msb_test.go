//go:build msb

package harness

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/sysinfo"
)

const (
	// tuiReadyTimeout bounds how long a harness may take to reach its
	// full-screen TUI. The image prebuild pays the one-off microsandbox
	// runtime install, so this only has to absorb the VM start; the budget
	// stays generous for a cold host.
	tuiReadyTimeout = 180 * time.Second
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
	// networkNone is the secure default network policy for a smoke run.
	networkNone = "none"
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
	// under; nil for a fresh root. prepare registers its release so it runs after
	// the sandbox sweep that shares the root.
	smokeRootLockRelease func()
	smokeBinary          string
	errSmokePrepare      error
	errPrebuild          error
)

// harnessConcurrency is the number of harness VMs to run at once: an explicit
// AGENTS_SANDBOX_MSB_PARALLEL override, else the count the host's free memory
// fits, else one. It reads the host, so it lives in the msb-tagged file;
// harnessConcurrencyFor holds the policy. It is enforced by a semaphore in
// TestHarnessSmoke, not only by `-parallel`, so even a bare
// `go test -tags msb ./...` cannot start a VM per harness.
func harnessConcurrency() int {
	if override := os.Getenv(harnessConcurrencyOverrideEnv); override != "" {
		if n, err := strconv.Atoi(override); err == nil && n > 0 {
			return n
		}
	}
	if free, ok := sysinfo.AvailableMemoryMiB(); ok {
		return harnessConcurrencyFor(int(free))
	}
	if total := sysinfo.TotalMemoryGiB(); total > 0 {
		return harnessConcurrencyFor(total * 1024)
	}
	return 1
}

// smokeVMCPUs is the per-VM vCPU cap each smoke run carries, read from the
// environment: the KVM guest sets AGENTS_SANDBOX_MSB_VM_CPUS so parallel
// harnesses do not each claim every guest vCPU. Zero (unset or invalid) leaves
// the launcher's own default. It reads the environment, so it lives in the
// msb-tagged file; smokeVMCPUsFromValue holds the parsing policy.
func smokeVMCPUs() uint8 {
	return smokeVMCPUsFromValue(os.Getenv(smokeVMCPUsEnv))
}

// smokeTimeoutFactor is the multiplier every smoke readiness and quit ceiling
// carries, read from the environment: the KVM guest sets
// AGENTS_SANDBOX_MSB_TIMEOUT_FACTOR, so a nested guest's slower VM start does
// not blow a budget sized for a host. One (unset or invalid) leaves every
// ceiling unchanged. It reads the environment, so it lives in the msb-tagged
// file; timeoutFactorFromValue holds the parsing policy.
func smokeTimeoutFactor() float64 {
	return timeoutFactorFromValue(os.Getenv(smokeTimeoutFactorEnv))
}

// TestHarnessSmoke starts every registered harness the way a user does
// (`agents-sandbox run --agent <name>`) inside a real microsandbox VM and
// asserts it reaches its full-screen TUI, exits 0 on its quit key, and never
// reports a daemon timeout. A sibling subtest runs the docker-storage smoke
// (runDockerStorageDriverSmoke) in the same suite. It requires KVM, a docker
// daemon, and network; the `msb` build tag keeps it out of the default test run.
//
// The checks run in order so an environment that cannot run the suite skips
// before paying for it: the platform/`/dev/kvm` preflight first (no build), then
// the shared root and binary, then the isolated-env docker preflight, then the
// image prebuild (the first docker-dependent step), then the harnesses.
//
// The per-harness subtests run in parallel, bounded by a semaphore sized to the
// host's free memory (harnessConcurrency); each gets its own prepared HOME tree
// (harnessHomeBase) while sharing the root's microsandbox home, so the image
// cache stays common.
//
//nolint:tparallel // top-level tests share the suite root and docker/VM state and must not overlap
func TestHarnessSmoke(t *testing.T) {
	preflight(t)
	root, binary := prepare(t)
	preflightDocker(t, root)
	// Clear a reused root's leftovers before anything starts: a previous run
	// that was killed, or whose teardown could not list, leaves a
	// stopped-but-persisted sandbox that the next run's EnsureProjectVM would
	// restart, paying the whole start cost again and skewing the timing.
	sweepSandboxes(t, root, binary, "startup")
	// Register cleanup after the docker preflight so a skip touches nothing, but
	// before the prebuild so a prebuild failure cannot leak the root. t.Cleanup,
	// not defer: it must run after the parallel subtests, which resume only once
	// this function returns.
	t.Cleanup(func() { sweepSandboxes(t, root, binary, "cleanup") })
	if !smokeRootReused {
		t.Cleanup(func() { cleanupRunnerImages(t, root) })
	}
	prebuildImages(t, root)

	slots := make(chan struct{}, harnessConcurrency())
	t.Logf("running up to %d smoke subtests in parallel", cap(slots))
	for _, name := range agent.Names() {
		spec, ok := harnessSpecs[name]
		if !ok {
			t.Fatalf("harness %q has no smoke-test spec", name)
		}
		network := spec.Network
		if network == "" {
			network = networkNone
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			slots <- struct{}{}
			defer func() { <-slots }()
			homeBase := harnessHomeBase(root, name)
			if err := prepareHome(homeBase); err != nil {
				t.Fatalf("prepare %s home: %v", name, err)
			}
			if err := seedHomeFiles(homeBase, spec.SeedHomeFiles); err != nil {
				t.Fatalf("seed %s home files: %v", name, err)
			}
			if err := seedLauncherConfig(homeBase, spec.SeedHomeManifest); err != nil {
				t.Fatalf("seed %s launcher config: %v", name, err)
			}
			profile, _ := agent.Lookup(name)
			if err := seedAgentSnippets(homeBase, profile.ConfigDirName(), spec.SeedAgentSnippets); err != nil {
				t.Fatalf("seed %s agent snippets: %v", name, err)
			}
			ses := mustStartUnderPTY(
				t, projectDir(root, name),
				isolatedEnv(root, homeBase, os.Environ()),
				append([]string{binary}, withVMCPUs(harnessRunArgs(name, network), smokeVMCPUs())...)...,
			)
			if err := waitReady(ses, spec); err != nil {
				t.Fatalf("harness %s did not reach its TUI: %v\n--- output ---\n%s", name, err, stripANSI(ses.output()))
			}
			code, err := quit(ses, spec)
			if err != nil {
				t.Fatalf("harness %s did not exit: %v\n--- output ---\n%s",
					name, err, stripANSI(ses.output()))
			}
			if code != 0 {
				t.Fatalf("harness %s exited %d, want 0\n--- output ---\n%s", name, code, stripANSI(ses.output()))
			}
			if sawDaemonTimeout(ses.output()) {
				t.Fatalf("harness %s reported a daemon timeout:\n%s", name, stripANSI(ses.output()))
			}
		})
	}

	// The docker-storage smoke is not a harness (the table's key set must equal
	// agent.Names()), so it runs as a sibling subtest: it shares the suite root,
	// home, prebuild, and cleanup, and overlaps the harnesses through the same
	// slot pool.
	t.Run("docker-storage-driver", func(t *testing.T) {
		t.Parallel()
		slots <- struct{}{}
		defer func() { <-slots }()
		runDockerStorageDriverSmoke(t, root, binary)
	})
}

// quit taps the harness's key in pairs until the process exits and returns its
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
// quitPressInterval (400ms) apart, well inside the chaining window, and every
// pair is followed by quitRetryInterval (2s) before the next pair. The pair
// itself delivers an arm and a fire when the harness needs two presses; a slow
// teardown gets the 2s to finish, and its alternate-screen leave to appear, so
// the loop stops — see teardownStarted — before it writes another press.
//
// The loop stops tapping once either teardown signal appears and only waits for
// the exit:
//
//   - the alternate screen came back (tuiLeft): the harness's own, guest-side
//     teardown signal, emitted while it restores its terminal. It is the only
//     signal visible before the guest exits, which is when qwen-code's slow
//     teardown gets tapped past.
//   - the test pty is back in cooked mode (ptySession.inRawMode): the launcher's
//     msb attach holds the pty raw for the session and restores it on the way
//     out, which covers a normal-buffer harness (claude-code) that emits no
//     alternate-screen sequence. It cannot stand in for the guest's restore,
//     though: the attach owns the host pty and only restores it after the guest
//     process has already exited, so it never observes the guest's earlier
//     restore.
func quit(ses *ptySession, spec harnessSpec) (int, error) {
	budget := scaleTimeout(quitTimeout, smokeTimeoutFactor())
	deadline := time.Now().Add(budget)
	interval := quitPressInterval
	for presses := 1; ; presses++ {
		if teardownStarted(ses, presses) {
			return waitExitWithin(ses, deadline, budget, presses, spec)
		}
		if err := ses.write(spec.QuitKey); err != nil {
			return 0, err
		}
		code, err := ses.waitExit(interval)
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
	ses *ptySession,
	deadline time.Time,
	budget time.Duration,
	presses int,
	spec harnessSpec,
) (int, error) {
	code, err := ses.waitExit(time.Until(deadline))
	if err == nil {
		return code, nil
	}
	return 0, quitTimeoutError(presses, spec, budget, err)
}

// quitTimeoutError reports that a harness did not exit within budget after
// presses taps of its key.
func quitTimeoutError(presses int, spec harnessSpec, budget time.Duration, err error) error {
	return fmt.Errorf("%d presses of %#x within %s did not exit it: %w",
		presses, spec.QuitKey, budget, err)
}

// waitReady blocks until the harness can be driven with its quit key. A harness
// with a readiness marker announces its UI as soon as that line is printed; one
// that takes over the alternate screen announces it with the screen; one that
// keeps the terminal's normal buffer and has neither marker is given the
// launcher's start-up time instead.
func waitReady(ses *ptySession, spec harnessSpec) error {
	factor := smokeTimeoutFactor()
	if spec.ReadyMarker != "" {
		return ses.waitForMarker(spec.ReadyMarker, scaleTimeout(spec.readyTimeout(), factor))
	}
	if spec.KeepsNormalBuffer {
		return ses.waitSettled(scaleTimeout(spec.readyTimeout(), factor))
	}
	return ses.waitForTUI(scaleTimeout(tuiReadyTimeout, factor))
}

// preflight skips the suite when the host cannot run it at all: an unsupported
// platform or Linux without KVM. It never fails, needs no root, and runs before
// anything is built, so a host that cannot run the suite pays nothing.
func preflight(t *testing.T) {
	t.Helper()

	switch runtime.GOOS {
	case "linux":
		if _, err := os.Stat(kvmDevice); err != nil {
			t.Skipf("harness smoke tests need %s: %v", kvmDevice, err)
		}
	case "darwin":
		// Apple Silicon virtualization needs no host device.
	default:
		t.Skipf("harness smoke tests need linux or darwin, not %s", runtime.GOOS)
	}
}

// preflightDocker skips the suite when no docker daemon is reachable. The probe
// runs under the same isolated environment the child gets, so it can never
// certify a daemon the child cannot reach — a docker context the child does not
// inherit, or a HOME the child does not share, would otherwise pass here and
// fail every harness. It never fails.
func preflightDocker(t *testing.T, root string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	defer cancel()

	info := exec.CommandContext(ctx, "docker", "info")
	info.Env = isolatedEnv(root, root, os.Environ())
	if out, err := info.CombinedOutput(); err != nil {
		t.Skipf("harness smoke tests need a reachable docker daemon: %v\n%s", err, out)
	}
}

// prepare creates the shared root and compiles the launcher into it once for the
// whole suite, and returns both. A freshly created root is registered for
// removal as soon as it exists, so a skip (the docker preflight) or a failed
// build cannot leave the root and its compiled binary behind; a reused root
// (smokeRootReuseEnv) is kept. Destroying the sandboxes inside the root stays
// separate: that needs the binary and a reachable docker daemon, so it runs only
// after the docker preflight.
func prepare(t *testing.T) (root, binary string) {
	t.Helper()

	prepareOnce.Do(createRootAndBuildBinary)
	// Release the reuse lock last: cleanups run LIFO, and the sandbox sweep
	// registered after prepare must run while the lock is still held.
	if smokeRootLockRelease != nil {
		releaseLock := smokeRootLockRelease
		t.Cleanup(func() { releaseLock() })
	}
	if smokeRoot != "" && !smokeRootReused {
		created := smokeRoot
		t.Cleanup(func() { removeTempRoot(t, created) })
	}
	if errSmokePrepare != nil {
		t.Fatalf("prepare harness smoke suite: %v", errSmokePrepare)
	}
	return smokeRoot, smokeBinary
}

// removeTempRoot deletes the suite's shared temp root. It runs in cleanup, so a
// failure is logged, never fatal.
func removeTempRoot(t *testing.T, root string) {
	t.Helper()

	if err := os.RemoveAll(root); err != nil {
		t.Logf("cleanup: remove temp root %s: %v", root, err)
	}
}

// createRootAndBuildBinary resolves the suite root and compiles the launcher
// into it.
func createRootAndBuildBinary() {
	root, reused, release, err := resolveSuiteRoot()
	if err != nil {
		errSmokePrepare = err
		return
	}
	smokeRoot = root
	smokeRootReused = reused
	smokeRootLockRelease = release
	smokeBinary = filepath.Join(root, "agents-sandbox")
	if err := os.MkdirAll(filepath.Join(root, "tmp"), 0o755); err != nil {
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

// prebuildImages builds every image in suiteProjectAgents once, so each
// subtest's budget only has to absorb its VM start rather than a cold image
// build. It is the first docker-dependent step, so it runs only after
// preflightDocker has confirmed the child can reach a daemon.
func prebuildImages(t *testing.T, root string) {
	t.Helper()

	prebuildOnce.Do(func() { errPrebuild = buildHarnessImages(t, root) })
	if errPrebuild != nil {
		t.Fatalf("prebuild harness images: %v", errPrebuild)
	}
}

// buildHarnessImages builds every image in suiteProjectAgents in the same
// throwaway project directory its subtest will run from, so the project slug
// and agent identity match. The builds run up to prebuildConcurrency at a time;
// the isolated home pays for the microsandbox runtime install here, outside any
// harness's TUI budget. The env carries the environment's GitHub token when one
// is set, so its release resolutions authenticate.
func buildHarnessImages(t *testing.T, root string) error {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), prepareTimeout)
	defer cancel()

	inherited := os.Environ()
	env := withGitHubToken(isolatedEnv(root, root, inherited), inherited)
	pairs := suiteProjectAgents()
	for _, pair := range pairs {
		if err := os.MkdirAll(projectDir(root, pair.Project), 0o755); err != nil {
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

			args := []string{"build", "--agent", pair.Agent}
			if pair.Docker {
				args = append(args, "--docker")
			}
			build := exec.CommandContext(ctx, smokeBinary, args...)
			build.Dir = projectDir(root, pair.Project)
			build.Env = env
			started := time.Now()
			out, err := build.CombinedOutput()
			// The prebuild is the suite's dominant one-off cost (a docker build
			// plus an `EnsureLoaded` transfer of a multi-GB runner image into
			// the microsandbox cache); the per-image duration separates build
			// from transfer when -v is in use.
			t.Logf(
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

// sweepSandboxes best-effort destroys any sandbox left in the isolated home. It
// runs at suite start — so a reused root never carries a previous run's
// stopped-but-persisted VM into this run's subtests — and again at teardown, so
// a failed harness cannot leak a VM pointing at a home the suite is about to
// delete. when names the phase in its log lines. The root's own removal is
// registered by prepare and runs after this. It logs failures and never fails
// the test.
func sweepSandboxes(t *testing.T, root, binary, when string) {
	t.Helper()

	list, cancelList := boundedLauncher(root, binary, "sandbox", "list", "-q")
	out, err := list.Output()
	cancelList()
	if err != nil {
		t.Logf("%s: list sandboxes: %v", when, err)
	}
	for _, name := range sandboxNames(string(out)) {
		// --yes is required: without it the non-interactive remove blocks on
		// its confirmation prompt and fails.
		remove, cancelRemove := boundedLauncher(root, binary, "sandbox", "remove", "--force", "--yes", name)
		removeOut, removeErr := remove.CombinedOutput()
		cancelRemove()
		if removeErr != nil {
			t.Logf("%s: remove sandbox %s: %v\n%s", when, name, removeErr, removeOut)
		}
	}
}

// boundedLauncher builds a launcher subcommand under the isolated environment
// with a bounded timeout, so a wedged socket cannot hang teardown. The caller
// must invoke the returned cancel once the command completes.
func boundedLauncher(root, binary string, args ...string) (*exec.Cmd, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Env = isolatedEnv(root, root, os.Environ())
	return cmd, cancel
}
