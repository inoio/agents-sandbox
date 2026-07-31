package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/moby/moby/client"
	"github.com/spf13/cobra"

	"gitlab.inoio.de/inoio/opencode-msb/internal/config"
	"gitlab.inoio.de/inoio/opencode-msb/internal/launcherconfig"
	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox"
	"gitlab.inoio.de/inoio/opencode-msb/internal/stdio"
)

func buildMinimalRootFlagsCmd() *cobra.Command {
	rootFlagsCmd := &cobra.Command{
		Use:   "opencode-msb",
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
		lc, keys, err := launcherconfig.Load(cfg.UserLauncherDir, projectLauncherDir)
		if err != nil {
			return err
		}
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

	return rootCmd
}

func buildTreeCmd(rootCmd *cobra.Command, ui stdio.UI) *cobra.Command {
	cmd := &cobra.Command{
		Use:   cmdTree,
		Short: "Print the full command tree",
		Run: func(cmd *cobra.Command, _ []string) {
			printTree(rootCmd, ui)
		},
	}
	return cmd
}

func buildVersionCmd(rootCmd *cobra.Command, ui stdio.UI) *cobra.Command {
	cmd := &cobra.Command{
		Use:   cmdVersion,
		Short: "Print version",
		Run: func(cmd *cobra.Command, _ []string) {
			ui.Outf("%s %s\n", rootCmd.Name(), version)
		},
	}
	return cmd
}

func buildListCmd(ui stdio.UI) *cobra.Command {
	cmd := &cobra.Command{
		Use:     cmdList,
		Aliases: []string{"ls"},
		Short:   "List sandboxes for this host",
		Annotations: map[string]string{
			annotationAlsoAs: "sandbox list",
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			sandboxes, err := sandbox.ListSandboxes(cmd.Context())
			if err != nil {
				return err
			}
			if len(sandboxes) == 0 {
				ui.Info("No sandboxes found.")
				return nil
			}
			for _, s := range sandboxes {
				ui.Outf("%-40s %s", s.Name, s.Status)
			}
			return nil
		},
	}
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
			opts := sandbox.RunOptions{Auto: false}
			opts.Branch, _ = cmd.Flags().GetString("branch")
			opts.Rebuild, _ = cmd.Flags().GetBool("rebuild")
			opts.DryRun, _ = cmd.Flags().GetBool("dry-run")
			opts.DryRunVM, _ = cmd.Flags().GetBool("dry-run-vm")
			if opts.DryRun {
				opts.DryRunVM = true
			}
			opts.CPUs, _ = cmd.Flags().GetUint8("cpus")
			opts.Memory, _ = cmd.Flags().GetString("memory")
			opts.TmpSize, _ = cmd.Flags().GetString("tmp-size")
			opts.User, _ = cmd.Flags().GetString("user")

			cfg := newConfig()

			err := sandbox.Shell(cmd.Context(), opts, cfg, ui)
			var exitErr *sandbox.ExitError
			if errors.As(err, &exitErr) {
				os.Exit(exitErr.Code)
			}
			return err
		},
	}

	cmd.Flags().StringP("branch", "b", "", "Run in an opencode worktree for the given branch name")
	cmd.Flags().BoolP("rebuild", "r", false, "Rebuild the runner image before starting")
	cmd.Flags().BoolP("dry-run", "n", false, "Dry run without starting anything")
	cmd.Flags().Bool("dry-run-vm", false, "Skip VM lifecycle but prepare everything else")
	cmd.Flags().Uint8P("cpus", "c", 0, "Number of CPUs (default: all)")
	cmd.Flags().StringP("memory", "m", "4G", "Memory limit")
	cmd.Flags().String("tmp-size", "2G", "Size of the /tmp tmpfs in the sandbox")
	cmd.Flags().StringP("user", "u", "", "Username or UID to use inside the sandbox (format: <name|uid>[:<group|gid>])")

	return cmd
}

