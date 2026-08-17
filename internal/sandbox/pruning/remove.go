package pruning

import (
	"context"
	"time"

	"github.com/moby/moby/client"

	"gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/docker"
	"gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/state"
	"gitlab.inoio.de/inoio/opencode-sandbox/internal/termio"
)

// isRecent reports whether a timestamp falls within the prune threshold.
// A zero value is treated as recent so that unknown timestamps are never pruned.
func isRecent(ts time.Time, threshold time.Duration) bool {
	return ts.IsZero() || time.Since(ts) < threshold
}

func removeHomeVolumes(
	ctx context.Context,
	client MsbClient,
	slug string,
	threshold time.Duration,
	homesBySlug map[string][]volumeWithAge,
	dryRun bool,
	ui termio.UI,
	report *StaleReport,
) {
	vols, ok := homesBySlug[slug]
	if !ok || len(vols) == 0 {
		return
	}
	removedAny := false
	for _, vol := range vols {
		if isRecent(vol.createdAt, threshold) {
			ui.Verbosef("keeping recent home volume %s", vol.name)
			continue
		}
		removedAny = true
		if !dryRun {
			if err := client.RemoveVolume(ctx, vol.name); err != nil {
				ui.Warnf("failed to remove home volume %s: %v", vol.name, err)
			}
		}
		report.PrunedVolumes++
		report.Details = append(report.Details, StaleEntry{
			Type:     StaleTypeVolume,
			Name:     vol.name,
			Slug:     slug,
			StaleFor: 0,
			Digest:   "",
		})
	}
	if removedAny && !dryRun {
		if err := state.RemoveState(slug); err != nil {
			ui.Warnf("failed to remove state file for slug %s: %v", slug, err)
		}
	}
}

func removeMSBImages(
	ctx context.Context,
	client MsbClient,
	slug string,
	threshold time.Duration,
	msbImagesBySlug map[string][]imageWithDigest,
	dryRun bool,
	ui termio.UI,
	report *StaleReport,
) {
	for _, img := range msbImagesBySlug[slug] {
		if isRecent(img.lastUsed, threshold) {
			ui.Verbosef("keeping recent msb image %s", img.ref)
			continue
		}
		if !dryRun {
			if err := client.ImageRemove(ctx, img.ref, true); err != nil {
				ui.Warnf("failed to remove msb image %s: %v", img.ref, err)
				continue
			}
		}
		report.PrunedMSBImages++
		report.Details = append(report.Details, StaleEntry{
			Type:     StaleTypeMsbImage,
			Name:     img.ref,
			Slug:     slug,
			StaleFor: 0,
			Digest:   img.digest,
		})
	}
}

// pruneDockerImages removes dangling (untagged) Docker images. After a rebuild
// the previous runner image is only referenced by tags we no longer create, so
// it becomes untagged and is reclaimed here. Tagged images (base images and the
// current :latest runner) are left untouched.
func pruneDockerImages(
	ctx context.Context,
	dryRun bool,
	ui termio.UI,
	report *StaleReport,
) {
	if dryRun {
		return
	}
	result, err := docker.Get().ImagePrune(ctx, client.ImagePruneOptions{Filters: client.Filters{}})
	if err != nil {
		ui.Warnf("failed to prune docker images: %v", err)
		return
	}
	if len(result.Report.ImagesDeleted) > 0 {
		ui.Verbosef("pruned %d dangling docker images", len(result.Report.ImagesDeleted))
	}
	report.PrunedDockerImages += len(result.Report.ImagesDeleted)
}
