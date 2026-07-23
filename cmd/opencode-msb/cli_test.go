package main

import (
	"strings"
	"testing"

	"gitlab.inoio.de/inoio/opencode-msb/internal/launcherconfig"
)

func TestIsKnownSubcommandRecognizesRegisteredCommands(t *testing.T) {
	root := buildRootCmd()
	tests := []struct {
		arg  string
		want bool
	}{
		{"run", true},
		{"doctor", true},
		{"build", true},
		{"list", true},
		{"ls", true},
		{"shell", true},
		{"config", true},
		{"image", true},
		{"volume", true},
		{"sandbox", true},
		{"help", true},
		{"--help", true},
		{"-h", true},
		{"--tree", true},
		{"--version", true},
		{"-V", true},
		{"unknown-cmd", false},
		{"--branch", false},
		{"-b", false},
	}
	for _, tt := range tests {
		t.Run(tt.arg, func(t *testing.T) {
			if got := isKnownSubcommand(tt.arg, root); got != tt.want {
				t.Errorf("isKnownSubcommand(%q) = %v, want %v", tt.arg, got, tt.want)
			}
		})
	}
}

func TestPrintTreeContainsAllCommands(t *testing.T) {
	root := buildRootCmd()
	var sb strings.Builder
	printTree(&sb, root, "")
	out := sb.String()
	expected := []string{"run", "doctor", "build", "list", "shell", "config", "image", "volume"}
	for _, cmd := range expected {
		if !strings.Contains(out, cmd) {
			t.Errorf("expected tree to contain %q, got:\n%s", cmd, out)
		}
	}
}

func TestRootHasGlobalFlags(t *testing.T) {
	root := buildRootCmd()
	flags := []string{"yes", "verbose", "quiet", "tree", "version"}
	for _, f := range flags {
		if root.PersistentFlags().Lookup(f) == nil {
			t.Errorf("expected persistent flag --%s on root", f)
		}
	}
}

func TestRunCommandHasExpectedFlags(t *testing.T) {
	root := buildRootCmd()
	runCmd, _, _ := root.Find([]string{"run"})
	if runCmd == nil {
		t.Fatal("expected run command")
	}
	flags := []string{"branch", "cpus", "memory", "rebuild", "dry-run", "no-auto"}
	for _, f := range flags {
		if runCmd.Flags().Lookup(f) == nil {
			t.Errorf("expected flag --%s on run command", f)
		}
	}
}

func TestRunCommandFlagShortcuts(t *testing.T) {
	root := buildRootCmd()
	runCmd, _, _ := root.Find([]string{"run"})
	if runCmd == nil {
		t.Fatal("expected run command")
	}
	shortcuts := map[string]string{
		"b": "branch", "c": "cpus", "m": "memory",
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
	root := buildRootCmd()
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
	root := buildRootCmd()
	runCmd, _, _ := root.Find([]string{"run"})
	if runCmd == nil {
		t.Fatal("expected run command")
	}
	lc := launcherconfig.Config{CPUs: 4, Memory: "8G", Yes: true, Verbose: true}
	keys := map[string]bool{"cpus": true, "memory": true, "yes": true, "verbose": true}

	if err := applyLauncherConfig(runCmd, lc, keys); err != nil {
		t.Fatalf("applyLauncherConfig failed: %v", err)
	}

	cpus, _ := runCmd.Flags().GetUint8("cpus")
	if cpus != 4 {
		t.Errorf("expected cpus 4, got %d", cpus)
	}
	mem, _ := runCmd.Flags().GetString("memory")
	if mem != "8G" {
		t.Errorf("expected memory 8G, got %q", mem)
	}
	yes, _ := root.PersistentFlags().GetBool("yes")
	if !yes {
		t.Error("expected yes=true")
	}
	verbose, _ := root.PersistentFlags().GetBool("verbose")
	if !verbose {
		t.Error("expected verbose=true")
	}
}

func TestApplyLauncherConfigRespectsCLIOverrides(t *testing.T) {
	root := buildRootCmd()
	runCmd, _, _ := root.Find([]string{"run"})
	if runCmd == nil {
		t.Fatal("expected run command")
	}
	if err := runCmd.ParseFlags([]string{"--cpus", "2", "--memory", "1G", "--yes=false"}); err != nil {
		t.Fatalf("ParseFlags failed: %v", err)
	}

	lc := launcherconfig.Config{CPUs: 8, Memory: "16G", Yes: true, Verbose: true}
	keys := map[string]bool{"cpus": true, "memory": true, "yes": true, "verbose": true}

	if err := applyLauncherConfig(runCmd, lc, keys); err != nil {
		t.Fatalf("applyLauncherConfig failed: %v", err)
	}

	cpus, _ := runCmd.Flags().GetUint8("cpus")
	if cpus != 2 {
		t.Errorf("expected cpus 2 (CLI override), got %d", cpus)
	}
	mem, _ := runCmd.Flags().GetString("memory")
	if mem != "1G" {
		t.Errorf("expected memory 1G (CLI override), got %q", mem)
	}
	yes, _ := runCmd.Flags().GetBool("yes")
	if yes {
		t.Error("expected yes=false (CLI override)")
	}
	verbose, _ := runCmd.Flags().GetBool("verbose")
	if !verbose {
		t.Error("expected verbose=true from config")
	}
}

func TestNewConfigSetsUserLauncherDir(t *testing.T) {
	t.Setenv("HOME", "/testhome")
	cfg := newConfig()
	if cfg.StateDir != "/testhome/.local/state/opencode-msb" {
		t.Errorf("unexpected state dir: %q", cfg.StateDir)
	}
	if cfg.UserConfigDir != "/testhome/.config/opencode-msb/opencode" {
		t.Errorf("unexpected user config dir: %q", cfg.UserConfigDir)
	}
	if cfg.UserLauncherDir != "/testhome/.config/opencode-msb" {
		t.Errorf("unexpected user launcher dir: %q", cfg.UserLauncherDir)
	}
}
