---
description: TDD implementation plan for #918 - stream the in-session pressure watcher on a new SandboxObserver.MetricsStream seam with a first-Recv polling fallback and bounded teardown, and record AllSandboxMetrics and the msb-metrics OTLP sidecar as deferred.
read_if: Touching session/pressure_watch.go, startPressureWatcher, metricsStreamStopTimeout, or the msb.SandboxObserver/MetricsStream/newMetricsStream seam; a pressure watcher that misses a metrics spike, blocks the attach on teardown, records a spurious read error on stop, or leaks a stream handle; or asking why AllSandboxMetrics and the msb-metrics sidecar are not adopted.
---

# msb metrics stream Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the in-session pressure watcher's 2 s `Metrics` polling loop with a `SandboxObserver.MetricsStream` subscription that falls back to polling exactly once when the stream cannot deliver, tears down within a bounded wait without blocking the attach, and keeps the existing `pressureSummary` contract unchanged.

**Architecture:** Task 1 adds the launcher-owned `MetricsStream` handle interface and the `MetricsStream` method to both `SandboxObserver` and `SandboxResourceControl` in `internal/sandbox/msb`, wrapping the SDK's `*msbSdk.MetricsStreamHandle` behind a `newMetricsStream` seam (the SDK handle has no exported constructor, so a seam typed on the SDK type could not be stubbed). Task 2 rewrites `session/pressure_watch.go` to prefer the stream, extract the sample fold into `foldSample`, and bound the stop func's wait. Task 3 records the change in the resource spec, the seam spec, the SDK guide, and a changelog fragment. Task 4 adds a host-only real-VM test that proves a stream delivers samples and ends for a running sandbox.

**Tech Stack:** Go 1.27, module `github.com/inoio/agents-sandbox`, microsandbox SDK v0.8.3 (`Sandbox.MetricsStream` / `*msbSdk.MetricsStreamHandle`), golangci-lint.

**Spec:** `.operator-shared/plans/2026-11-27-msb-metrics-design.md`

## Global Constraints

- Base is `main` after #923 (PR #928) merges; the adapter's explicit `LocalContext` destination and the #924 observation aliases (`type Metrics = msbSdk.Metrics`) are present. The seam is referenced post-#923.
- `phase4_boundary_test.go` must stay green: no production (non-`_test.go`) package outside `internal/sandbox/msb` may import `msbSdk`.
- `roles_boundary_test.go` must stay green: `MetricsStream` is added to both the `SandboxObserver` and `SandboxResourceControl` roles, so each composite still equals the union of its roles (`SandboxHandle` gains it through `SandboxResourceControl`, `Sandbox` through `SandboxObserver`).
- `startPressureWatcher` keeps its signature, its `sb == nil` no-op, and its `pressureSummary` shape.
- The watcher must never write to the terminal: no `termio.UI`/logger is threaded into it (there is no package-level logger in `session`), and a failed read is reported by the caller after the attach.
- The interval stays the `pressureWatchInterval` seam (2 s by default) for both the stream and the ticker.
- A nullable/absent metrics field (`MemoryAvailableBytes`, `Upper*`) is never folded as zero; the `MemoryLimitBytes == 0` guard stays.
- Unit tests must stay FFI-free: swap the `newMetricsStream` package seam or use the `msbtest` doubles; never resolve the native library.
- A behavior change needs a `changelog.d/<PR-number>.changed.md` fragment (literal `<PR-number>`, as the sibling plan uses).
- Format with `make fmt` (never `go fmt`); lint with `make lint` (never `go vet`); run `make check` before finalizing.

## Review Focus

- **A metrics read that returns no snapshot, or a snapshot with an unknown (zero) limit, must leave the summary unchanged** rather than fold as zero and trip or clear the threshold. Any new fold path must reuse the existing guard. Pinned by the unchanged `TestPressureWatcherIgnoresUnusableSnapshot` and by `foldSample` carrying the `MemoryLimitBytes == 0` guard in Task 2.
- **A stream that cannot start (open error) or never yields a first sample must still observe via polling, exactly once** — never lose observation and never ping-pong. Pinned by `TestPressureWatcherFallsBackToPollingOnOpenError` and `TestPressureWatcherFallsBackToPollingOnFirstRecvError` in Task 2.
- **A `Recv` error after a sample (the VM stopped) must not trigger a second, failing mechanism** against a dead VM. Pinned by `TestPressureWatcherMidStreamErrorEndsWithoutFallback` in Task 2.
- **Teardown must not block the attach when the stream ignores cancellation.** Pinned by `TestPressureWatcherTeardownIsBoundedWhenRecvBlocks` in Task 2.
- **A normal stop must not record a spurious read error** (`Recv` returning `context.Canceled` on teardown). Pinned by `TestPressureWatcherCancelDoesNotRecordReadErr` in Task 2.

