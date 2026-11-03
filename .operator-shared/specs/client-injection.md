---
description: Contract for resolving the external clients (msb, docker, config-paths) once at an entry point and threading them explicitly, replacing the remaining process globals after #345; the four exported behavior test seams (to be injected as one struct on RunOptions), and the process-root configpaths.Get() reads that stay global by design.
read_if: Changing a docker.Get()/configpaths.Get() call site, image.EnsureImage*/EnsureLoaded/Build/PreviewImage or the image render helpers, adding a client or path resolver parameter to a run/build/doctor/prune entry point, or the image build's agent-release resolver.
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
  `vm.StopProjectVM`/`KillProjectVM`, which receive it from their `cmd` callers;
  only `msb.Get()` still resolves inside them.

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

Two families remain beyond the threaded clients.

**Behavior test seams (to be injected).** The four exported hooks
(`vm.SetProjectVMLock`, `vm.SetDaemonHealthFunc`,
`prepare.SetAgentLatestVersion`, `prepare.SetRuntimeHomeMigrationOffer`) mutate
package-level state; their resolution is one injected struct of functions carried
on `options.RunOptions`, defaulted to the production impls, so tests stop
touching package state. Tracked by **#518**. Unlike the clients, these are
behavior seams, not per-operation dependencies.

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
