package pruning

import (
	"context"
	"time"

	"github.com/moby/moby/client"

	"gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/docker"
	"gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/msb"
	"gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/naming"
	"gitlab.inoio.de/inoio/opencode-sandbox/internal/termio"
)

// ImageReport summarizes a PruneImages run.
type ImageReport struct {
	MSBImagesPruned    int
	DockerImagesPruned int
	Details            []StaleEntry
}

// PruneImages prunes MSB runner images with no surviving VM (or surplus digests
// of a running VM) and host-side dangling docker images. A prunable MSB image is
// removed only when LastUsedAt is older than threshold.
func PruneImages(
	ctx context.Context,
	snap LiveState,
	threshold time.Duration,
	all, dryRun bool,
	ui termio.UI,
) (ImageReport, error) {
	report := ImageReport{MSBImagesPruned: 0, DockerImagesPruned: 0, Details: nil}
	keep := snap.AllVMs
	if all {
		keep = activeSlugs(snap)
	}
	handles, err := msb.Get().ImageList(ctx)
	if err != nil {
		return report, err
	}
	for _, h := range handles {
		ref := h.Reference()
		if !hasPrefix(ref, naming.ImagePrefix) {
			continue
		}
		info := naming.ArtifactFor(ref)
		if info.Slug == naming.BaseSlug {
			continue
		}
		if !pruneImage(info, snap, keep) {
			continue
		}
		if time.Since(h.LastUsedAt()) <= threshold {
			continue
		}
		if !dryRun {
			if err := msb.Get().ImageRemove(ctx, ref, true); err != nil {
				ui.Warnf("failed to remove msb image %s: %v", ref, err)
				continue
			}
		}
		report.MSBImagesPruned++
		report.Details = append(report.Details, StaleEntry{
			Type:     StaleTypeMsbImage,
			Name:     ref,
			StaleFor: 0,
			Slug:     info.Slug,
			Digest:   info.Digest,
		})
	}
	report.DockerImagesPruned = pruneDockerImagesCount(ctx, dryRun, ui)
	return report, nil
}

// pruneImage reports whether an MSB image should be removed under the given keep-set.
func pruneImage(info naming.ArtifactInfo, snap LiveState, keep map[string]bool) bool {
	if !keep[info.Slug] {
		return true // no surviving VM
	}
	if cur, ok := snap.ActiveVMs[info.Slug]; ok {
		return info.Digest != "" && info.Digest != cur // surplus digest of a running VM
	}
	return false
}

// pruneDockerImagesCount removes dangling (untagged) docker images; skipped on dry-run.
func pruneDockerImagesCount(ctx context.Context, dryRun bool, ui termio.UI) int {
	if dryRun {
		return 0
	}
	result, err := docker.Get().ImagePrune(ctx, client.ImagePruneOptions{Filters: client.Filters{}})
	if err != nil {
		ui.Warnf("failed to prune docker images: %v", err)
		return 0
	}
	return len(result.Report.ImagesDeleted)
}
