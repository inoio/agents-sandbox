---
description: TDD implementation plan for #916 - adopt the SDK's Touch as the VM keepalive behind a new SandboxActivity role, replace the silent-exec keeper, thread the effective idle timeout, add the opt-in creation-time max-lifetime bound, and record the declined lifecycle primitives.
read_if: `session.keepVMAlive`, `runServeOnly`, or `waitQuiescent` failing to hold a VM / a keeper that stops working silently; `msb.SandboxActivity.Touch`; the `max-lifetime` config key, `RunOptions.MaxLifetime`, `SandboxSpec.MaxDuration`, or the `WithMaxDuration` mapping; or the #916 spec/guide clauses and non-adoption record.
---

# msb lifecycle (Touch keepalive and max-lifetime) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the launcher's silent-exec VM keepalive with the SDK's `Touch` behind a new `SandboxActivity` role, thread the effective idle timeout to the keeper, and add an opt-in creation-time `max-lifetime` bound mapped to `WithMaxDuration`.

**Architecture:** The keeper stops assuming the runtime's idle monitor treats `active_exec_sessions > 0` as never-idle: `keepVMAlive` becomes a `Touch` loop ticking at `clamp(idleTimeout/2, 1s, 2s)` and returns the last touch error so a broken keepalive is observable. The idle timeout is threaded from `RunOptions` to both keeper callers. Separately, a new `max-lifetime` config key flows `viperconfig` → `RunOptions.MaxLifetime` → `msb.SandboxSpec.MaxDuration` → `WithMaxDuration` at creation only (no reconfig detector, no fingerprint). `Ping`, `RequestDrain`, `WaitUntilStopped`/`WaitForStatus`, and `Pause`/`Resume` stay declined in the specs and guide.

**Tech Stack:** Go 1.27, module `github.com/inoio/agents-sandbox`, microsandbox SDK v0.8.3, golangci-lint.

**Spec:** `.operator-shared/plans/2026-11-27-msb-lifecycle-design.md`

## Global Constraints

