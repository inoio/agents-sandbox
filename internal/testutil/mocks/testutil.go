package termio

import (
	"testing"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/doctor"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
)

// InstallFailFastMocks installs all fail-fast mocks for tests, so tests that
// forget to define a mock fail fast instead of using real implementations.
func InstallFailFastMocks() {
	configpaths.InstallFailFastConfigPaths()
	doctor.InstallFailFast()
	msb.InstallFailFastGet()
}

// InitFailFastMocks installs the fail-fast mocks and runs the test suite.
func InitFailFastMocks(m *testing.M) {
	InstallFailFastMocks()
	m.Run()
}
