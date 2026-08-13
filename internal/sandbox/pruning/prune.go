package pruning

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/moby/moby/client"

	"gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/docker"
	"gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/msb"
	"gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/naming"
	"gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/state"
	"gitlab.inoio.de/inoio/opencode-sandbox/internal/termio"
)

// MsbClient is a type alias for msb.Client, matching the original sandbox-level alias.
type MsbClient = msb.Client

// hasAnything reports whether the report contains any pruned items.
func (r *StaleReport) hasAnything() bool {
	if r == nil {
		return false
	}
	return r.PrunedVMs > 0 ||
		r.PrunedVolumes > 0 ||
		r.PrunedDockerImages > 0 ||
		r.PrunedMSBImages > 0 ||
		r.PrunedTaskSandboxes > 0 ||
		r.PrunedCloneVolumes > 0
}

// Prune orchestrates a three-phase cleanup pipeline: collecting all artifacts
// into a catalog, then pruning stale VMs, active VM artifacts, orphans, and
// orphaned clone volumes in sequence.
func Prune(
	ctx context.Context,
	threshold time.Duration,
	dryRun bool,
	autoPrune bool,
	ui termio.UI,
) error {
	report, err := catalogAndPrune(ctx, threshold, dryRun, ui)
	printPruneSummary(ui, report, dryRun, autoPrune)
	return err
}

func printPruneSummary(ui termio.UI, report *StaleReport, dryRun bool, autoPrune bool) {
	if report == nil {
		return
	}
	var out func(string, ...any)
	var action string
	if autoPrune {
		out = ui.Verbosef
		action = "auto-prune: Pruned"
		if dryRun {
			action = "auto-prune: Would prune"
		}
	} else {
		out = ui.Outf
		action = "Pruned"
		if dryRun {
			action = "dry-run: Would prune"
		}
	}
	if !report.hasAnything() {
		out = ui.Verbosef
	}
	out(
		"%s %d VMs, %d home volumes, %d docker images, %d msb images, %d task sandboxes, %d clone volumes",
		action,
		report.PrunedVMs,
		report.PrunedVolumes,
		report.PrunedDockerImages,
		report.PrunedMSBImages,
		report.PrunedTaskSandboxes,
		report.PrunedCloneVolumes,
	)
	ui.Verbosef("details %d", len(report.Details))
	for _, entry := range report.Details {
		ui.Verbosef("x  %s (%s, digest=%s, type=%s)", entry.Name, entry.Slug, entry.Digest, entry.Type)
	}
}

func catalogAndPrune(ctx context.Context, threshold time.Duration, dryRun bool, ui termio.UI) (*StaleReport, error) {
	report := &StaleReport{} //nolint:exhaustruct // Counts initialized to zero, populated during pruning
	msbClient := msb.Get()

	catalog, catalogErr := buildCatalog(ctx, msbClient, threshold)
	if catalogErr != nil {
		return report, catalogErr
	}

	report, staleVMErr := pruneStaleVMs(ctx, msbClient, catalog, threshold, dryRun, ui, report)
	report, activeVMErr := pruneActiveVMArtifacts(ctx, msbClient, catalog, dryRun, ui, report)
	report, orphanErr := pruneOrphanArtifacts(ctx, msbClient, catalog, threshold, dryRun, ui, report)
	report, cloneVolErr := pruneCloneVolumes(ctx, msbClient, catalog, dryRun, ui, report)

	// Prune task sandboxes (collected during catalog build, pruned here).
	report, sandboxErrs := pruneTaskSandboxes(ctx, catalog, dryRun, msbClient, ui, report)
	return report, errors.Join(
		catalogErr,
		staleVMErr,
		activeVMErr,
		orphanErr,
		cloneVolErr,
		errors.Join(sandboxErrs...),
	)
}

func pruneTaskSandboxes(
	ctx context.Context,
	catalog *PruningCatalog,
	dryRun bool,
	msbClient msb.Client,
	ui termio.UI,
	report *StaleReport,
) (*StaleReport, []error) {
	var sandboxErrs []error
	for _, entry := range catalog.TaskSandboxes {
		if !dryRun {
			if removeErr := msbClient.RemoveSandbox(ctx, entry.Name); removeErr != nil {
				ui.Warnf("failed to remove task sandbox %s: %v", entry.Name, removeErr)
				sandboxErrs = append(sandboxErrs, removeErr)
				continue
			}
		}
		report.PrunedTaskSandboxes++
		report.Details = append(report.Details, entry)
	}
	return report, sandboxErrs
}

