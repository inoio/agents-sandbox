package vm

import (
	"context"
	"fmt"

	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/naming"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
)

// projectVMName generates the prefix-free VM name from the project/agent key.
// Long slugs are truncated while the agent suffix is preserved, so the name
// always round-trips back to the same agent.
func projectVMName(k state.Key) string {
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

// findProjectVM looks up the project VM for a key under the new name first, then
// the legacy prefixed name, so a VM created before the naming change is still
// found and reused rather than recreated. found is false when neither exists.
func findProjectVM(ctx context.Context, client msb.Client, k state.Key) (msb.SandboxHandle, bool, error) {
	handle, err := client.GetSandbox(ctx, projectVMName(k))
	if err == nil {
		return handle, true, nil
	}
	if !msb.IsNotFound(err) {
		return nil, false, fmt.Errorf("get sandbox %q: %w", projectVMName(k), err)
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
