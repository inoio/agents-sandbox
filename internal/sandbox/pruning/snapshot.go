package pruning

import (
	"context"
	"time"

	"gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/msb"
	"gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/naming"
)

// LiveState is a point-in-time view of which project slugs have a surviving VM.
// A slug is kept only if it has a VM that PruneVMs will not remove.
type LiveState struct {
	ActiveVMs map[string]string // slug -> current image digest, for RUNNING VMs
	AllVMs    map[string]bool   // slug with a kept VM (running, or stopped-but-not-stale)
}

// BuildLiveState lists sandboxes and records which slugs have a kept VM.
// A stopped/crashed VM is kept (in AllVMs) only when younger than threshold; a stale
// stopped VM is excluded so its volumes/images become prunable. Task sandboxes
// (transient workers) are never keep-set members.
func BuildLiveState(ctx context.Context, client msb.Client, threshold time.Duration) (LiveState, error) {
	snap := LiveState{
		ActiveVMs: make(map[string]string),
		AllVMs:    make(map[string]bool),
	}
	handles, err := client.ListSandboxes(ctx)
	if err != nil {
		return LiveState{}, err
	}
	for _, h := range handles {
		name := h.Name()
		if !hasPrefix(name, naming.VmPrefix) {
			continue
		}
		slug := naming.ArtifactFor(name).Slug
		if slug == "" {
			continue
		}
		if msb.IsSandboxActive(h.Status()) {
			snap.ActiveVMs[slug] = imageDigest(h.Image())
			snap.AllVMs[slug] = true
			continue
		}
		if time.Since(h.UpdatedAt()) <= threshold {
			snap.AllVMs[slug] = true
		}
	}
	return snap, nil
}
