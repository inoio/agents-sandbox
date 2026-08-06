package main

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"gitlab.inoio.de/inoio/opencode-msb/internal/config"
	"gitlab.inoio.de/inoio/opencode-msb/internal/git"
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

//nolint:funlen // Multiple subcommand definitions for volume management
func buildVolumeCmd(ui termio.UI) *cobra.Command {
	cmd := &cobra.Command{
		Use:     cmdVolume,
		Aliases: cmdVolumeAliases,
		Short:   "Manage home volumes",
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

	var migrateRmOld bool
	migrateCmd := &cobra.Command{
		Use:   cmdMigrate,
		Args:  cobra.MaximumNArgs(1),
		Short: "Migrate: create new volume, copy files from existing volume on top",
		RunE: func(c *cobra.Command, args []string) error {
			if !sandbox.CheckAll(c.Context(), ui) {
				return errors.New("preflight failed")
			}
			projectSlug := git.ProjectSlug(ui)
			dryRun, _ := c.Flags().GetBool("dry-run")
			rebuild, _ := c.Flags().GetBool("rebuild")
			imageTag, _, _, err := sandbox.EnsureImage(c.Context(), projectSlug, rebuild, ui)
			if err != nil {
				return fmt.Errorf("ensure image: %w", err)
			}
			var volName string
			if len(args) > 0 {
				volName = args[0]
			}
			return sandbox.CmdMigrate(c.Context(), projectSlug, volName, imageTag, migrateRmOld, dryRun, ui)
		},
	}
	migrateCmd.Flags().BoolVar(&migrateRmOld, flagRemove, false, "Remove old volume after migration")
	migrateCmd.Flags().Bool("dry-run", false, "Show what would be done")
	migrateCmd.Flags().Bool("rebuild", false, "Rebuild runner image first")
	cmd.AddCommand(migrateCmd)

	var resetRmOld bool
	resetCmd := &cobra.Command{
		Use:   cmdReset,
		Args:  cobra.MaximumNArgs(1),
		Short: "Reset: create new volume from image, ",
		RunE: func(c *cobra.Command, args []string) error {
			if !sandbox.CheckAll(c.Context(), ui) {
				return errors.New("preflight failed")
			}
			projectSlug := git.ProjectSlug(ui)
			dryRun, _ := c.Flags().GetBool("dry-run")
			rebuild, _ := c.Flags().GetBool("rebuild")
			imageTag, _, _, err := sandbox.EnsureImage(c.Context(), projectSlug, rebuild, ui)
			if err != nil {
				return fmt.Errorf("ensure image: %w", err)
			}
			var volName string
			if len(args) > 0 {
				volName = args[0]
			}
			return sandbox.CmdReset(c.Context(), projectSlug, volName, imageTag, resetRmOld, dryRun, ui)
		},
	}
	resetCmd.Flags().BoolVar(&resetRmOld, flagRemove, false, "Remove old volume after reset")
	resetCmd.Flags().Bool("dry-run", false, "Show what would be done")
	resetCmd.Flags().Bool("rebuild", false, "Rebuild runner image first")
	cmd.AddCommand(resetCmd)

	var editRmOld bool
	editCmd := &cobra.Command{
		Use:   cmdEdit,
		Args:  cobra.MaximumNArgs(1),
		Short: "Edit: create new volume alongside old one for manual transfer",
		RunE: func(c *cobra.Command, args []string) error {
			if !sandbox.CheckAll(c.Context(), ui) {
				return errors.New("preflight failed")
			}
			projectSlug := git.ProjectSlug(ui)
			dryRun, _ := c.Flags().GetBool("dry-run")
			rebuild, _ := c.Flags().GetBool("rebuild")
			imageTag, _, _, err := sandbox.EnsureImage(c.Context(), projectSlug, rebuild, ui)
			if err != nil {
				return fmt.Errorf("ensure image: %w", err)
			}
			var volName string
			if len(args) > 0 {
				volName = args[0]
			}
			return sandbox.CmdEdit(c.Context(), projectSlug, volName, imageTag, editRmOld, dryRun, ui)
		},
	}
	editCmd.Flags().BoolVar(&editRmOld, flagRemove, false, "Remove old volume after editing")
	editCmd.Flags().Bool("dry-run", false, "Show what would be done")
	editCmd.Flags().Bool("rebuild", false, "Rebuild runner image before editing")
	cmd.AddCommand(editCmd)

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
