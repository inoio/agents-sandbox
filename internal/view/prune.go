package view

import "github.com/inoio/agents-sandbox/internal/sandbox/pruning"

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
