package msb

import msbSdk "github.com/superradcompany/microsandbox/sdk/go"

// StorageReport is the SDK's runtime storage-usage observation. The launcher
// surfaces it directly rather than mirroring its fields.
type StorageReport = msbSdk.StorageUsageReport

// StorageCategory is the SDK's per-category storage observation.
type StorageCategory = msbSdk.StorageCategoryUsage

// StorageItem is the SDK's one-object-in-a-storage-category observation.
type StorageItem = msbSdk.StorageItemUsage

// PruneRequest selects unused runtime backing; zero values prune without an
// age filter.
type PruneRequest struct {
	DryRun           bool
	OlderThanSeconds uint64
}

// PruneEntry is one runtime-memory backing's prune outcome. Error is set when the
// backing could not be pruned; State explains eligibility or retention.
type PruneEntry struct {
	Path           string
	Kind           string
	LogicalBytes   *uint64
	AllocatedBytes *uint64
	State          string
	Error          *string
}

// PruneReport keeps the reclaimed totals and the per-backing outcomes. The SDK's
// DryRun echo is not surfaced: the caller issued the prune and knows it.
type PruneReport struct {
	FilesRemoved           uint64
	LogicalBytesRemoved    uint64
	PhysicalBytesReclaimed *uint64
	Entries                []PruneEntry
	Truncated              bool
}
