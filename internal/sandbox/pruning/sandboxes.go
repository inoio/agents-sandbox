package pruning

import (
	"context"
	"time"

	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// SandboxReport summarizes a PruneSandboxes run.
type SandboxReport struct {
	VMsPruned int
	Details   []StaleEntry
}

// PruneSandboxes prunes stale VMs and stopped task sandboxes. Task sandboxes have no
// age gate (transient workers) but running ones are skipped. A stopped VM is
// pruned only when older than threshold (age 0 = no wait).
func PruneSandboxes(
	ctx context.Context,
	pruneState PruneState,
	dryRun bool,
	ui termio.UI,
) (SandboxReport, error) {
	report := SandboxReport{VMsPruned: 0, Details: nil}
	for slug, handle := range pruneState.ToPrune {
		name := handle.Name()
		if msb.IsSandboxActive(handle.Status()) {
			continue
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
			Slug:     slug.Slug,
			StaleFor: time.Since(handle.UpdatedAt()),
			Digest:   "",
		})
	}
	printSandboxPruneReport(ui, report, dryRun)
	return report, nil
}

func printSandboxPruneReport(ui termio.UI, r SandboxReport, dryRun bool) {
	ui.Outf("%s %d sandbox(es)", pruneReportPrefix(dryRun), r.VMsPruned)
	for _, d := range r.Details {
		ui.Verbosef("  %s (%s)", d.Name, d.Slug)
	}
}
