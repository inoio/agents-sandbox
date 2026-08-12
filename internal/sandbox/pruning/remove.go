package pruning

import (
	"context"

	"github.com/moby/moby/client"

	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox/docker"
	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox/state"
	"gitlab.inoio.de/inoio/opencode-msb/internal/termio"
)

func removeHomeVolumes(
	ctx context.Context,
	client MsbClient,
	slug string,
	homesBySlug map[string][]string,
	dryRun bool,
	ui termio.UI,
	report *StaleReport,
) {
	vols, ok := homesBySlug[slug]
	if !ok || len(vols) == 0 {
		return
	}
	for _, volName := range vols {
		if !dryRun {
			if err := client.RemoveVolume(ctx, volName); err != nil {
				ui.Warnf("failed to remove home volume %s: %v", volName, err)
			}
		}
		report.PrunedVolumes++
		report.Details = append(report.Details, StaleEntry{
			Type:     StaleTypeVolume,
			Name:     volName,
			Slug:     slug,
			StaleFor: 0,
			Digest:   "",
		})
	}
	if !dryRun {
		if err := state.RemoveState(slug); err != nil {
			ui.Warnf("failed to remove state file for slug %s: %v", slug, err)
		}
	}
}

func removeMSBImages(
	ctx context.Context,
	client MsbClient,
	slug string,
	msbImagesBySlug map[string][]imageWithDigest,
	dryRun bool,
	ui termio.UI,
	report *StaleReport,
) {
	for _, img := range msbImagesBySlug[slug] {
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
	msbImagesBySlug map[string][]imageWithDigest,
	dryRun bool,
	ui termio.UI,
	report *StaleReport,
) {
	for _, img := range msbImagesBySlug[slug] {
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
