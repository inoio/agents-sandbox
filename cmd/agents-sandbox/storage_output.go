package main

import (
	"strconv"
	"strings"

	"github.com/inoio/agents-sandbox/internal/humanize"
	"github.com/inoio/agents-sandbox/internal/sandbox/storage"
	"github.com/inoio/agents-sandbox/internal/termio"
)

const (
	colType        = "TYPE"
	colCount       = "COUNT"
	colInUse       = "IN USE"
	colLogicalSize = "LOGICAL SIZE"
	colReclaimable = "RECLAIMABLE"
)

const storageLogicalFooter = "logical sizes; '-' means unknown. use --verbose for details."

// storageDisplayName turns a canonical category name (e.g. "branch_memory")
// into its human label (e.g. "branch memory").
func storageDisplayName(name string) string {
	return strings.ReplaceAll(name, "_", " ")
}

func storageCategoryHeaders() []string {
	return []string{colType, colCount, colInUse, colLogicalSize, colReclaimable}
}

// printStorageReport renders the report in the shape of microsandbox's `df`:
// a category totals table by default, or (with verbose) per-category item
// blocks preceded by the reclaimable/accounting notes.
func printStorageReport(ui termio.UI, report *storage.Report, verbose bool) {
	printStorageTable(ui, report)
	if !verbose {
		ui.Out(storageLogicalFooter)
		for _, note := range report.Notes {
			ui.Out(note)
		}
		return
	}
	printStorageVerbose(ui, report)
}

func printStorageTable(ui termio.UI, report *storage.Report) {
	table := ui.NewTable(storageCategoryHeaders()...)
	for _, category := range report.Categories {
		table.AddRow(
			storageDisplayName(category.Name),
			formatOptionalUint(category.Totals.Count),
			formatOptionalUint(category.Totals.InUse),
			formatOptionalBytes(category.Totals.LogicalBytes),
			formatOptionalBytes(category.Totals.ReclaimableBytes),
		)
	}
	table.Print()
}

func printStorageVerbose(ui termio.UI, report *storage.Report) {
	ui.Out("")
	ui.Out("Reclaimable shows logical bytes; '-' means unknown.")
	ui.Out("Physical space freed may differ because files can share disk blocks.")
	for _, note := range report.Notes {
		ui.Out(note)
	}
	for _, category := range report.Categories {
		ui.Out("")
		ui.Out(storageCategoryTitle(category))
		for _, item := range category.Objects {
			ui.Out("  " + item.Name)
			ui.Out("    Path:          " + item.Path)
			ui.Out("    Logical size:  " + formatOptionalBytes(item.LogicalBytes))
			ui.Out("    Allocated:     " + formatOptionalBytes(item.AllocatedBytes))
			ui.Out("    In use:        " + formatOptionalBool(item.InUse))
			ui.Out("    Reclaimable:   " + formatOptionalBool(item.Reclaimable))
			for _, reason := range item.Reasons {
				ui.Out("    Reason:        " + reason)
			}
		}
		for _, note := range category.Notes {
			ui.Out("  " + storageDisplayName(category.Name) + ": " + note)
		}
	}
}

// storageCategoryTitle renders a verbose category heading, e.g. "Branch memory (5)".
func storageCategoryTitle(category storage.Category) string {
	label := storageDisplayName(category.Name)
	title := strings.ToUpper(label[:1]) + label[1:]
	return title + " (" + formatOptionalUint(category.Totals.Count) + ")"
}

// formatOptionalUint renders an optional count as a decimal, or "-" when unknown.
func formatOptionalUint(value *uint64) string {
	return derefOr(value, func(v uint64) string { return strconv.FormatUint(v, 10) })
}

// formatOptionalBytes humanizes an optional byte count, or renders "-" when unknown.
func formatOptionalBytes(value *uint64) string {
	return derefOr(value, humanize.FormatBytes)
}

// formatOptionalBool renders an optional flag as Yes/No, or "-" when unknown.
func formatOptionalBool(value *bool) string {
	if value == nil {
		return "-"
	}
	if *value {
		return "Yes"
	}
	return "No"
}

