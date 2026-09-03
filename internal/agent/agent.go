// Package agent provides built-in coding-agent profiles. The active agent is
// selected by name; optional capabilities are discovered by type assertion, so
// agents without a capability simply lack it and degrade gracefully.
package agent

import "sort"

// opencodeName is the canonical registry name of the default agent.
const opencodeName = "opencode"

// Agent is a built-in coding agent profile. It carries the stable identity and
// the structured bits needed to bake the agent into the runner image.
type Agent interface {
	// Name is the canonical id, e.g. "opencode", "pi".
	Name() string
	// ConfigDirName is the subdirectory under the tool's config dir holding
	// this agent's snippet files, e.g. "opencode".
	ConfigDirName() string
	// ImageSpec returns the bits used to bake the agent into the runner image.
	ImageSpec() ImageSpec
}

// WorktreeSpec describes a git worktree to create in the VM. It is a minimal,
// local type so the agent package does not depend on internal/sandbox/options.
type WorktreeSpec struct {
	Name   string
	Base   string
	Target string
}

//nolint:gochecknoglobals // built-in agent registry, populated once at init
var registry = map[string]Agent{}

// Register adds an agent profile to the registry. Built-ins call this in init.
func Register(a Agent) {
	registry[a.Name()] = a
}

// Lookup returns the agent profile for name. An empty name or "opencode"
// resolves to the default opencode profile, preserving zero-config behavior.
func Lookup(name string) (Agent, bool) {
	if name == "" {
		name = opencodeName
	}
	a, ok := registry[name]
	return a, ok
}

// Names returns the canonical names of all registered agents, sorted.
func Names() []string {
	names := make([]string, 0, len(registry))
	for n := range registry {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
