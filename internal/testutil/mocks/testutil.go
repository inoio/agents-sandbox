//deadcode:ignore reason="test support: referenced only by _test.go files"
package mocks

import (
	"testing"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/notify"
	"github.com/inoio/agents-sandbox/internal/opener"
	"github.com/inoio/agents-sandbox/internal/sandbox/docker"
	"github.com/inoio/agents-sandbox/internal/sandbox/doctor"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
)

// InitFailFastMocks initializes every fail-fast mock for tests, so tests that forget to define a mock fail fast instead
// of using real implementations. A package notify imports back cannot import this package without an import cycle; it
// installs the same set minus notify directly, mirroring this body.
func InitFailFastMocks(m *testing.M) {
	configpaths.InstallFailFastConfigPaths()
	docker.InstallFailFastGet()
	doctor.InstallFailFast()
	msb.InstallFailFastGet()
	notify.InstallFailFast()
	// gomutants:disable-next-line STATEMENT_REMOVE reason="reachable only through another package's TestMain: InitFailFastMocks calls m.Run() after this, so no test inside this package can invoke it; the installed seam (opener.InstallFailFast) is itself covered by internal/opener's tests"
	opener.InstallFailFast()
	// gomutants:disable-next-line STATEMENT_REMOVE reason="the wrapper is reachable only through TestMain, so removing m.Run() returns 0 with zero tests run and the package still reports ok: no assertion inside the test binary can reach this statement, and nothing asserts the process exit status"
	m.Run()
}
