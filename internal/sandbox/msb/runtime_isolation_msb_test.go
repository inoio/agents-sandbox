//go:build msb

package msb

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/hostlock"
)

// isolatedRuntimeHome is the microsandbox home the msb-tagged tests install the
// SDK-pinned runtime into. It is set in TestMain, before any SDK call, so the
// SDK's own resolution and FFI cache land there.
var isolatedRuntimeHome string

// isolatedRuntimeReady records that a real-client test resolved the runtime, so
// the post-test sweep only touches the SDK when tests actually ran. compile-all
// executes TestMain under -run=^$, where the runtime install must stay lazy.
var isolatedRuntimeReady bool

// destroyTimeout bounds a test's best-effort sandbox teardown, so a wedged VM
// cannot hang the suite.
const destroyTimeout = 30 * time.Second

// testIdleTimeout is the out-of-process backstop for a sandbox a test creates:
// if the test binary is hard-killed (SIGKILL, an OOM, a CI timeout) neither its
// t.Cleanup Destroy nor the TestMain sweep runs, but the runtime still stops the
// idle VM instead of leaving it up forever. It exceeds TestJobsRoundTrip's
// three-minute context on purpose, so it can never fire during a legitimate run
// (the context cancels first); it stops the VM without removing the persisted
// sandbox, which is why Destroy and the sweep stay primary.
const testIdleTimeout = 5 * time.Minute

// reuseRuntimeHomeEnv, when set, points the msb-tagged tests at an existing
// microsandbox home they keep between runs instead of a fresh temp home. A kept
// home skips reinstalling the SDK-pinned runtime and reloading cached images.
// Local iteration only: unset (the default) creates and removes a fresh home.
const reuseRuntimeHomeEnv = "AGENTS_SANDBOX_MSB_TEST_HOME"

// reuseLockSuffix names the lock file that serializes runs sharing a reused
// microsandbox home. The lock sits beside the home, not inside it, so it never
// lands in the runtime home.
const reuseLockSuffix = ".lock"

// reuseLockPath is the lock file that serializes runs sharing the reused home
// dir.
func reuseLockPath(dir string) string {
	return filepath.Join(filepath.Dir(dir), filepath.Base(dir)+reuseLockSuffix)
}

// acquireReuseLock blocks until this run holds the exclusive lock for the reused
// home dir, waiting out any other run that shares it. Two runs that share a home
// would otherwise race on the sandbox sweep: whichever finishes first destroys
// every sandbox in the home, including the other's live VMs. It returns the
// release.
func acquireReuseLock(dir string) (func(), error) {
	return hostlock.AcquireExclusive(filepath.Dir(dir), filepath.Base(dir)+reuseLockSuffix)
}

// installIsolatedRuntimeHome points the process at a microsandbox home and
// returns a cleanup that removes it. It is cheap — the runtime installs lazily
// in ensureIsolatedRuntime, so compile-all's -run=^$ stays a no-op.
//
// A set reuseRuntimeHomeEnv reuses that directory and keeps it (the cleanup only
// releases the home's exclusive lock, held for the run so two runs sharing the
// home serialize their sandbox sweeps), so the runtime and image cache survive
// between runs. Otherwise a fresh temp home is created and removed. Both paths
// set MSB_HOME: the SDK resolves its FFI cache and all non-setup operations
// through MSB_HOME, so a reuse that only named the home in RuntimeConfig would
// leave sandbox creation pointed at the real ~/.microsandbox.
//
// Without an isolated home the SDK resolves its default home (~/.microsandbox),
// so a stale runtime left there by a pre-decoupling agents-sandbox install or a
// manual msb would fail sandbox creation with "no tested sandbox launch contract
// for runtime <v>" after an SDK bump (specs/volumes/runtime-home-decoupling.md).
// The launcher repairs its own private home, not this one, so the tests must
// provision their own.
func installIsolatedRuntimeHome() func() {
	if dir := os.Getenv(reuseRuntimeHomeEnv); dir != "" {
		reuseHome, err := filepath.Abs(dir)
		if err != nil {
			panic(fmt.Sprintf("resolve reused microsandbox home %s: %v", dir, err))
		}
		if err := os.MkdirAll(reuseHome, 0o755); err != nil {
			panic(fmt.Sprintf("create reused microsandbox home %s: %v", reuseHome, err))
		}
		release, err := acquireReuseLock(reuseHome)
		if err != nil {
			panic(fmt.Sprintf("lock reused microsandbox home %s: %v", reuseHome, err))
		}
		setMSBHome(reuseHome)
		isolatedRuntimeHome = reuseHome
		return release
	}
	home, err := os.MkdirTemp("", "agents-sandbox-msb-test-")
	if err != nil {
		panic(fmt.Sprintf("create isolated microsandbox home: %v", err))
	}
	setMSBHome(home)
	isolatedRuntimeHome = home
	return func() { _ = os.RemoveAll(home) }
}

// setMSBHome points the SDK at home. The SDK resolves its FFI cache and all
// non-setup operations through MSB_HOME; RuntimeConfig{Home: ...} alone only
// governs runtime setup.
func setMSBHome(home string) {
	if err := os.Setenv("MSB_HOME", home); err != nil {
		panic(fmt.Sprintf("set MSB_HOME: %v", err))
	}
}

