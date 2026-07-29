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
}

// staleVM is an internal type used by findStaleVMs.
type staleVM struct {
	name      string
	status    msb.SandboxStatus
	updatedAt time.Time
}

// extractProjectSlugAndDigest extracts the project slug and optional digest
// from an artifact name (sandbox/volume/Docker image/MSB image).
//
// Examples:
//
//	"opencode-msb-vm-projectname-main" → slug="projectname", digest=""
//	"opencode-msb-home-myproject-aB3cDe4fGhIjKl-xYz1234AbCdEfGh" → slug="myproject-aB3cDe4fGhIjKl", digest="xYz1234AbCdEfGh"
//	"opencode-msb/runner-myproject:xYz1234AbCdEfGh" → slug="myproject", digest="xYz1234AbCdEfGh"
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
		// VM: "slug-branch" → slug is everything before last dash.
		return strings.Join(parts[:len(parts)-1], "-"), ""
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
	dryRun, force bool,
	logger *output.Printer,
) (*StaleReport, error) {
	report := &StaleReport{}

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
				})
			}
			continue
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
			})
			if !dryRun {
				if removeErr := msb.RemoveSandbox(ctx, name); removeErr != nil {
					logger.Warnf("failed to remove task sandbox %s: %v", name, removeErr)
				} else {
					report.PrunedTaskSandboxes++
				}
			}
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
	allHomeVolumes := make(map[string]string) // name -> slug
	cloneVolumes := make([]string, 0)

	for _, h := range volumeHandles {
		name := h.Name()
		if !strings.HasPrefix(name, "opencode-msb-") {
			continue
		}

		if strings.HasPrefix(name, "opencode-msb-home-") {
			slug, _ := extractProjectSlugAndDigest(name)
			allHomeVolumes[name] = slug
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
	homeBySlug := make(map[string][]string)      // slug -> home volume names
	msbImagesBySlug := make(map[string][]string) // slug -> msb image refs
	seenMSB := make(map[string]bool)

	for name, slug := range allHomeVolumes {
		homeBySlug[slug] = append(homeBySlug[slug], name)
	}

	for _, h := range imageHandles {
		ref := h.Reference()
		if !strings.HasPrefix(ref, "opencode-msb/runner-") {
			continue
		}
		slug, _ := extractProjectSlugAndDigest(ref)
		if slug == "base" {
			continue
		}
		if !seenMSB[ref] {
			seenMSB[ref] = true
			msbImagesBySlug[slug] = append(msbImagesBySlug[slug], ref)
		}
	}

	// Collect stale VM slugs.
	vmSlugs := make(map[string]bool)
	for _, entry := range staleEntries {
		vmSlugs[entry.Slug] = true
	}

	// Step 7: delete in order: VMs → volumes → MSB images → Docker images.

	// Delete VMs.
	for _, entry := range staleEntries {
		if !dryRun {
			if removeErr := msb.RemoveSandbox(ctx, entry.Name); removeErr != nil {
				logger.Warnf("failed to remove stale VM %s: %v", entry.Name, removeErr)
			} else {
				report.PrunedVMs++
			}
		}
	}

	// Delete home volumes for stale slugs.
	for _, entry := range staleEntries {
		slug := entry.Slug
		if homes, ok := homeBySlug[slug]; ok {
			for _, v := range homes {
				if !dryRun {
					if removeErr := msb.RemoveVolume(ctx, v); removeErr != nil {
						logger.Warnf("failed to remove home volume %s: %v", v, removeErr)
					} else {
						report.PrunedVolumes++
					}
				}
			}
		}
	}

	// Delete MSB images for stale slugs.
	// Also track Docker image refs to delete alongside MSB images.
	for slug, refs := range msbImagesBySlug {
		if !vmSlugs[slug] {
			continue
		}
		for _, ref := range refs {
			if !dryRun {
				if removeErr := msb.Image.Remove(ctx, ref, true); removeErr != nil {
					logger.Warnf("failed to remove MSB image %s: %v", ref, removeErr)
				} else {
					report.PrunedMSBImages++
				}
			}
			// Docker image with the same reference
			dockerRef := stripDockerHostPrefix(ref)
			if !dryRun {
				cmd := exec.CommandContext(ctx, "docker", "rmi", dockerRef)
				if out, err := cmd.CombinedOutput(); err != nil {
					logger.Warnf("failed to remove docker image %s: %v: %s", dockerRef, err, string(out))
				} else {
					report.PrunedDockerImages++
				}
			}
		}
	}

	// Delete orphaned clone volumes.
	for _, cv := range cloneVolumes {
		extractProjectSlugAndDigest(cv)
		if !dryRun {
			if removeErr := msb.RemoveVolume(ctx, cv); removeErr != nil {
				logger.Warnf("failed to remove clone volume %s: %v", cv, removeErr)
			} else {
				report.PrunedCloneVolumes++
			}
		}
	}

	return report, nil
}

func stripDockerHostPrefix(ref string) string {
	if strings.HasPrefix(ref, "docker.io/") {
		return strings.TrimPrefix(ref, "docker.io/")
	}
	return ref
}
