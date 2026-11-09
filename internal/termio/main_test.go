package termio

import (
	"testing"

	"github.com/inoio/agents-sandbox/internal/configpaths/configpathstest"
	"github.com/inoio/agents-sandbox/internal/opener/openertest"
	"github.com/inoio/agents-sandbox/internal/sandbox/docker/dockertest"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb/msbtest"
)

func TestMain(m *testing.M) {
	// can't call testutil.InitFailFastMocks(m) because of cyclic dependency. Keep in sync with
	// testutil.InitFailFastMocks' body
	configpathstest.InstallFailFastConfigPaths()
	dockertest.InstallFailFastGet()
	// cyclic dependency on termio
	// doctor.InstallFailFast()
	msbtest.InstallFailFastGet()
	openertest.InstallFailFast()
	m.Run()
}
