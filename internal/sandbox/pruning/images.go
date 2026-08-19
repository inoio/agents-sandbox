package pruning

import (
	"context"

	"github.com/moby/moby/client"

	"github.com/inoio/opencode-sandbox/internal/sandbox/docker"
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

// PruneImages prunes MSB runner images of stale slugs and surplus digests of
// slugs with a surviving VM, plus host-side dangling docker images. A stale slug
// (present in pruneState) has all its digests removed; any other slug keeps only
// the digest recorded in its state file, and all diverging digests are removed.
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
		if imageArtifact.Slug == naming.BaseSlug || imageArtifact.Slug == naming.BaseDindSlug {
			continue
		}
		if _, stale := pruneState[imageArtifact.Slug]; !stale &&
			!surplusDigest(imageArtifact.Slug, imageArtifact.Digest) {
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

// surplusDigest reports whether digest is a surplus digest for the slug, i.e.
// it diverges from the slug's current digest recorded in its state file. A slug
// without a state file (or without a recorded digest) is kept, since its current
// digest cannot be determined.
func surplusDigest(slug, digest string) bool {
	if digest == "" {
		return false
	}
	st, err := state.ReadState(slug)
	if err != nil {
		return false
	}
	return st.ImageDigest != "" && digest != st.ImageDigest
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
