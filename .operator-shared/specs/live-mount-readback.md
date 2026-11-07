---
description: Contract for reading a live project VM's mounts and published ports. The microsandbox Go SDK's `SandboxConfig.UnmarshalJSON` drops the persisted `mounts` and `network.ports`, so `cfg.Volumes` and `cfg.PortBindings` are always empty on a read-back; the launcher instead decodes the persisted config JSON through `msb.SandboxHandle.ConfigMounts()` and `ConfigPorts()`. Consumers — the reconfig tmp-size/workspace-quota/home-volume detectors, the published-ports detectors (`publishReconfig`, `ports.ResolveServeHostPort`, `prepare.existingServeHostPort`), home-volume recovery via `vm.ProjectVMHomeVolume` (fail-closed), and `sandbox inspect` — all read from those sources; `reconfig` and `prepare.liveHomeVolume` degrade rather than fail when it is unavailable.
read_if: Reading or changing how the launcher learns a live VM's mounts or published ports, or diagnosing an empty `cfg.Volumes` / `mounts (0)` / silently ignored `tmp_size`, `workspace-quota`, or `ports:` change, or a serve-only VM that reads as changed on every start (`internal/sandbox/reconfig/plan.go`, `internal/sandbox/ports`, `internal/sandbox/vm/inspect.go`, `ProjectVMHomeVolume`/`liveHomeVolume`).
---

# Live VM mounts and ports come from the persisted config JSON

## The gap

`SandboxHandle.Config()` decodes the sandbox record's `config_json` column through the Go SDK's
`SandboxConfig.UnmarshalJSON` (`sdk/go@v0.7.8`, `options.go`), which populates a `persistedSandboxConfig` that has
**no mounts field and no ports field**. `cfg.Volumes` and `cfg.PortBindings` are therefore nil for any config read
back from a live VM. Fields that do round-trip (`cfg.Image`, `cfg.RootDisk`, `cfg.Labels`, resources) are
unaffected, which is why `rootDiskChange` works and the `cfg.Volumes`/`cfg.PortBindings` readers do not.

The data is present upstream. `config_json` holds a serialized `SandboxConfig` whose `#[serde(flatten)]`ed
`SandboxSpec` carries `mounts: Vec<VolumeMount>` (`microsandbox-types`, `domain.rs`), serialized as an internally
tagged array — one entry per mount, keyed in the launcher by its `guest` path:

```json
"mounts": [
  {"type":"Named","name":"agents-sandbox-home-<slug>-<agent>-<ts>","guest":"/home/dev","options":{...}},
  {"type":"Bind","host":"<host repo>","guest":"/workspace","quota_mib":16384,"options":{...}},
  {"type":"Tmpfs","guest":"/tmp","size_mib":2048,"options":{...}}
]
```

This is exactly the shape `buildMounts` (`internal/sandbox/vm/vm_lifecycle.go`) creates, so the round-trip is
lossless for everything the launcher sets.

Published ports sit in the same record under `network.ports` — the `SandboxSpec`'s `NetworkSpec` carries
`ports: Vec<PublishedPortSpec>` — with snake_case fields and the protocol spelled as the Rust variant name:

```json
"network": {
  "ports": [
    {"host_port":4096,"guest_port":4096,"protocol":"Tcp","host_bind":"127.0.0.1"}
  ]
}
```

This too is exactly the shape `ports.PublishBindings` composes, so the round-trip is lossless for the bindings the
launcher sets.

## Contract

- The single source of truth for a live VM's mounts is `msb.SandboxHandle.ConfigMounts()`, backed by
  `msb.ParseConfigMounts(handle.ConfigJSON())`, and for its published ports it is
  `msb.SandboxHandle.ConfigPorts()`, backed by `msb.ParseConfigPorts(handle.ConfigJSON())`. `cfg.Volumes` and
  `cfg.PortBindings` must not be treated as populated on a read-back; a consumer that needs them applies
  `ConfigMounts()`/`ConfigPorts()` first (this is the one place the SDK gap is worked around). In `prepare`,
  `readLiveProjectVM` applies both (`applyLiveMounts`/`applyLivePorts`) before any detector sees the config.
