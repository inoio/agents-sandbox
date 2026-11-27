---
description: Design for issue #916 - adopt the microsandbox lifecycle primitives the launcher's ephemeral-client model gives a job (a Touch keepalive, an opt-in max-lifetime bound) and record the deliberate non-adoptions (Ping, RequestDrain, WaitUntilStopped/WaitForStatus, Pause/Resume) with their reasons.
read_if: Implementing #916 (replacing the reaper/serve-only keepalive with Touch, or adding a max-lifetime config bound); changing `session.keepVMAlive` or its callers; asking why the launcher does not pause on idle or adopt Ping/drain; or reviving pause/resume as a command or an idle policy.
---

# msb lifecycle: Touch keepalive, max-lifetime, and the recorded non-adoptions

Status: proposed. Issue #916. Base: branch `chore/923-msb-adapter` (PR #928); lands after #923
merges. Supersedes the #916 half of `guides/msb-sdk-integration.md` §1.2.

## Intent

The launcher's VM lifecycle is coarse by design: a VM is created with `WithIdleTimeout`, the runtime
stops it once the last attached client has been quiet for the idle timeout, and `stop`/`kill` act by
handle. Two places compensate for that model with hand-rolled machinery, and the SDK offers the
primitives they are imitating:

- the **reaper** (`session.waitQuiescent`) and the **serve-only keeper** hold a VM across a wait by
  running a silent `sleep 1h` in-VM exec (`keepVMAlive`), on the assumption that
  `active_exec_sessions > 0` suppresses the idle timer - exactly the aggregate microsandbox v0.7.11
  changed, which `specs/vm/detached-jobs.md` records as an **unverified open risk**;
- the launcher sets only `WithIdleTimeout`; `WithMaxDuration` (a total-lifetime bound) is unused.

#916 lists six primitives. This design adopts the two that fit a real launcher need, replaces the
fragile keeper, and records the rest as deliberate non-adoptions so the guide's checklist is settled
rather than pending.

## Goals

- Replace the silent-exec keeper with the SDK's `Touch` keepalive, removing the dependency on an
  idle-monitor behaviour the launcher cannot verify.
- Offer an opt-in total-lifetime bound (`WithMaxDuration`) as a safety net for a leaked long-lived
  VM, defaulting to unlimited so nothing changes by default.
- Record `Ping`, `RequestDrain`, `WaitUntilStopped`/`WaitForStatus`, and `Pause`/`Resume` as
  non-adoptions with reasons that a future reader can re-evaluate.

## Non-goals

- Any change to the idle-stop policy: the SDK's `WithIdleTimeout` stays the only idle timer, and the
  default (`options.DefaultVMIdleTimeout`, 10s) is unchanged.
- A `sandbox pause`/`resume` command, and a `sandbox wait` command (the management surface excludes
  them; `specs/volumes/runtime-management-commands.md`).
- Replacing the application-level readiness probes (`docker info`, the agent health endpoint) with
  `Ping`.

## Anti-goals

- A silent-exec keeper kept "just in case" alongside the Touch loop: two keepalives hide which one
  holds the VM. `Touch` replaces it.
