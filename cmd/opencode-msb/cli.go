package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"gitlab.inoio.de/inoio/opencode-msb/internal/launcherconfig"
	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox"
	"gitlab.inoio.de/inoio/opencode-msb/internal/stdio"
)

var version = "dev"

// Execute runs the CLI with the given arguments and UI.
//
// For integration testing, override factory variables:
//   - sandbox packages: sandbox.NewMsbClient (same pattern as prune_client_test.go)
//   - this package: newDockerClient (for prune command mock injection)
//
// Example:
//
//	func TestListSandboxCommand(t *testing.T) {
//	    old := sandbox.NewMsbClient
//	    sandbox.NewMsbClient = func() sandbox.MsbClient { return mock }
//	    t.Cleanup(func() sandbox.NewMsbClient = old )
//
//	    ui := stdio.NewMock(t)
//	    err := Execute([]string{"list"}, ui)
//	    // ...assert...
//	}
func Execute(args []string, ui stdio.UI) error {
	rootCmd := buildRootCmd(ui)
	rootCmd.SetArgs(args)
	rootCmd.SetOut(ui.StdOut())
	rootCmd.SetErr(ui.StdErr())

	return rootCmd.Execute()
}

func getIOLevel(root *cobra.Command) stdio.Level {
	verbose, _ := root.Flags().GetBool(pFlagVerbose)
	quiet, _ := root.Flags().GetBool(pFlagQuiet)
	level := stdio.LevelNormal
	if quiet {
		level = stdio.LevelQuiet
	} else if verbose {
		level = stdio.LevelVerbose
	}
	return level
}

func newUI(args []string) stdio.UI {
	minimalCmd := buildMinimalRootFlagsCmd()
	// We don't care about errors, just parse the minimal flags for UI initialization
	_ = minimalCmd.ParseFlags(args)
	yes, _ := minimalCmd.Flags().GetBool(pFlagYes)
	level := getIOLevel(minimalCmd)

	return stdio.New(os.Stdin, os.Stdout, os.Stderr,
		term.IsTerminal(int(os.Stderr.Fd())), level, yes)
}

func newConfig() sandbox.Config {
	home, _ := os.UserHomeDir()
	return sandbox.Config{
		StateDir:        filepath.Join(home, ".local", "state", "opencode-msb"),
		UserConfigDir:   filepath.Join(home, ".config", "opencode-msb", "opencode"),
		UserLauncherDir: filepath.Join(home, ".config", "opencode-msb"),
	}
}

func applyLauncherConfig(cmd *cobra.Command, lc launcherconfig.Config, keys map[string]bool, _ stdio.UI) error {
	apply := []struct {
		key string
		fn  func() error
	}{
		{pFlagYes, func() error { return setBoolFlag(cmd, pFlagYes, lc.Yes) }},
		{pFlagVerbose, func() error { return setBoolFlag(cmd, pFlagVerbose, lc.Verbose) }},
		{pFlagQuiet, func() error { return setBoolFlag(cmd, pFlagQuiet, lc.Quiet) }},
		{flagRebuild, func() error { return setBoolFlag(cmd, flagRebuild, lc.Rebuild) }},
		{flagCpus, func() error { return setUint8Flag(cmd, flagCpus, lc.CPUs) }},
		{flagMemory, func() error { return setStringFlag(cmd, flagMemory, lc.Memory) }},
		{flagTmpSize, func() error { return setStringFlag(cmd, flagTmpSize, lc.TmpSize) }},
		{"manual-prune-age", func() error { return setDurationFlag(cmd, "age", lc.ManualPruneAge) }},
	}
	for _, item := range apply {
		if keys[item.key] {
			if err := item.fn(); err != nil {
				return fmt.Errorf("apply launcher config %q: %w", item.key, err)
			}
		}
	}
	return nil
}

func setBoolFlag(cmd *cobra.Command, name string, val bool) error {
	f := cmd.Flags().Lookup(name)
	if f == nil {
		f = cmd.InheritedFlags().Lookup(name)
	}
	if f == nil || f.Changed {
		return nil
	}
	return f.Value.Set(strconv.FormatBool(val))
}

func setUint8Flag(cmd *cobra.Command, name string, val uint8) error {
	f := cmd.Flags().Lookup(name)
	if f == nil {
		f = cmd.InheritedFlags().Lookup(name)
	}
	if f == nil || f.Changed {
		return nil
	}
	return f.Value.Set(strconv.FormatUint(uint64(val), 10))
}

func setStringFlag(cmd *cobra.Command, name string, val string) error {
	f := cmd.Flags().Lookup(name)
	if f == nil {
		f = cmd.InheritedFlags().Lookup(name)
	}
	if f == nil || f.Changed || val == "" {
		return nil
	}
	return f.Value.Set(val)
}

func setDurationFlag(cmd *cobra.Command, name string, val time.Duration) error {
	f := cmd.Flags().Lookup(name)
	if f == nil {
		f = cmd.InheritedFlags().Lookup(name)
	}
	if f == nil || f.Changed || val == 0 {
		return nil
	}
	return f.Value.Set(val.String())
}
