package agent_test

import (
	"testing"

	"github.com/inoio/agents-sandbox/internal/agent"
)

// TestEnvIndirectionKey pins which harness declares a native env indirection and
// the key it reads an environment-variable name from: kimi reads no key from the
// process environment, so the write-back must emit its "api_key_env" form rather
// than a ${VAR} placeholder (#749).
func TestEnvIndirectionKey(t *testing.T) {
	a, ok := agent.Lookup("kimi-code")
	if !ok {
		t.Fatal("Lookup(\"kimi-code\") not ok")
	}
	describer, ok := agent.AsEnvIndirectionDescriber(a)
	if !ok {
		t.Fatal("kimi-code must implement EnvIndirectionDescriber")
	}
	if got := describer.EnvIndirectionKey(); got != "api_key_env" {
		t.Errorf("EnvIndirectionKey() = %q, want %q", got, "api_key_env")
	}
}

// envIndirectionlessProfile implements Agent but not EnvIndirectionDescriber.
type envIndirectionlessProfile struct{}

func (envIndirectionlessProfile) Name() string               { return "indirectionless" }
func (envIndirectionlessProfile) ConfigDirName() string      { return "indirectionless" }
func (envIndirectionlessProfile) Experimental() bool         { return false }
func (envIndirectionlessProfile) ImageSpec() agent.ImageSpec { return agent.ImageSpec{} }

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
