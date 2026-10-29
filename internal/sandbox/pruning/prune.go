package pruning

import (
	"context"
	"errors"
	"time"

	"github.com/inoio/agents-sandbox/internal/humanize"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// MsbClient is a type alias for msb.Client, matching the original sandbox-level alias.
type MsbClient = msb.Client

// Prune orchestrates all three pruners against one shared snapshot and returns a
// merged report. Task sandboxes count into PrunedVMs.
//
// The keep-set comes from the snapshot taken before pruning runs, so a stale VM's
// home volumes and images are reclaimed even if the VM's own removal fails.
// retain names the keys the current invocation is about to use (a `run`/`shell`
// passes its own project key): their images survive regardless of VM liveness or
// age, so a run's startup prune never reclaims the image it is about to import.
func Prune(
	ctx context.Context,
	threshold time.Duration,
	dryRun bool,
	ui termio.UI,
	retain ...state.Key,
) (*StaleReport, error) {
	pruneState, err := buildPruneState(ctx, threshold)
	if err != nil {
		return nil, err
	}
	pruneState.RetainKeys = keySet(retain)
	vmReport, vmErr := PruneSandboxes(ctx, pruneState, dryRun, ui)
	volReport, volErr := PruneVolumes(ctx, pruneState, dryRun, ui)
	imgReport, imgErr := PruneImages(ctx, pruneState, dryRun, ui)
	storageReport, storageErr := PruneStorage(ctx, pruneState, dryRun, ui)

	report := &StaleReport{
		PrunedSandboxes:     vmReport.VMsPruned,
		PrunedVolumes:       volReport.VolumesPruned,
		PrunedDockerImages:  imgReport.DockerImagesPruned,
		PrunedSandboxImages: imgReport.MSBImagesPruned,
		SkippedImagesInUse:  imgReport.SkippedInUse,
		ImageBytesReclaimed: imgReport.BytesReclaimed,
		Storage:             storageReport,
		Details:             append(append(vmReport.Details, volReport.Details...), imgReport.Details...),
	}
	return report, errors.Join(vmErr, volErr, imgErr, storageErr)
}

func RenderPruneSummary(ui termio.UI, report *StaleReport, dryRun bool) {
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
	if report.SkippedImagesInUse > 0 || report.ImageBytesReclaimed != nil {
		ui.Outf(
			"  image data: %d skipped (in use), %s reclaimed",
			report.SkippedImagesInUse,
			formatOptionalBytes(report.ImageBytesReclaimed),
		)
	}
	if report.Storage.FilesRemoved > 0 || report.Storage.LogicalBytesRemoved > 0 ||
		report.Storage.PhysicalBytesReclaimed != nil {
		ui.Outf(
			"  runtime memory: %d file(s), %s logical, %s reclaimed",
			report.Storage.FilesRemoved,
			humanize.FormatBytes(report.Storage.LogicalBytesRemoved),
			formatOptionalBytes(report.Storage.PhysicalBytesReclaimed),
		)
	}
	for _, detail := range report.Details {
		ui.Verbosef("  %s (%s)", detail.Name, detail.Slug)
	}
}
