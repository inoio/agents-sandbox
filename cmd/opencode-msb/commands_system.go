package main

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"gitlab.inoio.de/inoio/opencode-msb/internal/config"
	"gitlab.inoio.de/inoio/opencode-msb/internal/launcherconfig"
	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox"
	"gitlab.inoio.de/inoio/opencode-msb/internal/termio"
)

func buildDoctorCmd(ui termio.UI) *cobra.Command {
	return &cobra.Command{
		Use:   cmdDoctor,
		Args:  cobra.NoArgs,
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

func buildListCmd(ui termio.UI) *cobra.Command {
	cmd := &cobra.Command{
		Use:     cmdList,
		Aliases: cmdListAliases,
		Args:    cobra.NoArgs,
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

func buildConfigCmd(ui termio.UI) *cobra.Command {
	cmd := &cobra.Command{
		Use:     cmdConfig,
		Aliases: cmdConfigAliases,
		Short:   "Inspect opencode configuration",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   cmdShow,
		Args:  cobra.NoArgs,
		Short: "Print merged opencode config with source paths",
		RunE: func(_ *cobra.Command, _ []string) error {
			cfg := newConfig()
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

func buildBuildCmd(ui termio.UI) *cobra.Command {
	cmd := &cobra.Command{
		Use:   cmdBuild,
		Args:  cobra.NoArgs,
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

func buildImageCmd(ui termio.UI) *cobra.Command {
	cmd := &cobra.Command{
		Use:     cmdImage,
		Aliases: cmdImageAliases,
		Args:    cobra.NoArgs,
		Short:   "Manage runner images",
	}
	cmd.AddCommand(&cobra.Command{
		Use:     cmdList,
		Args:    cobra.NoArgs,
		Aliases: cmdListAliases,
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

func buildVolumeCmd(ui termio.UI) *cobra.Command {
	cmd := &cobra.Command{
		Use:     cmdVolume,
		Aliases: cmdVolumeAliases,
		Short:   "Manage volumes",
	}
	cmd.AddCommand(&cobra.Command{
		Use:     cmdList,
		Aliases: cmdListAliases,
		Args:    cobra.NoArgs,
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

func buildSandboxCmd(ui termio.UI) *cobra.Command {
	cmd := &cobra.Command{
		Use:     cmdSandbox,
		Aliases: cmdSandboxAliases,
		Args:    cobra.NoArgs,
		Short:   "Manage sandboxes",
	}
	cmd.AddCommand(buildListCmd(ui))
	cmd.AddCommand(buildShellCmd(ui))
	cmd.AddCommand(buildRunCmd(ui))
	cmd.AddCommand(buildStopCmd(ui))
	cmd.AddCommand(buildKillCmd(ui))
	return cmd
}

func buildPruneCmd(ui termio.UI) *cobra.Command {
	cmd := &cobra.Command{
		Use:   cmdPrune,
		Args:  cobra.NoArgs,
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
			return sandbox.Prune(cmd.Context(), age, dryRun, false, ui)
		},
	}
	cmd.Flags().StringP("age", "a", "", "Prune threshold (default: manualPruneAge from config)")
	cmd.Flags().BoolP("dry-run", "n", false, "Show what would be pruned without deleting")
	cmd.Flags().Bool("dry-run-vm", false, "Suppress VM deletion during prune")
	cmd.Flags().BoolP("force", "f", false, "Skip confirmation prompt")
	return cmd
}
