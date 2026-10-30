package volume

import (
	"fmt"

	"github.com/inoio/agents-sandbox/internal/sandbox/state"
)

// commitNewHomeVolume records newVolume and digest as the project's home volume,
// keeping every other field (env/secret/network/mount fingerprints and the
// upgrade state) that a volume operation does not affect: dropping them would
// trigger a false VM rebuild or re-fire the upgrade prompt on the next startup
// (see EnvChanged/SecretsChanged/NetworkChanged/MountsChanged treating empty
// state as "changed").
//
// The write runs under the home lock but is guarded by a compare-and-set on
// expectedVolume, since the caller's msb I/O ran without the lock: if another
// writer changed home_volume in the meantime, it aborts rather than clobber it.
//
// clearRecoveredMarker drops a pending identity-adoption marker (#144) when the
// operation replaced the volume that marker recorded; leaving it set would make
// the next home resolution recover the old volume and undo the operation.
func commitNewHomeVolume(k state.Key, expectedVolume, newVolume, digest string, clearRecoveredMarker bool) error {
	return state.WithHomeState(k, func(cur *state.HomeState, _ bool) (bool, error) {
		if cur.HomeVolume != expectedVolume {
			return false, fmt.Errorf(
				"home volume changed concurrently (want %q, have %q)",
				expectedVolume,
				cur.HomeVolume,
			)
		}
		cur.HomeVolume = newVolume
		cur.ImageDigest = digest
		if clearRecoveredMarker {
			cur.RecoveredHomeVolume = ""
		}
		return true, nil
	})
}
