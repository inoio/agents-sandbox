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
		"opencode":     {".config/opencode/*", ".local/share/opencode/auth.json"},
		"opencode2":    {".config/opencode/*", ".local/share/opencode/auth.json"},
		"codex":        {".codex/auth.json"},
		"claude-code":  {".claude/settings.json"},
		"hermes":       {".hermes/config.yaml", ".hermes/.env"},
		"kimi-code":    {".kimi-code/config.toml", ".kimi-code/mcp.json", ".kimi-code/credentials/*.json"},
		"qwen-code":    {".qwen/oauth_creds.json", ".qwen/mcp-oauth-tokens.json"},
		"goose":        nil,
		"grok-build":   {".grok/auth.json"},
		"mistral-vibe": {".vibe/.env"},
		"oh-my-pi":     {".omp/agent/config.yml", ".omp/agent/config.yaml", ".omp/agent/models.yml", ".omp/agent/.env"},
		"pi":           {".pi/agent/settings.json", ".pi/agent/auth.json"},
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

	for _, name := range []string{"opencode", "codex", "claude-code", "hermes", "kimi-code", "oh-my-pi", "pi", "qwen-code", "goose", "grok-build", "mistral-vibe"} {
		t.Run(name, func(t *testing.T) {
			a, _ := agent.Lookup(name)
			describer, _ := agent.AsReadBackDescriber(a)
			if got := describer.CredentialProbes(); got != nil {
				t.Errorf("%s CredentialProbes() = %v, want nil", name, got)
			}
		})
	}
}

// readBacklessProfile implements Agent but not ReadBackDescriber.
type readBacklessProfile struct{}

func (readBacklessProfile) Name() string               { return "readbackless" }
func (readBacklessProfile) ConfigDirName() string      { return "readbackless" }
func (readBacklessProfile) Experimental() bool         { return false }
func (readBacklessProfile) ImageSpec() agent.ImageSpec { return agent.ImageSpec{} }

// TestAsReadBackDescriberRejectsAnAgentWithoutTheCapability: an Agent that does
// not implement ReadBackDescriber yields a nil describer and ok false, so a
// caller can tell the capability is absent.
func TestAsReadBackDescriberRejectsAnAgentWithoutTheCapability(t *testing.T) {
	describer, ok := agent.AsReadBackDescriber(readBacklessProfile{})

	if ok {
		t.Fatal("AsReadBackDescriber ok = true, want false for an agent without the capability")
	}
	if describer != nil {
		t.Errorf("AsReadBackDescriber describer = %v, want nil", describer)
	}
}
