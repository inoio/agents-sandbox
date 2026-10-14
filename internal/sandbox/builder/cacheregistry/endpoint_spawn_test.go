//go:build !windows

package cacheregistry

import (
	"os/exec"
	"syscall"
	"testing"
	"time"
)

// TestStartDetachedCommandReapsChild asserts a detached child is reaped once it
// exits, so a long-lived launcher does not accumulate zombies from registry
// daemons that idle-reaped. syscall.Kill(pid, 0) returns ESRCH only after the
// child has been reaped; a zombie still answers signal 0.
func TestStartDetachedCommandReapsChild(t *testing.T) {
	pid, err := startDetachedCommand(exec.Command("/bin/sh", "-c", "exit 0"))
	if err != nil {
		t.Fatalf("startDetachedCommand: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); err == syscall.ESRCH {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("detached child %d was not reaped (still present)", pid)
}
