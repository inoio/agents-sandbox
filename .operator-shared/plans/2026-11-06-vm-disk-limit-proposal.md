# VM Disk-Limit Detection and Raise Proposal — Implementation Plan (Phase 2 of #544)

**Goal:** Detect when the project VM's root disk, `/workspace` write quota or `/tmp` runs full, tell the user, and
offer to raise the matching setting (`disk-size`, `workspace-quota`, `tmp-size`); apply the root disk live and the
other two by the existing VM rebuild.

**Architecture:** Reuse Phase 1's pieces. `pressure` gains disk kinds and a `df` parser; `session` probes disk usage
with one in-guest `df` after every attach; `resourceproposal` gets a disk counterpart of
`DetectAndOfferMemoryRaise` that shares the prompt, validation and the owner-layer writer; `prepare` offers an
unanswered disk hit at the next launch before the reconfig decision; `reconfig`/`vm` turn a root-disk increase into a
live grow.

**Spec:** `.operator-shared/specs/resource-limit-proposal.md` (§1e, §1 threshold, §3, §4, §5, §6). Phase 1 is merged;
this plan builds on its code, not on its plan.

## Host findings (KVM host, msb 0.7.13, 2026-11-06)

Measured with a throwaway `msb`-tagged probe (mounts as `vm.buildMounts` builds them, `/workspace` quota and `/tmp`
size 64 MiB, a managed root of 1 GiB and the default):

- `SandboxHandle.Config().RootDisk.SizeMiB` reports the root size for a default VM too (4096), and after a live grow a
  fresh handle reports the new size, also after stop and restart. Drift detection can rely on it.
- A root-disk grow through `Modify{RootDiskSizeMiB, Policy: NoRestart}` is `disposition: live`, applies immediately,
  and `df /` shows the new size.
- `Metrics.UpperUsedBytes`/`UpperFreeBytes` are not "null until first write": they stay nil for several seconds after
  a write and then appear (sampled periodically, ~10 s). Usable, but lagging.
- In-guest `df -Pk / /workspace /tmp` is immediate and covers all three limits: `/` is the overlay at the root-disk
  size, `/workspace` is virtiofs whose size is the guest-write budget (existing host content counts as used, the quota
  comes on top), `/tmp` is the tmpfs at its size. `df` still works with a mount at 100%.
- `/tmp` is RAM: filling a 64 MiB `/tmp` raised the VM's memory use from 37 to 84 MiB.

## Decisions to confirm before implementing

1. **Detection signal: in-guest `df` for all three** (recommended) instead of `UpperFreeBytes` for the root and `df` for
   the rest: one mechanism, immediate, and the same numbers the user sees.
2. **Threshold.** The spec's "≥ 90 % used or ≤ 1 GiB free" trips the default 2 GiB `/tmp` at half full and would nag.
   Recommended: trip when free space is below `min(10 % of the limit, 2 GiB)`; re-arm when free space is back above
   twice that. Small mounts trip at 90 % used, large ones at 2 GiB free. Memory keeps its threshold.
3. **When to probe.** After every attach end, clean or abnormal (disk pressure rarely ends a session), skipped after a
   signal. The next launch offers only an unanswered recorded hit; it does not probe (the VM may be stopped).
   `--serve-only` is not covered.
4. **Suggestion and bounds.** +50 % rounded up to a whole GiB, at least +1 GiB. `/tmp` is capped at the VM's memory
   limit, and its prompt says that `/tmp` uses memory. Root and quota get no hard cap (warn when the host filesystem
   holding the runtime home, respectively the repo, has less free space than the raise).
5. **Apply.** Persist only; the next launch applies it: the root disk by a live grow in the reconcile step, the quota
   and `/tmp` through the existing drift → rebuild path (with its keep/rebuild prompt when other clients are attached).
   A raise accepted at launch applies to that run, like memory. No immediate post-attach grow (one apply path).
6. **Out of this plan:** the host available-memory warning for a memory raise (spec §7) — a separate small change.

## Tasks (TDD; each ends green with `make check`)

### Task 1: `pressure` — disk usage and the disk threshold

- Add kinds `root-disk`, `workspace-quota`, `tmp` and evidence `disk-usage`.
- `ParseDiskUsage(output string) map[Kind]DiskUsage` for POSIX `df -Pk` output (`SizeKiB`, `UsedKiB`, `AvailKiB`),
  mapping `/`, `/workspace`, `/tmp`; unknown lines are ignored, a malformed line drops only that mount.
