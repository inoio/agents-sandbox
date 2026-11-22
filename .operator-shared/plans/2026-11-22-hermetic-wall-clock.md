# Hermetic wall-clock surface Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Remove wall-clock dependence from the default suite — replace each sleep that merely waits for an event with channel synchronization, keep only the genuine dwells under a reason-bearing marker — and enforce it with a `time.Sleep` category in the ambient-environment gate.

**Architecture:** The design's surface 5. A test that polls `time.Sleep` until an event is load-dependent; a test where the wait *is* the behavior (a poll cadence, a negative assertion that something does not happen) keeps a marked sleep. Enforcement is a new category in the existing ambient gate (`internal/testutil/ambient_environment_contract_test.go`), the same AST scanner that already flags sockets, env reads and host-probe calls, so a new unmarked sleep fails `make test`.

**Tech Stack:** Go 1.27, the repo's package-level test seams, `testutil`'s AST ambient gate.

**Spec:** `.operator-shared/plans/2026-11-07-hermetic-test-suite-design.md` (surface 5 and the Decisions entry "The static gate grows a wall-clock category"). Follows `specs/testing/test-isolation.md` (seams restored; tests off host state). This is the wall-clock half of the design's remaining work; the cadence-wiring half is `.operator-shared/plans/2026-11-22-hermetic-cadence.md`.

## Global Constraints

