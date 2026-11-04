package main

import (
	"testing"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/runoptions"
	"github.com/inoio/agents-sandbox/internal/termio"
	launcherconfig "github.com/inoio/agents-sandbox/internal/viperconfig"
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

func TestRunAndShellHaveDockerFlags(t *testing.T) {
	for _, name := range []string{cmdRun, cmdShell} {
		cmd, _ := setupCommandFixtures(t, name, "--help")
		foundCmd, _, err := cmd.Find([]string{name})
		if err != nil {
			t.Fatalf("Find %q: %v", name, err)
		}
		if flag := foundCmd.Flags().Lookup(flagDocker); flag == nil {
			t.Errorf("%s command must have --docker flag", name)
		}
		if flag := foundCmd.Flags().Lookup(flagDind); flag == nil {
			t.Errorf("%s command must keep the deprecated --dind alias", name)
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

func TestRunAndShellHaveComposableNetworkFlag(t *testing.T) {
	for _, name := range []string{cmdRun, cmdShell} {
		cmd, _ := setupCommandFixtures(t, name, "--help")
		foundCmd, _, err := cmd.Find([]string{name})
		if err != nil {
			t.Fatalf("Find %q: %v", name, err)
		}
		flag := foundCmd.Flags().Lookup(flagNetwork)
		if flag == nil {
			t.Fatalf("%s command must have --network flag", name)
		}
		if flag.Value.Type() != "stringSlice" {
			t.Errorf("%s --network type = %q, want stringSlice (comma-separated or repeated)", name, flag.Value.Type())
		}
	}
}

func TestRunAndShellHavePublishFlag(t *testing.T) {
	for _, name := range []string{cmdRun, cmdShell} {
		cmd, _ := setupCommandFixtures(t, name, "--help")
		foundCmd, _, err := cmd.Find([]string{name})
		if err != nil {
			t.Fatalf("Find %q: %v", name, err)
		}
		flag := foundCmd.Flags().Lookup(flagPort)
		if flag == nil {
			t.Errorf("%s command must have --port flag", name)
			continue
		}
		if flag.Shorthand != "p" {
			t.Errorf("%s --port shorthand = %q, want p", name, flag.Shorthand)
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
func (noDaemonAgent) Experimental() bool         { return false }

// TestBuildInvalidNotifyFlag exercises the --notify flag override
// error branch in runoptions.Build (an invalid value passed directly on the
// flag, not via the env var), with a nil resolver.
func TestBuildInvalidNotifyFlag(t *testing.T) {
	ui := &termio.Mock{}
	cmd := buildRunCmd(ui)
	if err := cmd.Flags().Set(flagNotify, "loud"); err != nil {
		t.Fatalf("set notify: %v", err)
	}
	var r *launcherconfig.Resolver
	if _, err := runoptions.Build(r.Config(), cmd, ui); err == nil {
		t.Fatal("expected error for invalid --notify flag value")
	}
}
