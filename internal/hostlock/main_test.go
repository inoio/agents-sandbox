package hostlock

import (
	"testing"

	"github.com/inoio/agents-sandbox/internal/configpaths/configpathstest"
	"github.com/inoio/agents-sandbox/internal/opener"
	"github.com/inoio/agents-sandbox/internal/sandbox/docker/dockertest"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
)

// TestMain installs the fail-fast mocks hostlock's tests can import without a
// cycle. testutil.InitFailFastMocks is out of reach: the doctor and notify
// families transitively import hostlock (via runtime and state).
func TestMain(m *testing.M) {
	configpathstest.InstallFailFastConfigPaths()
	dockertest.InstallFailFastGet()
	msb.InstallFailFastGet()
	opener.InstallFailFast()
	m.Run()
}
