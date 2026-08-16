package pruning

import (
	"context"
	"fmt"
	"strings"
	"time"

	"gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/msb"
	"gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/naming"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"
)

// staleVM is an internal type used by findStaleVMs.
type staleVM struct {
	name      string
	status    msbSdk.SandboxStatus
	updatedAt time.Time
	image     string // image ref for active VMs, empty for stale
}

// findStaleVMs filters sandboxes to only those that are stopped/crashed and
// older than the given threshold.
func findStaleVMs(sandboxes []staleVM, threshold time.Duration) []StaleEntry {
	var stale []StaleEntry
	for _, sandbox := range sandboxes {
		if msb.IsSandboxActive(sandbox.status) {
			continue
		}
		elapsed := time.Since(sandbox.updatedAt)
		if elapsed > threshold {
			stale = append(stale, StaleEntry{
				Type:     StaleTypeVM,
				Name:     sandbox.name,
				StaleFor: elapsed,
				Slug:     "",
				Digest:   "",
			})
		}
	}
	return stale
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
		HomeVolumes:    make(map[string][]volumeWithAge),
		CloneVolumes:   make([]string, 0),
		MSBImages:      make(map[string][]imageWithDigest),
		ActiveVMDigest: make(map[string]string),
	}

	staleVMs := make([]staleVM, 0)

	// Process sandboxes: collect stale VMs, task sandboxes, and active VMs.
	for _, h := range sandboxHandles {
		name := h.Name()
		if !strings.HasPrefix(name, naming.SbPrefix) {
			continue
		}

		if strings.HasPrefix(name, naming.VmPrefix) {
			staleVMs = append(staleVMs, staleVM{
				name:      name,
				status:    h.Status(),
				updatedAt: h.UpdatedAt(),
				image:     h.Image(),
			})
		}

		if strings.HasPrefix(name, naming.TaskPrefix) {
			// Task sandboxes are always pruned immediately.
			elapsed := time.Since(h.UpdatedAt())
			slug := naming.ArtifactFor(name).Slug
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
		slug := naming.ArtifactFor(e.Name).Slug
		staleEntries[i].Slug = slug
	}

	// Process volumes: home volumes (slug/digest) and clone volumes.
	for _, h := range volumeHandles {
		name := h.Name()
		if !strings.HasPrefix(name, naming.SbPrefix) {
			continue
		}

		if strings.HasPrefix(name, naming.HomePrefix) {
			slug := naming.ArtifactFor(name).Slug
			if catalog.HomeVolumes[slug] == nil {
				catalog.HomeVolumes[slug] = []volumeWithAge{}
			}
			catalog.HomeVolumes[slug] = append(catalog.HomeVolumes[slug], volumeWithAge{
				name:      name,
				createdAt: h.CreatedAt(),
			})
		}

		if strings.HasPrefix(name, naming.ClonePrefix) {
			catalog.CloneVolumes = append(catalog.CloneVolumes, name)
		}
	}

	// Group MSB images by slug, excluding the base image.
	seenMSB := make(map[string]bool)
	for _, h := range imageHandles {
		ref := h.Reference()
		if !strings.HasPrefix(ref, naming.ImagePrefix) {
			continue
		}
		info := naming.ArtifactFor(ref)
		if info.Slug == naming.BaseSlug {
			continue
		}
		if seenMSB[ref] {
			continue
		}
		seenMSB[ref] = true
		catalog.MSBImages[info.Slug] = append(catalog.MSBImages[info.Slug], imageWithDigest{
			ref:      ref,
			digest:   info.Digest,
			isLatest: info.Digest == "",
			lastUsed: h.LastUsedAt(),
		})
	}

	// Collect stale VMs and active VM digests for later reference in prune phases.
	catalog.StaleVMs = append(catalog.StaleVMs, staleEntries...)

	// Collect active VM digests (non-stale VMs with a digest). Stopped-but-not
	// stale VMs are included so their artifact directories are cascaded before
	// they age into staleness; this keeps the running image around for quick restarts.
	for _, vm := range staleVMs {
		slug := naming.ArtifactFor(vm.name).Slug
		// Already-stale VMs: skip here to avoid collision with stale slugs.
		if isStaleSlug(staleEntries, slug) {
			continue
		}
		if vm.image != "" {
			if digest := naming.ArtifactFor(vm.image).Digest; digest != "" {
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
