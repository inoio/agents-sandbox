package sandbox

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/moby/moby/client"

	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox/docker"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox/msb"

	"gitlab.inoio.de/inoio/opencode-msb/internal/termio"
)

// StaleReport describes the result of a prune operation.
type StaleReport struct {
	PrunedVMs           int
	PrunedVolumes       int
	PrunedDockerImages  int
	PrunedMSBImages     int
	PrunedTaskSandboxes int
	PrunedCloneVolumes  int
	Details             []StaleEntry
}

type StaleType int

const (
	StaleTypeVM StaleType = iota
	StaleTypeVolume
	StaleTypeDockerImage
	StaleTypeMsbImage
)

// StaleEntry describes a single artifact that was pruned or would be pruned.
type StaleEntry struct {
	Type     StaleType
	Name     string
	StaleFor time.Duration
	Slug     string // project slug, for grouping related artifacts
	Digest   string // for images/volumes: the identifying digest tag; empty for :latest or VMs
}

// PruningCatalog holds all collected artifact data for a single prune run.
// buildCatalog creates it by inspecting sandboxes, volumes, and images.
type PruningCatalog struct {
	StaleVMs       []StaleEntry
	TaskSandboxes  []StaleEntry
	ActiveVMDigest map[string]string // slug -> digest of the running VM
	HomeVolumes    map[string]map[string]string
	CloneVolumes   []string
	MSBImages      map[string][]imageWithDigest
}

// staleVM is an internal type used by findStaleVMs.
type staleVM struct {
	name      string
	status    msbSdk.SandboxStatus
	updatedAt time.Time
	image     string // image ref for active VMs, empty for stale
}

// imageWithDigest holds a reference and its digest for MSB images.
type imageWithDigest struct {
	ref      string
	digest   string // empty string for :latest
	isLatest bool
}

// findHashSuffix finds the start index of a 14-character base36 hash suffix
// in the name remainder (e.g. "saife-1mjusbm3wikhb0" -> returns 6, pointing
// at the '1' in the 14-char hash). Returns -1 when no such suffix is found.
// isStoppedStatus returns true if the status indicates the sandbox is not
// actively running (stopped or crashed).
func isStoppedStatus(status msbSdk.SandboxStatus) bool {
	return status == msbSdk.SandboxStatusStopped || status == msbSdk.SandboxStatusCrashed
}

// findStaleVMs filters sandboxes to only those that are stopped/crashed and
// older than the given threshold.
func findStaleVMs(sandboxes []staleVM, threshold time.Duration) []StaleEntry {
	var stale []StaleEntry
	for _, s := range sandboxes {
		if !isStoppedStatus(s.status) {
			continue
		}
		elapsed := time.Since(s.updatedAt)
		if elapsed > threshold {
			stale = append(stale, StaleEntry{
				Type:     StaleTypeVM,
				Name:     s.name,
				StaleFor: elapsed,
				Slug:     "",
				Digest:   "",
			})
		}
	}
	return stale
}

