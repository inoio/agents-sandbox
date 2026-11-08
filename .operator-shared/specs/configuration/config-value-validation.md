---
description: Contract for validating the launcher's VM size and duration settings - memory has a 128 MiB floor and is clamped (not rejected) to the host's RAM at boot, tmp-size defaults to min(2 GiB, memory/2) and must stay below the effective memory (clamped at creation), disk-size and workspace-quota must be positive, and the duration/retry keys keep their existing bounds.
read_if: Changing VM size validation, `options.ParseMemoryOK`/`ResolveTmpSizeMiB`/`ValidateSizes`/`ResolveDiskSizeMiB`/`ResolveWorkspaceQuotaMiB`, `runoptions.resolveSizes`, the `memory`/`tmp-size`/`disk-size`/`workspace-quota` bounds or the tmp-vs-memory relation, `vm.bootMemoryMiB`/`vm.effectiveTmpSizeMiB`/`reconfig.tmpSizeChange` or the tmp mount; or diagnosing a rejected size, a zero-size setting, or a `/tmp` larger than the VM's memory.
---

# Config value validation

`runoptions.resolveSizes` checked only the *syntax* of `tmp-size`/`disk-size`/`workspace-quota` and
never looked at `memory`. `options.ParseMemoryOK("0")` returns `(0, true)`, so `disk-size: 0` and
`workspace-quota: 0` resolved to zero; `tmp-size` (a tmpfs in the VM's RAM) was never compared with
`memory`; and `memory` had no floor beyond the msb SDK's "must be greater than 0". This makes the size
settings a checked contract instead of syntax-only.

## Where validation runs

Two layers, because the sizes are settable by flag and env as well as the config file:

- **`options.ValidateSizes`** is a pure function of the raw specs (memory, tmp-size, disk-size,
  workspace-quota) with no host dependency, so it is directly unit-testable. It owns both the syntax
  checks ("invalid --tmp-size …") and the range/relation checks.
- **`runoptions.resolveSizes`** delegates to `ValidateSizes` after the flag > env > config merge, so a
  bad `--memory` is caught the same as a bad config value. `viperconfig.validate` does **not** gain the
  size checks: it sees only the config-file layer and would miss a flag.

The host-RAM comparison is deliberately **not** part of validation: `memory` above host RAM stays a
boot-time clamp with a warning (`vm.bootMemoryMiB`), matching `docs/configuration/launcher.md`.

## The size rules

| key | rule |
|---|---|
| `memory` | parses; `>= options.MinMemoryMiB` (128 MiB); above host RAM clamped at boot (unchanged) |
| `tmp-size` | parses; `> 0`; resolved value `<` resolved `memory`; unset → `min(2 GiB, memory/2)` |
| `disk-size` | parses; `> 0` |
| `workspace-quota` | parses; `> 0` |

New constant `options.MinMemoryMiB = 128`. It is a sanity bound, not a runtime requirement: the msb
SDK only requires memory `> 0` (default `WithMemory(512)`), and no real run approaches 128 MiB, so the
floor only catches typos and unit mistakes. It also makes the tmp default always usable — with
`memory >= 128`, `memory/2 >= 64`, so the derived tmp needs no special floor.

A rejected size names the key and the constraint (e.g. `--tmp-size 8G must be below memory 4G`), not just
"invalid size". The existing syntax errors ("expected a size like 4G, 512M, or 2048") stay.

## The tmp default and the tmp < memory invariant

`options.ResolveTmpSizeMiB` becomes memory-relative:

```go
// ResolveTmpSizeMiB returns the tmpfs size in MiB: the parsed spec when set,
// otherwise min(DefaultTmpSizeCapMiB, memoryMiB/2).
func ResolveTmpSizeMiB(spec string, memoryMiB uint32) uint32
```

- The unset default is `min(2 GiB, memory/2)`. `2 GiB` (today's `options.DefaultTmpSizeMiB`) becomes
  the cap constant, `DefaultTmpSizeCapMiB`.
- The default **preserves today's behavior exactly**: memory 4096 MiB → tmp 2048 MiB, the current
  default. So no existing project's tmp changes and none recreates its VM for this. `/4` was rejected
  for that reason.
- The derived default is always `< memory`: if `memory/2 <= 2 GiB` it is `memory/2 < memory`; otherwise
  `2 GiB < memory/2 < memory`. So the `tmp < memory` check only ever fires on an **explicit** value.

### Creation-side clamp

Validation compares tmp against the *configured* `memory`, but the boot clamp can lower the effective
memory on a smaller host: `memory: 64G` / `tmp-size: 48G` on a 32G host validates (48 < 64) yet boots
a 32G VM, leaving tmp above VM memory. So the resolved tmp is **clamped to the effective memory at
creation**, next to the memory clamp, with a warning when it bites — mirroring `bootMemoryMiB`. This
keeps the physical invariant (tmpfs is RAM-backed) true regardless of the host, and keeps validation
free of any `sysinfo` call.

`bootMemoryMiB` already returns the effective memory; `createProjectVM` computes it once and feeds
both `msbSdk.WithMemory` and `effectiveTmpSizeMiB`, which clamps the resolved tmp (with a warning) before
`buildMounts` receives it instead of the raw resolved value. `memory` stays the single authoritative
knob — there is no floor derived from tmp, because a memory floor cannot survive the boot clamp and
would silently inflate the VM's RAM.

### Reconfig comparison

`reconfig.tmpSizeChange` compares only an **explicit** `tmp-size`. Comparing the memory-derived default
would recreate every VM whose configured memory is below the default (the default changes with memory),
breaking "no existing project recreates". Because the creation-side clamp can leave the live tmp at or
below the boot maximum, the comparison caps the desired explicit value at the live VM's
`cfg.MaxMemoryMiB` — otherwise a host-RAM-clamped tmp would differ from the raw configured value and
recreate the VM on every run.

## Unchanged bounds

These are already enforced by `viperconfig.validate` and stay:

- `cpus` 0–255, where `0` means "all CPUs" (`vm_resources.go`);
- `auto-stop-max-session-retries >= 0`, where `0` means "no retries";
- `auto-stop-timeout`, `auto-prune-age`, `manual-prune-age > 0`;
- `upgrade.interval >= 1h` (`upgrade.MinInterval`).

`0` is a legitimate value only for `cpus` and `auto-stop-max-session-retries`; for every size and every
duration it is rejected.

## Call sites

`ResolveTmpSizeMiB` gains its memory argument at each caller: `vm.createProjectVM` (the creation path —
pass the effective memory, then clamp through `effectiveTmpSizeMiB`), and
`resourceproposal.RunDiskLimits` (the disk-limit offer's current tmp; pass the configured memory via
`options.ParseMemory(opts.Memory)`). `ResolveDiskSizeMiB` and `ResolveWorkspaceQuotaMiB` are unchanged.
`reconfig.tmpSizeChange` does not call `ResolveTmpSizeMiB` (see *Reconfig comparison*).

`docs/configuration/launcher.md` states `/tmp` must stay below `memory`; this change makes that an
enforced contract and documents the 128 MiB memory floor and the memory-relative tmp default.

## Non-goals

- No change to the memory boot clamp: an over-host-RAM `memory` is still clamped and warned, not
  rejected.
- No new config keys or formats.
- No cross-check of `disk-size`/`workspace-quota` against host free space (the disk-limit offer already
  does that at runtime).