- A max-lifetime expiry indistinguishable from a crash. A mid-session expiry is a graceful stop, and
  the existing stop-classification already reports a stop during an attach ("the VM stopped during
  the attach; run again", `specs/vm/lifecycle.md`); naming the cause specifically is out of scope, so
  the anti-goal is met by the stop being reported, not by expiry-specific detection.
- Adopting a primitive the launcher has no workflow for, only because the SDK exposes it.

## Constraints

- `msb-seam.md`: new lifecycle operations are launcher-owned roles; the consumer-narrowing rule
  means the keeper takes the narrowest role that has `Touch`.
- The keeper interval must be strictly below the effective idle timeout, which is configurable
  (`auto-stop-timeout`), so the interval is derived, never a fixed constant the user can undercut.
- `WithMaxDuration` is a create-time option, and - like the idle timeout - it is **not** compared
  against the live VM by reconfig: the SDK exposes it only in the persisted config's untyped
  lifecycle map, the launcher's read-back `SandboxConfig` models neither `MaxDuration` nor
  `IdleTimeout`, and a fingerprint family for it would be new machinery. A change therefore applies
  on the next VM creation, not to an existing VM (the read-back alternative is recorded below).
- The composite/role invarariant (`roles_boundary_test.go`) and the SDK boundary
  (`phase4_boundary_test.go`) must stay green.
- Format with `make fmt`; lint with `make lint`; run `make check` before finalizing. A
  behavior/config change needs a `changelog.d/<PR-number>.<type>.md` fragment.

## Current state (measured on the #923 base)

- `session/reaper.go` `keepVMAlive(ctx, sb msb.SandboxExecutor)` starts a goroutine running
  `sb.Exec(ctx, "sleep", []string{"1h"}, …)` and returns a stop func; `waitQuiescent` and
  `session/run.go`'s serve-only keeper
  (`runServeOnly`) both use it.
- `keepVMAlive` is silent about success: the `Exec` error is discarded, so if the keeper exec no
  longer suppresses the idle timer the VM simply stops and the wait returns through a failed poll
  (or `vm_stopped.go` classification), with no signal that the keepalive failed.
- `msb.SandboxLifecycle` (`Destroy`, `Close`) and `msb.SandboxExecutor` (`Shell`, `Exec`, `FS`) are
  the roles the keeper sees today; there is no `Touch`, `Pause`, `Resume`, `RequestDrain`,
  `WaitUntilStopped`, `WaitForStatus`, or `Ping` on any role.
- `msb.SandboxSpec` carries `IdleTimeout` only; `translate.go`'s `sandboxOptions` maps it to
  `WithIdleTimeout`.
- `vm.vm_control.go` already refuses `stop`/`kill` on a paused VM ("resume is not supported"), and
  `status.go` classifies `paused`/`draining` as active - the launcher models the states even though
  no launcher path produces them.
- `options.DefaultVMIdleTimeout` is 10s; `RunOptions.IdleTimeout` is the configured
  `auto-stop-timeout` or zero, defaulted at creation. The idle timeout is applied only at creation:
  reconfig neither reads it back nor recreates on a change (the SDK inspection has it only in the
  untyped `Lifecycle` map; `msb.SandboxConfig` projects image/labels/resources only), so a reused VM
  keeps its creation-time value - the model `max-lifetime` follows.

## Approaches considered

### Keepalive: Touch vs the silent exec vs both

- **A (chosen): replace `keepVMAlive`'s exec with a `Touch` loop.** `Touch` is the SDK's documented
  "refresh this sandbox's idle activity timer"; it takes no exec session, needs no assumption about
  the idle monitor's aggregate, and its failure is observable (the loop can report a failed touch).
  The loop ticks at `effectiveIdleTimeout/2` (floor 1s), so the VM stays alive regardless of the
  configured idle timeout.
- **B: keep the silent exec, add `Touch` beside it.** Rejected: two keepalives, and the exec's
  assumption is the thing we are removing.
- **C: keep the silent exec, verify it on a KVM host.** Rejected as the design: it leaves a
  launcher-critical behaviour resting on an undocumented runtime aggregate that has already changed
  once (v0.7.11). `Touch` is the API contract instead. (The verification in `detached-jobs.md`'s
  open-risk note still happens, but nothing depends on the outcome.)

### Max-lifetime: opt-in config vs serve-only-only vs decline

- **A (chosen): an opt-in `max-lifetime` config bound, applied at creation like the idle timeout.**
  Unset means unlimited; a set value maps to `WithMaxDuration`. It bounds a leaked detached/serve
  VM's total lifetime without changing default behaviour, and a change takes effect on the next VM
  creation - no reconfig detector, no fingerprint family, no read-back change. This is the smallest
  shape that delivers the bound.
- **B: set it only for `--serve-only` creations.** Rejected: the project VM is shared and recreated
  as needed, so a VM created serve-only then used by a normal run would still carry the bound, and a
  VM created by a normal run then used serve-only would not - the bound would depend on creation
  order, not on the config.
- **C: exclude it from existing VMs, but recreate on a change (read-back route).** Add `MaxDuration`
  (and `IdleTimeout`) to the read-back `msb.SandboxConfig`, parse the persisted `Lifecycle` map in
  `sandboxConfigFrom`, and add `PlanReconfig` detectors, mirroring memory/disk. Rejected as more
  machinery than the knob earns: it also drags in an idle-timeout detector that does not exist
  today, and the serve-only/detached safety-net case creates its VM fresh.
- **D: decline it.** Rejected: it is the one capability in the issue that bounds an unbounded
  resource, and an opt-in default-unset knob costs nothing. It remains the weakest adopted item; if
  the plan finds it touches more than the create path, drop it and record the deferral.

### Pause/Resume: decline as idle policy, no command

- **A (chosen): decline, recorded.** The issue's headline use case - pause instead of stop on idle -
  is **not expressible in the launcher's model**. The runtime enforces `idle_timeout` itself once the
  last client detaches, and the SDK exposes no "pause on idle" action; making pause the idle policy
  would require a resident supervisor process to time out and pause the VM, and the launcher's
  clients are ephemeral (a run attaches, detaches, and exits; the reaper holds briefly, then exits).
  There is also no command surface: `sandbox pause`/`resume` are excluded by
  `specs/volumes/runtime-management-commands.md`. No launcher path produces a paused VM, so
  `Resume` has no consumer. The existing `stopOrKillProjectVM` guard (refuse, "resume is not
  supported") stays.
- **B: revive pause/resume as a `sandbox pause`/`resume` command.** A new management surface that
  the spec excludes and that no user flow needs; rejected for now, revisit only if a workflow (for
  example, parking a VM to save CPU while keeping RAM) is proposed.
- **C: pause-on-idle via a resident supervisor.** Rejected as infeasible without a persistent
  launcher process; it also fights the OOM story (`specs/resources/resource-limit-proposal.md`),
  since a paused VM's RAM stays resident and is invisible to the guest-side headroom detectors.

### Ping: decline

`Ping` checks the microsandbox guest agent (`agentd`) is reachable without refreshing idle activity.
`agentd` is present whenever the VM runs, so `Ping` is implied by the sandbox status and says nothing
about the two things the launcher actually waits for: `dockerd` readiness and the agent serve
daemon's health (`specs/vm/detached-jobs.md`). The application-level probes are strictly stronger;
recorded as declined.

### Drain and clean waits: decline

`RequestDrain` stops accepting commands and lets in-flight ones finish. The launcher has no drain
workflow: its stop paths are the runtime's idle stop and the explicit `sandbox stop`/`kill`, and
`SandboxHandle.Stop` already waits for graceful shutdown. `WaitUntilStopped`/`WaitForStatus` would
replace polling that does not exist. Recorded as declined; revisit if a graceful-stop or
drain-then-wait command is added.

## The design

### 1. A `Touch` keepalive role

Add one role and one seam method; the adapter delegates to `msbSdk.Sandbox.Touch`.

```go
// SandboxActivity refreshes a sandbox's idle activity timer. It is the keeper's
// narrow dependency: a resident holder touches the VM so the runtime's idle
// monitor does not stop it while the holder waits.
type SandboxActivity interface {
    Touch(ctx context.Context) error
}
```

- Add `SandboxActivity` to the `Sandbox` composite (`Sandbox = … + SandboxActivity`); `realSandbox`
  implements it by calling `Touch` and discarding the result. `roles_boundary_test.go` stays green
  because the composite still equals the role union.
- Add `Touch` to the mock (`msbtest.MockSandbox`), with a callback field and a default that
  succeeds.

### 2. Replace `keepVMAlive`

```go
// keepVMAlive touches the VM at half the idle timeout so the runtime's idle
// monitor does not stop it while the caller holds it, and reports the last touch
// error at release so a keepalive that stopped working is visible.
func keepVMAlive(ctx context.Context, sb msb.SandboxActivity, idleTimeout time.Duration) func() error
```

- Interval `= clamp(effectiveIdleTimeout/2, keeperMinInterval=1s, keeperMaxInterval=2s)`. The cap
  is the load-bearing part: an existing VM keeps its creation-time idle timeout, which may differ
  from this run's `auto-stop-timeout`, so deriving a large interval from the run's value (say the
  user raised it to 10m) could outrun the VM's live ~10s timer and fail to hold it. Capped at 2s,
  the keeper holds any live timeout comfortably above ~2s regardless of drift. An idle timeout at or
  below the 1s floor cannot be held reliably; that is accepted (the runtime's own timer granularity
  is coarser anyway).
- The loop touches on the ticker, tolerating a transient error but remembering the last one; the
  stop func returns the last error, matching the current `func() error` signature. `waitQuiescent`
  and `runServeOnly` **log that error at Verbose on release** - today both discard it
  (`_ = keeperDone()`), so the design's observability claim needs the one-line change to be true.
- The effective idle timeout is threaded to both callers from `RunOptions.IdleTimeout` (defaulted
  through `options.DefaultVMIdleTimeout`) rather than read from a global. The interval uses it only
  as an upper bound through the 2s cap, so a stale value cannot defeat the keeper.

### 3. The `max-lifetime` bound

- `options.RunOptions` gains `MaxLifetime time.Duration`, built from a new config key `max-lifetime`
  (a human duration, parsed like `auto-stop-timeout`); unset means unlimited (zero).
- `msb.SandboxSpec` gains `MaxDuration time.Duration`; `translate.go`'s `sandboxOptions` maps a
  non-zero value to `WithMaxDuration`.
- `vm.createProjectVM` sets `spec.MaxDuration = opts.MaxLifetime`. It does **not** join reconfig or
  a fingerprint: like the idle timeout, it applies at creation and a change takes effect on the next
  VM creation.
- Validation mirrors the SDK's `validateLifecycleLimits`: a **set** `max-lifetime` must be positive
  and a whole number of seconds (the SDK rejects a negative or sub-second `MaxDuration` with
  `ErrInvalidConfig`); unset is unlimited. It joins `auto-stop-timeout`'s validation so a bad value
  fails at config load rather than at create with an opaque error. Document that it counts from VM
  creation, not from last activity, and that a bound below the idle timeout effectively caps the
  session.

### Recorded decisions

- **Ping** — declined: agentd readiness is implied by the sandbox status and weaker than the
  application-level probes.
- **RequestDrain / WaitUntilStopped / WaitForStatus** — declined: no drain workflow, and `Stop`
  already waits.
- **Pause / Resume** — declined: pause-on-idle needs a resident supervisor the launcher lacks; no
  command surface; no path produces a paused VM.

## Spec and guide updates

- `specs/vm/detached-jobs.md`: replace the "Open risk in the sibling keepalive paths, verify in the
  same pass" paragraph with the Touch-keepalive contract (the keeper no longer relies on the idle
  aggregate), and drop the silent-exec assumption.
- `specs/vm/lifecycle.md`: the `ensure-vm`/`attach` rows note the `max-lifetime` creation option and
  the Touch keeper.
- `specs/vm/idle-timeout.md`: add `max-lifetime` as the second creation-time lifetime bound and its
  one-default rule (zero = unlimited).
- `guides/msb-sdk-integration.md` §1.2: fold in the adopted/rejected outcomes.
- `changelog.d/<PR-number>.<type>.md`: the keepalive switch (internal) and the new `max-lifetime`
  config key (added).

## Testing

- Unit (`session`): the Touch loop ticks at half the idle timeout, stops on cancel, returns the last
  touch error, and tolerates a transient failure; `waitQuiescent`/`runServeOnly` pass the effective
  idle timeout.
- Unit (`msb`): `realSandbox.Touch` delegates; the mock records touches.
- Unit (`vm`): `createProjectVM` maps a set `MaxLifetime` to `spec.MaxDuration` and leaves it zero
  when unset.
- Unit (`session`): the keeper interval clamps to `[1s, 2s]` for a large idle timeout; `waitQuiescent`
  logs the keeper's returned error on release.
- CLI (`cmd/agents-sandbox`): a config `max-lifetime` is parsed and validated (positive whole
  seconds, unset = unlimited, a sub-second or negative value rejected at load); `--dry-run-vm`
  reports it.
- Real VM (`msb` tag / KVM host): a quiet VM whose last client detached stays alive while a Touch
  keeper runs and stops after the keeper ends; a VM created with a short `max-lifetime` stops at it.

## Estimates

- `msb`: one role, one adapter method, one mock method. Small.
- `session`: rewrite `keepVMAlive`, thread the idle timeout through two callers. Small.
- `options`/`configpaths`/`vm`: the `max-lifetime` key, option, spec field, translate mapping,
  fingerprint, validation. Small-to-medium (the config-key wiring is the bulk).
- Docs/spec updates. Small.

## What is left to the builder

- The exact config key spelling (`max-lifetime` proposed) and its validation group beside
  `auto-stop-timeout`.
- Whether the keeper interval constants live in `session` or `options`.
- Confirm on a KVM host that `Touch` against a running VM resets the idle timer (the whole design
  rests on it), that a ~1-2s Touch cadence holds a VM past the idle timeout, and that the cadence is
  not itself a load concern.

## References

- `.operator-shared/guides/msb-sdk-integration.md` §1.2
- microsandbox v0.8.3 `docs/sandboxes/lifecycle.mdx`, `sdk/go/core/sandbox.go` (`Pause`, `Resume`,
  `RequestDrain`, `Ping`, `Touch`, `WaitUntilStopped`, `WaitForStatus`), `options.go`
  (`WithMaxDuration`, `WithIdleTimeout`)
- `.operator-shared/specs/vm/detached-jobs.md`, `specs/vm/idle-timeout.md`, `specs/vm/lifecycle.md`
- `internal/sandbox/session/reaper.go`, `session/run.go`, `internal/sandbox/msb/sandbox.go`,
  `internal/sandbox/vm/vm_lifecycle.go`
