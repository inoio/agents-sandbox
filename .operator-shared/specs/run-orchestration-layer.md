---
description: Decision record for the run orchestration layer (#454) - stages resolve-identity through setup-sandbox, the --dry-run preview and the decisions only they make live in internal/sandbox/prepare with narrow signatures, internal/sandbox/vm keeps the project-VM capabilities, and no shared mutable State carries the working set between stages.
read_if: Adding or moving a run/shell stage, a version/reconfig/home decision, or a VM capability between internal/sandbox/prepare and internal/sandbox/vm; deciding where new run-preparation code belongs; or proposing a shared context/State object for the stages.
---

# Run orchestration layer

Date: 2026-11-02. Issue #454, part of the lifecycle tracker #448.

## Context

Before #454, `internal/sandbox/vm` held three responsibilities: the capabilities that act on a project VM (find,
name, create, start, stop, set up, inspect), identity adoption, and the orchestration of a run (`PrepareSandbox`, the
preview, and the decisions behind them). #450 had already turned `PrepareSandbox` into an ordered stage list whose
steps hand typed results (`sessionIdentity`, `versionDecision`, `preparedHome`, `reconfigPlan`) to each other, so the
sequence was legible; the open question was whether the orchestration should also be its own layer.

An AST call graph of `vm`'s non-test files showed the boundary is clean. The orchestration files called a small set
of capabilities that other `vm` files also use (`ensureProjectVM`, `setUpSandbox`, `findProjectVM`, `projectVMName`,
`projectVMHomeVolume`, `vmBoot`), and a larger set that nothing but the orchestration used: the agent-version
decision (`upgrade.go`, `upgrade_migration.go`), the reconfig decision half of `reconfig.go`, fingerprint persistence
and the shared-git-dir mount. Only `reconfig.go` mixed both: `setUpSandbox` and its helpers are capability.

## Options

1. **One orchestrator over a shared mutable `State`.** Every stage reads and writes fields of one struct. It puts the
   whole lifecycle in one place, but it trades visible coupling for hidden coupling: a stage's real inputs disappear
   from its signature, and any stage can depend on any field. This is the context-object failure.
2. **Move the orchestration into its own package, keeping narrow signatures.** Each stage still names what it reads
   and returns what it produces; the package boundary makes the layering a compile-time rule.
3. **Leave it in `vm`.** The spec and the stage trace already make the sequence legible; the layering stays a
   convention that nothing enforces.

## Decision

Option 2. The orchestration lives in `internal/sandbox/prepare`; the layering is `cmd` → `session` → `prepare` →
`vm`.

- `prepare` owns stages 4–10 (`PrepareSandbox`), the `--dry-run` preview (`PreviewSandbox`), and the decisions only
  they make: the agent version and upgrade prompt, the reconfig decision and home action, fingerprint persistence, the
  shared-git-dir mount. `cmd`'s `build` uses its `ResolveAgentVersionForBuild` so `build` and `run` share one
  precedence rule.
- `vm` keeps the capabilities and exports the ones `prepare` calls: `FindProjectVM`, `ProjectVMName`,
  `ProjectVMHomeVolume`, `EnsureProjectVM` (with `VMBoot` and its `ProjectVMCreation` request), `SetUpSandbox`,
  `AdoptLegacyIdentity`. `SetProjectVMLock` is a test seam so tests outside `vm` can fail `ensure-vm` at its lock.
- Identity adoption stays in `vm`: `StopProjectVM`/`KillProjectVM` call it, and it needs `FindProjectVM` and the
  handle destroy, so a separate package would form an import cycle.
- `session` is not extended with the stages. It owns attach, the client lease, the reaper and the host opener, and
  calls `PrepareSandbox` through a test seam; keeping the stages in their own package keeps both focused.

The move kept names, signatures, stage ids and the trace unchanged; `cmd`'s `TestRunShellRecordTheLifecycleTrace` and
`prepare`'s `TestPrepareSandboxSkipVMRecordsStagesInOrder` pin the sequence across it.

## Consequences

- A decision a stage makes belongs in `prepare`; an action on a VM that more than one caller needs belongs in `vm`.
  New code that needs an unexported `vm` function from `prepare` exports it deliberately rather than moving the
  decision into `vm`.
- No shared mutable state between stages: a new stage takes its inputs as parameters and returns a typed result.
- Tests follow the code they exercise. A few small helpers (`opencodeAgent`, `seedHomeState`, …) exist in both
  packages' tests, and `prepare`'s tests stub the dockerd probe by its command string, which must match `vm`'s.
