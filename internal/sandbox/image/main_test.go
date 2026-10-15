package image

import (
	"os"
	"testing"

	"github.com/inoio/agents-sandbox/internal/sandbox/builder/cacheregistry"
	termio "github.com/inoio/agents-sandbox/internal/testutil/mocks"
)

// integrationTestCleanup, when set by an integration-tagged file, removes
// process-shared resources (e.g. the run-wide registry cache directory) after
// the tests finish. It is a variable so the default build carries no dependency
// on integration-only files.
var integrationTestCleanup func()

func TestMain(m *testing.M) {
	cacheregistry.RunIfDaemonInvocation()
	termio.InstallFailFastMocks()
	if os.Getenv(integrationEnv) == "" {
		InstallOfflineImageDelivery()
	}
	code := m.Run()
	if integrationTestCleanup != nil {
		integrationTestCleanup()
	}
	os.Exit(code)
}

// integrationEnv gates the tagged integration tier that drives the real builder
// and registry. It duplicates integration_build_test.go's constant because that
// file is build-tagged and unavailable to this default-build file.
const integrationEnv = "AGENTS_SANDBOX_INTEGRATION"
