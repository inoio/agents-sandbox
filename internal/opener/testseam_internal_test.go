package opener

import (
	"os/exec"
	"testing"
)

// TestSetExecCommandReturnsPrevious pins the setter contract openertest relies
// on: it installs the given command and returns the previous one, so a caller
// restores the previous seam by passing it back.
func TestSetExecCommandReturnsPrevious(t *testing.T) {
	first := func(string, ...string) *exec.Cmd { return exec.Command("first") }
	second := func(string, ...string) *exec.Cmd { return exec.Command("second") }

	original := SetExecCommand(first)
	t.Cleanup(func() { SetExecCommand(original) })

	if got := execCommand("x").Path; got != "first" {
		t.Fatalf("first command not in effect: Path = %q", got)
	}

	returned := SetExecCommand(second)
	if got := returned("x").Path; got != "first" {
		t.Fatalf("setter did not return the previous command: Path = %q, want %q", got, "first")
	}
	if got := execCommand("x").Path; got != "second" {
		t.Fatalf("second command not in effect: Path = %q", got)
	}
	if got := SetExecCommand(original)("x").Path; got != "second" {
		t.Fatalf("passing the previous command back did not restore it: Path = %q, want %q", got, "second")
	}
}
