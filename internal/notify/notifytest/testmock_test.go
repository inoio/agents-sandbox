package notifytest

import (
	"os/exec"
	"testing"

	"github.com/inoio/agents-sandbox/internal/notify"
)

// TestInstallFailFastPanicsOnNotify pins that after InstallFailFast a test that
// reaches the real desktop-notification command panics instead of spawning a
// player.
func TestInstallFailFastPanicsOnNotify(t *testing.T) {
	previous := notify.SetExecCommand(func(string, ...string) *exec.Cmd {
		return exec.Command("true")
	})
	t.Cleanup(func() { notify.SetExecCommand(previous) })

	InstallFailFast()

	defer func() {
		if recover() == nil {
			t.Fatal("InstallFailFast did not wire the fail-fast exec seam")
		}
	}()
	(&notify.DesktopNotifier{}).Notify(notify.Notification{Title: "t", Body: "b"})
}
