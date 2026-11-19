package main

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/firstrun"
	"github.com/inoio/agents-sandbox/internal/runoptions"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb/msbtest"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/termio"
	"github.com/inoio/agents-sandbox/internal/termio/termiotest"
	"github.com/inoio/agents-sandbox/internal/testutil"
	launcherconfig "github.com/inoio/agents-sandbox/internal/viperconfig"
)

func TestRunCommandHasNoOpenCodeVersionFlag(t *testing.T) {
	cmd, _ := setupCommandFixtures(t, cmdRun, "--help")
	foundCmd, _, err := cmd.Find([]string{cmdRun})
	if err != nil {
		t.Fatalf("Find %q: %v", cmdRun, err)
	}
	if flag := foundCmd.Flags().Lookup("opencode-version"); flag != nil {
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

// TestRunCommandHasLearnFlag pins the run-only --learn flag: it is a run flag
// (like --serve-only) and is absent from shell.
func TestRunCommandHasLearnFlag(t *testing.T) {
	runCmd, _ := setupCommandFixtures(t, cmdRun, "--help")
	found, _, err := runCmd.Find([]string{cmdRun})
	if err != nil {
		t.Fatalf("Find %q: %v", cmdRun, err)
	}
	if flag := found.Flags().Lookup(flagLearn); flag == nil {
		t.Errorf("run command must have --learn flag")
	}

	shellCmd, _ := setupCommandFixtures(t, cmdShell, "--help")
	foundShell, _, err := shellCmd.Find([]string{cmdShell})
	if err != nil {
		t.Fatalf("Find %q: %v", cmdShell, err)
	}
	if flag := foundShell.Flags().Lookup(flagLearn); flag != nil {
		t.Errorf("shell command must NOT have --learn flag, got %q", flag.Name)
	}
}

func TestRunAndShellHaveProxyFlag(t *testing.T) {
	for _, name := range []string{cmdRun, cmdShell} {
		cmd, _ := setupCommandFixtures(t, name, "--help")
		foundCmd, _, err := cmd.Find([]string{name})
		if err != nil {
			t.Fatalf("Find %q: %v", name, err)
		}
		if flag := foundCmd.Flags().Lookup(flagProxy); flag == nil {
			t.Errorf("%s command must have --proxy flag", name)
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

// TestRunNotifySpaceFormInvalidValueRejected pins that a value spelling the
// flag documents is consumed from the space form and rejected with the same
// error as --notify=<invalid>. --notify OFF matches the mode case-insensitively
// and --notify false is a boolean literal: both are taken as the value, then
// rejected by ParseOverride, instead of pflag taking the implicit "on" and
// leaking the word into the agent argv.
func TestRunNotifySpaceFormInvalidValueRejected(t *testing.T) {
	for _, value := range []string{"OFF", "false"} {
		t.Run(value, func(t *testing.T) {
			initTestProjectDir(t)

			mock := &msbtest.MockMsbClient{}
			sb := &msbtest.MockSandbox{}
			_, ui := setupRunMocks(t, mock, sb, cmdRun)

			err := execute([]string{cmdRun, "--notify", value}, ui)
			if err == nil || !strings.Contains(err.Error(), `invalid --notify value "`+value+`"`) {
				t.Fatalf("execute run --notify %s error = %v, want the invalid value rejected", value, err)
			}
		})
	}
}

// stubFirstRunMenu replaces the first-run menu with a no-op for the test, so an
// interactive run/shell test is not interrupted by the first-run setup prompt.
func stubFirstRunMenu(t *testing.T) {
	t.Helper()
	original := firstRunMenu
	firstRunMenu = func(termio.UI, *options.RunOptions, string, string) error { return nil }
	t.Cleanup(func() { firstRunMenu = original })
}

// captureSessionRun replaces the session-start seam with one that records the
// options the cmd boundary hands the session, so a test can assert the menu's
// outcome reaches the options session.Run actually consumes.
func captureSessionRun(t *testing.T) func() options.RunOptions {
	t.Helper()
	var captured options.RunOptions
	original := sessionRun
	sessionRun = func(_ context.Context, opts options.RunOptions, _ options.Mode, _ termio.UI) error {
		captured = opts
		return nil
	}
	t.Cleanup(func() { sessionRun = original })
	return func() options.RunOptions { return captured }
}

// TestRunFirstRunMenuSetsLearn pins Review Focus 6: the outcome the first-run
// menu applies reaches the very options session.Run consumes, not a copy. The
// menu is stubbed so the test needs no host config or state dir.
func TestRunFirstRunMenuSetsLearn(t *testing.T) {
	initTestProjectDir(t)

	original := firstRunMenu
	firstRunMenu = func(_ termio.UI, opts *options.RunOptions, _, _ string) error {
		opts.Learn = true
		return nil
	}
	t.Cleanup(func() { firstRunMenu = original })

	captured := captureSessionRun(t)
	root, _ := setupRunMocks(t, &msbtest.MockMsbClient{}, &msbtest.MockSandbox{}, cmdRun)
	if err := root.Execute(); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !captured().Learn {
		t.Error("session.Run did not see opts.Learn set by the first-run menu")
	}
}

// TestRunFirstRunMenuSeedsFromHostAndStartsLearning drives the PRODUCTION
// first-run seam end to end for UC4: with a host config the agent's provision
// rules select, the "learn from my host setup" choice must seed the per-slug
// managed scope AND start a learning session. It is the only test covering the
// seed call inside the production seam, so dropping or reordering that call
// fails here rather than only in the manual accept.
func TestRunFirstRunMenuSeedsFromHostAndStartsLearning(t *testing.T) {
	initTestProjectDir(t)

	captured := captureSessionRun(t)
	root, ui := setupRunMocks(
		t, &msbtest.MockMsbClient{}, &msbtest.MockSandbox{}, cmdRun, "--agent", "codex",
	)

	paths := configpaths.Get()
	hostHome, err := paths.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir: %v", err)
	}
	hostConfigDir := filepath.Join(hostHome, ".codex")
	if err := os.MkdirAll(hostConfigDir, 0o755); err != nil {
		t.Fatalf("create host config dir: %v", err)
	}
	testutil.WriteFile(t, hostConfigDir, "config.toml", "model = \"gpt-5\"\n")

	slug := effectiveConfigSlugForTest(t, mustIdentity(t, "."))

	ui.IsInteractiveResult = true
	ui.SelectFn = func(string, []termio.Choice, string) (string, error) {
		return string(firstrun.ChoiceLearnFromHost), nil
	}

	if err := root.Execute(); err != nil {
		t.Fatalf("run: %v", err)
	}

	seeded := filepath.Join(paths.UserSlugConfigDir(slug), "codex", "config.toml")
	if _, err := os.Stat(seeded); err != nil {
		t.Errorf("the host-seed choice must write the per-slug managed config %s: %v", seeded, err)
	}
	if !captured().Learn {
		t.Error("session.Run did not see opts.Learn for the host-seed choice")
	}
}

// TestRunFirstRunMenuSkippedNonInteractive pins invariant 6 at the cmd boundary:
// a non-interactive run runs the production menu (against mocked config and
// state), which offers nothing and warns, so session.Run sees no learning.
func TestRunFirstRunMenuSkippedNonInteractive(t *testing.T) {
	initTestProjectDir(t)

	captured := captureSessionRun(t)
	root, ui := setupRunMocks(t, &msbtest.MockMsbClient{}, &msbtest.MockSandbox{}, cmdRun)
	if err := root.Execute(); err != nil {
		t.Fatalf("run: %v", err)
	}
	if captured().Learn {
		t.Error("a non-interactive run must not start a learning session")
	}
	if !slices.ContainsFunc(ui.WarnCalls, func(w string) bool { return strings.Contains(w, "first-run") }) {
		t.Errorf("warnings = %v, want a non-interactive first-run warning", ui.WarnCalls)
	}
}

// TestRunNotifySpaceFormPlainWordForwarded pins the reconciliation end to end:
// a plain word after --notify is not a value spelling, so it stays a positional
// argument -- the watcher keeps the bare --notify "on" and "fix the bug" reaches
// the agent argv, as docs/commands.md documents for non-flag arguments.
func TestRunNotifySpaceFormPlainWordForwarded(t *testing.T) {
	initTestProjectDir(t)

	mock := &msbtest.MockMsbClient{}
	sb := &msbtest.MockSandbox{}
	_, ui := setupRunMocks(t, mock, sb, cmdRun)

	if err := execute([]string{cmdRun, "--notify", "fix", "the", "bug"}, ui); err != nil {
		t.Fatalf("execute run --notify fix the bug: %v", err)
	}
	forwarded := strings.Join(sb.AttachArgs, " ")
	for _, word := range []string{"'fix'", "'the'", "'bug'"} {
		if !strings.Contains(forwarded, word) {
			t.Errorf("AttachArgs = %q, want the plain words forwarded to the agent", sb.AttachArgs)
		}
	}
}