// pruneStaleVMs removes each stale VM and cascades deletion of all
// associated artifacts (home volumes, MSB images, Docker images).
//
//nolint:unparam // Error return is always nil; kept for uniform phase signature across callers
func pruneStaleVMs(
	ctx context.Context,
	client MsbClient,
	catalog *PruningCatalog,
	threshold time.Duration,
	dryRun bool,
	ui termio.UI,
	report *StaleReport,
) (*StaleReport, error) {
	for _, entry := range catalog.StaleVMs {
		pruneStaleCascade(ctx, client, entry, threshold, catalog.HomeVolumes, catalog.MSBImages, dryRun, ui, report)
	}
	return report, nil
}

// pruneActiveVMArtifacts removes home volumes, MSB images, and Docker
// images that don't match an active VM's state.
func pruneActiveVMArtifacts(
	ctx context.Context,
	client MsbClient,
	catalog *PruningCatalog,
	dryRun bool,
	ui termio.UI,
	report *StaleReport,
) (*StaleReport, error) {
	var errs []error
	for slug, digest := range catalog.ActiveVMDigest {
		err := pruneActiveVMCleanup(
			ctx,
			client,
			slug,
			digest,
			catalog.HomeVolumes,
			catalog.MSBImages,
			dryRun,
			ui,
			report,
		)
		if err != nil {
			errs = append(errs, err)
		}
	}
	return report, errors.Join(errs...)
}

// pruneOrphanArtifacts removes all home volumes, MSB images, and Docker
// images for project slugs that have no VM at all.
//
//nolint:unparam // Error return is always nil; kept for uniform phase signature across callers
func pruneOrphanArtifacts(
	ctx context.Context,
	client MsbClient,
	catalog *PruningCatalog,
	threshold time.Duration,
	dryRun bool,
	ui termio.UI,
	report *StaleReport,
) (*StaleReport, error) {
	staleVMs := make(map[string]bool)
	for _, entry := range catalog.StaleVMs {
		staleVMs[entry.Slug] = true
	}

	for slug := range catalog.MSBImages {
		if staleVMs[slug] {
			continue
		}
		if _, active := catalog.ActiveVMDigest[slug]; active {
			continue
		}
		pruneOrphanSlug(ctx, client, slug, threshold, catalog.HomeVolumes, catalog.MSBImages, report, dryRun, ui)
	}
	return report, nil
}

// pruneCloneVolumes removes clone volumes whose project slug has no
// associated active VM.
//
//nolint:unparam // Error return is always nil; kept for uniform phase signature across callers
func pruneCloneVolumes(
	ctx context.Context,
	client MsbClient,
	catalog *PruningCatalog,
	dryRun bool,
	ui termio.UI,
	report *StaleReport,
) (*StaleReport, error) {
	for _, cv := range catalog.CloneVolumes {
		slug, _ := naming.ExtractProjectSlugAndDigest(cv)
		if _, active := catalog.ActiveVMDigest[slug]; active {
			continue
		}
		if !dryRun {
			if err := client.RemoveVolume(ctx, cv); err != nil {
				ui.Warnf("failed to remove clone volume %s: %v", cv, err)
				continue
			}
		}
		report.PrunedCloneVolumes++
		report.Details = append(report.Details, StaleEntry{
			Type:     StaleTypeVolume,
			Name:     cv,
			Slug:     slug,
			StaleFor: 0,
			Digest:   "???",
		})
	}
	return report, nil
}

// pruneStaleCascade removes a stale VM and all associated artifacts (volumes, images).
func pruneStaleCascade(
	ctx context.Context,
	client MsbClient,
	entry StaleEntry,
	threshold time.Duration,
	homesBySlug map[string][]volumeWithAge,
	msbImagesBySlug map[string][]imageWithDigest,
	dryRun bool,
	ui termio.UI,
	report *StaleReport,
) {
	slug := entry.Slug
	if !dryRun {
		if err := client.RemoveSandbox(ctx, entry.Name); err != nil {
			ui.Warnf("failed to remove stale VM %s: %v", entry.Name, err)
			return
		}
	}
	report.PrunedVMs++
	report.Details = append(report.Details, entry)
	removeHomeVolumes(ctx, client, slug, threshold, homesBySlug, dryRun, ui, report)
	removeMSBImages(ctx, client, slug, threshold, msbImagesBySlug, dryRun, ui, report)
	removeDockerImages(ctx, slug, threshold, msbImagesBySlug, dryRun, ui, report)
}

