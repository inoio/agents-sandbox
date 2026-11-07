package view

import (
	"fmt"

	"github.com/inoio/agents-sandbox/internal/sandbox/pruning"
	"github.com/inoio/agents-sandbox/internal/termio"
)

type pruneDetailJSON struct {
	Type            string `json:"type"`
	Name            string `json:"name"`
	Slug            string `json:"slug"`
	Digest          string `json:"digest"`
	StaleForSeconds int64  `json:"stale_for_seconds"`
}

// PruneReportJSON is the --format json shape of a full prune report. Optional
// byte counts are pointers so an unknown reclamation marshals as null, never 0.
type PruneReportJSON struct {
	DryRun                        bool              `json:"dry_run"`
	Sandboxes                     int               `json:"sandboxes"`
	Volumes                       int               `json:"volumes"`
	Images                        int               `json:"images"`
	DockerImages                  int               `json:"docker_images"`
	SkippedImagesInUse            uint32            `json:"skipped_images_in_use"`
	ImageBytesReclaimed           *uint64           `json:"image_bytes_reclaimed"`
	RuntimeFilesRemoved           uint64            `json:"runtime_files_removed"`
	RuntimeLogicalBytesRemoved    uint64            `json:"runtime_logical_bytes_removed"`
	RuntimePhysicalBytesReclaimed *uint64           `json:"runtime_physical_bytes_reclaimed"`
	Details                       []pruneDetailJSON `json:"details"`
	RetiredProjects               []string          `json:"retired_projects"`
}

// ImageReportJSON is the --format json shape of an image prune report.
type ImageReportJSON struct {
	DryRun         bool              `json:"dry_run"`
	Images         int               `json:"images"`
	DockerImages   int               `json:"docker_images"`
	SkippedInUse   uint32            `json:"skipped_in_use"`
	BytesReclaimed *uint64           `json:"bytes_reclaimed"`
	Details        []pruneDetailJSON `json:"details"`
}

// VolumeReportJSON is the --format json shape of a volume prune report.
type VolumeReportJSON struct {
	DryRun  bool              `json:"dry_run"`
	Volumes int               `json:"volumes"`
	Details []pruneDetailJSON `json:"details"`
}

// SandboxReportJSON is the --format json shape of a sandbox prune report.
type SandboxReportJSON struct {
	DryRun    bool              `json:"dry_run"`
	Sandboxes int               `json:"sandboxes"`
	Details   []pruneDetailJSON `json:"details"`
}

func NewPruneReportJSON(report *pruning.StaleReport, dryRun bool) PruneReportJSON {
	return PruneReportJSON{
		DryRun:                        dryRun,
		Sandboxes:                     report.PrunedSandboxes,
		Volumes:                       report.PrunedVolumes,
		Images:                        report.PrunedSandboxImages,
		DockerImages:                  report.PrunedDockerImages,
		SkippedImagesInUse:            report.SkippedImagesInUse,
		ImageBytesReclaimed:           report.ImageBytesReclaimed,
		RuntimeFilesRemoved:           report.Storage.FilesRemoved,
		RuntimeLogicalBytesRemoved:    report.Storage.LogicalBytesRemoved,
		RuntimePhysicalBytesReclaimed: report.Storage.PhysicalBytesReclaimed,
		Details:                       newPruneDetailsJSON(report.Details),
		RetiredProjects:               []string{},
	}
}

func NewImageReportJSON(report pruning.ImageReport, dryRun bool) ImageReportJSON {
	return ImageReportJSON{
		DryRun:         dryRun,
		Images:         report.MSBImagesPruned,
		DockerImages:   report.DockerImagesPruned,
		SkippedInUse:   report.SkippedInUse,
		BytesReclaimed: report.BytesReclaimed,
		Details:        newPruneDetailsJSON(report.Details),
	}
}

func NewVolumeReportJSON(report pruning.VolumeReport, dryRun bool) VolumeReportJSON {
	return VolumeReportJSON{
		DryRun:  dryRun,
		Volumes: report.VolumesPruned,
		Details: newPruneDetailsJSON(report.Details),
	}
}

func NewSandboxReportJSON(report pruning.SandboxReport, dryRun bool) SandboxReportJSON {
	return SandboxReportJSON{
		DryRun:    dryRun,
		Sandboxes: report.VMsPruned,
		Details:   newPruneDetailsJSON(report.Details),
	}
}

