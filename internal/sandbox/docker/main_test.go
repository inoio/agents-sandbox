package docker

import (
	"testing"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/notify"
	"github.com/inoio/agents-sandbox/internal/opener"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
)

func TestMain(m *testing.M) {
	// can't call testutil.InitFailFastMocks(m) because of cyclic dependency. Keep in sync with
	// testutil.InitFailFastMocks' body
	configpaths.InstallFailFastConfigPaths()
	InstallFailFastGet()
	// cyclic dependency on Ping
	// doctor.InstallFailFast()
	msb.InstallFailFastGet()
	notify.InstallFailFast()
	opener.InstallFailFast()
	m.Run()
}
