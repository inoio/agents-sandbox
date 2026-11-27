package session

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/pressure"
)

// pressureWatchInterval is a test seam for the sampling period.
var pressureWatchInterval = 2 * time.Second

// metricsStreamStopTimeout bounds how long the stop func waits for the watch
// goroutine. A stream handle that ignores cancellation leaves the goroutine to
// finish when Recv eventually returns; the attach must not block on it
// (specs/resources/resource-limit-proposal.md §1a).
var metricsStreamStopTimeout = 2 * time.Second

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

// pressureWatcher owns the summary the watch goroutine folds into. The mutex
// guards it because the bounded-teardown path lets the stop func read it while
// the goroutine may still be running.
type pressureWatcher struct {
	mu      sync.Mutex
	summary pressureSummary
}

// startPressureWatcher samples metrics until stop is called and returns the
// summary. It never blocks the caller and never writes to the terminal, a
// failing read is kept in the summary, and a nil sandbox (dry-run) returns a
// no-op that yields a zero summary. It prefers the stream and falls back to
// polling exactly once when the stream cannot deliver.
func startPressureWatcher(
	ctx context.Context,
	sb msb.SandboxObserver,
	t pressure.Threshold,
) func() pressureSummary {
	if sb == nil {
		return func() pressureSummary { return pressureSummary{} }
	}
	interval := pressureWatchInterval
	watchCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	watcher := &pressureWatcher{
		mu:      sync.Mutex{},
		summary: pressureSummary{MinHeadroomFraction: 1.0, Tripped: false, ReadErr: nil},
	}
	go func() {
		defer close(done)
		stream, err := sb.MetricsStream(watchCtx, interval)
		if err != nil || stream == nil {
			watcher.tick(watchCtx, sb, interval, t)
			return
		}
		watcher.stream(watchCtx, sb, stream, interval, t)
	}()
	return func() pressureSummary {
		cancel()
		select {
		case <-done:
		case <-time.After(metricsStreamStopTimeout):
		}
		watcher.mu.Lock()
		defer watcher.mu.Unlock()
		return watcher.summary
	}
}

// tick is the pre-stream sampling loop, kept as the fallback when the stream
// cannot deliver.
func (w *pressureWatcher) tick(
	ctx context.Context,
	sb msb.SandboxObserver,
	interval time.Duration,
	t pressure.Threshold,
) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.sample(ctx, sb, t)
		}
	}
}

// stream folds streamed samples until the stream ends, an error arrives, or ctx
// is cancelled. A Recv error before any sample means the stream never produced
// one (the runtime's no-slot error surfaces on Recv), so it closes the handle
// and falls back to polling exactly once. A Recv error after a sample means the
// VM stopped: the error is kept and the loop ends, because polling a dead VM
// would only fail. The goroutine owns Close, so Close never races an in-flight
// Recv.
func (w *pressureWatcher) stream(
	ctx context.Context,
	sb msb.SandboxObserver,
	stream msb.MetricsStream,
	interval time.Duration,
	t pressure.Threshold,
) {
	samples := 0
	for {
		m, err := stream.Recv(ctx)
		if err != nil {
			_ = stream.Close()
			if isCancellation(err) {
				return
			}
			if samples == 0 {
				w.tick(ctx, sb, interval, t)
				return
			}
			w.mu.Lock()
			w.summary.ReadErr = err
			w.mu.Unlock()
			return
		}
		if m == nil {
			_ = stream.Close()
			return
		}
		samples++
		w.fold(m, t)
	}
}

// isCancellation reports whether a Recv error is the watch context ending
// rather than a real read failure. It inspects the error, not ctx.Err(): stop()
// cancels the context concurrently with the final Recv, and the error type is
// the only reliable discriminator between a clean teardown and a real failure.
func isCancellation(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

// sample reads one metrics snapshot and folds it into the summary. A failed
// read is kept as the summary's ReadErr.
func (w *pressureWatcher) sample(ctx context.Context, sb msb.SandboxObserver, t pressure.Threshold) {
	m, err := sb.Metrics(ctx)
	if err != nil {
		w.mu.Lock()
		w.summary.ReadErr = err
		w.mu.Unlock()
		return
	}
	w.fold(m, t)
}

func (w *pressureWatcher) fold(m *msb.Metrics, t pressure.Threshold) {
	w.mu.Lock()
	defer w.mu.Unlock()
	foldSample(m, t, &w.summary)
}

// foldSample folds one snapshot into summary: the minimum headroom fraction and
// whether the threshold tripped. An absent snapshot or an unknown (zero) limit
// leaves summary unchanged, so unknown never reads as zero.
func foldSample(m *msb.Metrics, t pressure.Threshold, summary *pressureSummary) {
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
