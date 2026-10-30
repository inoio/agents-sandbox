package state

import (
	"fmt"
)

// RootTxn is the lock-protected, mutable handle for the launcher-global
// RootState produced by WithRootState.
//
// A RootTxn is valid only for the duration of its WithRootState closure: the
// root lock is released when the closure returns, so the handle must not be
// retained, read, or Commit()ed afterwards.
type RootTxn struct {
	state *RootState
}

// State returns the in-memory document the transaction mutates.
func (t *RootTxn) State() *RootState { return t.state }

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
// A missing file hands fn a zero RootState. A corrupt or unreadable document is
// returned as an error and fn is never called, so an unknown document is never
// clobbered; RepairRootState is the explicit way to quarantine a corrupt one.
//
//nolint:nonamedreturns // the documented (ok, err) signature is the contract
func WithRootState(blocking bool, fn func(*RootTxn) error) (ok bool, err error) {
	release, ok, err := lockRootState(blocking)
	if err != nil || !ok {
		return ok, err
	}
	defer release()

	root, err := ReadRootState()
	if err != nil {
		return true, fmt.Errorf("read root state for transaction: %w", err)
	}
	return true, fn(&RootTxn{state: root})
}
