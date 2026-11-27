package session

import (
	"context"
	"errors"
	"math"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb/msbtest"
	"github.com/inoio/agents-sandbox/internal/sandbox/pressure"
)

// countingMetricsSandbox wraps MockSandbox to count Metrics samples and signal
// the first one, so a test can wait until sampling has started before stopping
// the watcher.
type countingMetricsSandbox struct {
	*msbtest.MockSandbox

	mu          sync.Mutex
	calls       int
	firstSample chan struct{}
}

func (c *countingMetricsSandbox) Metrics(ctx context.Context) (*msb.Metrics, error) {
	c.mu.Lock()
	c.calls++
	first := c.calls == 1
	c.mu.Unlock()
	if first {
		close(c.firstSample)
	}
	return c.MockSandbox.Metrics(ctx)
}

func (c *countingMetricsSandbox) callCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

// stubPressureWatchInterval pins the sampling period to 1ms so a test can
// outwait the ticker, restoring the production value afterwards.
func stubPressureWatchInterval(t *testing.T) {
	t.Helper()
	original := pressureWatchInterval
	pressureWatchInterval = time.Millisecond
	t.Cleanup(func() { pressureWatchInterval = original })
}

// TestPressureWatchIntervalDefault pins the sampling period, a wall-clock
// constant the tests otherwise shrink through the seam.
func TestPressureWatchIntervalDefault(t *testing.T) {
	if pressureWatchInterval != 2*time.Second {
		t.Errorf("pressureWatchInterval = %v, want 2s", pressureWatchInterval)
	}
}

// gatedMetricsSandbox blocks the first Metrics call until released, so a test
// can observe that the watcher's stop waits for an in-flight sample to fold into
// the summary.
type gatedMetricsSandbox struct {
	*msbtest.MockSandbox

	entered   chan struct{}
	release   chan struct{}
	enterOnce sync.Once
}

func (g *gatedMetricsSandbox) Metrics(ctx context.Context) (*msb.Metrics, error) {
	g.enterOnce.Do(func() { close(g.entered) })
	<-g.release
	return g.MockSandbox.Metrics(ctx)
}

