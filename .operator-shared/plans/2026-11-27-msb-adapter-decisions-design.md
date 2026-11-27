---
description: Design for issue #923 - adopt an explicit LocalContext and its Sandboxes()/Volumes() collections in the msb adapter, and settle the captured-observation, error-class, enum, and inspection-unknown decisions that sit on top of the #924 projection reduction.
read_if: Implementing #923 (explicit contexts/collections and the captured-observation contract); changing internal/sandbox/msb/client.go's destination resolution or its package-level SDK seams; revising specs/architecture/msb-seam.md's launcher-owned types, error classes, or captured-observation contract; or asking why Status/Image/Config are snapshots, why the Is* predicates stay, or how an unavailable inspection field is reported.
---

# msb adapter: explicit contexts and the captured-observation contract

Status: proposed. Issue #923. Base: branch `chore/924-msb-projection` (PR #925), which lands the
value-projection reduction on top of the v0.8 migration; that change must land first. This design
supersedes the #923-relevant halves of `guides/msb-sdk-integration.md` §2.1, §2.2, §2.4, §2.5 (the
guide lives on `main`, not on this branch; fold these decisions back into it when the branch lands).

## Intent

The v0.8 migration mechanically adapted the SDK break while keeping the seam's shape. #924 then
removed the value-projection mirroring. What remains is a seam whose *destination* is still ambient
(the SDK resolves the local microsandbox home from the process-global `MSB_HOME`) and whose
*observation semantics* are accidental rather than stated: a handle's `Status`/`Image`/`Config` are
snapshots taken at lookup, the error classes are predicates over SDK kinds with one string match, the
lifecycle enums are string mirrors, and an unavailable inspection field reads as a zero value.

#923 is a decision set, not a feature. Its deliverable is that each of those is a **recorded
decision**, with the one adopted item implemented.

## Goals

- Make the adapter's local destination explicit with a `LocalContext` carrying the launcher's pinned
  home, and route sandbox/volume discovery, creation, listing and removal through its
  `Sandboxes()`/`Volumes()` collections instead of the ambient package-level SDK functions.
- State the captured-observation contract, the error-class vocabulary, the enum policy, and the
  unknown-value convention as durable seam contracts.
- Keep the consumer surface unchanged: no signature churn, and the `phase4_boundary_test.go` /
  `roles_boundary_test.go` invariants stay green.

## Non-goals

- Any SDK capability work (warm start, pause/resume, metrics streaming, filesystem widening, create
  knobs, SSH/agent). Those are issues #915-#922.
- Removing `MSB_HOME`. The runtime installer's extraction cache, the image cache, and the
  package-level storage API still read it, so it stays; item 1 only makes the *sandbox/volume*
  destination explicit.
- Changing the seam's consumer-facing behavior. Item 1 forces the local destination (the launcher is
  local-only), which is an intentional clarification of an ambient choice rather than a new behavior;
  every other item is internal or a documented contract.

## Anti-goals

- A live-`Status`/`Config` rewrite that changes the synchronous accessor signatures and every
  consumer's error path, for no consumer that needs it (see Item 2).
- A launcher sentinel-error layer that wraps every SDK error so `errors.Is` can match, replacing a
  thin predicate layer with a fragile one (see Item 3).
- Introducing SDK enum types as the seam's currency (rejected by #924 and by `msb-seam.md`).

## Constraints

- `phase4_boundary_test.go`: no production package outside `internal/sandbox/msb` may import `msbSdk`.
  Item 1 keeps all SDK types inside the adapter, so the boundary is unchanged.
- `roles_boundary_test.go`: the composite/role method-set invariant. Item 1 touches no interface.
- The adapter's unit tests must stay FFI-free. The existing package-level function-pointer seams
  (`getSandbox`, `createVolume`, …) are the injection points for ~40 test call sites; item 1
  retargets their default bodies without removing them.
- Format with `make fmt`; lint with `make lint`; run `make check` before finalizing. A behavior/target
  change needs a `changelog.d/<PR-number>.changed.md` fragment.

## Current state (measured on `chore/924-msb-projection`)

- `realMsbClient` is a `struct{}` whose methods delegate to package-level function vars bound to the
  SDK's ambient functions (`getSandbox = msbSdk.GetSandbox`, `createVolume = msbSdk.CreateVolume`, …)
  plus `msbSdk.Image.*` and the package-level `msbSdk.StorageUsage`/`PruneStorage`.
