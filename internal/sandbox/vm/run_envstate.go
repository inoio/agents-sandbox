package vm

import (
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
)

// persistFingerprints records the env/secret/network/mount fingerprints in one
// home-state transaction, keeping every other field. With no prior state file it
// is a no-op: home resolution already seeds a fresh home volume's fingerprints
// (#183), and creating a document here would persist home_volume: "" — which the
// next run's home resolution cannot resolve (#430).
func persistFingerprints(k state.Key, fingerprints state.Fingerprints) error {
	return state.WithHomeState(k, func(cur *state.HomeState, exists bool) (bool, error) {
		if !exists {
			return false, nil
		}
		*cur = fingerprints.Apply(*cur)
		return true, nil
	})
}
