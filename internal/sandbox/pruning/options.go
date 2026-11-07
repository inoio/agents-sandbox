package pruning

import (
	"time"

	"github.com/inoio/agents-sandbox/internal/sandbox/state"
)

// PruneOptions configures a Prune run: the stale-age threshold, whether the run
// only reports, and the keys the current invocation is about to use (their images
// are never reclaimed).
//
// Prune is the safe tier and can never remove a home volume: the volume phase
// lives in PruneVolumes, reachable only through `volume prune` and the explicit
// project purge.
type PruneOptions struct {
	Age    time.Duration
	DryRun bool
	Retain []state.Key
}
