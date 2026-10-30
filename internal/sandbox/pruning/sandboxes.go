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

// PruneSandboxes prunes stale project VMs and stopped task sandboxes. Task
// sandboxes have no age gate (transient workers) but running ones are skipped. A
// stopped VM is pruned only when older than threshold (age 0 = no wait).
func PruneSandboxes(
	ctx context.Context,
	pruneState PruneState,
	dryRun bool,
	ui termio.UI,
) (SandboxReport, error) {
	report := SandboxReport{VMsPruned: 0, Details: nil}
	handles := make([]msb.SandboxHandle, 0, len(pruneState.ToPrune)+len(pruneState.Tasks))
	for _, handle := range pruneState.ToPrune {
		handles = append(handles, handle)
	}
	handles = append(handles, pruneState.Tasks...)

	for _, handle := range handles {
		name := handle.Name()
		if !msb.IsSandboxInactive(handle.Status()) {
			continue
		}
		if !dryRun {
			if err := handle.Remove(ctx); err != nil {
				if msb.IsReplaced(err) || msb.IsNotFound(err) {
					ui.Verbosef("sandbox %s was already replaced or removed; skipping", name)
					continue
				}
				ui.Warnf("failed to remove sandbox %s: %v", name, err)
				continue
			}
		}
		report.VMsPruned++
		report.Details = append(report.Details, StaleEntry{
			Type:     StaleTypeVM,
			Name:     name,
			Slug:     noteIdentity(handle).Slug,
			StaleFor: time.Since(handle.UpdatedAt()),
			Digest:   "",
		})
	}
	return report, nil
}

func RenderSandboxReport(ui termio.UI, r SandboxReport, dryRun bool) {
	ui.Outf("%s %d sandbox(es)", pruneReportPrefix(dryRun), r.VMsPruned)
	for _, d := range r.Details {
		ui.Verbosef("  %s (%s)", d.Name, d.Slug)
	}
}
