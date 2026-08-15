package termio

import (
	"testing"

	"gitlab.inoio.de/inoio/opencode-sandbox/internal/configpaths"
	"gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/docker"
	"gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/msb"
)

// InitFailFastMocks initializes all fail-fast mocks for tests, so tests that forget to define a mock fail fast instead
// of using real implementations.
func InitFailFastMocks(m *testing.M) {
	configpaths.InstallFailFastConfigPaths()
	docker.InstallFailFastGet()
	// doctor.InstallFailFast()
	msb.InstallFailFastGet()
	m.Run()
}
