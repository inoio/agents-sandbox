package pruning

import (
	"context"
	"time"

	"gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/msb"
	"gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/naming"
	"gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/state"
	"gitlab.inoio.de/inoio/opencode-sandbox/internal/termio"
)

// VolumeReport summarizes a PruneVolumes run.
type VolumeReport struct {
	VolumesPruned int
	Details       []StaleEntry
}

// PruneVolumes prunes home volumes whose slug is not in the keep-set and that
// are older than threshold. The keep-set is AllVMs by default, ActiveVMs under
// all. When any of a slug's volumes are removed, its state file is removed too.
//
//nolint:gocognit // independent filter branches for prefix, keep-set, and age
func PruneVolumes(
	ctx context.Context,
	snap LiveState,
	threshold time.Duration,
	all, dryRun bool,
	ui termio.UI,
) (VolumeReport, error) {
	report := VolumeReport{VolumesPruned: 0, Details: nil}
	keep := snap.AllVMs
	if all {
		keep = activeSlugs(snap)
	}
	handles, err := msb.Get().ListVolumes(ctx)
	if err != nil {
		return report, err
	}
	removedSlugs := map[string]bool{}
	for _, h := range handles {
		name := h.Name()
		if !hasPrefix(name, naming.HomePrefix) {
			continue
		}
		slug := naming.ArtifactFor(name).Slug
		if slug == "" || keep[slug] {
			continue
		}
		if time.Since(h.CreatedAt()) <= threshold {
			continue
		}
		if !dryRun {
			if err := msb.Get().RemoveVolume(ctx, name); err != nil {
				ui.Warnf("failed to remove home volume %s: %v", name, err)
				continue
			}
			removedSlugs[slug] = true
		}
		report.VolumesPruned++
		report.Details = append(report.Details, StaleEntry{
			Type:     StaleTypeVolume,
			Name:     name,
			Slug:     slug,
			StaleFor: time.Since(h.CreatedAt()),
			Digest:   naming.ArtifactFor(name).Digest,
		})
	}
	if !dryRun {
		for slug := range removedSlugs {
			if err := state.RemoveState(slug); err != nil {
				ui.Warnf("failed to remove state file for slug %s: %v", slug, err)
			}
		}
	}
	return report, nil
}
