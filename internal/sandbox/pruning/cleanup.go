package pruning

import (
	"context"
	"sync"
	"time"

	"github.com/inoio/opencode-sandbox/internal/termio"
)

//nolint:gochecknoglobals // once-per-process singleton is the right pattern
var autoPruneOnce sync.Once

// AutoPrune runs the prune logic once per process with the given threshold.
// Threshold of 0 defaults to 30 days.
func AutoPrune(ctx context.Context, threshold time.Duration, dryRun bool, ui termio.UI) {
	if threshold == 0 {
		threshold = 30 * 24 * time.Hour
	}
	autoPruneOnce.Do(func() {
		err := Prune(ctx, threshold, dryRun, ui)
		if err != nil {
			ui.Warnf("auto-prune failed: %s", err)
		}
	})
}
