package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/inoio/agents-sandbox/internal/cliout"
	"github.com/inoio/agents-sandbox/internal/git"
	"github.com/inoio/agents-sandbox/internal/runoptions"
	"github.com/inoio/agents-sandbox/internal/sandbox/doctor"
	"github.com/inoio/agents-sandbox/internal/sandbox/image"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/sandbox/pruning"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	sandbox "github.com/inoio/agents-sandbox/internal/sandbox/vm"
	"github.com/inoio/agents-sandbox/internal/sandbox/volume"
	"github.com/inoio/agents-sandbox/internal/termio"
	"github.com/inoio/agents-sandbox/internal/view"
)

type volumeOpFunc func(context.Context, state.Key, string, image.ImageInfo, bool, bool, termio.UI) error

// volumeOp describes one `volume` subcommand that replaces the project's home
// volume (migrate, reset, edit).
type volumeOp struct {
	name       string
	run        volumeOpFunc
	short      string
	removeHelp string
}

var volumeOps = []volumeOp{
	{
		name:       cmdMigrate,
		run:        volume.CmdMigrate,
		short:      "Migrate: create new volume, copy files from existing volume on top",
		removeHelp: "Remove the old home volume after migration",
	},
	{
		name:       cmdReset,
		run:        volume.CmdReset,
		short:      "Reset: create new volume from image, ",
		removeHelp: "Remove the old home volume after reset",
	},
	{
		name:       cmdEdit,
		run:        volume.CmdEdit,
		short:      "Edit: create new volume alongside old one for manual transfer",
		removeHelp: "Remove the old home volume after editing",
	},
}

func buildVolumeOpsCmd(ui termio.UI, op volumeOp) *cobra.Command {
	var removeOld bool
	cmd := &cobra.Command{
		Use:   op.name,
		Args:  cobra.MaximumNArgs(1),
		Short: op.short,
		RunE: func(c *cobra.Command, args []string) error {
			if !doctor.CheckAll(c.Context(), ui) {
				return errors.New("preflight failed")
			}
			dryRun, _ := c.Flags().GetBool(flagDryRun)
			key, info, err := prepareVolumeOp(c, ui, dryRun)
			if err != nil {
				return err
			}
			var volumeName string
			if len(args) > 0 {
				volumeName = args[0]
			}
			return op.run(c.Context(), key, volumeName, info, removeOld, dryRun, ui)
		},
	}
	cmd.Flags().BoolVar(&removeOld, flagRemove, false, op.removeHelp)
	cmd.Flags().Bool(flagDryRun, false, "Show what would be done")
	cmd.Flags().String(flagAgent, defaultAgentName, "Coding agent profile")
	// --rebuild was dropped from these volume ops without a deprecation cycle
	// (migrate/reset shipped it in 0.5.0). Restore it on all three as a hidden
	// no-op for one cycle: they ensure the runner image but never force a clean
	// rebuild.
	cmd.Flags().Bool(flagRebuild, false, "Deprecated no-op; run 'agents-sandbox build' to rebuild the runner image")
	_ = cmd.Flags().MarkDeprecated(flagRebuild, "run 'agents-sandbox build' to rebuild the runner image")
	return cmd
}

// prepareVolumeOp resolves the agent, adopts a legacy project identity, and
// ensures the runner image a volume op seeds from, recording the release the
// ensured image bakes (these ops resolve latest, not the recorded baseline, but
// whatever they bake must still be the project's recorded release). It adopts
// like PrepareSandbox does and uses the adopted slug for both the image and the
// state key, never re-deriving it from git: adoption may have just moved the
// state, and a dry run deliberately returns the legacy repo slug while the
// migration stays pending. A dry run only reports the state-derived old
// volume, so it leaves the runner image untouched: ensuring it could build
// one, a side effect a preview must not have.
func prepareVolumeOp(c *cobra.Command, ui termio.UI, dryRun bool) (state.Key, image.ImageInfo, error) {
	a, err := resolveAgentFlag(c)
	if err != nil {
		return state.Key{}, image.ImageInfo{}, err
	}
	identity, err := git.ResolveIdentity(".")
	if err != nil {
		return state.Key{}, image.ImageInfo{}, err
	}
	adoption, err := sandbox.AdoptLegacyIdentity(
		c.Context(), msb.Get(), identity, a.Name(), !dryRun, ui,
	)
	if err != nil {
		return state.Key{}, image.ImageInfo{}, err
	}
	key := state.Key{Slug: adoption.Slug, Agent: a.Name()}
	if dryRun {
		return key, image.ImageInfo{}, nil
	}
	info, err := image.EnsureImage(
		c.Context(),
		a,
		adoption.Slug,
		image.BuildOptions{Force: false, AgentVersion: "", UserProvided: false, Docker: resolveBuildDockerSetting(c)},
		ui,
	)
	if err != nil {
		return state.Key{}, image.ImageInfo{}, fmt.Errorf("ensure image: %w", err)
	}
	// Record the release the ensured image bakes so the volume subcommands are
	// writers of record too, whatever release they resolve. A failure only warns:
	// the next run reconciles from the image label.
	if err := state.RecordAgentVersion(key, info.AgentVersion); err != nil {
		ui.Warnf("could not persist the agent version: %v (continuing)", err)
	}
	return key, info, nil
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
			mode, err := cliout.ResolveMode(format, ui.Quiet() || namesOnly)
			if err != nil {
				return err
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
			return cliout.PrintList(ui, mode, sandboxes, "No sandboxes found.", view.SandboxPrinter{})
		},
	}
	cmd.Flags().BoolVar(&namesOnly, pFlagNames, false, "Print only sandbox names")
	_ = cmd.Flags().MarkDeprecated(pFlagNames, "use --quiet")
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

