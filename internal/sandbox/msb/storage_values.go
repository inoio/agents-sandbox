package msb

// StorageReport is the launcher-owned projection of the runtime's storage
// observation. Optional counters stay nil when the runtime cannot measure
// them, so unknown never reads as zero.
type StorageReport struct {
	Images         StorageCategory
	Snapshots      StorageCategory
	Sandboxes      StorageCategory
	Volumes        StorageCategory
	BranchMemory   StorageCategory
	SnapshotMemory StorageCategory
	Notes          []string
}

// StorageCategory observes one storage category's totals and, when reported,
// its detailed items.
type StorageCategory struct {
	Count                   *uint64
	InUse                   *uint64
	LogicalBytes            *uint64
	AllocatedBytes          *uint64
	ReclaimableLogicalBytes *uint64
	Items                   []StorageItem
	Notes                   []string
}

// StorageItem describes one object or shared-cache component in a category.
type StorageItem struct {
	Name           string
	Path           string
	LogicalBytes   *uint64
	AllocatedBytes *uint64
	InUse          *bool
	Reclaimable    *bool
	Reasons        []string
}

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
