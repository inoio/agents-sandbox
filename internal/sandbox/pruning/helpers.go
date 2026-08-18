package pruning

import (
	"context"
	"strings"
	"time"

	"gitlab.inoio.de/inoio/opencode-sandbox/internal/termio"
)

func hasPrefix(s, prefix string) bool { return strings.HasPrefix(s, prefix) }

func pruneReportPrefix(dryRun bool) string {
	prefix := "Pruned"
	if dryRun {
		prefix = "dry-run: Would prune"
	}
	return prefix
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
) error {
	pruneState, err := buildPruneState(context, age)
	if err != nil {
		return err
	}
	_, pruneErr := pruneLambda(context, pruneState, dryRun, ui)
	return pruneErr
}