func newPruneDetailsJSON(entries []pruning.StaleEntry) []pruneDetailJSON {
	details := make([]pruneDetailJSON, 0, len(entries))
	for _, entry := range entries {
		details = append(details, pruneDetailJSON{
			Type:            entry.Type.String(),
			Name:            entry.Name,
			Slug:            entry.Slug,
			Digest:          entry.Digest,
			StaleForSeconds: int64(entry.StaleFor.Seconds()),
		})
	}
	return details
}

// RemovalPlanJSON is the --format json shape of a targeted purge plan. Keys are
// rendered "slug/agent" (a legacy agent-less key as the bare slug), matching the
// claim-key convention. Empty artifact lists marshal as [] rather than null.
type RemovalPlanJSON struct {
	DryRun         bool     `json:"dry_run"`
	Slugs          []string `json:"slugs"`
	Keys           []string `json:"keys"`
	Sandboxes      []string `json:"sandboxes"`
	Volumes        []string `json:"volumes"`
	Images         []string `json:"images"`
	StateFiles     []string `json:"state_files"`
	Claims         []string `json:"claims"`
	AdoptionClaims []string `json:"adoption_claims"`
}

func NewRemovalPlanJSON(plan *pruning.RemovalPlan, dryRun bool) RemovalPlanJSON {
	keys := make([]string, 0, len(plan.Keys))
	for _, key := range plan.Keys {
		name := key.Slug
		if key.Agent != "" {
			name += "/" + key.Agent
		}
		keys = append(keys, name)
	}
	return RemovalPlanJSON{
		DryRun:         dryRun,
		Slugs:          removalPlanSlice(plan.Slugs),
		Keys:           keys,
		Sandboxes:      removalPlanSlice(plan.Sandboxes),
		Volumes:        removalPlanSlice(plan.Volumes),
		Images:         removalPlanSlice(plan.Images),
		StateFiles:     removalPlanSlice(plan.StateFiles),
		Claims:         removalPlanSlice(plan.Claims),
		AdoptionClaims: removalPlanSlice(plan.AdoptionClaims),
	}
}

// removalPlanSlice copies names into a non-nil slice, so an empty plan artifact
// marshals as [] rather than null.
func removalPlanSlice(names []string) []string {
	return append([]string{}, names...)
}

// RenderRemovalPlan names every artifact a targeted purge removes, so a user
// approves exactly what will be deleted. The same list is printed for a dry
// run, phrased as what would be removed. It writes through the quiet-immune
// channel (termio.Output.Print): a destructive purge must name what it removes
// even under --quiet, unlike an ordinary prune summary. It reports through
// termio.Output; cliout stays domain-free.
func RenderRemovalPlan(plan *pruning.RemovalPlan, ui termio.Output, dryRun bool) {
	verb := "will"
	if dryRun {
		verb = "would"
	}
	ui.Print(fmt.Sprintf("Purge %s remove:", verb))
	renderRemovalSlugs(ui, plan.Slugs)
	renderRemovalArtifacts(ui, "sandboxes", plan.Sandboxes)
	renderRemovalArtifacts(ui, "home volumes", plan.Volumes)
	renderRemovalArtifacts(ui, "runner images", plan.Images)
	renderRemovalArtifacts(ui, "state files", plan.StateFiles)
	renderRemovalArtifacts(ui, "claims", plan.Claims)
	renderRemovalArtifacts(ui, "adoption claims", plan.AdoptionClaims)
}

// renderRemovalSlugs prints the plan's projects, singular when there is one.
func renderRemovalSlugs(ui termio.Output, slugs []string) {
	switch len(slugs) {
	case 0:
		return
	case 1:
		ui.Print(fmt.Sprintf("  project: %s", slugs[0]))
	default:
		ui.Print("  projects:")
		for _, slug := range slugs {
			ui.Print(fmt.Sprintf("    - %s", slug))
		}
	}
}

// renderRemovalArtifacts prints a non-empty artifact group as an indented list.
func renderRemovalArtifacts(ui termio.Output, label string, names []string) {
	if len(names) == 0 {
		return
	}
	ui.Print(fmt.Sprintf("  %s:", label))
	for _, name := range names {
		ui.Print(fmt.Sprintf("    - %s", name))
	}
}

// RenderRetiredProjects names each retired project and the purge command that
// removes it. A batch prune never deletes a retired project's home volume or
// state; it lists the candidate and points at the explicit purge. It prints
// nothing when no project is retired, so a batch prune with nothing to report
// keeps its previous output.
func RenderRetiredProjects(ui termio.Output, slugs []string) {
	for _, slug := range slugs {
		ui.Outf("  retired project: %s (purge with: prune --purge %s)", slug, slug)
	}
}
