package runtimemigration

import "github.com/inoio/agents-sandbox/internal/sandbox/state"

// seedRootState writes rs as the launcher-global RootState through the root
// transaction. Tests only.
func seedRootState(rs state.RootState) error {
	_, err := state.WithRootState(true, func(tx *state.RootTxn) error {
		*tx.State() = rs
		return tx.Commit()
	})
	return err
}
