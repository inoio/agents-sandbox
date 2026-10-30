package pruning

import "time"

// StaleReport describes the result of an aggregate prune. Task sandboxes count
// into PrunedSandboxes; clone volumes are removed (dead code).
type StaleReport struct {
	PrunedSandboxes     int
	PrunedVolumes       int
	PrunedDockerImages  int
	PrunedSandboxImages int
	SkippedImagesInUse  uint32
	ImageBytesReclaimed *uint64
	Storage             StorageReport
	Details             []StaleEntry
}

// StorageReport summarizes a runtime-memory reclaim run. PhysicalBytesReclaimed
// is nil when the runtime cannot measure it, following the nil-not-zero
// convention; it is never rendered as zero.
type StorageReport struct {
	FilesRemoved           uint64
	LogicalBytesRemoved    uint64
	PhysicalBytesReclaimed *uint64
}

// StaleEntry describes a single artifact that was pruned or would be pruned.
type StaleEntry struct {
	Type     StaleType
	Name     string
	StaleFor time.Duration
	Slug     string
	Digest   string
}

// StaleType indicates the kind of artifact being pruned.
type StaleType int

const (
	StaleTypeVM StaleType = iota
	StaleTypeVolume
	StaleTypeDockerImage
	StaleTypeMsbImage
)

var typeName = map[StaleType]string{
	StaleTypeVM:          "vm",
	StaleTypeVolume:      "volume",
	StaleTypeDockerImage: "docker-image",
	StaleTypeMsbImage:    "msb-image",
}

func (ss StaleType) String() string { return typeName[ss] }
