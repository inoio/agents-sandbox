package pruning

import (
	"context"
	"strings"

	"github.com/moby/moby/client"

	"github.com/inoio/agents-sandbox/internal/sandbox/docker"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/naming"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// ImageReport summarizes a PruneImages run.
type ImageReport struct {
	MSBImagesPruned    int
	DockerImagesPruned int
	Details            []StaleEntry
}

// PruneImages prunes MSB runner images that are no longer referenced by a live
// slug (kept are per-agent "-latest" tags and any image a kept sandbox still
// references), plus host-side dangling docker images.
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
	keptImageRefs := referencedImages(pruneState.ToKeep)
	for _, imageHandle := range handles {
		ref := imageHandle.Reference()
		if !hasPrefix(ref, naming.ImagePrefix) {
			continue
		}
		imageArtifact := naming.ArtifactFor(ref)
		if keepImage(imageHandle, pruneState, keptImageRefs) {
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

// keepImage reports whether an msb image reference of a live slug should be
// retained: images referenced by a kept sandbox are always retained, per-agent
// "-latest" tags are the current image for that agent; every other ref is
// surplus (orphaned content or pre-redesign digest refs).
func keepImage(imageHandle msb.ImageHandle, pruneState PruneState, keptImageRefs map[string]struct{}) bool {
	ref := imageHandle.Reference()
	if _, used := keptImageRefs[ref]; used {
		return true
	}
	artifact := naming.ArtifactFor(ref)
	key := state.Key{Slug: artifact.Slug, Agent: artifact.Agent}
	if _, live := pruneState.ToKeep[key]; !live {
		return false
	}
	if _, pruned := pruneState.ToPrune[key]; pruned {
		return false
	}
	return artifact.Agent != "" && strings.HasSuffix(ref, "-latest")
}

// referencedImages collects the image references that kept sandboxes currently
// point at. An image still in use by a kept sandbox must not be removed, even
// when it is a digest ref rather than a per-agent "-latest" tag (pre-redesign
// VMs), since msb rejects removal of an image its database still references.
func referencedImages(kept map[state.Key]msb.SandboxHandle) map[string]struct{} {
	result := make(map[string]struct{}, len(kept))
	for _, handle := range kept {
		if ref := handle.Image(); ref != "" {
			result[ref] = struct{}{}
		}
	}
	return result
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