- `runtime.ApplyHome` sets `MSB_HOME` to `configpaths.MSBHome()` for the whole process; the runtime
  installer also names that home explicitly through `msbSdk.RuntimeConfig{Home}`. Discovery and
  creation rely on the ambient variable.
- `realSandboxHandle` captures `Info` and the decoded config inspection at `sandboxHandleFrom`, so
  `Status()`/`Image()`/`Config()` share one snapshot; `Connect` reads live status via
  `realSandboxStatus` to avoid booting on a read.
- `errors.go` has six `Is*` predicates over `msbSdk.IsKind` and `IsJobNotRunning`, which matches
  `*msbSdk.JobError.Code == "job_not_running"` because the SDK exposes no kind for that code.
- `enums.go` declares string-backed `SandboxStatus`/`BackendKind`/`VolumeKind`; `LogEntry.Source` is a
  launcher `LogSource`; `JobInfo.State`/`JobLogEntry.Source` are plain `string`.
- `SandboxConfigInspection` carries `Unavailable`/`Redacted` path lists the adapter ignores;
  `sandboxConfigFrom` reads `Spec` and maps a nil field (or a failed rootfs decode) to zero/nil.
- The SDK image/storage surfaces are **not** context-scoped: `microsandbox.Image` is a package-level
  factory and `StorageUsage`/`PruneStorage` are package functions. Only sandboxes and volumes have a
  `Context` collection (`LocalContext.Sandboxes()`/`Volumes()`).

## Approaches considered

### Item 1 - destination

- **A (chosen): explicit `LocalContext`, resolved per operation behind the existing seams.** Build a
  `*msbSdk.LocalContext` from `LocalOptions{Home: configpaths.MSBHome()}` and redefine the
  sandbox/volume seam defaults as closures that call `localContext().Sandboxes()`/`.Volumes()`. The
  test seams stay; the destination becomes explicit; downstream resources inherit the context.
- **B (keep ambient).** Document `MSB_HOME` as the deliberate pin and change nothing. Rejected: it
  leaves the destination decided by an environment variable the issue names as the smell, and the
  SDK's resources already carry a context the launcher could be naming.
- **C (thread a `Context` parameter through the `Client` interface).** Most explicit, but it changes
  every consumer signature and the mock. Rejected as consumer churn for no behavior.

### Item 2 - captured observation

- **A (chosen): keep the snapshot; state the contract.** Every current consumer resolves a fresh
  handle and reads immediately, and the one freshness-critical path (`Connect`) already reads live.
  Document the point-in-time contract and pin it with a test.
- **B: add `LiveStatus(ctx)`/`LiveConfig(ctx)`.** Honest freshness, but no consumer needs it - YAGNI.
- **C: make the accessors live.** Changes `Status()` to `Status(ctx) (SandboxStatus, error)` and the
  like across ~13 call sites and their fail paths. Rejected.

### Item 3 - error classes

- **A (chosen): keep the predicates as the seam's error vocabulary.** They already fold the SDK's
  typed `*Error{Kind}` into named launcher concepts, which is exactly what `msb-seam.md` says the port
  normalizes. Record the one genuine wart (`job_not_running` has no SDK kind) as an upstream gap.
- **B: launcher sentinels matched by `errors.Is`.** Go matches by asking the *returned* error whether
  it is the target, so the adapter would have to wrap essentially every SDK error in a launcher type;
  a single unwrapped path silently breaks the contract. Rejected.

### Item 4 - typed enums

- **A (chosen): keep the launcher string enums; keep job state/source as plain strings.** `#924`
  already settled that an enum-renaming observation stays launcher-owned and that `JobInfo`/
  `JobLogEntry` rename the SDK enum to a launcher string; `JobInfo.State` has no consumer today, so a
  typed enum adds surface with no behavior.
- **B: add launcher `JobState`/`JobOutputSource` types.** Type safety with consumer churn for no
  current reader. Rejected.
- **C: alias the SDK enums.** Violates the seam currency rule. Rejected.

### Item 5 - inspection unknown/redacted

