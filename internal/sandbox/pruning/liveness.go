package pruning

import (
	"context"
	"time"

	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/naming"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
)

// PruneState is a point-in-time view of the project VMs and task sandboxes in
// the private runtime home, plus the keys the current invocation retains: the
// inputs of the liveness rules in specs/lifecycle.md, which this file alone
// implements. ToPrune holds the project/agent keys whose project VM is being
// reclaimed (a stale, stopped VM) with its handle: those keys are retired in this
// pass. ToKeep holds the keys that have a live, kept project VM. A key in neither
// set has no project VM at all and is not yet retired: a build loads its image
// before any VM exists, so its image is kept while used within the age, and its
// home volume is kept. RetainKeys holds the keys the current invocation is about
// to use (a `run`/`shell` passes its own project key): their runner images are
// never reclaimed regardless of VM liveness or age, so a run never destroys the
// image it is about to import. Tasks holds the transient task sandboxes to
// reclaim; they are deliberately kept out of the key maps so a leftover task can
// never mark its still-active project as stale.
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

// buildPruneState snapshots the project VMs and task sandboxes and records the
// keys the current invocation retains, so every liveness input is built here.
func buildPruneState(ctx context.Context, age time.Duration, retain ...state.Key) (PruneState, error) {
	result := PruneState{
		Age:        age,
		ToPrune:    map[state.Key]msb.SandboxHandle{},
		ToKeep:     map[state.Key]msb.SandboxHandle{},
		RetainKeys: keySet(retain),
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

// retires reports whether key's project VM is reclaimed in this pass, which
// retires the key's home volumes and its runner images that nothing else retains.
func (pruneState PruneState) retires(key state.Key) bool {
	_, retired := pruneState.ToPrune[key]
	return retired
}

// allowsNativeImagePrune reports whether the runtime's native image-data prune
// may run. It reclaims cached image data that no sandbox references and cannot
// honor RetainKeys, so it would discard the very image a retaining invocation
// (a `run`/`shell`, pre-loading) is about to use. It is deferred to a prune that
// retains nothing — an explicit `prune`, or a run whose identity could not be
// resolved.
func (pruneState PruneState) allowsNativeImagePrune() bool {
	return len(pruneState.RetainKeys) == 0
}

// keepImage reports whether an msb image reference should be retained. artifact
// must be the parse of imageHandle's reference (the caller parses each listed
// reference once and passes it in, so a reference read back from the microsandbox
// cache is never parsed twice). A ref a kept sandbox still holds is always
// retained, as is one whose key the current invocation is about to use (the
// run's own project, so a run never reclaims the image it is about to import).
// Otherwise the ref survives while it parses an agent and was used within the
// prune age; a ref whose key is being pruned, an agent-less ref, and an aged-out
// one are surplus.
func keepImage(
	imageHandle msb.ImageHandle,
	artifact naming.ArtifactInfo,
	pruneState PruneState,
	keptImageRefs map[string]struct{},
) bool {
	if _, used := keptImageRefs[imageHandle.Reference()]; used {
		return true
	}
	key := state.Key{Slug: artifact.Slug, Agent: artifact.Agent}
	if _, retain := pruneState.RetainKeys[key]; retain {
		return true
	}
	if pruneState.retires(key) {
		return false
	}
	return artifact.Agent != "" && imageWithinAge(imageHandle, pruneState.Age)
}

// imageWithinAge reports whether an image was used within the prune age, falling
// back to its creation time when it was never used. A zero age disables the
// recency rule and a zero timestamp counts as not-within-age.
func imageWithinAge(imageHandle msb.ImageHandle, age time.Duration) bool {
	if age <= 0 {
		return false
	}
	used := imageHandle.LastUsedAt()
	if used.IsZero() {
		used = imageHandle.CreatedAt()
	}
	// gomutants:disable-next-line CONDITIONALS_BOUNDARY reason="the boundary is an exact nanosecond equality between time.Since and the age, with no injectable clock to hit it; < and <= agree on every reachable input"
	return time.Since(used) < age
}

// referencedImages collects the image references that kept sandboxes currently
// point at. An image still in use by a kept sandbox must not be removed, even
// when it is a digest ref rather than a per-agent "-latest" tag (pre-redesign
// VMs), since msb rejects removal of an image its database still references.
func referencedImages(kept map[state.Key]msb.SandboxHandle) map[string]struct{} {
	result := make(map[string]struct{}, len(kept))
	for _, handle := range kept {
		if ref := handle.Image(); ref != "" {
			result[ref] = struct{}{}
		}
	}
	return result
}

// keySet indexes keys for membership tests, returning nil for none.
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
