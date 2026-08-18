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
	exists := countVolumesBySlug(handles)
	removed := pruneHomeVolumes(ctx, handles, pruneState, dryRun, ui, &report)
	if !dryRun {
		removeStateForGoneSlugs(ui, removed, exists)
	}
	printVolumePruneReport(ui, report, dryRun)
	return report, nil
}

// countVolumesBySlug returns how many home volumes each slug currently has.
func countVolumesBySlug(handles []msb.VolumeHandle) map[string]int {
	exists := map[string]int{}
	for _, handle := range handles {
		if !hasPrefix(handle.Name(), naming.HomePrefix) {
			continue
		}
		if slug := naming.ArtifactFor(handle.Name()).Slug; slug != "" {
			exists[slug]++
		}
	}
	return exists
}

// pruneHomeVolumes removes the home volumes of stale slugs and records the
// number removed per slug.
func pruneHomeVolumes(
	ctx context.Context,
	handles []msb.VolumeHandle,
	pruneState PruneState,
	dryRun bool,
	ui termio.UI,
	report *VolumeReport,
) map[string]int {
	removed := map[string]int{}
	for _, handle := range handles {
		name := handle.Name()
		if !hasPrefix(name, naming.HomePrefix) {
			continue
		}
		volumeArtifact := naming.ArtifactFor(name)
		if volumeArtifact.Slug == "" {
			continue
		}
		if _, found := pruneState[volumeArtifact.Slug]; !found {
			continue
		}
		if !dryRun {
			if err := msb.Get().RemoveVolume(ctx, name); err != nil {
				ui.Warnf("failed to remove home volume %s: %v", name, err)
				continue
			}
			removed[volumeArtifact.Slug]++
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

// removeStateForGoneSlugs removes the state file for slugs whose last home
// volume was removed in this run.
func removeStateForGoneSlugs(ui termio.UI, removed, exists map[string]int) {
	for slug, count := range removed {
		if count < exists[slug] {
			continue
		}
		if err := state.RemoveState(slug); err != nil {
			ui.Warnf("failed to remove state file for slug %s: %v", slug, err)
		}
	}
}

func printVolumePruneReport(ui termio.UI, r VolumeReport, dryRun bool) {
	ui.Outf("%s %d home volume(s)", pruneReportPrefix(dryRun), r.VolumesPruned)
	for _, d := range r.Details {
		ui.Verbosef("  %s (%s)", d.Name, d.Slug)
	}
}
