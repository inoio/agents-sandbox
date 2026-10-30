package volume

import "github.com/inoio/agents-sandbox/internal/sandbox/state"

// seedHomeState writes hs as the key's HomeState through the recovery
// transaction. Tests only.
func seedHomeState(k state.Key, hs state.HomeState) error {
	return state.RecoverHomeState(k, func(cur *state.HomeState, _ bool) (bool, error) {
		*cur = hs
		return true, nil
	})
}
