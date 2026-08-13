package volume

import (
	"testing"

	"gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/msb"
)

func TestMain(m *testing.M) {
	msb.InstallFailFastGet()
	m.Run()
}
