package vm

import (
	"context"
	"fmt"

	"github.com/inoio/agents-sandbox/internal/sandbox/mounts"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/naming"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
)

// ProjectVMName generates the prefix-free VM name from the project/agent key.
// Long slugs are truncated while the agent suffix is preserved, so the name
// always round-trips back to the same agent.
func ProjectVMName(k state.Key) string {
	return truncateProjectVMName("", k)
}

// legacyProjectVMName generates the pre-change VM name for a key. It is the
// lookup fallback for a VM created before the naming change.
func legacyProjectVMName(k state.Key) string {
	return truncateProjectVMName(naming.LegacyVMPrefix, k)
}

// truncateProjectVMName trims a prefixed project VM name to the runtime's
// maximum length by shortening the slug, never the agent suffix.
func truncateProjectVMName(prefix string, k state.Key) string {
	name := prefix + k.Slug + "-" + k.Agent
	// gomutants:disable-next-line CONDITIONALS_BOUNDARY reason="at exactly the maximum a fall-through rebuilds the identical name (over == 0 keeps the whole slug) and returns it, so the two boundaries cannot differ"
	if len(name) <= options.MaxSandboxNameLen {
		return name
	}
	over := len(name) - options.MaxSandboxNameLen
	slugLen := max(len(k.Slug)-over, 0)
	name = prefix + k.Slug[:slugLen] + "-" + k.Agent
	// gomutants:disable-next-line CONDITIONALS_BOUNDARY reason="the fall-through only slices a name whose length is already >= the maximum; an exactly-maximum name is unchanged by name[:Max]"
	if len(name) > options.MaxSandboxNameLen { // extreme case: agent itself too long
		return name[:options.MaxSandboxNameLen]
	}
	return name
}

// FindProjectVM looks up the project VM for a key under the new name first, then
// the legacy prefixed name, so a VM created before the naming change is still
// found and reused rather than recreated. found is false when neither exists.
func FindProjectVM(ctx context.Context, client msb.Client, k state.Key) (msb.SandboxHandle, bool, error) {
	handle, err := client.GetSandbox(ctx, ProjectVMName(k))
	if err == nil {
		return handle, true, nil
	}
	if !msb.IsNotFound(err) {
		return nil, false, fmt.Errorf("get sandbox %q: %w", ProjectVMName(k), err)
	}
	legacyName := legacyProjectVMName(k)
	handle, err = client.GetSandbox(ctx, legacyName)
	if err == nil {
		return handle, true, nil
	}
	if !msb.IsNotFound(err) {
		return nil, false, fmt.Errorf("get sandbox %q: %w", legacyName, err)
	}
	return nil, false, nil
}

// ProjectVMHomeVolume reports whether the project VM for k exists and the home
// volume it has mounted. A nil handle counts as "no VM". A mounts read failure
// returns exists=true together with the error, so a caller that would destroy the
// VM on the strength of this result can fail closed instead of dropping its
// volume; the best-effort home-recovery caller swallows it.
func ProjectVMHomeVolume(ctx context.Context, client msb.Client, k state.Key) (string, bool, error) {
	handle, found, err := FindProjectVM(ctx, client, k)
	if err != nil {
		return "", false, err
	}
	if !found {
		return "", false, nil
	}
	if handle == nil {
		return "", false, nil
	}
	liveMounts, err := handle.ConfigMounts()
	if err != nil {
		return "", true, err
	}
	return liveMounts[mounts.VMHomeDir].Named, true, nil
}
