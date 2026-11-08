package runtime

import (
	"testing"

	"github.com/inoio/agents-sandbox/internal/configpaths/configpathstest"
	"github.com/inoio/agents-sandbox/internal/notify/notifytest"
	"github.com/inoio/agents-sandbox/internal/opener/openertest"
	"github.com/inoio/agents-sandbox/internal/sandbox/docker/dockertest"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb/msbtest"
)

// TestMain installs the fail-fast defaults for the seam factories so no runtime
// test can silently touch real config paths, Docker, msb, or notifications. It
// cannot call testutil.InitFailFastMocks: doctor imports runtime, so that would
// close a test import cycle. Doctor is not reachable from runtime, so its
// fail-fast is not installed here.
func TestMain(m *testing.M) {
	configpathstest.InstallFailFastConfigPaths()
	dockertest.InstallFailFastGet()
	msbtest.InstallFailFastGet()
	notifytest.InstallFailFast()
	openertest.InstallFailFast()
	m.Run()
}
