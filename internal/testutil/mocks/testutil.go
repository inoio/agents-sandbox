package termio

import (
	"testing"

	"github.com/inoio/opencode-sandbox/internal/configpaths"
	"github.com/inoio/opencode-sandbox/internal/sandbox/docker"
	"github.com/inoio/opencode-sandbox/internal/sandbox/doctor"
	"github.com/inoio/opencode-sandbox/internal/sandbox/msb"
)

// InitFailFastMocks initializes all fail-fast mocks for tests, so tests that forget to define a mock fail fast instead
// of using real implementations.
func InitFailFastMocks(m *testing.M) {
	configpaths.InstallFailFastConfigPaths()
	docker.InstallFailFastGet()
	doctor.InstallFailFast()
	msb.InstallFailFastGet()
	m.Run()
}
