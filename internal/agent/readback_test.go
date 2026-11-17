package agent_test

import (
	"slices"
	"testing"

	"github.com/inoio/agents-sandbox/internal/agent"
)

// TestReadBackPaths pins the per-harness declared read-back paths: a change to
// one of these layouts silently loses a read-back fact, so each profile's set
// is asserted explicitly.
func TestReadBackPaths(t *testing.T) {
	want := map[string][]string{
		"opencode":    {".config/opencode/*", ".local/share/opencode/auth.json"},
		"opencode2":   {".config/opencode/*", ".local/share/opencode/auth.json"},
		"codex":       {".codex/auth.json"},
		"claude-code": {".claude/settings.json"},
		"hermes":      {".hermes/config.yaml", ".hermes/.env"},
		"oh-my-pi":    {".omp/agent/config.yml", ".omp/agent/config.yaml", ".omp/agent/models.yml", ".omp/agent/.env"},
		"pi":          {".pi/agent/settings.json"},
	}
	for name, paths := range want {
		t.Run(name, func(t *testing.T) {
			a, ok := agent.Lookup(name)
			if !ok {
				t.Fatalf("Lookup(%q) not ok", name)
			}
			describer, ok := agent.AsReadBackDescriber(a)
			if !ok {
				t.Fatalf("%s must implement ReadBackDescriber", name)
			}
			if got := describer.ReadBackPaths(); !slices.Equal(got, paths) {
				t.Errorf("ReadBackPaths() = %v, want %v", got, paths)
			}
		})
	}
}

// TestCredentialProbes: opencode2 alone declares its SQLite credential probe;
// every other profile's credentials are file-based.
func TestCredentialProbes(t *testing.T) {
	a, _ := agent.Lookup("opencode2")
	describer, ok := agent.AsReadBackDescriber(a)
	if !ok {
		t.Fatal("opencode2 must implement ReadBackDescriber")
	}
	if got := describer.CredentialProbes(); !slices.Equal(got, []string{"opencode2-sqlite"}) {
		t.Errorf("opencode2 CredentialProbes() = %v, want [opencode2-sqlite]", got)
	}

	for _, name := range []string{"opencode", "codex", "claude-code", "hermes", "oh-my-pi", "pi"} {
		t.Run(name, func(t *testing.T) {
			a, _ := agent.Lookup(name)
			describer, _ := agent.AsReadBackDescriber(a)
			if got := describer.CredentialProbes(); got != nil {
				t.Errorf("%s CredentialProbes() = %v, want nil", name, got)
			}
		})
	}
}
