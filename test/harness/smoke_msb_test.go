//go:build msb

package harness

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/inoio/agents-sandbox/internal/agent"
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
	// settledReadyTimeout bounds the readiness wait for a harness whose TUI
	// keeps the terminal's normal buffer, where no marker announces that its UI
	// is up. It covers the launcher's image load and VM start — about 14s on a
	// cold host when the image prebuild has already run — with margin. A harness
	// whose runner image loads far slower overrides it (see harnessSpec.ReadyTimeout).
	settledReadyTimeout = 25 * time.Second
	// prepareTimeout bounds the one-off launcher build and the per-harness
	// runner-image prebuild every subtest shares.
	prepareTimeout = 30 * time.Minute
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
	smokeBinary     string
	errSmokePrepare error
	errPrebuild     error
)

// TestHarnessSmoke starts every registered harness the way a user does
// (`agents-sandbox run --agent <name>`) inside a real microsandbox VM and
// asserts it reaches its full-screen TUI, exits 0 on its quit key, and never
// reports a daemon timeout. It requires KVM, a docker daemon, and network; the
// `msb` build tag keeps it out of the default test run.
//
// The checks run in order so an environment that cannot run the suite skips
// before paying for it: the platform/`/dev/kvm` preflight first (no build), then
// the shared root and binary, then the isolated-env docker preflight, then the
// image prebuild (the first docker-dependent step), then the harnesses.
func TestHarnessSmoke(t *testing.T) {
	preflight(t)
	root, binary := prepare(t)
	preflightDocker(t, root)
	// Register cleanup after the docker preflight so a skip touches nothing,
	// but before the prebuild so a prebuild failure cannot leak the root.
	defer cleanupSandboxes(t, root, binary)
	prebuildImages(t, root)

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
			ses := mustStartUnderPTY(
				t, projectDir(root, name),
				isolatedEnv(root, os.Environ()),
				binary, "run", "--agent", name, "--network", network,
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

// waitReady blocks until the harness can be driven with its quit key. A
// harness that takes over the alternate screen announces its UI is up; one that
// keeps the terminal's normal buffer has no such marker, so it is given the
// launcher's start-up time instead.
func waitReady(ses *ptySession, spec harnessSpec) error {
	if spec.KeepsNormalBuffer {
		return ses.waitSettled(spec.readyTimeout(settledReadyTimeout))
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
	info.Env = isolatedEnv(root, os.Environ())
	if out, err := info.CombinedOutput(); err != nil {
		t.Skipf("harness smoke tests need a reachable docker daemon: %v\n%s", err, out)
	}
}

// prepare creates the shared temp root and compiles the launcher into it once
// for the whole suite, and returns both. The root is registered for removal as
// soon as it exists, so a skip (the docker preflight) or a failed build cannot
// leave the root and its compiled binary behind. Destroying the sandboxes
// inside the root stays separate: that needs the binary and a reachable docker
// daemon, so it runs only after the docker preflight.
func prepare(t *testing.T) (root, binary string) {
	t.Helper()

	prepareOnce.Do(createRootAndBuildBinary)
	if smokeRoot != "" {
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

// createRootAndBuildBinary creates the shared root and compiles the launcher
// into it.
func createRootAndBuildBinary() {
	root, err := os.MkdirTemp(smokeRootBase, smokeRootPrefix)
	if err != nil {
		errSmokePrepare = fmt.Errorf("create temp root: %w", err)
		return
	}
	smokeRoot = root
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

	prebuildOnce.Do(func() { errPrebuild = buildHarnessImages(root) })
	if errPrebuild != nil {
		t.Fatalf("prebuild harness images: %v", errPrebuild)
	}
}

// buildHarnessImages builds each registered harness's runner image in the same
// throwaway project directory its subtest will run from, so the project slug
// and agent identity match. The isolated home pays for the microsandbox runtime
// install here, outside any harness's TUI budget.
func buildHarnessImages(root string) error {
	ctx, cancel := context.WithTimeout(context.Background(), prepareTimeout)
	defer cancel()

	env := isolatedEnv(root, os.Environ())
	for _, name := range agent.Names() {
		dir := projectDir(root, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create project dir for %s: %w", name, err)
		}
		build := exec.CommandContext(ctx, smokeBinary, "build", "--agent", name)
		build.Dir = dir
		build.Env = env
		if out, err := build.CombinedOutput(); err != nil {
			return fmt.Errorf("prebuild image for %s: %w\n%s", name, err, out)
		}
	}
	return nil
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
	cmd.Env = isolatedEnv(root, os.Environ())
	return cmd, cancel
}
