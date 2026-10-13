package termio

import (
	"testing"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
)

// InstallFailFastMocks installs the fail-fast config-path and msb mocks so tests
// that forget to define a mock fail fast instead of using real implementations.
// The doctor mock is not installed here: only the CLI drives doctor, and importing
// it would pull the builder package (and its dependencies) into every test binary
// that uses this helper, creating import cycles. The CLI installs it directly.
func InstallFailFastMocks() {
	configpaths.InstallFailFastConfigPaths()
	msb.InstallFailFastGet()
}

// InitFailFastMocks installs the fail-fast mocks and runs the test suite.
func InitFailFastMocks(m *testing.M) {
	InstallFailFastMocks()
	m.Run()
}
