package pruning

import (
	"time"

	"github.com/inoio/agents-sandbox/internal/sandbox/state"
)

// PruneOptions configures a Prune run: the stale-age threshold, whether the run
// only reports, whether unreferenced home volumes may be removed, and the keys
// the current invocation is about to use (their images are never reclaimed).
//
// RemoveUnreferencedVolumes is the safe/approval-gated tier boundary: only a
// caller that has already confirmed with the user may set it, so auto-prune can
// never delete a home volume.
type PruneOptions struct {
	Age                       time.Duration
	DryRun                    bool
	RemoveUnreferencedVolumes bool
	Retain                    []state.Key
}