// resolveBuildDockerSetting returns the effective docker setting for a build.
// It resolves through runoptions.ResolveDockerSetting — the same shared
// docker/dind precedence the run path uses, so a build and a run of one project
// cannot disagree — reading the resolver from the command context (a nil one
// resolves from the changed flags alone).
func resolveBuildDockerSetting(cmd *cobra.Command) options.DockerSetting {
	// Resolver.Config tolerates a nil resolver, so the flag-only path (no
	// resolver on the context) resolves from the flags alone.
	return runoptions.ResolveDockerSetting(cmd, resolverFromContext(cmd.Context()).Config())
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
			a, err := resolveAgentFlag(cmd)
			if err != nil {
				return err
			}
			// Check the Docker prerequisite before adopting, like run/shell and
			// the volume ops do: the check does not need the slug, and a failing
			// build must not destroy the legacy VM and move the state first.
			if !dryRun {
				if dockerErr := doctor.CheckDocker(cmd.Context()); dockerErr != nil {
					return fmt.Errorf("docker not available: %w", dockerErr)
				}
			}
			// Adopt like PrepareSandbox does, and use the adopted slug for the
			// image tag. It runs before the dry-run gate so the migration notice
			// prints; a dry run passes apply=false, so the migration stays
			// pending and adoption returns the legacy repo slug.
			identity, err := git.ResolveIdentity(".")
			if err != nil {
				return err
			}
			adoption, err := sandbox.AdoptLegacyIdentity(
				cmd.Context(), msb.Get(), identity, a.Name(), !dryRun, ui,
			)
			if err != nil {
				return err
			}
			if dryRun {
				ui.Infof("dry-run: Would build runner image")
				return nil
			}
			agentVersion, userProvided := sandbox.ResolveAgentVersionForBuild(
				state.Key{Slug: adoption.Slug, Agent: a.Name()},
				openCodeVersion,
			)
			info, err := image.Build(cmd.Context(), a, adoption.Slug, image.BuildOptions{
				Force:        force,
				AgentVersion: agentVersion,
				UserProvided: userProvided,
				Docker:       resolveBuildDockerSetting(cmd),
			}, ui)
			if err != nil {
				return err
			}
			// Record the release the built image reports so an explicit
			// --agent-version pin persists for later unpinned runs. A failure
			// only warns: the next run reconciles from the image label.
			if err := state.RecordAgentVersion(
				state.Key{Slug: adoption.Slug, Agent: a.Name()}, info.AgentVersion,
			); err != nil {
				ui.Warnf("could not persist the agent version: %v (continuing)", err)
			}
			return nil
		},
	}
	cmd.Flags().BoolP(flagRebuild, flagRebuild[:1], false, "Force a clean rebuild")
	cmd.Flags().BoolP(flagDryRun, flagDryRunShort, false, "Dry run without building")
	cmd.Flags().String(flagAgent, defaultAgentName, "Coding agent profile to build")
	registerDockerFlags(cmd)
	cmd.Flags().
		String(flagAgentVersion, "", "Pin the agent version baked into the runner image (default: latest release)")
	cmd.Flags().
		String(flagOpenCodeVersion, "", "Deprecated alias for --agent-version")
	_ = cmd.Flags().MarkDeprecated(flagOpenCodeVersion, "use --agent-version instead")

	cmd.AddCommand(buildDockerfileCmd(ui))
	return cmd
}

