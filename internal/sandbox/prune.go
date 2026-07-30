package sandbox

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	msb "github.com/superradcompany/microsandbox/sdk/go"

	"gitlab.inoio.de/inoio/opencode-msb/internal/output"
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

// StaleEntry describes a single artifact that was pruned or would be pruned.
type StaleEntry struct {
	Type     string // "vm", "volume", "docker-image", "msb-image", "task-sandbox", "clone-volume"
	Name     string
	StaleFor time.Duration
	Slug     string // project slug, for grouping related artifacts
	Digest   string // for images/volumes: the identifying digest tag; empty for :latest or VMs
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
				Type:     "vm",
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

// Prune finds stale VMs, volumes, and images and removes them.
// dryRun=true collects artifacts without deleting.
// force skips confirmation (used for auto-prune).
// Logger is used for per-artifact warnings on non-fatal deletion errors.
func Prune(
	ctx context.Context,
	threshold time.Duration,
	dryRun bool,
	logger *output.Printer,
) (*StaleReport, error) {
	report := &StaleReport{
		PrunedVMs:           0,
		PrunedVolumes:       0,
		PrunedDockerImages:  0,
		PrunedMSBImages:     0,
		PrunedTaskSandboxes: 0,
		PrunedCloneVolumes:  0,
		Details:             nil,
	}

	// Step 1: list all sandboxes.
	sandboxHandles, err := msb.ListSandboxes(ctx)
	if err != nil {
		return nil, fmt.Errorf("list sandboxes: %w", err)
	}

	// Step 2: collect stale VMs, task sandboxes.
	var staleVMs []staleVM
	for _, h := range sandboxHandles {
		name := h.Name()
		status := h.Status()

		// Skip non-opencode sandboxes.
		if !strings.HasPrefix(name, "opencode-msb-") {
			continue
		}

		if strings.HasPrefix(name, projectVMPrefix) {
			if isStoppedStatus(status) {
				staleVMs = append(staleVMs, staleVM{
					name:      name,
					status:    status,
					updatedAt: h.UpdatedAt(),
					image:     "",
				})
				continue
			}
			// Active VM: extract image ref.
			cfg, configErr := h.Config()
			if configErr != nil {
				continue // best-effort: skip if config unreadable
			}
			staleVMs = append(staleVMs, staleVM{
				name:      name,
				status:    status,
				updatedAt: h.UpdatedAt(),
				image:     cfg.Image,
			})
		}

		if strings.HasPrefix(name, "opencode-msb-task-") {
			// Task sandboxes are always pruned.
			elapsed := time.Since(h.UpdatedAt())
			slug, _ := extractProjectSlugAndDigest(name)
			report.Details = append(report.Details, StaleEntry{
				Type:     "task-sandbox",
				Name:     name,
				StaleFor: elapsed,
				Slug:     slug,
				Digest:   "",
			})
			if !dryRun {
				if removeErr := msb.RemoveSandbox(ctx, name); removeErr != nil {
					logger.Warnf("failed to remove task sandbox %s: %v", name, removeErr)
					continue
				}
			}
			report.PrunedTaskSandboxes++
		}
	}

	staleEntries := findStaleVMs(staleVMs, threshold)
	for i, e := range staleEntries {
		slug, _ := extractProjectSlugAndDigest(e.Name)
		staleEntries[i].Slug = slug
		report.Details = append(report.Details, e)
	}

	// Step 3: list all volumes.
	volumeHandles, err := msb.ListVolumes(ctx)
	if err != nil {
		return nil, fmt.Errorf("list volumes: %w", err)
	}

	// Step 4: collect home volumes and clone volumes.
	homeBySlugDigest := make(map[string]map[string]string) // slug -> digest -> volume name
	cloneVolumes := make([]string, 0)

	for _, h := range volumeHandles {
		name := h.Name()
		if !strings.HasPrefix(name, "opencode-msb-") {
			continue
		}

		if strings.HasPrefix(name, "opencode-msb-home-") {
			slug, digest := extractProjectSlugAndDigest(name)
			if homeBySlugDigest[slug] == nil {
				homeBySlugDigest[slug] = make(map[string]string)
			}
			homeBySlugDigest[slug][digest] = name
		}

		if strings.HasPrefix(name, "opencode-msb-clone-") {
			cloneVolumes = append(cloneVolumes, name)
		}
	}

	// Step 5: list all MSB images.
	imageHandles, err := msb.Image.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("list msb images: %w", err)
	}

	// Step 6: group artifacts by slug.
	msbImagesBySlug := make(map[string][]imageWithDigest)
	seenMSB := make(map[string]bool)

	for _, h := range imageHandles {
		ref := h.Reference()
		if !strings.HasPrefix(ref, "opencode-msb/runner-") {
			continue
		}
		slug, digest := extractProjectSlugAndDigest(ref)
		if slug == "base" {
			continue
		}
		if seenMSB[ref] {
			continue
		}
		seenMSB[ref] = true
		msbImagesBySlug[slug] = append(msbImagesBySlug[slug], imageWithDigest{
			ref:      ref,
			digest:   digest,
			isLatest: digest == "",
		})
	}

	// Collect stale VM slugs.
	vmSlugs := make(map[string]bool)
	for _, entry := range staleEntries {
		vmSlugs[entry.Slug] = true
	}

	// Step 7: delete in order: VMs → volumes → MSB images → Docker images.

	// --- Case 1: Stale VM exists (cascade) ---
	for _, entry := range staleEntries {
		slug := entry.Slug
		if !dryRun {
			if err := msb.RemoveSandbox(ctx, entry.Name); err != nil {
				logger.Warnf("failed to remove stale VM %s: %v", entry.Name, err)
				continue
			}
		}
		report.PrunedVMs++
		// Cascade-delete all home volumes for this slug.
		if vols, ok := homeBySlugDigest[slug]; ok {
			for _, volName := range vols {
				if !dryRun {
					if err := msb.RemoveVolume(ctx, volName); err != nil {
						logger.Warnf("failed to remove home volume %s: %v", volName, err)
						continue
					}
				}
				report.PrunedVolumes++
			}
		}
		// Cascade-delete all images.
		for _, img := range msbImagesBySlug[slug] {
			if !dryRun {
				if err := msb.Image.Remove(ctx, img.ref, true); err != nil {
					logger.Warnf("failed to remove msb image %s: %v", img.ref, err)
					continue
				}
			}
			report.PrunedMSBImages++
		}
		for _, img := range msbImagesBySlug[slug] {
			if !dryRun {
				dockerRef := stripDockerHostPrefix(img.ref)
				cmd := exec.CommandContext(ctx, "docker", "rmi", dockerRef)
				if out, err := cmd.CombinedOutput(); err != nil {
					logger.Warnf("failed to remove docker image %s: %v: %s", dockerRef, err, string(out))
					continue
				}
			}
			report.PrunedDockerImages++
		}
	}

	// --- Case 2: Active VM exists (delete unused artifacts) ---
	// Collect active VM slugs -> digest.
	activeVMDigests := make(map[string]string)
	for _, vm := range staleVMs {
		slug, _ := extractProjectSlugAndDigest(vm.name)
		// Already-stale VMs: handled in Case 1 above. Skip here.
		if vmSlugs[slug] {
			continue
		}
		if vm.image != "" {
			_, digest := extractProjectSlugAndDigest(vm.image)
			activeVMDigests[slug] = digest
		}
	}
	for slug, digest := range activeVMDigests {
		// Home volumes: delete those NOT matching the VM's digest.
		if vols, ok := homeBySlugDigest[slug]; ok {
			for volDigest, volName := range vols {
				if volDigest == digest || volDigest == "" {
					continue // matches VM or :latest; always keep
				}
				// Unused digest volume.
				if !dryRun {
					if err := msb.RemoveVolume(ctx, volName); err != nil {
						logger.Warnf("failed to remove home volume %s: %v", volName, err)
						continue
					}
				}
				report.PrunedVolumes++
			}
		}
		// Images: delete unused ones, keep :latest, keep matching digest.
		for _, img := range msbImagesBySlug[slug] {
			if img.isLatest {
				continue // :latest always kept when VM exists
			}
			if img.digest == digest {
				continue // matches VM's digest; keep
			}
			// Delete unused digest image.
			if !dryRun {
				if err := msb.Image.Remove(ctx, img.ref, true); err != nil {
					logger.Warnf("failed to remove msb image %s: %v", img.ref, err)
					continue
				}
			}
			report.PrunedMSBImages++
		}
		// Docker: same.
		for _, img := range msbImagesBySlug[slug] {
			if img.isLatest {
				continue
			}
			if img.digest == digest {
				continue
			}
			if !dryRun {
				dockerRef := stripDockerHostPrefix(img.ref)
				cmd := exec.CommandContext(ctx, "docker", "rmi", dockerRef)
				if out, err := cmd.CombinedOutput(); err != nil {
					logger.Warnf("failed to remove docker image %s: %v: %s", dockerRef, err, string(out))
					continue
				}
			}
			report.PrunedDockerImages++
		}
	}

	// --- Case 3: No VM for slug (delete everything, no age threshold) ---
	// Collect all VM slugs.
	allVMSlugs := make(map[string]bool)
	for _, entry := range staleEntries {
		allVMSlugs[entry.Slug] = true
	}
	for slug := range activeVMDigests {
		allVMSlugs[slug] = true
	}
	for slug := range msbImagesBySlug {
		if !allVMSlugs[slug] {
			// No VM for this slug → delete everything.
			if vols, ok := homeBySlugDigest[slug]; ok {
				for _, volName := range vols {
					if !dryRun {
						if err := msb.RemoveVolume(ctx, volName); err != nil {
							logger.Warnf("failed to remove home volume %s: %v", volName, err)
							continue
						}
					}
					report.PrunedVolumes++
				}
			}
			for _, img := range msbImagesBySlug[slug] {
				if !dryRun {
					if err := msb.Image.Remove(ctx, img.ref, true); err != nil {
						logger.Warnf("failed to remove msb image %s: %v", img.ref, err)
						continue
					}
				}
				report.PrunedMSBImages++
			}
			for _, img := range msbImagesBySlug[slug] {
				if !dryRun {
					dockerRef := stripDockerHostPrefix(img.ref)
					cmd := exec.CommandContext(ctx, "docker", "rmi", dockerRef)
					if out, err := cmd.CombinedOutput(); err != nil {
						logger.Warnf("failed to remove docker image %s: %v: %s", dockerRef, err, string(out))
						continue
					}
				}
				report.PrunedDockerImages++
			}
		}
	}

	// Delete orphaned clone volumes (not associated with any active VM).
	// Also delete clone volumes for stale VM slugs (cascade) and orphan slugs.
	activeVMSlugs := make(map[string]bool)
	for slug := range activeVMDigests {
		activeVMSlugs[slug] = true
	}
	for _, cv := range cloneVolumes {
		slug, _ := extractProjectSlugAndDigest(cv)
		if activeVMSlugs[slug] {
			continue
		}
		// Clone volumes for stale OR orphan slugs → delete.
		if !vmSlugs[slug] {
			// Orphan slug: no VM at all → delete clone volume.
			if !dryRun {
				if err := msb.RemoveVolume(ctx, cv); err != nil {
					logger.Warnf("failed to remove clone volume %s: %v", cv, err)
					continue
				}
			}
			report.PrunedCloneVolumes++
			continue
		}
		// Already a stale VM with this slug. Delete orphaned clones.
		if !dryRun {
			if err := msb.RemoveVolume(ctx, cv); err != nil {
				logger.Warnf("failed to remove clone volume %s: %v", cv, err)
				continue
			}
		}
		report.PrunedCloneVolumes++
	}

	return report, nil
}

func stripDockerHostPrefix(ref string) string {
	if prefix, ok := strings.CutPrefix(ref, "docker.io/"); ok {
		return prefix
	}
	return ref
}
