package pruning

import "time"

// StaleReport describes the result of an aggregate prune. Task sandboxes count
// into PrunedVMs; clone volumes are removed (dead code).
type StaleReport struct {
	PrunedVMs          int
	PrunedVolumes      int
	PrunedDockerImages int
	PrunedMSBImages    int
	Details            []StaleEntry
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

var typeName = map[StaleType]string{ //nolint:gochecknoglobals // fmt.stringer pattern
	StaleTypeVM:          "vm",
	StaleTypeVolume:      "volume",
	StaleTypeDockerImage: "docker-image",
	StaleTypeMsbImage:    "msb-image",
}

func (ss StaleType) String() string { return typeName[ss] }
