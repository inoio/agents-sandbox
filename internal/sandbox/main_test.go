package sandbox

import (
	"testing"

	"gitlab.inoio.de/inoio/opencode-msb/internal/configpaths"
	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox/docker"
	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox/msb"
	"gitlab.inoio.de/inoio/opencode-msb/internal/testutil"
)

// TestMain installs the fail-fast defaults for the sandbox factories so no
// sandbox test can silently touch real config paths, Docker, or microsandbox.
// Tests opt in via configpaths.WithMockConfigPaths / docker.WithDockerMock / msb.WithMsbMock.
func TestMain(m *testing.M) {
	testutil.InitMocks(m,
		configpaths.InstallFailFastConfigPaths,
		docker.InstallFailFastGet,
		msb.InstallFailFastGet,
	)
}
