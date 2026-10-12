package image

import (
	"os"
	"testing"

	termio "github.com/inoio/agents-sandbox/internal/testutil/mocks"
)

// integrationHarnessSetup lets the integration-tagged tests install a real
// builder harness before the suite runs and tear it down afterwards. The
// non-integration build keeps a no-op default so TestMain compiles and runs
// without the build tag.
var integrationHarnessSetup = func() func() { return func() {} }

func TestMain(m *testing.M) {
	termio.InstallFailFastMocks()
	teardown := integrationHarnessSetup()
	code := m.Run()
	teardown()
	os.Exit(code)
}
