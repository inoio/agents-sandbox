package session

import (
	"testing"

	"gitlab.inoio.de/inoio/opencode-msb/internal/configpaths"
	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox/msb"
	"gitlab.inoio.de/inoio/opencode-msb/internal/testutil"
)

func TestMain(m *testing.M) {
	testutil.InitMocks(m, msb.InstallFailFastGet, configpaths.InstallFailFastConfigPaths)
}