// TestPressureWatcherStopWaitsForInFlightSample pins that stop() drains the
// sampling goroutine: an in-flight sample's trip must reach the returned
// summary rather than being dropped by an early return.
func TestPressureWatcherStopWaitsForInFlightSample(t *testing.T) {
	stubPressureWatchInterval(t)
	sb := &gatedMetricsSandbox{
		MockSandbox: &msbtest.MockSandbox{
			MetricsResult: &msb.Metrics{MemoryBytes: 950 << 20, MemoryLimitBytes: 1000 << 20},
		},
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}

	stop := startPressureWatcher(context.Background(), sb, pressure.DefaultThreshold())
	<-sb.entered

	stopped := make(chan pressureSummary, 1)
	go func() { stopped <- stop() }()
	select {
	case <-stopped:
		t.Fatal("stop returned before the in-flight sample was folded into the summary")
	case <-time.After(50 * time.Millisecond):
	}
	close(sb.release)
	select {
	case summary := <-stopped:
		if !summary.Tripped {
			t.Fatalf("summary = %+v, want the in-flight tripped sample folded in", summary)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("stop did not return after the sample completed")
	}
}

func TestPressureWatcherTripsAndStoresHeadroom(t *testing.T) {
	stubPressureWatchInterval(t)
	sb := &countingMetricsSandbox{
		MockSandbox: &msbtest.MockSandbox{
			MetricsResult: &msb.Metrics{MemoryBytes: 950 << 20, MemoryLimitBytes: 1000 << 20},
		},
		firstSample: make(chan struct{}),
	}

	stop := startPressureWatcher(context.Background(), sb, pressure.DefaultThreshold())
	<-sb.firstSample
	summary := stop()

	if !summary.Tripped {
		t.Fatalf("summary = %+v, want Tripped", summary)
	}
	if math.Abs(summary.MinHeadroomFraction-0.05) > 1e-9 {
		t.Fatalf("MinHeadroomFraction = %v, want 0.05", summary.MinHeadroomFraction)
	}
}

func TestPressureWatcherToleratesMetricsError(t *testing.T) {
	stubPressureWatchInterval(t)
	sb := &countingMetricsSandbox{
		MockSandbox: &msbtest.MockSandbox{MetricsErr: errors.New("vm stopped")},
		firstSample: make(chan struct{}),
	}

	stop := startPressureWatcher(context.Background(), sb, pressure.DefaultThreshold())
	<-sb.firstSample
	summary := stop()

	if summary.Tripped {
		t.Fatalf("summary = %+v, want un-tripped", summary)
	}
	if summary.MinHeadroomFraction != 1.0 {
		t.Fatalf("MinHeadroomFraction = %v, want 1.0 (no successful sample)", summary.MinHeadroomFraction)
	}
	if summary.ReadErr == nil || !strings.Contains(summary.ReadErr.Error(), "vm stopped") {
		t.Errorf("ReadErr = %v, want the failed metrics read kept for after the attach", summary.ReadErr)
	}
}

func TestPressureWatcherStopIsPromptAndStopsSampling(t *testing.T) {
	stubPressureWatchInterval(t)
	sb := &countingMetricsSandbox{
		MockSandbox: &msbtest.MockSandbox{
			MetricsResult: &msb.Metrics{MemoryBytes: 1 << 20, MemoryLimitBytes: 1000 << 20},
		},
		firstSample: make(chan struct{}),
	}

	stop := startPressureWatcher(context.Background(), sb, pressure.DefaultThreshold())
	<-sb.firstSample

	stopped := make(chan struct{})
	go func() {
		stop()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("stop did not return promptly")
	}

	after := sb.callCount()
	time.Sleep(20 * time.Millisecond) //testenv:allow a negative assertion: sampling must stay stopped after stop()
	if got := sb.callCount(); got != after {
		t.Fatalf("sampling continued after stop: %d -> %d calls", after, got)
	}
}

func TestPressureWatcherIgnoresUnusableSnapshot(t *testing.T) {
	stubPressureWatchInterval(t)
	cases := map[string]*msb.Metrics{
		"nil metrics": nil,
		"zero limit":  {MemoryBytes: 1, MemoryLimitBytes: 0},
	}
	for name, metrics := range cases {
		t.Run(name, func(t *testing.T) {
			sb := &countingMetricsSandbox{
				MockSandbox: &msbtest.MockSandbox{MetricsResult: metrics},
				firstSample: make(chan struct{}),
			}
			stop := startPressureWatcher(context.Background(), sb, pressure.DefaultThreshold())
			<-sb.firstSample
			summary := stop()
			if summary.Tripped || summary.MinHeadroomFraction != 1.0 {
				t.Fatalf("summary = %+v, want untripped 1.0", summary)
			}
		})
	}
}

func TestPressureWatcherNilSandboxYieldsZeroSummary(t *testing.T) {
	stop := startPressureWatcher(context.Background(), nil, pressure.DefaultThreshold())
	if summary := stop(); summary != (pressureSummary{}) {
		t.Fatalf("summary = %+v, want zero", summary)
	}
}

// sampleThenCancelMetricsStream delivers one sample and then blocks until its
// context is done, returning its configured cancellation error. It mirrors a
// Recv that folds a sample and then observes the teardown cancellation, so it
// reaches the mid-stream cancellation branch (samples > 0) rather than the
// no-sample fallback.
type sampleThenCancelMetricsStream struct {
	closed    chan struct{}
	cancelErr error
	once      sync.Once
	delivered bool
}

func (s *sampleThenCancelMetricsStream) Recv(ctx context.Context) (*msb.Metrics, error) {
	if !s.delivered {
		s.delivered = true
		return &msb.Metrics{MemoryBytes: 950 << 20, MemoryLimitBytes: 1000 << 20}, nil
	}
	<-ctx.Done()
	return nil, s.cancelErr
}

func (s *sampleThenCancelMetricsStream) Close() error {
	s.once.Do(func() { close(s.closed) })
	return nil
}

// blockingMetricsStream blocks every Recv on release, ignoring its context, so
// a test can observe the stop func's bounded wait.
type blockingMetricsStream struct {
	release  chan struct{}
	closedCh chan struct{}
	once     sync.Once
}

func (s *blockingMetricsStream) Recv(context.Context) (*msb.Metrics, error) {
	<-s.release
	return nil, nil //nolint:nilnil // (nil, nil) is the MetricsStream contract's end-of-stream signal
}

func (s *blockingMetricsStream) Close() error {
	s.once.Do(func() { close(s.closedCh) })
	return nil
}

// stubMetricsStreamStopTimeout shrinks the teardown bound so a test does not
// wait the production two seconds.
func stubMetricsStreamStopTimeout(t *testing.T) {
	t.Helper()
	original := metricsStreamStopTimeout
	metricsStreamStopTimeout = 20 * time.Millisecond
	t.Cleanup(func() { metricsStreamStopTimeout = original })
}

// TestMetricsStreamStopTimeoutDefault pins the teardown bound, a wall-clock
// constant the tests otherwise shrink through the seam.
func TestMetricsStreamStopTimeoutDefault(t *testing.T) {
	if metricsStreamStopTimeout != 2*time.Second {
		t.Errorf("metricsStreamStopTimeout = %v, want 2s", metricsStreamStopTimeout)
	}
}

// TestPressureWatcherStreamFoldsSamples pins that the watcher prefers the stream
// (never polling), folds a streamed sample into the summary, and closes the
// handle on the normal path.
func TestPressureWatcherStreamFoldsSamples(t *testing.T) {
	stubPressureWatchInterval(t)
	stream := msbtest.NewScriptedMetricsStream(
		msbtest.MetricsStreamSample{Metrics: &msb.Metrics{MemoryBytes: 950 << 20, MemoryLimitBytes: 1000 << 20}},
	)
	sb := &countingMetricsSandbox{
		MockSandbox: &msbtest.MockSandbox{MetricsStreamResult: stream},
		firstSample: make(chan struct{}),
	}

	summary := startPressureWatcher(context.Background(), sb, pressure.DefaultThreshold())()

	if !summary.Tripped {
		t.Fatalf("summary = %+v, want Tripped from the streamed sample", summary)
	}
	if math.Abs(summary.MinHeadroomFraction-0.05) > 1e-9 {
		t.Fatalf("MinHeadroomFraction = %v, want 0.05", summary.MinHeadroomFraction)
	}
	if got := sb.callCount(); got != 0 {
		t.Errorf("Metrics called %d times, want 0 (the stream path must not poll)", got)
	}
	if !stream.DidClose() {
		t.Error("the stream handle was not closed on the normal path")
	}
}

// TestPressureWatcherFallsBackToPollingOnOpenError pins that a stream that
// cannot start still observes through the polling fallback.
func TestPressureWatcherFallsBackToPollingOnOpenError(t *testing.T) {
	stubPressureWatchInterval(t)
	sb := &countingMetricsSandbox{
		MockSandbox: &msbtest.MockSandbox{
			MetricsResult:    &msb.Metrics{MemoryBytes: 950 << 20, MemoryLimitBytes: 1000 << 20},
			MetricsStreamErr: errors.New("no metrics slot"),
		},
		firstSample: make(chan struct{}),
	}

	stop := startPressureWatcher(context.Background(), sb, pressure.DefaultThreshold())
	<-sb.firstSample
	if summary := stop(); !summary.Tripped {
		t.Fatalf("summary = %+v, want the polled tripped sample", summary)
	}
}

// TestPressureWatcherFallsBackToPollingOnFirstRecvError pins that the no-slot
// error, which surfaces on Recv rather than open, also falls back to polling
// exactly once (one open attempt, then the ticker).
func TestPressureWatcherFallsBackToPollingOnFirstRecvError(t *testing.T) {
	stubPressureWatchInterval(t)
	stream := msbtest.NewScriptedMetricsStream(
		msbtest.MetricsStreamSample{Err: errors.New("no live metrics slot")},
	)
	sb := &countingMetricsSandbox{
		MockSandbox: &msbtest.MockSandbox{
			MetricsResult:       &msb.Metrics{MemoryBytes: 950 << 20, MemoryLimitBytes: 1000 << 20},
			MetricsStreamResult: stream,
		},
		firstSample: make(chan struct{}),
	}

	stop := startPressureWatcher(context.Background(), sb, pressure.DefaultThreshold())
	<-sb.firstSample
	if summary := stop(); !summary.Tripped {
		t.Fatalf("summary = %+v, want the polled tripped sample after the fallback", summary)
	}
	if !stream.DidClose() {
		t.Error("the failed stream handle was not closed before the fallback")
	}
	if got := len(sb.MetricsStreamCalls); got != 1 {
		t.Errorf("MetricsStream opened %d times, want 1 (the fallback must not ping-pong)", got)
	}
}

// TestPressureWatcherFallsBackToPollingOnNilStreamHandle pins the Observer
// double's nil-callback convention: a nil handle with a nil error is "no
// stream", so the watcher falls back instead of dereferencing it.
func TestPressureWatcherFallsBackToPollingOnNilStreamHandle(t *testing.T) {
	stubPressureWatchInterval(t)
	firstSample := make(chan struct{})
	var once sync.Once
	sb := &msbtest.MockSandboxObserver{
		MetricsFn: func(context.Context) (*msb.Metrics, error) {
			once.Do(func() { close(firstSample) })
			return &msb.Metrics{MemoryBytes: 950 << 20, MemoryLimitBytes: 1000 << 20}, nil
		},
	}

	stop := startPressureWatcher(context.Background(), sb, pressure.DefaultThreshold())
	<-firstSample
	if summary := stop(); !summary.Tripped {
		t.Fatalf("summary = %+v, want the polled tripped sample after a nil stream handle", summary)
	}
}

// TestPressureWatcherMidStreamErrorEndsWithoutFallback pins that a Recv error
// after a sample is kept and ends the loop, with no polling against a dead VM.
// The error is a plain error, not a context error, so it is not mistaken for
// the teardown cancellation even though the immediately-invoked stop func
// cancels concurrently: the assertion is deterministic.
func TestPressureWatcherMidStreamErrorEndsWithoutFallback(t *testing.T) {
	stubPressureWatchInterval(t)
	stream := msbtest.NewScriptedMetricsStream(
		msbtest.MetricsStreamSample{Metrics: &msb.Metrics{MemoryBytes: 1 << 20, MemoryLimitBytes: 1000 << 20}},
		msbtest.MetricsStreamSample{Err: errors.New("vm stopped")},
	)
	sb := &countingMetricsSandbox{
		MockSandbox: &msbtest.MockSandbox{MetricsStreamResult: stream},
		firstSample: make(chan struct{}),
	}

	summary := startPressureWatcher(context.Background(), sb, pressure.DefaultThreshold())()

	if summary.ReadErr == nil || !strings.Contains(summary.ReadErr.Error(), "vm stopped") {
		t.Errorf("ReadErr = %v, want the mid-stream error kept", summary.ReadErr)
	}
	if got := sb.callCount(); got != 0 {
		t.Errorf("Metrics called %d times, want 0 (no fallback after a sample)", got)
	}
}

// TestPressureWatcherCancelDoesNotRecordReadErr pins that a normal stop after a
// folded sample is not reported as a failed read: Recv returning the context
// error on teardown must leave the summary's ReadErr nil and keep the sample.
// Reaching the mid-stream branch (samples > 0) is what exercises isCancellation's
// discrimination between a clean teardown and a real failure.
func TestPressureWatcherCancelDoesNotRecordReadErr(t *testing.T) {
	for _, cancelErr := range []error{context.Canceled, context.DeadlineExceeded} {
		t.Run(cancelErr.Error(), func(t *testing.T) {
			stubPressureWatchInterval(t)
			stream := &sampleThenCancelMetricsStream{closed: make(chan struct{}), cancelErr: cancelErr}
			sb := &countingMetricsSandbox{
				MockSandbox: &msbtest.MockSandbox{MetricsStreamResult: stream},
				firstSample: make(chan struct{}),
			}

			summary := startPressureWatcher(context.Background(), sb, pressure.DefaultThreshold())()

			if summary.ReadErr != nil {
				t.Errorf("ReadErr = %v, want nil on a normal stop", summary.ReadErr)
			}
			if math.Abs(summary.MinHeadroomFraction-0.05) > 1e-9 {
				t.Errorf(
					"MinHeadroomFraction = %v, want 0.05 (the streamed sample was folded)",
					summary.MinHeadroomFraction,
				)
			}
			select {
			case <-stream.closed:
			default:
				t.Error("the stream handle was not closed on the cancellation branch")
			}
			if got := sb.callCount(); got != 0 {
				t.Errorf("Metrics called %d times, want 0 (cancellation must not fall back to polling)", got)
			}
		})
	}
}

// TestPressureWatcherTeardownIsBoundedWhenRecvBlocks pins that stop() returns
// within the bound even when Recv ignores cancellation, leaving the goroutine
// to finish (and close the handle) when Recv eventually returns.
func TestPressureWatcherTeardownIsBoundedWhenRecvBlocks(t *testing.T) {
	stubPressureWatchInterval(t)
	stubMetricsStreamStopTimeout(t)
	stream := &blockingMetricsStream{release: make(chan struct{}), closedCh: make(chan struct{})}
	sb := &msbtest.MockSandbox{MetricsStreamResult: stream}

	stop := startPressureWatcher(context.Background(), sb, pressure.DefaultThreshold())
	start := time.Now()
	stop()
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("stop() took %v, want within the bound", elapsed)
	}

	close(stream.release)
	select {
	case <-stream.closedCh:
	case <-time.After(time.Second):
		t.Fatal("the handle was not closed after Recv returned")
	}
}