func buildConfigCmd(ui stdio.UI) *cobra.Command {
	cmd := &cobra.Command{
		Use:   cmdConfig,
		Short: "Inspect opencode configuration",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "show",
		Short: "Print merged opencode config with source paths",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg := newConfig()
			projectConfigDir := ""
			if _, statErr := os.Stat(".opencode-msb/opencode"); statErr == nil {
				projectConfigDir = ".opencode-msb/opencode"
			}
			providerCfg, err := config.LoadProviderConfig(config.EmbeddedProviderConfig)
			if err != nil {
				return fmt.Errorf("load provider config: %w", err)
			}

			descs, err := config.DescribeConfig(cfg.UserConfigDir, projectConfigDir, providerCfg)
			if err != nil {
				return err
			}
			files, err := config.BuildMergedConfig(cfg.UserConfigDir, projectConfigDir, providerCfg)
			if err != nil {
				return err
			}

			for _, desc := range descs {
				ui.Outf("=== %s ===", desc.Name)
				for _, src := range desc.Sources {
					ui.Outf("  source: %s", src)
				}
				if data, ok := files[desc.Name]; ok {
					ui.Out("  merged content:")
					for line := range strings.SplitSeq(string(data), "\n") {
						ui.Outf("    %s", line)
					}
				}
				ui.Out("")
			}
			return nil
		},
	})
	return cmd
}

func buildImageCmd(ui stdio.UI) *cobra.Command {
	cmd := &cobra.Command{
		Use:   cmdImage,
		Short: "Manage runner images",
	}
	cmd.AddCommand(&cobra.Command{
		Use:     cmdList,
		Aliases: []string{"ls"},
		Short:   "List cached runner images",
		RunE: func(cmd *cobra.Command, _ []string) error {
			images, err := sandbox.ListImages(cmd.Context())
			if err != nil {
				return err
			}
			if len(images) == 0 {
				ui.Info("No images found.")
				return nil
			}
			for _, img := range images {
				ui.Outf("%-50s %s", img.Reference, img.Digest)
			}
			return nil
		},
	})
	cmd.AddCommand(buildBuildCmd(ui))
	return cmd
}

func buildVolumeCmd(ui stdio.UI) *cobra.Command {
	cmd := &cobra.Command{
		Use:   cmdVolume,
		Short: "Manage volumes",
	}
	cmd.AddCommand(&cobra.Command{
		Use:     cmdList,
		Aliases: []string{"ls"},
		Short:   "List managed volumes",
		RunE: func(cmd *cobra.Command, _ []string) error {
			volumes, err := sandbox.ListVolumes(cmd.Context())
			if err != nil {
				return err
			}
			if len(volumes) == 0 {
				ui.Info("No volumes found.")
				return nil
			}
			for _, vol := range volumes {
				ui.Outf("%-50s %s", vol.Name, vol.Path)
			}
			return nil
		},
	})
	return cmd
}

func buildSandboxCmd(ui stdio.UI) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sandbox",
		Short: "Manage sandboxes",
	}
	cmd.AddCommand(buildListCmd(ui))
	cmd.AddCommand(buildShellCmd(ui))
	cmd.AddCommand(buildRunCmd(ui))
	cmd.AddCommand(buildStopCmd(ui))
	cmd.AddCommand(buildKillCmd(ui))
	return cmd
}

func buildDoctorCmd(ui stdio.UI) *cobra.Command {
	return &cobra.Command{
		Use:   cmdDoctor,
		Short: "Check prerequisites",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !sandbox.CheckAll(cmd.Context(), ui) {
				return errors.New("preflight failed")
			}
			ui.Info("doctor: all checks passed")
			return nil
		},
	}
}