// buildDockerfileCmd returns the "build dockerfile" subcommand, which prints
// the runner Dockerfile exactly as it would be built without invoking docker.
func buildDockerfileCmd(ui termio.UI) *cobra.Command {
	cmd := &cobra.Command{
		Use:   cmdDockerfile,
		Args:  cobra.NoArgs,
		Short: "Print the runner Dockerfile as it would be built",
		RunE: func(cmd *cobra.Command, _ []string) error {
			image.WarnIgnoredProjectConfig(ui)
			a, err := resolveAgentFlag(cmd)
			if err != nil {
				return err
			}
			setting := resolveBuildDockerSetting(cmd)
			image.EmitProjectDockerfileDiagnostics(ui, setting)
			agentVersion, _ := cmd.Flags().GetString(flagAgentVersion)
			out, err := image.RenderProjectDockerfile(a, agentVersion, setting)
			if err != nil {
				return err
			}
			if _, err := ui.StdOut().Write(out); err != nil {
				return err
			}
			return nil
		},
	}
	cmd.Flags().String(flagAgent, defaultAgentName, "Coding agent profile to build")
	cmd.Flags().
		String(flagAgentVersion, "", "Pin the agent version rendered into the preview (default: no pinned default)")
	registerDockerFlags(cmd)
	return cmd
}

func buildImageCmd(ui termio.UI) *cobra.Command {
	cmd := &cobra.Command{
		Use:     cmdImage,
		Aliases: cmdImageAliases,
		Args:    cobra.NoArgs,
		Short:   "Manage runner images",
	}
	var format string
	listCmd := &cobra.Command{
		Use:     cmdList,
		Args:    cobra.NoArgs,
		Aliases: cmdListAliases,
		Short:   "List cached runner images",
		RunE: func(cmd *cobra.Command, _ []string) error {
			mode, err := cliout.ResolveMode(format, ui.Quiet())
			if err != nil {
				return err
			}
			images, err := image.ListImages(cmd.Context())
			if err != nil {
				return err
			}
			return cliout.PrintList(ui, mode, images, "No images found.", view.ImagePrinter{})
		},
	}
	listCmd.Flags().StringVar(&format, flagFormat, "", "Output format (json)")
	cmd.AddCommand(listCmd)
	cmd.AddCommand(buildImageInspectCmd(ui))
	cmd.AddCommand(buildImageRemoveCmd(ui))
	cmd.AddCommand(buildBuildCmd(ui))
	cmd.AddCommand(buildImagePruneCmd(ui))
	return cmd
}

// buildImageRemoveCmd builds `image remove <ref>...`, which deletes cached
// runner images by reference. It refuses when an image is still in use unless
// --force is set, and prompts unless --yes is set.
func buildImageRemoveCmd(ui termio.UI) *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:     cmdRemove + " <ref>...",
		Aliases: cmdRemoveAliases,
		Args:    cobra.MinimumNArgs(1),
		Short:   "Remove cached runner images by reference",
		Long: "Remove the named cached runner images. Refuses when an image is still in use by a " +
			"project's state or a live sandbox; pass --force to remove it anyway. Prompts unless --yes is set.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return image.RemoveImages(cmd.Context(), args, force, ui)
		},
	}
	cmd.Flags().
		BoolVarP(&force, flagForce, flagForce[:1], false, "Remove even when a project or sandbox still references the image")
	return cmd
}

func buildVolumeCmd(ui termio.UI) *cobra.Command {
	cmd := &cobra.Command{
		Use:     cmdVolume,
		Aliases: cmdVolumeAliases,
		Short:   "Manage home volumes",
	}
	var format string
	listCmd := &cobra.Command{
		Use:     cmdList,
		Aliases: cmdListAliases,
		Args:    cobra.NoArgs,
		Short:   "List managed volumes",
		RunE: func(cmd *cobra.Command, _ []string) error {
			mode, err := cliout.ResolveMode(format, ui.Quiet())
			if err != nil {
				return err
			}
			volumes, err := volume.ListVolumes(cmd.Context())
			if err != nil {
				return err
			}
			return cliout.PrintList(ui, mode, volumes, "No volumes found.", view.VolumePrinter{})
		},
	}
	listCmd.Flags().StringVar(&format, flagFormat, "", "Output format (json)")
	cmd.AddCommand(listCmd)
	cmd.AddCommand(buildVolumeInspectCmd(ui))

	for _, op := range volumeOps {
		cmd.AddCommand(buildVolumeOpsCmd(ui, op))
	}
	cmd.AddCommand(buildVolumeRemoveCmd(ui))
	cmd.AddCommand(buildVolumePruneCmd(ui))
	return cmd
}

