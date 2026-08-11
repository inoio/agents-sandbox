package configpaths

import (
	"testing"

	"gitlab.inoio.de/inoio/opencode-msb/internal/testutil"
)

func TestMain(m *testing.M) {
	testutil.InitMocks(m, InstallFailFastConfigPaths)
}
