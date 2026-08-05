package main

import (
	"github.com/spf13/cobra"

	"gitlab.inoio.de/inoio/opencode-msb/internal/launcherconfig"
	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox"
	"gitlab.inoio.de/inoio/opencode-msb/internal/stdio"
)

// extractRunOptions extracts shared run/shell flags from the given command
// and returns a populated sandbox.RunOptions. The auto parameter controls
// whether the Auto field is set on RunOptions.
func extractRunOptions(cmd *cobra.Command, auto bool, ui stdio.UI) sandbox.RunOptions {
	opts := sandbox.RunOptions{Auto: auto}
	opts.Branch, _ = cmd.Flags().GetString("branch")
	opts.Rebuild, _ = cmd.Flags().GetBool("rebuild")
	opts.DryRun, _ = cmd.Flags().GetBool("dry-run")
	opts.DryRunVM, _ = cmd.Flags().GetBool("dry-run-vm")
	if opts.DryRun {
		opts.DryRunVM = true
		ui.Verbosef("dry-run-vm: auto-enabled (--dry-run)")
	}
	opts.CPUs, _ = cmd.Flags().GetUint8("cpus")
	opts.Memory, _ = cmd.Flags().GetString("memory")
	opts.TmpSize, _ = cmd.Flags().GetString("tmp-size")
	opts.User, _ = cmd.Flags().GetString("user")
	return opts
}

// printItems renders a list of items using the given format, item type,
// and type-specific accessors for name and value strings.
func printItems[T any](
	items []T,
	emptyMsg string,
	format string,
	nameFunc func(T) string,
	valueFunc func(T) string,
	ui stdio.UI,
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

func buildRootCmd(ui stdio.UI) *cobra.Command {
	rootCmd := buildMinimalRootFlagsCmd()

	rootCmd.PersistentPreRunE = func(cmd *cobra.Command, _ []string) error {
		cfg := newConfig()
		lc, keys, err := launcherconfig.Load(cfg.UserLauncherDir, projectConfigDir)
		if err != nil {
			return err
		}
		sandbox.AutoPrune(cmd.Context(), lc.AutoPruneAge, ui)
		if err := applyLauncherConfig(cmd, lc, keys, ui); err != nil {
			return err
		}
		return nil
	}
	rootCmd.RunE = runFunc(ui)

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

func buildTreeCmd(rootCmd *cobra.Command, ui stdio.UI) *cobra.Command {
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

func buildVersionCmd(rootCmd *cobra.Command, ui stdio.UI) *cobra.Command {
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

func printPruneSummary(ui stdio.UI, report *sandbox.StaleReport, dryRun bool) {
	action := "Pruned"
	if dryRun {
		action = "dry-run: Would prune"
	}

	ui.Outf(
		"%s %d VMs, %d home volumes, %d docker images, %d msb images, %d task sandboxes, %d clone volumes",
		action,
		report.PrunedVMs,
		report.PrunedVolumes,
		report.PrunedDockerImages,
		report.PrunedMSBImages,
		report.PrunedTaskSandboxes,
		report.PrunedCloneVolumes,
	)
	ui.Verbosef("details %d", len(report.Details))
	for _, entry := range report.Details {
		ui.Verbosef("x  %s (%s, digest=%s, type=%s)", entry.Name, entry.Slug, entry.Digest, entry.Type)
	}
}
