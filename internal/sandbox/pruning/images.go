package pruning

import (
	"context"
	"time"

	"github.com/moby/moby/api/types/image"
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

// imageStaleFor reports how long a cached image has been stale: since it was
// last used, falling back to its creation time when it was never used, and zero
// when it has neither timestamp.
func imageStaleFor(imageHandle msb.ImageHandle) time.Duration {
	used := imageHandle.LastUsedAt()
	if used.IsZero() {
		used = imageHandle.CreatedAt()
	}
	if used.IsZero() {
		return 0
	}
	return time.Since(used)
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
	dclient docker.Client,
	pruneState PruneState,
	dryRun bool,
	ui termio.UI,
) (ImageReport, error) {
	var report ImageReport
	client := msb.Get()
	handles, err := client.ImageList(ctx)
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
			if err := client.ImageRemove(ctx, ref, true); err != nil {
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
			StaleFor: imageStaleFor(imageHandle),
			Slug:     imageArtifact.Slug,
			Digest:   digest,
		})
	}
	if !dryRun && pruneState.allowsNativeImagePrune() {
		nativeReport, err := client.ImagePrune(ctx)
		if err != nil {
			ui.Warnf("failed to prune unused msb image data: %v", err)
		} else {
			report.SkippedInUse = nativeReport.SkippedInUse
			report.BytesReclaimed = nativeReport.BytesReclaimed
		}
	}
	// A docker failure is warned inside pruneDockerImages and must not fail the
	// prune; the count and details fall back to zero and empty.
	dockerPruned, dockerDetails, _ := pruneDockerImages(ctx, dclient, pruneState.Age, dryRun, ui)
	report.DockerImagesPruned = dockerPruned
	report.Details = append(report.Details, dockerDetails...)
	return report, nil
}

// pruneDockerImages removes dangling (untagged) docker images created by us and
// older than age. It enumerates the managed dangling candidates first so a
// dry-run can preview each one and JSON can detail them; a real prune then asks
// the daemon for the same set via the dangling, label and until filters. The
// count is the number the daemon deleted, or the number of candidates on a
// dry-run.
func pruneDockerImages(
	ctx context.Context,
	dclient docker.Client,
	age time.Duration,
	dryRun bool,
	ui termio.Logger,
) (int, []StaleEntry, error) {
	listResult, err := dclient.ImageList(ctx, client.ImageListOptions{
		Filters: client.Filters{}.Add("dangling", "true"),
	})
	if err != nil {
		ui.Warnf("failed to list docker images: %v", err)
		return 0, nil, err
	}
	var details []StaleEntry
	for _, summary := range listResult.Items {
		if !dockerImageIsCandidate(summary, age) {
			continue
		}
		details = append(details, StaleEntry{
			Type:     StaleTypeDockerImage,
			Name:     summary.ID,
			StaleFor: time.Since(time.Unix(summary.Created, 0)),
			Slug:     "",
			Digest:   "",
		})
	}
	if dryRun {
		return len(details), details, nil
	}
	result, err := dclient.ImagePrune(ctx, client.ImagePruneOptions{
		Filters: client.Filters{}.Add("dangling", "true").
			Add("label", "org.agents-sandbox.managed=true").
			Add("until", age.String()),
	})
	if err != nil {
		ui.Warnf("failed to prune docker images: %v", err)
		// No details on a failed prune: the candidates were not deleted, so
		// reporting them would name docker images the daemon never removed.
		return 0, nil, err
	}
	// The daemon is authoritative: it returns every image it deleted, which can
	// be fewer than we listed (a candidate gained a tag between ImageList and
	// ImagePrune, so the daemon kept it). Report only the candidates the daemon
	// actually deleted, so the count and the details agree.
	deleted := make(map[string]struct{}, len(result.Report.ImagesDeleted))
	for _, response := range result.Report.ImagesDeleted {
		if response.Deleted != "" {
			deleted[response.Deleted] = struct{}{}
		}
	}
	removed := make([]StaleEntry, 0, len(details))
	for _, detail := range details {
		if _, ok := deleted[detail.Name]; ok {
			removed = append(removed, detail)
		}
	}
	return len(removed), removed, nil
}

// dockerImageIsCandidate reports whether a listed docker image is one of ours
// and old enough to reclaim: untagged (no RepoTags), carrying the launcher's
// managed label, and created before the age threshold.
func dockerImageIsCandidate(summary image.Summary, age time.Duration) bool {
	if len(summary.RepoTags) != 0 || summary.Labels["org.agents-sandbox.managed"] != "true" {
		return false
	}
	return time.Since(time.Unix(summary.Created, 0)) > age
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
