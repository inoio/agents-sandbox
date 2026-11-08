//deadcode:ignore reason="test support: referenced only by _test.go files"
package notifytest

import (
	"os/exec"

	"github.com/inoio/agents-sandbox/internal/notify"
)

// InstallFailFast points notify's exec seam at a panic so a test that reaches a
// real desktop/audio notification command without overriding the seam fails
// loud instead of spawning a real player. Wired up by the package TestMain.
func InstallFailFast() {
	notify.SetExecCommand(failFastExecCommand)
}

func failFastExecCommand(name string, _ ...string) *exec.Cmd {
	panic("notify: reached real execCommand(" + name + "); override execCommand in the test")
}
