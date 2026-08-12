package sandbox

import (
	"context"
	"time"

	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox/pruning"
	"gitlab.inoio.de/inoio/opencode-msb/internal/termio"
)

// Re-exported pruning module symbols preserve the public API of the sandbox
// core so that cmd/opencode-msb continues to compile without changing its
// import paths.

// Prune re-exports the pruning module's Prune.
func Prune(ctx context.Context, threshold time.Duration, dryRun bool, autoPrune bool, ui termio.UI) error {
	return pruning.Prune(ctx, threshold, dryRun, autoPrune, ui)
}

// AutoPrune re-exports the pruning module's AutoPrune.
func AutoPrune(ctx context.Context, threshold time.Duration, dryRun bool, ui termio.UI) {
	pruning.AutoPrune(ctx, threshold, dryRun, ui)
}

// StaleReport is re-exported from the pruning module.
type StaleReport = pruning.StaleReport

// StaleEntry is re-exported from the pruning module.
type StaleEntry = pruning.StaleEntry

// PruningCatalog is re-exported from the pruning module.
type PruningCatalog = pruning.PruningCatalog
