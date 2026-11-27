---
description: Design for issue #918 - replace the pressure watcher's point-read polling with the SDK's MetricsStream, and record the fleet-metrics and OTLP-sidecar capabilities as deferred with the "unknown is not zero" rule.
read_if: Implementing #918 (the in-session pressure watcher on MetricsStream, or the `msb.SandboxObserver`/`MetricsStream` seam); changing `session/pressure_watch.go` or `pressure.FromMetrics`; asking why the launcher does not expose a fleet metrics view or the msb-metrics sidecar; or touching a nullable metrics field that must not render as zero.
---

# msb metrics: stream the pressure watcher, record fleet/sidecar as deferred

Status: proposed. Issue #918. Base: branch `chore/923-msb-adapter` (PR #928); lands after #923
merges. Supersedes the #918 half of `guides/msb-sdk-integration.md` §1.4.

## Intent

Resource observation in the launcher is the in-session **pressure watcher**
(`session/pressure_watch.go`): while an attach is open it polls `sb.Metrics()` every 2s and folds
each snapshot into a summary (minimum memory headroom, whether the threshold tripped, the last read
error), which together with the guest kernel OOM line drives the raise proposal
(`specs/resources/resource-limit-proposal.md`). The SDK offers a push-based `MetricsStream` at a
caller-chosen interval, plus a fleet-wide `AllSandboxMetrics` and an OTLP sidecar. Only the stream
has a consumer today; the polling loop is a weaker version of it.

## Goals

- Replace the watcher's polling loop with `Sandbox.MetricsStream`, keeping the same summary and the
  same start/stop lifecycle around the attach.
- Keep the current degrade path: no metrics at all must not break a session or the raise proposal.
- Record `AllSandboxMetrics` and the `msb-metrics` OTLP sidecar as deferred, and restate the
  "unknown is not zero" rule the watcher and any future reader must obey.

## Non-goals

- A `sandbox metrics`/`status` command or a fleet view (`specs/volumes/runtime-management-commands.md`
  excludes `sandbox metrics`; `df` and `sandbox inspect` are the observation surface).
- Changing the OOM/disk detection thresholds, the disk `df` probe, or the raise proposal - those are
  `resource-limit-proposal.md`'s contract and are untouched.
- Wiring the `msb-metrics` sidecar into the runtime or the dev image.

## Anti-goals

- A nullable metrics field (`MemoryAvailableBytes`, `UpperUsedBytes`, `UpperFreeBytes`,
  `UpperHostAllocatedBytes`) folding to zero and tripping or clearing the threshold.
- A stream that outlives the attach or leaks a Rust-side handle.
- Making correctness depend on the stream: if it cannot start, the watcher must still observe.

## Constraints

- `msb-seam.md`: `Metrics` is a sanctioned observation alias; the stream handle is a method-carrying
  type, so it is a launcher-owned interface over the SDK handle.
- The watcher must never write to the terminal while the attach owns it (the current code reports a
  failed read only after the attach).
- The seam's unit tests stay FFI-free (mock/roles), and `roles_boundary_test.go` /
  `phase4_boundary_test.go` stay green.
- Format with `make fmt`; lint with `make lint`; run `make check` before finalizing.

## Current state (measured on the #923 base)

