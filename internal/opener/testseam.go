package opener

import "os/exec"

// SetExecCommand replaces the exec seam OpenOnHost reaches the host opener
// through, and returns the previous value so a caller can restore it.
//
//deadcode:ignore reason="test seam: only openertest sets it"
func SetExecCommand(fn func(name string, args ...string) *exec.Cmd) func(string, ...string) *exec.Cmd {
	previous := execCommand
	execCommand = fn
	return previous
}
