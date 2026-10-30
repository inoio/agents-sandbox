package notify

import (
	"testing"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/opener"
	"github.com/inoio/agents-sandbox/internal/sandbox/docker"
	"github.com/inoio/agents-sandbox/internal/sandbox/doctor"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
)

func TestMain(m *testing.M) {
	// can't call testutil.InitFailFastMocks(m) because of cyclic dependency: it installs notify, which is this
	// package. Keep in sync with testutil.InitFailFastMocks' body.
	configpaths.InstallFailFastConfigPaths()
	docker.InstallFailFastGet()
	doctor.InstallFailFast()
	msb.InstallFailFastGet()
	InstallFailFast()
	opener.InstallFailFast()
	m.Run()
}
