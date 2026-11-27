---
description: Assessment of the agents-sandbox ⇄ microsandbox Go SDK integration after the v0.8 migration (SDK 0.8.3): SDK capabilities the launcher still does not use, the surfaces it has declined (SSH/SFTP, the raw agent client, cloud-only services), where the adapter could adapt better, and where it can be simplified.
read_if: Evaluating the SDK v0.7→v0.8 work; planning a VM feature such as warm start, pause/resume, live resize, drain, metrics streaming, snapshots, or config inspection; reviewing `internal/sandbox/msb` (the seam, `roles`, `translate.go`, the drift register, the SDK boundary); looking for seam simplification; or weighing the SDK's SSH/SFTP or raw agent client (declined).
---

# Microsandbox SDK integration: capability map and adaptation review

The SDK line crossed to v0.8.3: `github.com/superradcompany/microsandbox/sdk/go` (with
`sdk/go/core v0.8.3`), and the adapter was rewritten for it in three commits — the bump
(`07c2cd1b`), the adapter migration (`f00c5e11`), and adapter error-path tests (`5dd99c90`) — landed
as #906, superseding Dependabot's earlier 0.7.16 → 0.8.1 bump.

This note records what the migration actually did, what SDK capabilities remain unused, where the
adapter could still adapt better, and what the migration left on the table for simplification.
Sources are the SDK's own primary docs and sources (`microsandbox@v0.8.3`, `go@v0.8.3`,
`go/core@v0.8.3`), the merged adapter, and the updated specs
(`specs/architecture/msb-seam.md`, `specs/vm/live-mount-readback.md`).

## What the v0.8 migration did

The migration keeps the seam's shape exactly: the launcher-owned role interfaces, request/value
types, and the reflection drift register are all retained; consumers are untouched. It absorbs the
source break inside `internal/sandbox/msb`:

- **Unified resource.** 0.8 removes `SandboxHandle`/`VolumeHandle` and returns a `*Sandbox` /
  `*Volume`. `realSandboxHandle` now wraps `*msbSdk.Sandbox` and captures its identity (`Info`) and
  persisted-config (`ConfigJSON`) observations when the handle is resolved; `realVolumeHandle` wraps
  `*msbSdk.Volume` and captures `Info`. The retained observations preserve the adapter's synchronous,
  context-free identity/config accessors.
- **`EnsureRunning` adopted.** `realSandboxHandle.ConnectOrStart` now calls the resource's
  `EnsureRunning` instead of the removed `ConnectOrStart`, keeping the boot-ownership read.
- **Status-gated `Connect`.** `Connect` reads live status and returns `ErrSandboxNotRunning` for a
  stopped sandbox, so a reconfig read of live VM state cannot boot a VM as a side effect.
- **Identity-bearing removal.** `Remove` delegates to the resource's `Destroy` rather than the
  name-based `RemoveSandbox`, so a same-name replacement fails closed
  (`specs/identity/sandbox-identity.md`).
- **Config inspection.** `sandboxConfigFrom` projects the new `SandboxConfigInspection` (image
  source, resources, labels); the image reference and root-disk size are decoded from the persisted
  `RootfsSource` JSON. `ConfigMounts`/`ConfigPorts` now descend into the inspection envelope's
  `spec` rather than the old `SandboxConfig` (`specs/vm/live-mount-readback.md`).
- **`BackendKind` → `ContextKind`.** The launcher's `BackendKind` enum and its consumers stay; the
  translation is now an explicit `ContextKind` switch (`ContextRemote` → the launcher's `cloud`
  spelling) instead of a raw cast.
- **Typed job enums.** `JobInfo.State`, `JobLogEntry.Source`, and the log-source filter are cast
  from the new named string types (`JobState`, `JobOutputSource`) onto the launcher's strings.
- **Unknown-not-zero.** Volume measurements read from `Info` are nil-safe: a field the provider did
  not report stays nil/zero rather than becoming a value.

Net: the migration is a mechanical, seam-preserving change. It adopts the resource object, the lazy
`EnsureRunning` preparation, the inspection envelope, and the context-kind rename, and it did not
take the opportunity to restructure the projection layer or add feature capabilities.

## 1. Capabilities the launcher still does not use

The migration added no feature capabilities; the whole list below remains open. Most already exist
in the v0.7 line the launcher left, so they do not depend on this bump. Only the items marked
**(0.8)** are specific to the new line.

### 1.1 Warm start: snapshots, checkpoints, fork, restore

