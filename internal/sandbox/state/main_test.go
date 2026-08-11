package state

import (
	"testing"

	"gitlab.inoio.de/inoio/opencode-msb/internal/configpaths"
	"gitlab.inoio.de/inoio/opencode-msb/internal/testutil"
)

func TestMain(m *testing.M) {
	testutil.InitMocks(m, configpaths.InstallFailFastConfigPaths)
}
