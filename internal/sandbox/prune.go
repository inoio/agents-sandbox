package sandbox

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/moby/moby/client"

	msb "github.com/superradcompany/microsandbox/sdk/go"

	"gitlab.inoio.de/inoio/opencode-msb/internal/stdio"
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

//nolint:gochecknoglobals // fmt.stringer pattern for StaleType type / consts
var stateName = map[StaleType]string{
	StaleTypeVM:          "vm",
	StaleTypeVolume:      "volume",
	StaleTypeDockerImage: "docker-image",
	StaleTypeMsbImage:    "msb-image",
}

func (ss StaleType) String() string {
	return stateName[ss]
}

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
	status    msb.SandboxStatus
	updatedAt time.Time
	image     string // image ref for active VMs, empty for stale
}

// imageWithDigest holds a reference and its digest for MSB images.
type imageWithDigest struct {
	ref      string
	digest   string // empty string for :latest
	isLatest bool
}

const baseSlug = "base"

// findHashSuffix finds the start index of a 14-character base36 hash suffix
// in the name remainder (e.g. "saife-1mjusbm3wikhb0" -> returns 6, pointing
// at the '1' in the 14-char hash). Returns -1 when no such suffix is found.
func findHashSuffix(name string) int {
	for i := 1; i < len(name)-13; i++ {
		if name[i-1] != '-' {
			continue
		}
		ok := true
		for j := range 14 {
			c := name[i+j]
			if (c < 'a' || c > 'z') && (c < '0' || c > '9') {
				ok = false
				break
			}
		}
		if ok {
			return i
		}
	}
	return -1
}

// extractProjectSlugAndDigest extracts the project slug and optional digest
// from an artifact name (sandbox/volume/Docker image/MSB image).
//
// Examples:
//
//	"opencode-msb-vm-projectname-main" → slug="projectname", digest=""
//	"opencode-msb-home-myproject-aB3cDe4fGhIjKl-xYz1234AbCdEfGh" → slug="myproject-aB3cDe4fGhIjKl", digest="xYz1234AbCdEfGh"
//	"opencode-msb/runner-myproject:xYz1234AbCdEfGh" → slug="myproject", digest="xYz1234AbCdEfGh"
//
//nolint:nonamedreturns // named returns simplify the many return paths in this parser
func extractProjectSlugAndDigest(name string) (slug, digest string) {
	// Handle image references: opencode-msb/runner-{slug}:{tag}
	if strings.HasPrefix(name, "opencode-msb/runner-") {
		afterPrefix := name[len("opencode-msb/runner-"):]
		lastColon := strings.LastIndex(afterPrefix, ":")
		if lastColon == -1 {
			return afterPrefix, ""
		}
		tag := afterPrefix[lastColon+1:]
		slug = afterPrefix[:lastColon]
		if tag != "" && tag != "latest" {
			digest = tag
		}
		return slug, digest
	}

	// For sandbox and volume names, strip prefix and parse remainder.
	var prefixLen int
	var kind string
	switch {
	case strings.HasPrefix(name, "opencode-msb-vm-"):
		prefixLen = len("opencode-msb-vm-")
		kind = "vm"
	case strings.HasPrefix(name, "opencode-msb-home-"):
		prefixLen = len("opencode-msb-home-")
		kind = "home"
	case strings.HasPrefix(name, "opencode-msb-clone-"):
		prefixLen = len("opencode-msb-clone-")
		kind = "clone"
	case strings.HasPrefix(name, "opencode-msb-task-"):
		prefixLen = len("opencode-msb-task-")
		kind = "task"
	default:
		return "", ""
	}

	remainder := name[prefixLen:]
	parts := strings.Split(remainder, "-")

	if len(parts) < 2 {
		return remainder, ""
	}

	switch kind {
	case "vm":
		// VM: "folderName-hash14" or "folderName-hash14-branch"
		// Find the 14-char base36 hash that is part of the project slug,
		// then split everything before it as the folder name and the rest (if a branch follows) as the digest.
		hashStart := findHashSuffix(remainder)
		if hashStart == -1 {
			// No 14-char hash suffix found; the entire remainder is the slug.
			return remainder, ""
		}
		// hashStart is the index of the 14-char hash within the remainder.
		// The folder name is remainder[:hashStart-1] (everything before the hyphen).
		// The hash is remainder[hashStart:hashStart+14].
		// Everything after the hash (starting at hashStart+14) is the branch (digest).
		folderName := remainder[:hashStart-1]
		hash := remainder[hashStart : hashStart+14]
		slug = folderName + "-" + hash
		if hashStart+14 < len(remainder) {
			rest := remainder[hashStart+14:]
			if len(rest) > 1 && rest[0] == '-' {
				digest = rest[1:]
			}
		}
		return slug, digest
	case "home":
		// Home volume: "slug-digest" → digest is last part, rest is slug.
		digest = parts[len(parts)-1]
		slug = strings.Join(parts[:len(parts)-1], "-")
		return slug, digest
	default:
		// Clone volumes and task sandboxes: no digest, just slug.
		return strings.Join(parts[:len(parts)-1], "-"), ""
	}
}

