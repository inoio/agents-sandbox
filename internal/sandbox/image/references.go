package image

import (
	"fmt"
	"slices"

	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/naming"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
)

// InUseImageReferences maps each runner image reference in use to the project
// keys that reference it. It unions two sources: the project-state scan
// (state.ImageReferences), the source of truth for ownership, and the image
// references held by the given kept sandboxes, which catch a digest-pinned VM
// whose tag is no longer the per-agent "-latest" that its slug and agent name
// derive.
//
// The scan fails closed: an unreadable or corrupt project state file, or a kept
// sandbox whose config cannot be read, is returned as an error rather than
// silently treated as unreferenced. It reads the image from Config rather than
// Image, because Image swallows that config error.
func InUseImageReferences(keptSandboxes []msb.SandboxHandle) (map[string][]state.Key, error) {
	references, err := state.ImageReferences()
	if err != nil {
		return nil, err
	}
	for _, handle := range keptSandboxes {
		config, err := handle.Config()
		if err != nil {
			return nil, fmt.Errorf("read sandbox %q config: %w", handle.Name(), err)
		}
		ref := config.Image
		if ref == "" {
			continue
		}
		key := sandboxKey(handle.Name(), config.Labels)
		if key.Slug == "" || slices.Contains(references[ref], key) {
			continue
		}
		references[ref] = append(references[ref], key)
	}
	return references, nil
}

// sandboxKey derives the project key a sandbox belongs to, preferring the
// identity labels and falling back to the name heuristic (SandboxIdentity).
func sandboxKey(name string, labels map[string]string) state.Key {
	info := naming.SandboxIdentity(name, labels)
	return state.Key{Slug: info.Slug, Agent: info.Agent}
}
