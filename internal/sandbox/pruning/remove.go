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

func removeDockerImages(
	ctx context.Context,
	slug string,
	threshold time.Duration,
	msbImagesBySlug map[string][]imageWithDigest,
	dryRun bool,
	ui termio.UI,
	report *StaleReport,
) {
	for _, img := range msbImagesBySlug[slug] {
		if isRecent(img.lastUsed, threshold) {
			ui.Verbosef("keeping recent docker image %s", img.ref)
			continue
		}
		if !dryRun {
			dockerRef := stripDockerHostPrefix(img.ref)
			_, err := docker.Get().ImageRemove(
				ctx, dockerRef,
				client.ImageRemoveOptions{PruneChildren: true},
			)
			if err != nil {
				ui.Verbosef("failed to remove docker image %s: %v", dockerRef, err)
				continue
			}
		}
		report.PrunedDockerImages++
		report.Details = append(report.Details, StaleEntry{
			Type:     StaleTypeDockerImage,
			Name:     img.ref,
			Slug:     slug,
			StaleFor: 0,
			Digest:   img.digest,
		})
	}
}