// HasAnything reports whether the report contains any pruned items.
func (r *StaleReport) HasAnything() bool {
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

// buildCatalog collects all artifacts from the system and returns a
// PruningCatalog. It lists sandboxes, volumes, and MSB images, then
// categorizes them by type and groups them by project slug.
//
//nolint:gocognit,funlen // Data collection involves multiple independent list operations with filtering
func buildCatalog(ctx context.Context, client MsbClient, threshold time.Duration) (*PruningCatalog, error) {
	sandboxHandles, err := client.ListSandboxes(ctx)
	if err != nil {
		return nil, fmt.Errorf("list sandboxes: %w", err)
	}

	volumeHandles, err := client.ListVolumes(ctx)
	if err != nil {
		return nil, fmt.Errorf("list volumes: %w", err)
	}

	imageHandles, err := client.ImageList(ctx)
	if err != nil {
		return nil, fmt.Errorf("list msb images: %w", err)
	}

	//nolint:exhaustruct // Only populating fields needed for the prune pipeline
	catalog := &PruningCatalog{
		HomeVolumes:    make(map[string]map[string]string),
		CloneVolumes:   make([]string, 0),
		MSBImages:      make(map[string][]imageWithDigest),
		ActiveVMDigest: make(map[string]string),
	}

	staleVMs := make([]staleVM, 0)

	// Process sandboxes: collect stale VMs, task sandboxes, and active VMs.
	for _, h := range sandboxHandles {
		name := h.Name()
		if !strings.HasPrefix(name, sbPrefix) {
			continue
		}

		if strings.HasPrefix(name, vmPrefix) {
			status := h.Status()
			if isStoppedStatus(status) {
				staleVMs = append(staleVMs, staleVM{
					name:      name,
					status:    status,
					updatedAt: h.UpdatedAt(),
					image:     "",
				})
				continue
			}
			// Active VM: extract image ref for digest tracking.
			staleVMs = append(staleVMs, staleVM{
				name:      name,
				status:    status,
				updatedAt: h.UpdatedAt(),
				image:     h.Image(),
			})
		}

		if strings.HasPrefix(name, taskPrefix) {
			// Task sandboxes are always pruned immediately.
			elapsed := time.Since(h.UpdatedAt())
			slug, _ := extractProjectSlugAndDigest(name)
			catalog.TaskSandboxes = append(catalog.TaskSandboxes, StaleEntry{
				Type:     StaleTypeVM,
				Name:     name,
				StaleFor: elapsed,
				Slug:     slug,
				Digest:   "",
			})
		}
	}

	staleEntries := findStaleVMs(staleVMs, threshold)
	for i, e := range staleEntries {
		slug, _ := extractProjectSlugAndDigest(e.Name)
		staleEntries[i].Slug = slug
	}

	// Process volumes: home volumes (slug/digest) and clone volumes.
	for _, h := range volumeHandles {
		name := h.Name()
		if !strings.HasPrefix(name, sbPrefix) {
			continue
		}

		if strings.HasPrefix(name, homePrefix) {
			slug, digest := extractProjectSlugAndDigest(name)
			if catalog.HomeVolumes[slug] == nil {
				catalog.HomeVolumes[slug] = make(map[string]string)
			}
			catalog.HomeVolumes[slug][digest] = name
		}

		if strings.HasPrefix(name, clonePrefix) {
			catalog.CloneVolumes = append(catalog.CloneVolumes, name)
		}
	}

	// Group MSB images by slug, excluding the base image.
	seenMSB := make(map[string]bool)
	for _, h := range imageHandles {
		ref := h.Reference()
		if !strings.HasPrefix(ref, imagePrefix) {
			continue
		}
		slug, digest := extractProjectSlugAndDigest(ref)
		if slug == baseSlug {
			continue
		}
		if seenMSB[ref] {
			continue
		}
		seenMSB[ref] = true
		catalog.MSBImages[slug] = append(catalog.MSBImages[slug], imageWithDigest{
			ref:      ref,
			digest:   digest,
			isLatest: digest == "",
		})
	}

	// Collect stale VMs and active VM digests for later reference in prune phases.
	catalog.StaleVMs = append(catalog.StaleVMs, staleEntries...)

	// Collect active VM digests (non-stale VMs with a digest).
	for _, vm := range staleVMs {
		slug, _ := extractProjectSlugAndDigest(vm.name)
		// Already-stale VMs: skip here to avoid collision with stale slugs.
		if isStaleSlug(staleEntries, slug) {
			continue
		}
		if vm.image != "" {
			_, digest := extractProjectSlugAndDigest(vm.image)
			if digest != "" {
				catalog.ActiveVMDigest[slug] = digest
			}
		}
	}

	return catalog, nil
}

// isStaleSlug checks if a slug matches any stale entry's slug.
func isStaleSlug(entries []StaleEntry, slug string) bool {
	for _, e := range entries {
		if e.Slug == slug {
			return true
		}
	}
	return false
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
	if report == nil || !report.HasAnything() {
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

	report, staleVMErr := pruneStaleVMs(ctx, msbClient, catalog, dryRun, ui, report)
	report, activeVMErr := pruneActiveVMArtifacts(ctx, msbClient, catalog, dryRun, ui, report)
	report, activeHomeVolErr := pruneActiveVMHomeVolume(ctx, msbClient, catalog, dryRun, ui, report)
	report, orphanErr := pruneOrphanArtifacts(ctx, msbClient, catalog, dryRun, ui, report)
	report, cloneVolErr := pruneCloneVolumes(ctx, msbClient, catalog, dryRun, ui, report)

	// Prune task sandboxes (collected during catalog build, pruned here).
	report, sandboxErrs := pruneTaskSandboxes(ctx, catalog, dryRun, msbClient, ui, report)
	return report, errors.Join(
		catalogErr,
		staleVMErr,
		activeVMErr,
		activeHomeVolErr,
		orphanErr,
		cloneVolErr,
		errors.Join(sandboxErrs...),
	)
}

func pruneTaskSandboxes(
	ctx context.Context,
	catalog *PruningCatalog,
	dryRun bool,
	msbClient msb.MsbClient,
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
	dryRun bool,
	ui termio.UI,
	report *StaleReport,
) (*StaleReport, error) {
	for _, entry := range catalog.StaleVMs {
		pruneStaleCascade(ctx, client, entry, catalog.HomeVolumes, catalog.MSBImages, dryRun, ui, report)
	}
	return report, nil
}

// pruneActiveVMArtifacts removes home volumes, MSB images, and Docker
// images that don't match an active VM's state.
//
//nolint:unparam // Error return is always nil; kept for uniform phase signature across callers
func pruneActiveVMArtifacts(
	ctx context.Context,
	client MsbClient,
	catalog *PruningCatalog,
	dryRun bool,
	ui termio.UI,
	report *StaleReport,
) (*StaleReport, error) {
	for slug, digest := range catalog.ActiveVMDigest {
		pruneActiveVMCleanup(ctx, client, slug, digest, catalog.HomeVolumes, catalog.MSBImages, dryRun, ui, report)
	}
	return report, nil
}

// pruneActiveVMHomeVolume checks if the volume tracked in the state file for
// each active VM still exists. If not (e.g., user ran `volume reset` externally),
// it creates a fresh replacement.
//
//nolint:unparam // Error return is always nil; kept for uniform phase signature across callers
func pruneActiveVMHomeVolume(
	ctx context.Context,
	client MsbClient,
	catalog *PruningCatalog,
	dryRun bool,
	ui termio.UI,
	report *StaleReport,
) (*StaleReport, error) {
	for slug := range catalog.ActiveVMDigest {
		state, err := ReadState(slug)
		if err != nil {
			ui.Warnf("corrupted state for slug %q, skipping: %v", slug, err)
			continue
		}
		if state == nil || state.HomeVolume == "" {
			continue
		}

		if _, err := client.GetVolume(ctx, state.HomeVolume); err != nil {
			if dryRun {
				ui.Infof("would create replacement volume for %q (slug %s)", state.HomeVolume, slug)
				continue
			}
			newVolName := HomeVolumeName(slug, "")
			if _, err := client.CreateVolume(ctx, newVolName, msbSdk.WithVolumeKind(msbSdk.VolumeKindDir)); err != nil {
				ui.Warnf("failed to create replacement volume for slug %q: %v", slug, err)
			} else {
				newState := HomeState{HomeVolume: newVolName, ImageDigest: state.ImageDigest}
				if writeErr := WriteState(slug, newState); writeErr != nil {
					ui.Warnf("failed to write state for slug %q: %v", slug, writeErr)
				}
			}
		}
	}
	return report, nil
}

// pruneOrphanArtifacts removes all home volumes, MSB images, and Docker
// images for project slugs that have no VM at all.
//
//nolint:unparam // Error return is always nil; kept for uniform phase signature across callers
func pruneOrphanArtifacts(
	ctx context.Context,
	client MsbClient,
	catalog *PruningCatalog,
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
		pruneOrphanSlug(ctx, client, slug, catalog.HomeVolumes, catalog.MSBImages, report, dryRun, ui)
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
		slug, _ := extractProjectSlugAndDigest(cv)
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
	homeBySlugDigest map[string]map[string]string,
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
	removeHomeVolumes(ctx, client, slug, homeBySlugDigest, dryRun, ui, report)
	removeMSBImages(ctx, client, slug, msbImagesBySlug, dryRun, ui, report)
	removeDockerImages(ctx, slug, msbImagesBySlug, dryRun, ui, report)
}

// pruneActiveVMCleanup removes volumes and images that don't match an active VM's state.
func pruneActiveVMCleanup(
	ctx context.Context,
	client MsbClient,
	slug string,
	digest string,
	homeBySlugDigest map[string]map[string]string,
	msbImagesBySlug map[string][]imageWithDigest,
	dryRun bool,
	ui termio.UI,
	report *StaleReport,
) {
	// Home volumes: no longer removed by digest matching.
	_ = homeBySlugDigest
	// Images: delete unused ones, keep :latest, keep matching digest.
	pruneActiveVMMSBImages(ctx, client, slug, digest, msbImagesBySlug, dryRun, ui, report)
	// Docker images: same logic.
	pruneActiveVMDockerImages(ctx, slug, digest, msbImagesBySlug, dryRun, ui, report)
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

// pruneCloneVolume removes a clone volume if it has no associated active VM.
// The staleVMs map is optional: when populated, clone volumes for stale VMs
// (cascade) are also pruned.
func pruneCloneVolume(
	ctx context.Context,
	client MsbClient,
	cv string,
	staleVMs map[string]bool,
	activeVMDigests map[string]string,
	dryRun bool,
	ui termio.UI,
	report *StaleReport,
) {
	slug, digest := extractProjectSlugAndDigest(cv)
	if staleVMs != nil && staleVMs[slug] {
		return
	}
	if _, active := activeVMDigests[slug]; active {
		return
	}
	if !dryRun {
		if err := client.RemoveVolume(ctx, cv); err != nil {
			ui.Warnf("failed to remove clone volume %s: %v", cv, err)
			return
		}
	}
	report.PrunedCloneVolumes++
	report.Details = append(report.Details, StaleEntry{
		Type:     StaleTypeVolume,
		Name:     cv,
		Slug:     slug,
		StaleFor: 0,
		Digest:   digest,
	})
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
	homeBySlugDigest map[string]map[string]string,
	msbImagesBySlug map[string][]imageWithDigest,
	report *StaleReport,
	dryRun bool,
	ui termio.UI,
) {
	removeHomeVolumes(ctx, client, slug, homeBySlugDigest, dryRun, ui, report)
	removeMSBImages(ctx, client, slug, msbImagesBySlug, dryRun, ui, report)
	removeDockerImages(ctx, slug, msbImagesBySlug, dryRun, ui, report)
}

func removeHomeVolumes(
	ctx context.Context,
	client MsbClient,
	slug string,
	homeBySlugDigest map[string]map[string]string,
	dryRun bool,
	ui termio.UI,
	report *StaleReport,
) {
	vols, ok := homeBySlugDigest[slug]
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
		if err := RemoveState(slug); err != nil {
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
