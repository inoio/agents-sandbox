package volume

import (
	"context"
	"fmt"

	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
)

// PreviewHomeVolume resolves, read-only, the home volume a run would use and
// whether it would have to be created. It mirrors ResolveHomeVolume's
// reuse → recover → create decision without any write: no volume is created,
// no state is repaired, and nothing is prefilled.
//
// A lookup error on the recorded volume that is not true absence is returned
// rather than reported as "would create", matching the real run's fail-closed
// check. The recovery candidate is supplied by the caller; how it was obtained
// (the live VM's mount, read best-effort) is the caller's concern and is not
// widened here.
//
// The create branch reports an empty name: a fresh volume's name embeds a
// timestamp (HomeVolumeName), so a preview can only name a volume it would
// reuse.
func PreviewHomeVolume(
	ctx context.Context,
	client msb.Client,
	k state.Key,
	recoveredVolume string,
) (string, bool, error) {
	if st, err := state.ReadState(k); err == nil && st.HomeVolume != "" {
		if _, err := client.GetVolume(ctx, st.HomeVolume); err == nil {
			return st.HomeVolume, false, nil
		} else if !msb.IsVolumeNotFound(err) {
			return "", false, fmt.Errorf("checking home volume %q: %w", st.HomeVolume, err)
		}
	}
	recovered, ok, err := confirmedRecoveredVolume(ctx, client, recoveredVolume)
	if err != nil {
		return "", false, err
	}
	if ok {
		return recovered, false, nil
	}
	return "", true, nil
}
