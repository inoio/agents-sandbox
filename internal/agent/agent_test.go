package agent_test

import (
	"testing"

	"github.com/inoio/opencode-sandbox/internal/agent"
)

func TestLookupDefaultIsOpencode(t *testing.T) {
	a, ok := agent.Lookup("")
	if !ok {
		t.Fatal("Lookup(\"\") returned not-ok")
	}
	if a.Name() != "opencode" {
		t.Errorf("Lookup(\"\") name = %q, want opencode", a.Name())
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
