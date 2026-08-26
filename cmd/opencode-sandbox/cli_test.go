package main

import (
	"context"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/inoio/opencode-sandbox/internal/configpaths"
	"github.com/inoio/opencode-sandbox/internal/sandbox/docker"
	"github.com/inoio/opencode-sandbox/internal/sandbox/msb"
	"github.com/inoio/opencode-sandbox/internal/termio"
	"github.com/inoio/opencode-sandbox/internal/testutil"
	launcherconfig "github.com/inoio/opencode-sandbox/internal/viperconfig"
)

func TestRootHasGlobalFlags(t *testing.T) {
	testUI := termio.NewTestMock(t)
	root := buildRootCmd(&testUI)
	flags := []string{"yes", "log-level", "quiet"}
	for _, f := range flags {
		if root.PersistentFlags().Lookup(f) == nil {
			t.Errorf("expected persistent flag --%s on root", f)
		}
	}
}

func TestRunCommandHasExpectedFlags(t *testing.T) {
	testUI := termio.NewTestMock(t)
	root := buildRootCmd(&testUI)
	runCmd, _, _ := root.Find([]string{"run"})
	if runCmd == nil {
		t.Fatal("expected run command")
	}
	flags := []string{"worktree", "cpus", "memory", "tmp-size", "disk-size", "rebuild", "dry-run"}
	for _, f := range flags {
		if runCmd.Flags().Lookup(f) == nil {
			t.Errorf("expected flag --%s on run command", f)
		}
	}
}

func TestRunCommandFlagShortcuts(t *testing.T) {
	testUI := termio.NewTestMock(t)
	root := buildRootCmd(&testUI)
	runCmd, _, _ := root.Find([]string{"run"})
	if runCmd == nil {
		t.Fatal("expected run command")
	}
	shortcuts := map[string]string{
		"w": "worktree", "c": "cpus", "m": "memory",
		"r": "rebuild", "n": "dry-run", "y": "yes",
		"l": "log-level", "q": "quiet",
	}
	for short, long := range shortcuts {
		f := runCmd.Flags().ShorthandLookup(short)
		if f == nil {
			f = root.PersistentFlags().ShorthandLookup(short)
		}
		if f == nil {
			t.Errorf("expected shorthand -%s for --%s", short, long)
		}
	}
}

func TestImageBuildNounFormExists(t *testing.T) {
	testUI := termio.NewTestMock(t)
	root := buildRootCmd(&testUI)
	imageCmd, _, _ := root.Find([]string{"image"})
	if imageCmd == nil {
		t.Fatal("expected image command")
	}
	buildCmd, _, _ := imageCmd.Find([]string{"build"})
	if buildCmd == nil {
		t.Fatal("expected image build subcommand")
	}
}

func TestCLILogLevelFlagSetsLevel(t *testing.T) {
	for _, args := range [][]string{
		{"prune", "--age", "1m", "--log-level", "verbose"},
		{"prune", "--age", "1m", "-l", "verbose"},
	} {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			configpaths.WithMockConfigPaths(t)
			ui := &termio.Mock{}
			mock := &msb.MockMsbClient{}
			msb.WithMsbMock(t, mock)
			docker.WithNoopDockerMock(t)

			root := buildRootCmd(ui)
			root.SetArgs(args)

			if err := root.Execute(); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if ui.Level() != termio.LevelVerbose {
				t.Errorf("expected LevelVerbose, got %v", ui.Level())
			}
		})
	}
}

func TestCLIPersistentYesAffectsUIAfterSubcommand(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := &termio.Mock{}
	mock := &msb.MockMsbClient{}
	msb.WithMsbMock(t, mock)
	docker.WithNoopDockerMock(t)

	root := buildRootCmd(ui)
	root.SetArgs([]string{"prune", "--age", "1m", "-n", "-y"})

	if err := root.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ui.AssumeYes() {
		t.Error("expected AssumeYes=true for -y after subcommand")
	}
}

func TestCLIQuietFlagSetsQuiet(t *testing.T) {
	for _, args := range [][]string{
		{"prune", "--age", "1m", "--quiet"},
		{"prune", "--age", "1m", "-q"},
	} {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			configpaths.WithMockConfigPaths(t)
			ui := &termio.Mock{}
			mock := &msb.MockMsbClient{}
			msb.WithMsbMock(t, mock)
			docker.WithNoopDockerMock(t)

			root := buildRootCmd(ui)
			root.SetArgs(args)

			if err := root.Execute(); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !ui.Quiet() {
				t.Errorf("expected Quiet=true, got %v", ui.Quiet())
			}
		})
	}
}

func TestCLIQuietFlagFalseByDefault(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := &termio.Mock{}
	mock := &msb.MockMsbClient{}
	msb.WithMsbMock(t, mock)
	docker.WithNoopDockerMock(t)

	root := buildRootCmd(ui)
	root.SetArgs([]string{"prune", "--age", "1m"})

	if err := root.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ui.Quiet() {
		t.Error("expected Quiet=false by default")
	}
}

func TestNewConfigSetsUserDirs(t *testing.T) {
	t.Setenv("HOME", "/testhome")
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("XDG_CACHE_HOME", "")
	t.Setenv("XDG_STATE_HOME", "")
	configpaths.WithRealConfigPaths(t)
	cfg := configpaths.Get()
	if cfg.UserStateDir() != "/testhome/.local/state/opencode-sandbox" {
		t.Errorf("unexpected state dir: %q", cfg.UserStateDir())
	}
	if cfg.UserConfigDir() != "/testhome/.config/opencode-sandbox" {
		t.Errorf("unexpected user config dir: %q", cfg.UserConfigDir())
	}
	if cfg.UserOpencodeConfigDir() != "/testhome/.config/opencode-sandbox/opencode" {
		t.Errorf("unexpected opencode config dir: %q", cfg.UserOpencodeConfigDir())
	}
	if cfg.UserCacheDir() != "/testhome/.cache/opencode-sandbox" {
		t.Errorf("unexpected cache dir: %q", cfg.UserCacheDir())
	}
}

