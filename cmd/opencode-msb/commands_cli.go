package main

import (
	"github.com/spf13/cobra"

	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox"
	"gitlab.inoio.de/inoio/opencode-msb/internal/stdio"
)

func buildRunCmd(ui stdio.UI) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run [flags] [ARGS...]",
		Short: "Run opencode in a microsandbox VM",
		Annotations: map[string]string{
			annotationArgsDesc: "Arguments forwarded to opencode (use -- to separate from launcher flags)",
			annotationAlsoAs:   "sandbox run",
		},
		RunE: runFunc(ui),
	}

	registerRunFlags(cmd)
	cmd.Flags().Bool("no-auto", false, "Do not pass --auto to opencode")

	return cmd
}

func buildShellCmd(ui stdio.UI) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "shell [flags]",
		Short: "Start sandbox and open a shell (debug)",
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

func buildStopCmd(ui stdio.UI) *cobra.Command {
	cmd := &cobra.Command{
		Use:   cmdStop,
		Short: "Stop the project VM",
		Annotations: map[string]string{
			annotationAlsoAs: "sandbox stop",
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			force, _ := cmd.Flags().GetBool("force")
			dryRun, _ := cmd.Flags().GetBool("dry-run")
			return sandbox.StopProjectVM(cmd.Context(), force, dryRun, ui)
		},
	}
	cmd.Flags().BoolP("force", "f", false, "Remove the VM's persisted state after stopping")
	cmd.Flags().BoolP("dry-run", "n", false, "Show what would be stopped without stopping")
	return cmd
}

func buildKillCmd(ui stdio.UI) *cobra.Command {
	cmd := &cobra.Command{
		Use:   cmdKill,
		Short: "Force-kill the project VM",
		Annotations: map[string]string{
			annotationAlsoAs: "sandbox kill",
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			force, _ := cmd.Flags().GetBool("force")
			dryRun, _ := cmd.Flags().GetBool("dry-run")
			return sandbox.KillProjectVM(cmd.Context(), force, dryRun, ui)
		},
	}
	cmd.Flags().BoolP("force", "f", false, "Remove the VM's persisted state after killing")
	cmd.Flags().BoolP("dry-run", "n", false, "Show what would be killed without killing")
	return cmd
}
