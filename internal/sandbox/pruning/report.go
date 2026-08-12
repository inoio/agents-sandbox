package pruning

import (
	"time"
)

// StaleReport describes the result of a prune operation.
type StaleReport struct {
	PrunedVMs           int
	PrunedVolumes       int
	PrunedDockerImages  int
	PrunedMSBImages     int
	PrunedTaskSandboxes int
	PrunedCloneVolumes  int
	Details             []StaleEntry
}

// StaleEntry describes a single artifact that was pruned or would be pruned.
type StaleEntry struct {
	Type     StaleType
	Name     string
	StaleFor time.Duration
	Slug     string // project slug, for grouping related artifacts
	Digest   string // for images/volumes: the identifying digest tag; empty for :latest or VMs
}

// PruningCatalog holds all collected artifact data for a single prune run.
// buildCatalog creates it by inspecting sandboxes, volumes, and images.
//
//nolint:revive // Type name PruningCatalog is the pre-existing name from the sandbox core.
type PruningCatalog struct {
	StaleVMs       []StaleEntry
	TaskSandboxes  []StaleEntry
	ActiveVMDigest map[string]string // slug -> digest of the running VM
	HomeVolumes    map[string][]string
	CloneVolumes   []string
	MSBImages      map[string][]imageWithDigest
}

// imageWithDigest holds a reference and its digest for MSB images.
type imageWithDigest struct {
	ref      string
	digest   string // empty string for :latest
	isLatest bool
}
