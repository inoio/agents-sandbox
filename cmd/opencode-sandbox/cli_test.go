package main

import (
	"strings"
	"testing"

	"gitlab.inoio.de/inoio/opencode-sandbox/internal/configpaths"
	"gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/docker"
	"gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/msb"
	"gitlab.inoio.de/inoio/opencode-sandbox/internal/termio"
	launcherconfig "gitlab.inoio.de/inoio/opencode-sandbox/internal/viperconfig"
)

func TestRootHasGlobalFlags(t *testing.T) {
	testUI := termio.NewTestMock(t)
	root := buildRootCmd(&testUI)
	flags := []string{"yes", "verbose", "quiet"}
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
		"v": "verbose", "q": "quiet",
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

func TestApplyLauncherConfigSetsUnsetFlags(t *testing.T) {
	testUI := termio.NewTestMock(t)
	root := buildRootCmd(&testUI)
	runCmd, _, _ := root.Find([]string{"run"})
	if runCmd == nil {
		t.Fatal("expected run command")
	}
	lc := launcherconfig.Config{CPUs: 4, Memory: "8G", TmpSize: "4G", DiskSize: "32G", Yes: true, Verbose: true}
	keys := map[string]bool{
		"cpus":      true,
		"memory":    true,
		"tmp-size":  true,
		"disk-size": true,
		"yes":       true,
		"verbose":   true,
	}

	if err := applyLauncherConfig(runCmd, lc, keys); err != nil {
		t.Fatalf("applyLauncherConfig failed: %v", err)
	}

	cpus, _ := runCmd.Flags().GetUint8(flagCpus)
	if cpus != 4 {
		t.Errorf("expected cpus 4, got %d", cpus)
	}
	mem, _ := runCmd.Flags().GetString(flagMemory)
	if mem != "8G" {
		t.Errorf("expected memory 8G, got %q", mem)
	}
	tmp, _ := runCmd.Flags().GetString(flagTmpSize)
	if tmp != "4G" {
		t.Errorf("expected tmp-size 4G, got %q", tmp)
	}
	disk, _ := runCmd.Flags().GetString(flagDiskSize)
	if disk != "32G" {
		t.Errorf("expected disk-size 32G, got %q", disk)
	}
	yes, _ := root.PersistentFlags().GetBool(pFlagYes)
	if !yes {
		t.Error("expected yes=true")
	}
	verbose, _ := root.PersistentFlags().GetBool(pFlagVerbose)
	if !verbose {
		t.Error("expected verbose=true")
	}
}

func TestCLICombinedShortFlagsActivateVerbose(t *testing.T) {
	for _, args := range [][]string{
		{"prune", "--age", "1m", "-nv"},
		{"prune", "--age", "1m", "-n", "-v"},
		{"prune", "--age", "1m", "--dry-run", "--verbose"},
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

func TestApplyLauncherConfigRespectsCLIOverrides(t *testing.T) {
	testUI := termio.NewTestMock(t)
	root := buildRootCmd(&testUI)
	runCmd, _, _ := root.Find([]string{"run"})
	if runCmd == nil {
		t.Fatal("expected run command")
	}
	if err := runCmd.ParseFlags(
		[]string{"--cpus", "2", "--memory", "1G", "--tmp-size", "512M", "--disk-size", "16G", "--yes=false"},
	); err != nil {
		t.Fatalf("ParseFlags failed: %v", err)
	}

	lc := launcherconfig.Config{CPUs: 8, Memory: "16G", TmpSize: "8G", DiskSize: "64G", Yes: true, Verbose: true}
	keys := map[string]bool{
		"cpus":      true,
		"memory":    true,
		"tmp-size":  true,
		"disk-size": true,
		"yes":       true,
		"verbose":   true,
	}

	if err := applyLauncherConfig(runCmd, lc, keys); err != nil {
		t.Fatalf("applyLauncherConfig failed: %v", err)
	}

	cpus, _ := runCmd.Flags().GetUint8(flagCpus)
	if cpus != 2 {
		t.Errorf("expected cpus 2 (CLI override), got %d", cpus)
	}
	mem, _ := runCmd.Flags().GetString(flagMemory)
	if mem != "1G" {
		t.Errorf("expected memory 1G (CLI override), got %q", mem)
	}
	tmp, _ := runCmd.Flags().GetString(flagTmpSize)
	if tmp != "512M" {
		t.Errorf("expected tmp-size 512M (CLI override), got %q", tmp)
	}
	disk, _ := runCmd.Flags().GetString(flagDiskSize)
	if disk != "16G" {
		t.Errorf("expected disk-size 16G (CLI override), got %q", disk)
	}
	yes, _ := runCmd.Flags().GetBool(pFlagYes)
	if yes {
		t.Error("expected yes=false (CLI override)")
	}
	verbose, _ := runCmd.Flags().GetBool(pFlagVerbose)
	if !verbose {
		t.Error("expected verbose=true from config")
	}
}

func TestNewConfigSetsUserDirs(t *testing.T) {
	t.Setenv("HOME", "/testhome")
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("XDG_CACHE_HOME", "")
	t.Setenv("XDG_STATE_HOME", "")
	configpaths.WithRealConfigPaths(t)
	cfg := newConfig()
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
	cfg := newConfig()
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

func TestApplyLauncherConfigSetsDiskSize(t *testing.T) {
	testUI := termio.NewTestMock(t)
	root := buildRootCmd(&testUI)
	runCmd, _, _ := root.Find([]string{"run"})
	if runCmd == nil {
		t.Fatal("expected run command")
	}
	keys := map[string]bool{"disk-size": true}
	lc := launcherconfig.Config{DiskSize: "32G"}
	if err := applyLauncherConfig(runCmd, lc, keys); err != nil {
		t.Fatalf("applyLauncherConfig failed: %v", err)
	}
	got, _ := runCmd.Flags().GetString(flagDiskSize)
	if got != "32G" {
		t.Errorf("disk-size = %q, want 32G", got)
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
