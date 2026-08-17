package pruning

import (
	"context"
	"time"

	"gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/msb"
	"gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/naming"
	"gitlab.inoio.de/inoio/opencode-sandbox/internal/termio"
)

// VMReport summarizes a PruneVMs run.
type VMReport struct {
	VMsPruned int
	Details   []StaleEntry
}

// PruneVMs prunes stale VMs and stopped task sandboxes. Task sandboxes have no
// age gate (transient workers) but running ones are skipped. A stopped VM is
// pruned only when older than threshold (age 0 = no wait).
//
//nolint:revive // snap is reserved for the decoupled per-type interface contract.
func PruneVMs(
	ctx context.Context,
	snap LiveState,
	threshold time.Duration,
	dryRun bool,
	ui termio.UI,
) (VMReport, error) {
	report := VMReport{VMsPruned: 0, Details: nil}
	handles, err := msb.Get().ListSandboxes(ctx, nil)
	if err != nil {
		return report, err
	}
	for _, h := range handles {
		name := h.Name()
		if msb.IsSandboxActive(h.Status()) {
			continue
		}
		isTask := hasPrefix(name, naming.TaskPrefix)
		if !isTask && !hasPrefix(name, naming.VmPrefix) {
			continue
		}
		if !isTask && time.Since(h.UpdatedAt()) <= threshold {
			continue // stopped but not stale
		}
		if !dryRun {
			if err := msb.Get().RemoveSandbox(ctx, name); err != nil {
				ui.Warnf("failed to remove sandbox %s: %v", name, err)
				continue
			}
		}
		report.VMsPruned++
		report.Details = append(report.Details, StaleEntry{
			Type:     StaleTypeVM,
			Name:     name,
			Slug:     naming.ArtifactFor(name).Slug,
			StaleFor: time.Since(h.UpdatedAt()),
			Digest:   "",
		})
	}
	return report, nil
}
