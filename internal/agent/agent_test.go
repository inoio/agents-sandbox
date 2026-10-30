package agent_test

import (
	"testing"

	"github.com/inoio/agents-sandbox/internal/agent"
)

func TestLookupDefaultIsOpenCode2(t *testing.T) {
	a, ok := agent.Lookup("")
	if !ok {
		t.Fatal("Lookup(\"\") returned not-ok")
	}
	if a.Name() != agent.DefaultAgentName {
		t.Errorf("Lookup(\"\") name = %q, want %q", a.Name(), agent.DefaultAgentName)
	}
}

func TestLookupOpencodeExplicit(t *testing.T) {
	if _, ok := agent.Lookup("opencode"); !ok {
		t.Error("Lookup(\"opencode\") = not-ok")
	}
}

func TestLookupUnknown(t *testing.T) {
	if _, ok := agent.Lookup("bogus"); ok {
		t.Error("Lookup(\"bogus\") = ok, want not-ok")
	}
}

func TestNamesIncludesOpencode(t *testing.T) {
	names := agent.Names()
	found := false
	for _, n := range names {
		if n == "opencode" {
			found = true
		}
	}
	if !found {
		t.Errorf("Names() = %v, want to include opencode", names)
	}
}

func TestOpencodeConfigDirName(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	if a.ConfigDirName() != "opencode" {
		t.Errorf("ConfigDirName = %q, want opencode", a.ConfigDirName())
	}
}

func TestImageSpecDerivationHelpers(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	spec := a.ImageSpec()
	if spec.VersionArg != "OPENCODE_VERSION" {
		t.Errorf("VersionArg = %q, want derived OPENCODE_VERSION", spec.VersionArg)
	}
	if _, ok := spec.AgentEnv["OPENCODE_DISABLE_AUTOUPDATE"]; !ok {
		t.Errorf("AgentEnv = %v, want OPENCODE_DISABLE_AUTOUPDATE key", spec.AgentEnv)
	}
}

func TestOpencodeImplementsDaemon(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	if _, ok := agent.AsDaemonProvider(a); !ok {
		t.Error("opencode should implement DaemonProvider")
	}
}

func TestExperimentalAgentSet(t *testing.T) {
	experimental := map[string]bool{
		"pi":          true,
		"claude-code": true,
		"codex":       true,
		"hermes":      true,
	}
	stable := map[string]bool{
		"opencode":  true,
		"opencode2": true,
	}

	names := agent.Names()
	classified := map[string]bool{}
	for _, name := range names {
		a, ok := agent.Lookup(name)
		if !ok {
			t.Fatalf("Lookup(%q) = not-ok", name)
		}
		want, known := experimental[name]
		if !known {
			if !stable[name] {
				t.Errorf("agent %q is in neither the experimental nor the stable set", name)
				continue
			}
			want = false
		}
		classified[name] = true
		if got := a.Experimental(); got != want {
			t.Errorf("agent %q Experimental() = %v, want %v", name, got, want)
		}
	}
	for name := range experimental {
		if !classified[name] {
			t.Errorf("experimental agent %q is not registered", name)
		}
	}
	for name := range stable {
		if !classified[name] {
			t.Errorf("stable agent %q is not registered", name)
		}
	}
}

func TestOpencodeImageSpec(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	spec := a.ImageSpec()
	if spec.VersionArg != "OPENCODE_VERSION" {
		t.Errorf("VersionArg = %q, want OPENCODE_VERSION", spec.VersionArg)
	}
	if _, ok := spec.AgentEnv["OPENCODE_DISABLE_AUTOUPDATE"]; !ok {
		t.Errorf("AgentEnv = %v, want OPENCODE_DISABLE_AUTOUPDATE key", spec.AgentEnv)
	}
	if spec.InstallCommand == "" {
		t.Error("InstallCommand must not be empty")
	}
}
