package agent_test

import (
	"testing"

	"github.com/inoio/agents-sandbox/internal/agent"
)

// nativeIndirections are the harnesses that declare a native env indirection and
// the key each reads an environment-variable name from: neither expands ${VAR}
// in its config, so the write-back and the host seed emit that form instead
// (kimi: #749; codex: #873).
var nativeIndirections = map[string]string{
	"kimi-code": "api_key_env",
	"codex":     "env_key",
}

// TestEnvIndirectionKey pins each declared native env indirection.
func TestEnvIndirectionKey(t *testing.T) {
	for name, want := range nativeIndirections {
		a, ok := agent.Lookup(name)
		if !ok {
			t.Fatalf("Lookup(%q) not ok", name)
		}
		describer, ok := agent.AsEnvIndirectionDescriber(a)
		if !ok {
			t.Fatalf("%s must implement EnvIndirectionDescriber", name)
		}
		if got := describer.EnvIndirectionKey(); got != want {
			t.Errorf("%s: EnvIndirectionKey() = %q, want %q", name, got, want)
		}
	}
}

// envIndirectionlessProfile implements Agent but not EnvIndirectionDescriber.
type envIndirectionlessProfile struct{}

func (envIndirectionlessProfile) Name() string               { return "indirectionless" }
func (envIndirectionlessProfile) ConfigDirName() string      { return "indirectionless" }
func (envIndirectionlessProfile) Experimental() bool         { return false }
func (envIndirectionlessProfile) ImageSpec() agent.ImageSpec { return agent.ImageSpec{} }

// TestEnvIndirectionIsDeclaredOnlyWhereNeeded pins that no other built-in
// profile implements EnvIndirectionDescriber, so the write-back keeps the
// default ${VAR} form for every other harness.
func TestEnvIndirectionIsDeclaredOnlyWhereNeeded(t *testing.T) {
	for _, name := range agent.Names() {
		if _, declared := nativeIndirections[name]; declared {
			continue
		}
		t.Run(name, func(t *testing.T) {
			a, ok := agent.Lookup(name)
			if !ok {
				t.Fatalf("Lookup(%q) not ok", name)
			}
			if _, ok := agent.AsEnvIndirectionDescriber(a); ok {
				t.Errorf("%s implements EnvIndirectionDescriber; only %v declare one", name, nativeIndirections)
			}
		})
	}
}

// TestAsEnvIndirectionDescriberRejectsAnAgentWithoutTheCapability: an Agent that
// does not implement EnvIndirectionDescriber yields a nil describer and ok false,
// so a caller emits the default ${VAR} form.
func TestAsEnvIndirectionDescriberRejectsAnAgentWithoutTheCapability(t *testing.T) {
	describer, ok := agent.AsEnvIndirectionDescriber(envIndirectionlessProfile{})

	if ok {
		t.Fatal("AsEnvIndirectionDescriber ok = true, want false for an agent without the capability")
	}
	if describer != nil {
		t.Errorf("AsEnvIndirectionDescriber describer = %v, want nil", describer)
	}
}
