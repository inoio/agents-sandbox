---
description: Contract for the launcher's package boundaries - `internal/cliout` stays domain-free (generic rendering only) with the sandbox-shaped renderers and report DTOs in `internal/view`, and the run orchestration lives in `internal/sandbox/prepare` with the layering `cmd` → `session` → `prepare` → `vm` and no shared mutable stage `State`.
read_if: Moving a Printer, report-JSON DTO, or renderer between `internal/cliout` and `internal/view`, or run-preparation code between `internal/sandbox/prepare` and `internal/sandbox/vm`; deciding where new list/inspect/report rendering or run/shell stages belong; or proposing a shared context/`State` object for the stages.
---

# Package boundaries

## CLI output boundary

`internal/cliout` is the reusable command-output core and must stay importable by any package, including the sandbox domain packages. The domain-shaped code lives in `internal/view`.

- `internal/cliout` imports no `internal/sandbox/...` package. It keeps `Mode`/`ResolveMode`, `WriteJSON`, `Printer[T]`/`PrintList`, and `RenderPruneReport` — the last switches between a caller's human renderer and a prebuilt JSON value, so it is domain-free despite the name.
- `internal/view` holds the domain renderers: `SandboxPrinter`/`ImagePrinter`/`VolumePrinter` (the `cliout.Printer` implementations, including digest truncation and the volume SIZE-column rules) and the prune/compact report JSON DTOs with their constructors.
- `internal/cliout/boundary_contract_test.go` enforces the rule by reading the package's own non-test imports, so a new domain import fails the suite rather than shipping.
- `internal/view` is the launcher's presentation of the domain, imported only by `cmd/agents-sandbox` today. A renderer for another domain belongs there, not in `cliout`.

## Run orchestration layer

The layering is `cmd` → `session` → `prepare` → `vm`.

- `internal/sandbox/prepare` owns the run stages (`PrepareSandbox`), the `--dry-run` preview (`PreviewSandbox`), and the decisions only they make: the agent version and upgrade prompt, the reconfig decision and home action, fingerprint persistence, the shared-git-dir mount. `cmd`'s `build` uses `prepare`'s `ResolveAgentVersionForBuild` so `build` and `run` share one precedence rule.
- `internal/sandbox/vm` keeps the project-VM capabilities and exports the ones `prepare` calls: `FindProjectVM`, `ProjectVMName`, `ProjectVMHomeVolume`, `EnsureProjectVM` (with `VMBoot` and its `ProjectVMCreation` request), `SetUpSandbox`, `AdoptLegacyIdentity`. `ProjectVMCreation.Seams` is a test seam so tests outside `vm` can fail `ensure-vm` at its lock without mutating package state.
- Identity adoption stays in `vm`: `StopProjectVM`/`KillProjectVM` call it, and it needs `FindProjectVM` and the handle destroy, so a separate package would form an import cycle.
- `session` is not extended with the stages. It owns attach, the client lease, the reaper and the host opener, and calls `PrepareSandbox` through a test seam.
- The selected agent is resolved once, at `runoptions.Build`, into an immutable `agent.Resolved` carried on `options.RunOptions`; `prepare`, `session`, and `reprovision` read capabilities off it rather than re-looking-up the profile. `options` imports `agent`; `agent` does not import `options`.

### Rules

- A decision a stage makes belongs in `prepare`; an action on a VM that more than one caller needs belongs in `vm`. New code that needs an unexported `vm` function from `prepare` exports it deliberately rather than moving the decision into `vm`.
- No shared mutable state between stages: a new stage takes its inputs as parameters and returns a typed result. A shared mutable `State`, whose fields every stage reads and writes, is rejected — it hides a stage's real inputs and lets any stage depend on any field.
