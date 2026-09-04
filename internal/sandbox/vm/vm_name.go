package vm

import (
	"github.com/inoio/agents-sandbox/internal/sandbox/naming"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
)

// projectVMName generates the VM name from the project/agent key. Long slugs
// are truncated while the agent suffix is preserved, so the name always
// round-trips back to the same agent.
func projectVMName(k state.Key) string {
	name := naming.VmPrefix + k.Slug + "-" + k.Agent
	if len(name) <= options.MaxSandboxNameLen {
		return name
	}
	over := len(name) - options.MaxSandboxNameLen
	slugLen := max(len(k.Slug)-over, 0)
	name = naming.VmPrefix + k.Slug[:slugLen] + "-" + k.Agent
	if len(name) > options.MaxSandboxNameLen { // extreme case: agent itself too long
		return name[:options.MaxSandboxNameLen]
	}
	return name
}
