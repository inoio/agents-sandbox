package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/inoio/agents-sandbox/internal/cliout"
	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/runoptions"
	"github.com/inoio/agents-sandbox/internal/sandbox/docker"
	"github.com/inoio/agents-sandbox/internal/sandbox/doctor"
	"github.com/inoio/agents-sandbox/internal/sandbox/image"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/sandbox/prepare"
	"github.com/inoio/agents-sandbox/internal/sandbox/pruning"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	sandbox "github.com/inoio/agents-sandbox/internal/sandbox/vm"
	"github.com/inoio/agents-sandbox/internal/sandbox/volume"
	"github.com/inoio/agents-sandbox/internal/termio"
	"github.com/inoio/agents-sandbox/internal/view"
)

type volumeOpFunc func(
	context.Context,
	docker.Client,
	state.Key,
	string,
	image.ImageInfo,
	bool,
	bool,
	termio.UI,
) error

// agentVersionResolver resolves the agent release to bake into the runner image
// when no version is pinned. It is the command boundary's injected dependency,
// so the image package holds no process-global test seam; tests replace it to
// keep a build off the network.
var agentVersionResolver image.AgentVersionResolver = image.DefaultAgentVersionResolver

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
			if !doctor.CheckAll(c.Context(), docker.Get(), ui) {
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
			return op.run(c.Context(), docker.Get(), key, volumeName, info, removeOld, dryRun, ui)
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

// prepareVolumeOp resolves the agent and ensures the runner image a volume op
// seeds from through adoptAndPrepareImage. These ops resolve the latest release,
// not the recorded baseline, but whatever they bake is still recorded as the
// project's release. A dry run only reports the state-derived old volume, so it
// leaves the runner image untouched.
func prepareVolumeOp(c *cobra.Command, ui termio.UI, dryRun bool) (state.Key, image.ImageInfo, error) {
	a, err := resolveAgentFlag(c)
	if err != nil {
		return state.Key{}, image.ImageInfo{}, err
	}
	paths := configpaths.Get()
	ensureImage := func(ctx context.Context, key state.Key) (image.ImageInfo, error) {
		info, err := image.EnsureImage(
			ctx,
			docker.Get(),
			paths,
			a,
			key.Slug,
			image.ReadProjectDockerfile(paths),
			image.BuildOptions{
				Force:          false,
				NoRebuild:      false,
				AgentVersion:   "",
				UserProvided:   false,
				Docker:         resolveBuildDockerSetting(c),
				ResolveVersion: agentVersionResolver,
				// The volume ops seed from an image but never publish or consume
				// the CI build cache.
				CacheFrom: nil,
				CacheTo:   nil,
			},
			ui,
		)
		if err != nil {
			return image.ImageInfo{}, fmt.Errorf("ensure image: %w", err)
		}
		return info, nil
	}
	return adoptAndPrepareImage(c.Context(), paths, msb.Get(), a.Name(), dryRun, ui, ensureImage)
}

func buildDoctorCmd(ui termio.Logger) *cobra.Command {
	return &cobra.Command{
		Use:   cmdDoctor,
		Args:  cobra.NoArgs,
		Short: "Check prerequisites",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !doctor.CheckAll(cmd.Context(), docker.Get(), ui) {
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
			if limit != 0 {
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
			agentVersionPin, _ := cmd.Flags().GetString(flagAgentVersion)
			a, err := resolveAgentFlag(cmd)
			if err != nil {
				return err
			}
			// Check the Docker prerequisite before adopting, like run/shell and
			// the volume ops do: the check does not need the slug, and a failing
			// build must not destroy the legacy VM and move the state first.
			if !dryRun {
				if dockerErr := doctor.CheckDocker(cmd.Context(), docker.Get()); dockerErr != nil {
					return fmt.Errorf("docker not available: %w", dockerErr)
				}
			}
			client := msb.Get()
			paths := configpaths.Get()
			buildImage := func(ctx context.Context, key state.Key) (image.ImageInfo, error) {
				agentVersion, userProvided := prepare.ResolveAgentVersionForBuild(key, agentVersionPin)
				return image.Build(
					ctx,
					docker.Get(),
					client,
					paths,
					a,
					key.Slug,
					image.ReadProjectDockerfile(paths),
					image.BuildOptions{
						Force:          force,
						NoRebuild:      false,
						AgentVersion:   agentVersion,
						UserProvided:   userProvided,
						Docker:         resolveBuildDockerSetting(cmd),
						ResolveVersion: agentVersionResolver,
						// The system build command runs on a developer machine,
						// not in CI, so it never publishes or consumes the CI
						// build cache.
						CacheFrom: nil,
						CacheTo:   nil,
					},
					ui,
				)
			}
			if _, _, err := adoptAndPrepareImage(
				cmd.Context(),
				paths,
				client,
				a.Name(),
				dryRun,
				ui,
				buildImage,
			); err != nil {
				return err
			}
			if dryRun {
				ui.Infof("dry-run: Would build runner image")
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
			paths := configpaths.Get()
			image.WarnIgnoredProjectConfig(paths, ui)
			a, err := resolveAgentFlag(cmd)
			if err != nil {
				return err
			}
			setting := resolveBuildDockerSetting(cmd)
			projectDockerfile := image.ReadProjectDockerfile(paths)
			image.EmitProjectDockerfileDiagnostics(ui, projectDockerfile, setting)
			agentVersion, _ := cmd.Flags().GetString(flagAgentVersion)
			out, err := image.RenderProjectDockerfile(a, agentVersion, projectDockerfile, setting)
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
func buildImageRemoveCmd(ui termio.Prompter) *cobra.Command {
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

// volumeRemoveUI is the role set `volume remove` needs: confirm the removal and
// report each removed volume. It is narrower than termio.UI.
type volumeRemoveUI interface {
	termio.Prompter
	termio.Logger
}

// buildVolumeRemoveCmd builds `volume remove <name>...`, which deletes home
// volumes by name. It refuses when a project still references a volume unless
// --force is set, and prompts unless --yes is set.
func buildVolumeRemoveCmd(ui volumeRemoveUI) *cobra.Command {
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

// buildSandboxRemoveCmd builds `sandbox remove [name]`, which removes a project
// VM by name. It refuses a running VM unless --force is set (which stops it
// first); the persisted project state is left intact so the next run reuses the
// recorded home volume. With --purge it removes the whole project instead: the
// name (a project slug or a <slug>-<agent> key) is optional and defaults to the
// current checkout, and the shared purge engine removes the project's VMs, home
// volumes, runner images, state and claims after a single confirmation.
func buildSandboxRemoveCmd(ui purgeUI) *cobra.Command {
	var (
		force bool
		purge bool
	)
	cmd := &cobra.Command{
		Use:     cmdRemove + " [name]",
		Aliases: cmdRemoveAliases,
		Args:    sandboxRemoveArgs,
		Short:   "Remove a project sandbox VM by name",
		Long: "Remove an agents-sandbox project VM. Refuses a running VM unless --force is set, " +
			"which stops it first. The project's persisted state is left intact. Prompts unless --yes is set. " +
			"With --purge the optional name is a project slug or a <slug>-<agent> key, defaulting to the " +
			"current checkout, and the project's state, home volumes, runner images and sandboxes are removed too.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if !purge {
				return sandbox.RemoveSandbox(cmd.Context(), args[0], force, ui)
			}
			target := ""
			if len(args) == 1 {
				target = args[0]
			}
			return runProjectPurge(cmd, ui, cliout.ModeTable, false, force, target)
		},
	}
	cmd.Flags().BoolVarP(&force, flagForce, flagForce[:1], false, "Remove a running VM (stopping it first)")
	cmd.Flags().BoolVar(&purge, flagPurge, false,
		"Purge the project's artifacts (state, home volumes, runner images, sandboxes) instead of one VM")
	return cmd
}

// sandboxRemoveArgs requires exactly one argument unless --purge is set, where
// the target is optional and an omitted one resolves from the cwd.
func sandboxRemoveArgs(cmd *cobra.Command, args []string) error {
	if purge, _ := cmd.Flags().GetBool(flagPurge); purge {
		return cobra.MaximumNArgs(1)(cmd, args)
	}
	return cobra.ExactArgs(1)(cmd, args)
}

func buildPruneCmd(ui termio.UI) *cobra.Command {
	cmd := &cobra.Command{
		Use:   cmdPrune + " [target]",
		Args:  cobra.MaximumNArgs(1),
		Short: "Prune stale VMs, images, and internal state",
		Long: "Prune stale runtime objects. Without --purge this is list-and-prune only: it never " +
			"removes a home volume or a retired project's state, and lists the retired projects it " +
			"observes with a hint to purge them. With --purge it removes one project's artifacts " +
			"(its VMs, home volumes, runner images, state and claims); the optional target is a " +
			"project slug or a <slug>-<agent> key, defaulting to the current checkout.",
		Annotations: map[string]string{
			annotationAlsoAs: cmdStorage + " " + cmdPrune,
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			age, dryRun, err := resolvePruneFlags(cmd)
			if err != nil {
				return err
			}
			mode, err := resolvePruneOutputMode(cmd, ui)
			if err != nil {
				return err
			}
			if cmd.Flags().Changed(flagPurge) {
				return runPrunePurge(cmd, ui, mode, dryRun, purgeTarget(cmd, args))
			}
			if len(args) > 0 {
				return fmt.Errorf("unexpected argument %q: pass --purge to purge a project", args[0])
			}
			return runPruneBatch(cmd, ui, mode, age, dryRun)
		},
	}
	setPruneFlags(cmd)
	cmd.Flags().String(flagPurge, "",
		"Purge a retired project instead of pruning (optional <slug> or <slug>-<agent>; default: current checkout)")
	cmd.Flags().Lookup(flagPurge).NoOptDefVal = purgeCwdTarget
	cmd.Flags().BoolP(flagForce, flagForce[:1], false, "Purge a project even when one of its sandboxes is running")
	return cmd
}

func buildImagePruneCmd(ui termio.UI) *cobra.Command {
	cmd := &cobra.Command{
		Use:   cmdPrune,
		Args:  cobra.NoArgs,
		Short: "Prune cached runner images not in use",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runGroupPrune(
				cmd,
				ui,
				func(ctx context.Context, pruneState pruning.PruneState, dryRun bool, ui termio.UI) (pruning.ImageReport, error) {
					return pruning.PruneImages(ctx, docker.Get(), pruneState, dryRun, ui)
				},
				func(report pruning.ImageReport, dryRun bool) { pruning.RenderImageReport(ui, report, dryRun) },
				func(report pruning.ImageReport, dryRun bool) any { return view.NewImageReportJSON(report, dryRun) },
			)
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
