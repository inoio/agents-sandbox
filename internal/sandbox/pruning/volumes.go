package pruning

import (
	"context"
	"time"

	"github.com/inoio/opencode-sandbox/internal/sandbox/msb"
	"github.com/inoio/opencode-sandbox/internal/sandbox/naming"
	"github.com/inoio/opencode-sandbox/internal/sandbox/state"
	"github.com/inoio/opencode-sandbox/internal/termio"
)

// VolumeReport summarizes a PruneVolumes run.
type VolumeReport struct {
	VolumesPruned int
	Details       []StaleEntry
}

// PruneVolumes prunes home volumes of stale slugs. When a slug's last home
// volume is removed, its state file is removed too, so the state file never
// outlives the volume it references.
func PruneVolumes(
	ctx context.Context,
	pruneState PruneState,
	dryRun bool,
	ui termio.UI,
) (VolumeReport, error) {
	report := VolumeReport{VolumesPruned: 0, Details: nil}
	handles, err := msb.Get().ListVolumes(ctx)
	if err != nil {
		return report, err
	}
	exists := countVolumesByKey(handles)
	removed := pruneHomeVolumes(ctx, handles, pruneState, dryRun, ui, &report)
	if !dryRun {
		removeStateForGoneKeys(ui, removed, exists)
	}
	printVolumePruneReport(ui, report, dryRun)
	return report, nil
}

// countVolumesByKey returns how many home volumes each project/agent key
// currently has.
func countVolumesByKey(handles []msb.VolumeHandle) map[state.Key]int {
	exists := map[state.Key]int{}
	for _, handle := range handles {
		if !hasPrefix(handle.Name(), naming.HomePrefix) {
			continue
		}
		artifact := naming.ArtifactFor(handle.Name())
		if artifact.Slug != "" {
			exists[state.Key{Slug: artifact.Slug, Agent: artifact.Agent}]++
		}
	}
	return exists
}

// pruneHomeVolumes removes the home volumes of stale slugs and records the
// number removed per project/agent key.
func pruneHomeVolumes(
	ctx context.Context,
	handles []msb.VolumeHandle,
	pruneState PruneState,
	dryRun bool,
	ui termio.UI,
	report *VolumeReport,
) map[state.Key]int {
	removed := map[state.Key]int{}
	for _, handle := range handles {
		name := handle.Name()
		if !hasPrefix(name, naming.HomePrefix) {
			continue
		}
		volumeArtifact := naming.ArtifactFor(name)
		if volumeArtifact.Slug == "" {
			continue
		}
		key := state.Key{Slug: volumeArtifact.Slug, Agent: volumeArtifact.Agent}
		if _, found := pruneState.ToPrune[key.Slug]; !found {
			continue
		}
		if !dryRun {
			if err := msb.Get().RemoveVolume(ctx, name); err != nil {
				ui.Warnf("failed to remove home volume %s: %v", name, err)
				continue
			}
			removed[key]++
		}
		report.VolumesPruned++
		report.Details = append(report.Details, StaleEntry{
			Type:     StaleTypeVolume,
			Name:     name,
			Slug:     volumeArtifact.Slug,
			StaleFor: time.Since(handle.CreatedAt()),
			Digest:   volumeArtifact.Digest,
		})
	}
	return removed
}

// removeStateForGoneKeys removes the state file for keys whose last home
// volume was removed in this run.
func removeStateForGoneKeys(ui termio.UI, removed, exists map[state.Key]int) {
	for key, count := range removed {
		if count < exists[key] {
			continue
		}
		if err := state.RemoveState(key); err != nil {
			ui.Warnf("failed to remove state file for slug %s: %v", key.Slug, err)
		}
	}
}

func printVolumePruneReport(ui termio.UI, r VolumeReport, dryRun bool) {
	ui.Outf("%s %d home volume(s)", pruneReportPrefix(dryRun), r.VolumesPruned)
	for _, d := range r.Details {
		ui.Verbosef("  %s (%s)", d.Name, d.Slug)
	}
}
