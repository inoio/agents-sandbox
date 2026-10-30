package pruning

import (
	"context"
	"fmt"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/humanize"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/termio"
)

const microsecondsPerMillisecond = 1000

// CompactEntry is the compaction outcome of one project VM.
type CompactEntry struct {
	Name   string
	Slug   string
	Result msbSdk.DiskCompactionResult
}

// CompactReport summarizes a CompactSandboxes run.
type CompactReport struct {
	DryRun  bool
	Entries []CompactEntry
}

// CompactSandboxes merges the sealed copy-on-write layers of every project VM's
// sandbox-owned disks (or only the VM named sandboxName, when set). Task
// sandboxes are left alone, as are named volumes: the SDK never compacts those. A VM that fails is warned about and skipped so one
// broken VM does not block the rest.
func CompactSandboxes(
	ctx context.Context,
	sandboxName string,
	options msbSdk.DiskCompactionOptions,
	ui termio.UI,
) (CompactReport, error) {
	handles, err := projectSandboxes(ctx, sandboxName)
	if err != nil {
		return CompactReport{}, err
	}
	report := CompactReport{DryRun: options.DryRun, Entries: nil}
	for _, handle := range handles {
		name := handle.Name()
		result, err := handle.Compact(ctx, options)
		if err != nil {
			reportCompactFailure(ui, name, err)
			continue
		}
		report.Entries = append(
			report.Entries,
			CompactEntry{Name: name, Slug: noteIdentity(handle).Slug, Result: *result},
		)
	}
	return report, nil
}

// projectSandboxes lists the project VMs (not task workers), narrowed to sandboxName when set.
func projectSandboxes(ctx context.Context, sandboxName string) ([]msb.SandboxHandle, error) {
	handles, err := msb.Get().ListSandboxes(ctx, nil)
	if err != nil {
		return nil, err
	}
	var projects []msb.SandboxHandle
	for _, handle := range handles {
		identity := noteIdentity(handle)
		if identity.Slug == "" || identity.Task {
			continue
		}
		if sandboxName != "" && handle.Name() != sandboxName {
			continue
		}
		projects = append(projects, handle)
	}
	if sandboxName != "" && len(projects) == 0 {
		return nil, fmt.Errorf("no project sandbox named %q", sandboxName)
	}
	return projects, nil
}

func reportCompactFailure(ui termio.UI, name string, err error) {
	switch {
	case msb.IsReplaced(err) || msb.IsNotFound(err):
		ui.Verbosef("sandbox %s was already replaced or removed; skipping", name)
	case msb.IsUnsupportedOperation(err):
		ui.Verbosef("sandbox %s does not support disk compaction (non-local backend); skipping", name)
	default:
		ui.Warnf("failed to compact sandbox %s: %v", name, err)
	}
}

// RenderCompactReport prints the totals and, in verbose mode, one line per VM
// and per disk. Materialized bytes are the data rewritten by the merge, not
// space reclaimed.
func RenderCompactReport(ui termio.UI, report CompactReport) {
	prefix := "Compacted"
	if report.DryRun {
		prefix = "dry-run: Would compact"
	}
	var inputLayers, outputLayers uint32
	var materializedBytes uint64
	for _, entry := range report.Entries {
		inputLayers += entry.Result.InputLayers
		outputLayers += entry.Result.OutputLayers
		materializedBytes += entry.Result.MaterializedBytes
	}
	ui.Outf("%s %d sandbox(es): %s", prefix, len(report.Entries),
		layerSummary(inputLayers, outputLayers, materializedBytes))
	for _, entry := range report.Entries {
		line := fmt.Sprintf("  %s (%s): %s", entry.Name, entry.Slug,
			layerSummary(entry.Result.InputLayers, entry.Result.OutputLayers, entry.Result.MaterializedBytes))
		if entry.Result.PauseUs > 0 {
			line += fmt.Sprintf(", paused %gms", float64(entry.Result.PauseUs)/microsecondsPerMillisecond)
		}
		ui.Verbosef("%s", line)
		for _, disk := range entry.Result.Disks {
			ui.Verbosef("    %s: %s", disk.GuestPath,
				layerSummary(disk.InputLayers, disk.OutputLayers, disk.MaterializedBytes))
		}
	}
}

func layerSummary(inputLayers, outputLayers uint32, materializedBytes uint64) string {
	return fmt.Sprintf("%d -> %d layers, %s materialized",
		inputLayers, outputLayers, humanize.FormatBytes(materializedBytes))
}
