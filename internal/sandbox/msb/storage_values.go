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

// PruneReport keeps the reclaimed totals. The SDK's DryRun echo, per-file
// Entries and Truncated listing cap are not surfaced.
type PruneReport struct {
	FilesRemoved           uint64
	LogicalBytesRemoved    uint64
	PhysicalBytesReclaimed *uint64
}
