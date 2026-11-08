//go:build msb

//deadcode:ignore reason="test support: an isolated microsandbox home and sandbox sweep for the msb-tagged tests"
package runtime

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

// DestroyTimeout bounds a test's best-effort sandbox teardown, so a wedged VM
// cannot hang the suite.
const DestroyTimeout = 30 * time.Second

// TestIdleTimeout is the out-of-process backstop for a sandbox a test creates:
// if the test binary is hard-killed (SIGKILL, an OOM, a CI timeout) neither its
// t.Cleanup Destroy nor the TestMain sweep runs, but the runtime still stops the
// idle VM instead of leaving it up forever. It exceeds TestJobsRoundTrip's
// three-minute context on purpose, so it can never fire during a legitimate run
// (the context cancels first); it stops the VM without removing the persisted
// sandbox, which is why Destroy and the sweep stay primary.
const TestIdleTimeout = 5 * time.Minute

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

// InstallIsolatedRuntimeHome points the process at a microsandbox home and
// returns a cleanup that removes it. It is cheap — the runtime installs lazily
// in EnsureIsolatedRuntime, so compile-all's -run=^$ stays a no-op.
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
func InstallIsolatedRuntimeHome() func() {
	if dir := os.Getenv(reuseRuntimeHomeEnv); dir != "" {
		reuseHome, err := filepath.Abs(dir)
		if err != nil {
			panic(fmt.Sprintf("resolve reused microsandbox home %s: %v", dir, err))
		}
		//nolint:gosec // G703: the reusable home path is a local-iteration env var the developer sets
		if mkdirErr := os.MkdirAll(reuseHome, 0o750); mkdirErr != nil {
			panic(fmt.Sprintf("create reused microsandbox home %s: %v", reuseHome, mkdirErr))
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

// IsolatedRuntimeHome is the home InstallIsolatedRuntimeHome installed, so a
// caller can hand it to a child process.
func IsolatedRuntimeHome() string { return isolatedRuntimeHome }

// EnsureIsolatedRuntime resolves the SDK-pinned runtime in the isolated home,
// installing it on first use, and fails the test if it cannot. It must run
// before the test's first real SDK call.
func EnsureIsolatedRuntime(t *testing.T) {
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

// SweepIsolatedSandboxes best-effort destroys every sandbox left in the
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
func SweepIsolatedSandboxes() error {
	if !isolatedRuntimeReady {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), DestroyTimeout)
	defer cancel()
	var destroyErrors []error
	var cursor *string
	for {
		var opts []msbSdk.SandboxListOption
		if cursor != nil {
			opts = append(opts, msbSdk.WithListCursor(*cursor))
		}
		page, err := msbSdk.ListSandboxesWith(ctx, opts...)
		if err != nil {
			return fmt.Errorf("list sandboxes in isolated runtime home %s: %w", isolatedRuntimeHome, err)
		}
		for _, handle := range page.Sandboxes {
			if err := handle.Destroy(ctx); err != nil {
				destroyErrors = append(destroyErrors, fmt.Errorf(
					"destroy sandbox %s in isolated runtime home %s: %w",
					handle.Name(), isolatedRuntimeHome, err,
				))
			}
		}
		cursor = page.NextCursor
		if cursor == nil {
			break
		}
	}
	return errors.Join(destroyErrors...)
}
