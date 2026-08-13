package doctor

import (
	"testing"

	"gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/docker"
	"gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/msb"
	"gitlab.inoio.de/inoio/opencode-sandbox/internal/testutil"
)

// TestMain installs the fail-fast defaults for the doctor factories so no
// doctor test can silently touch real msb or Docker.
func TestMain(m *testing.M) {
	testutil.InitMocks(m, msb.InstallFailFastGet, docker.InstallFailFastGet)
}
