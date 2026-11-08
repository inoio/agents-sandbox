package network

import (
	"testing"

	"github.com/inoio/agents-sandbox/internal/configpaths/configpathstest"
	"github.com/inoio/agents-sandbox/internal/opener/openertest"
	"github.com/inoio/agents-sandbox/internal/sandbox/docker/dockertest"
	"github.com/inoio/agents-sandbox/internal/sandbox/doctor/doctortest"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb/msbtest"
)

func TestMain(m *testing.M) {
	// can't call testutil.InitFailFastMocks(m) because of cyclic dependency: it installs notify, and notify
	// imports network. Keep in sync with testutil.InitFailFastMocks' body, minus notify.
	configpathstest.InstallFailFastConfigPaths()
	dockertest.InstallFailFastGet()
	doctortest.InstallFailFast()
	msbtest.InstallFailFastGet()
	openertest.InstallFailFast()
	m.Run()
}
