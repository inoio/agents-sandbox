//go:build msb

package msb

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"
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

// installIsolatedRuntimeHome points the process at a fresh microsandbox home and
// returns a cleanup that removes it. It is cheap — the runtime installs lazily
// in ensureIsolatedRuntime, so compile-all's -run=^$ stays a no-op.
//
// Without this the SDK resolves its default home (~/.microsandbox), so a stale
// runtime left there by a pre-decoupling agents-sandbox install or a manual msb
// would fail sandbox creation with "no tested sandbox launch contract for
// runtime <v>" after an SDK bump (runtime-home-decoupling.md). The launcher
// repairs its own private home, not this one, so the tests must provision their
// own.
func installIsolatedRuntimeHome() func() {
	home, err := os.MkdirTemp("", "agents-sandbox-msb-test-")
	if err != nil {
		panic(fmt.Sprintf("create isolated microsandbox home: %v", err))
	}
	if err := os.Setenv("MSB_HOME", home); err != nil {
		panic(fmt.Sprintf("set MSB_HOME: %v", err))
	}
	isolatedRuntimeHome = home
	return func() { _ = os.RemoveAll(home) }
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
// isolated runtime home after the tests. A test that reached its own cleanup
// leaves nothing; one interrupted before it (a timeout, a panic, a SIGINT at
// the wrong moment) would otherwise leak a detached VM whose runtime home the
// teardown is about to delete, unreachable from any later invocation. The
// harness suite guards the same failure with its sandbox sweep
// (specs/harness-smoke-tests.md); this is the msb package's equivalent.
//
// Destroy stops and removes, because Remove requires an already-stopped
// sandbox. The sweep is a no-op when no test installed the runtime, keeping
// compile-all's -run=^$ inert.
func sweepIsolatedSandboxes() {
	if !isolatedRuntimeReady {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), destroyTimeout)
	defer cancel()
	handles, err := (&realMsbClient{}).ListSandboxes(ctx, nil)
	if err != nil {
		return
	}
	for _, handle := range handles {
		_ = handle.Destroy(ctx)
	}
}