// pruneActiveVMCleanup removes volumes and images that don't match an active VM's state.
func pruneActiveVMCleanup(
	ctx context.Context,
	client MsbClient,
	slug string,
	digest string,
	homesBySlug map[string][]volumeWithAge,
	msbImagesBySlug map[string][]imageWithDigest,
	dryRun bool,
	ui termio.UI,
	report *StaleReport,
) error {
	homeErr := pruneActiveVMHomeVolumes(ctx, client, slug, digest, homesBySlug, dryRun, ui, report)
	// Images: delete unused ones, keep :latest, keep matching digest.
	pruneActiveVMMSBImages(ctx, client, slug, digest, msbImagesBySlug, dryRun, ui, report)
	// Docker images: same logic.
	pruneActiveVMDockerImages(ctx, slug, digest, msbImagesBySlug, dryRun, ui, report)
	return homeErr
}

func pruneActiveVMHomeVolumes(
	ctx context.Context,
	client MsbClient,
	slug string,
	digest string,
	homesBySlug map[string][]volumeWithAge,
	dryRun bool,
	ui termio.UI,
	report *StaleReport,
) error {
	vols, ok := homesBySlug[slug]
	if !ok {
		return nil
	}

	st, err := state.ReadState(slug)
	if err != nil {
		if errors.Is(err, state.ErrStateNotFound) {
			return fmt.Errorf("no state file found for project %q", slug)
		}
		return err
	}
	if st == nil || st.HomeVolume == "" {
		return nil
	}
	for _, vol := range vols {
		if vol.name == st.HomeVolume {
			continue
		}
		if !dryRun {
			if err := client.RemoveVolume(ctx, vol.name); err != nil {
				ui.Warnf("failed to remove home volume %s: %v", vol.name, err)
				continue
			}
		}
		report.PrunedVolumes++
		report.Details = append(report.Details, StaleEntry{
			Type:     StaleTypeVolume,
			Name:     vol.name,
			Slug:     slug,
			StaleFor: 0,
			Digest:   digest,
		})
	}
	return nil
}

func pruneActiveVMMSBImages(
	ctx context.Context,
	client MsbClient,
	slug string,
	digest string,
	msbImagesBySlug map[string][]imageWithDigest,
	dryRun bool,
	ui termio.UI,
	report *StaleReport,
) {
	for _, img := range msbImagesBySlug[slug] {
		if img.isLatest || img.digest == digest {
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

func pruneActiveVMDockerImages(
	ctx context.Context,
	slug string,
	digest string,
	msbImagesBySlug map[string][]imageWithDigest,
	dryRun bool,
	ui termio.UI,
	report *StaleReport,
) {
	for _, img := range msbImagesBySlug[slug] {
		if img.isLatest || img.digest == digest {
			continue
		}
		if !dryRun {
			dockerRef := stripDockerHostPrefix(img.ref)
			_, err := docker.Get().ImageRemove(
				ctx, dockerRef,
				client.ImageRemoveOptions{PruneChildren: true},
			)
			if err != nil {
				ui.Warnf("failed to remove docker image %s: %v", dockerRef, err)
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

func stripDockerHostPrefix(ref string) string {
	if prefix, ok := strings.CutPrefix(ref, "docker.io/"); ok {
		return prefix
	}
	return ref
}

// pruneOrphanSlug deletes all home volumes, MSB images, and Docker images
// for a slug that has no VM at all.
func pruneOrphanSlug(
	ctx context.Context,
	client MsbClient,
	slug string,
	threshold time.Duration,
	homesBySlug map[string][]volumeWithAge,
	msbImagesBySlug map[string][]imageWithDigest,
	report *StaleReport,
	dryRun bool,
	ui termio.UI,
) {
	removeHomeVolumes(ctx, client, slug, threshold, homesBySlug, dryRun, ui, report)
	removeMSBImages(ctx, client, slug, threshold, msbImagesBySlug, dryRun, ui, report)
	removeDockerImages(ctx, slug, threshold, msbImagesBySlug, dryRun, ui, report)
}
