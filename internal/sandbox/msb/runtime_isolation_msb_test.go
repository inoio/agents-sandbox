//go:build msb

package msb

import (
	"context"
	"fmt"
	"os"
	"testing"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"
)

// isolatedRuntimeHome is the microsandbox home the msb-tagged tests install the
// SDK-pinned runtime into. It is set in TestMain, before any SDK call, so the
// SDK's own resolution and FFI cache land there.
var isolatedRuntimeHome string

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
}
