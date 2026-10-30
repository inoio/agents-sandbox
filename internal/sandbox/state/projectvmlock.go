package state

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/inoio/agents-sandbox/internal/hostlock"
)

// ProjectVMLockName is the file name of the project-VM creation lock inside
// ProjectVMLockDir. It is exported so ensureProjectVM (in package vm) and this
// package cannot disagree about the file the lock is taken on.
const ProjectVMLockName = "ensure-vm.lock"

// Seams over the project-VM lock's I/O so tests can exercise failures the real
// functions cannot trigger. Production keeps the real functions.

var (
	mkdirProjectVMLock = os.MkdirAll
	openProjectVMLock  = os.OpenFile
)

// flockProjectVMLock takes an exclusive advisory lock on the project-VM lock
// file, non-blocking when blocking is false. It is a seam so tests can exercise
// the flock failure path.
var flockProjectVMLock = func(f *os.File, blocking bool) error {
	if blocking {
		return hostlock.Exclusive(f)
	}
	return hostlock.ExclusiveNonBlocking(f)
}

// ProjectVMLockDir returns the directory holding the project's VM-creation lock
// file. It lives under the never-deleted locksDir tree, not the removable key
// directory, so pruning a project's state cannot unlink the lock a concurrent
// VM creation holds (#330). It is keyed by agent as well as slug: agents of one
// project use separate VMs, so a shared lock would serialize unrelated creates.
func ProjectVMLockDir(k Key) string {
	return filepath.Join(locksDir(), k.Slug, k.Agent)
}

// lockProjectVM takes the per-(slug, agent) advisory lock that serializes the
// project VM's creation and destruction, so two launchers cannot build or tear
// down the same VM at once. The flock auto-releases on process exit, so a
// crashed holder never pins the lock. It is not reentrant: a single call path
// must never take the same key's project-VM lock twice, since flock locks a
// file description per open. The file is shared with ensureProjectVM, which
// takes it via hostlock.AcquireExclusive and so bypasses the same-process
// registry: nesting that acquire inside this one hangs on the second flock
// instead of failing fast.
//
// With blocking true the call waits for the lock; with blocking false it
// returns ok=false and a nil release when another process holds it.
//
//nolint:nonamedreturns // the documented (release, ok, err) signature is the contract
func lockProjectVM(k Key, blocking bool) (release func(), ok bool, err error) {
	dir := ProjectVMLockDir(k)
	if mkdirErr := mkdirProjectVMLock(dir, 0o700); mkdirErr != nil {
		return nil, false, fmt.Errorf("create lock dir: %w", mkdirErr)
	}
	path := filepath.Join(dir, ProjectVMLockName)
	if blocking {
		if holdErr := holdLockPath(path); holdErr != nil {
			return nil, false, holdErr
		}
	}
	f, err := openProjectVMLock(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		if blocking {
			releaseLockPath(path)
		}
		return nil, false, fmt.Errorf("open project VM lock: %w", err)
	}
	if err := flockProjectVMLock(f, blocking); err != nil {
		_ = f.Close()
		if blocking {
			releaseLockPath(path)
		}
		if !blocking && hostlock.IsContention(err) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("flock project VM lock: %w", err)
	}
	release = func() {
		_ = f.Close()
		if blocking {
			releaseLockPath(path)
		}
	}
	return release, true, nil
}
