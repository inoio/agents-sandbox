package session

import (
	"context"
	"time"

	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/pressure"
)

// pressureWatchInterval is a test seam for the sampling period.
var pressureWatchInterval = 2 * time.Second

// pressureSummary is the worst snapshot observed during a session: the minimum
// memory headroom fraction seen (1.0 when memory was never the tightest
// resource), whether any sample tripped the threshold, and the last failed
// metrics read, reported after the attach because the agent's TUI owns the
// terminal while the watcher runs.
type pressureSummary struct {
	MinHeadroomFraction float64
	Tripped             bool
	ReadErr             error
}

// startPressureWatcher samples metrics until stop is called and returns the
// summary. It never blocks the caller and never writes to the terminal, a
// failing Metrics call is kept in the summary, and a nil sandbox (dry-run)
// returns a no-op that yields a zero summary.
func startPressureWatcher(
	ctx context.Context,
	sb msb.Sandbox,
	t pressure.Threshold,
) func() pressureSummary {
	if sb == nil {
		return func() pressureSummary { return pressureSummary{} }
	}
	interval := pressureWatchInterval
	watchCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	summary := pressureSummary{MinHeadroomFraction: 1.0, Tripped: false, ReadErr: nil}
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-watchCtx.Done():
				return
			case <-ticker.C:
				samplePressure(watchCtx, sb, t, &summary)
			}
		}
	}()
	return func() pressureSummary {
		cancel()
		<-done
		return summary
	}
}

// samplePressure reads one metrics snapshot and folds it into summary: the
// minimum headroom fraction and whether the threshold tripped. A failed read is
// kept as summary.ReadErr; an unusable one leaves summary unchanged.
func samplePressure(
	ctx context.Context,
	sb msb.Sandbox,
	t pressure.Threshold,
	summary *pressureSummary,
) {
	m, err := sb.Metrics(ctx)
	if err != nil {
		summary.ReadErr = err
		return
	}
	if m == nil || m.MemoryLimitBytes == 0 {
		return
	}
	// Convert before subtracting: MemoryBytes can exceed the limit by a
	// transient accounting sliver, and uint64 subtraction would wrap.
	headroom := (float64(m.MemoryLimitBytes) - float64(m.MemoryBytes)) / float64(m.MemoryLimitBytes)
	// gomutants:disable-next-line CONDITIONALS_BOUNDARY reason="assigning an equal headroom fraction is a no-op, so < and <= are indistinguishable"
	if headroom < summary.MinHeadroomFraction {
		summary.MinHeadroomFraction = headroom
	}
	if _, tripped := pressure.FromMetrics(m, t); tripped {
		summary.Tripped = true
	}
}
