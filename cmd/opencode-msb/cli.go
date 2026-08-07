package main

import (
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"gitlab.inoio.de/inoio/opencode-msb/internal/launcherconfig"
	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox"
	"gitlab.inoio.de/inoio/opencode-msb/internal/termio"
)

var version = "dev"

// Execute runs the CLI with the given arguments and UI.
//
// For integration testing, override the injection seams using:
//   - sandbox: sandbox.SetNewMsbClient (replaces the MsbClient factory)
//   - docker:  docker.WithNoopDockerMock / docker.WithDefaultErrorDockerMock / docker.WithDockerMock
//
// Example:
//
//	func TestListSandboxCommand(t *testing.T) {
//	    orig := sandbox.SetNewMsbClient(func() sandbox.MsbClient { return mock })
//	    t.Cleanup(func() { sandbox.SetNewMsbClient(orig) })
//
//	    ui := &termio.Mock{}
//	    err := Execute([]string{"list"}, ui)
//	    // ...assert...
//	}
func Execute(args []string, ui termio.UI) error {
	rootCmd := buildRootCmd(ui)
	rootCmd.SetArgs(args)
	rootCmd.SetOut(ui.StdOut())
	rootCmd.SetErr(ui.StdErr())

	return rootCmd.Execute()
}

func getIOLevel(root *cobra.Command) termio.Level {
	verbose, _ := root.Flags().GetBool(pFlagVerbose)
	quiet, _ := root.Flags().GetBool(pFlagQuiet)
	level := termio.LevelNormal
	if quiet {
		level = termio.LevelQuiet
	} else if verbose {
		level = termio.LevelVerbose
	}
	return level
}

func newUI(args []string) termio.UI {
	minimalCmd := buildMinimalRootFlagsCmd()
	// We don't care about errors, just parse the minimal flags for UI initialization
	_ = minimalCmd.ParseFlags(args)
	yes, _ := minimalCmd.Flags().GetBool(pFlagYes)
	level := getIOLevel(minimalCmd)

	return termio.New(os.Stdin, os.Stdout, os.Stderr,
		term.IsTerminal(int(os.Stderr.Fd())), level, yes)
}

func newConfig() sandbox.Config {
	return sandbox.Config{
		UserStateDir:  sandbox.XdgStateDir(),
		UserConfigDir: sandbox.XdgConfigDir(),
		UserCacheDir:  sandbox.XdgCacheDir(),
	}
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