---

### Task 1: Add the `MetricsStream` seam and doubles (design §1)

**Files:**
- Modify: `internal/sandbox/msb/sandbox.go` (add `MetricsStream` to `SandboxObserver` **and** `SandboxResourceControl`; add the `MetricsStream` interface, `realMetricsStream`, the `newMetricsStream` seam, and `MetricsStream` on `realSandbox` and `realSandboxHandle`)
- Modify: `internal/sandbox/msb/msbtest/testmock.go` (the `MockSandbox` and `MockSandboxHandle` stream fields and methods; the scripted stream types)
- Modify: `internal/sandbox/msb/msbtest/testmock_roles.go` (the `MockSandboxObserver` double)
- Test: `internal/sandbox/msb/sandbox_test.go`

**Interfaces:**
- Produces (msb): `type MetricsStream interface { Recv(ctx context.Context) (*Metrics, error); Close() error }`; `MetricsStream(ctx context.Context, interval time.Duration) (MetricsStream, error)` on **both** `SandboxObserver` and `SandboxResourceControl`; `var newMetricsStream func(s *msbSdk.Sandbox, ctx context.Context, interval time.Duration) (MetricsStream, error)`; `type realMetricsStream struct { handle *msbSdk.MetricsStreamHandle }`; `realSandbox.MetricsStream` and `realSandboxHandle.MetricsStream`.
- Produces (msbtest): `MockSandbox` and `MockSandboxHandle` each gain `MetricsStreamFn func(ctx context.Context, interval time.Duration) (msb.MetricsStream, error)`, `MetricsStreamResult msb.MetricsStream`, `MetricsStreamErr error`, `MetricsStreamCalls []time.Duration`; `type MetricsStreamSample struct { Metrics *msb.Metrics; Err error }`; `type ScriptedMetricsStream struct`; `func NewScriptedMetricsStream(samples ...MetricsStreamSample) *ScriptedMetricsStream`; `(*ScriptedMetricsStream).Recv`/`Close`/`DidClose`; `MockSandboxObserver.MetricsStreamFn`.
- Consumes: nothing.

- [ ] **Step 1: Write the failing delegation test**

Append to `internal/sandbox/msb/sandbox_test.go`:

```go
// stubMetricsStream is a MetricsStream double for the adapter delegation test.
type stubMetricsStream struct{}

func (*stubMetricsStream) Recv(context.Context) (*Metrics, error) { return nil, nil }
func (*stubMetricsStream) Close() error                           { return nil }

// TestRealSandboxMetricsStreamDelegatesThroughSeam pins that the adapter routes
// the SDK sandbox and the interval through the newMetricsStream seam and returns
// the launcher handle it yields, so the watcher never touches the SDK handle.
func TestRealSandboxMetricsStreamDelegatesThroughSeam(t *testing.T) {
	wanted := &stubMetricsStream{}
	var gotInterval time.Duration
	swapSeam(t, &newMetricsStream, func(
		_ *msbSdk.Sandbox, _ context.Context, interval time.Duration,
	) (MetricsStream, error) {
		gotInterval = interval
		return wanted, nil
	})

	sandbox := realSandbox{sandbox: &msbSdk.Sandbox{}}
	stream, err := sandbox.MetricsStream(context.Background(), 3*time.Second)
	if err != nil {
		t.Fatalf("MetricsStream() error = %v", err)
	}
	if stream != MetricsStream(wanted) {
		t.Errorf("MetricsStream() = %v, want the seam's handle", stream)
	}
	if gotInterval != 3*time.Second {
		t.Errorf("interval = %v, want 3s", gotInterval)
	}
}

// TestRealSandboxHandleMetricsStreamDelegatesThroughSeam pins the same
// delegation for the persisted/unified handle: it shares the resource-control
// role, so a SandboxHandle still satisfies SandboxObserver.
func TestRealSandboxHandleMetricsStreamDelegatesThroughSeam(t *testing.T) {
	wanted := &stubMetricsStream{}
	var gotInterval time.Duration
	swapSeam(t, &newMetricsStream, func(
		_ *msbSdk.Sandbox, _ context.Context, interval time.Duration,
	) (MetricsStream, error) {
		gotInterval = interval
		return wanted, nil
	})

	handle := realSandboxHandle{handle: &msbSdk.Sandbox{}}
	stream, err := handle.MetricsStream(context.Background(), 4*time.Second)
	if err != nil {
		t.Fatalf("MetricsStream() error = %v", err)
	}
	if stream != MetricsStream(wanted) {
		t.Errorf("MetricsStream() = %v, want the seam's handle", stream)
	}
	if gotInterval != 4*time.Second {
		t.Errorf("interval = %v, want 4s", gotInterval)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/sandbox/msb/ -run 'TestRealSandboxMetricsStreamDelegatesThroughSeam|TestRealSandboxHandleMetricsStreamDelegatesThroughSeam'`