- **A (chosen): zero/nil already reads as unknown; document and pin it.** The read-back compares
  fields with `!= ""` / `> 0` / `!= nil`, so an unavailable field degrades correctly. Add the contract
  to `msb-seam.md` and a seam test.
- **B: surface `Unavailable`/`Redacted` on `SandboxConfig`.** More honest, but no consumer acts on it;
  a read-back-contract change for no behavior. Deferred until a consumer needs it.

## The design

### Item 1 (adopted) - explicit destination

Introduce one resolver seam and rebind the sandbox/volume seams' defaults to it; the seam signatures,
the `Client` interface, and all consumer code are unchanged.

```go
// newLocalContext is the SDK constructor as a seam, so a test can assert the
// destination options without FFI.
var newLocalContext = msbSdk.NewLocalContext

// localContext resolves agents-sandbox's destination, naming the pinned home
// explicitly instead of relying on the process-global MSB_HOME. It is a seam so
// a test can substitute a destination.
var localContext = func() (*msbSdk.LocalContext, error) {
    return newLocalContext(msbSdk.LocalOptions{Home: configpaths.MSBHome()})
}

var (
    getSandbox        = sandboxGet
    createSandbox     = sandboxCreate
    listSandboxesWith = sandboxList
    getVolume         = volumeGet
    createVolume      = volumeCreate
    listVolumes       = volumeList
    removeVolume      = volumeRemove
)

func sandboxGet(ctx context.Context, name string) (*msbSdk.Sandbox, error) {
    c, err := localContext()
    if err != nil {
        return nil, err
    }
    return c.Sandboxes().Get(ctx, name)
}
// ... create -> Sandboxes().Create, list -> Sandboxes().List,
//     volume get/create/list/remove -> Volumes().Get/Create/List/Remove
```

- The `Image.*` and `StorageUsage`/`PruneStorage` seams stay as-is: the SDK exposes no context
  collection for them, so there is nothing to adopt.
- `MSB_HOME` is still applied by `runtime.ApplyHome` for the extraction cache and the image/storage
  paths; the two mechanisms coexist and each covers what the SDK models.
- **Resolution is per call, not memoized.** The SDK resolves its configured context per operation
  (`ffi.ConfiguredContext` per package call), so a per-operation `LocalContext` is the same shape. A
  process-global `sync.OnceValues` was rejected: a test that swaps the constructor after the first
  call cannot observe it, and `-count=2` re-runs the test function in one process against a completed
  `Once`. `localContext` and `newLocalContext` are both reassignable package vars, so every
  destination test is deterministic.
- **The destination is forced local, deliberately.** The ambient path (`msbSdk.GetSandbox` →
  `ffi.ConfiguredContext()`) can select a remote provider from the host's microsandbox configuration;
  `NewLocalContext(Home)` forces local and ignores `ConfigPath`. agents-sandbox is local-only and its
  private home carries no provider config, so both resolve local today; forcing local states the
  intent and removes the remote-selection surface. Record it as an intentional (not user-visible)
  clarification and verify equivalence on a host run (see Testing).
- `Connect` keeps its live status read; adopting `Sandboxes().GetRunning` would re-fetch and would
  replace the launcher's deliberate `ErrSandboxNotRunning` construction, so it is out of scope.

### Item 2 (recorded decision) - captured observation

Add a "Captured observations" clause to `msb-seam.md`: `SandboxHandle`'s `Status`, `Image`, and
`Config`/`ConfigMounts`/`ConfigPorts` are a point-in-time observation taken when the handle is
resolved; a consumer that needs the live VM state re-resolves the handle (as `FindProjectVM` does) or
uses the status-gated `Connect`, which reads live. Pin the contract with a seam test that a
resolved handle reports its captured value without a second lookup.

### Item 3 (recorded decision) - error classes

Keep `errors.go` as the seam's error vocabulary. Record in `msb-seam.md` that the `Is*` predicates are
the launcher's named error classes over the SDK's `*Error{Kind}`, and that `IsJobNotRunning`'s
`*JobError.Code` match is the one class the SDK does not type - an upstream gap to revisit if the SDK
gains a kind.

### Item 4 (recorded decision) - enums

