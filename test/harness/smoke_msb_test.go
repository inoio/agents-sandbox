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
	"strings"
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
	// quitPressInterval and quitTimeout pace the quit: the harness's key is
	// tapped every quitPressInterval until the process exits, for at most
	// quitTimeout. The interval is a human double-tap — a harness whose exit is
	// a repeated key leaves on the next tap, and one that paints its TUI before
	// it accepts input simply ignores the taps made meanwhile.
	quitPressInterval = 400 * time.Millisecond
	quitTimeout       = 15 * time.Second
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
	// Register cleanup after the docker preflight so a skip touches nothing, but
	// before the prebuild so a prebuild failure cannot leak the root. t.Cleanup,
	// not defer: it must run after the parallel subtests, which resume only once
	// this function returns.
	t.Cleanup(func() { cleanupSandboxes(t, root, binary) })
	prebuildImages(t, root)
	prebuildDockerStorageImage(t, root)

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
			ses := mustStartUnderPTY(
				t, projectDir(root, name),
				isolatedEnv(root, homeBase, os.Environ()),
				append([]string{binary}, harnessRunArgs(name, network)...)...,
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

// quit taps the harness's key until the process exits and returns its exit
// code. Tapping on is what a user does, and it is what the harnesses need: the
// press that lands can be ignored because the TUI painted its screen before it
// accepted input (opencode draws its footer while it is still loading), and a
// harness whose exit is a repeated key — claude-code announces "Press Ctrl-C
// again to exit" — needs the taps to keep coming.
func quit(ses *ptySession, spec harnessSpec) (int, error) {
	deadline := time.Now().Add(quitTimeout)
	for presses := 1; ; presses++ {
		if err := ses.write(spec.QuitKey); err != nil {
			return 0, err
		}
		code, err := ses.waitExit(quitPressInterval)
		if err == nil {
			return code, nil
		}
		if !time.Now().Before(deadline) {
			return 0, fmt.Errorf("%d presses of %#x within %s did not exit it: %w",
				presses, spec.QuitKey, quitTimeout, err)
		}
	}
}

// waitReady blocks until the harness can be driven with its quit key. A harness
// with a readiness marker announces its UI as soon as that line is printed; one
// that takes over the alternate screen announces it with the screen; one that
// keeps the terminal's normal buffer and has neither marker is given the
// launcher's start-up time instead.
func waitReady(ses *ptySession, spec harnessSpec) error {
	if spec.ReadyMarker != "" {
		return ses.waitForMarker(spec.ReadyMarker, spec.readyTimeout())
	}
	if spec.KeepsNormalBuffer {
		return ses.waitSettled(spec.readyTimeout())
	}
	return ses.waitForTUI(tuiReadyTimeout)
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

// projectDir is the throwaway project directory a harness runs in. It fixes the
// project slug the runner image is keyed by, so the prebuild's image matches
// the one the subtest's `run` loads.
func projectDir(root, name string) string {
	return filepath.Join(root, "projects", name)
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

// prebuildImages builds every registered harness's runner image once, so each
// harness's TUI budget only has to absorb its VM start rather than a cold image
// build. It is the first docker-dependent step, so it runs only after
// preflightDocker has confirmed the child can reach a daemon.
func prebuildImages(t *testing.T, root string) {
	t.Helper()

	prebuildOnce.Do(func() { errPrebuild = buildHarnessImages(t, root) })
	if errPrebuild != nil {
		t.Fatalf("prebuild harness images: %v", errPrebuild)
	}
}

// buildHarnessImages builds each registered harness's runner image in the same
// throwaway project directory its subtest will run from, so the project slug
// and agent identity match. The builds run up to prebuildConcurrency at a time;
// the isolated home pays for the microsandbox runtime install here, outside any
// harness's TUI budget.
func buildHarnessImages(t *testing.T, root string) error {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), prepareTimeout)
	defer cancel()

	env := isolatedEnv(root, root, os.Environ())
	names := agent.Names()
	for _, name := range names {
		if err := os.MkdirAll(projectDir(root, name), 0o755); err != nil {
			return fmt.Errorf("create project dir for %s: %w", name, err)
		}
	}

	buildErrors := make([]error, len(names))
	semaphore := make(chan struct{}, prebuildConcurrency)
	var builds sync.WaitGroup
	for index, name := range names {
		builds.Go(func() {
			semaphore <- struct{}{}
			defer func() { <-semaphore }()

			build := exec.CommandContext(ctx, smokeBinary, "build", "--agent", name)
			build.Dir = projectDir(root, name)
			build.Env = env
			started := time.Now()
			out, err := build.CombinedOutput()
			// The prebuild is the suite's dominant one-off cost (a docker build
			// plus an `EnsureLoaded` transfer of a multi-GB runner image into
			// the microsandbox cache); the per-image duration separates build
			// from transfer when -v is in use.
			t.Logf("prebuilt %s image in %s", name, time.Since(started).Round(time.Millisecond))
			if err != nil {
				buildErrors[index] = fmt.Errorf("prebuild image for %s: %w\n%s", name, err, out)
			}
		})
	}
	builds.Wait()
	return errors.Join(buildErrors...)
}

// cleanupSandboxes best-effort destroys any sandbox left in the isolated home,
// so a failed harness cannot leak a VM pointing at a home the suite is about to
// delete. The root's own removal is registered by prepare and runs after this.
// It logs failures and never fails the test.
func cleanupSandboxes(t *testing.T, root, binary string) {
	t.Helper()

	list, cancelList := boundedLauncher(root, binary, "sandbox", "list", "-q")
	out, err := list.Output()
	cancelList()
	if err != nil {
		t.Logf("cleanup: list sandboxes: %v", err)
	}
	for line := range strings.SplitSeq(string(out), "\n") {
		name := strings.TrimSpace(line)
		if name == "" {
			continue
		}
		// --yes is required: without it the non-interactive remove blocks on
		// its confirmation prompt and fails.
		remove, cancelRemove := boundedLauncher(root, binary, "sandbox", "remove", "--force", "--yes", name)
		removeOut, removeErr := remove.CombinedOutput()
		cancelRemove()
		if removeErr != nil {
			t.Logf("cleanup: remove sandbox %s: %v\n%s", name, removeErr, removeOut)
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
