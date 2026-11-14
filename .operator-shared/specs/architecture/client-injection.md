---
description: Contract for resolving the external clients (msb, docker, config-paths) once at an entry point and threading them explicitly, replacing the remaining process globals after #345; the four behavior test seams as one injected struct on RunOptions; the process-root configpaths.Get() reads that stay global by design and the project-config trust gate's two process-global installs; and the two deliberate cross-layer edges (viperconfig→sandbox/mounts+network, notify→sandbox/state+msb) alongside the removed options→state edge.
read_if: Changing a docker.Get()/configpaths.Get() call site, image.EnsureImage*/EnsureLoaded/Build/PreviewImage or the image render helpers, adding a client or path resolver parameter to a run/build/doctor/prune entry point, or the image build's agent-release resolver; changing the project-config trust gate's `configpaths.Use` factory swap or `projectguard.Install` guard; or deciding whether a cross-layer dependency (options, viperconfig, notify) is a deliberate exception.
---

# External client injection

## Contract

The launcher has three external dependency families with a process-global
default factory: the microsandbox client (`msb.Get()`), the Docker client
(`docker.Get()`), and the host path resolver (`configpaths.Get()`). At
`777b0fd0` the two latter families were read at 10 non-test `docker.Get()` sites
and 16 non-test `configpaths.Get()` sites. The rule is the same for all three:

- **Resolve once at an entry point.** The command boundary (`cmd/agents-sandbox`)
  resolves the factory and passes the value down. Where a run/build/prune/doctor
  operation has no cmd-level param (because the parameter would have to thread
  through `session.Run`, a sibling-owned signature), the highest internal entry
  point resolves it instead: `prepare.PrepareSandbox`/`PreviewSandbox` call
  `msb.Get()`, `docker.Get()` and `configpaths.Get()` once (hoisted at the top of
  the function, never re-read below), matching the #345 precedent.
- **No nested reads.** A function below the entry point takes the value as an
  explicit parameter. `image.EnsureImageWithClient` and `image.EnsureLoaded` are
  the model: both take the Docker client (and `EnsureLoaded` the msb client too),
  so the two halves of the image-ensure path agree. The image reader and
  identity helper (`ReadProjectDockerfile`, `WarnIgnoredProjectConfig`,
  `RunnerDockerfileID`) take the `configpaths.ConfigPaths` instead of reading
  the global; the renderer and the diagnostics helper
  (`RenderProjectDockerfile`, `EmitProjectDockerfileDiagnostics`) take the
  project Dockerfile's bytes, which the boundary read once. So does
  `vm.StopProjectVM`/`KillProjectVM`, which now take the msb client as an
  explicit parameter from their `cmd` callers: no `msb.Get()` resolves below the
  command boundary there.

## The image build's release resolver

`image` must not import `"testing"`, and its agent-release resolution is
injected, not a package-global hook:

- `BuildOptions.ResolveVersion` (`image.AgentVersionResolver`) carries the
  resolver. `cmd`'s `agentVersionResolver` (for the build/volume commands) and
  `prepare.buildVersionResolver` (defaulting to the shared `agentLatestVersion`
  seam) supply it.
- `BuildOptions.resolveAgentVersion` falls back to `defaultAgentVersionResolver`
  when none is injected. Production always injects one; only `image`'s own tests
  override the package default (via `withMockAgentVersion*`).

## Residue

Three things sit outside the threaded clients: the behavior test seams — now injected rather than
globals — the process-root path lookups that stay global by design, and the project-config trust
gate's process-global installs.

