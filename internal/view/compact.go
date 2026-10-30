package view

import "github.com/inoio/agents-sandbox/internal/sandbox/pruning"

// CompactReportJSON is the --format json shape of a sandbox compact report.
type CompactReportJSON struct {
	DryRun    bool                 `json:"dry_run"`
	Sandboxes []compactSandboxJSON `json:"sandboxes"`
}

type compactSandboxJSON struct {
	Name              string            `json:"name"`
	Slug              string            `json:"slug"`
	InputLayers       uint32            `json:"input_layers"`
	SelectedLayers    uint32            `json:"selected_layers"`
	OutputLayers      uint32            `json:"output_layers"`
	MaterializedBytes uint64            `json:"materialized_bytes"`
	TotalMicros       uint64            `json:"total_us"`
	PauseMicros       uint64            `json:"pause_us"`
	Disks             []compactDiskJSON `json:"disks"`
}

type compactDiskJSON struct {
	GuestPath         string `json:"guest_path"`
	InputLayers       uint32 `json:"input_layers"`
	SelectedLayers    uint32 `json:"selected_layers"`
	OutputLayers      uint32 `json:"output_layers"`
	MaterializedBytes uint64 `json:"materialized_bytes"`
	TotalMicros       uint64 `json:"total_us"`
}

func NewCompactReportJSON(report pruning.CompactReport) CompactReportJSON {
	sandboxes := make([]compactSandboxJSON, 0, len(report.Entries))
	for _, entry := range report.Entries {
		disks := make([]compactDiskJSON, 0, len(entry.Result.Disks))
		for _, disk := range entry.Result.Disks {
			disks = append(disks, compactDiskJSON{
				GuestPath:         disk.GuestPath,
				InputLayers:       disk.InputLayers,
				SelectedLayers:    disk.SelectedLayers,
				OutputLayers:      disk.OutputLayers,
				MaterializedBytes: disk.MaterializedBytes,
				TotalMicros:       disk.TotalUs,
			})
		}
		sandboxes = append(sandboxes, compactSandboxJSON{
			Name:              entry.Name,
			Slug:              entry.Slug,
			InputLayers:       entry.Result.InputLayers,
			SelectedLayers:    entry.Result.SelectedLayers,
			OutputLayers:      entry.Result.OutputLayers,
			MaterializedBytes: entry.Result.MaterializedBytes,
			TotalMicros:       entry.Result.TotalUs,
			PauseMicros:       entry.Result.PauseUs,
			Disks:             disks,
		})
	}
	return CompactReportJSON{DryRun: report.DryRun, Sandboxes: sandboxes}
}
