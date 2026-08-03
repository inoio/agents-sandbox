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

// newDockerClient creates a new Docker client for the prune command.
// Tests override this to inject stub clients.
//
//nolint:gochecknoglobals // factory variable for test injection
var newDockerClient = func() (sandbox.DockerClient, error) {
	cli, err := client.New(client.FromEnv)
	if err != nil {
		return nil, err
	}
	return cli, nil
}

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
		Run: func(_ *cobra.Command, _ []string) {
			printTree(rootCmd, ui)
		},
	}
	return cmd
}

func buildVersionCmd(rootCmd *cobra.Command, ui stdio.UI) *cobra.Command {
	cmd := &cobra.Command{
		Use:   cmdVersion,
		Short: "Print version",
		Run: func(_ *cobra.Command, _ []string) {
			ui.Outf("%s %s\n", rootCmd.Name(), version)
		},
	}
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
			printItems(sandboxes, "No sandboxes found.", "%-40s %s",
				func(s sandbox.Info) string { return s.Name },
				func(s sandbox.Info) string { return s.Status },
				ui)
			return nil
		},
	}
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
		RunE: func(_ *cobra.Command, _ []string) error {
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

// registerRunFlags adds the shared run/shell flags to the given command.
func registerRunFlags(cmd *cobra.Command) {
	cmd.Flags().StringP("branch", "b", "", "Run in an opencode worktree for the given branch name")
	cmd.Flags().BoolP("rebuild", "r", false, "Rebuild the runner image before starting")
	cmd.Flags().BoolP("dry-run", "n", false, "Dry run without starting anything")
	cmd.Flags().Bool("dry-run-vm", false, "Skip VM lifecycle but prepare everything else")
	cmd.Flags().Uint8P("cpus", "c", 0, "Number of CPUs (default: all)")
	cmd.Flags().StringP("memory", "m", "4G", "Memory limit")
	cmd.Flags().String("tmp-size", "2G", "Size of the /tmp tmpfs in the sandbox")
	cmd.Flags().StringP("user", "u", "", "Username or UID for the runtime user (format: <name|uid>[:<group|gid>])")
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

	registerRunFlags(cmd)
	cmd.Flags().Bool("no-auto", false, "Do not pass --auto to opencode")

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

func buildPruneCmd(ui stdio.UI) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "prune [flags]",
		Short: "Prune stale VMs, volumes, and images",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ageStr, _ := cmd.Flags().GetString("age")
			var age time.Duration
			if ageStr != "" {
				d, ok := launcherconfig.ParseHumanDuration(ageStr)
				if !ok {
					return fmt.Errorf("invalid age %q: use a Go duration or suffix d/w (e.g. 7d, 2w)", ageStr)
				}
				age = d
			}
			if age == 0 {
				age = 7 * 24 * time.Hour
			}
			dryRun, _ := cmd.Flags().GetBool("dry-run")
			dockerCli, err := newDockerClient()
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
			printItems(images, "No images found.", "%-50s %s",
				func(i sandbox.ImageInfo) string { return i.Reference },
				func(i sandbox.ImageInfo) string { return i.Digest },
				ui)
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
			printItems(volumes, "No volumes found.", "%-50s %s",
				func(v sandbox.VolumeInfo) string { return v.Name },
				func(v sandbox.VolumeInfo) string { return v.Path },
				ui)
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

func runFunc(ui stdio.UI) func(cmd *cobra.Command, args []string) error {
	return func(cmd *cobra.Command, args []string) error {
		opts := extractRunOptions(cmd, true, ui)
		opts.Args = args

		// Handle the --no-auto flag specific to the run command
		if noAuto, _ := cmd.Flags().GetBool("no-auto"); noAuto {
			opts.Auto = false
		}

		cfg := newConfig()

		return sandbox.Run(cmd.Context(), opts, cfg, ui)
	}
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
		ui.Verbosef("x  %s", entry)
	}
}
