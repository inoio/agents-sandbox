package pruning

import (
	"context"

	"github.com/moby/moby/client"

	"github.com/inoio/opencode-sandbox/internal/sandbox/docker"
	"github.com/inoio/opencode-sandbox/internal/sandbox/image"
	"github.com/inoio/opencode-sandbox/internal/sandbox/msb"
	"github.com/inoio/opencode-sandbox/internal/sandbox/naming"
	"github.com/inoio/opencode-sandbox/internal/sandbox/state"
	"github.com/inoio/opencode-sandbox/internal/termio"
)

// ImageReport summarizes a PruneImages run.
type ImageReport struct {
	MSBImagesPruned    int
	DockerImagesPruned int
	Details            []StaleEntry
}

// PruneImages prunes MSB runner images of VM-less slugs and images created before
// the currently-in-use image, plus host-side dangling docker images.
func PruneImages(
	ctx context.Context,
	pruneState PruneState,
	dryRun bool,
	ui termio.UI,
) (ImageReport, error) {
	report := ImageReport{MSBImagesPruned: 0, DockerImagesPruned: 0, Details: nil}
	handles, err := msb.Get().ImageList(ctx)
	if err != nil {
		return report, err
	}
	for _, imageHandle := range handles {
		ref := imageHandle.Reference()
		if !hasPrefix(ref, naming.ImagePrefix) {
			continue
		}
		imageArtifact := naming.ArtifactFor(ref)
		if keepImage(imageArtifact.Slug, imageArtifact.Digest, imageHandle, handles, pruneState) {
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
			Slug:     imageArtifact.Slug,
			Digest:   imageArtifact.Digest,
		})
	}
	report.DockerImagesPruned = pruneDockerImages(ctx, dryRun, ui)
	printImagePruneReport(ui, report, dryRun)
	return report, nil
}

func keepImage(
	slug, digest string,
	imageHandle msb.ImageHandle,
	handles []msb.ImageHandle,
	pruneState PruneState,
) bool {
	if _, live := pruneState.ToKeep[slug]; !live {
		return false
	}
	if _, pruned := pruneState.ToPrune[slug]; pruned {
		return false
	}
	return isCurrentOrNewer(slug, digest, imageHandle, handles)
}

func isCurrentOrNewer(slug, digest string, imageHandle msb.ImageHandle, handles []msb.ImageHandle) bool {
	st, err := state.ReadState(slug)
	if err != nil || st.ImageDigest == "" {
		return true
	}
	if digest == image.TagDigest(st.ImageDigest) {
		return true
	}
	currentRef := naming.ImagePrefix + slug + ":" + image.TagDigest(st.ImageDigest)
	for _, h := range handles {
		if h.Reference() == currentRef {
			return !imageHandle.CreatedAt().Before(h.CreatedAt())
		}
	}
	return true
}

// pruneDockerImages removes dangling (untagged) docker images created by us; skipped on dry-run.
func pruneDockerImages(ctx context.Context, dryRun bool, ui termio.UI) int {
	if dryRun {
		return 0
	}
	result, err := docker.Get().
		ImagePrune(ctx, client.ImagePruneOptions{Filters: client.Filters{}.Add("dangling", "true").Add("label", "org.opencode-sandbox.managed=true").Add("until", "24h")})
	if err != nil {
		ui.Warnf("failed to prune docker images: %v", err)
		return 0
	}
	return len(result.Report.ImagesDeleted)
}

func printImagePruneReport(ui termio.UI, r ImageReport, dryRun bool) {
	ui.Outf(
		"%s %d runner image(s), %d dangling docker image(s)",
		pruneReportPrefix(dryRun),
		r.MSBImagesPruned,
		r.DockerImagesPruned,
	)
	for _, d := range r.Details {
		ui.Verbosef("  %s (%s)", d.Name, d.Slug)
	}
}