Expected: build failure, `undefined: newMetricsStream` (the seam does not exist yet).

- [ ] **Step 3: Add the seam, the adapter, and the doubles**

In `internal/sandbox/msb/sandbox.go`, add the handle interface beside `SandboxObserver`:

```go
// MetricsStream is a live metrics subscription. Recv blocks until the next
// sample arrives or the stream ends; Close releases the runtime-side resources.
// Recv returning (nil, nil) means the stream ended.
type MetricsStream interface {
	Recv(ctx context.Context) (*Metrics, error)
	Close() error
}
```

Add the method to `SandboxObserver` **and** to `SandboxResourceControl` (both roles need it: the watcher takes the live `Sandbox`, an `Observer`; `resourceproposal.DetectAndOfferMemoryRaise` is passed a `msb.SandboxHandle` from `prepare/reconfig.go`, which satisfies `Observer` only structurally, through `SandboxResourceControl`):

```go
// SandboxObserver reads a sandbox's metrics and logs.
type SandboxObserver interface {
	Metrics(ctx context.Context) (*Metrics, error)
	// MetricsStream subscribes to samples delivered every interval. The caller
	// owns the returned handle and must Close it.
	MetricsStream(ctx context.Context, interval time.Duration) (MetricsStream, error)
	Logs(ctx context.Context, query LogQuery) ([]LogEntry, error)
}

// SandboxResourceControl modifies, compacts and observes a sandbox.
type SandboxResourceControl interface {
	Modify(ctx context.Context, opts ModifyOptions) (*SandboxModificationPlan, error)
	Compact(ctx context.Context, req CompactionRequest) (*DiskCompactionResult, error)
	Metrics(ctx context.Context) (*Metrics, error)
	// MetricsStream is the same subscription the observer exposes; it lives on
	// both roles so a persisted SandboxHandle, whose method set comes from here,
	// still satisfies SandboxObserver.
	MetricsStream(ctx context.Context, interval time.Duration) (MetricsStream, error)
	Logs(ctx context.Context, query LogQuery) ([]LogEntry, error)
}
```

Add the adapter and seam near the existing `realSandbox.Metrics`:

```go
// realMetricsStream wraps the SDK handle behind the launcher MetricsStream, so
// no SDK type crosses the seam (the SDK handle has no exported constructor and
// so cannot itself be stubbed).
type realMetricsStream struct {
	handle *msbSdk.MetricsStreamHandle
}

func (s realMetricsStream) Recv(ctx context.Context) (*Metrics, error) {
	return s.handle.Recv(ctx)
}

func (s realMetricsStream) Close() error {
	return s.handle.Close()
}

// newMetricsStream is the SDK call as a seam returning the launcher handle, so
// a unit test injects a fake without the native library.
var newMetricsStream = func(
	s *msbSdk.Sandbox, ctx context.Context, interval time.Duration,
) (MetricsStream, error) {
	handle, err := s.MetricsStream(ctx, interval)
	if err != nil {
		return nil, err
	}
	return realMetricsStream{handle: handle}, nil
}

func (s realSandbox) MetricsStream(ctx context.Context, interval time.Duration) (MetricsStream, error) {
	return newMetricsStream(s.sandbox, ctx, interval)
}

// realSandboxHandle wraps the same unified *msbSdk.Sandbox, so a persisted
// handle can stream too; its method satisfies both the observer and the
// resource-control role.
func (w realSandboxHandle) MetricsStream(ctx context.Context, interval time.Duration) (MetricsStream, error) {
	return newMetricsStream(w.handle, ctx, interval)
}
```

