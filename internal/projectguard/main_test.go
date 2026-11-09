package projectguard

import (
	"testing"

	"github.com/inoio/agents-sandbox/internal/configpaths/configpathstest"
	"github.com/inoio/agents-sandbox/internal/opener/openertest"
	"github.com/inoio/agents-sandbox/internal/sandbox/docker/dockertest"
	"github.com/inoio/agents-sandbox/internal/sandbox/doctor/doctortest"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb/msbtest"
)

// TestMain installs the fail-fast mocks projectguard's tests can import without
// a cycle. testutil's InitFailFastMocks is out of reach: the notify family
// transitively imports projectguard through filewalk.
func TestMain(m *testing.M) {
	configpathstest.InstallFailFastConfigPaths()
	dockertest.InstallFailFastGet()
	doctortest.InstallFailFast()
	msbtest.InstallFailFastGet()
	openertest.InstallFailFast()
	m.Run()
}
