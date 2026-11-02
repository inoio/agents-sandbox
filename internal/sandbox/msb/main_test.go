package msb

import (
	"testing"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/opener"
	"github.com/inoio/agents-sandbox/internal/sandbox/docker"
)

// realGetFactory captures the package's default Client factory before TestMain
// installs the fail-fast mock, so tests can exercise the real closure body.
var realGetFactory = Get

func TestMain(m *testing.M) {
	// can't call testutil.InitFailFastMocks(m) because of cyclic dependency. Keep in sync with
	// testutil.InitFailFastMocks' body
	configpaths.InstallFailFastConfigPaths()
	docker.InstallFailFastGet()
	// cyclic dependency on msb
	// doctor.InstallFailFast()
	InstallFailFastGet()
	opener.InstallFailFast()
	// Point the msb-tagged tests at an isolated microsandbox home; a no-op in
	// the untagged build (see runtime_isolation_msb_test.go).
	removeIsolatedRuntimeHome := installIsolatedRuntimeHome()
	m.Run()
	// A test that did not reach its own cleanup (interrupted, timed out,
	// panicked) would otherwise leave a detached VM in a home the next line
	// deletes; sweep it first (no-op untagged and when nothing ran).
	sweepIsolatedSandboxes()
	removeIsolatedRuntimeHome()
}
