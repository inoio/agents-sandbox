package msb

import (
	"fmt"
	"os"
	"testing"

	"github.com/inoio/agents-sandbox/internal/configpaths/configpathstest"
	"github.com/inoio/agents-sandbox/internal/opener/openertest"
	"github.com/inoio/agents-sandbox/internal/sandbox/docker/dockertest"
)

// realGetFactory captures the package's default Client factory before TestMain
// installs the fail-fast mock, so tests can exercise the real closure body.
var realGetFactory = Get

func TestMain(m *testing.M) {
	// can't call testutil.InitFailFastMocks(m) because of cyclic dependency. Keep in sync with
	// testutil.InitFailFastMocks' body
	configpathstest.InstallFailFastConfigPaths()
	dockertest.InstallFailFastGet()
	// cyclic dependency on msb
	// doctor.InstallFailFast()
	InstallFailFastGet()
	openertest.InstallFailFast()
	// Point the msb-tagged tests at an isolated microsandbox home; a no-op in
	// the untagged build (see runtime_isolation_msb_test.go).
	removeIsolatedRuntimeHome := installIsolatedRuntimeHome()
	m.Run()
	// A test that failed or panicked without reaching its own cleanup would
	// otherwise leave a detached VM in the home removeIsolatedRuntimeHome
	// deletes; sweep it first (no-op untagged and when nothing ran). A hard kill
	// — a -timeout expiry, SIGINT, or SIGKILL — exits without returning from
	// m.Run(), so the sweep cannot run and the sandbox's idle timeout is the
	// backstop instead.
	if sweepErr := sweepIsolatedSandboxes(); sweepErr != nil {
		// Keep the home so the surviving sandbox stays reachable for manual
		// cleanup; the error names the home and each sandbox it could not
		// destroy.
		fmt.Fprintf(os.Stderr, "msb tests: sandbox sweep failed, keeping the isolated runtime home: %v\n", sweepErr)
	} else {
		removeIsolatedRuntimeHome()
	}
}
