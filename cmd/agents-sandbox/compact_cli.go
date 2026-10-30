package main

import (
	"errors"

	"github.com/spf13/cobra"
	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/cliout"
	"github.com/inoio/agents-sandbox/internal/sandbox/pruning"
	"github.com/inoio/agents-sandbox/internal/termio"
	"github.com/inoio/agents-sandbox/internal/view"
)

// minimumCompactionLayers is the smallest layer count the runtime can merge.
const minimumCompactionLayers = 2

// buildSandboxCompactCmd builds `sandbox compact [name]`, which merges the
// sealed copy-on-write layers of project VM disks. It is opt-in only: compaction
// briefly pauses a running VM, so it never runs on the automatic prune path.
func buildSandboxCompactCmd(ui termio.UI) *cobra.Command {
	cmd := &cobra.Command{
		Use:   cmdCompact + " [name]",
		Args:  cobra.MaximumNArgs(1),
		Short: "Merge the copy-on-write disk layers of project sandbox VMs",
		Long: "Merge the oldest sealed copy-on-write layers of each project VM's disks (or only the named VM). " +
			"Running VMs are paused briefly. Named volumes such as the home volume are never compacted.",
		RunE: func(cmd *cobra.Command, args []string) error {
			options, err := resolveCompactOptions(cmd)
			if err != nil {
				return err
			}
			mode, err := resolvePruneOutputMode(cmd, ui)
			if err != nil {
				return err
			}
			sandboxName := ""
			if len(args) == 1 {
				sandboxName = args[0]
			}
			report, err := pruning.CompactSandboxes(cmd.Context(), sandboxName, options, ui)
			if err != nil {
				return err
			}
			cliout.RenderPruneReport(ui, mode,
				func() { pruning.RenderCompactReport(ui, report) },
				view.NewCompactReportJSON(report))
			return nil
		},
	}
	cmd.Flags().BoolP(flagDryRun, flagDryRunShort, false, "Show what would be compacted without changing disks")
	cmd.Flags().String(flagDisk, "", "Compact only the disk mounted at this guest path (\"/\" is the root disk)")
	cmd.Flags().Bool(flagRootOnly, false, "Compact only the root disk")
	// gomutants:disable-next-line INTEGER_INCREMENT reason="the default is never read: Layers is only set when --layers was given explicitly"
	cmd.Flags().Uint32(flagLayers, 0, "Merge only the oldest N sealed layers (at least 2; default: all)")
	cmd.Flags().String(flagFormat, "", "Output format (json)")
	cmd.MarkFlagsMutuallyExclusive(flagDisk, flagRootOnly)
	return cmd
}

func resolveCompactOptions(cmd *cobra.Command) (msbSdk.DiskCompactionOptions, error) {
	flags := cmd.Flags()
	dryRun, _ := flags.GetBool(flagDryRun)
	disk, _ := flags.GetString(flagDisk)
	rootDiskOnly, _ := flags.GetBool(flagRootOnly)
	options := msbSdk.DiskCompactionOptions{DryRun: dryRun, Disk: disk, RootDiskOnly: rootDiskOnly, Layers: nil}
	if flags.Changed(flagLayers) {
		layers, _ := flags.GetUint32(flagLayers)
		if layers < minimumCompactionLayers {
			return msbSdk.DiskCompactionOptions{}, errors.New("--layers must be at least 2")
		}
		options.Layers = &layers
	}
	return options, nil
}
