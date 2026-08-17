package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"gitlab.inoio.de/inoio/opencode-sandbox/internal/configpaths"
	"gitlab.inoio.de/inoio/opencode-sandbox/internal/git"
	"gitlab.inoio.de/inoio/opencode-sandbox/internal/homeconfig"
	"gitlab.inoio.de/inoio/opencode-sandbox/internal/opencodeconfig"
	"gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/doctor"
	"gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/humanize"
	"gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/image"
	"gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/pruning"
	"gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/session"
	"gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/volume"
	"gitlab.inoio.de/inoio/opencode-sandbox/internal/termio"
	"gitlab.inoio.de/inoio/opencode-sandbox/internal/viperconfig"
)

type volumeOpFunc func(context.Context, string, string, string, string, bool, bool, termio.UI) error

const (
	colName    = "NAME"
	colStatus  = "STATUS"
	colImage   = "IMAGE"
	colCreated = "CREATED"
	colKind    = "KIND"
	colSize    = "SIZE"
	colRef     = "REFERENCE"
	colDigest  = "DIGEST"
)

// sandboxListHeaders returns the column order for sandbox lists. Matches msb:
// NAME IMAGE STATUS CREATED.
func sandboxListHeaders() []string {
	return []string{colName, colImage, colStatus, colCreated}
}

func imageListHeaders() []string {
	return []string{colRef, colDigest, colSize, colCreated}
}

func volumeListHeaders() []string {
	return []string{colName, colKind, colSize, colCreated}
}

// volumeSize renders the SIZE column to match msb: disk volumes show
// capacity, directory volumes show quota, and "-" when unavailable.
func volumeSize(kind string, q *uint32, c *uint64) string {
	if kind == "disk" {
		if c != nil {
			return humanize.FormatBytes(*c)
		}
		return "-"
	}
	if q != nil {
		return humanize.FormatBytes(uint64(*q) * 1024 * 1024)
	}
	return "-"
}

// truncateDigest shortens a full manifest digest to the short form msb uses in
// image list output: the "sha256:" prefix followed by the first 12 hex chars.
func truncateDigest(digest string) string {
	const shortLen = len("sha256:") + 12
	if len(digest) <= shortLen {
		return digest
	}
	return digest[:shortLen]
}

func buildVolumeOpsCmd(
	ui termio.UI,
	name string,
	fn volumeOpFunc,
	short, rmFlag, rmHelp string,
	rmVar *bool,
	buildImage bool,
) *cobra.Command {
	cmd := &cobra.Command{
		Use:   name,
		Args:  cobra.MaximumNArgs(1),
		Short: short,
		RunE: func(c *cobra.Command, args []string) error {
			if !doctor.CheckAll(c.Context(), ui) {
				return errors.New("preflight failed")
			}
			projectSlug := git.ProjectSlug(ui)
			dryRun, _ := c.Flags().GetBool(flagDryRun)
			rebuild, _ := c.Flags().GetBool(flagRebuild)
			info, err := image.EnsureImage(
				c.Context(),
				projectSlug,
				image.BuildOptions{Force: rebuild, OpenCodeVersion: ""},
				ui,
			)
			if err != nil {
				return fmt.Errorf("ensure image: %w", err)
			}
			var volName string
			if len(args) > 0 {
				volName = args[0]
			}
			return fn(c.Context(), projectSlug, volName, info.Tag, info.Digest, *rmVar, dryRun, ui)
		},
	}
	cmd.Flags().BoolVar(rmVar, rmFlag, false, rmHelp)
	cmd.Flags().Bool(flagDryRun, false, "Show what would be done")
	if buildImage {
		cmd.Flags().Bool(flagRebuild, false, "Rebuild runner image first")
	}
	return cmd
}

func buildDoctorCmd(ui termio.UI) *cobra.Command {
	return &cobra.Command{
		Use:   cmdDoctor,
		Args:  cobra.NoArgs,
		Short: "Check prerequisites",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !doctor.CheckAll(cmd.Context(), ui) {
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
			sandboxes, err := session.ListSandboxes(cmd.Context())
			if err != nil {
				return err
			}
			printItems(sandboxes, "No sandboxes found.", sandboxListHeaders(), ui,
				func(s session.Info) string { return s.Name },
				func(s session.Info) string { return s.Image },
				func(s session.Info) string { return termio.StyleStatus(s.Status) },
				func(s session.Info) string { return s.CreatedAt },
			)
			return nil
		},
	}
	return cmd
}