The launcher boots every project VM cold and pays dockerd + agent-daemon readiness on each run
(`specs/vm/lifecycle.md`, `ensure-vm`/`setup-sandbox`). The SDK can capture a prepared sandbox and
spawn from it (`docs/sandboxes/snapshots.mdx`, `docs/sdk/go/snapshots.mdx`): `Sandbox.Snapshot`
creates a history, `Checkpoint` is one immutable capture, and `SpawnSandbox` starts a child from a
group head or exact checkpoint **(0.8)**. Disk snapshots boot fresh; full snapshots save memory and
running processes and resume execution; nested-VM snapshots capture an inner Docker VM. `Fork` /
`ForkMany` create copy-on-write children. A per-agent prepared baseline spawned per project VM is the
largest untapped win, and it composes with `setup-sandbox` rather than replacing the home volume or
`/workspace`, which a snapshot does not capture.

### 1.2 Idle and lifecycle: pause/resume, drain, ping/touch, wait, max-lifetime

`Pause`/`Resume` freeze RAM and resume instantly; the launcher's idle path stops the VM
(`specs/vm/idle-timeout.md`) and reboots on return, so pause is a candidate idle policy trading host
memory for an instant resume. `RequestDrain` stops accepting commands, lets in-flight ones finish,
then stops — the SDK's graceful counterpart to the launcher's `Stop` for detached jobs
(`specs/vm/detached-jobs.md`). `Ping` is a guest-agent health check and `Touch` a keepalive; the
launcher's readiness waits could use `Ping`. `WaitUntilStopped`/`WaitForStatus` replace polling, and
the `WithMaxDuration` create option, still unused, bounds a VM's total lifetime (the launcher sets
only `WithIdleTimeout`).

### 1.3 Live resize with tracked outcomes and pressure admission

`Modify` already grows CPUs/memory/root disk, but 0.8 adds `ModifyWithResizeTracking`,
`ResizeOperations`, `ResizeStatus`, `WaitUntilResized`, `WaitForResizeOperations`, with named outcome
types and a stricter memory-target admission that samples guest pressure before shrinking
(`docs/sandboxes/tuning.mdx`, `COMPATIBILITY.md` §4). The launcher's OOM response
(`internal/sandbox/resourceproposal`, `specs/resources/resource-limit-proposal.md`) proposes a raise
but still has no completion signal for it.

### 1.4 Metrics streaming, fleet metrics, sidecar

`Sandbox.MetricsStream`, `AllSandboxMetrics`, and the `msb-metrics` OTLP sidecar
(`docs/sandboxes/metrics.mdx`, `docs/observability/msb-metrics.mdx`). The launcher reads a single
`Metrics` point for OOM detection.

### 1.5 Storage diagnosis, recovery, compaction

`DiagnoseStorage`/`RecoverStorage` and the compaction result are unused beyond
`StorageUsage`/`PruneStorage`/`Compact`. `PruneStorage` reports per-object errors and shared-memory
attribution the launcher's `PruneReport` drops.

### 1.6 Filesystem and transfer surface

The seam's `SandboxFS` exposes six methods while the SDK offers `List`, `Stat`, `Copy`, `Rename`,
`RemoveDir`, `CopyFromHost`, `CopyToHost`, and streams (`docs/sdk/go/filesystem.mdx`). Copying a
patch out of an isolated workspace is not expressible today.

### 1.7 Create-time knobs and progress

Named `scripts`, `thp`, `guest_clock`, `nested_virt`, flat root disks with clone strategies, CPU
placement/placement profile, and published-port connection limits (`max_tcp_connections`,
`max_udp_connections`, `max_inbound_tcp_connections`, `tcp_accept_queue_size`;
`docs/sandboxes/overview.mdx`, `optimization.mdx`, `COMPATIBILITY.md` §5). `CreateSandboxWithProgress`
(typed `CreationProgressKind`/`PullEventKind`/`StartupPhase`) is also unused: the launcher renders
its own image-build/pull spinner and calls the plain `CreateSandbox`.

### 1.8 SSH, agent client, services — declined (#922)

`SSH()`/SFTP and the raw agent client (`ConnectAgentSandbox`, `AgentClient`) exist and are unused;
`Service` URLs are cloud-only (`ErrCloudOnly`). All three are declined for this launcher:

