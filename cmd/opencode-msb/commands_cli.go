package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox"
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
	cmd.Flags().Bool(flagNoAuto, false, "Do not pass --auto to opencode")

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
		opts := extractRunOptions(cmd, true, ui)
		opts.Args = args

		// Handle the --no-auto flag specific to the run command
		if noAuto, _ := cmd.Flags().GetBool(flagNoAuto); noAuto {
			opts.Auto = false
		}

		return sandbox.Run(cmd.Context(), opts, ui)
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
			opts := extractRunOptions(cmd, false, ui)

			cfg := newConfig()

			return sandbox.Shell(cmd.Context(), opts, cfg, ui)
		},
	}

	registerRunFlags(cmd)

	return cmd
}

// registerRunFlags adds the shared run/shell flags to the given command.
func registerRunFlags(cmd *cobra.Command) {
	cmd.Flags().StringP(flagBranch, flagBranch[:1], "", "Run in an opencode worktree for the given branch name")
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
			return sandbox.StopProjectVM(cmd.Context(), force, dryRun, ui)
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
			return sandbox.KillProjectVM(cmd.Context(), force, dryRun, ui)
		},
	}
	cmd.Flags().BoolP(flagForce, flagForce[:1], false, "Remove the VM's persisted state after killing")
	cmd.Flags().BoolP(flagDryRun, flagDryRunShort, false, "Show what would be killed without killing")
	return cmd
}
