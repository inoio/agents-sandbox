package sandbox

import (
	"context"
	"sync"
	"time"

	"gitlab.inoio.de/inoio/opencode-msb/internal/output"
)

var autoPruneOnce sync.Once

// AutoPrune runs the prune logic once per process with the given threshold.
// Threshold of 0 defaults to 7 days.
func AutoPrune(ctx context.Context, threshold time.Duration, logger *output.Printer) {
	if threshold == 0 {
		threshold = 7 * 24 * time.Hour
	}
	autoPruneOnce.Do(func() {
		_, _ = Prune(ctx, threshold, false, true, logger)
	})
}
