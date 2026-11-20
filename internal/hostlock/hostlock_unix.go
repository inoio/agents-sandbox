//go:build !windows

package hostlock

import (
	"errors"
	"syscall"
)

// flock is indirect because a real, open lock file cannot make it fail, so only
// a substitute reaches the failure path.
var flock = syscall.Flock

const (
	lockExclusive   = syscall.LOCK_EX
	lockNonBlocking = syscall.LOCK_NB
)

func isContention(err error) bool {
	return errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN)
}
