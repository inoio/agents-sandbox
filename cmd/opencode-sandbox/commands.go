package main

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/naming"
	"gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/options"
	"gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/pruning"
	"gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/session"
	"gitlab.inoio.de/inoio/opencode-sandbox/internal/termio"
	launcherconfig "gitlab.inoio.de/inoio/opencode-sandbox/internal/viperconfig"
)

// launcherConfigKey is the context key type for storing the built
// viperconfig.Resolver between PersistentPreRunE and command RunE.
type launcherConfigKey struct{}

// extractRunOptions extracts shared run/shell flags from the given command
// and returns a populated options.RunOptions.
func extractRunOptions(cmd *cobra.Command, ui termio.UI) (options.RunOptions, error) {
	opts := options.RunOptions{}
	rawWorktree, _ := cmd.Flags().GetString(flagWorktree)
	worktree, err := session.ResolveWorktreeSpec(rawWorktree)
	if err != nil {
		return options.RunOptions{}, err
	}
	opts.Worktree = worktree
	opts.Rebuild, _ = cmd.Flags().GetBool(flagRebuild)
	opts.DryRun, _ = cmd.Flags().GetBool(flagDryRun)
	opts.DryRunVM, _ = cmd.Flags().GetBool(flagDryRunVM)
	if opts.DryRun {
		opts.DryRunVM = true
		ui.Verbosef("dry-run-vm: auto-enabled (--dry-run)")
	}
	opts.ServeOnly, _ = cmd.Flags().GetBool(flagServeOnly)
	if cmd.Flags().Lookup(flagRoot) != nil {
		opts.Root, _ = cmd.Flags().GetBool(flagRoot)
	}

	r := resolverFromContext(cmd.Context())
	if r != nil {
		opts.CPUs = r.CPUs()
		opts.Memory = r.Memory()
		opts.TmpSize = r.TmpSize()
		opts.DiskSize = r.DiskSize()
		opts.ReapPolicy = options.NewReapPolicy(r.AutoStopOnActiveSessions(), r.AutoStopMaxSessionRetries())
		opts.IdleTimeout = r.IdleTimeout()
	}

	if opts.TmpSize != "" {
		if _, ok := options.ParseMemoryOK(opts.TmpSize); !ok {
			return options.RunOptions{}, fmt.Errorf(
				"invalid --tmp-size %q: expected a size like 4G, 512M, or 2048",
				opts.TmpSize,
			)
		}
	}
	if opts.DiskSize != "" {
		if _, ok := options.ParseMemoryOK(opts.DiskSize); !ok {
			return options.RunOptions{}, fmt.Errorf(
				"invalid --disk-size %q: expected a size like 16G, 512M, or 4096",
				opts.DiskSize,
			)
		}
	}
	return opts, nil
}

// resolverFromContext returns the viperconfig.Resolver stored on the context,
// or nil if absent.
func resolverFromContext(ctx context.Context) *launcherconfig.Resolver {
	if ctx == nil {
		return nil
	}
	r, _ := ctx.Value((*launcherConfigKey)(nil)).(*launcherconfig.Resolver)
	return r
}

// printItems renders a list of items using the given format string with one
// verb per accessor, item type, and type-specific accessors for each column.
func printItems[T any](
	items []T,
	emptyMsg string,
	format string,
	ui termio.UI,
	funcs ...func(T) string,
) {
	if len(items) == 0 {
		ui.Info(emptyMsg)
		return
	}
	for _, item := range items {
		args := make([]any, len(funcs))
		for i, f := range funcs {
			args[i] = f(item)
		}
		ui.Outf(format, args...)
	}
}

func buildMinimalRootFlagsCmd() *cobra.Command {
	rootFlagsCmd := &cobra.Command{
		Use:   naming.Prefix,
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
		r, err := launcherconfig.NewResolver(cmd)
		if err != nil {
			return err
		}
		cmd.SetContext(context.WithValue(cmd.Context(), (*launcherConfigKey)(nil), r))
		applyCLISettings(cmd, ui, r)

		isDryRun, _ := cmd.Flags().GetBool(flagDryRun)
		pruning.AutoPrune(cmd.Context(), r.AutoPruneAge(), isDryRun, ui)
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