- Base is `main` after #923 (PR #928) merges; the adapter's `localContext` destination and `Sandboxes()`/`Volumes()` seams are present. Do not re-touch them.
- `phase4_boundary_test.go` must stay green: no production (non-`_test.go`) package outside `internal/sandbox/msb` may import `msbSdk`.
- `roles_boundary_test.go` must stay green: adding `SandboxActivity` to the `Sandbox` composite requires adding `SandboxActivity` to that test's `Sandbox` role list too (the composite must still equal its role union).
- Unit tests must stay FFI-free: swap the package-level seams (`realSandboxTouch`, `keeperInterval`, `keepVMAlive`) or use `msbtest` mocks; never load the native library.
- The keeper interval must be **derived** from the effective idle timeout, never a fixed constant: `clamp(idleTimeout/2, keeperMinInterval=1s, keeperMaxInterval=2s)`. The 2s cap is load-bearing (an existing VM keeps its creation-time idle timeout, which may differ from this run's).
- `WithMaxDuration` is a **create-time** option: it does not join reconfig, a fingerprint family, or the read-back `SandboxConfig`. A change takes effect on the next VM creation.
- A set `max-lifetime` must be a positive whole number of seconds; unset (zero) means unlimited and must not emit `WithMaxDuration`.
- `max-lifetime` counts from VM creation, not from last activity; a bound below the idle timeout effectively caps the session.
- Format with `make fmt` (never `go fmt`); lint with `make lint` (never `go vet`); run `make check` before finalizing.
- A behavior/config change needs a `changelog.d/<PR-number>.<type>.md` fragment (use the literal `<PR-number>`); the new `max-lifetime` key is user-visible and the keepalive switch is internal.

## Review Focus

- **An existing VM whose live idle timeout is not this run's** (`auto-stop-timeout` raised to 10m while the reused VM keeps its ~10s creation-time timer) must still be held: the keeper interval is capped at 2s so a stale derivation cannot outrun the live timer. Pinned by Task 2's `TestKeeperIntervalClamps`.
- **A keeper whose `Touch` fails** must not silently drop the VM: the loop returns the last touch error and both callers log it at Verbose on release. Pinned by Task 2's `TestKeepVMAliveReturnsLastTouchError` and `TestWaitQuiescentLogsKeeperError`.
- **A `max-lifetime` the SDK would reject at create** (negative, or a sub-second value that trips `validateLifecycleLimits` as `ErrInvalidConfig`) must fail at config load instead. Pinned by Task 3's `TestValidateMaxLifetime`.
- **An unset `max-lifetime` must stay unlimited**: the adapter omits `WithMaxDuration` rather than emitting a zero bound a reader could misread. Pinned by Task 4's `TestSandboxOptionsOmitSDKDefaults`.
- **A `max-lifetime` change must not be mistaken for a reconfig**: it is not compared against a live VM, so a reused VM keeps its creation-time bound and the new value applies only on the next creation. Pinned by Task 5's `TestCreateProjectVMConfiguresTheVM` (the spec carries the value) and the absence of any `PlanReconfig` detector for it (Task 5 Step 4).

---

### Task 1: Add the `SandboxActivity` role and the `Touch` adapter

**Files:**
- Modify: `internal/sandbox/msb/sandbox.go` (new role interface; add it to the `Sandbox` composite; `realSandbox.Touch`; the `realSandboxTouch` seam)
- Modify: `internal/sandbox/msb/roles_boundary_test.go` (add `SandboxActivity` to the `Sandbox` role list)
- Modify: `internal/sandbox/msb/sandbox_test.go` (delegation test)
- Modify: `internal/sandbox/msb/msbtest/testmock.go` (add `Touch` to `MockSandbox`)
- Modify: `internal/sandbox/msb/msbtest/testmock_test.go` (record the mock's touches)

**Interfaces:**
- Produces: `type SandboxActivity interface { Touch(ctx context.Context) error }`.
- Produces: `Sandbox` becomes `SandboxExecutor + SandboxStreamer + SandboxAttacher + SandboxJobs + SandboxObserver + SandboxLifecycle + SandboxActivity`.
- Produces: `var realSandboxTouch func(*msbSdk.Sandbox, context.Context) error` (default discards `(*msbSdk.Sandbox).Touch`'s result and returns its error).
- Produces: `msbtest.MockSandbox.Touch(context.Context) error`, its `TouchFn func(context.Context) error`, `TouchErr error`, and `TouchCalls *int`.

- [ ] **Step 1: Write the failing tests**

Append to `internal/sandbox/msb/sandbox_test.go`:

```go
// TestRealSandboxTouchDelegates pins that the adapter's Touch delegates to the
// SDK sandbox's Touch (its result is discarded) and propagates the error.
func TestRealSandboxTouchDelegates(t *testing.T) {
	var called bool
	swapSeam(t, &realSandboxTouch, func(*msbSdk.Sandbox, context.Context) error {
		called = true
		return nil
	})
	if err := (realSandbox{}).Touch(context.Background()); err != nil {
		t.Fatalf("Touch() error = %v, want nil", err)
	}
	if !called {
		t.Error("Touch() did not delegate to realSandboxTouch")
	}

	boom := errors.New("touch boom")
	swapSeam(t, &realSandboxTouch, func(*msbSdk.Sandbox, context.Context) error { return boom })
	if err := (realSandbox{}).Touch(context.Background()); !errors.Is(err, boom) {
		t.Errorf("Touch() error = %v, want %v", err, boom)
	}
}
```

Append to `internal/sandbox/msb/msbtest/testmock_test.go`:

```go
// TestMockSandboxTouch pins the sandbox double's Touch: it records the call and
// returns the configured error, defaulting to success.
func TestMockSandboxTouch(t *testing.T) {
	var calls int
	sb := &MockSandbox{TouchCalls: &calls}
	if err := sb.Touch(context.Background()); err != nil {
		t.Fatalf("Touch() error = %v, want nil", err)
	}
	if calls != 1 {
		t.Errorf("TouchCalls = %d, want 1", calls)
	}

	boom := errors.New("touch boom")
	if err := (&MockSandbox{TouchErr: boom}).Touch(context.Background()); !errors.Is(err, boom) {
		t.Errorf("Touch() error = %v, want %v", err, boom)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/sandbox/msb/... -run 'TestRealSandboxTouchDelegates|TestMockSandboxTouch'`
Expected: build failure, `undefined: realSandboxTouch` / `sb.Touch undefined`.

- [ ] **Step 3: Implement the role, adapter, and mock**

In `internal/sandbox/msb/sandbox.go`, add after `SandboxLifecycle`:

```go
// SandboxActivity refreshes a sandbox's idle activity timer. It is the keeper's
// narrow dependency: a resident holder touches the VM so the runtime's idle
// monitor does not stop it while the holder waits.
type SandboxActivity interface {
	Touch(ctx context.Context) error
}
```

Add `SandboxActivity` to the `Sandbox` composite, and implement it on `realSandbox`:

```go
// realSandboxTouch is the SDK Touch as a seam: the adapter discards the
// SDK's touch result and surfaces only the error.
var realSandboxTouch = func(sandbox *msbSdk.Sandbox, ctx context.Context) error {
	_, err := sandbox.Touch(ctx)
	return err
}

func (s realSandbox) Touch(ctx context.Context) error {
	return realSandboxTouch(s.sandbox, ctx)
}
```

In `internal/sandbox/msb/roles_boundary_test.go`, add `reflect.TypeFor[SandboxActivity]()` to the `Sandbox` case's `roles` slice.

In `internal/sandbox/msb/msbtest/testmock.go`, add to `MockSandbox`:

```go
	// Touch backs the SandboxActivity seam; TouchCalls records the count.
	TouchFn    func(ctx context.Context) error
	TouchErr   error
	TouchCalls *int
```

and the method:

```go
func (m *MockSandbox) Touch(ctx context.Context) error {
	if m.TouchCalls != nil {
		*m.TouchCalls++
	}
	if m.TouchFn != nil {
		return m.TouchFn(ctx)
	}
	return m.TouchErr
}
```

- [ ] **Step 4: Run the tests and the boundaries**

Run: `go test ./internal/sandbox/msb/... -run 'TestRealSandboxTouchDelegates|TestMockSandboxTouch|TestCompositeMethodSetsEqualTheirRoleUnions'` → PASS.
Run: `make lint` → clean.

- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/msb/sandbox.go internal/sandbox/msb/roles_boundary_test.go internal/sandbox/msb/sandbox_test.go internal/sandbox/msb/msbtest/testmock.go internal/sandbox/msb/msbtest/testmock_test.go
git commit -m "feat(msb): add the SandboxActivity role and Touch adapter"
```

---

### Task 2: Replace the silent-exec keeper with the Touch loop and thread the idle timeout

**Files:**
- Modify: `internal/sandbox/session/reaper.go` (`keepVMAlive`, `keeperInterval`, the interval constants, `waitQuiescent`, `reapOnLastClient`)
- Modify: `internal/sandbox/session/run.go` (`withClientSession`, `runServeOnly`, `runServeOnlySession`, `runAttach`, `Run`/`Shell`, `effectiveIdleTimeout`)
- Modify: `internal/sandbox/session/reaper_test.go` and `internal/sandbox/session/run_test.go` (call sites + new tests; `TestReapOnLastClient_ClientReattachesDuringWait`'s executor double changes)

**Interfaces:**
- Consumes: `msb.SandboxActivity` and `msbtest.MockSandbox.Touch` from Task 1.
- Produces: `var keepVMAlive func(ctx context.Context, sb msb.SandboxActivity, idleTimeout time.Duration) func() error` (a seam holding the production loop). It takes the **narrow** `msb.SandboxActivity`; only its two callers need the composite.
- Produces: `var keeperInterval func(idleTimeout time.Duration) time.Duration`; `const keeperMinInterval = 1 * time.Second`; `const keeperMaxInterval = 2 * time.Second`.
- Produces: `func effectiveIdleTimeout(configured time.Duration) time.Duration`.
- Produces (changed signatures): the keeper callers widen `sb` from `msb.SandboxExecutor` to the composite `msb.Sandbox` (its method set now includes `SandboxActivity`), and each gains `idleTimeout time.Duration`:
  - `waitQuiescent(ctx, k, sb msb.Sandbox, provider, maxRetry, idleTimeout, ui)` — runs `Shell` (executor) and calls `keepVMAlive(keeperCtx, sb, idleTimeout)` (activity).
  - `reapOnLastClient(ctx, a, k, sb msb.Sandbox, policy, idleTimeout, ui)` — forwards to `waitQuiescent`.
  - `withClientSession(ctx, k, sb msb.Sandbox, policy, idleTimeout, ui, shouldReap, a, body)` — forwards to `reapOnLastClient`.
  - `runServeOnly(ctx, a, sb msb.Sandbox, ui, hostPort, idleTimeout, stdinWatched)` — calls `keepVMAlive`.
  - `runServeOnlySession(ctx, a, k, sb msb.Sandbox, policy, ui, hostPort, idleTimeout, deferred, stdinWatched)` — forwards to both.
  - `runAttach` already takes `sb msb.Sandbox`; it computes `effectiveIdleTimeout(opts.IdleTimeout)` and passes it to `withClientSession`.
- Consumes (unchanged): `preparedSandbox.Sandbox()` already returns `msb.Sandbox`, so `Run`/`Shell` need only pass `effectiveIdleTimeout(opts.IdleTimeout)` down; no `msb.Sandbox` construction is added.

- [ ] **Step 1: Write the failing tests**

Replace the existing `TestKeepVMAliveStopEndsTheKeeper` in `reaper_test.go` (its silent-exec assertion no longer applies) with the loop tests below.

Append to `internal/sandbox/session/reaper_test.go` (this adds the `sync/atomic` import to the file):

```go
// touchRecorder is a SandboxActivity double that counts touches. The count is
// atomic: the keeper touches from its own goroutine while the test reads it.
type touchRecorder struct {
	touches atomic.Int32
	err     error
}

func (r *touchRecorder) Touch(context.Context) error {
	r.touches.Add(1)
	return r.err
}

// TestKeeperIntervalClamps pins the keeper cadence: half the idle timeout,
// floored at 1s and capped at 2s so a stale run-level derivation cannot outrun
// a reused VM's live timer.
func TestKeeperIntervalClamps(t *testing.T) {
	for _, tc := range []struct {
		idle, want time.Duration
	}{
		{0, keeperMinInterval},
		{1 * time.Second, keeperMinInterval},
		{2 * time.Second, keeperMinInterval},
		{4 * time.Second, keeperMaxInterval},
		{10 * time.Second, keeperMaxInterval},
		{10 * time.Minute, keeperMaxInterval},
	} {
		if got := keeperInterval(tc.idle); got != tc.want {
			t.Errorf("keeperInterval(%v) = %v, want %v", tc.idle, got, tc.want)
		}
	}
}

// TestKeepVMAliveTouchesUntilStopped pins that the real keeper touches the VM
// and stops touching once released.
func TestKeepVMAliveTouchesUntilStopped(t *testing.T) {
	restore := swapKeeperInterval(t, time.Millisecond)
	defer restore()

	sb := &touchRecorder{}
	stop := keepVMAlive(context.Background(), sb, options.DefaultVMIdleTimeout)
	touched := false
	for range 50 {
		if sb.touches.Load() > 0 {
			touched = true
			break
		}
		time.Sleep(time.Millisecond)
	}
	if !touched {
		t.Fatal("keepVMAlive never touched the VM")
	}
	if err := stop(); err != nil {
		t.Fatalf("stop() = %v, want nil after successful touches", err)
	}
	before := sb.touches.Load()
	time.Sleep(5 * time.Millisecond)
	if got := sb.touches.Load(); got != before {
		t.Errorf("keeper touched %d more times after stop()", got-before)
	}
}

// TestKeepVMAliveReturnsLastTouchError pins the observability contract: a
// failing touch is tolerated (the loop keeps going) and the last error is
// returned by the stop func.
func TestKeepVMAliveReturnsLastTouchError(t *testing.T) {
	restore := swapKeeperInterval(t, time.Millisecond)
	defer restore()

	boom := errors.New("touch boom")
	sb := &touchRecorder{err: boom}
	stop := keepVMAlive(context.Background(), sb, options.DefaultVMIdleTimeout)
	for range 50 {
		if sb.touches.Load() >= 2 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if sb.touches.Load() < 2 {
		t.Fatalf("keeper stopped after one touch (%d), want it to tolerate the error and keep going", sb.touches.Load())
	}
	if err := stop(); !errors.Is(err, boom) {
		t.Errorf("stop() = %v, want %v", err, boom)
	}
}

// TestReapOnLastClientPassesIdleTimeoutToKeeper pins that the reaper hands the
// effective idle timeout to the keeper (so the interval is derived, not fixed).
func TestReapOnLastClientPassesIdleTimeoutToKeeper(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	got := make(chan time.Duration, 1)
	origKeeper := keepVMAlive
	keepVMAlive = func(_ context.Context, _ msb.SandboxActivity, idleTimeout time.Duration) func() error {
		got <- idleTimeout
		return func() error { return nil }
	}
	defer func() { keepVMAlive = origKeeper }()

	sb := &msbtest.MockSandbox{Name_: "test-vm", ShellOut: map[string]msb.ShellResult{
		"curl -sf http://127.0.0.1:4096/session/status": msbtest.NewTestResult(true, 0, `{}`, "", nil),
	}}
	if err := reapOnLastClient(context.Background(), opencodeResolved(t),
		state.Key{Slug: "idlek", Agent: "opencode"}, sb, options.ReapPolicy{}, 45*time.Second, &termiotest.Mock{}); err != nil {
		t.Fatalf("reapOnLastClient: %v", err)
	}
	if d := <-got; d != 45*time.Second {
		t.Errorf("keeper idle timeout = %v, want 45s", d)
	}
}

// TestWaitQuiescentLogsKeeperError pins that the reaper logs a keeper that
// returned an error, so a keepalive that stopped working is visible.
func TestWaitQuiescentLogsKeeperError(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	boom := errors.New("touch boom")
	orig := keepVMAlive
	keepVMAlive = func(context.Context, msb.SandboxActivity, time.Duration) func() error {
		return func() error { return boom }
	}
	defer func() { keepVMAlive = orig }()

	ui := &termiotest.Mock{}
	sb := &msbtest.MockSandbox{Name_: "test-vm", ShellOut: map[string]msb.ShellResult{
		"curl -sf http://127.0.0.1:4096/session/status": msbtest.NewTestResult(true, 0, `{}`, "", nil),
	}}
	err := reapOnLastClient(context.Background(), opencodeResolved(t),
		state.Key{Slug: "logkeeper", Agent: "opencode"}, sb, options.ReapPolicy{}, options.DefaultVMIdleTimeout, ui)
	if err != nil {
		t.Fatalf("reapOnLastClient: %v", err)
	}
	found := false
	for _, v := range ui.VerboseCalls {
		if strings.Contains(v, "touch boom") {
			found = true
		}
	}
	if !found {
		t.Errorf("keeper error not logged; verbose = %v", ui.VerboseCalls)
	}
}
```

Add the test helper at the end of `reaper_test.go`:

```go
// swapKeeperInterval lowers the keeper cadence for a test and restores it.
func swapKeeperInterval(t *testing.T, d time.Duration) func() {
	t.Helper()
	orig := keeperInterval
	keeperInterval = func(time.Duration) time.Duration { return d }
	return func() { keeperInterval = orig }
}
```

Append to `internal/sandbox/session/run_test.go`:

```go
// TestRunServeOnlyPassesIdleTimeoutToKeeper pins that serve-only derives the
// keeper interval from the configured idle timeout.
func TestRunServeOnlyPassesIdleTimeoutToKeeper(t *testing.T) {
	got := make(chan time.Duration, 1)
	orig := keepVMAlive
	keepVMAlive = func(_ context.Context, _ msb.SandboxActivity, idleTimeout time.Duration) func() error {
		got <- idleTimeout
		return func() error { return nil }
	}
	defer func() { keepVMAlive = orig }()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_ = runServeOnly(ctx, opencodeResolved(t), &msbtest.MockSandbox{}, &termiotest.Mock{}, 4096, 90*time.Second, false)
	if d := <-got; d != 90*time.Second {
		t.Errorf("keeper idle timeout = %v, want 90s", d)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/sandbox/session/ -run 'TestKeeperIntervalClamps|TestKeepVMAliveTouchesUntilStopped|TestKeepVMAliveReturnsLastTouchError|TestReapOnLastClientPassesIdleTimeoutToKeeper|TestWaitQuiescentLogsKeeperError|TestRunServeOnlyPassesIdleTimeoutToKeeper'`
Expected: build failure, `undefined: keeperInterval` / wrong-arity calls.

- [ ] **Step 3: Implement the keeper in `reaper.go`**

Replace `keepVMAlive` with the Touch loop and its interval seam:

```go
// keeperMinInterval and keeperMaxInterval bound the Touch cadence. The cap is
// load-bearing: an existing VM keeps its creation-time idle timeout, which may
// differ from this run's, so deriving a large interval from the run's value
// could outrun the VM's live ~10s timer. Capped at 2s the keeper holds any live
// timeout comfortably above ~2s.
const (
	keeperMinInterval = 1 * time.Second
	keeperMaxInterval = 2 * time.Second
)

// keeperInterval is the Touch cadence: half the idle timeout, floored at 1s and
// capped at 2s. It is a seam so loop tests can lower the cadence and the
// timeout-threading tests can record the value it received.
var keeperInterval = func(idleTimeout time.Duration) time.Duration {
	return min(max(idleTimeout/2, keeperMinInterval), keeperMaxInterval)
}

// keepVMAlive touches the VM at the keeper interval so the runtime's idle
// monitor does not stop it while the caller holds it, and returns a stop func
// that returns the last touch error, so a keepalive that stopped working is
// observable. It is a seam so the release-logging tests can inject a failure.
var keepVMAlive = func(ctx context.Context, sb msb.SandboxActivity, idleTimeout time.Duration) func() error {
	done := make(chan struct{})
	keeperCtx, keeperCancel := context.WithCancel(ctx)
	interval := keeperInterval(idleTimeout)
	var lastErr error
	go func() {
		defer close(done)
		select {
		case <-keeperCtx.Done():
			return
		default:
			lastErr = sb.Touch(keeperCtx)
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-keeperCtx.Done():
				return
			case <-ticker.C:
				lastErr = sb.Touch(keeperCtx)
			}
		}
	}()
	return func() error {
		keeperCancel()
		<-done
		return lastErr
	}
}
```

Change `waitQuiescent` to take `idleTimeout time.Duration` (between `maxRetry` and `ui`) **and to widen its `sb` parameter from `msb.SandboxExecutor` to the composite `msb.Sandbox`**, pass it to `keepVMAlive`, and log the release error:

```go
	keeperDone := keepVMAlive(keeperCtx, sb, idleTimeout)
	defer func() {
		if err := keeperDone(); err != nil {
			ui.Verbosef("vm keepalive touch failed: %v", err)
		}
	}()
```

The widening is required: `waitQuiescent` runs `Shell` (a `SandboxExecutor` method) and now touches the VM, and `keepVMAlive` accepts only `msb.SandboxActivity`. `msb.Sandbox` embeds both roles, so it satisfies each. Change `reapOnLastClient` to take `idleTimeout time.Duration` (after `policy`) and widen its `sb` parameter to `msb.Sandbox` (it forwards `sb` to `waitQuiescent`).

- [ ] **Step 4: Thread the idle timeout through `run.go`**

Add:

```go
// effectiveIdleTimeout applies the single idle-timeout default, matching the VM
// lifecycle's creation-time fallback.
func effectiveIdleTimeout(configured time.Duration) time.Duration {
	if configured <= 0 {
		return options.DefaultVMIdleTimeout
	}
	return configured
}
```

- `withClientSession` gains `idleTimeout time.Duration` after `policy`, widens `sb` from `msb.SandboxExecutor` to `msb.Sandbox`, and forwards both to `reapOnLastClient`.
- `runServeOnly` gains `idleTimeout time.Duration` after `hostPort`, widens `sb` from `msb.SandboxExecutor` to `msb.Sandbox`, passes it to `keepVMAlive`, and logs its release error the same way as `waitQuiescent`.
- `runServeOnlySession` gains `idleTimeout time.Duration` after `hostPort`, widens `sb` from `msb.SandboxExecutor` to `msb.Sandbox`, and forwards to both `runServeOnly` and `withClientSession`.
- `runAttach` already takes `sb msb.Sandbox`; it computes `effectiveIdleTimeout(opts.IdleTimeout)` and passes it to `withClientSession`.
- `Run` passes `effectiveIdleTimeout(opts.IdleTimeout)` to `runServeOnlySession` (`Run`/`Shell` already hold `sb` as `msb.Sandbox`).

- [ ] **Step 5: Update every existing call site**

Add the new argument at every existing call in the tests, using `options.DefaultVMIdleTimeout`:
- `reaper_test.go`: all `reapOnLastClient(...)` calls.
- `run_test.go`: all `withClientSession(...)` calls, the direct `runServeOnly(...)` call, and the `runServeOnlySession(...)` call.

Retarget the one call site whose double is only an executor: `TestReapOnLastClient_ClientReattachesDuringWait` in `reaper_test.go` builds a `*msbtest.MockSandboxExecutor` (an `msb.SandboxExecutor`-only double) with stateful `ExecFn`/`ShellFn` hooks, which no longer satisfies the widened `reapOnLastClient`. Replace it with a test-local double embedding `*msbtest.MockSandbox` that overrides `Shell` to keep the poll counter, e.g.:

```go
// reattachSandbox drives the reattach from the second status poll; embedding
// MockSandbox supplies the composite (including Touch), and the Shell override
// keeps the stateful poll counter a plain MockSandbox cannot express.
type reattachSandbox struct {
	*msbtest.MockSandbox
	statusPolls  int
	releaseLease func()
}

func (s *reattachSandbox) Shell(_ context.Context, command string, _ msb.ExecRequest) (msb.ShellResult, error) {
	if command != "curl -sf http://127.0.0.1:4096/session/status" {
		return msbtest.NewTestResult(false, 1, "", "", nil), nil
	}
	s.statusPolls++
	if s.statusPolls == 2 {
		s.releaseLease, _ = state.AcquireClientLease(state.Key{Slug: "reattachproj", Agent: "opencode"})
	}
	return msbtest.NewTestResult(true, 0, `{"s1":{"type":"busy","attempt":0}}`, "", nil), nil
}
```

(`MockSandbox.Exec` already returns success by default, so the old `ExecFn` needs no replacement; `MockSandbox` ignores ctx, matching the fixture.) `reattachSandbox` embeds `*msbtest.MockSandbox`, so it satisfies `msb.Sandbox` once Task 1 gives `MockSandbox` its `Touch`.

In the test body, replace `sb := &msbtest.MockSandboxExecutor{...}` with `sb := &reattachSandbox{MockSandbox: &msbtest.MockSandbox{Name_: "test-vm"}}`, drop the outer `var statusPolls int` / `var releaseLease func()` declarations, and point the existing `t.Cleanup` lease release at `sb.releaseLease`.

Delete the now-unused `blockingExecSandbox` type and its `Exec` method from `reaper_test.go` (the `TestKeepVMAliveStopEndsTheKeeper` test it backed is replaced above).

The package must compile before the next step (the whole chain changed together).

- [ ] **Step 6: Run the tests and the lint**

Run: `go test ./internal/sandbox/session/` → PASS.
Run: `go test ./internal/sandbox/...` → PASS.
Run: `make lint` → clean.

- [ ] **Step 7: Host verification of the Touch keepalive (KVM)**

On a KVM host, confirm the design's load-bearing assumption: `CGO_ENABLED=1 go test -tags msb ./internal/sandbox/msb/` passes, and a manually started VM whose last client detached stays alive while a keeper runs at the 1–2s cadence and stops once the keeper ends. Record the result in the PR; nothing else depends on the outcome.

- [ ] **Step 8: Commit**

```bash
git add internal/sandbox/session/reaper.go internal/sandbox/session/run.go internal/sandbox/session/reaper_test.go internal/sandbox/session/run_test.go
git commit -m "fix(session): hold the VM with the SDK Touch keepalive and derive its interval"
```

---

### Task 3: Add the `max-lifetime` config key and `RunOptions.MaxLifetime`

**Files:**
- Modify: `internal/viperconfig/config.go` (the `Config` field and the key constant)
- Modify: `internal/viperconfig/validate.go` (`validateMaxLifetime` and its call in `validate`)
- Modify: `internal/viperconfig/validate_test.go`
- Modify: `internal/viperconfig/configkeys_test.go` (the golden env-key set)
- Modify: `internal/sandbox/options/options.go` (`RunOptions.MaxLifetime`)
- Modify: `internal/runoptions/runoptions.go` (`opts.MaxLifetime = cfg.MaxLifetime`)
- Modify: `internal/runoptions/runoptions_test.go`
- Modify: `cmd/agents-sandbox/cli_run_shell_test.go` (the CLI-level load-rejection test)

**Interfaces:**
- Consumes: the existing `durationDecodeHook`/`ParseHumanDuration` (`7d`-style values).
- Produces: `viperconfig.Config.MaxLifetime time.Duration` with `mapstructure:"max-lifetime"`; `keyMaxLifetime = "max-lifetime"`; `func validateMaxLifetime(v *viper.Viper) error`.
- Produces: `options.RunOptions.MaxLifetime time.Duration`.

- [ ] **Step 1: Write the failing tests**

Append to `internal/viperconfig/validate_test.go`:

```go
// TestValidateMaxLifetime pins the bound's validation: unset is unlimited and
// passes; a positive whole number of seconds passes (including a "7d" string);
// a zero, negative, or sub-second value fails at load.
func TestValidateMaxLifetime(t *testing.T) {
	v := viper.New()
	if err := validateMaxLifetime(v); err != nil {
		t.Fatalf("validateMaxLifetime() with unset key should pass, got %v", err)
	}
	v.Set(keyMaxLifetime, 2*time.Hour)
	if err := validateMaxLifetime(v); err != nil {
		t.Fatalf("validateMaxLifetime() with 2h should pass, got %v", err)
	}
	v.Set(keyMaxLifetime, "7d")
	if err := validateMaxLifetime(v); err != nil {
		t.Fatalf("validateMaxLifetime() with '7d' should pass, got %v", err)
	}
	for _, bad := range []any{0, -1 * time.Second, 500 * time.Millisecond} {
		v.Set(keyMaxLifetime, bad)
		if err := validateMaxLifetime(v); err == nil {
			t.Errorf("validateMaxLifetime() with %v should fail", bad)
		}
	}
}
```

Append to `internal/runoptions/runoptions_test.go`:

```go
// TestBuildMaxLifetime pins the config-to-option mapping: a configured bound
// reaches RunOptions, and unset stays zero (unlimited).
func TestBuildMaxLifetime(t *testing.T) {
	r := viperconfig.NewResolverWithConfig(viperconfig.Config{MaxLifetime: 2 * time.Hour})
	opts, err := Build(r.Config(), newRunCommand(), &termiotest.Mock{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if opts.MaxLifetime != 2*time.Hour {
		t.Errorf("MaxLifetime = %v; want 2h", opts.MaxLifetime)
	}

	zero, err := Build(viperconfig.NewResolverWithConfig(viperconfig.Config{}).Config(), newRunCommand(), &termiotest.Mock{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if zero.MaxLifetime != 0 {
		t.Errorf("MaxLifetime = %v, want 0 (unlimited) when unset", zero.MaxLifetime)
	}
}
```

(This mirrors `TestBuildAutoStopTimeout`.)

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/viperconfig/ -run TestValidateMaxLifetime`
Run: `go test ./internal/runoptions/ -run TestBuildMaxLifetime`
Expected: build failure, `undefined: keyMaxLifetime` / `unknown field MaxLifetime`.

- [ ] **Step 3: Implement the config key and validation**

In `internal/viperconfig/config.go`, add to `Config` beside `AutoStopTimeout`:

```go
	// MaxLifetime bounds a project VM's total lifetime from creation; zero
	// means unlimited. It is applied at creation only (no reconfig).
	MaxLifetime time.Duration `mapstructure:"max-lifetime"`
```

and to the key constants:

```go
	keyMaxLifetime = "max-lifetime"
```

In `internal/viperconfig/validate.go`, call `validateMaxLifetime(v)` from `validate` (beside `validateAutoStopTimeout`):

```go
	if err := validateMaxLifetime(v); err != nil {
		return err
	}
```

and add, mirroring `validateAutoStopTimeout`'s string fallback and adding the whole-seconds rule:

```go
// validateMaxLifetime rejects a set max-lifetime the SDK's lifecycle validation
// would reject at create (ErrInvalidConfig): it must be a positive whole number
// of seconds. Unset means unlimited and passes.
func validateMaxLifetime(v *viper.Viper) error {
	if !v.IsSet(keyMaxLifetime) {
		return nil
	}
	d := v.GetDuration(keyMaxLifetime)
	if d <= 0 {
		if s, ok := v.Get(keyMaxLifetime).(string); ok {
			if parsed, ok := ParseHumanDuration(s); ok {
				d = parsed
			}
		}
	}
	if d <= 0 {
		return fmt.Errorf("launcher config %s must be > 0, got %v", keyMaxLifetime, d)
	}
	if d%time.Second != 0 {
		return fmt.Errorf("launcher config %s must be a whole number of seconds, got %v", keyMaxLifetime, d)
	}
	return nil
}
```

In `internal/viperconfig/configkeys_test.go`, add `keyMaxLifetime` to the `want` slice of `TestConfigEnvKeysGoldenSet` (the derived env/bind surface grew by one key).

In `internal/sandbox/options/options.go`, add to `RunOptions` after `IdleTimeout`:

```go
	// MaxLifetime bounds a project VM's total lifetime from creation; zero
	// means unlimited. It is a create-time option, not compared against a live
	// VM by reconfig.
	MaxLifetime time.Duration
```

In `internal/runoptions/runoptions.go`, in the `if cfg != nil` block beside `opts.IdleTimeout`:

```go
		opts.MaxLifetime = cfg.MaxLifetime
```

- [ ] **Step 4: Run the tests and the lint**

Run: `go test ./internal/viperconfig/ -run 'TestValidateMaxLifetime|TestConfigEnvKeysGoldenSet'` → PASS.
Run: `go test ./internal/runoptions/ -run TestBuildMaxLifetime` → PASS.
Run: `make lint` → clean.

- [ ] **Step 5: Add the CLI-level load-rejection test**

Append to `cmd/agents-sandbox/cli_run_shell_test.go` (the design's Testing lists a CLI-level "rejected at load" case; this is its owning test):

```go
// TestRunRejectsSubSecondMaxLifetimeAtLoad pins that an invalid max-lifetime is
// rejected when the launcher loads its config, before any VM work.
func TestRunRejectsSubSecondMaxLifetimeAtLoad(t *testing.T) {
	initTestProjectDir(t)
	root, _ := setupRunMocks(t, &msbtest.MockMsbClient{}, &msbtest.MockSandbox{}, "run", "--dry-run-vm")
	testutil.WriteYAML(t, configpaths.Get().UserConfigDir(), "config.yaml", map[string]any{"max-lifetime": "500ms"})

	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "max-lifetime") {
		t.Fatalf("run with max-lifetime 500ms: err = %v, want a load error naming max-lifetime", err)
	}
}
```

(`setupRunMocks` installs the mock config paths before the YAML is written; `strings`, `testutil`, `configpaths`, and `msbtest` are already imported in the file.)

Run: `go test ./cmd/agents-sandbox/ -run TestRunRejectsSubSecondMaxLifetimeAtLoad` → PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/viperconfig/config.go internal/viperconfig/validate.go internal/viperconfig/validate_test.go internal/viperconfig/configkeys_test.go internal/sandbox/options/options.go internal/runoptions/runoptions.go internal/runoptions/runoptions_test.go cmd/agents-sandbox/cli_run_shell_test.go
git commit -m "feat(config): add the opt-in max-lifetime VM lifetime bound"
```

---

### Task 4: Map `SandboxSpec.MaxDuration` to `WithMaxDuration`

**Files:**
- Modify: `internal/sandbox/msb/spec_values.go` (`SandboxSpec.MaxDuration`)
- Modify: `internal/sandbox/msb/translate.go` (`sandboxOptions` mapping; move `MaxDuration` from `sandboxConfigNotModeled` to `sandboxConfigProjected`)
- Modify: `internal/sandbox/msb/translate_test.go`

**Interfaces:**
- Consumes: `msbSdk.WithMaxDuration` (SDK v0.8.3).
- Produces: `SandboxSpec.MaxDuration time.Duration` (zero omits the option).

- [ ] **Step 1: Write the failing tests**

In `internal/sandbox/msb/translate_test.go`:
- In `TestSandboxOptionsProjectSpec`, add `MaxDuration: 3 * time.Hour` to the spec and assert `cfg.MaxDuration != 3*time.Hour` fails.
- In `TestSandboxOptionsOmitSDKDefaults`, add `cfg.MaxDuration != 0` to the resource assertion.

The projection test `TestSDKStructFieldsAreClassified` then fails because `MaxDuration` is claimed by both `sandboxConfigNotModeled` and (soon) `sandboxConfigProjected`; it passes only after the register moves.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/sandbox/msb/ -run 'TestSandboxOptionsProjectSpec|TestSDKStructFieldsAreClassified'`
Expected: `sandboxOptions` leaves `MaxDuration` zero / the register reports `MaxDuration` classified twice.

- [ ] **Step 3: Implement the field and the mapping**

In `internal/sandbox/msb/spec_values.go`, add to `SandboxSpec` after `IdleTimeout`:

```go
	// MaxDuration bounds the sandbox's total lifetime from creation; zero
	// leaves it unlimited.
	MaxDuration time.Duration
```

In `internal/sandbox/msb/translate.go`'s `sandboxOptions`, after the idle-timeout block:

```go
	if spec.MaxDuration != 0 {
		opts = append(opts, msbSdk.WithMaxDuration(spec.MaxDuration))
	}
```

and move `"MaxDuration"` from `sandboxConfigNotModeled` (delete its entry) to `sandboxConfigProjected`:

```go
		"MaxDuration":  {},
```

- [ ] **Step 4: Run the tests and the lint**

Run: `go test ./internal/sandbox/msb/` → PASS.
Run: `make lint` → clean.

- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/msb/spec_values.go internal/sandbox/msb/translate.go internal/sandbox/msb/translate_test.go
git commit -m "feat(msb): translate SandboxSpec.MaxDuration to WithMaxDuration"
```

---

### Task 5: Apply `MaxLifetime` at project-VM creation

**Files:**
- Modify: `internal/sandbox/vm/vm_lifecycle.go` (`createProjectVM`)
- Modify: `internal/sandbox/vm/vm_lifecycle_test.go` (`TestCreateProjectVMConfiguresTheVM`)

**Interfaces:**
- Consumes: `options.RunOptions.MaxLifetime` (Task 3) and `msb.SandboxSpec.MaxDuration` (Task 4).
- Produces: no new symbol; `spec.MaxDuration = opts.MaxLifetime` at creation.

- [ ] **Step 1: Write the failing test**

In `internal/sandbox/vm/vm_lifecycle_test.go`'s `TestCreateProjectVMConfiguresTheVM`:
- In the `"defaults"` case, assert `spec.MaxDuration == 0` ("unlimited when unset").
- Add a `"configured max lifetime"` case whose `opts` set `MaxLifetime: 2 * time.Hour` and whose check asserts `spec.MaxDuration == 2*time.Hour`.

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/sandbox/vm/ -run TestCreateProjectVMConfiguresTheVM`
Expected: FAIL, `MaxDuration = 0, want 2h`.

- [ ] **Step 3: Implement the creation-time mapping**

In `internal/sandbox/vm/vm_lifecycle.go`'s `createProjectVM`, add to the `spec := msb.SandboxSpec{...}` literal after `IdleTimeout`:

```go
		MaxDuration:  opts.MaxLifetime,
```

- [ ] **Step 4: Run the test, the lint, and confirm creation-time-only**

Run: `go test ./internal/sandbox/vm/ -run TestCreateProjectVMConfiguresTheVM` → PASS.
Run: `go test ./internal/sandbox/prepare/` → PASS (no `PlanReconfig` detector for `MaxLifetime` exists; a change must not trigger a rebuild).
Run: `make lint` → clean.

- [ ] **Step 5: Host verification of the bound (KVM)**

On a KVM host, confirm the design's real-VM behaviour: create a project VM with a short `max-lifetime` (for example `max-lifetime: "30s"`) and no attached client, and confirm it stops at the bound (total lifetime, independent of idle activity); a `max-lifetime` larger than the idle timeout must leave the VM running well past the idle timeout. Where practical, add an `msb`-tagged test beside `internal/sandbox/msb/realstack_msb_test.go` that creates a sandbox with a ~10s `WithMaxDuration` and asserts it reaches a stopped state at the bound; otherwise record the manual result in the PR. This covers the design's Testing bullet "a VM created with a short `max-lifetime` stops at it".

- [ ] **Step 6: Commit**

```bash
git add internal/sandbox/vm/vm_lifecycle.go internal/sandbox/vm/vm_lifecycle_test.go
git commit -m "feat(vm): apply max-lifetime when creating the project VM"
```

---

### Task 6: Report the bound on the `--dry-run-vm` path

**Files:**
- Modify: `internal/sandbox/session/run.go` (`Run` and `Shell` `mode.SkipsVM()` branches)
- Modify: `internal/sandbox/session/run_test.go`
- Modify: `cmd/agents-sandbox/cli_run_shell_test.go`

**Interfaces:**
- Consumes: `options.RunOptions.MaxLifetime` (Task 3).
- Produces: no new symbol; a `dry-run:` info line when a bound is configured.

- [ ] **Step 1: Write the failing tests**

Append to `internal/sandbox/session/run_test.go`:

```go
// TestRunDryRunVMReportsMaxLifetime pins that a configured lifetime bound is
// reported on the would-start line's path.
func TestRunDryRunVMReportsMaxLifetime(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	ui := &termiotest.Mock{}
	orig := prepareSandbox
	prepareSandbox = func(context.Context, options.RunOptions, bool, termio.UI) (preparedSandbox, error) {
		return &fakePrepared{}, nil
	}
	defer func() { prepareSandbox = orig }()

	opts := resolvedRunOptions(t, options.RunOptions{MaxLifetime: 2 * time.Hour})
	if err := Run(context.Background(), opts, options.ModeSkipVM, ui); err != nil {
		t.Fatalf("Run dry-run-vm: %v", err)
	}
	joined := strings.Join(ui.InfoCalls, "\n")
	if !strings.Contains(joined, "dry-run: max lifetime 2h") {
		t.Errorf("info = %v, want a max-lifetime report", ui.InfoCalls)
	}
}
```

In `cmd/agents-sandbox/cli_run_shell_test.go`, add `TestRunDryRunVMReportsMaxLifetimeFromConfig` that calls `initTestProjectDir(t)` and `setupRunMocks(t, &msbtest.MockMsbClient{}, &msbtest.MockSandbox{}, "run", "--dry-run-vm")` first (it installs the mock config paths), then writes the config with `testutil.WriteYAML(t, configpaths.Get().UserConfigDir(), "config.yaml", map[string]any{"max-lifetime": "2h"})`, then `root.Execute()`, and asserts an info call containing `dry-run: max lifetime 2h`. (`configpaths` is already imported in the file.)

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/sandbox/session/ -run TestRunDryRunVMReportsMaxLifetime`
Run: `go test ./cmd/agents-sandbox/ -run TestRunDryRunVMReportsMaxLifetimeFromConfig`
Expected: FAIL, no such info line.

- [ ] **Step 3: Implement the report**

In `internal/sandbox/session/run.go`, in both `Run`'s and `Shell`'s `mode.SkipsVM()` branch, after the existing `ui.Infof("dry-run: Would start ...")` line, add:

```go
		if opts.MaxLifetime > 0 {
			ui.Infof("dry-run: max lifetime %s", opts.MaxLifetime)
		}
```

Leave the existing would-start line first, so `TestRunDryRunVM`'s `InfoCalls[0]` assertion is unchanged (the default has no bound).

- [ ] **Step 4: Run the tests and the lint**

Run: `go test ./internal/sandbox/session/ ./cmd/agents-sandbox/ -run 'DryRunVM|MaxLifetime'` → PASS.
Run: `make lint` → clean.

- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/session/run.go internal/sandbox/session/run_test.go cmd/agents-sandbox/cli_run_shell_test.go
git commit -m "feat(session): report the configured max-lifetime on a dry-run-vm"
```

---

### Task 7: Update the specs, guide, and user docs; add the changelog

**Files:**
- Modify: `.operator-shared/specs/vm/detached-jobs.md`
- Modify: `.operator-shared/specs/vm/lifecycle.md`
- Modify: `.operator-shared/specs/vm/idle-timeout.md`
- Modify: `.operator-shared/guides/msb-sdk-integration.md` (§1.2)
- Modify: `docs/configuration/launcher.md`
- Modify: `docs/sandboxes.md`
- Create: `changelog.d/<PR-number>.added.md`
- Create: `changelog.d/<PR-number>.changed.md`

**Interfaces:**
- Consumes: the decisions implemented in Tasks 1–6.
- Produces: nothing; the recorded contracts.

- [ ] **Step 1: Replace the open-risk note in `detached-jobs.md`**

Replace the "Open risk in the sibling keepalive paths, verify in the same pass" paragraph with the Touch contract: the reaper and serve-only keeper now hold the VM with the SDK's `Touch` at `clamp(idleTimeout/2, 1s, 2s)`, so they no longer rely on the idle monitor treating `active_exec_sessions > 0` as never-idle; a broken touch is logged at Verbose on release. Keep the manual KVM verification of the detached-job idle behaviour.

- [ ] **Step 2: Note the new creation option and keeper in `lifecycle.md`**

In the stage table, add to the `ensure-vm` row that creation includes an optional `max-lifetime` total-lifetime bound; in the `attach` row, state that the serve-only keeper and the reaper hold the VM with `Touch`. In the `--serve-only` / `--dry-run-vm` notes (the "Modes" block), record that `--dry-run-vm` reports a configured `max-lifetime`.

- [ ] **Step 3: Add the second creation-time bound to `idle-timeout.md`**

Extend the contract with `max-lifetime`: a second, opt-in creation-time lifetime bound (`RunOptions.MaxLifetime` → `msb.SandboxSpec.MaxDuration` → `WithMaxDuration`), zero meaning unlimited, applied at creation only (never compared by reconfig), and distinct from the idle timer (it counts from creation, not from last activity).

- [ ] **Step 4: Fold the outcomes into the SDK integration guide**

Rewrite `guides/msb-sdk-integration.md` §1.2 from a candidate list to the recorded outcome: `Touch` is adopted as the keeper (`SandboxActivity`); `WithMaxDuration` is adopted behind the opt-in `max-lifetime` key; `Ping`, `RequestDrain`, `WaitUntilStopped`/`WaitForStatus`, and `Pause`/`Resume` are declined, each with the design's one-line reason.

- [ ] **Step 5: Add `max-lifetime` to the user docs**

In `docs/configuration/launcher.md`:
- Add a table row beside `auto-stop-timeout`, matching that row's phrasing (no CLI flag, but an `AGENTS_SANDBOX_MAX_LIFETIME` env var; "only in config" rather than "config-file only", since the derived key does have an env var): `max-lifetime` | — | `AGENTS_SANDBOX_MAX_LIFETIME` | Total VM lifetime from creation, independent of activity; unset means unlimited (a positive whole number of seconds, only in config; applied at creation, a change takes effect on the next VM creation).
- Add `max-lifetime` to the example YAML (after `auto-stop-max-session-retries`) and to the Duration-fields list beside `auto-stop-timeout`.
- Add a Validation bullet: "a set `max-lifetime` must be a positive whole number of seconds".

In `docs/sandboxes.md`, in the "Idle timeout: when a sandbox stops" section: mention `max-lifetime` beside the idle-timeout paragraph as the optional total-lifetime cap, add a commented `# max-lifetime: "7d"  # optional; unset = unlimited` line to the example YAML block (do not show a set `"0s"` — a set zero is invalid under the validation), and reword the two hard-coded counts so they do not go stale: "Set all three fields in launcher config:" → "Set these fields in launcher config:" and "None of the three has a CLI flag." → "None of these has a CLI flag."

- [ ] **Step 6: Add the changelog fragments**

Create `changelog.d/<PR-number>.added.md` for the user-visible `max-lifetime` config key and `changelog.d/<PR-number>.changed.md` for the internal keepalive switch (Touch replaces the silent-exec keeper).

- [ ] **Step 7: Run the doc and brain gates**

Run: `make docs-linkcheck brain-check`
Expected: both report ok.

- [ ] **Step 8: Commit**

```bash
git add .operator-shared/specs/vm/detached-jobs.md .operator-shared/specs/vm/lifecycle.md .operator-shared/specs/vm/idle-timeout.md .operator-shared/guides/msb-sdk-integration.md docs/configuration/launcher.md docs/sandboxes.md changelog.d/
git commit -m "docs(vm): record the Touch keepalive and the max-lifetime bound"
```

- [ ] **Step 9: Run the full gate**

Run: `make check`
Expected: every target reports PASS. If formatting fails, run `make fmt` and re-run.

---

## Out of scope

- Any change to the idle-stop policy: `WithIdleTimeout` stays the only idle timer and `options.DefaultVMIdleTimeout` (10s) is unchanged.
- A `sandbox pause`/`resume`, `sandbox wait`, or drain-then-wait command; and idle-pause via a resident supervisor.
- Replacing the application-level readiness probes (`docker info`, the agent health endpoint) with `Ping`.
- A reconfig detector, a fingerprint family, or a read-back `SandboxConfig.MaxDuration`/`IdleTimeout` (the design's approach C, rejected).
- The other #916–#922 SDK capabilities (metrics streaming, filesystem transfer, storage diagnosis, adapter decisions) — separate plans.
