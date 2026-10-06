package main

import (
	"testing"

	"github.com/inoio/agents-sandbox/internal/agent"
)

func TestRunCommandHasNoOpenCodeVersionFlag(t *testing.T) {
	cmd, _ := setupCommandFixtures(t, cmdRun, "--help")
	foundCmd, _, err := cmd.Find([]string{cmdRun})
	if err != nil {
		t.Fatalf("Find %q: %v", cmdRun, err)
	}
	if flag := foundCmd.Flags().Lookup(flagOpenCodeVersion); flag != nil {
		t.Errorf("run command must NOT have --opencode-version flag, got %q", flag.Name)
	}
}

func TestRunAndShellHaveDindFlag(t *testing.T) {
	for _, name := range []string{cmdRun, cmdShell} {
		cmd, _ := setupCommandFixtures(t, name, "--help")
		foundCmd, _, err := cmd.Find([]string{name})
		if err != nil {
			t.Fatalf("Find %q: %v", name, err)
		}
		if flag := foundCmd.Flags().Lookup(flagDind); flag == nil {
			t.Errorf("%s command must have --dind flag", name)
		}
	}
}

func TestRunAndShellHaveDNSFlag(t *testing.T) {
	for _, name := range []string{cmdRun, cmdShell} {
		cmd, _ := setupCommandFixtures(t, name, "--help")
		foundCmd, _, err := cmd.Find([]string{name})
		if err != nil {
			t.Fatalf("Find %q: %v", name, err)
		}
		if flag := foundCmd.Flags().Lookup(flagDNSServers); flag == nil {
			t.Errorf("%s command must have --dns flag", name)
		}
	}
}

// noDaemonAgent is a minimal agent.Agent that intentionally does not implement
// DaemonProvider, so agent-support checks can be exercised without polluting
// the global agent registry.
type noDaemonAgent struct{ name string }

func (a noDaemonAgent) Name() string             { return a.name }
func (noDaemonAgent) ConfigDirName() string      { return "nod" }
func (noDaemonAgent) ImageSpec() agent.ImageSpec { return agent.ImageSpec{} }
