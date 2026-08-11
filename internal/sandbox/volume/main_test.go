package volume

import (
	"testing"

	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox/msb"
)

func TestMain(m *testing.M) {
	msb.InstallFailFastGet()
	m.Run()
}