// TestPressureWatcherClosesHandleOnOpenErrorWithHandle pins the fallback's
// handle guard: a MetricsStream that returns a handle alongside an open error
// must have that handle closed before polling, so the fallback does not leak
// it. The seam's real and mock implementations return a nil handle on error,
// so this is the only path that reaches the guard.
func TestPressureWatcherClosesHandleOnOpenErrorWithHandle(t *testing.T) {
	stubPressureWatchInterval(t)
	stream := msbtest.NewScriptedMetricsStream()
	sb := &countingMetricsSandbox{
		MockSandbox: &msbtest.MockSandbox{
			MetricsResult: &msb.Metrics{MemoryBytes: 950 << 20, MemoryLimitBytes: 1000 << 20},
			MetricsStreamFn: func(context.Context, time.Duration) (msb.MetricsStream, error) {
				return stream, errors.New("open failed after allocating a handle")
			},
		},
		firstSample: make(chan struct{}),
	}

	stop := startPressureWatcher(context.Background(), sb, pressure.DefaultThreshold())
	select {
	case <-sb.firstSample:
	case <-time.After(time.Second):
		t.Fatal("the watcher did not fall back to polling after the guarded open error")
	}
	if summary := stop(); !summary.Tripped {
		t.Fatalf("summary = %+v, want the polled tripped sample after the guarded fallback", summary)
	}
	if !stream.DidClose() {
		t.Error("the handle returned alongside the open error was not closed")
	}
	if got := len(sb.MetricsStreamCalls); got != 1 {
		t.Errorf("MetricsStream opened %d times, want 1", got)
	}
}

