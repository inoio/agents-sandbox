package pruning

import (
	"context"
	"errors"
	"time"

	"github.com/inoio/agents-sandbox/internal/humanize"
	"github.com/inoio/agents-sandbox/internal/sandbox/docker"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// MsbClient is a type alias for msb.Client, matching the original sandbox-level alias.
type MsbClient = msb.Client

// Prune orchestrates the safe-tier pruners against one shared snapshot and
// returns a merged report. Task sandboxes count into PrunedVMs.
//
// The keep-set comes from the snapshot taken before pruning runs.
// opts.Retain names the keys the current invocation is about to use (a `run`/`shell`
// passes its own project key): their images survive regardless of VM liveness or
// age, so a run's startup prune never reclaims the image it is about to import.
//
// Prune never removes a home volume. The volume phase is unreachable here by
// design: it lives in PruneVolumes, which only the `volume prune` command calls,
// so batch and auto-prune can never destroy one. StaleReport.PrunedVolumes
// therefore stays zero.
func Prune(
	ctx context.Context,
	dclient docker.Client,
	opts PruneOptions,
	ui termio.UI,
) (*StaleReport, error) {
	pruneState, err := buildPruneState(ctx, opts.Age, opts.Retain...)
	if err != nil {
		return nil, err
	}
	vmReport, vmErr := PruneSandboxes(ctx, pruneState, opts.DryRun, ui)
	imgReport, imgErr := PruneImages(ctx, dclient, pruneState, opts.DryRun, ui)
	storageReport, storageErr := PruneStorage(ctx, pruneState, opts.DryRun, ui)
	// The state phase runs after the sandbox/image phases so a just-retired
	// key's state is swept, and never in a dry-run: reclaiming a state file is a
	// change, and --dry-run makes no changes. It is the safe tier, so a state
	// that records a surviving home volume is always kept.
	var stateReport StateSweepReport
	var stateErr error
	if !opts.DryRun {
		stateReport, stateErr = PruneStateArtifacts(ctx, ui)
	}

	report := &StaleReport{
		PrunedSandboxes:     vmReport.VMsPruned,
		PrunedVolumes:       0,
		PrunedDockerImages:  imgReport.DockerImagesPruned,
		PrunedSandboxImages: imgReport.MSBImagesPruned,
		SkippedImagesInUse:  imgReport.SkippedInUse,
		ImageBytesReclaimed: imgReport.BytesReclaimed,
		Storage:             storageReport,
		State:               stateReport,
		Details:             append(vmReport.Details, imgReport.Details...),
	}
	return report, errors.Join(vmErr, imgErr, storageErr, stateErr)
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
	if report.State.ClaimsRemoved > 0 || report.State.LeasesRemoved > 0 ||
		report.State.StatesRemoved > 0 || report.State.DirsRemoved > 0 {
		ui.Outf(
			"  internal state: %d claims, %d leases, %d state files removed, %d kept, %d dirs removed",
			report.State.ClaimsRemoved,
			report.State.LeasesRemoved,
			report.State.StatesRemoved,
			report.State.StaleStatesKept,
			report.State.DirsRemoved,
		)
	}
	for _, detail := range report.Details {
		ui.Verbosef("  %s (%s)", detail.Name, detail.Slug)
	}
}

func InvokePruneFunc[R any](
	context context.Context,
	pruneLambda func(context.Context,
		PruneState,
		bool,
		termio.UI,
	) (R, error),
	age time.Duration,
	dryRun bool,
	ui termio.UI,
) (R, error) {
	pruneState, err := buildPruneState(context, age)
	if err != nil {
		var zero R
		return zero, err
	}
	return pruneLambda(context, pruneState, dryRun, ui)
}
