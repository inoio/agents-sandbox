package agent_test

import (
	"slices"
	"testing"

	"github.com/inoio/agents-sandbox/internal/agent"
)

// TestInstructionPaths pins each profile's instruction-file allowlist: the
// first-run host seed copies exactly these files and trees, so a widened entry
// is a reviewed change. hermes and oh-my-pi keep no instruction files in their
// config dir and declare none.
func TestInstructionPaths(t *testing.T) {
	opencodePaths := []string{"AGENTS.md", "agent/", "agents/", "command/", "commands/", "skill/", "skills/"}
	want := map[string][]string{
		"claude-code": {"CLAUDE.md", "commands/", "agents/", "skills/"},
		"codex":       {"AGENTS.md", "AGENTS.override.md", "skills/"},
		"pi": {
			"AGENTS.md", "AGENTS.MD", "AGENTS.override.md", "CLAUDE.md", "CLAUDE.MD",
			"SYSTEM.md", "APPEND_SYSTEM.md", "prompts/", "skills/",
		},
		"opencode":     opencodePaths,
		"opencode2":    opencodePaths,
		"qwen-code":    {"QWEN.md", "AGENTS.md", "commands/", "skills/", "agents/", "workflows/"},
		"mistral-vibe": {"AGENTS.md", "agents/", "prompts/", "skills/"},
		"goose":        {"AGENTS.md", "prompts/"},
		"grok-build":   {"AGENTS.md", "Agents.md", "AGENT.md", "CLAUDE.md", "Claude.md", "skills/", "workflows/"},
		"kimi-code":    {"AGENTS.md"},
	}
	for _, name := range agent.Names() {
		t.Run(name, func(t *testing.T) {
			a, ok := agent.Lookup(name)
			if !ok {
				t.Fatalf("Lookup(%q) not ok", name)
			}
			seeder, ok := agent.AsInstructionSeeder(a)
			paths, declared := want[name]
			if ok != declared {
				t.Fatalf("AsInstructionSeeder(%s) ok = %v, want %v", name, ok, declared)
			}
			if !ok {
				return
			}
			if got := seeder.InstructionPaths(); !slices.Equal(got, paths) {
				t.Errorf("InstructionPaths() = %v, want %v", got, paths)
			}
		})
	}
}

// TestInstructionPathsAreRelativeToAConfigMergersDir pins that every seeder
// also merges config: its paths are relative to the VM config dir the
// config-dir mirror copies into, which only a ConfigMerger declares.
func TestInstructionPathsAreRelativeToAConfigMergersDir(t *testing.T) {
	for _, name := range agent.Names() {
		a, _ := agent.Lookup(name)
		if _, ok := agent.AsInstructionSeeder(a); !ok {
			continue
		}
		if _, ok := agent.AsConfigMerger(a); !ok {
			t.Errorf("%s declares instruction paths but no ConfigMerger", name)
		}
	}
}

func TestAsInstructionSeederRejectsAnAgentWithoutTheCapability(t *testing.T) {
	seeder, ok := agent.AsInstructionSeeder(readBacklessProfile{})

	if ok || seeder != nil {
		t.Errorf("AsInstructionSeeder = (%v, %v), want (nil, false)", seeder, ok)
	}
}
