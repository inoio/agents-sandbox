---
description: Contract for resolving the external clients (msb, docker, config-paths) once at an entry point and threading them explicitly, replacing the remaining process globals after #345; and the residue still resolved through configpaths.Get().
read_if: Changing a docker.Get()/configpaths.Get() call site, image.EnsureImage*/EnsureLoaded/Build/PreviewImage or the image render helpers, Add a client or path resolver parameter to a run/build/doctor/prune entry point, or the image build's agent-release resolver.
---

# External client injection

## Contract

The launcher has three external dependency families with a process-global
default factory: the microsandbox client (`msb.Get()`), the Docker client
(`docker.Get()`), and the host path resolver (`configpaths.Get()`). The rule is
the same for all three:

- **Resolve once at an entry point.** The command boundary (`cmd/agents-sandbox`)
  resolves the factory and passes the value down. Where a run/build/prune/doctor
  operation has no cmd-level param (because the parameter would have to thread
  through `session.Run`, a sibling-owned signature), the highest internal entry
  point resolves it instead — `prepare.PrepareSandbox`/`PreviewSandbox` call
  `msb.Get()`, `docker.Get()` and `configpaths.Get()` once, matching the #345
  precedent; `vm.StopProjectVM`/`KillProjectVM` resolve `configpaths.Get()` for
  identity adoption.
- **No nested reads.** A function below the entry point takes the value as an
  explicit parameter. `image.EnsureImageWithClient` and `image.EnsureLoaded` are
  the model: both take the Docker client (and `EnsureLoaded` the msb client too),
  so the two halves of the image-ensure path agree. The image render/identity
  helpers (`readProjectDockerfile`, `WarnIgnoredProjectConfig`,
  `RenderProjectDockerfile`, `EmitProjectDockerfileDiagnostics`) take the
  `configpaths.ConfigPaths` instead of reading the global.

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

## Residue (deliberately not threaded)

`configpaths.Get()` remains at a few process-root lookups whose call sites are
too numerous or too cross-cutting to thread in this change:

- `state.stateRoot()` — ~40 exported state functions reach it, with hundreds of
  call sites across vm/volume/prepare/pruning/session.
- `runtime.PrivateHome()`/`LegacyHome()` and `configpaths.MSBHome()` — the
  microsandbox runtime home, reached from runtimemigration and doctor.
- `homeconfig.ResolveManifestSource` — the host `$HOME` fallback of the home
  manifest resolver.

These are the run's process-global roots, not per-operation clients; a follow-up
may give them an injected resolver when their own call graphs are touched.
