package main

import (
	"context"

	"github.com/spf13/cobra"

	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox"
	"gitlab.inoio.de/inoio/opencode-msb/internal/termio"
	launcherconfig "gitlab.inoio.de/inoio/opencode-msb/internal/viperconfig"
)

// launcherConfigKey is the context key type for storing the loaded
// viperconfig.Config between PersistentPreRunE and extractRunOptions.
type launcherConfigKey struct{}

// extractRunOptions extracts shared run/shell flags from the given command
// and returns a populated sandbox.RunOptions. The auto parameter controls
// whether the Auto field is set on RunOptions.
func extractRunOptions(cmd *cobra.Command, auto bool, ui termio.UI) (sandbox.RunOptions, error) {
	opts := sandbox.RunOptions{Auto: auto}
	rawWorktree, _ := cmd.Flags().GetString(flagWorktree)
	worktree, err := sandbox.ResolveWorktreeSpec(rawWorktree)
	if err != nil {
		return sandbox.RunOptions{}, err
	}
	opts.Worktree = worktree
	opts.Rebuild, _ = cmd.Flags().GetBool(flagRebuild)
	opts.DryRun, _ = cmd.Flags().GetBool(flagDryRun)
	opts.DryRunVM, _ = cmd.Flags().GetBool(flagDryRunVM)
	if opts.DryRun {
		opts.DryRunVM = true
		ui.Verbosef("dry-run-vm: auto-enabled (--dry-run)")
	}
	opts.CPUs, _ = cmd.Flags().GetUint8(flagCpus)
	opts.Memory, _ = cmd.Flags().GetString(flagMemory)
	opts.TmpSize, _ = cmd.Flags().GetString(flagTmpSize)
	opts.DiskSize, _ = cmd.Flags().GetString(flagDiskSize)
	opts.User, _ = cmd.Flags().GetString(flagUser)
	ctx := cmd.Context()
	if ctx != nil {
		if lc, ok := ctx.Value((*launcherConfigKey)(nil)).(launcherconfig.Config); ok {
			opts.ReapPolicy = lc.ReapPolicy()
			opts.IdleTimeout = lc.IdleTimeout()
		}
	}
	return opts, nil
}

// printItems renders a list of items using the given format, item type,
// and type-specific accessors for name and value strings.
func printItems[T any](
	items []T,
	emptyMsg string,
	format string,
	nameFunc func(T) string,
	valueFunc func(T) string,
	ui termio.UI,
) {
	if len(items) == 0 {
		ui.Info(emptyMsg)
		return
	}
	for _, item := range items {
		ui.Outf(format, nameFunc(item), valueFunc(item))
	}
}

func buildMinimalRootFlagsCmd() *cobra.Command {
	rootFlagsCmd := &cobra.Command{
		Use:   sandbox.Prefix,
		Short: "Run opencode inside an ephemeral microsandbox VM",
		Long: "Run opencode inside an ephemeral microsandbox VM.\n\n" +
			"When invoked without a subcommand, the \"run\" command is implied.",
	}

	rootFlagsCmd.PersistentFlags().BoolP(pFlagYes, pFlagYes[:1], false, "Assume yes to all prompts")
	rootFlagsCmd.PersistentFlags().BoolP(pFlagVerbose, pFlagVerbose[:1], false, "Show debug-level output")
	rootFlagsCmd.PersistentFlags().BoolP(pFlagQuiet, pFlagQuiet[:1], false, "Suppress non-error output")

	return rootFlagsCmd
}

func buildRootCmd(ui termio.UI) *cobra.Command {
	rootCmd := buildMinimalRootFlagsCmd()

	rootCmd.PersistentPreRunE = func(cmd *cobra.Command, _ []string) error {
		lc, keys, err := launcherconfig.Load()
		if err != nil {
			return err
		}
		cmd.SetContext(context.WithValue(cmd.Context(), (*launcherConfigKey)(nil), lc))
		if err := applyLauncherConfig(cmd, lc, keys); err != nil {
			return err
		}

		// execute Auto-Prune
		isDryRun, _ := cmd.Flags().GetBool(flagDryRun)
		sandbox.AutoPrune(cmd.Context(), lc.AutoPruneAge, isDryRun, ui)
		applyCLISettings(cmd, ui)
		return nil
	}
	extendRunCmd(ui, rootCmd)

	rootCmd.AddCommand(buildRunCmd(ui))
	rootCmd.AddCommand(buildTreeCmd(rootCmd, ui))
	rootCmd.AddCommand(buildVersionCmd(rootCmd, ui))
	rootCmd.AddCommand(buildDoctorCmd(ui))
	rootCmd.AddCommand(buildBuildCmd(ui))
	rootCmd.AddCommand(buildListCmd(ui))
	rootCmd.AddCommand(buildShellCmd(ui))
	rootCmd.AddCommand(buildConfigCmd(ui))
	rootCmd.AddCommand(buildImageCmd(ui))
	rootCmd.AddCommand(buildVolumeCmd(ui))
	rootCmd.AddCommand(buildSandboxCmd(ui))
	rootCmd.AddCommand(buildStopCmd(ui))
	rootCmd.AddCommand(buildKillCmd(ui))
	rootCmd.AddCommand(buildPruneCmd(ui))

	rootCmd.SetOut(ui.StdOut())
	rootCmd.SetErr(ui.StdErr())

	return rootCmd
}

func buildTreeCmd(rootCmd *cobra.Command, ui termio.UI) *cobra.Command {
	cmd := &cobra.Command{
		Use:   cmdTree,
		Args:  cobra.NoArgs,
		Short: "Print the full command tree",
		Run: func(_ *cobra.Command, _ []string) {
			printTree(rootCmd, ui)
		},
	}
	return cmd
}

func buildVersionCmd(rootCmd *cobra.Command, ui termio.UI) *cobra.Command {
	cmd := &cobra.Command{
		Use:   cmdVersion,
		Args:  cobra.NoArgs,
		Short: "Print version",
		Run: func(_ *cobra.Command, _ []string) {
			ui.Outf("%s %s\n", rootCmd.Name(), version)
		},
	}
	return cmd
}