- `ParseConfigMounts` keys by guest path and maps the five upstream variants onto the SDK's `MountConfig` through its
  `Mount` factories: `Bind` → `quota_mib` + `options.readonly`, `Tmpfs` → `size_mib`, `Named` → `name`, `Owned` and
  `DiskImage` for completeness. `Owned`'s `storage.kind` maps upstream's `"directory"`/`"disk"` onto
  `VolumeKindDir`/`VolumeKindDisk` — never passed through, because the SDK spells the directory kind `"dir"`, not
  `"directory"`; `DiskImage`'s `format` is lowercased, because upstream serializes `DiskImageFormat` as the bare
  variant name — `"Raw"` — while `DiskOptions.Format` is the lowercase hint. An entry whose `type` is unrecognized (a
  newer runtime) is skipped rather than failing the whole read; a `guest`-less entry, invalid JSON, or an empty
  `config_json` is an error. A duplicated `guest` path resolves deterministically — the last entry wins — so a
  repeated mount cannot make the decision output order-dependent (see `reconfig-decision.md`).
- Consumers and what they read:
  - `reconfig.PlanReconfig` via `resolveReconfigDecision` — `tmpSizeChange` (`size_mib` at `/tmp`), `workspaceQuotaChange`
    (`quota_mib` at `/workspace`), `homeVolumeChange` (the `name` of the `Named` mount at `/home/dev`). It degrades,
    never fails: a parse/lookup failure is logged and the run proceeds with no mounts.
  - `vm.ProjectVMHomeVolume` — the same `Named` mount for home-volume recovery (#219,
    `specs/home-volume-recovery.md`); the shared reader behind both `prepare.liveHomeVolume` and the #144 adoption path.
    It returns `(volume, exists, err)` and is **fail-closed**: a mounts-read failure returns `exists=true` with the
    error, so a caller that might destroy the VM on the strength of the result can fail closed instead of dropping its
    volume. `prepare.liveHomeVolume` is a verbose-logging wrapper that discards the error (so home-volume recovery and the
    dry-run preview keep the previous degrade behaviour), while `vm.legacyAdoptionPending` propagates it and aborts
    adoption.
  - `vm.InspectSandbox` — the mount list rendered by `sandbox inspect`
    (`specs/runtime-management-commands.md`, Slice 3). It reports the read failure like its `Config()` read does, so
    `sandbox inspect` never silently shows zero mounts for a VM that has them.
- A missing `mounts` key is not an error — a config persisted by a runtime that does not record mounts is valid, and
  must not read as "everything changed". It yields an empty (non-nil) map. The same holds for `network.ports`: a
  config without it yields no bindings, and `ParseConfigPorts` skips an entry whose protocol the launcher does not
  know (a newer runtime), mirroring the mounts treatment of unknown variants. The ports consumers
  (`reconfig.publishReconfig` for change detection, `ports.ResolveServeHostPort` for serve-port reuse, and
  `prepare.existingServeHostPort` for the deferred-keep serve port) degrade, never fail: without the read-back every
  run would read the ports as changed and recreate the VM, which is the failure mode this contract exists to prevent.

## Deliberate non-goal

The `MountState` fingerprint for the user's extra host bind mounts (`opts.Mounts`) stays. It covers a different set
(the user-configured binds, fingerprinted with source and readonly) than the managed mounts read here, and the two do
not overlap. Replacing it with a live comparison is possible now that mounts are readable, but is not part of this
change.

## Upstream

`SandboxConfig.UnmarshalJSON` dropping `mounts` is a Go SDK defect, not an intentional contract: `SandboxConfig`
already exposes `Volumes` and `WithMounts`, so the reverse direction (create) works while the read-back direction
silently loses the same data. Fixing it upstream would let `ConfigMounts` collapse into `Config()`. Until then the
launcher keeps its own decode.
