package pruning

import (
	"context"
	"time"

	"github.com/inoio/agents-sandbox/internal/humanize"
	"github.com/inoio/agents-sandbox/internal/termio"
)

func pruneReportPrefix(dryRun bool) string {
	prefix := "Pruned"
	if dryRun {
		prefix = "dry-run: Would prune"
	}
	return prefix
}

// formatOptionalBytes renders a byte count the runtime reported, or "-" when it
// did not (nil), never as zero.
func formatOptionalBytes(bytes *uint64) string {
	if bytes == nil {
		return "-"
	}
	return humanize.FormatBytes(*bytes)
}

func InvokePruneFunc[R any](
	context context.Context,
	pruneLambda func(context.Context,
		PruneState,
		bool,
		termio.UI,
	) (R, error),
	age time.Duration,
	dryRun bool,
	ui termio.UI,
) (R, error) {
	pruneState, err := buildPruneState(context, age)
	if err != nil {
		var zero R
		return zero, err
	}
	return pruneLambda(context, pruneState, dryRun, ui)
}