In `internal/sandbox/msb/msbtest/testmock.go`, add to the `MockSandbox` struct (after `LogsOptions`):

```go
	// MetricsStream backs the seam's push-based metrics observation. An
	// unconfigured mock reports the stream unsupported, so a watcher that
	// prefers the stream takes its Metrics-polling fallback, matching the tests
	// written before the stream existed.
	MetricsStreamFn     func(ctx context.Context, interval time.Duration) (msb.MetricsStream, error)
	MetricsStreamResult msb.MetricsStream
	MetricsStreamErr    error
	// MetricsStreamCalls records every interval passed to MetricsStream.
	MetricsStreamCalls []time.Duration
```

Add the method beside `MockSandbox.Metrics`:

```go
// MetricsStream implements SandboxObserver. With neither a callback nor an
// explicit error it reports the stream unsupported, so the pressure watcher
// falls back to polling.
func (m *MockSandbox) MetricsStream(
	ctx context.Context, interval time.Duration,
) (msb.MetricsStream, error) {
	m.MetricsStreamCalls = append(m.MetricsStreamCalls, interval)
	if m.MetricsStreamFn != nil {
		return m.MetricsStreamFn(ctx, interval)
	}
	if m.MetricsStreamErr != nil {
		return nil, m.MetricsStreamErr
	}
	if m.MetricsStreamResult != nil {
		return m.MetricsStreamResult, nil
	}
	return nil, errors.New("metrics stream not configured")
}
```

Add the same wiring to `MockSandboxHandle`, whose stream method comes from the resource-control role:

```go
	// MetricsStream backs the resource-control role's live subscription.
	MetricsStreamFn     func(ctx context.Context, interval time.Duration) (msb.MetricsStream, error)
	MetricsStreamResult msb.MetricsStream
	MetricsStreamErr    error
	MetricsStreamCalls  []time.Duration
```

```go
// MetricsStream implements SandboxResourceControl. With neither a callback nor
// an explicit error it reports the stream unsupported.
func (m *MockSandboxHandle) MetricsStream(
	ctx context.Context, interval time.Duration,
) (msb.MetricsStream, error) {
	m.MetricsStreamCalls = append(m.MetricsStreamCalls, interval)
	if m.MetricsStreamFn != nil {
		return m.MetricsStreamFn(ctx, interval)
	}
	if m.MetricsStreamErr != nil {
		return nil, m.MetricsStreamErr
	}
	if m.MetricsStreamResult != nil {
		return m.MetricsStreamResult, nil
	}
	return nil, errors.New("metrics stream not configured")
}
```

Add the scripted stream types to `testmock.go` (beside the `ScriptedStreamHandle` types):

```go
// MetricsStreamSample is one scripted Recv result: a sample, the end (both
// values zero), or an error.
type MetricsStreamSample struct {
	Metrics *msb.Metrics
	Err     error
}

// ScriptedMetricsStream is a MetricsStream double replaying samples in order
// and then reporting the stream ended (nil, nil). It records whether the caller
// closed it.
type ScriptedMetricsStream struct {
	mu      sync.Mutex
	samples []MetricsStreamSample
	closed  bool
}

// NewScriptedMetricsStream returns a stream replaying samples in order.
func NewScriptedMetricsStream(samples ...MetricsStreamSample) *ScriptedMetricsStream {
	return &ScriptedMetricsStream{samples: samples}
}

// Recv implements MetricsStream: the next scripted sample, or (nil, nil) once
// the script is exhausted.
func (s *ScriptedMetricsStream) Recv(context.Context) (*msb.Metrics, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.samples) == 0 {
		return nil, nil
	}
	sample := s.samples[0]
	s.samples = s.samples[1:]
	return sample.Metrics, sample.Err
}

// Close implements MetricsStream.
func (s *ScriptedMetricsStream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	return nil
}

// DidClose reports whether Close was called.
func (s *ScriptedMetricsStream) DidClose() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}
```

