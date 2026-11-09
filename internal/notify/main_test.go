package notify_test

import (
	"testing"

	"github.com/inoio/agents-sandbox/internal/configpaths/configpathstest"
	"github.com/inoio/agents-sandbox/internal/notify/notifytest"
	"github.com/inoio/agents-sandbox/internal/opener/openertest"
	"github.com/inoio/agents-sandbox/internal/sandbox/docker/dockertest"
	"github.com/inoio/agents-sandbox/internal/sandbox/doctor/doctortest"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb/msbtest"
)

// TestMain installs the fail-fast defaults directly. It stays an external test
// package because notifytest imports notify (to call its exported setter), so an
// internal TestMain could not import it without an import cycle. Keep in sync
// with testutil.InitFailFastMocks' body.
func TestMain(m *testing.M) {
	configpathstest.InstallFailFastConfigPaths()
	dockertest.InstallFailFastGet()
	doctortest.InstallFailFast()
	msbtest.InstallFailFastGet()
	notifytest.InstallFailFast()
	openertest.InstallFailFast()
	m.Run()
}
