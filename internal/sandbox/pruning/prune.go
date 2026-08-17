package pruning

import (
	"context"
	"errors"
	"time"

	"gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/msb"
	"gitlab.inoio.de/inoio/opencode-sandbox/internal/termio"
)

// MsbClient is a type alias for msb.Client, matching the original sandbox-level alias.
type MsbClient = msb.Client

// hasAnything reports whether the report contains any pruned items.
func (r *StaleReport) hasAnything() bool {
	if r == nil {
		return false
	}
	return r.PrunedVMs > 0 ||
		r.PrunedVolumes > 0 ||
		r.PrunedDockerImages > 0 ||
		r.PrunedMSBImages > 0
}

// Prune orchestrates all three pruners against one shared snapshot and prints a
// merged summary. Task sandboxes count into PrunedVMs.
//
// The keep-set comes from the snapshot taken before pruning runs, so a stale VM's
// home volumes and images are reclaimed even if the VM's own removal fails.
func Prune(ctx context.Context, threshold time.Duration, dryRun, autoPrune bool, ui termio.UI) error {
	snap, err := BuildLiveState(ctx, msb.Get(), threshold)
	if err != nil {
		return err
	}
	vmReport, vmErr := PruneVMs(ctx, snap, threshold, dryRun, ui)
	volReport, volErr := PruneVolumes(ctx, snap, threshold, false, dryRun, ui)
	imgReport, imgErr := PruneImages(ctx, snap, threshold, false, dryRun, ui)

	report := &StaleReport{
		PrunedVMs:          vmReport.VMsPruned,
		PrunedVolumes:      volReport.VolumesPruned,
		PrunedDockerImages: imgReport.DockerImagesPruned,
		PrunedMSBImages:    imgReport.MSBImagesPruned,
		Details:            append(append(vmReport.Details, volReport.Details...), imgReport.Details...),
	}
	printPruneSummary(ui, report, dryRun, autoPrune)
	return errors.Join(vmErr, volErr, imgErr)
}

func printPruneSummary(ui termio.UI, report *StaleReport, dryRun, autoPrune bool) {
	if report == nil {
		return
	}
	out := ui.Outf
	action := "Pruned"
	if autoPrune {
		out = ui.Verbosef
		action = "auto-prune: Pruned"
	}
	if dryRun {
		action = "dry-run: Would prune"
	}
	if !report.hasAnything() {
		out = ui.Verbosef
	}
	out(
		"%s %d VMs, %d home volumes, %d docker images, %d msb images",
		action,
		report.PrunedVMs,
		report.PrunedVolumes,
		report.PrunedDockerImages,
		report.PrunedMSBImages,
	)
}