- `DiskTripped(DiskUsage) bool` and `DiskRecovered(DiskUsage) bool` per decision 2.
- Tests: busybox and coreutils `df -P` output (from the probe), 100 % full, missing mounts, malformed lines, the
  threshold at the boundary for a 2 GiB and a 64 GiB limit, recovery hysteresis.

### Task 2: `state` — per-kind disk markers

- `HomeState.DiskPressure map[string]ResourcePressureState` keyed by kind, with record / mark-offered / re-arm through
  the existing locked transactions (`specs/project-state-transactions.md`), the same semantics as the memory headroom
  marker: an answered hit stays closed until it recovers and trips again; an unanswered one stays offerable.
- Tests: record, answer, re-arm only after recovery, unknown kinds kept on rewrite, missing state file is a no-op.

### Task 3: `resourceproposal` — generalize persistence and shadowing

- `PersistSetting(paths, slug, key, value, flagChanged)` replaces `PersistMemory`; `ShadowSource(key, flagChanged)`
  replaces `MemoryShadowSource` (env names from `viperconfig.EnvVarNames(key)`).
- `options.RunOptions` records which of `--memory`, `--disk-size`, `--workspace-quota`, `--tmp-size` were set
  explicitly (`runoptions.Build`), replacing the single `MemoryFlagChanged`.
- Tests: owner-layer selection and shadowing per key; existing memory tests keep passing unchanged in behavior.

### Task 4: `resourceproposal` — `DetectAndOfferDiskRaise`

- Input: the parsed usage, the current limits (root from `Config().RootDisk`, quota and `/tmp` from the persisted
  mounts or the configured values), the markers, `opts`, the UI. For each tripped kind: dedup against its marker,
  offer (Select + Input with the Phase 1 re-prompt validation: above current, within the decision-4 bound), persist the
  key, mark answered; non-interactive records without answering. Returns the persisted settings per key.
- Prompt texts name the mount, the free space, and whether the change applies live (root) or by rebuilding the VM
  (quota, `/tmp`; home volume kept).
- Tests: one test per path, asserting the returned settings in this package (gomutants runs only the mutated
  package's tests), plus two kinds tripped in one probe.

### Task 5: `session` — post-attach disk probe

- After every attach end (skip when interrupted), run `sb.Exec("df", ["-Pk", "/", "/workspace", "/tmp"])` inside
  `runAttach`'s closure, before the lease is released; parse and call `DetectAndOfferDiskRaise`. A failed exec or parse
  is logged at verbose and never changes the session's result.
- Tests: clean exit with a full `/tmp` offers; exec error is silent; interrupted attach skips; order relative to the
  memory offer (memory first).

### Task 6: `prepare` — next-launch offer

- In plan-reconfig, before the reconfig decision, offer unanswered disk markers; an accepted value replaces the run's
  `DiskSize`/`WorkspaceQuota`/`TmpSize` so this run applies it (same pattern as `memory`).
- Tests: accepted raise lands in the plan; declined keeps the configured value; `--dry-run-vm` does not prompt.

### Task 7: `reconfig` / `vm` — live root-disk grow

- `rootDiskChange` reports only a decrease (still a rebuild); an increase is not a recreate trigger.
- `vm.buildModifyOptions` stages `RootDiskSizeMiB` when the desired size exceeds `cfg.RootDisk.SizeMiB`;
  `reconcileResourceConfig` applies it with the other live changes and warns on a conflict.
- Tests: increase → no recreate and a staged grow; decrease → recreate; equal → nothing; unset `disk-size` → nothing.

### Task 8: CLI and host tests

- `cmd/agents-sandbox/cli_resource_pressure_test.go`: a post-attach disk hit offers and persists `disk-size`; a
  recorded unanswered hit is offered at the next launch.
- `msb`-tagged test: the real `df -Pk` output parses for all three mounts in the runner image, and the launcher's
  reconcile grows a running VM's root disk.

### Task 9: Docs, spec, changelog

- `docs/configuration/launcher.md`: `disk-size` grows live (a decrease rebuilds); the raise-proposal section covers
  the three disk limits.
- Spec: replace §1e's root-disk signal with the confirmed `df` probe, record the threshold and the apply rules, and
  close the open questions.
- `changelog.d/<pr>.added.md`; delete this plan once the work lands (fold any missing clause into the spec first).

## Review focus

1. A full disk must never break the probe, the prompt or the session end; an exec or parse failure means no proposal.
2. A raise never shrinks a limit, and a decrease of `disk-size` still rebuilds instead of failing a live modify.
3. `/tmp` competes with memory: a `/tmp` raise above the memory limit is refused with that reason.
4. Non-interactive runs never block; their hits are offered at the next interactive launch.
5. One probe can trip several kinds; each is offered and marked on its own.