// buildVolumeRemoveCmd builds `volume remove <name>...`, which deletes home
// volumes by name. It refuses when a project still references a volume unless
// --force is set, and prompts unless --yes is set.
func buildVolumeRemoveCmd(ui termio.UI) *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:     cmdRemove + " <name>...",
		Aliases: cmdRemoveAliases,
		Args:    cobra.MinimumNArgs(1),
		Short:   "Remove home volumes by name",
		Long: "Remove the named home volumes. Refuses when a volume is still referenced by a " +
			"project's state; pass --force to remove it anyway. Prompts unless --yes is set.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return volume.RemoveVolumes(cmd.Context(), args, force, ui)
		},
	}
	cmd.Flags().
		BoolVarP(&force, flagForce, flagForce[:1], false, "Remove even when a project still references the volume")
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
	cmd.AddCommand(buildSandboxInspectCmd(ui))
	cmd.AddCommand(buildSandboxRemoveCmd(ui))
	cmd.AddCommand(buildSandboxPruneCmd(ui))
	cmd.AddCommand(buildSandboxCompactCmd(ui))
	return cmd
}

// buildSandboxRemoveCmd builds `sandbox remove <name>`, which removes a project
// VM by name. It refuses a running VM unless --force is set (which stops it
// first); the persisted project state is left intact so the next run reuses the
// recorded home volume.
func buildSandboxRemoveCmd(ui termio.UI) *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:     cmdRemove + " <name>",
		Aliases: cmdRemoveAliases,
		Args:    cobra.ExactArgs(1),
		Short:   "Remove a project sandbox VM by name",
		Long: "Remove an agents-sandbox project VM. Refuses a running VM unless --force is set, " +
			"which stops it first. The project's persisted state is left intact. Prompts unless --yes is set.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return sandbox.RemoveSandbox(cmd.Context(), args[0], force, ui)
		},
	}
	cmd.Flags().BoolVarP(&force, flagForce, flagForce[:1], false, "Remove a running VM (stopping it first)")
	return cmd
}

func buildPruneCmd(ui termio.UI) *cobra.Command {
	cmd := &cobra.Command{
		Use:   cmdPrune,
		Args:  cobra.NoArgs,
		Short: "Prune stale VMs, volumes, and images",
		Annotations: map[string]string{
			annotationAlsoAs: cmdStorage + " " + cmdPrune,
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			age, dryRun, err := resolvePruneFlags(cmd)
			if err != nil {
				return err
			}
			mode, err := resolvePruneOutputMode(cmd, ui)
			if err != nil {
				return err
			}
			if err = confirmPrune(ui, dryRun); err != nil {
				return err
			}
			report, pruneErr := pruning.Prune(cmd.Context(), age, dryRun, ui)
			if report != nil {
				cliout.RenderPruneReport(ui, mode,
					func() { pruning.RenderPruneSummary(ui, report, dryRun) },
					view.NewPruneReportJSON(report, dryRun))
			}
			return pruneErr
		},
	}
	setPruneFlags(cmd)
	return cmd
}

func buildImagePruneCmd(ui termio.UI) *cobra.Command {
	cmd := &cobra.Command{
		Use:   cmdPrune,
		Args:  cobra.NoArgs,
		Short: "Prune cached runner images not in use",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runGroupPrune(cmd, ui, pruning.PruneImages,
				func(report pruning.ImageReport, dryRun bool) { pruning.RenderImageReport(ui, report, dryRun) },
				func(report pruning.ImageReport, dryRun bool) any { return view.NewImageReportJSON(report, dryRun) })
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
			return runGroupPrune(
				cmd,
				ui,
				pruning.PruneVolumes,
				func(report pruning.VolumeReport, dryRun bool) { pruning.RenderVolumeReport(ui, report, dryRun) },
				func(report pruning.VolumeReport, dryRun bool) any { return view.NewVolumeReportJSON(report, dryRun) },
			)
		},
	}
	setPruneFlags(cmd)
	return cmd
}

func buildSandboxPruneCmd(ui termio.UI) *cobra.Command {
	cmd := &cobra.Command{
		Use:   cmdPrune,
		Args:  cobra.NoArgs,
		Short: "Prune stale sandboxes and leftover task workers",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runGroupPrune(cmd, ui, pruning.PruneSandboxes,
				func(report pruning.SandboxReport, dryRun bool) { pruning.RenderSandboxReport(ui, report, dryRun) },
				func(report pruning.SandboxReport, dryRun bool) any {
					return view.NewSandboxReportJSON(report, dryRun)
				})
		},
	}
	setPruneFlags(cmd)
	return cmd
}
