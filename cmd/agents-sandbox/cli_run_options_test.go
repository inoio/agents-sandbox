package main

import (
	"slices"
	"strings"
	"testing"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/runoptions"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb/msbtest"
	"github.com/inoio/agents-sandbox/internal/termio/termiotest"
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

// The image-cache-from/image-cache-to keys are env/config-only: the derived
// bind set includes them, so binding would find a flag if one existed. Assert
// the commands that consume them register none, so the keys cannot be set via a
// CLI flag that would shadow the env/config value.
func TestImageCacheKeysHaveNoCLIFlag(t *testing.T) {
	for _, name := range []string{cmdRun, cmdShell, cmdBuild} {
		cmd, _ := setupCommandFixtures(t, name, "--help")
		foundCmd, _, err := cmd.Find([]string{name})
		if err != nil {
			t.Fatalf("Find %q: %v", name, err)
		}
		for _, key := range []string{"image-cache-from", "image-cache-to"} {
			if flag := foundCmd.Flags().Lookup(key); flag != nil {
				t.Errorf("%s command must NOT have --%s flag, got %q", name, key, flag.Name)
			}
		}
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

// TestBuildInvalidNotifyFlag pins that an invalid --notify value given on the
// flag (not through the env var) is rejected with the accepted values.
func TestBuildInvalidNotifyFlag(t *testing.T) {
	ui := &termiotest.Mock{}
	cmd := buildRunCmd(ui)
	if err := cmd.Flags().Set(flagNotify, "loud"); err != nil {
		t.Fatalf("set notify: %v", err)
	}
	var r *launcherconfig.Resolver
	_, err := runoptions.Build(r.Config(), cmd, ui)
	if err == nil || !strings.Contains(err.Error(), `invalid --notify value "loud" (want on, off, desktop, or audio)`) {
		t.Fatalf("error = %v, want the invalid --notify value rejected", err)
	}
}

// TestRunNotifySpaceFormOffDisablesWatcherAndLeaksNothing pins F18 end to end:
// the space form `--notify off` must disable the notify watcher exactly like
// `--notify=off` and must not leak the word into the agent argv. It runs
// through execute, the only path that normalizes the space form before pflag
// parses (pflag's NoOptDefVal would otherwise leave the watcher on and forward
// "off" as a positional argument).
func TestRunNotifySpaceFormOffDisablesWatcherAndLeaksNothing(t *testing.T) {
	initTestProjectDir(t)

	mock := &msbtest.MockMsbClient{}
	sb := &msbtest.MockSandbox{}
	_, ui := setupRunMocks(t, mock, sb, cmdRun)

	if err := execute([]string{cmdRun, "--notify", "off"}, ui); err != nil {
		t.Fatalf("execute run --notify off: %v", err)
	}

	watcherStayedOff := slices.ContainsFunc(ui.VerboseCalls, func(line string) bool {
		return strings.Contains(line, "not starting notify watcher") && strings.Contains(line, "active false")
	})
	if !watcherStayedOff {
		t.Errorf("expected the notify watcher to stay off; verbose calls: %q", ui.VerboseCalls)
	}
	if strings.Contains(strings.Join(sb.AttachArgs, " "), "'off'") {
		t.Errorf("AttachArgs = %q, want the space-form value not to reach the agent argv", sb.AttachArgs)
	}
}

// TestRunNotifySpaceFormInvalidValueRejected pins the out-of-vocabulary case of
// the space form: --notify <invalid> must be rejected with the same error as
// --notify=<invalid>, instead of pflag taking the implicit "on" (watcher stays
// active) and leaking the word into the agent argv.
func TestRunNotifySpaceFormInvalidValueRejected(t *testing.T) {
	initTestProjectDir(t)

	mock := &msbtest.MockMsbClient{}
	sb := &msbtest.MockSandbox{}
	_, ui := setupRunMocks(t, mock, sb, cmdRun)

	err := execute([]string{cmdRun, "--notify", "bogus"}, ui)
	if err == nil || !strings.Contains(err.Error(), `invalid --notify value "bogus"`) {
		t.Fatalf("execute run --notify bogus error = %v, want the invalid value rejected", err)
	}
}
