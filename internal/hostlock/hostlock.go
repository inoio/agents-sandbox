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

// flock is a seam so tests can exercise the failure path, which a real file
// cannot trigger.
var flock = syscall.Flock

// AcquireExclusive creates dir (0700) if needed, opens dir/name (0600) and
// blocks until it holds an exclusive flock on it. The returned function
// releases the lock.
func AcquireExclusive(dir, name string) (func(), error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create lock dir: %w", err)
	}
	path := filepath.Join(dir, name)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open lock %s: %w", path, err)
	}
	if err := Exclusive(f); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("flock %s: %w", path, err)
	}
	return func() { _ = f.Close() }, nil
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
