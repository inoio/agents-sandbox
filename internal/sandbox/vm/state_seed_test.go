package vm

import "github.com/inoio/agents-sandbox/internal/sandbox/state"

// seedHomeState writes hs as the key's HomeState through the recovery
// transaction, overwriting any existing document. Tests only.
func seedHomeState(k state.Key, hs state.HomeState) error {
	return state.RecoverHomeState(k, func(cur *state.HomeState, _ bool) (bool, error) {
		*cur = hs
		return true, nil
	})
}

// seedRootState writes rs as the launcher-global RootState through the root
// transaction. Tests only.
func seedRootState(rs state.RootState) error {
	_, err := state.WithRootState(true, func(tx *state.RootTxn) error {
		*tx.State() = rs
		return tx.Commit()
	})
	return err
}