In `internal/sandbox/msb/msbtest/testmock_roles.go`, add the field to `MockSandboxObserver` and the method:

```go
	MetricsStreamFn func(ctx context.Context, interval time.Duration) (msb.MetricsStream, error)
```

```go
// MetricsStream implements SandboxObserver.
func (m *MockSandboxObserver) MetricsStream(
	ctx context.Context, interval time.Duration,
) (msb.MetricsStream, error) {
	if m.MetricsStreamFn != nil {
		return m.MetricsStreamFn(ctx, interval)
	}
	return nil, nil
}
```

`internal/sandbox/resourceproposal/detect_test.go` needs no change: with `MetricsStream` on `SandboxResourceControl`, `msb.SandboxHandle` still satisfies `msb.SandboxObserver`, so its existing two-line assertion (`_ msb.SandboxObserver = msb.Sandbox(nil)` and `_ msb.SandboxObserver = msb.SandboxHandle(nil)`) stays and passes.

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/sandbox/msb/ ./internal/sandbox/msb/msbtest/ ./internal/sandbox/resourceproposal/`
Expected: PASS.

- [ ] **Step 5: Run the lint**

Run: `make lint`
Expected: clean.

- [ ] **Step 6: Commit**

```bash
git add internal/sandbox/msb/sandbox.go internal/sandbox/msb/msbtest/testmock.go internal/sandbox/msb/msbtest/testmock_roles.go internal/sandbox/msb/sandbox_test.go
git commit -m "feat(msb): add a MetricsStream observer seam and doubles"
```

---

### Task 2: Stream the pressure watcher with a bounded fallback (design §2)

**Files:**
- Modify: `internal/sandbox/session/pressure_watch.go`
- Test: `internal/sandbox/session/pressure_watch_test.go`

**Interfaces:**
- Consumes: `msb.SandboxObserver.MetricsStream`, `msb.MetricsStream`, and `msbtest.NewScriptedMetricsStream`/`MetricsStreamSample` from Task 1.
- Produces: `var metricsStreamStopTimeout = 2 * time.Second`; `type pressureWatcher struct { mu sync.Mutex; summary pressureSummary }` with methods `fold(*msb.Metrics, pressure.Threshold)`, `sample(context.Context, msb.SandboxObserver, pressure.Threshold)`, `tick(context.Context, msb.SandboxObserver, time.Duration, pressure.Threshold)`, `stream(context.Context, msb.SandboxObserver, msb.MetricsStream, time.Duration, pressure.Threshold)`; `func foldSample(m *msb.Metrics, t pressure.Threshold, summary *pressureSummary)`. `startPressureWatcher` keeps its signature; the old free `samplePressure` is replaced by `pressureWatcher.sample`.

- [ ] **Step 1: Write the failing tests**

Append to `internal/sandbox/session/pressure_watch_test.go`:

```go
// contextMetricsStream blocks until its context is done and then returns the
// context error, mirroring a Recv that observes cancellation.
type contextMetricsStream struct {
	closed chan struct{}
	once   sync.Once
}

