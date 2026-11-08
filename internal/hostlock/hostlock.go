// Package hostlock provides exclusive advisory flocks on host files. It is a
// dependency-free leaf so every sandbox package can share one lock primitive.
// A flock auto-releases on process exit, so a crashed holder never pins it.
package hostlock

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// flock is indirect because a real, open lock file cannot make it fail, so only
// a substitute reaches the failure path.
var flock = syscall.Flock

// AcquireExclusive creates dir (0700) if needed, opens dir/name (0600) and
// blocks until it holds an exclusive flock on it. The returned function
// releases the lock.
func AcquireExclusive(dir, name string) (func(), error) {
	return AcquireExclusiveWaiting(dir, name, nil)
}

// AcquireExclusiveWaiting is AcquireExclusive, but calls onWait once before it
// blocks when another open file description already holds the lock, so a caller
// that may wait can say why instead of pausing silently. onWait may be nil.
func AcquireExclusiveWaiting(dir, name string, onWait func()) (func(), error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create lock dir: %w", err)
	}
	path := filepath.Join(dir, name)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open lock %s: %w", path, err)
	}
	if err := acquireWaiting(f, onWait); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("flock %s: %w", path, err)
	}
	return func() { _ = f.Close() }, nil
}

// acquireWaiting takes the lock on f, calling onWait first when the lock is
// already held elsewhere and onWait is non-nil.
func acquireWaiting(f *os.File, onWait func()) error {
	if onWait == nil {
		return Exclusive(f)
	}
	if err := ExclusiveNonBlocking(f); err == nil || !IsContention(err) {
		return err
	}
	onWait()
	return Exclusive(f)
}

// Exclusive blocks until it holds an exclusive flock on f.
func Exclusive(f *os.File) error {
	return flock(int(f.Fd()), syscall.LOCK_EX)
}

// ExclusiveNonBlocking takes an exclusive flock on f, failing with an error
// satisfying IsContention when another open file description holds it.
func ExclusiveNonBlocking(f *os.File) error {
	return flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
}

// IsContention reports whether a flock error means the lock is held elsewhere
// (as opposed to a real failure).
func IsContention(err error) bool {
	return errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN)
}
