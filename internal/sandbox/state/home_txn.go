package state

import (
	"errors"
	"fmt"
)

// WithHomeState holds the (slug, agent) home lock across read -> fn -> one
// write, so no caller can pick the wrong lock or forget it. It is the single
// locked transaction primitive for a project's HomeState; every mutation goes
// through it (or RecoverHomeState).
//
// exists reports whether a readable state file was present. A missing file
// hands fn exists=false and a zero HomeState, letting the writer decide whether
// to create it. An unreadable or unparsable document is returned as an error
// and fn is never called, so an unknown document is never clobbered.
//
// fn returns whether to write and may fail, so a caller can abort a stale write
// (compare-and-set guard). A non-nil error or write=false writes nothing.
func WithHomeState(k Key, fn func(cur *HomeState, exists bool) (write bool, err error)) error {
	return withHomeLock(k, func() error {
		return homeStateTxn(k, fn, false)
	})
}

// RecoverHomeState is WithHomeState with repair semantics: a missing OR
// ErrStateUnparsable document hands fn exists=false and a zero HomeState, so
// recovery can rebuild and repair it. It is the only transaction that may
// discard an unparsable document; any other read error is returned and fn is
// never called.
func RecoverHomeState(k Key, fn func(cur *HomeState, exists bool) (write bool, err error)) error {
	return withHomeLock(k, func() error {
		return homeStateTxn(k, fn, true)
	})
}

// homeStateTxn is the read -> fn -> conditional write body of WithHomeState and
// RecoverHomeState, run by its caller while the home lock is held. repair widens
// the repairable read errors from "missing only" to "missing or unparsable".
func homeStateTxn(k Key, fn func(cur *HomeState, exists bool) (write bool, err error), repair bool) error {
	cur, err := ReadState(k)
	exists := true
	if err != nil {
		repairable := errors.Is(err, ErrStateNotFound) || (repair && errors.Is(err, ErrStateUnparsable))
		if !repairable {
			return fmt.Errorf("read state for transaction: %w", err)
		}
		cur, exists = new(HomeState), false
	}
	write, fnErr := fn(cur, exists)
	if fnErr != nil {
		return fnErr
	}
	if !write {
		return nil
	}
	return writeCurrentHomeState(k, cur)
}

// writeCurrentHomeState persists the transaction's current document.
func writeCurrentHomeState(k Key, cur *HomeState) error {
	return writeState(k, *cur)
}