// isStoppedStatus returns true if the status indicates the sandbox is not
// actively running (stopped or crashed).
func isStoppedStatus(status msb.SandboxStatus) bool {
	return status == msb.SandboxStatusStopped || status == msb.SandboxStatusCrashed
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
func buildCatalog(ctx context.Context, client msbClient, threshold time.Duration) (*PruningCatalog, error) {
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
		if !strings.HasPrefix(name, "opencode-msb-") {
			continue
		}

		if strings.HasPrefix(name, projectVMPrefix) {
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

		if strings.HasPrefix(name, "opencode-msb-task-") {
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
		if !strings.HasPrefix(name, "opencode-msb-") {
			continue
		}

		if strings.HasPrefix(name, "opencode-msb-home-") {
			slug, digest := extractProjectSlugAndDigest(name)
			if catalog.HomeVolumes[slug] == nil {
				catalog.HomeVolumes[slug] = make(map[string]string)
			}
			catalog.HomeVolumes[slug][digest] = name
		}

		if strings.HasPrefix(name, "opencode-msb-clone-") {
			catalog.CloneVolumes = append(catalog.CloneVolumes, name)
		}
	}

	// Group MSB images by slug, excluding the base image.
	seenMSB := make(map[string]bool)
	for _, h := range imageHandles {
		ref := h.Reference()
		if !strings.HasPrefix(ref, "opencode-msb/runner-") {
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
	cli dockerClient,
	threshold time.Duration,
	dryRun bool,
	ui stdio.UI,
) (*StaleReport, error) {
	client := newMsbClient()
	report := &StaleReport{} //nolint:exhaustruct // Counts initialized to zero, populated during pruning

	catalog, err := buildCatalog(ctx, client, threshold)
	if err != nil {
		return nil, err
	}

	report, _ = pruneStaleVMs(ctx, client, cli, catalog, dryRun, ui, report)
	report, _ = pruneActiveVMArtifacts(ctx, client, cli, catalog, dryRun, ui, report)
	report, _ = pruneOrphanArtifacts(ctx, client, cli, catalog, dryRun, ui, report)
	report, _ = pruneCloneVolumes(ctx, client, catalog, dryRun, ui, report)

	// Prune task sandboxes (collected during catalog build, pruned here).
	for _, entry := range catalog.TaskSandboxes {
		if !dryRun {
			if removeErr := client.RemoveSandbox(ctx, entry.Name); removeErr != nil {
				ui.Warnf("failed to remove task sandbox %s: %v", entry.Name, removeErr)
				continue
			}
		}
		report.PrunedTaskSandboxes++
		report.Details = append(report.Details, entry)
	}

	return report, nil
}

// pruneStaleVMs removes each stale VM and cascades deletion of all
// associated artifacts (home volumes, MSB images, Docker images).
func pruneStaleVMs(
	ctx context.Context,
	client msbClient,
	cli dockerClient,
	catalog *PruningCatalog,
	dryRun bool,
	ui stdio.UI,
	report *StaleReport,
) (*StaleReport, bool) {
	for _, entry := range catalog.StaleVMs {
		pruneStaleCascade(ctx, client, cli, entry, catalog.HomeVolumes, catalog.MSBImages, dryRun, ui, report)
	}
	return report, true
}

// pruneActiveVMArtifacts removes home volumes, MSB images, and Docker
// images that don't match an active VM's state.
func pruneActiveVMArtifacts(
	ctx context.Context,
	client msbClient,
	cli dockerClient,
	catalog *PruningCatalog,
	dryRun bool,
	ui stdio.UI,
	report *StaleReport,
) (*StaleReport, bool) {
	for slug, digest := range catalog.ActiveVMDigest {
		pruneActiveVMCleanup(ctx, client, cli, slug, digest, catalog.HomeVolumes, catalog.MSBImages, dryRun, ui, report)
	}
	return report, true
}

// pruneOrphanArtifacts removes all home volumes, MSB images, and Docker
// images for project slugs that have no VM at all.
func pruneOrphanArtifacts(
	ctx context.Context,
	client msbClient,
	cli dockerClient,
	catalog *PruningCatalog,
	dryRun bool,
	ui stdio.UI,
	report *StaleReport,
) (*StaleReport, bool) {
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
		pruneOrphanSlug(ctx, client, cli, slug, catalog.HomeVolumes, catalog.MSBImages, report, dryRun, ui)
	}
	return report, true
}

// pruneCloneVolumes removes clone volumes whose project slug has no
// associated active VM.
func pruneCloneVolumes(
	ctx context.Context,
	client msbClient,
	catalog *PruningCatalog,
	dryRun bool,
	ui stdio.UI,
	report *StaleReport,
) (*StaleReport, bool) {
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
			StaleFor: time.Duration(10),
			Digest:   "???",
		})
	}
	return report, true
}

