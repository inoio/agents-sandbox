package notify

import "os/exec"

// SetExecCommand replaces the exec seam the desktop and audio notifiers reach
// their host command through, and returns the previous value so a caller can
// restore it.
//
//deadcode:ignore reason="test seam: only notifytest sets it"
func SetExecCommand(fn func(name string, args ...string) *exec.Cmd) func(string, ...string) *exec.Cmd {
	previous := execCommand
	execCommand = fn
	return previous
}
