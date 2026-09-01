package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/inoio/opencode-sandbox/internal/agent"
	"github.com/inoio/opencode-sandbox/internal/configpaths"
	"github.com/inoio/opencode-sandbox/internal/git"
	"github.com/inoio/opencode-sandbox/internal/homeconfig"
	"github.com/inoio/opencode-sandbox/internal/humanize"
	"github.com/inoio/opencode-sandbox/internal/sandbox/doctor"
	"github.com/inoio/opencode-sandbox/internal/sandbox/image"
	"github.com/inoio/opencode-sandbox/internal/sandbox/pruning"
	"github.com/inoio/opencode-sandbox/internal/sandbox/reprovision"
	sandbox "github.com/inoio/opencode-sandbox/internal/sandbox/vm"
	"github.com/inoio/opencode-sandbox/internal/sandbox/volume"
	"github.com/inoio/opencode-sandbox/internal/termio"
	"github.com/inoio/opencode-sandbox/internal/viperconfig"
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
			projectSlug := git.ProjectSlug()
			dryRun, _ := c.Flags().GetBool(flagDryRun)
			rebuild, _ := c.Flags().GetBool(flagRebuild)
			a, err := resolveAgentFlag(c)
			if err != nil {
				return err
			}
			info, err := image.EnsureImage(
				c.Context(),
				a,
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
	cmd.Flags().String(flagAgent, defaultAgentName, "Coding agent profile")
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
	var (
		labelsStr []string
		limit     uint32
		running   bool
		stopped   bool
		namesOnly bool
		format    string
	)
	cmd := &cobra.Command{
		Use:     cmdList,
		Aliases: cmdListAliases,
		Args:    cobra.NoArgs,
		Short:   "List sandboxes for this host",
		Annotations: map[string]string{
			annotationAlsoAs: "sandbox list",
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			if namesOnly && format != "" {
				return errors.New("--names and --format are mutually exclusive")
			}
			if format != "" && format != formatJSON {
				return fmt.Errorf("unsupported format %q: only %q is supported", format, formatJSON)
			}
			labels, err := parseLabelFlags(labelsStr)
			if err != nil {
				return err
			}
			var lim *uint32
			if cmd.Flags().Changed(flagLimit) && limit != 0 {
				lim = &limit
			}
			opt := sandbox.ListOption{
				Labels:      labels,
				Limit:       lim,
				RunningOnly: running,
				StoppedOnly: stopped,
			}
			sandboxes, err := sandbox.ListSandboxes(cmd.Context(), opt)
			if err != nil {
				return err
			}
			if namesOnly {
				for _, s := range sandboxes {
					ui.Out(s.Name)
				}
				return nil
			}
			if format == formatJSON {
				return printSandboxesJSON(ui, sandboxes)
			}
			printItems(sandboxes, "No sandboxes found.", sandboxListHeaders(), ui,
				func(s sandbox.Info) string { return s.Name },
				func(s sandbox.Info) string { return s.Image },
				func(s sandbox.Info) string { return termio.StyleStatus(s.Status) },
				func(s sandbox.Info) string { return s.CreatedAt },
			)
			return nil
		},
	}
	cmd.Flags().BoolVar(&namesOnly, pFlagNames, false, "Print only sandbox names")
	cmd.Flags().
		StringArrayVar(&labelsStr, flagLabel, nil, "Only show sandboxes carrying this label KEY=VALUE (repeatable, all must match)")
	cmd.Flags().Uint32Var(&limit, flagLimit, 0, "Limit the number of sandboxes shown")
	cmd.Flags().BoolVar(&running, flagRunning, false, "Show only running sandboxes")
	cmd.Flags().BoolVar(&stopped, flagStopped, false, "Show only stopped sandboxes")
	cmd.Flags().StringVar(&format, flagFormat, "", "Output format (json)")
	return cmd
}

func parseLabelFlags(values []string) (map[string]string, error) {
	labels := make(map[string]string, len(values))
	for _, v := range values {
		key, val, ok := strings.Cut(v, "=")
		if !ok || key == "" {
			return nil, fmt.Errorf("invalid label %q: must be KEY=VALUE", v)
		}
		labels[key] = val
	}
	return labels, nil
}

type jsonSandbox struct {
	Name    string            `json:"name"`
	Status  string            `json:"status"`
	Image   string            `json:"image"`
	Created time.Time         `json:"created"`
	Updated time.Time         `json:"updated"`
	Labels  map[string]string `json:"labels"`
}

