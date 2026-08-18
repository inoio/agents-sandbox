package pruning

import (
	"context"
	"errors"
	"time"

	"github.com/inoio/opencode-sandbox/internal/sandbox/msb"
	"github.com/inoio/opencode-sandbox/internal/termio"
)

// MsbClient is a type alias for msb.Client, matching the original sandbox-level alias.
type MsbClient = msb.Client

// Prune orchestrates all three pruners against one shared snapshot and prints a
// merged summary. Task sandboxes count into PrunedVMs.
//
// The keep-set comes from the snapshot taken before pruning runs, so a stale VM's
// home volumes and images are reclaimed even if the VM's own removal fails.
func Prune(ctx context.Context, threshold time.Duration, dryRun bool, ui termio.UI) error {
	pruneState, err := buildPruneState(ctx, threshold)
	if err != nil {
		return err
	}
	outToVerboseUI := &termio.OutToVerboseRedirect{UI: ui}
	vmReport, vmErr := PruneSandboxes(ctx, pruneState, dryRun, outToVerboseUI)
	volReport, volErr := PruneVolumes(ctx, pruneState, dryRun, outToVerboseUI)
	imgReport, imgErr := PruneImages(ctx, pruneState, dryRun, outToVerboseUI)

	report := &StaleReport{
		PrunedSandboxes:     vmReport.VMsPruned,
		PrunedVolumes:       volReport.VolumesPruned,
		PrunedDockerImages:  imgReport.DockerImagesPruned,
		PrunedSandboxImages: imgReport.MSBImagesPruned,
		Details:             append(append(vmReport.Details, volReport.Details...), imgReport.Details...),
	}
	printPruneSummary(ui, report, dryRun)
	return errors.Join(vmErr, volErr, imgErr)
}

func printPruneSummary(ui termio.UI, report *StaleReport, dryRun bool) {
	if report == nil {
		return
	}
	action := "Pruned"
	if dryRun {
		action = "dry-run: Would prune"
	}
	ui.Outf(
		"%s %d VMs, %d home volumes, %d docker images, %d msb images",
		action,
		report.PrunedSandboxes,
		report.PrunedVolumes,
		report.PrunedDockerImages,
		report.PrunedSandboxImages,
	)
}