- **SSH/SFTP as a transfer path — decline.** The SDK filesystem surface already carries
  `CopyFromHost`/`CopyToHost`, `List`/`Stat`, `Copy`/`Rename`/`RemoveDir`, and
  `ReadStream`/`WriteStream`; SFTP reaches the same files through an SSH session plus a second
  connection, adding a transport and credential lifecycle for no capability gain. The transfer
  capability belongs to the filesystem ticket (#920), which owns `SandboxFS`; SFTP must not be
  reopened as a second path to it.
- **SSH `Attach` as an attach transport — decline.** `SSHClient.Attach` takes only a terminal name
  and detach keys; the launcher's `AttachWith` contract carries a command, `cwd`, `user`, and `env`,
  and is used for interactive sessions and startup-hook commands alike (`session/run.go`,
  `vm/setup_sandbox.go`). SSH also reports a signal death as `128+signal`, which
  `AttachEndedAbnormally`'s guest `exit -1` detection would miss, and it opens the login shell
  rather than an arbitrary command. No robustness win justifies re-covering the attach surface with
  an SSH layer.
- **Raw agent client — decline.** `ConnectAgentSandbox`/`AgentClient` speak microsandbox's `agentd`
  CBOR frames — the layer beneath the SDK's typed `Shell`/`Exec`/`ShellStream`/`Attach`/`FS`
  wrappers the launcher already uses — and offer no capability those lack. They do not touch the
  notify seam, which reads the harness's own SSE stream over an in-guest `curl`, not `agentd`.
- **`Service`** stays out: cloud-only.

The one separable angle — making a launcher-managed VM reachable by standard tooling
(`scp`/`rsync`/editors) through the *external* SSH server (`PrepareServer`/`msb ssh serve`) — is a new
command surface and host listener, not SDK-client adoption, and is left as its own future decision.

## 2. Where further adaptation is possible

The migration settled the mechanical break but left several places where the adapter is thinner or
thicker than it could be.

### 2.1 Explicit contexts and collections are still bypassed

The adapter still uses the package-level functions (`msbSdk.GetSandbox`/`CreateSandbox`/
`ListSandboxesWith`, `msbSdk.GetVolume`/…) rather than a `LocalContext` and its
`Sandboxes()`/`Volumes()` collections, and it never uses `GetRunningSandbox`/`Sandboxes.GetRunning`
or `ConfiguredContext()`. Those exist to make destination selection explicit and to keep related
operations on one provider — the same reason the launcher pins its private `MSB_HOME`. Adopting a
`LocalContext` would make the launcher's destination explicit rather than an ambient environment
variable, and would make the handle classes' captured-observation approach a deliberate choice
rather than a workaround for the missing context.

### 2.2 The captured-observation trade-off

Because the seam interface is synchronous and 0.8's identity/config reads are context-bearing, the
migration snapshots `Info`/`ConfigJSON` when a handle is resolved and keeps them. This is a sound
choice, and it retains a failed config read on the handle so a run degrades to "config unreadable"
rather than failing the lookup. The cost is that `Status()`, `Image()`, `Config()` never refresh and
can be stale relative to the live VM, and every read-back carries per-handle state. `Info`/`Status`
are live reads in the SDK; a caller that genuinely needs current state has no seam method for it.
That is worth a deliberate decision (the seam spec documents the degrade path) rather than an
accidental contract.

### 2.3 The mounts/ports read-back is now a documented non-goal, but still hand-rolled

`specs/vm/live-mount-readback.md` states the new contract: the inspection envelope's
`spec.mounts` and `spec.network.ports` are untyped raw collections, so the launcher keeps its own
`ParseConfigMounts`/`ParseConfigPorts` decoder (internally tagged `VolumeMount`, snake_case fields,
Rust variant spellings, `directory`→`dir` remapping). The removal of the old `mountConfigFrom`/
`mountConfigsFrom`/`mountOwnerFrom` projections is a genuine simplification; the raw decoder
remains because the SDK still does not expose a typed mount/port model. That is correct as stated —
the further adaptation, if any, belongs upstream.

### 2.4 Typed errors are still not folded

The six `Is*` predicates in `msb/errors.go` wrap `msbSdk.IsKind`, and `IsJobNotRunning` matches the
bare string `job_not_running` because the SDK exposes no typed error for it. The SDK already carries
typed kinds the launcher does not fold (`ErrSandboxNotRunning` — now produced by the migrated
`Connect` — `ErrSandboxStopTimedOut`, `ErrSandboxStillRunning`, `ErrModificationNotConfirmed`), and
0.8 adds `ErrResizeWait`/`ErrResizeTimeout` (with `*ResizeWaitError` carrying a reason) plus the
snapshot-recovery kinds. Exposing seam errors as sentinels would remove the predicates and the
string match — and the migration now manufactures `msbSdk.Error{Kind: ErrSandboxNotRunning}` by
hand, which is a signal that a launcher-owned sentinel is the cleaner contract.

### 2.5 New-line-only items not taken

The resource/collection model (§2.1), `SandboxConfigInspection`'s `unavailable`/`redacted` lists (the
launcher projects `Spec` and marks the bookkeeping `notModeled`), typed enums (only cast to
strings), and the lean `sdk/go/local` package are all available and unused. The inspection's
explicit unknown/redacted modelling is the same principle the adapter applies to volume `Info`; the
launcher does not surface it.

## 3. Simplification opportunities (unchanged, and now larger)

