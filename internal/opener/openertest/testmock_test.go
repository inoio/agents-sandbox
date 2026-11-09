package openertest

import (
	"os/exec"
	"testing"

	"github.com/inoio/agents-sandbox/internal/opener"
)

// TestInstallFailFastWiresExecCommand covers InstallFailFast replacing the
// package's exec seam with the panic, so a test that forgets to stub it fails
// loud instead of opening a browser.
func TestInstallFailFastWiresExecCommand(t *testing.T) {
	previous := opener.SetExecCommand(func(string, ...string) *exec.Cmd {
		return exec.Command("true")
	})
	t.Cleanup(func() { opener.SetExecCommand(previous) })

	InstallFailFast()

	defer func() {
		if recover() == nil {
			t.Fatal("InstallFailFast did not wire the fail-fast command")
		}
	}()
	_ = opener.OpenOnHost("https://example.com")
}

func TestFailFastExecCommandPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic")
		}
	}()
	failFastExecCommand("xdg-open", "https://example.com")
}
