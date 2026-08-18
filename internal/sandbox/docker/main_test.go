package docker

import (
	"testing"

	"github.com/inoio/opencode-sandbox/internal/configpaths"
	"github.com/inoio/opencode-sandbox/internal/sandbox/msb"
)

func TestMain(m *testing.M) {
	// can't call testutil.InitFailFastMocks(m) because of cyclic dependency. Keep in sync with
	// testutil.InitFailFastMocks' body
	configpaths.InstallFailFastConfigPaths()
	InstallFailFastGet()
	// cyclic dependency on Ping
	// doctor.InstallFailFast()
	msb.InstallFailFastGet()
	m.Run()
}