func TestNewConfigHonorsXdgEnv(t *testing.T) {
	t.Setenv("HOME", "/testhome")
	t.Setenv("XDG_CONFIG_HOME", "/xdg/config")
	t.Setenv("XDG_CACHE_HOME", "/xdg/cache")
	t.Setenv("XDG_STATE_HOME", "/xdg/state")
	configpaths.WithRealConfigPaths(t)
	cfg := configpaths.Get()
	if cfg.UserStateDir() != "/xdg/state/opencode-sandbox" {
		t.Errorf("unexpected state dir: %q", cfg.UserStateDir())
	}
	if cfg.UserConfigDir() != "/xdg/config/opencode-sandbox" {
		t.Errorf("unexpected user config dir: %q", cfg.UserConfigDir())
	}
	if cfg.UserOpencodeConfigDir() != "/xdg/config/opencode-sandbox/opencode" {
		t.Errorf("unexpected opencode config dir: %q", cfg.UserOpencodeConfigDir())
	}
	if cfg.UserCacheDir() != "/xdg/cache/opencode-sandbox" {
		t.Errorf("unexpected cache dir: %q", cfg.UserCacheDir())
	}
}

func TestShellRootFlag(t *testing.T) {
	testUI := termio.NewTestMock(t)
	root := buildRootCmd(&testUI)

	shellCmd, _, _ := root.Find([]string{"shell"})
	if shellCmd == nil {
		t.Fatal("expected shell command")
	}
	rootFlag := shellCmd.Flags().Lookup(flagRoot)
	if rootFlag == nil {
		t.Fatal("expected --root flag on shell")
	}
	if rootFlag.Shorthand != "" {
		t.Errorf("--root must have no short form, got -%s", rootFlag.Shorthand)
	}
	if err := shellCmd.ParseFlags([]string{"--root"}); err != nil {
		t.Fatalf("ParseFlags --root failed: %v", err)
	}
	if got, _ := shellCmd.Flags().GetBool(flagRoot); !got {
		t.Errorf("--root should be true, got false")
	}

	runCmd, _, _ := root.Find([]string{"run"})
	if runCmd == nil {
		t.Fatal("expected run command")
	}
	if runCmd.Flags().Lookup(flagRoot) != nil {
		t.Error("run must not have a --root flag")
	}
	if runCmd.Flags().Lookup("user") != nil {
		t.Error("run must not have a --user flag")
	}
	if shellCmd.Flags().Lookup("user") != nil {
		t.Error("shell must not have a --user flag")
	}
}

func TestStopCommandExists(t *testing.T) {
	testUI := termio.NewTestMock(t)
	root := buildRootCmd(&testUI)
	stopCmd, _, _ := root.Find([]string{"stop"})
	if stopCmd == nil {
		t.Fatal("expected stop command")
	}
}

func TestKillCommandExists(t *testing.T) {
	testUI := termio.NewTestMock(t)
	root := buildRootCmd(&testUI)
	killCmd, _, _ := root.Find([]string{"kill"})
	if killCmd == nil {
		t.Fatal("expected kill command")
	}
}

func TestStopCommandHasForceFlag(t *testing.T) {
	testUI := termio.NewTestMock(t)
	root := buildRootCmd(&testUI)
	stopCmd, _, _ := root.Find([]string{"stop"})
	if stopCmd == nil {
		t.Fatal("expected stop command")
	}
	if stopCmd.Flags().Lookup("force") == nil {
		t.Error("expected --force flag on stop command")
	}
}

func TestKillCommandHasForceFlag(t *testing.T) {
	testUI := termio.NewTestMock(t)
	root := buildRootCmd(&testUI)
	killCmd, _, _ := root.Find([]string{"kill"})
	if killCmd == nil {
		t.Fatal("expected kill command")
	}
	if killCmd.Flags().Lookup("force") == nil {
		t.Error("expected --force flag on kill command")
	}
}

func TestCLIConfigPrecedenceViaResolver(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()
	testutil.WriteYAML(t, cp.UserConfigDir(), "config.yaml", map[string]any{"cpus": 2, "memory": "8G"})

	ui := &termio.Mock{}
	root := buildRootCmd(ui)
	runCmd, _, _ := root.Find([]string{"run"})
	if err := runCmd.ParseFlags([]string{"--cpus", "6"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	rootCtx := context.WithValue(context.Background(), (*launcherConfigKey)(nil), mustResolver(t, runCmd))
	runCmd.SetContext(rootCtx)

	opts, err := extractRunOptions(runCmd, ui)
	if err != nil {
		t.Fatalf("extractRunOptions: %v", err)
	}
	if opts.CPUs != 6 {
		t.Errorf("CPUs = %d; want 6 (flag overrides config)", opts.CPUs)
	}
	if opts.Memory != "8G" {
		t.Errorf("Memory = %q; want 8G (config, flag unspecified)", opts.Memory)
	}
}

// mustResolver builds a resolver from a real command, failing the test on error.
func mustResolver(t *testing.T, cmd *cobra.Command) *launcherconfig.Resolver {
	t.Helper()
	r, err := launcherconfig.NewResolver(cmd, "")
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	return r
}
