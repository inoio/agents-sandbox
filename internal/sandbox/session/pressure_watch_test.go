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
	"github.com/inoio/agents-sandbox/internal/sandbox/pressure"
)

// countingMetricsSandbox wraps MockSandbox to count Metrics samples and signal
// the first one, so a test can wait until sampling has started before stopping
// the watcher.
type countingMetricsSandbox struct {
	*msb.MockSandbox

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
	*msb.MockSandbox

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
		MockSandbox: &msb.MockSandbox{
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
		MockSandbox: &msb.MockSandbox{
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
		MockSandbox: &msb.MockSandbox{MetricsErr: errors.New("vm stopped")},
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
		MockSandbox: &msb.MockSandbox{
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
	time.Sleep(20 * time.Millisecond)
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
				MockSandbox: &msb.MockSandbox{MetricsResult: metrics},
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