// ensureIsolatedRuntime resolves the SDK-pinned runtime in the isolated home,
// installing it on first use, and fails the test if it cannot. It must run
// before the test's first real SDK call.
func ensureIsolatedRuntime(t *testing.T) {
	t.Helper()
	if _, err := msbSdk.EnsureRuntime(
		context.Background(),
		msbSdk.RuntimeConfig{Home: isolatedRuntimeHome},
		msbSdk.InstallOptions{},
	); err != nil {
		t.Fatalf("ensure isolated msb runtime in %s: %v", isolatedRuntimeHome, err)
	}
	isolatedRuntimeReady = true
}

// sweepIsolatedSandboxes best-effort destroys every sandbox left in the
// isolated runtime home after m.Run() returns. A test that reached its own
// cleanup leaves nothing; one that failed or panicked without reaching it would
// otherwise leak a detached VM whose runtime home the teardown is about to
// delete, unreachable from any later invocation. A hard kill (a -timeout
// expiry, SIGINT, SIGKILL) exits without returning from m.Run(), so the sweep
// cannot run and the sandbox's idle timeout is the backstop instead. The
// harness suite guards the same failure with its sandbox sweep
// (specs/testing/harness-smoke-tests.md); this is the msb package's equivalent.
//
// Destroy stops and removes, because Remove requires an already-stopped
// sandbox. A failure is returned rather than swallowed, naming the home and
// each sandbox it could not destroy, so the caller can report it and keep the
// home reachable for manual cleanup. The sweep is a no-op when no test
// installed the runtime, keeping compile-all's -run=^$ inert.
func sweepIsolatedSandboxes() error {
	if !isolatedRuntimeReady {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), destroyTimeout)
	defer cancel()
	handles, err := (&realMsbClient{}).ListSandboxes(ctx, nil)
	if err != nil {
		return fmt.Errorf("list sandboxes in isolated runtime home %s: %w", isolatedRuntimeHome, err)
	}
	var destroyErrors []error
	for _, handle := range handles {
		if err := handle.Destroy(ctx, LifecycleOptions{}); err != nil {
			destroyErrors = append(destroyErrors, fmt.Errorf(
				"destroy sandbox %s in isolated runtime home %s: %w",
				handle.Name(), isolatedRuntimeHome, err,
			))
		}
	}
	return errors.Join(destroyErrors...)
}

// TestInstallIsolatedRuntimeHomeSetsMSBHomeOnReuse pins that the reuse path
// points the SDK at the reused home. Setting only RuntimeConfig{Home: ...}
// would leave MSB_HOME at the real ~/.microsandbox, and sandbox creation — which
// resolves its storage root through MSB_HOME — would fail the "catalog ancestor
// permits path substitution" check on a reused home.
func TestInstallIsolatedRuntimeHomeSetsMSBHomeOnReuse(t *testing.T) {
	reuseDir := t.TempDir()
	t.Setenv(reuseRuntimeHomeEnv, reuseDir)
	// Register MSB_HOME for restoration: it starts at the home TestMain
	// installed, and installIsolatedRuntimeHome overwrites it through setMSBHome.
	t.Setenv("MSB_HOME", os.Getenv("MSB_HOME"))

	previousHome := isolatedRuntimeHome
	t.Cleanup(func() { isolatedRuntimeHome = previousHome })
	t.Cleanup(installIsolatedRuntimeHome())

	if got := os.Getenv("MSB_HOME"); got != reuseDir {
		t.Errorf("MSB_HOME = %q, want the reused home %q", got, reuseDir)
	}
	if isolatedRuntimeHome != reuseDir {
		t.Errorf("isolatedRuntimeHome = %q, want %q", isolatedRuntimeHome, reuseDir)
	}
}

// TestInstallIsolatedRuntimeHomeLocksReuseHome pins that a reused home is held
// under an exclusive lock for the run: two runs sharing a home serialize there
// instead of one run's sandbox sweep destroying the other's live VMs.
func TestInstallIsolatedRuntimeHomeLocksReuseHome(t *testing.T) {
	reuseDir := t.TempDir()
	t.Setenv(reuseRuntimeHomeEnv, reuseDir)
	t.Setenv("MSB_HOME", os.Getenv("MSB_HOME"))
	previousHome := isolatedRuntimeHome
	t.Cleanup(func() { isolatedRuntimeHome = previousHome })

	release := installIsolatedRuntimeHome()
	if !lockContended(t, reuseLockPath(reuseDir)) {
		t.Error("the reuse lock is free while the run holds it, want it held")
	}
	release()
	if lockContended(t, reuseLockPath(reuseDir)) {
		t.Error("the reuse lock is still held after release")
	}
}

// lockContended reports whether another open file description holds the lock at
// path.
func lockContended(t *testing.T, path string) bool {
	t.Helper()
	f, err := os.OpenFile(path, os.O_RDWR, 0o600)
	if err != nil {
		t.Fatalf("open lock %s: %v", path, err)
	}
	defer f.Close()
	err = hostlock.ExclusiveNonBlocking(f)
	if err == nil {
		return false
	}
	if !hostlock.IsContention(err) {
		t.Fatalf("flock %s: %v", path, err)
	}
	return true
}
