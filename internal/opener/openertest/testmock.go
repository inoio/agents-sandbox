//deadcode:ignore reason="test support: referenced only by _test.go files"
package openertest

import (
	"os/exec"

	"github.com/inoio/agents-sandbox/internal/opener"
)

// InstallFailFast points opener's exec seam at a panic so a test that reaches
// the real host opener without overriding the seam fails loud instead of opening
// a browser. Wired up by the package TestMain and testutil.InitFailFastMocks.
func InstallFailFast() {
	opener.SetExecCommand(failFastExecCommand)
}

func failFastExecCommand(name string, _ ...string) *exec.Cmd {
	panic("opener: reached real execCommand(" + name + "); override execCommand in the test")
}