// jsonStorageUsage mirrors microsandbox's storage report shape, adding the
// host-side runtime category. Unknown counters are null.
type jsonStorageUsage struct {
	Images         jsonStorageCategory `json:"images"`
	Snapshots      jsonStorageCategory `json:"snapshots"`
	BranchMemory   jsonStorageCategory `json:"branch_memory"`
	SnapshotMemory jsonStorageCategory `json:"snapshot_memory"`
	Sandboxes      jsonStorageCategory `json:"sandboxes"`
	Volumes        jsonStorageCategory `json:"volumes"`
	Runtime        jsonStorageCategory `json:"runtime"`
	Notes          []string            `json:"notes"`
}

//exhaustruct:ignore
type jsonStorageCategory struct {
	Count                   *uint64           `json:"count"`
	InUse                   *uint64           `json:"in_use"`
	LogicalBytes            *uint64           `json:"logical_bytes"`
	AllocatedBytes          *uint64           `json:"allocated_bytes"`
	ReclaimableLogicalBytes *uint64           `json:"reclaimable_logical_bytes"`
	Items                   []jsonStorageItem `json:"items"`
	Notes                   []string          `json:"notes"`
}

type jsonStorageItem struct {
	Name           string   `json:"name"`
	Path           string   `json:"path"`
	LogicalBytes   *uint64  `json:"logical_bytes"`
	AllocatedBytes *uint64  `json:"allocated_bytes"`
	InUse          *bool    `json:"in_use"`
	Reclaimable    *bool    `json:"reclaimable"`
	Reasons        []string `json:"reasons"`
}

// newStorageReportJSON maps a storage report onto the microsandbox-shaped
// structured output.
func newStorageReportJSON(report *storage.Report) jsonStorageUsage {
	byName := make(map[string]jsonStorageCategory, len(report.Categories))
	for _, category := range report.Categories {
		byName[category.Name] = jsonStorageCategory{
			Count:                   category.Totals.Count,
			InUse:                   category.Totals.InUse,
			LogicalBytes:            category.Totals.LogicalBytes,
			AllocatedBytes:          category.Totals.AllocatedBytes,
			ReclaimableLogicalBytes: category.Totals.ReclaimableBytes,
			Items:                   storageItemsJSON(category.Objects),
			Notes:                   nonNilStrings(category.Notes),
		}
	}
	return jsonStorageUsage{
		Images:         storageCategoryJSON(byName, storage.CategoryImages),
		Snapshots:      storageCategoryJSON(byName, storage.CategorySnapshots),
		BranchMemory:   storageCategoryJSON(byName, storage.CategoryBranchMemory),
		SnapshotMemory: storageCategoryJSON(byName, storage.CategorySnapshotMemory),
		Sandboxes:      storageCategoryJSON(byName, storage.CategorySandboxes),
		Volumes:        storageCategoryJSON(byName, storage.CategoryVolumes),
		Runtime:        storageCategoryJSON(byName, storage.CategoryRuntime),
		Notes:          nonNilStrings(report.Notes),
	}
}

// storageCategoryJSON returns a category or an empty one with non-nil
// collections, so `items`/`notes` serialize as [] even for a missing category.
func storageCategoryJSON(byName map[string]jsonStorageCategory, name string) jsonStorageCategory {
	if category, ok := byName[name]; ok {
		return category
	}
	return jsonStorageCategory{Items: []jsonStorageItem{}, Notes: []string{}}
}

// storageItemsJSON always returns a non-nil slice so items serializes as [].
func storageItemsJSON(objects []storage.Object) []jsonStorageItem {
	items := make([]jsonStorageItem, 0, len(objects))
	for _, object := range objects {
		items = append(items, jsonStorageItem{
			Name:           object.Name,
			Path:           object.Path,
			LogicalBytes:   object.LogicalBytes,
			AllocatedBytes: object.AllocatedBytes,
			InUse:          object.InUse,
			Reclaimable:    object.Reclaimable,
			Reasons:        nonNilStrings(object.Reasons),
		})
	}
	return items
}