Record that the launcher's string-backed enums (`SandboxStatus`, `BackendKind`, `VolumeKind`,
`LogSource`) are the seam's currency, that a launcher type that **renames** an SDK enum stays
launcher-owned (#924's rule), and that job `State`/`Source` therefore surface as plain strings rather
than new launcher enum types.

### Item 5 (recorded decision) - unknown values

Record in `msb-seam.md` that an inspection field the provider marks unavailable (or that the adapter
cannot decode) reads as the launcher zero value / nil, and that consumers treat that as "unknown"
(`cfg.Image != ""`, `cfg.MaxMemoryMiB > 0`, `cfg.RootDisk != nil`). Add a seam test that an
inspection with `Unavailable`/`Redacted` entries still projects a zero value for those paths.
Surfacing the lists themselves stays deferred.

### Spec and guide updates

- `specs/architecture/msb-seam.md`: the destination clause (item 1), the captured-observation clause
  (item 2), the error-class clause (item 3), the enum clause (item 4), the unknown-value clause
  (item 5).
- `guides/msb-sdk-integration.md` §2.1/§2.2/§2.4/§2.5: fold in the adopted/decided outcomes. The
  guide is on `main`, so this happens when the branch lands; note it in the plan.
- `changelog.d/<PR-number>.changed.md`: the internal destination change (no user-visible behavior).

## Testing

- A destination-options test: install mock config paths, swap `newLocalContext` to capture its
  argument, call a seam, and assert it received `LocalOptions{Home: configpaths.MSBHome()}`. FFI-free.
- Routing itself (that sandbox operations reach `Sandboxes()` and volume operations reach `Volumes()`)
  is verified by the `msb`-tagged suite against the real stack. A zero `*msbSdk.LocalContext{}` cannot
  distinguish the two collections without native code - both fail with `ErrInvalidConfig` - and the
  SDK's collections are concrete structs with no interface, so no unit-level routing assertion is
  attempted and no launcher-owned collection interface is introduced.
- **Test delta from item 1:** `TestRealMsbClientCreateVolumeError` now evaluates `configpaths.MSBHome()`
  before the SDK call, so it must swap `localContext`/`createVolume` or install mock config paths.
  Audit the other `realMsbClient` tests that rely on the SDK failing at `ffi.ConfiguredContext()` for
  the same reason.
- Existing `swapSeam` tests keep passing unchanged otherwise (the seam signatures do not move).
- New `msb-seam` contract tests: captured-status staleness, the error predicates, and the
  unavailable-inspection zero projection.
- Host-run equivalence check: on a KVM host, confirm the explicit-local path resolves the same private
  home the ambient path did (`make test-msb` exercises the real destination).
- `make check` (fmt, lint incl. `deadcode`, test) plus `make brain-check` for the new plan doc and its
  catalog entry.

## Estimates

- Item 1: small. New resolver + ~7 closure bodies in `client.go`; no signature change and no consumer
  change. ~60-90 production lines, plus one existing test adjusted and one new destination-options
  test (~30 test lines). Route verification rides the existing `msb`-tagged suite.
- Items 2-5: documentation and contract tests only. ~2 small seam tests, ~40 spec lines.
- No consumer code changes anywhere.

## What is left to the builder

- Pick the exact seam names and whether the resolver lives in `client.go` or a small
  `destination.go` (the plan proposes `client.go`).
- Decide whether the captured-observation contract test belongs in `msb_test.go` or a focused
  contract file; the plan proposes a new `captured_observation_test.go` and
  `inspection_unknown_test.go`.
- Confirm on a host run that the forced-local destination matches the ambient path's private home.

## References

- `.operator-shared/guides/msb-sdk-integration.md` §2.1, §2.2, §2.4, §2.5, §3
- `.operator-shared/specs/architecture/msb-seam.md`
- `.operator-shared/plans/2026-11-27-msb-projection-reduction-design.md` (#924, the base)
- `internal/sandbox/msb/client.go`, `msb.go`, `sandbox.go`, `errors.go`, `enums.go`, `translate.go`
- `internal/sandbox/runtime/home.go` (`ApplyHome`), `internal/configpaths` (`MSBHome`)
- SDK `core@v0.8.3`: `context.go` (`LocalContext`, `Sandboxes`, `Volumes`), `resource.go`
  (`Info`/`Status`/`ConfigJSON`), `errors.go` (`Error`/`ErrorKind`/`IsKind`/`JobError`), `enums.go`,
  `config_inspection.go` (`Unavailable`/`Redacted`)
