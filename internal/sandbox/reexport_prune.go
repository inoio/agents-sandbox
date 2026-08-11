package sandbox

// Re-exported pruning module symbols preserve the public API of the sandbox
// core so that cmd/opencode-msb and any sandbox-internal consumers continue
// to work without changing their import paths.

import "gitlab.inoio.de/inoio/opencode-msb/internal/sandbox/pruning"

//nolint:gochecknoglobals // Re-exports preserve the sandbox core public API.
var (
	// Prune is re-exported from the pruning module.
	Prune = pruning.Prune
	// AutoPrune is re-exported from the pruning module.
	AutoPrune = pruning.AutoPrune
)

// StaleReport is re-exported from the pruning module.
type StaleReport = pruning.StaleReport

// StaleEntry is re-exported from the pruning module.
type StaleEntry = pruning.StaleEntry

// PruningCatalog is re-exported from the pruning module.
type PruningCatalog = pruning.PruningCatalog
