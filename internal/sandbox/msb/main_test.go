package msb_test

import (
	"fmt"
	"os"
	"testing"

	"github.com/inoio/agents-sandbox/internal/configpaths/configpathstest"
	"github.com/inoio/agents-sandbox/internal/notify/notifytest"
	"github.com/inoio/agents-sandbox/internal/opener/openertest"
	"github.com/inoio/agents-sandbox/internal/sandbox/docker/dockertest"
	"github.com/inoio/agents-sandbox/internal/sandbox/doctor/doctortest"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb/msbtest"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb/msbtest/runtime"
)

func TestMain(m *testing.M) {
	// Can't call testutil.InitFailFastMocks(m): it calls m.Run() itself, and the
	// msb suite wraps the run in an isolated microsandbox home. Keep in sync with
	// testutil.InitFailFastMocks' body.
	configpathstest.InstallFailFastConfigPaths()
	dockertest.InstallFailFastGet()
	doctortest.InstallFailFast()
	msbtest.InstallFailFastGet()
	notifytest.InstallFailFast()
	openertest.InstallFailFast()
	// Point the msb-tagged tests at an isolated microsandbox home; a no-op in
	// the untagged build (see the leaf's runtime.go).
	removeIsolatedRuntimeHome := runtime.InstallIsolatedRuntimeHome()
	m.Run()
	// A test that failed or panicked without reaching its own cleanup would
	// otherwise leave a detached VM in the home removeIsolatedRuntimeHome
	// deletes; sweep it first (no-op untagged and when nothing ran). A hard kill
	// — a -timeout expiry, SIGINT, or SIGKILL — exits without returning from
	// m.Run(), so the sweep cannot run and the sandbox's idle timeout is the
	// backstop instead.
	if sweepErr := runtime.SweepIsolatedSandboxes(); sweepErr != nil {
		// Keep the home so the surviving sandbox stays reachable for manual
		// cleanup; the error names the home and each sandbox it could not
		// destroy.
		fmt.Fprintf(os.Stderr, "msb tests: sandbox sweep failed, keeping the isolated runtime home: %v\n", sweepErr)
	} else {
		removeIsolatedRuntimeHome()
	}
}