- `SandboxObserver` is `Metrics(ctx) (*Metrics, error)` + `Logs(ctx, query) ([]LogEntry, error)`;
  `Metrics` is `type Metrics = msbSdk.Metrics` (an alias, per the #924 reduction).
- `startPressureWatcher` starts a `time.Ticker` at `pressureWatchInterval` (2s), calling
  `samplePressure` → `sb.Metrics(ctx)`; it returns a stop func yielding the summary. `sb == nil`
  yields a no-op zero summary.
- `samplePressure` ignores a snapshot with `MemoryLimitBytes == 0`; `pressure.FromMetrics(m, t)`
  decides the trip. `MinHeadroomFraction` uses `MemoryLimitBytes - MemoryBytes`.
- The watcher is created in `session/run.go`'s attach path and stopped inside `runAttach` before the
  client lease is released (`specs/resources/resource-limit-proposal.md` §1a).
- The spec records that `Metrics` may be unavailable for a sandbox's whole life ("no verified live
  metrics slot"), which disables the headroom detector; the memory offer must not depend on it.

## Approaches considered

### Observation mechanism

- **A (chosen): `MetricsStream` with a polling fallback.** `sb.MetricsStream(ctx, interval)` returns a
  handle; a goroutine (or the stop-func's drain) receives snapshots until the attach ends. On a
  stream-creation error (unsupported runtime, unavailable slot), fall back to the existing
  `Metrics` ticker so observation degrades rather than disappearing. The summary is unchanged.
- **B: keep polling, shorten the interval.** Less spike coverage and more host→guest calls for the
  same result; rejected.
- **C: stream only, no fallback.** Rejected: the stream can fail where a single `Metrics` read
  succeeds (and vice versa), and the spec already tolerates a metrics-less sandbox.
- **D: a `MetricsStream` on `SandboxResourceControl` instead of `SandboxObserver`.** The watcher
  takes `msb.SandboxObserver`; the stream is observation, so it belongs on `SandboxObserver` beside
  `Metrics`. (`Metrics`/`Logs` already overlap `SandboxResourceControl`/`SandboxObserver`.)

### Fleet/status surface

- **A (chosen): defer.** `AllSandboxMetrics` would back a fleet status view, but the management
  surface excludes `sandbox metrics` and `df`/`sandbox inspect` already answer "what is running".
  Defer until a concrete surface wants it; record the option.
- **B: add `AllSandboxMetrics` behind a `sandbox status` command.** New command surface with no
  committed request; rejected for now.

### OTLP sidecar

- **A (chosen): defer.** The `msb-metrics` sidecar ships metrics to an external backend. agents-sandbox
  owns no telemetry pipeline and the dev image would have to grow; defer until a user asks for it.
- **B: bundle it.** Rejected: scope and an image dependency for no current need.

## The design

### 1. A `MetricsStream` seam

```go
// MetricsStream is a live metrics subscription. Recv blocks until the next
// sample or the stream ends; Close releases the runtime-side resources. Recv
// returning (nil, nil) means the stream ended.
type MetricsStream interface {
    Recv(ctx context.Context) (*Metrics, error)
    Close() error
}
```

- Add `MetricsStream(ctx context.Context, interval time.Duration) (MetricsStream, error)` to
  `SandboxObserver`; `Metrics` stays the alias.
- The SDK returns a concrete `*msbSdk.MetricsStreamHandle` with no exported constructor, so a seam
  typed on the SDK handle could not be stubbed. The adapter seam therefore returns the launcher
  interface, wrapping the SDK handle in a launcher `realMetricsStream`:

```go
// newMetricsStream is the SDK call as a seam returning the launcher handle, so a
// unit test injects a fake without the native library.
var newMetricsStream = func(
    s *msbSdk.Sandbox, ctx context.Context, interval time.Duration,
) (MetricsStream, error) {
    h, err := s.MetricsStream(ctx, interval)
    if err != nil {
        return nil, err
    }
    return realMetricsStream{handle: h}, nil
}
```

- The mock (`msbtest`) gains a `MetricsStream` callback typed on the launcher interface, so it can
  yield a fixed sequence, the end, a first-Recv error, or a mid-stream error.

### 2. The watcher

`startPressureWatcher` keeps its signature, its nil-sandbox no-op, and its summary. Internally it
prefers the stream and falls back to the existing ticker:

- open `sb.MetricsStream(ctx, interval)`; on an **open** error, fall back to the ticker;
- the no-slot error arrives on **`Recv`**, not at open (the SDK's own metrics tests retry
  `no live metrics slot` on `Recv`), so a **first-`Recv` error before any sample** also triggers the
  fallback: close the handle and switch to the ticker. The fallback is attempted at most once (no
  ping-pong);
- a `Recv` error **after** a sample (the VM stopped) records `ReadErr` and ends the loop - the
  stream is gone, and the fallback would only make failing calls against a dead VM;
- the fallback preserves the pre-change behaviour exactly (a failing `Metrics` read every interval,
  the last error reported once after the attach), so a sandbox with no metrics slot at all degrades
  the same way it does today - no fallback can restore observation there, since point reads fail
  too. No new stop-after-N rule is added;
- both paths fold each sample through one shared `foldSample(m, t, *summary)` (today's
  `pressure.FromMetrics`/headroom code, extracted).

**Teardown never blocks the session.** The receive loop runs in a goroutine; `Recv` is called with
the watch context, and on exit the goroutine calls `stream.Close()` itself - so `Close` is never
concurrent with an in-flight `Recv` (the SDK does not establish that combination as safe). The stop
func cancels the context and waits for the goroutine with a small bound
(`metricsStreamStopTimeout`, ~2s); if it does not finish, it logs at Verbose and returns the summary
anyway, leaving the goroutine (and the handle) to close when `Recv` eventually returns. An attach is
the last thing a run does, so a leaked goroutine at process exit is acceptable; blocking teardown is
not (`resource-limit-proposal.md` §1a).

`interval` stays the `pressureWatchInterval` seam (2s by default) for both the ticker and the
stream, so behaviour is unchanged except for push delivery. A `(nil, nil)` `Recv` (stream ended)
ends the loop cleanly.

### 3. Recorded decisions

- **`AllSandboxMetrics`** — deferred: no fleet/status surface wants it.
- **`msb-metrics` OTLP sidecar** — deferred: no telemetry consumer.
- **Nullable fields** — the summary and any future reader treat a nil field as absent
  (`MemoryAvailableBytes`, `Upper*`), never as zero. The watcher keeps its `MemoryLimitBytes == 0`
  guard, and no nil pointer is dereferenced.

## Spec and guide updates

- `specs/resources/resource-limit-proposal.md` §1a: the watcher samples via `MetricsStream` with a
  polling fallback; the "tunable interval" note stays.
- `guides/msb-sdk-integration.md` §1.4: fold in the stream adoption and the deferred fleet/sidecar.
- `specs/architecture/msb-seam.md`: the new `SandboxObserver.MetricsStream` method and the
  launcher-owned `MetricsStream` handle.
- `changelog.d/<PR-number>.changed.md`: the internal watcher change (no user-visible behaviour).

## Testing

- Unit (`session`): the stream path folds samples into the summary; a short/tripping sample sets
  `Tripped`; an open error falls back to polling; a **first-`Recv` error** (no sample yet) also falls
  back, once (no ping-pong); a mid-stream `Recv` error is kept as `ReadErr` and ends the loop without
  falling back; a nil sandbox yields a zero summary; teardown returns within the bound even when
  `Recv` blocks until cancel; the stop func closes the handle on the normal path.
- Unit (`msb`): `realSandbox.MetricsStream` delegates through `newMetricsStream`; the mock's stream
  callback drives the roles test.
- Real VM (`msb` tag / KVM host): a stream delivers samples for a running VM and ends when it stops;
  a sandbox with no live metrics slot records `ReadErr` and does not error the session.

## Estimates

- `msb`: one observer method, one handle interface + adapter, one mock. Small.
- `session`: swap the polling loop for the stream + extract the shared fold; keep the fallback.
  Small.
- Docs. Small.

## What is left to the builder

- Whether the fallback is silent or emits one `Verbosef`.
- The exact bound for `metricsStreamStopTimeout`.
- Confirm on a KVM host that `MetricsStream` returns at the requested interval, that a slotless
  sandbox surfaces its error on `Recv` (the fallback's trigger point), and that `Close` after the
  loop exits releases the handle promptly.

## References

- `.operator-shared/guides/msb-sdk-integration.md` §1.4
- microsandbox v0.8.3 `docs/sandboxes/metrics.mdx`, `docs/observability/msb-metrics.mdx`,
  `sdk/go/core/sandbox.go` (`Metrics`, `MetricsStream`, `MetricsStreamHandle`, `AllSandboxMetrics`)
- `.operator-shared/specs/resources/resource-limit-proposal.md`,
  `specs/architecture/msb-seam.md`
- `internal/sandbox/session/pressure_watch.go`, `internal/sandbox/msb/sandbox.go`,
  `internal/sandbox/pressure/`