func printSandboxesJSON(ui termio.UI, infos []sandbox.Info) error {
	out := make([]jsonSandbox, 0, len(infos))
	for _, s := range infos {
		out = append(out, jsonSandbox{
			Name:    s.Name,
			Status:  s.Status,
			Image:   s.Image,
			Created: s.CreatedAtRaw,
			Updated: s.UpdatedAtRaw,
			Labels:  s.Labels,
		})
	}
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	ui.Out(string(data))
	return nil
}

func buildConfigCmd(ui termio.UI) *cobra.Command {
	cmd := &cobra.Command{
		Use:     cmdConfig,
		Aliases: cmdConfigAliases,
		Short:   "Inspect opencode and home configuration",
	}

	cmd.AddCommand(buildConfigAgentCmd(ui))

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

// buildConfigAgentCmd returns the config agent subcommand, which prints the
// agent's merged snippet config and the host drop-in files that provisioning
// would copy into the VM.
func buildConfigAgentCmd(ui termio.UI) *cobra.Command {
	return &cobra.Command{
		Use:   cmdAgent,
		Args:  cobra.MaximumNArgs(1),
		Short: "Show the merged agent config and the host files provisioned into the VM",
		RunE: func(c *cobra.Command, args []string) error {
			name := defaultAgentName
			if len(args) > 0 {
				name = args[0]
			} else if r := resolverFromContext(c.Context()); r != nil {
				name = r.Agent()
			}
			a, ok := agent.Lookup(name)
			if !ok {
				return fmt.Errorf("unknown agent %q: must be one of %s", name, strings.Join(agent.Names(), ", "))
			}
			provision := true
			if r := resolverFromContext(c.Context()); r != nil {
				provision = r.ProvisionHostConfig()
			}
			hostHome, _ := os.UserHomeDir()
			merged, sources, hostFiles, err := reprovision.Describe(a, hostHome, reprovision.VMHomeDir, ui, provision)
			if err != nil {
				return err
			}
			ui.Outf("agent: %s", a.Name())
			if len(sources) == 0 {
				ui.Out("No snippet files found; no merged config is provisioned.")
				return nil
			}
			ui.Out("merged files:")
			for _, src := range sources {
				ui.Outf("  %s", src)
			}
			ui.Out("merged agent config:")
			for line := range strings.SplitSeq(string(merged), "\n") {
				ui.Outf("  %s", line)
			}
			ui.Outf("host files (drop-in, provision-host-config=%v):", provision)
			for _, hf := range hostFiles {
				status := "not merged"
				if hf.Merged {
					status = "merged"
				}
				ui.Outf("  %-9s %s  ->  %s", status, hf.HostPath, hf.VMPath)
			}
			return nil
		},
	}
}

func buildBuildCmd(ui termio.UI) *cobra.Command {
	cmd := &cobra.Command{
		Use:   cmdBuild,
		Args:  cobra.NoArgs,
		Short: "Build or rebuild the runner image",
		RunE: func(cmd *cobra.Command, _ []string) error {
			force, _ := cmd.Flags().GetBool(flagRebuild)
			dryRun, _ := cmd.Flags().GetBool(flagDryRun)
			openCodeVersion, _ := cmd.Flags().GetString(flagAgentVersion)
			if !cmd.Flags().Changed(flagAgentVersion) && cmd.Flags().Changed(flagOpenCodeVersion) {
				openCodeVersion, _ = cmd.Flags().GetString(flagOpenCodeVersion)
			}
			if dryRun {
				ui.Infof("dry-run: Would build runner image")
				return nil
			}
			if err := doctor.CheckDocker(cmd.Context()); err != nil {
				return fmt.Errorf("docker not available: %w", err)
			}
			a, err := resolveAgentFlag(cmd)
			if err != nil {
				return err
			}
			return image.Build(cmd.Context(), a, git.ProjectSlug(), image.BuildOptions{
				Force: force, OpenCodeVersion: openCodeVersion,
			}, ui)
		},
	}
	cmd.Flags().BoolP(flagRebuild, flagRebuild[:1], false, "Force a clean rebuild")
	cmd.Flags().BoolP(flagDryRun, flagDryRunShort, false, "Dry run without building")
	cmd.Flags().String(flagAgent, defaultAgentName, "Coding agent profile to build")
	cmd.Flags().
		String(flagAgentVersion, "", "Pin the agent version baked into the runner image (default: latest release)")
	cmd.Flags().
		String(flagOpenCodeVersion, "", "Deprecated alias for --agent-version")
	_ = cmd.Flags().MarkDeprecated(flagOpenCodeVersion, "use --agent-version instead")
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
	cmd.AddCommand(buildImagePruneCmd(ui))
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

	cmd.AddCommand(buildVolumePruneCmd(ui))
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
	cmd.AddCommand(buildSandboxPruneCmd(ui))
	return cmd
}

func buildPruneCmd(ui termio.UI) *cobra.Command {
	cmd := &cobra.Command{
		Use:   cmdPrune,
		Args:  cobra.NoArgs,
		Short: "Prune stale VMs, volumes, and images",
		RunE: func(cmd *cobra.Command, _ []string) error {
			age, dryRun, err := resolvePruneFlags(cmd)
			if err != nil {
				return err
			}
			return pruning.Prune(cmd.Context(), age, dryRun, ui)
		},
	}
	setPruneFlags(cmd)
	return cmd
}

// resolvePruneAge returns the effective prune threshold for a manual prune:
// --age if set, else manual-prune-age from config, else the 7d default.
func resolvePruneAge(cmd *cobra.Command) (time.Duration, error) {
	ageFlag := cmd.Flags().Lookup(flagAge)
	if !ageFlag.Changed {
		if r := resolverFromContext(cmd.Context()); r != nil && r.ManualPruneAge() > 0 {
			return r.ManualPruneAge(), nil
		}
		return 7 * 24 * time.Hour, nil
	}
	ageStr := ageFlag.Value.String()
	d, ok := viperconfig.ParseHumanDuration(ageStr)
	if !ok {
		return 0, fmt.Errorf("invalid age %q: use a Go duration or suffix d/w (e.g. 7d, 2w)", ageStr)
	}
	return d, nil
}

func buildImagePruneCmd(ui termio.UI) *cobra.Command {
	cmd := &cobra.Command{
		Use:   cmdPrune,
		Args:  cobra.NoArgs,
		Short: "Prune cached runner images not in use",
		RunE: func(cmd *cobra.Command, _ []string) error {
			age, dryRun, err := resolvePruneFlags(cmd)
			if err != nil {
				return err
			}
			return pruning.InvokePruneFunc(cmd.Context(), pruning.PruneImages, age, dryRun, ui)
		},
	}
	setPruneFlags(cmd)
	return cmd
}

func buildVolumePruneCmd(ui termio.UI) *cobra.Command {
	cmd := &cobra.Command{
		Use:   cmdPrune,
		Args:  cobra.NoArgs,
		Short: "Prune home volumes no longer referenced by a project VM",
		RunE: func(cmd *cobra.Command, _ []string) error {
			age, dryRun, err := resolvePruneFlags(cmd)
			if err != nil {
				return err
			}
			return pruning.InvokePruneFunc(cmd.Context(), pruning.PruneVolumes, age, dryRun, ui)
		},
	}
	setPruneFlags(cmd)
	return cmd
}

func resolvePruneFlags(cmd *cobra.Command) (time.Duration, bool, error) {
	age, err := resolvePruneAge(cmd)
	if err != nil {
		return 0 * time.Second, false, err
	}
	dryRun, _ := cmd.Flags().GetBool(flagDryRun)
	return age, dryRun, nil
}

func buildSandboxPruneCmd(ui termio.UI) *cobra.Command {
	cmd := &cobra.Command{
		Use:   cmdPrune,
		Args:  cobra.NoArgs,
		Short: "Prune stale sandboxes and leftover task workers",
		RunE: func(cmd *cobra.Command, _ []string) error {
			age, dryRun, err := resolvePruneFlags(cmd)
			if err != nil {
				return err
			}
			return pruning.InvokePruneFunc(cmd.Context(), pruning.PruneSandboxes, age, dryRun, ui)
		},
	}
	setPruneFlags(cmd)
	return cmd
}

func setPruneFlags(cmd *cobra.Command) {
	cmd.Flags().StringP(flagAge, flagAge[:1], "7d", "Prune threshold (default: manualPruneAge from config)")
	cmd.Flags().BoolP(flagDryRun, flagDryRunShort, false, "Show what would be pruned without deleting")
}
