package pruning

import (
	"context"
	"time"

	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/naming"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
)

// PruneState is a point-in-time view of the project VMs and task sandboxes in
// the private runtime home. ToPrune holds the project/agent keys whose project
// VM is being reclaimed (a stale, stopped VM) with its handle; ToKeep holds the
// keys that have a live, kept project VM. A key in neither set has no project VM
// at all, so its cached artifacts (e.g. its home volume) are dangling and can be
// reclaimed. RetainKeys holds the keys the current invocation is about to use
// (a `run`/`shell` passes its own project key): their runner images are never
// reclaimed regardless of VM liveness or age, so a run never destroys the image
// it is about to import. Tasks holds the transient task sandboxes to reclaim;
// they are deliberately kept out of the key maps so a leftover task can never
// mark its still-active project as stale.
//
// Age is the staleness threshold the snapshot was built with, reused by image
// retention as the window in which a project's cached image variant counts as
// reusable. A zero Age disables recency retention, so callers must build the
// state via buildPruneState rather than constructing a PruneState literal.
type PruneState struct {
	Age        time.Duration
	ToPrune    map[state.Key]msb.SandboxHandle
	ToKeep     map[state.Key]msb.SandboxHandle
	RetainKeys map[state.Key]struct{}
	Tasks      []msb.SandboxHandle
}

func buildPruneState(ctx context.Context, age time.Duration) (PruneState, error) {
	result := PruneState{
		Age:        age,
		ToPrune:    map[state.Key]msb.SandboxHandle{},
		ToKeep:     map[state.Key]msb.SandboxHandle{},
		RetainKeys: nil,
		Tasks:      nil,
	}
	handles, err := msb.Get().ListSandboxes(ctx, nil)
	if err != nil {
		// gomutants:disable-next-line RETURN_ZERO reason="every caller returns immediately on this error, discarding the PruneState, so its zero versus initialized maps are unobservable"
		return result, err
	}
	for _, h := range handles {
		identity := noteIdentity(h)
		if identity.Slug == "" {
			continue
		}
		if identity.Task {
			// Task sandboxes are transient workers: always reclaimable once
			// stopped, and never representative of a project's liveness.
			if msb.IsSandboxInactive(h.Status()) {
				result.Tasks = append(result.Tasks, h)
			}
			continue
		}
		key := state.Key{Slug: identity.Slug, Agent: identity.Agent}
		if prunable(h, age) {
			result.ToPrune[key] = h
			continue
		}
		result.ToKeep[key] = h
	}
	return result, nil
}

// keySet indexes keys for membership tests, returning nil for none so a caller
// can assign it straight into PruneState.RetainKeys.
func keySet(keys []state.Key) map[state.Key]struct{} {
	if len(keys) == 0 {
		return nil
	}
	set := make(map[state.Key]struct{}, len(keys))
	for _, key := range keys {
		set[key] = struct{}{}
	}
	return set
}

// noteIdentity resolves a sandbox's project key and kind, reading its labels
// (authoritative for anything the launcher created) and falling back to the name
// heuristic for a sandbox created before the labels existed. A config read error
// degrades to the name fallback rather than failing the whole prune.
func noteIdentity(h msb.SandboxHandle) naming.ArtifactInfo {
	var labels map[string]string
	if cfg, err := h.Config(); err == nil && cfg != nil {
		labels = cfg.Labels
	}
	return naming.SandboxIdentity(h.Name(), labels)
}

// prunable reports whether a project VM should be reclaimed: a stopped VM older
// than the age threshold.
func prunable(h msb.SandboxHandle, age time.Duration) bool {
	if !msb.IsSandboxInactive(h.Status()) {
		return false
	}
	// gomutants:disable-next-line CONDITIONALS_BOUNDARY reason="the boundary is an exact nanosecond equality between time.Since and the threshold, with no injectable clock to hit it; >= and > agree on every reachable input"
	return time.Since(h.UpdatedAt()) >= age
}
