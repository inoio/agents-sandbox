package network

import (
	"testing"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/opener"
	"github.com/inoio/agents-sandbox/internal/sandbox/docker"
	"github.com/inoio/agents-sandbox/internal/sandbox/doctor"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
)

func TestMain(m *testing.M) {
	// can't call testutil.InitFailFastMocks(m) because of cyclic dependency: it installs notify, and notify
	// imports network. Keep in sync with testutil.InitFailFastMocks' body, minus notify.
	configpaths.InstallFailFastConfigPaths()
	docker.InstallFailFastGet()
	doctor.InstallFailFast()
	msb.InstallFailFastGet()
	opener.InstallFailFast()
	m.Run()
}
