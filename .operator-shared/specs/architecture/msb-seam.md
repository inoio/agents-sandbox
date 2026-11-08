---
description: Contract for the `internal/sandbox/msb` seam - it is the launcher's port over the microsandbox SDK, not an SDK mirror; provider-owned role interfaces composed into preserved composites, launcher-owned option/value types on the way in and out, the consumer narrowing rule, the mock strategy, and the upstream-sync guards.
read_if: Changing `internal/sandbox/msb` interfaces, its roles/composites, the mock, or how a consumer package depends on it; working on issue #196 (narrowing the adapter) or its follow-ups (#22); or deciding whether a new consumer should accept a role or the composite.
---

# The msb seam

`internal/sandbox/msb` is the launcher's abstract port over the microsandbox SDK
(`github.com/superradcompany/microsandbox/sdk/go`). It exists because there are two adapters behind it
(the real SDK client and the hand-rolled mock) and because the SDK has identity/no-op edge cases the
port normalizes (`ConfigMounts`/`ConfigPorts` hide an upstream `UnmarshalJSON` defect;
`IsUnsupportedOperation`/`IsReplaced`/`IsNotFound`/`IsJobNotRunning` fold error classes). It is **not**
meant to mirror
the SDK: the interface method set is exactly what callers use, and SDK types must not be the seam's
currency.

Issue #196 narrows the adapter to that contract. Phase 0 (dead surface + the `realVolumeHandle`
silent-zero fix), Phase 1 (role interfaces), Phase 2 (launcher-owned option types), Phase 3
(launcher-owned value types and lifecycle enums), Phase 4a (the launcher-owned creation model) and
Phase 4b (the launcher-owned read-back config model) have all landed, followed by the runtime-residue
follow-up that re-homed the runtime install/version surface behind `msb`. The SDK is now an
implementation detail of `internal/sandbox/msb` alone, with no exceptions. The sections below are the
durable contract, stated as the end state; the "Status" note at the end records what is already true.

## Roles and composites

Provider-owned role interfaces live in `msb` beside the composites. Each composite **embeds** its
roles; `composite method set == union of role method sets` is the invariant, pinned by
`roles_boundary_test.go`. The concrete adapters satisfy each role directly, so a caller narrows by
naming the role and needs no glue.

```
Client        = RuntimeInstaller + SandboxStore + VolumeStore + ImageStore + StorageStore

SandboxHandle = SandboxIdentity + SandboxLifecycleHandle + SandboxConfigReader
                + SandboxResourceControl

Sandbox       = SandboxExecutor + SandboxStreamer + SandboxAttacher
                + SandboxJobs + SandboxObserver + SandboxLifecycle
```

Role membership overlaps on purpose (`Shell` in `SandboxExecutor`/`SandboxStreamer`; `Metrics`/`Logs`
in `SandboxResourceControl`/`SandboxObserver`; `Destroy` in `SandboxLifecycleHandle`/`SandboxLifecycle`)
— Go collapses identical methods in the composite.

**Consumer narrowing rule.** A signature narrows to a role only where the function's real need *is*
the role; a function that genuinely spans roles keeps `msb.Sandbox`. The win is per-caller honesty,
not an all-at-once rewrite, so `vm`/`pruning`/`prepare` may keep the composite.

**One sanctioned consumer-owned interface:** `notify.StreamOpener` (a two-method, SDK-option-free
shape with its `SandboxStreamOpener` adapter). Its adapter's underlying field is `msb.SandboxStreamer`;
`Watch`'s signature is unchanged. Consumer-owned interfaces are otherwise *not* the pattern — the
provider-owned split avoids an adapter per consumer.

## Launcher-owned types

- **Options in.** The interfaces take launcher-owned request structs (`SandboxSpec`, `VolumeSpec`,
  `ExecRequest`, `AttachRequest`, `JobSpec`/`JobQuery`, `LifecycleOptions`, `LogQuery`,
  `CompactionRequest`, …), never `...msbSdk.*Option`. The adapter owns one translation function per
  family, calling the SDK `With*` constructors it maps. A launcher struct that merely embeds or
  returns an SDK option is an anti-goal.
