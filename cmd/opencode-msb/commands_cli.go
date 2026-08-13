package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox/session"
	"gitlab.inoio.de/inoio/opencode-msb/internal/termio"
)

const minUsagePadding = 25

func buildRunCmd(ui termio.UI) *cobra.Command {
	cmd := &cobra.Command{
		Use:   cmdRun,
		Short: "Run opencode in a microsandbox VM",
	}
	return extendRunCmd(ui, cmd)
}

func extendRunCmd(ui termio.UI, cmd *cobra.Command) *cobra.Command {
	cmd.Args = cobra.ArbitraryArgs
	cmd.Annotations = map[string]string{
		annotationArgs:   `[{"name":"[-- OPENCODE_ARG...]","help":"Arguments forwarded to opencode (use -- to separate from launcher flags)"}]`,
		annotationAlsoAs: "sandbox run",
	}
	cmd.RunE = runFunc(ui)
	cmd.SetUsageFunc(runUsageFunc)

	registerRunFlags(cmd)

	return cmd
}

func runUsageFunc(cmd *cobra.Command) error {
	var out strings.Builder

	out.WriteString("Usage:\n  ")
	out.WriteString(cmd.UseLine())

	// Compute max width for alignment.
	maxLen := minUsagePadding
	args := argsFromAnnotations(cmd)
	for _, a := range args {
		//nolint:staticcheck // using strings.Builder, false positive
		out.WriteString(fmt.Sprintf(" %s", a.Name))
		if len(a.Name) > maxLen {
			maxLen = len(a.Name)
		}
	}
	out.WriteString("\n")

	for _, a := range args {
		out.WriteString("\n  ")
		out.WriteString(a.Name)
		out.WriteString(strings.Repeat(" ", maxLen-len(a.Name)+2))
		out.WriteString(a.Help)
	}

	if cmd.Flags().HasFlags() {
		out.WriteString("\n\nFlags:\n")
		out.WriteString(cmd.Flags().FlagUsages())
	}

	// Additional help topics.
	for _, subcmd := range cmd.Commands() {
		if subcmd.IsAdditionalHelpTopicCommand() {
			out.WriteString("\n\nAdditional help topics:")
			out.WriteString("\n  ")
			out.WriteString(rpad(subcmd.CommandPath(), minUsagePadding))
			out.WriteString(" ")
			out.WriteString(subcmd.Short)
		}
	}

	if cmd.HasAvailableSubCommands() {
		out.WriteString("\n\nUse \"")
		out.WriteString(cmd.CommandPath())
		out.WriteString(" [command] --help\" for more information about a command.")
	}
	out.WriteString("\n")

	_, err := io.WriteString(cmd.OutOrStdout(), out.String())
	return err
}

func rpad(s string, padding int) string {
	return s + strings.Repeat(" ", padding-len(s))
}

func runFunc(ui termio.UI) func(cmd *cobra.Command, args []string) error {
	return func(cmd *cobra.Command, args []string) error {
		opts, err := extractRunOptions(cmd, ui)
		if err != nil {
			return err
		}
		opts.Args = args

		ctx := cmd.Context()
		if opts.ServeOnly {
			ctx, _ = serveOnlyContext(ctx)
		}
		return session.Run(ctx, opts, ui)
	}
}

// serveOnlyContext builds a cancellable context for the serve-only path.
// It wires SIGINT/SIGTERM and stdin EOF (Ctrl-D) to cancel the context,
// so runServeOnly can exit cleanly and trigger proper teardown
// (lease release, keeper cancel, reaping, exit code 0).
func serveOnlyContext(base context.Context) (context.Context, func()) {
	ctx, stop := signal.NotifyContext(base, os.Interrupt, syscall.SIGTERM)
	cancelCtx, cancel := context.WithCancel(ctx)
	go func() {
		_, _ = io.Copy(io.Discard, os.Stdin)
		cancel()
	}()
	return cancelCtx, func() {
		stop()
		cancel()
	}
}

func buildShellCmd(ui termio.UI) *cobra.Command {
	cmd := &cobra.Command{
		Use:     cmdShell,
		Aliases: cmdShellAliases,
		Short:   "Start sandbox and open a shell (debug)",
		Annotations: map[string]string{
			annotationAlsoAs: "sandbox shell",
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			opts, err := extractRunOptions(cmd, ui)
			if err != nil {
				return err
			}
			return session.Shell(cmd.Context(), opts, ui)
		},
	}
	registerSharedRunShellFlags(cmd)
	return cmd
}

// registerRunFlags registers run-specific flags, then the shared run/shell flags,
// on the given command.
func registerRunFlags(cmd *cobra.Command) {
	cmd.Flags().
		StringP(flagWorktree, "w", "", "Run in an isolated opencode worktree named <name>, optionally starting from the local base ref <name>:<base>")
	cmd.Flags().
		BoolP(flagServeOnly, "s", false, "Serve opencode on the host port (http://127.0.0.1:4096) for clients like Opencode Desktop, without attaching the TUI")
	registerSharedRunShellFlags(cmd)
}

func registerSharedRunShellFlags(cmd *cobra.Command) {
	cmd.Flags().BoolP(flagRebuild, flagRebuild[:1], false, "Rebuild the runner image before starting")
	cmd.Flags().BoolP(flagDryRun, flagDryRunShort, false, "Dry run without starting anything")
	cmd.Flags().Bool(flagDryRunVM, false, "Skip VM lifecycle but prepare everything else")
	cmd.Flags().Uint8P(flagCpus, flagCpus[:1], 0, "Number of CPUs (default: all)")
	cmd.Flags().StringP(flagMemory, flagMemory[:1], "4G", "Memory limit")
	cmd.Flags().String(flagTmpSize, "2G", "Size of the /tmp tmpfs in the sandbox")
	cmd.Flags().String(flagDiskSize, "", "Size of the project VM root disk (e.g. 16G)")
	cmd.Flags().
		StringP(flagUser, flagUser[:1], "", "Username or UID for the runtime user (format: <name|uid>[:<group|gid>])")
}

func buildStopCmd(ui termio.UI) *cobra.Command {
	cmd := &cobra.Command{
		Use:   cmdStop,
		Args:  cobra.NoArgs,
		Short: "Stop the project VM",
		Annotations: map[string]string{
			annotationAlsoAs: "sandbox stop",
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			force, _ := cmd.Flags().GetBool(flagForce)
			dryRun, _ := cmd.Flags().GetBool(flagDryRun)
			return session.StopProjectVM(cmd.Context(), force, dryRun, ui)
		},
	}
	cmd.Flags().BoolP(flagForce, flagForce[:1], false, "Remove the VM's persisted state after stopping")
	cmd.Flags().BoolP(flagDryRun, flagDryRunShort, false, "Show what would be stopped without stopping")
	return cmd
}

func buildKillCmd(ui termio.UI) *cobra.Command {
	cmd := &cobra.Command{
		Use:   cmdKill,
		Args:  cobra.NoArgs,
		Short: "Force-kill the project VM",
		Annotations: map[string]string{
			annotationAlsoAs: "sandbox kill",
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			force, _ := cmd.Flags().GetBool(flagForce)
			dryRun, _ := cmd.Flags().GetBool(flagDryRun)
			return session.KillProjectVM(cmd.Context(), force, dryRun, ui)
		},
	}
	cmd.Flags().BoolP(flagForce, flagForce[:1], false, "Remove the VM's persisted state after killing")
	cmd.Flags().BoolP(flagDryRun, flagDryRunShort, false, "Show what would be killed without killing")
	return cmd
}