func buildBuildCmd(ui stdio.UI) *cobra.Command {
	cmd := &cobra.Command{
		Use:   cmdBuild,
		Short: "Build or rebuild the runner image",
		RunE: func(cmd *cobra.Command, _ []string) error {
			force, _ := cmd.Flags().GetBool("rebuild")
			dryRun, _ := cmd.Flags().GetBool("dry-run")
			return sandbox.BuildImage(cmd.Context(), force, dryRun, ui)
		},
	}
	cmd.Flags().BoolP("rebuild", "r", false, "Force a clean rebuild")
	cmd.Flags().BoolP("dry-run", "n", false, "Dry run without building")
	return cmd
}

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

	cmd.Flags().StringP("branch", "b", "", "Run in an opencode worktree for the given branch name")
	cmd.Flags().BoolP("rebuild", "r", false, "Rebuild the runner image before starting")
	cmd.Flags().BoolP("dry-run", "n", false, "Validate setup without running opencode")
	cmd.Flags().Bool("dry-run-vm", false, "Skip VM lifecycle but prepare everything else")
	cmd.Flags().Uint8P("cpus", "c", 0, "Number of CPUs (default: all)")
	cmd.Flags().StringP("memory", "m", "4G", "Memory limit")
	cmd.Flags().String("tmp-size", "2G", "Size of the /tmp tmpfs in the sandbox")
	cmd.Flags().
		StringP("user", "u", "", "Username or UID for the runtime user (format: <name|uid>[:<group|gid>])")
	cmd.Flags().Bool("no-auto", false, "Do not pass --auto to opencode")

	return cmd
}

func runFunc(ui stdio.UI) func(cmd *cobra.Command, args []string) error {
	return func(cmd *cobra.Command, args []string) error {
		opts := sandbox.RunOptions{Args: args, Auto: true}
		opts.Branch, _ = cmd.Flags().GetString("branch")
		opts.Rebuild, _ = cmd.Flags().GetBool("rebuild")
		opts.DryRun, _ = cmd.Flags().GetBool("dry-run")
		opts.DryRunVM, _ = cmd.Flags().GetBool("dry-run-vm")
		// --dry-run implies --dry-run-vm
		if opts.DryRun {
			opts.DryRunVM = true
		}
		opts.CPUs, _ = cmd.Flags().GetUint8("cpus")
		opts.Memory, _ = cmd.Flags().GetString("memory")
		opts.TmpSize, _ = cmd.Flags().GetString("tmp-size")
		opts.User, _ = cmd.Flags().GetString("user")
		if noAuto, _ := cmd.Flags().GetBool("no-auto"); noAuto {
			opts.Auto = false
		}

		cfg := newConfig()

		err := sandbox.Run(cmd.Context(), opts, cfg, ui)
		var exitErr *sandbox.ExitError
		if errors.As(err, &exitErr) {
			os.Exit(exitErr.Code)
		}
		return err
	}
}
func buildPruneCmd(ui stdio.UI) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "prune [flags]",
		Short: "Prune stale VMs, volumes, and images",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ageStr, _ := cmd.Flags().GetString("age")
			var age time.Duration
			if ageStr != "" {
				if d, ok := launcherconfig.ParseHumanDuration(ageStr); ok {
					age = d
				}
			}
			if age == 0 {
				age = 7 * 24 * time.Hour
			}
			dryRun, _ := cmd.Flags().GetBool("dry-run")
			dockerCli, err := client.New(client.FromEnv)
			if err != nil {
				return fmt.Errorf("cannot connect to Docker daemon (is dockerd running?): %w", err)
			}
			report, err := sandbox.Prune(cmd.Context(), dockerCli, age, dryRun, ui)
			_ = dockerCli.Close()
			if err != nil {
				return err
			}
			if report != nil {
				printPruneSummary(ui, report, dryRun)
			}
			return nil
		},
	}
	cmd.Flags().StringP("age", "a", "", "Prune threshold (default: manualPruneAge from config)")
	cmd.Flags().BoolP("dry-run", "n", false, "Show what would be pruned without deleting")
	cmd.Flags().Bool("dry-run-vm", false, "Suppress VM deletion during prune")
	cmd.Flags().BoolP("force", "f", false, "Skip confirmation prompt")
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