- Only default-build test files are in scope. The `time.Sleep` sites in `msb`-tagged files (`internal/sandbox/msb/*_msb_test.go`, `internal/sandbox/readback/readback_msb_test.go`) and `test/harness/*_msb_test.go` are out of scope: the design's non-goals exclude the `msb`/`test/harness` suites from hermeticity, and the gate skips non-default-build files.
- A seam override is restored in `t.Cleanup` (`specs/testing/test-isolation.md`); the suite must stay idempotent under `-count>1`.
- The gate category is added **last**, after every site is converted or marked, so each conversion task leaves `make test` green on its own.
- Format/lint with `make fmt`/`make lint` (golangci-lint), never `gofmt`/`go vet`.
- Every code change adds a `changelog.d/<PR-number>.changed.md` fragment (test-only changes are exempt per `ci/check-changelog.sh`, so no fragment is required; add one only if a production file changes).
- The acceptance is `make test-hermetic` and `make test-scrubbed` green (see the cadence plan's task 6 for the run), plus a normal in-tree `make check`.

## Review Focus

1. **A converted wait still waits for the event, not a shorter clock.** Removing the sleep must not remove the synchronization: the test must block on the seam's signal with a bounded timeout, so a dropped signal fails fast rather than passing on a race.
2. **A kept sleep is genuinely a dwell.** A negative assertion ("the proxy still accepts nothing after Stop") or an OS-resource poll may keep a sleep; a "wait until X happens" may not. The marker reason must name *what* dwell is under test.
3. **No production behavior change for the conversions.** The seam a conversion adds is a test-only hook (nil in production) or an existing seam; no production path changes its behavior.
4. **Repeated runs agree.** Each converted package is green under `go test -count=2 ./<pkg>/`; a channel that is signalled once must not hang the second iteration (per-test channels, not package-global state).
5. **The gate catches a new sleep.** After Task 5 a bare `time.Sleep` in a default-build test fails `TestUntaggedTestsReachNoUnsanctionedEnvironment`; the marker fixture proves a reason-bearing `//testenv:allow` sanctions one.

---

### Task 1: `notify` watch tests — replace the count-polls with a signal

**Files:**
- Test: `internal/notify/watch_test.go`

**Interfaces:**
- Consumes: the test-local `recordBackend` (has `count()`), and the fake stream/backoff whose attempt count the tests poll.
- Produces: no production change; the test backend gains a signal channel the tests receive on.

Three sites poll `time.Sleep(10 * time.Millisecond)` inside a 2s-deadline loop: `watch_test.go:116` (waits for `attempts.Load() >= 5`), `:316` (`backend.count() == 1`), `:356` (`inner.count() == 2`).

- [ ] **Step 1: Give the test backend a signal**

Add a `notify chan struct{}` (buffered, e.g. `make(chan struct{}, 16)`) to `recordBackend`, closed/sent on each recorded notification in its `Send`/record method; and give the retry-attempts fake the same signal on each attempt. The exact field/method names follow the existing `recordBackend` shape; read it first.

- [ ] **Step 2: Replace each poll loop with a bounded receive**

At each of the three sites, replace the `deadline`/`time.Now()`/`time.Sleep` loop with a loop that receives on the signal and a `select` against a `time.After(2 * time.Second)` guard that `t.Fatal`s on timeout — so the test proceeds on the event and a lost signal fails loudly.

- [ ] **Step 3: Run the tests**

Run: `go test -tags microsandbox_ffi_path -count=2 ./internal/notify/`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/notify/watch_test.go
git commit -m "test(notify): sync watch tests on a signal, not a poll sleep"
```

---

### Task 2: `opener` watch test — sync the late request on the first tick

**Files:**
- Test: `internal/opener/watch_test.go`

**Interfaces:**
- Consumes: the `opened` channel the test already declares and the watcher it starts.
- Produces: no production change.

`watch_test.go:616` sleeps `40ms` so the watcher has done its first tick before the test writes a "late" request and asserts it is served.

- [ ] **Step 1: Receive on the first tick's signal instead of sleeping**

Replace the `time.Sleep(40 * time.Millisecond)` with a receive on the signal the test already has for the first served request (the `opened` channel) before writing the late request, bounded by a `select` timeout that `t.Fatal`s. The intent — "a request written after the first tick is still served" — is preserved; only the way the test learns the first tick happened changes.

- [ ] **Step 2: Run the test**

Run: `go test -tags microsandbox_ffi_path -count=2 -run TestEffectivePollInterval -run 'Watch' ./internal/opener/`
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add internal/opener/watch_test.go
git commit -m "test(opener): sync the late-request watcher test on the first tick"
```

---

### Task 3: `session` reaper and attach tests — drive the interleaving from the mock

**Files:**
- Test: `internal/sandbox/session/reaper_test.go`, `internal/sandbox/session/run_test.go`

**Interfaces:**
- Consumes: `reapOnLastClient` and its `msbtest.MockSandbox` scripting; `startPressureWatcher`/`runAttach` and the `msbtest.MockSandbox` metrics hook.
- Produces: no production change; both fixtures use the existing mock hooks.

Two sites:

- `reaper_test.go:48`: a goroutine sleeps `20ms` then acquires a client lease so a *later* quiescence poll sees an active client and aborts. Because `waitQuiescent` polls the mock's session-status command, make the **mock** acquire the lease on the poll that should observe it (a scripted `ExecFn`/per-call result) instead of racing a goroutine's sleep. The reattach then happens exactly on the intended poll.
- `run_test.go:2438`: inside `AttachFn`, `time.Sleep(20 * pressureWatchInterval)` lets the watcher sample once during the attach. Make the mock's `Metrics` signal a channel on its first call, and have `AttachFn` receive on that channel (bounded `select`) instead of sleeping.

- [ ] **Step 1: Convert `reaper_test.go`**

Replace the goroutine+`time.Sleep` with a scripted mock response that acquires the lease during the poll the test wants to observe it (read `reaper_test.go`'s existing `sb.ExecOut`/`ExecFn` scripting first). Keep the assertions unchanged.

- [ ] **Step 2: Convert `run_test.go`**

Add a signal channel to the test's `msbtest.MockSandbox` metrics hook (e.g. a `MetricsFn` that notifies on first call) and replace the sleep with a bounded receive, keeping `linesDuringAttach`'s assertion.

- [ ] **Step 3: Run the tests**

Run: `go test -tags microsandbox_ffi_path -count=2 ./internal/sandbox/session/`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/sandbox/session/reaper_test.go internal/sandbox/session/run_test.go
git commit -m "test(session): drive reaper/attach interleavings from the mock, not sleeps"
```

---

### Task 4: mark the genuine dwells

**Files:**
- Test: `internal/opener/forward_test.go`, `internal/sandbox/channel/proxy_test.go`, `internal/sandbox/session/pressure_watch_test.go`, `internal/termio/spinner_test.go`

**Interfaces:**
- Produces: a reason-bearing `//testenv:allow <reason>` marker on each kept sleep's statement, sanctioned by the gate already in `TestUntaggedTestsReachNoUnsanctionedEnvironment`.

Five sleeps are the behavior under test, not an event wait. Mark each, on the sleep's own line, with a reason naming the dwell:

- `internal/opener/forward_test.go:316` — `150ms`, a **negative** assertion: a returned callback's listener must not have closed within the window.
- `internal/opener/forward_test.go:534` — `10ms`, `waitForPortFree`'s poll cadence waiting for the OS to release a port.
- `internal/sandbox/channel/proxy_test.go:814` — `10ms`, a **negative** assertion: the proxy must not accept connections after `Stop`.
- `internal/sandbox/session/pressure_watch_test.go:180` — `20ms`, a **negative** assertion: sampling must not continue after `stop`.
- `internal/termio/spinner_test.go:98` — `5 * spinnerInterval`, the animation-frame cadence that *is* the behavior (a frame must be drawn before `finish`).

- [ ] **Step 1: Add the markers**

For each site, append `//testenv:allow <what the dwell proves>` to the sleep's statement line, e.g. `time.Sleep(150 * time.Millisecond) //testenv:allow proves the returned callback's listener stays open`. The reason must name the dwell, not restate the code.

- [ ] **Step 2: Confirm no unmarked sleep remains in the default suite**

Run: `git grep -n "time.Sleep" -- '*_test.go' :!*_msb_test.go`
Expected: only the five marked sites (plus the msb/harness files, which are out of scope).

- [ ] **Step 3: Run the affected packages**

Run: `go test -tags microsandbox_ffi_path -count=2 ./internal/opener/ ./internal/sandbox/channel/ ./internal/sandbox/session/ ./internal/termio/`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/opener/forward_test.go internal/sandbox/channel/proxy_test.go internal/sandbox/session/pressure_watch_test.go internal/termio/spinner_test.go
git commit -m "test: mark the genuine wall-clock dwells with a reason"
```

---

### Task 5: add the `time.Sleep` category to the ambient gate

**Files:**
- Modify: `internal/testutil/ambient_environment_contract_test.go`
- Test: `internal/testutil/ambient_environment_contract_test.go` (the `TestAmbientEnvironmentUses` fixture table)

**Interfaces:**
- Produces: `var ambientTimeCalls = map[string]bool{"Sleep": true}` and a `case importPath == "time" && ambientTimeCalls[name]: return "time." + name` in `ambientCallKind`.

- [ ] **Step 1: Add the failing fixture cases**

In `TestAmbientEnvironmentUses`' table, add:

```go
{
	name: "a time.Sleep wait is flagged",
	file: `package example
import (
	"testing"
	"time"
)
func TestX(t *testing.T) { time.Sleep(time.Millisecond) }
`,
	want: []string{"time.Sleep"},
},
{
	name: "a marker sanctions a genuine dwell",
	file: `package example
import (
	"testing"
	"time"
)
func TestX(t *testing.T) { time.Sleep(time.Millisecond) } //testenv:allow proves the proxy refuses after Stop
`,
	want: nil,
},
```

- [ ] **Step 2: Run the fixture to verify it fails**

Run: `go test -tags microsandbox_ffi_path -run TestAmbientEnvironmentUses ./internal/testutil/`
Expected: FAIL — the first case's `want` is `time.Sleep`, `got` is empty.

- [ ] **Step 3: Add the category**

Beside `ambientEnvCalls`, add:

```go
// ambientTimeCalls are the time package's blocking waits. A test that waits on
// the clock rather than on a signal is host- and load-dependent; a genuine dwell
// (a poll cadence, a negative assertion that something does not happen within a
// window) keeps the sleep under a reason-bearing //testenv:allow marker.
var ambientTimeCalls = map[string]bool{"Sleep": true}
```

In `ambientCallKind`, before the `isHostProbe` case, add:

```go
case importPath == "time" && ambientTimeCalls[name]:
	return "time." + name
```

- [ ] **Step 4: Run the gate and its fixture**

Run: `go test -tags microsandbox_ffi_path -run 'TestAmbientEnvironmentUses|TestUntaggedTestsReachNoUnsanctionedEnvironment' ./internal/testutil/`
Expected: PASS — the fixture cases pass and the repo-wide scan finds every remaining `time.Sleep` marked (Tasks 1-4 landed).

- [ ] **Step 5: Commit**

```bash
git add internal/testutil/ambient_environment_contract_test.go
git commit -m "test: enforce the wall-clock rule with a time.Sleep gate category"
```

---

### Task 6: verify the envelope and fold the contract

**Files:**
- Modify: `.operator-shared/specs/testing/test-isolation.md` (the "Ambient-environment gate" section's covered-forms list, and the "Wall-clock waits" residual in the audit status).

- [ ] **Step 1: Run the strict envelope**

Run: `make test-hermetic`
Expected: EXIT 0.

- [ ] **Step 2: Confirm the default suite**

Run: `make check`
Expected: EXIT 0.

- [ ] **Step 3: Fold the rule into the spec**

In `specs/testing/test-isolation.md`: add `time.Sleep` to the "Ambient-environment gate" covered-forms list (a clock wait is flagged; a genuine dwell keeps a reason-bearing marker), and update the "Wall-clock waits" audit residual to state the rule is now enforced by the gate and the conversions landed.

- [ ] **Step 4: Commit**

```bash
git add .operator-shared/specs/testing/test-isolation.md
git commit -m "docs(operator): the wall-clock rule is enforced by the ambient gate"
```

---

## Self-Review

- **Spec coverage:** the design's surface 5 has two clauses — replace event-waits with channel sync (Tasks 1-3), keep and mark genuine dwells (Task 4) — and one enforcement clause (Task 5). Task 6 is the acceptance and the contract fold.
- **Step scan:** each step names one action and a checkable result; the conversions name the file and the mechanism (the mock hook or existing channel), leaving the body to the implementer.
- **Type consistency:** `ambientTimeCalls`, the `time.Sleep` category, and the `//testenv:allow` marker are used with the same names throughout.
- **Proportion:** five test-file conversions/marks plus one scanner category — the code the design describes, not a transcript.
- **Ordering:** the gate category lands last (Task 5), so Tasks 1-4 each leave `make test` green.