**Behavior test seams (injected).** The four former exported hooks
(`vm.SetProjectVMLock`, `vm.SetDaemonHealthFunc`,
`prepare.SetAgentLatestVersion`, `prepare.SetRuntimeHomeMigrationOffer`) are one
injected struct of functions, `options.Seams`, carried on
`options.RunOptions` (#518). Its zero value is production: each consumer
(`projectVMLock`, `daemonHealth`, `agentLatestVersionFor`,
`runtimeHomeMigrationOffer`, and `prepare`'s `agentVersionResolverFor`) uses the
package default when its field is nil, so tests stop touching package state.
`cmd` threads the struct into the run stages through the boundary seam var
`runSeams` (the cmd→run route). Unlike the clients, these are behavior seams, not
per-operation dependencies.

**Process-root path lookups (by design).** `configpaths.Get()` stays at these
process-global roots, too numerous or too cross-cutting to thread:

- `state.stateRoot()` (`internal/sandbox/state/state.go`) — ~40 exported state
  functions reach it, with hundreds of call sites across
  vm/volume/prepare/pruning/session.
- `runtime.PrivateHome()`/`LegacyHome()` (`internal/sandbox/runtime/home.go`),
  `configpaths.MSBHome()`, and `runtime.sanitizeRuntimePath`
  (`internal/sandbox/runtime/runtime.go`, the `UserHomeDir` read that is not part
  of the PrivateHome/LegacyHome/MSBHome chain) — the microsandbox runtime home,
  reached from runtimemigration and doctor.
- `homeconfig.ResolveManifestSource` (`internal/homeconfig/homeconfig.go`) — the
  host `$HOME` fallback of the home manifest resolver.

These are the run's process-global roots, not per-operation clients; they are not
a threading backlog.

One `msb.Get()` remains below the boundary outside these roots: `session`'s
`resolveHomeVolumePath` (`internal/sandbox/session/host_opener.go`), the
host-opener home-volume probe reached from `session.Run` when `--open-links` is
on. It is not a per-operation client and is left for a later pass.

**Project-config trust gate (by design).** The gate that decides whether a
command may use the checkout's `.agents-sandbox` layer mutates two process-global
seams, both in `cmd/agents-sandbox/trust.go` and both reset by `execute` in
`cmd/agents-sandbox/cli.go` when the command ends:

- `configpaths.Use(withProject)` (`trust.go`) swaps the `configpaths.Get` factory
  for the command, so every config reader derives its project inputs from the
  gate's decision. The factory cannot be threaded: config resolution runs before
  any resolver the run stages receive (`specs/configuration/project-config-trust.md`).
- `projectguard.Install(roots, reads)` (`internal/projectguard`) installs the
  approved-bytes guard the config readers route their project reads through
  (`configread`, `filewalk`, `viperconfig`, `homeconfig`, `envsecret`,
  `configmerge`, `image`). It is global because those readers are leaf packages
  that hold no resolver.

Both are per-command and cleared before the process exits; the reset lives in
`execute`, not in the gate function (a `defer` inside the gate, which runs in the
root command's `PersistentPreRunE`, would fire before the command body and before
the reads the guard exists to verify).

## Deliberate cross-layer edges

Three dependency edges sit outside the client families above (#532). Two are
deliberate exceptions; the third is removed.

- **`options` does not import `state`.** The run-options aggregate's
  `Seams.ProjectVMLock` is keyed by `options.ProjectVMKey`, a dependency-free key
  type defined in `options`, so `options` stays clear of the state package's lock
  and state model. `vm` adapts it to `state.Key` in `vm.projectVMLock` where the
  creation flock is taken. `internal/sandbox/options/boundary_contract_test.go`
  enforces the rule by reading the package's own imports.
- **`viperconfig` imports `sandbox/mounts` and `sandbox/network` — deliberate.**
  Both are import-level leaf *value* packages: their production sources import no
  other `internal/` package, so no cycle forms and no sandbox behavior crosses.
  `viperconfig` consumes their types (`mounts.Mounts`, `network.Policy`,
  `network.Profile`) and their pure decoders and validators (`mounts.DecodeMounts`,
  `network.ParseProfiles`, `network.NormalizeDNSServers`), not sandbox behavior.
  Relocating either out of `sandbox/` is pure churn; the namespace mismatch is
  accepted.
- **`notify` imports `sandbox/state` and `sandbox/msb` — deliberate.** `notify` is
  presentation, but each domain touchpoint is a seam `session` injects and
  constructs in `internal/sandbox/session/run.go`: `StateClaimer` (the one-line
  `sandboxstate.AcquireClaim` behind the `claimer` interface) and
  `SandboxStreamOpener` (adapting `msb.Sandbox` to `StreamOpener`, which uses only
  `msb.StreamHandle` and `msb.ShellResult`). A later pass could move the two
  adapters into `session` and leaf-type `StreamOpener`; until then the edge is
  recorded here.
