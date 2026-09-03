package pruning

import (
	"context"
	"time"

	"github.com/inoio/opencode-sandbox/internal/sandbox/msb"
	"github.com/inoio/opencode-sandbox/internal/sandbox/naming"
	"github.com/inoio/opencode-sandbox/internal/sandbox/state"
)

// PruneState is a point-in-time view of which project/agent keys have a VM and
// how it is classified. ToPrune holds the keys whose VM is being reclaimed
// (stale VMs and leftover task sandboxes) with their sandbox handle; ToKeep
// holds the keys that have a live, kept project VM and its sandbox handle. A
// key in neither set has no project VM at all, so its cached artifacts (e.g.,
// runner images) are dangling and can be reclaimed.
type PruneState struct {
	ToPrune map[state.Key]msb.SandboxHandle
	ToKeep  map[state.Key]msb.SandboxHandle
}

func buildPruneState(ctx context.Context, age time.Duration) (PruneState, error) {
	result := PruneState{
		ToPrune: map[state.Key]msb.SandboxHandle{},
		ToKeep:  map[state.Key]msb.SandboxHandle{},
	}
	handles, err := msb.Get().ListSandboxes(ctx, nil)
	if err != nil {
		return result, err
	}
	for _, h := range handles {
		name := h.Name()
		if !hasPrefix(name, naming.VmPrefix) && !hasPrefix(name, naming.TaskPrefix) {
			continue
		}
		info := naming.ArtifactFor(name)
		key := state.Key{Slug: info.Slug, Agent: info.Agent}
		if key.Slug == "" {
			continue
		}
		if prunable(name, h, age) {
			result.ToPrune[key] = h
			continue
		}
		// A project VM that is not being pruned counts as a kept VM. Task
		// sandboxes are transient workers and never represent a kept project.
		if hasPrefix(name, naming.VmPrefix) {
			result.ToKeep[key] = h
		}
	}
	return result, nil
}

// prunable reports whether a sandbox should be reclaimed: an inactive project VM
// older than the age threshold, or any stopped task sandbox (transient workers).
func prunable(name string, h msb.SandboxHandle, age time.Duration) bool {
	if msb.IsSandboxActive(h.Status()) {
		return false
	}
	return time.Since(h.UpdatedAt()) >= age || hasPrefix(name, naming.TaskPrefix)
}
