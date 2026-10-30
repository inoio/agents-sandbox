package pruning

import (
	"context"

	"github.com/moby/moby/client"

	"github.com/inoio/agents-sandbox/internal/sandbox/docker"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/naming"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// ImageReport summarizes a PruneImages run. SkippedInUse and BytesReclaimed
// come from the runtime's own image-prune report: BytesReclaimed is nil when the
// runtime cannot measure it.
type ImageReport struct {
	MSBImagesPruned    int
	DockerImagesPruned int
	SkippedInUse       uint32
	BytesReclaimed     *uint64
	Details            []StaleEntry
}

// PruneImages prunes MSB runner-image references that the launcher no longer
// needs, then asks microsandbox to reclaim unreferenced image data. Retained are
// the refs a kept sandbox still holds, the keys the current invocation is about
// to use, and any agent ref used within the prune age; a pruned key's refs, an
// agent-less ref, and an aged-out ref are reclaimed, plus host-side dangling
// Docker images. Ownership is positional (see ListImages) and applies the same
// name-shape predicate as list and remove: a reference is a runner image only
// when its parsed slug embeds the launcher's 14-character slug hash
// (naming.IsRunnerImageRef). A manually loaded single-segment image in the
// private runtime home is foreign and never removed.
func PruneImages(
	ctx context.Context,
	pruneState PruneState,
	dryRun bool,
	ui termio.UI,
) (ImageReport, error) {
	var report ImageReport
	handles, err := msb.Get().ImageList(ctx)
	if err != nil {
		// gomutants:disable-next-line RETURN_ZERO reason="report is still its zero value at this point, so returning a fresh zero ImageReport is identical"
		return report, err
	}
	keptImageRefs := referencedImages(pruneState.ToKeep)
	for _, imageHandle := range handles {
		ref := imageHandle.Reference()
		if !naming.IsRunnerImageRef(ref) {
			continue
		}
		imageArtifact := naming.ParseImageTag(ref)
		if keepImage(imageHandle, imageArtifact, pruneState, keptImageRefs) {
			continue
		}
		if !dryRun {
			if err := msb.Get().ImageRemove(ctx, ref, true); err != nil {
				ui.Warnf("failed to remove msb image %s: %v", ref, err)
				continue
			}
		}
		report.MSBImagesPruned++
		digest := imageArtifact.Digest
		if digest == "" {
			// A content ref has no legacy digest; report which variant went.
			digest = imageArtifact.ContentID
		}
		report.Details = append(report.Details, StaleEntry{
			Type:     StaleTypeMsbImage,
			Name:     ref,
			StaleFor: 0,
			Slug:     imageArtifact.Slug,
			Digest:   digest,
		})
	}
	if !dryRun && pruneState.allowsNativeImagePrune() {
		nativeReport, err := msb.Get().ImagePrune(ctx)
		if err != nil {
			ui.Warnf("failed to prune unused msb image data: %v", err)
		} else {
			report.SkippedInUse = nativeReport.SkippedInUse
			report.BytesReclaimed = nativeReport.BytesReclaimed
		}
	}
	report.DockerImagesPruned = pruneDockerImages(ctx, dryRun, ui)
	return report, nil
}

// pruneDockerImages removes dangling (untagged) docker images created by us; skipped on dry-run.
func pruneDockerImages(ctx context.Context, dryRun bool, ui termio.UI) int {
	if dryRun {
		return 0
	}
	result, err := docker.Get().
		ImagePrune(ctx, client.ImagePruneOptions{Filters: client.Filters{}.Add("dangling", "true").Add("label", "org.agents-sandbox.managed=true").Add("until", "24h")})
	if err != nil {
		ui.Warnf("failed to prune docker images: %v", err)
		return 0
	}
	return len(result.Report.ImagesDeleted)
}

func RenderImageReport(ui termio.UI, r ImageReport, dryRun bool) {
	ui.Outf(
		"%s %d runner image(s), %d dangling docker image(s)",
		pruneReportPrefix(dryRun),
		r.MSBImagesPruned,
		r.DockerImagesPruned,
	)
	if r.SkippedInUse > 0 || r.BytesReclaimed != nil {
		ui.Outf(
			"  image data: %d skipped (in use), %s reclaimed",
			r.SkippedInUse,
			formatOptionalBytes(r.BytesReclaimed),
		)
	}
	for _, d := range r.Details {
		ui.Verbosef("  %s (%s)", d.Name, d.Slug)
	}
}