func buildConfigCmd(ui termio.UI) *cobra.Command {
	cmd := &cobra.Command{
		Use:     cmdConfig,
		Aliases: cmdConfigAliases,
		Short:   "Inspect opencode and home configuration",
	}

	cmd.AddCommand(&cobra.Command{
		Use:   cmdShow,
		Args:  cobra.NoArgs,
		Short: "Print the merged opencode config and the snippet files used",
		RunE: func(*cobra.Command, []string) error {
			cp := configpaths.Get()
			data, sources, has, err := opencodeconfig.BuildOpenCodeJSON(
				cp.UserOpencodeConfigDir(),
				cp.ProjectOpencodeConfigDir(),
			)
			if err != nil {
				return err
			}
			if !has {
				ui.Out("No opencode snippet files found; no merged opencode.json is provisioned.")
				return nil
			}
			ui.Out("merged files:")
			for _, src := range sources {
				ui.Outf("  %s", src)
			}
			ui.Out("merged opencode.json:")
			for line := range strings.SplitSeq(string(data), "\n") {
				ui.Outf("  %s", line)
			}
			return nil
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   cmdHome,
		Args:  cobra.NoArgs,
		Short: "List home-file mappings from the home.yaml manifest",
		RunE: func(*cobra.Command, []string) error {
			cp := configpaths.Get()
			pairs, has, err := homeconfig.DescribeManifest(cp.UserConfigDir(), cp.ProjectConfigDir(), "/home/dev")
			if err != nil {
				return err
			}
			if !has {
				ui.Out("No home.yaml manifest found.")
				return nil
			}
			if len(pairs) == 0 {
				ui.Out("No home.yaml mappings.")
				return nil
			}
			for _, p := range pairs {
				ui.Outf("%s  <-  %s", p[0], p[1])
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
			force, _ := cmd.Flags().GetBool(flagRebuild)
			dryRun, _ := cmd.Flags().GetBool(flagDryRun)
			openCodeVersion, _ := cmd.Flags().GetString(flagOpenCodeVersion)
			return session.BuildImage(cmd.Context(), force, dryRun, openCodeVersion, ui)
		},
	}
	cmd.Flags().BoolP(flagRebuild, flagRebuild[:1], false, "Force a clean rebuild")
	cmd.Flags().BoolP(flagDryRun, flagDryRunShort, false, "Dry run without building")
	cmd.Flags().
		String(flagOpenCodeVersion, "", "Pin the opencode version baked into the runner image (default: latest release)")
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
			images, err := image.ListImages(cmd.Context())
			if err != nil {
				return err
			}
			printItems(images, "No images found.", imageListHeaders(), ui,
				func(i image.Info) string { return i.Reference },
				func(i image.Info) string { return truncateDigest(i.Digest) },
				func(i image.Info) string { return i.Size },
				func(i image.Info) string { return i.CreatedAt },
			)
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
		Short:   "Manage home volumes",
	}
	cmd.AddCommand(&cobra.Command{
		Use:     cmdList,
		Aliases: cmdListAliases,
		Args:    cobra.NoArgs,
		Short:   "List managed volumes",
		RunE: func(cmd *cobra.Command, _ []string) error {
			volumes, err := volume.ListVolumes(cmd.Context())
			if err != nil {
				return err
			}
			printItems(volumes, "No volumes found.", volumeListHeaders(), ui,
				func(v volume.VolumeInfo) string { return v.Name },
				func(v volume.VolumeInfo) string { return v.Kind },
				func(v volume.VolumeInfo) string { return volumeSize(v.Kind, v.QuotaMiB, v.CapacityBytes) },
				func(v volume.VolumeInfo) string { return v.CreatedAt },
			)
			return nil
		},
	})

	var migrateRmOld bool
	cmd.AddCommand(
		buildVolumeOpsCmd(
			ui,
			cmdMigrate,
			volume.CmdMigrate,
			"Migrate: create new volume, copy files from existing volume on top",
			flagRemove,
			"Remove the old home volume after migration",
			&migrateRmOld,
			true,
		),
	)

	var resetRmOld bool
	cmd.AddCommand(
		buildVolumeOpsCmd(
			ui,
			cmdReset,
			volume.CmdReset,
			"Reset: create new volume from image, ",
			flagRemove,
			"Remove the old home volume after reset",
			&resetRmOld,
			true,
		),
	)

	var editRmOld bool
	cmd.AddCommand(
		buildVolumeOpsCmd(
			ui,
			cmdEdit,
			volume.CmdEdit,
			"Edit: create new volume alongside old one for manual transfer",
			flagRemove,
			"Remove the old home volume after editing",
			&editRmOld,
			false,
		),
	)

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
			ageStr, _ := cmd.Flags().GetString(flagAge)
			var age time.Duration
			if ageStr != "" {
				d, ok := viperconfig.ParseHumanDuration(ageStr)
				if !ok {
					return fmt.Errorf("invalid age %q: use a Go duration or suffix d/w (e.g. 7d, 2w)", ageStr)
				}
				age = d
			}
			if age == 0 {
				if r := resolverFromContext(cmd.Context()); r != nil && r.ManualPruneAge() > 0 {
					age = r.ManualPruneAge()
				} else {
					age = 7 * 24 * time.Hour
				}
			}
			dryRun, _ := cmd.Flags().GetBool(flagDryRun)
			return pruning.Prune(cmd.Context(), age, dryRun, false, ui)
		},
	}
	cmd.Flags().StringP(flagAge, flagAge[:1], "", "Prune threshold (default: manualPruneAge from config)")
	cmd.Flags().BoolP(flagDryRun, flagDryRunShort, false, "Show what would be pruned without deleting")
	cmd.Flags().Bool(flagDryRunVM, false, "Suppress VM deletion during prune")
	cmd.Flags().BoolP(flagForce, flagForce[:1], false, "Skip confirmation prompt")
	return cmd
}
