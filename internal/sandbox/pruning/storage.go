package pruning

import (
	"context"
	"time"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// PruneStorage reclaims unused runtime RAM backings (the published branch and
// snapshot memory) older than the prune age. It runs even on a dry run, with the
// runtime's own DryRun flag, so the user can preview what it would reclaim; a
// failure is warned and returned so the aggregate prune reports it without
// aborting the other phases.
func PruneStorage(
	ctx context.Context,
	pruneState PruneState,
	dryRun bool,
	ui termio.UI,
) (StorageReport, error) {
	report, err := msb.Get().PruneStorage(ctx, msbSdk.StoragePruneOptions{
		DryRun:           dryRun,
		OlderThanSeconds: pruneAgeSeconds(pruneState.Age),
	})
	if err != nil {
		ui.Warnf("failed to prune runtime memory: %v", err)
		return StorageReport{}, err
	}
	return StorageReport{
		FilesRemoved:           report.FilesRemoved,
		LogicalBytesRemoved:    report.LogicalBytesRemoved,
		PhysicalBytesReclaimed: report.PhysicalBytesReclaimed,
	}, nil
}

// pruneAgeSeconds converts the prune threshold to the whole seconds the runtime
// API expects. A non-positive age disables the age filter, matching the runtime's
// zero value and the other phases' zero-age behavior. A positive age rounds up to
// the next whole second so a subsecond threshold (the CLI accepts `500ms`) never
// collapses to zero and silently disables filtering.
func pruneAgeSeconds(age time.Duration) uint64 {
	if age <= 0 {
		return 0
	}
	return uint64((age-1)/time.Second + 1)
}