func (s *contextMetricsStream) Recv(ctx context.Context) (*msb.Metrics, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func (s *contextMetricsStream) Close() error {
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
	return nil, nil
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

// TestPressureWatcherCancelDoesNotRecordReadErr pins that a normal stop is not
// reported as a failed read: Recv returning context.Canceled on teardown must
// leave the summary's ReadErr nil.
func TestPressureWatcherCancelDoesNotRecordReadErr(t *testing.T) {
	stubPressureWatchInterval(t)
	stream := &contextMetricsStream{closed: make(chan struct{})}
	sb := &msbtest.MockSandbox{MetricsStreamResult: stream}

	summary := startPressureWatcher(context.Background(), sb, pressure.DefaultThreshold())()

	if summary.ReadErr != nil {
		t.Errorf("ReadErr = %v, want nil on a normal stop", summary.ReadErr)
	}
	if summary.MinHeadroomFraction != 1.0 {
		t.Errorf("MinHeadroomFraction = %v, want 1.0", summary.MinHeadroomFraction)
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
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/sandbox/session/ -run 'TestPressureWatcherStreamFoldsSamples|TestPressureWatcherFallsBackToPollingOnOpenError|TestPressureWatcherFallsBackToPollingOnFirstRecvError|TestPressureWatcherMidStreamErrorEndsWithoutFallback|TestPressureWatcherCancelDoesNotRecordReadErr|TestPressureWatcherTeardownIsBoundedWhenRecvBlocks|TestMetricsStreamStopTimeoutDefault'`
Expected: build failure, `undefined: metricsStreamStopTimeout` (the seam does not exist yet).

- [ ] **Step 3: Rewrite the watcher**

Replace the body of `internal/sandbox/session/pressure_watch.go` (the `pressureSummary` type is unchanged) so that the seam, the watcher, and its fold read:

```go
// metricsStreamStopTimeout bounds how long the stop func waits for the watch
// goroutine. A stream handle that ignores cancellation leaves the goroutine to
// finish when Recv eventually returns; the attach must not block on it
// (specs/resources/resource-limit-proposal.md §1a).
var metricsStreamStopTimeout = 2 * time.Second

// pressureWatcher owns the summary the watch goroutine folds into. The mutex
// guards it because the bounded-teardown path lets the stop func read it while
// the goroutine may still be running.
type pressureWatcher struct {
	mu      sync.Mutex
	summary pressureSummary
}

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
	watcher := &pressureWatcher{summary: pressureSummary{MinHeadroomFraction: 1.0}}
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
```

The `sync` and `errors` imports join `context`/`time` in the file. The stop func waits only up to `metricsStreamStopTimeout`; on the timeout it returns the summary **silently**. That is the chosen wording: `startPressureWatcher` keeps its signature (no `termio.Logger` can be threaded in) and must not write to the terminal, so the design's "logs at Verbose" is stated as a silent return, and the caller still logs `summary.ReadErr` at Verbose after the attach.

- [ ] **Step 4: Run the watcher tests and the whole session package**

Run: `go test ./internal/sandbox/session/`
Expected: PASS, including the pre-existing polling tests (`TestPressureWatcherTripsAndStoresHeadroom`, `TestPressureWatcherStopWaitsForInFlightSample`, `TestPressureWatcherToleratesMetricsError`, `TestPressureWatcherStopIsPromptAndStopsSampling`, `TestPressureWatcherIgnoresUnusableSnapshot`, `TestPressureWatcherNilSandboxYieldsZeroSummary`) via the fallback.

- [ ] **Step 5: Run the lint and format**

Run: `make fmt && make lint`
Expected: clean. (`make fmt` never rewrites the watch loop away from `gofmt`; the `gomutants` directive comment is preserved.)

- [ ] **Step 6: Commit**

```bash
git add internal/sandbox/session/pressure_watch.go internal/sandbox/session/pressure_watch_test.go
git commit -m "feat(session): stream the pressure watcher with a bounded polling fallback"
```

---

### Task 3: Record the change in the specs, guide, and changelog (design §3)

**Files:**
- Modify: `.operator-shared/specs/resources/resource-limit-proposal.md` (§1a; the frontmatter `read_if`)
- Modify: `.operator-shared/specs/architecture/msb-seam.md` (the "Launcher-owned types" section)
- Modify: `.operator-shared/guides/msb-sdk-integration.md` (§1.4)
- Create: `changelog.d/<PR-number>.changed.md`

**Interfaces:**
- Consumes: the `MetricsStream` seam (Task 1) and the streamed watcher (Task 2).
- Produces: no code symbol; the recorded contracts.

- [ ] **Step 1: Update the resource spec §1a**

In `.operator-shared/specs/resources/resource-limit-proposal.md`, replace the first sentence of §1a's "Metrics watcher" paragraph (the sentence that ends "tunable via a `Session` option/seam) for the lifetime of the attach.") with, keeping the rest of the paragraph word-for-word:

```markdown
**(a) Metrics watcher during the session.** In the shared `session.runAttach` path, start a watcher
goroutine that subscribes to `sb.MetricsStream(ctx, interval)` (default ~2 s, the existing
`pressureWatchInterval` seam) for the lifetime of the attach. When the stream cannot start, or yields
no sample at all (a `Recv` error before the first sample, where the runtime's no-slot error surfaces),
it closes the handle and falls back to polling `sb.Metrics()` at the same interval, exactly once — so a
sandbox with no live metrics slot degrades exactly as it did before, and a mid-stream `Recv` error
(the VM stopped) is kept without polling a dead VM. Teardown cancels the watch and waits for it only
until a small bound, so a stream that ignores cancellation cannot block the attach. A metrics reader
treats an absent field (`MemoryAvailableBytes`, `Upper*`) or an unknown (zero) limit as unknown, never
as zero: the existing `MemoryLimitBytes == 0` guard keeps an unknown limit from tripping or clearing
the threshold, and no nullable pointer is dereferenced.
```

Then extend the frontmatter `read_if` line with the clause `the MetricsStream pressure-watcher fallback, or metricsStreamStopTimeout` before its trailing period.

- [ ] **Step 2: Record the seam in `msb-seam.md`**

In `.operator-shared/specs/architecture/msb-seam.md`, add a bullet to "Launcher-owned types" after the observation-aliases bullet:

```markdown
- **Live stream handles.** A method-carrying handle the SDK returns is a launcher-owned interface, not
  an SDK alias: `MetricsStream(ctx, interval)` is declared on **both** `SandboxObserver` and
  `SandboxResourceControl` and returns the launcher `MetricsStream` (`Recv(ctx) (*Metrics, error)`;
  `Close() error`), never the SDK's `*msbSdk.MetricsStreamHandle`, which has no exported constructor
  and so cannot be stubbed. It sits on both roles because the watcher takes the live `Sandbox` (an
  `Observer`) while `resourceproposal.DetectAndOfferMemoryRaise` is handed a persisted `SandboxHandle`,
  whose method set comes from `SandboxResourceControl`. The adapter's `realMetricsStream` wraps the SDK
  handle and the `newMetricsStream` package seam returns the launcher interface, so a unit test
  substitutes a fake without the native library; `realSandbox` and `realSandboxHandle` both delegate
  through it.
```

- [ ] **Step 3: Fold the adoption and the deferrals into the integration guide §1.4**

Replace `.operator-shared/guides/msb-sdk-integration.md`'s §1.4 body with:

```markdown
### 1.4 Metrics streaming, fleet metrics, sidecar

`Sandbox.MetricsStream` is adopted: the in-session pressure watcher subscribes to it (closing the
handle and falling back to polling `Metrics` when the stream cannot start or yields no first sample)
instead of polling on a ticker (`specs/resources/resource-limit-proposal.md` §1a). `AllSandboxMetrics`
and the `msb-metrics` OTLP sidecar remain deferred: no fleet/status surface wants the former
(`specs/volumes/runtime-management-commands.md` excludes `sandbox metrics`; `df` and `sandbox inspect`
are the observation surface), and agents-sandbox owns no telemetry pipeline for the latter.
```

- [ ] **Step 4: Add the changelog fragment**

Create `changelog.d/<PR-number>.changed.md` (use the implementation PR's number):

```markdown
The in-session pressure watcher now subscribes to the microsandbox metrics stream (with a polling
fallback when the stream is unavailable). No user-visible behaviour changes.
```

- [ ] **Step 5: Run the docs and brain gates**

Run: `make docs-linkcheck brain-check`
Expected: both report ok.

- [ ] **Step 6: Commit**

```bash
git add .operator-shared/specs/resources/resource-limit-proposal.md .operator-shared/specs/architecture/msb-seam.md .operator-shared/guides/msb-sdk-integration.md changelog.d/
git commit -m "docs(msb): record the metrics-stream adoption and the deferred fleet/sidecar"
```

---

### Task 4: Real-VM stream test and the full gate

**Files:**
- Create: `internal/sandbox/msb/metrics_stream_msb_test.go`
- Test: the file itself (host-only, `msb` build tag)

**Interfaces:**
- Consumes: `realSandbox.MetricsStream` (Task 1), `msbtest/runtime.EnsureIsolatedRuntime`/`TestIdleTimeout`/`DestroyTimeout` (existing).
- Produces: nothing; the real-runtime evidence.

- [ ] **Step 1: Write the real-VM test**

Create `internal/sandbox/msb/metrics_stream_msb_test.go`:

```go
//go:build msb

package msb

import (
	"context"
	"strconv"
	"testing"
	"time"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/sandbox/msb/msbtest/runtime"
)

// TestRealMetricsStreamDeliversSamplesAndEnds proves the stream contract the
// pressure watcher relies on against the real runtime: a running sandbox
// delivers a sample at the requested interval with a non-zero memory limit, and
// the stream ends once the sandbox stops, so the watcher stops rather than
// polling a dead VM (specs/resources/resource-limit-proposal.md §1a).
func TestRealMetricsStreamDeliversSamplesAndEnds(t *testing.T) {
	runtime.EnsureIsolatedRuntime(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	name := "agents-sandbox-msb-test-metrics-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	sb, err := msbSdk.CreateSandbox(ctx, name,
		msbSdk.WithImage("alpine:3.20"),
		msbSdk.WithReplace(),
		msbSdk.WithDetached(),
		msbSdk.WithIdleTimeout(runtime.TestIdleTimeout),
	)
	if err != nil {
		t.Fatalf("CreateSandbox: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), runtime.DestroyTimeout)
		defer cleanupCancel()
		if handle, handleErr := msbSdk.GetSandbox(cleanupCtx, name); handleErr == nil {
			_ = handle.Destroy(cleanupCtx)
		}
	})

	adapter := realSandbox{sandbox: sb}
	stream, err := adapter.MetricsStream(ctx, 200*time.Millisecond)
	if err != nil {
		t.Fatalf("MetricsStream: %v", err)
	}
	defer func() { _ = stream.Close() }()

	sample, err := stream.Recv(ctx)
	if err != nil {
		t.Fatalf("Recv: %v", err)
	}
	if sample == nil {
		t.Fatal("Recv returned no sample for a running sandbox")
	}
	if sample.MemoryLimitBytes == 0 {
		t.Errorf("sample = %+v, want a non-zero memory limit", sample)
	}

	if err := sb.Stop(ctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		sample, recvErr := stream.Recv(ctx)
		if recvErr != nil || sample == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the stream did not end after the sandbox stopped")
		}
	}
}
```

- [ ] **Step 2: Run the full gate**

Run: `make check`
Expected: every target reports PASS. `compile-all` compiles the new `msb`-tagged file, so a compile error there fails the gate even though the test does not run.

- [ ] **Step 3: Verify the real-VM stream on a KVM host**

Run on a host with KVM, a Docker daemon, and network: `CGO_ENABLED=1 go test -tags msb -run TestRealMetricsStreamDeliversSamplesAndEnds ./internal/sandbox/msb/`
Expected: PASS — a stream delivers a sample at the requested interval with a non-zero memory limit and ends after `Stop`.

Then, **manually** (no test expectation), exercise the fallback's real trigger on the same host: take a sandbox known to report `no verified live metrics slot` (a sandbox created before the runtime established the slot, per `guides/vm-oom-crash-resilience.md`) and confirm that `startPressureWatcher` records the `Recv` error as the summary's `ReadErr` (reported once after the attach) and that the session does not error — the case a freshly created test sandbox cannot reproduce. While here, confirm the design's open assumptions by hand: that `Sandbox.MetricsStream` returns at the requested interval, and that `Close` after the loop exits releases the handle promptly. If `Recv` reports the end as `(nil, nil)` on cancellation (rather than an error), note it in `specs/resources/resource-limit-proposal.md` §1a's open questions.

- [ ] **Step 4: Commit**

```bash
git add internal/sandbox/msb/metrics_stream_msb_test.go
git commit -m "test(msb): pin the real-runtime metrics stream contract"
```

---

## Out of scope

- `AllSandboxMetrics` and a `sandbox metrics`/fleet status command — deferred (design §3).
- The `msb-metrics` OTLP sidecar or any telemetry pipeline — deferred (design §3).
- Changing the OOM/disk thresholds, the in-guest `df` probe, or the raise proposal — `resource-limit-proposal.md`'s contract, untouched.
- A `MetricsStream` method on `SandboxResourceControl` (design approach D) — rejected: the stream is observation, so it sits on `SandboxObserver`.
- Pause/resume, drain, snapshots, and the other SDK capabilities of issues #915-#922.
