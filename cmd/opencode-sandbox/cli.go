package main

import (
	"fmt"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"gitlab.inoio.de/inoio/opencode-sandbox/internal/configpaths"
	"gitlab.inoio.de/inoio/opencode-sandbox/internal/termio"
	launcherconfig "gitlab.inoio.de/inoio/opencode-sandbox/internal/viperconfig"
)

var version = "dev"

// execute runs the CLI with the given arguments and UI.
//
// For integration testing, override the injection seams using:
//   - msb:   msb.ResetGetFn (replaces the msb.Client factory)
//   - docker:  docker.WithNoopDockerMock / docker.WithDefaultErrorDockerMock / docker.WithDockerMock
//
// Example:
//
//	func TestListSandboxCommand(t *testing.T) {
//	    orig := msb.ResetGetFn(func() msb.Client { return mock })
//	    t.Cleanup(func() { msb.ResetGetFn(orig) })
//
//	    ui := &termio.Mock{}
//	    err := Execute([]string{"list"}, ui)
//	    // ...assert...
//	}
func execute(args []string, ui termio.UI) error {
	rootCmd := buildRootCmd(ui)
	rootCmd.SetArgs(args)
	rootCmd.SetOut(ui.StdOut())
	rootCmd.SetErr(ui.StdErr())
	rootCmd.SilenceUsage = true
	rootCmd.SilenceErrors = true

	return rootCmd.Execute()
}

// applyCLISettings sets the terminal output level and assume-yes state on the UI
// based on the effective --verbose/--quiet/--yes flags of the running command.
//
// It must run after cobra parses the real command tree and after launcher
// config has been merged, so that flags work regardless of position or how
// short shorthands are grouped (e.g. "-nv").
func applyCLISettings(cmd *cobra.Command, ui termio.UI) {
	if cmd == nil {
		return
	}
	quiet, _ := cmd.Flags().GetBool(pFlagQuiet)
	verbose, _ := cmd.Flags().GetBool(pFlagVerbose)
	yes, _ := cmd.Flags().GetBool(pFlagYes)
	ui.SetLevel(levelFrom(quiet, verbose))
	ui.SetAssumeYes(yes)
}

func levelFrom(quiet, verbose bool) termio.Level {
	switch {
	case quiet:
		return termio.LevelQuiet
	case verbose:
		return termio.LevelVerbose
	default:
		return termio.LevelNormal
	}
}

func newConfig() configpaths.ConfigPaths {
	return configpaths.Get()
}

func applyLauncherConfig(cmd *cobra.Command, lc launcherconfig.Config, keys map[string]bool) error {
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
		{flagDiskSize, func() error { return setStringFlag(cmd, flagDiskSize, lc.DiskSize) }},
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
