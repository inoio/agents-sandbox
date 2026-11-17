package readback

import (
	"fmt"
	"os"
	"testing"

	"github.com/inoio/agents-sandbox/internal/sandbox/msb/msbtest/runtime"
	mocks "github.com/inoio/agents-sandbox/internal/testutil/mocks"
)

func TestMain(m *testing.M) {
	// Point the process at an isolated microsandbox home before any SDK call, so
	// the msb-tagged read-back test never touches the developer's real msb
	// state. A set AGENTS_SANDBOX_MSB_TEST_HOME reuses and keeps that home (its
	// image cache survives between runs); otherwise a fresh temp home is created
	// and removed. Both paths are no-ops outside the msb build.
	removeIsolatedRuntimeHome := runtime.InstallIsolatedRuntimeHome()
	mocks.InitFailFastMocks(m)
	// A test that failed or panicked without reaching its own cleanup may have
	// left a detached VM in the home the teardown is about to delete; sweep it
	// first. On a failed sweep keep the home so the surviving VM stays reachable
	// for manual cleanup.
	if sweepErr := runtime.SweepIsolatedSandboxes(); sweepErr != nil {
		fmt.Fprintf(
			os.Stderr,
			"readback msb tests: sandbox sweep failed, keeping the isolated runtime home: %v\n",
			sweepErr,
		)
	} else {
		removeIsolatedRuntimeHome()
	}
}