// TestPressureWatcherFallsBackToPollingOnCleanEndWithoutSample pins that a
// stream that ends cleanly (nil, nil) before delivering a sample — the
// runtime's exited-sandbox or absent-slot signal — closes the handle and falls
// back to polling exactly once, rather than ending silently with no observation.
func TestPressureWatcherFallsBackToPollingOnCleanEndWithoutSample(t *testing.T) {
	stubPressureWatchInterval(t)
	stream := msbtest.NewScriptedMetricsStream()
	sb := &countingMetricsSandbox{
		MockSandbox: &msbtest.MockSandbox{
			MetricsResult:       &msb.Metrics{MemoryBytes: 950 << 20, MemoryLimitBytes: 1000 << 20},
			MetricsStreamResult: stream,
		},
		firstSample: make(chan struct{}),
	}

	stop := startPressureWatcher(context.Background(), sb, pressure.DefaultThreshold())
	select {
	case <-sb.firstSample:
	case <-time.After(time.Second):
		t.Fatal("the watcher did not fall back to polling after the clean end with no sample")
	}
	if summary := stop(); !summary.Tripped {
		t.Fatalf("summary = %+v, want the polled tripped sample after a clean end with no sample", summary)
	}
	if !stream.DidClose() {
		t.Error("the cleanly-ended stream handle was not closed before the fallback")
	}
	if got := len(sb.MetricsStreamCalls); got != 1 {
		t.Errorf("MetricsStream opened %d times, want 1", got)
	}
}
