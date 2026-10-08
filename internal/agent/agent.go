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
//
//exhaustruct:ignore
type WorktreeSpec struct {
	Name   string
	Base   string
	Target string
}

// builtinRegistry returns the built-in agent profiles keyed by canonical name.
func builtinRegistry() map[string]Agent {
	profiles := []Agent{
		opencodeProfile{opencodeConfig: opencodeConfig{}},
		opencode2Profile{opencodeConfig: opencodeConfig{}},
		claudeCodeProfile{},
		piProfile{},
	}
	registry := make(map[string]Agent, len(profiles))
	for _, a := range profiles {
		registry[a.Name()] = a
	}
	return registry
}

// registry holds the built-in agent profiles, plus any added via Register.
var registry = builtinRegistry()

// Register adds an agent profile to the registry, overriding a built-in of the
// same name.
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
