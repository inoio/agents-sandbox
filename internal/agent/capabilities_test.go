package agent_test

import (
	"testing"

	"github.com/inoio/agents-sandbox/internal/agent"
)

// opencode must expose a distinct WorktreeProvider capability, separate from
// DaemonProvider.
func TestOpencodeImplementsWorktreeProvider(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	if _, ok := agent.AsWorktreeProvider(a); !ok {
		t.Fatal("opencode should implement WorktreeProvider")
	}
	if _, ok := agent.AsDaemonProvider(a); !ok {
		t.Fatal("opencode should implement DaemonProvider")
	}
}

func TestOnlyOpencodeImplementsEventStreamProvider(t *testing.T) {
	for _, name := range agent.Names() {
		a, _ := agent.Lookup(name)
		_, ok := agent.AsEventStreamProvider(a)
		if name == "opencode" {
			if !ok {
				t.Errorf("opencode should implement EventStreamProvider")
			}
			continue
		}
		if ok {
			t.Errorf("%s should not implement EventStreamProvider", name)
		}
	}
}

func TestMigrationSpecDeclaresOAuthAsLoginType(t *testing.T) {
	for _, name := range []string{"opencode", "opencode2", "pi"} {
		a, ok := agent.Lookup(name)
		if !ok {
			t.Fatalf("agent %q not registered", name)
		}
		spec := a.(agent.MigrationSpecProvider).MigrationSpec()
		if _, ok := spec.Auth.AuthFields["oauth"]; ok {
			t.Errorf("%s still lists oauth in AuthFields", name)
		}
		types := spec.Auth.LoginAuthTypes
		if len(types) != 1 || types[0] != "oauth" {
			t.Errorf("%s LoginAuthTypes = %v; want [oauth]", name, types)
		}
	}
}

func TestOpenCodeMigrationCapability(t *testing.T) {
	a, ok := agent.Lookup("opencode")
	if !ok {
		t.Fatal("opencode agent not registered")
	}
	provider, ok := agent.AsMigrationSpecProvider(a)
	if !ok {
		t.Fatal("opencode should implement MigrationSpecProvider")
	}
	spec := provider.MigrationSpec()
	if spec.NativeConfigEnv != "XDG_CONFIG_HOME" || spec.NativeDataEnv != "XDG_DATA_HOME" {
		t.Errorf("migration XDG envs = %q, %q", spec.NativeConfigEnv, spec.NativeDataEnv)
	}
}
