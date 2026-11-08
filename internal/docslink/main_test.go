package docslink

import (
	"testing"

	"github.com/inoio/agents-sandbox/internal/configpaths/configpathstest"
	"github.com/inoio/agents-sandbox/internal/notify/notifytest"
	"github.com/inoio/agents-sandbox/internal/opener/openertest"
	"github.com/inoio/agents-sandbox/internal/sandbox/docker/dockertest"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
)

func TestMain(m *testing.M) {
	// can't call testutil.InitFailFastMocks(m) because of cyclic dependency: it installs doctor, which imports this
	// package. Keep in sync with testutil.InitFailFastMocks' body.
	configpathstest.InstallFailFastConfigPaths()
	dockertest.InstallFailFastGet()
	msb.InstallFailFastGet()
	notifytest.InstallFailFast()
	openertest.InstallFailFast()
	m.Run()
}