The migration grew the adapter rather than shrinking it: it added five drift-register pairs
(`sandboxConfigInspection`, `sandboxInspectionSpec`, `sandboxResourceInspection`, `volumeInfo`,
`sandboxInfo`, each with a `notModeled` reason map), new raw-JSON decode helpers
(`decodeInspectionRootfs`, `persistedRootDisk`, `imageFromInspection`), and cast helpers, while
keeping the launcher-owned interfaces and value types unchanged in shape. The ~3,494-line adapter and
its ~397-line register are still the dominant cost.

### 3.1 Collapse the value-projection layer

Roughly 25 of `translate.go`'s functions are field-for-field `XFrom` projectors over ~24 SDK
structs, backed by the launcher value-type declarations and the reflection drift register. Many
projected fields are never read by a consumer (`ImageLayer` fields, most `Metrics` counters,
`JobInfo.RuntimeBootID`, `DiskCompactionResult` timing fields). Options, in increasing order of
change: shrink each projection to the fields consumers read; return the SDK value directly for
observation-only structs (`Metrics`, `JobInfo`, `StorageReport`, `ImageLayer`, `LogEntry`) and keep
launcher types only where behavior differs; or replace the reflection drift test with the compiler by
letting the SDK type flow. The migration makes this harder to defer: it added registers for the
inspection/resource/`Info` structs specifically to keep the projection complete.

### 3.2 Delete the duplicate factories

`msb.Mount.Bind/Named/Tmpfs` duplicate `msbSdk.Mount.*` and convert back through them, and the
`Secret.Env` factory is reimplemented in both `internal/sandbox/msb` and
`internal/sandbox/envsecret`. Where a launcher factory adds no policy, the SDK factory can be the
only builder. (The migration removed the read-back `mountConfigFrom`/`mountOwnerFrom`, so the
remaining mount duplication is only on the creation side.)

### 3.3 Reconsider the role split

Three composites over 15 roles, with deliberate overlap (`Shell` in `SandboxExecutor`/
`SandboxStreamer`; `Metrics`/`Logs` in `SandboxResourceControl`/`SandboxObserver`; `Destroy` in two
lifecycle roles). Most consumers still take the composite (`vm`, `prepare`, `pruning`, `volume`,
`runtimemigration`), and several roles are single-consumer. The migration adds more delegation seams
to the same structure; the invariant machinery (`roles_boundary_test.go`) still outweighs the
narrowing win so far.

### 3.4 The test seam surface

Dozens of package-level function-pointer variables exist so unit tests can replace SDK method
expressions without the native library, plus the `imageSpoolFile` interface and the
`Get`/`skipRuntimeInstall`/`IsRealClient` globals. The migration replaces handle method expressions
with resource ones and adds more (`realSandboxName`, `realSandboxID`, `realSandboxInfo`, …). Worth
weighing against moving these tests to the `msb`-tagged suite that already exercises the real client.

### 3.5 Status classification

`GetVMStatus` maps the SDK status string to a launcher class with a fail-closed unknown branch. The
migrated reader keeps the mapping on the captured `Info.Status`; a small semantic status value on
the seam would collapse the mapping and its unknown branch.

## 4. State and next steps

- The v0.8 migration has landed as #906 (`changelog.d/906.changed.md`), including its refinement in
  `internal/sandbox/msb` — the identity-bearing `Remove` via `Destroy` and the volume-orphan cleanup.
  The launcher-owned seam and its live-VM read-back behaviour are unchanged.
- The capability work in §1 is independent of the SDK line — nearly all of it was available on the
  v0.7 pin — so it can proceed as feature work. The most valuable single item is the warm-start
  snapshot/fork path.
- The simplification work in §3 is orthogonal to the migration and was explicitly not taken; if it is
  wanted, do it as its own change so the projection reduction and its drift-register shrink stay
  reviewable.

## References

- The v0.8 migration (#906): `07c2cd1b` (bump to 0.8.3), `f00c5e11` (adapter migration),
  `5dd99c90` (error-path tests).
- `.operator-shared/specs/architecture/msb-seam.md` — the `#906` status bullet and the
  "SDK line" paragraph.
- `.operator-shared/specs/vm/live-mount-readback.md` — the mounts/ports read-back contract
  and its non-goal.
- `microsandbox@v0.8.3/docs/migrations/v0.8.mdx`, `docs/changelog/v0.8.0.mdx`, `COMPATIBILITY.md`.
- `microsandbox@v0.8.3/docs/sandboxes/{lifecycle,snapshots,tuning,metrics,volumes,overview}.mdx`,
  `docs/sdk/{overview,setup,storage}.mdx`, `docs/sdk/go/{sandbox,snapshots,execution,filesystem}.mdx`.
- `sdk/go@v0.8.3/aliases.go`, `sdk/go/core@v0.8.3/config_inspection.go`.