- **Values out.** The interfaces return launcher-owned value types, not `*msbSdk.Metrics`,
  `*msbSdk.StorageUsageReport`, `*msbSdk.ImageConfig`, `msbSdk.JobInfo`, enum types, etc.
  `ImageDetail`/`ParseConfigMounts`/`ParseConfigPorts` are the existing model; Phase 4b added the
  read-back `SandboxConfig`/`RootDiskConfig`, `ModifyOptions`/`SandboxModificationPlan`/
  `ModificationConflict`, `DiskCompactionResult`/`DiskCompactionDiskResult`, and the
  `ModificationPolicy` string enum. `ConfigMounts`/`ConfigPorts` now return the launcher `MountConfig`/
  `PortBinding`. Aliases that must stay distinct from an SDK enum keep their values verbatim (e.g.
  `msb.SandboxStatus` string-mirrors `msbSdk.SandboxStatus` so `string(x)` and `==` keep working), and
  the sync guards below cover them.
- **Target.** No production file outside `internal/sandbox/msb` imports `msbSdk`, pinned by
  `phase4_boundary_test.go` (no residue: the boundary has zero exemptions). `storage`, `pressure`,
  `resourceproposal`, `image` and `session` were the first to drop it (`phase3_boundary_test.go`);
  `reconfig`, `prepare`, `vm`, `volume` and `pruning` dropped it with the Phase-4b read-back model;
  `runtime` dropped it with the runtime-residue follow-up, which moved its
  `SDKVersion`/`RuntimeConfig`/`EnsureRuntime`/`InstallRuntime` use behind `msb`'s
  `PinnedRuntimeVersion`/`EnsureRuntime`/`InstallRuntime` wrappers.

## Upstream sync guards

The launcher types are a curated projection of the SDK, kept honest by three guards:

1. **The compiler** — adapter `With*` calls and field reads fail to build on an SDK rename, signature
   change, or removal.