// pruneStaleCascade removes a stale VM and all associated artifacts (volumes, images).
func pruneStaleCascade(
	ctx context.Context,
	client msbClient,
	cli dockerClient,
	entry StaleEntry,
	homeBySlugDigest map[string]map[string]string,
	msbImagesBySlug map[string][]imageWithDigest,
	dryRun bool,
	ui stdio.UI,
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
	removeDockerImages(ctx, slug, cli, msbImagesBySlug, dryRun, ui, report)
}

// pruneActiveVMCleanup removes volumes and images that don't match an active VM's state.
func pruneActiveVMCleanup(
	ctx context.Context,
	client msbClient,
	cli dockerClient,
	slug string,
	digest string,
	homeBySlugDigest map[string]map[string]string,
	msbImagesBySlug map[string][]imageWithDigest,
	dryRun bool,
	ui stdio.UI,
	report *StaleReport,
) {
	// Home volumes: delete those NOT matching the VM's digest.
	pruneActiveVMHomeVolumes(ctx, client, slug, digest, homeBySlugDigest, dryRun, ui, report)
	// Images: delete unused ones, keep :latest, keep matching digest.
	pruneActiveVMMSBImages(ctx, client, slug, digest, msbImagesBySlug, dryRun, ui, report)
	// Docker images: same logic.
	pruneActiveVMDockerImages(ctx, cli, slug, digest, msbImagesBySlug, dryRun, ui, report)
}

func pruneActiveVMHomeVolumes(
	ctx context.Context,
	client msbClient,
	slug string,
	digest string,
	homeBySlugDigest map[string]map[string]string,
	dryRun bool,
	ui stdio.UI,
	report *StaleReport,
) {
	if vols, ok := homeBySlugDigest[slug]; ok {
		for volDigest, volName := range vols {
			if volDigest == digest || volDigest == "" {
				continue
			}
			if !dryRun {
				if err := client.RemoveVolume(ctx, volName); err != nil {
					ui.Warnf("failed to remove home volume %s: %v", volName, err)
					continue
				}
			}
			report.PrunedVolumes++
			report.Details = append(report.Details, StaleEntry{
				Type:     StaleTypeVolume,
				Name:     volName,
				Slug:     slug,
				StaleFor: time.Duration(10),
				Digest:   digest,
			})
		}
	}
}

