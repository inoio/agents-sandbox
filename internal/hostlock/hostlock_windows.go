//go:build windows

package hostlock

import (
	"errors"

	"golang.org/x/sys/windows"
)

// flock is indirect because a real, open lock file cannot make it fail, so only
// a substitute reaches the failure path.
var flock = windowsFlock

const (
	lockExclusive   = 1
	lockNonBlocking = 2
)

func windowsFlock(fd int, how int) error {
	flags := uint32(windows.LOCKFILE_EXCLUSIVE_LOCK)
	if how&lockNonBlocking != 0 {
		flags |= windows.LOCKFILE_FAIL_IMMEDIATELY
	}
	return windows.LockFileEx(windows.Handle(fd), flags, 0, 1, 0, new(windows.Overlapped))
}

func isContention(err error) bool {
	return errors.Is(err, windows.ERROR_LOCK_VIOLATION)
}
