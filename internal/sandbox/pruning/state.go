package pruning

import (
	"context"
	"time"

	"github.com/inoio/opencode-sandbox/internal/sandbox/msb"
	"github.com/inoio/opencode-sandbox/internal/sandbox/naming"
)

// PruneState is a point-in-time view of which project slugs have a surviving VM.
// A slug is kept only if it has a VM that PruneSandboxes will not remove.
type PruneState map[string]msb.SandboxHandle // slug -> current handle, for sandboxes to be pruned

func buildPruneState(ctx context.Context, age time.Duration) (PruneState, error) {
	result := PruneState{}
	handles, err := msb.Get().ListSandboxes(ctx, nil)
	if err != nil {
		return result, err
	}
	for _, h := range handles {
		name := h.Name()
		if hasPrefix(name, naming.VmPrefix) || hasPrefix(name, naming.TaskPrefix) {
			slug := naming.ArtifactFor(name).Slug
			if slug == "" {
				continue
			}
			if !msb.IsSandboxActive(h.Status()) {
				if time.Since(h.UpdatedAt()) >= age || hasPrefix(name, naming.TaskPrefix) {
					result[slug] = h
				}
			}
		}
	}
	return result, nil
}
