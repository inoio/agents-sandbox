package state

import (
	"fmt"
	"os"
)

// statRootStateFile is a seam over stat'ing the root state file so tests can
// exercise the existence check's failure path.
var statRootStateFile = os.Stat

// RootTxn is the lock-protected, mutable handle for the launcher-global
// RootState produced by WithRootState.
//
// A RootTxn is valid only for the duration of its WithRootState closure: the
// root lock is released when the closure returns, so the handle must not be
// retained, read, or Commit()ed afterwards.
type RootTxn struct {
	state  *RootState
	exists bool
}

// State returns the in-memory document the transaction mutates.
func (t *RootTxn) State() *RootState { return t.state }

// Exists reports whether a readable root state file was present when the
// transaction opened. A missing file is Exists()==false with a zero State().
func (t *RootTxn) Exists() bool { return t.exists }

// Commit atomically writes State() to disk. It is repeatable, so a long campaign
// can checkpoint its progress after each unit of work.
func (t *RootTxn) Commit() error { return writeRootState(*t.state) }

// WithRootState holds the host-only root-state lock across read -> fn, handing fn
// a RootTxn. It is the single locked transaction primitive for the
// launcher-global RootState, so no caller can pick the wrong lock or forget it.
//
// With blocking true it waits for the lock for the whole closure and ok is true
// unless the lock itself fails. With blocking false it returns ok=false and a nil
// error when the lock is contended, so the caller defers to a later run.
//
// A missing file hands fn a zero RootState with Exists()==false. A corrupt or
// unreadable document is returned as an error and fn is never called, so an
// unknown document is never clobbered.
//
//nolint:nonamedreturns // the documented (ok, err) signature is the contract
func WithRootState(blocking bool, fn func(*RootTxn) error) (ok bool, err error) {
	release, ok, err := lockRootState(blocking)
	if err != nil || !ok {
		return ok, err
	}
	defer release()

	exists, err := rootStatePresent()
	if err != nil {
		return true, err
	}
	root, err := ReadRootState()
	if err != nil {
		return true, fmt.Errorf("read root state for transaction: %w", err)
	}
	return true, fn(&RootTxn{state: root, exists: exists})
}

// rootStatePresent reports whether the root state file exists, distinguishing a
// missing file (Exists()==false) from an unreadable one.
func rootStatePresent() (bool, error) {
	_, err := statRootStateFile(rootStateFile())
	switch {
	case err == nil:
		return true, nil
	case os.IsNotExist(err):
		return false, nil
	default:
		return false, fmt.Errorf("stat root state: %w", err)
	}
}