2. **A reflection drift test** — for every SDK struct the interface exposes, a contract test reflects
   over its fields and asserts each is either projected by the launcher type or listed in a
   reason-bearing `notModeled` allowlist beside the translator (unexported fields get a "set
   internally" reason). A new upstream field fails the test on the dependabot SDK bump — the moment
   to decide whether to wire the capability. The guard is field-level, not option-level.
3. **A documented residue** — `JobExecOption`/`JobListOption` and the `Stop`/`Kill`/`Destroy` options
   mutate unexported SDK structs (`jobExecConfig`, `lifecycleOptions`, `destroyOptions`), so
   reflection cannot see them; they reduce to plain scalars (`JobSpec`, `JobQuery`,
   `LifecycleOptions`) and drift there is compiler-only. Record this limitation in the drift test's
   doc comment.

The drift guard is reflection-based by decision: a `golang.org/x/tools/go/packages` scan of the SDK's
`With*` constructors would be complete but adds a module dependency, against `AGENTS.md`.

## The mock

`testmock.go`'s `MockMsbClient` stays the wide composite double the suite already uses, with **all
callback/collection/recorder fields on the outer struct** — a promoted field from an embedded type
cannot be set in a composite literal, and ~100 test sites build `&msb.MockMsbClient{…}` directly, so
moving fields into embedded roles would break every literal. Standalone role doubles
(`MockImageStore`, `MockSandboxStore`, `MockStorageStore`, `MockSandboxExecutor`, …) live in
`testmock_roles.go` and are used by tests that need the small double; they are never embedded in
`MockMsbClient`. The central mock therefore shrinks only as consumers migrate, not all at once
(follow-up #22 relocates whatever remains into a test-support package). `failFastMsbClient` keeps its
role. The mock files carry a `//deadcode:ignore` marker because they are referenced only by `_test.go`
files.

## Status

- **Landed (#196 Phase 0):** six dead adapter methods deleted (`Sandbox.Stop`, `Job.ID`,
  `SandboxFS.Stat`/`List`/`ReadStream`, `VolumeHandle.IsDefault`; docker `ImageTag`/`ImageRemove`);
  `CreateVolume` resolves the created volume through `getVolume` so `realVolumeHandle` wraps one
  `*msbSdk.VolumeHandle` with direct delegation (no silent-zero type switch).
- **Landed (#196 Phase 1):** the roles and composites above; `roles_boundary_test.go` pins
  composite == role-union; the standalone role doubles; `notify`, `image`, `reprovision` and the
  narrowing-capable `session` helpers accept roles (`storage` needed none — its only contact was an
  internal seam; `resourceproposal.MemoryReads` was already narrow).
- **Landed (#196 Phase 2):** the dependency-free option families flow as launcher-owned request
  types — `Shell`/`Exec`/`ShellStream` take `ExecRequest`, `AttachWith` takes `AttachRequest`,
  `ExecDetached` takes `JobSpec`, `ListJobs` takes `JobQuery`, `Stop`/`Kill`/`Destroy` take
  `LifecycleOptions`, `Logs` takes `LogQuery`, `Compact` takes `CompactionRequest` — with the
  adapter translating each through `translate.go` (defaults omitted, so a zero request emits no
  option). `resourceproposal.MemoryReads` follows the `SandboxObserver.Logs` shape.
- **Landed (#196 Phase 3):** the interfaces now return launcher-owned value types:
  `*StorageReport`/`StorageCategory`/`StorageItem` and `PruneRequest`/`PruneReport` (`StorageStore`);
  `JobInfo`/`JobLogEntry`/`JobLogQuery`/`LogEntry`/`Metrics` (`SandboxJobs`, `Job`, `SandboxObserver`,
  `SandboxResourceControl`); `ImageConfig`/`ImageLayer`/`ImagePruneReport` (`ImageStore`, and the
  `ImageDetail` fields); and the string-backed lifecycle enums `SandboxStatus`/`BackendKind`/
  `VolumeKind` (`SandboxIdentity.Status`/`BackendKind`, `VolumeHandle.Kind`). The `JobLogOptions` and
  `StoragePruneOptions` inputs joined the reflection register. `realVolumeHandle` wraps the SDK
  volume handle, translating only `Kind()`. `Modify`/`msbSdk.ModifyOptions` and the compaction result
  were still SDK types until Phase 4b.
- **Landed (#196 Phase 4a):** the creation path no longer speaks the SDK.
  `CreateSandbox(name, SandboxSpec)` and `CreateVolume(name, VolumeSpec)` take launcher-owned specs;
  `translate.go` gained `sandboxOptions`, the mount/port/secret/network translators (`networkConfig`
  composes gateway-DNS → deny → profile → allow), and `volumeOptions`/`volumeKindTo`. New value
  types: `SandboxSpec`/`VolumeSpec`, the mount model (`MountConfig` + the `Mount` factory for
  bind/named/tmpfs; disk/owned descriptors come from read-back), `PortBinding`/`PortProtocol`,
  `SecretEntry`/`SecretSubstitution`/`SecretEnvOptions`/`ViolationAction` + `Secret.Env`, and
  `NetworkSpec`/`NetworkProfile`. `vm` project-VM creation, the `volume`/`runtimemigration` create
  paths, and the `network`/`envsecret`/`ports`/`state/fingerprint` producers are SDK-free. The
  `SandboxConfig`/`VolumeConfig` inputs joined the reflection register.
- **Landed (#196 Phase 4b):** the read-back path no longer speaks the SDK.
  `SandboxConfigReader.Config` returns the launcher `SandboxConfig`, `ConfigMounts`/`ConfigPorts`
  return launcher `MountConfig`/`PortBinding` (translated by `sandboxConfigFrom`/`mountConfigFrom`/
  `portBindingsFrom`), and `SandboxResourceControl.Modify`/`Compact` take `ModifyOptions` and return
  `SandboxModificationPlan`/`DiskCompactionResult` (translated by `modifyOptions`/
  `modificationPlanFrom`/`diskCompactionResultFrom`). `ParseConfigMounts`/`ParseConfigPorts` build
  launcher values directly. The `ModifyOptions`/`SandboxModificationPlan`/`DiskCompactionResult`/
  `DiskCompactionDiskResult` structs joined the reflection register. Consumers migrated:
  `reconfig/plan`, `prepare/reconfig`, `pruning/compact`, `vm/inspect`/`vm_resources`/`vm_name`,
  `volume/operations`, `image/references`, `ports.ResolveServeHostPort` (and the transitional
  `ports.BindingsFromSDK` was removed). `msb.IsJobNotRunning` re-homed `vm/detached`'s `JobError`
  check, so `vm` is SDK-free. `phase4_boundary_test.go` pins the whole-module end state.
- **Landed (#196 runtime-residue follow-up):** the last exemption is gone. `msb` gained
  `PinnedRuntimeVersion()`, `EnsureRuntime(ctx)` and `InstallRuntime(ctx)` (in `msb.go`, reusing the
  private-home `runtimeConfig()` seam), and `realMsbClient.EnsureInstalled` delegates to
  `EnsureRuntime`. `internal/sandbox/runtime` retargets its `ensureRuntime`/`installRuntime` seams at
  those wrappers, drops its `msbSdk` import and `runtimeConfig()`, and `phase4_boundary_test.go` scans
  with zero residue, with `TestSDKBoundaryDetectsAnImporter` as the positive control.
