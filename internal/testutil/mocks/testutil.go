//deadcode:ignore reason="test support: referenced only by _test.go files"
package mocks

import (
	"testing"

	"github.com/inoio/agents-sandbox/internal/configpaths/configpathstest"
	"github.com/inoio/agents-sandbox/internal/notify/notifytest"
	"github.com/inoio/agents-sandbox/internal/opener/openertest"
	"github.com/inoio/agents-sandbox/internal/sandbox/docker/dockertest"
	"github.com/inoio/agents-sandbox/internal/sandbox/doctor/doctortest"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb/msbtest"
)

// InitFailFastMocks initializes every fail-fast mock for tests, so tests that forget to define a mock fail fast instead
// of using real implementations. A package notify imports back cannot import this package without an import cycle; it
// installs the same set minus notify directly, mirroring this body.
func InitFailFastMocks(m *testing.M) {
	configpathstest.InstallFailFastConfigPaths()
	dockertest.InstallFailFastGet()
	doctortest.InstallFailFast()
	msbtest.InstallFailFastGet()
	notifytest.InstallFailFast()
	// gomutants:disable-next-line STATEMENT_REMOVE reason="reachable only through another package's TestMain: InitFailFastMocks calls m.Run() after this, so no test inside this package can invoke it; the installed seam (openertest.InstallFailFast) is itself covered by the openertest tests"
	openertest.InstallFailFast()
	// gomutants:disable-next-line STATEMENT_REMOVE reason="the wrapper is reachable only through TestMain, so removing m.Run() returns 0 with zero tests run and the package still reports ok: no assertion inside the test binary can reach this statement, and nothing asserts the process exit status"
	m.Run()
}
