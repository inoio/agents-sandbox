package hostlock

import (
	"testing"

	"github.com/inoio/agents-sandbox/internal/configpaths/configpathstest"
	"github.com/inoio/agents-sandbox/internal/opener/openertest"
	"github.com/inoio/agents-sandbox/internal/sandbox/docker/dockertest"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb/msbtest"
)

// TestMain installs the fail-fast mocks hostlock's tests can import without a
// cycle. testutil.InitFailFastMocks is out of reach: the doctor and notify
// families transitively import hostlock (via runtime and state).
func TestMain(m *testing.M) {
	configpathstest.InstallFailFastConfigPaths()
	dockertest.InstallFailFastGet()
	msbtest.InstallFailFastGet()
	openertest.InstallFailFast()
	m.Run()
}
