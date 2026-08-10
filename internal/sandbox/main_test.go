package sandbox

import (
	"testing"

	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox/docker"
	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox/msb"
	"gitlab.inoio.de/inoio/opencode-msb/internal/testutil"
)

// TestMain installs the fail-fast defaults for the sandbox factories so no
// sandbox test can silently touch real config paths, Docker, or microsandbox.
// Tests opt in via WithMockConfigPaths / docker.WithDockerMock / msb.WithMsbMock.
func TestMain(m *testing.M) {
	testutil.InitMocks(m,
		InstallFailFastConfigPaths,
		docker.InstallFailFastGet,
		msb.InstallFailFastGet,
	)
}