func pruneActiveVMMSBImages(
	ctx context.Context,
	client msbClient,
	slug string,
	digest string,
	msbImagesBySlug map[string][]imageWithDigest,
	dryRun bool,
	ui stdio.UI,
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
			StaleFor: time.Duration(10),
			Digest:   img.digest,
		})
	}
}

func pruneActiveVMDockerImages(
	ctx context.Context,
	cli dockerClient,
	slug string,
	digest string,
	msbImagesBySlug map[string][]imageWithDigest,
	dryRun bool,
	ui stdio.UI,
	report *StaleReport,
) {
	for _, img := range msbImagesBySlug[slug] {
		if img.isLatest || img.digest == digest {
			continue
		}
		if !dryRun {
			dockerRef := stripDockerHostPrefix(img.ref)
			_, err := cli.ImageRemove(
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
			StaleFor: time.Duration(10),
			Digest:   img.digest,
		})
	}
}

// pruneCloneVolume removes a clone volume if it has no associated active VM.
// The staleVMs map is optional: when populated, clone volumes for stale VMs
// (cascade) are also pruned.
func pruneCloneVolume(
	ctx context.Context,
	client msbClient,
	cv string,
	staleVMs map[string]bool,
	activeVMDigests map[string]string,
	dryRun bool,
	ui stdio.UI,
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
		StaleFor: time.Duration(10),
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
	client msbClient,
	cli dockerClient,
	slug string,
	homeBySlugDigest map[string]map[string]string,
	msbImagesBySlug map[string][]imageWithDigest,
	report *StaleReport,
	dryRun bool,
	ui stdio.UI,
) {
	removeHomeVolumes(ctx, client, slug, homeBySlugDigest, dryRun, ui, report)
	removeMSBImages(ctx, client, slug, msbImagesBySlug, dryRun, ui, report)
	removeDockerImages(ctx, slug, cli, msbImagesBySlug, dryRun, ui, report)
}

func removeHomeVolumes(
	ctx context.Context,
	client msbClient,
	slug string,
	homeBySlugDigest map[string]map[string]string,
	dryRun bool,
	ui stdio.UI,
	report *StaleReport,
) {
	if vols, ok := homeBySlugDigest[slug]; ok {
		for digest, volName := range vols {
			if !dryRun {
				if err := client.RemoveVolume(ctx, volName); err != nil {
					ui.Warnf("failed to remove home volume %s: %v", volName, err)
					continue
				}
			}
			report.PrunedVolumes++
			report.Details = append(report.Details, StaleEntry{
				Type:     StaleTypeVolume,
				Name:     volName,
				Slug:     slug,
				StaleFor: time.Duration(10),
				Digest:   digest,
			})
		}
	}
}

func removeMSBImages(
	ctx context.Context,
	client msbClient,
	slug string,
	msbImagesBySlug map[string][]imageWithDigest,
	dryRun bool,
	ui stdio.UI,
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
			StaleFor: time.Duration(10),
			Digest:   img.digest,
		})
	}
}

func removeDockerImages(
	ctx context.Context,
	slug string,
	cli dockerClient,
	msbImagesBySlug map[string][]imageWithDigest,
	dryRun bool,
	ui stdio.UI,
	report *StaleReport,
) {
	for _, img := range msbImagesBySlug[slug] {
		if !dryRun {
			dockerRef := stripDockerHostPrefix(img.ref)
			_, err := cli.ImageRemove(
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
			StaleFor: time.Duration(10),
			Digest:   img.digest,
		})
	}
}
