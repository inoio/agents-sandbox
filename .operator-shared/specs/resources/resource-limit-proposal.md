---
description: Contract for detecting a project VM resource-limit hit (memory OOM and full disks) and prompting the user to raise the persisted setting, with the host terminal recovered automatically — the evidence classes (guest OOM kernel-log line, metrics headroom, in-guest probe), the threshold and re-arm, the anti-nag marker, the persistence-layer selection and env-shadow rule, and which limits apply live vs recreate.
read_if: Working on the OOM/limit-hit detector, the disk probe or disk raise, the "raise the limit" proposal, terminal corruption after an in-VM kill, the `msb.Sandbox.Metrics`/`Logs` seam, the persistence of `memory`/`disk-size`/`workspace-quota`/`tmp-size`, the live root-disk grow, or the memory-above-boot-max recreate rule.
---

# Resource-limit detection and raise proposal

## Intent

When the VM hits a resource limit — most visibly an OOM kill of the agent harness or the
serve daemon — the user is left with a broken terminal and no explanation, and the only fix
is to relaunch. The project VM's limits (memory, disk, CPU) are set at creation from
`--memory`/`--disk-size`/`--cpus` or the config default, and nothing observes them being hit.

We want the launcher to **detect a limit being hit and propose raising it**, letting the user
decide and adjust the value, and to **catch the adverse effects** of the hit (the broken
terminal, the un-explained session death) rather than leaving the user to diagnose them.

## What exists today

- **Limits are set at VM creation** in `vm.createProjectVM`: `WithMemory(opts.Memory)`
  (default `4G`), `WithMaxMemory(ParseMemoryGiB(sysinfo.TotalMemoryGiB()))`,
  `WithCPUs`, `WithMaxCPUs(sysinfo.NumCPUs())`, and a root disk only when `--disk-size` is set.
  So the boot maximum for memory is host physical RAM and for CPU is host CPU count.
- **The reconfig machinery already splits changes by how they can be applied**
  (`internal/sandbox/reconfig`): memory/CPU are applied live by `vm.reconcileResourceConfig`
  (`buildModifyOptions`, `ModificationPolicyNoRestart`, clamped to `MaxMemoryMiB`/`MaxCPUs`),
  while `tmpSizeChange`, `workspaceQuotaChange`, a root-disk decrease (`rootDiskChange`), ports,
  network, and the home volume all force a recreate. `ResolveReconfig` + `PromptA`/`PromptB` drive the apply/keep
  choice.
  This path triggers on **desired-vs-live config drift**, never on a limit actually being hit.
- **The serve daemon and dockerd are re-ensured on every run** (`vm.SetUpSandbox` →
  `ensureDaemon`/`startDockerdIfPresent`), so a daemon that died while detached is already
  relaunched at next launch. The gap is the *reason* and any in-session handling.
- **The interactive harness is an attached exec**: `session.runAttach` →
  `sb.AttachWith(ctx, "/bin/bash", ["-l","-c","opencode attach …"])`. The host terminal's raw
  mode is owned by the SDK's Rust `attach` for the duration of the call.
- **Limits are not observable to the launcher today.** The launcher's `msb.Sandbox` and
  `msb.SandboxHandle` seam interfaces declare neither `Metrics` nor `Logs`, although the pinned
  SDK (`microsandbox/sdk/go@v0.7.11`) provides both on `*Sandbox` and `*SandboxHandle`
  (`Metrics(ctx)`, `MetricsStream`, `Logs(ctx, LogOptions)`). Nothing reads kernel logs.
- **An OOM kill is not identifiable by exit code**: the guest reports `-1` for any signal death,
  and `msbSdk.JobInfo` has no OOM field. The guest has no swap/zram and no cgroup `memory.max`/
  `memory.events`, so there is no cgroup OOM counter either. See
  `guides/vm-oom-crash-resilience.md` for the primary-source findings.

## Goals

- Detect that a VM resource limit was hit, at the granularity of "memory" vs "disk" vs "CPU".
- Prompt the user to raise the matching setting, with the value adjustable, using
  `termio.Prompter` (so a future huh backend is a drop-in behind the seam).
- Persist an accepted raise so it survives the next launch, and apply it (live where possible).
- Catch the adverse effects: emit a terminal reset after the harness dies; explain an
  abnormal session end.
- Prefer prompting **directly after the hit**, falling back to **next launch** when in-session
  prompting is not possible.

## Non-goals

- **The huh prompt backend.** Building on `termio.Prompter` is in scope; implementing the
  deferred `OPENCODE_SANDBOX_PROMPT` backend (`specs/architecture/termio-ui.md`) is not. The prompt
  must be implementable with the existing line backend (`Select`/`Input`).
- **Auto-raising during a session.** The watcher observes; it never modifies resources
  mid-session. A user-opt-in auto-raise is a possible later addition.
- **Preventing OOM by protecting processes** (`oom_score_adj`, cgroup weights, in-guest systemd/
  earlyoom). Noted as a future direction; the detached-job marker identity blocks the obvious
  shell wrapper (see the guide).
- **Changing the default `--memory`.** The raise is a consequence of a detected hit, not a
  blanket default change.

## Anti-goals

- A false proposal on a normal exit. The detector must not nag on every session end.
- Surfacing a raise we cannot actually apply (e.g. persisting to a layer an env var or flag
  overrides) without saying so.
- Silent resource changes. The user decides; we never raise unprompted.

## Design decisions (approaches considered)

- **A — catch on the way out, remember per project.** Trigger on an abnormal end of the
  attached session (or a dead service at re-ensure), attribute it with metrics + kernel log,
  then prompt (terminal reset first) and persist an accepted raise to the per-project user
  config; the existing reconfig path applies it. *(chosen)*
- **B — watch and prevent.** A host-side `MetricsStream` watcher acts during the session
  (warn / auto-raise). Rejected as the primary mechanism: it cannot prompt mid-session (the
  TUI owns the terminal), and auto-raise is out of scope. **Its watcher is adopted into A**
  because post-mortem metrics show no pressure — the pressure must be sampled while it exists.
- **C — symptoms only.** Terminal auto-reset plus a daemon-restart reason, no detection/prompt.
  Rejected: does not deliver the raise proposal.

## Design

### 1. Detection pipeline

Three mechanisms compose; the first two run in-session, the third covers everything else.

**(a) Metrics watcher during the session.** In the shared `session.runAttach` path, start a
watcher goroutine that samples `sb.Metrics()` on an interval (default ~2 s, tunable via
a `Session` option/seam) for the lifetime of the attach. It retains a compact summary: the
minimum observed memory headroom (`MemoryLimitBytes - MemoryBytes`, or `MemoryAvailableBytes` —
confirmed on a KVM host that `MemoryBytes` is guest-in-use and `MemoryAvailableBytes` tracks it),
the peak `MemoryBytes`/`MemoryLimitBytes` ratio, and the minimum `UpperFreeBytes` when present
(the `upper_*` fields are sampled periodically and lag a write by several seconds — confirmed on a
KVM host — so absence is not "no pressure"). It must tolerate a metrics read failing (VM stopped), never
block session teardown, and never write to the terminal while the attach owns it: a failed read
is reported once after the attach. It is stopped and its summary read inside `runAttach`, before the client
lease is released and the VM reaped, so both `Run` and `Shell` get the watcher.

**(b) Abnormal-end classification.** `session.runAttach` already funnels the guest exit
through `finalizeRun(attachErr, exitCode)`. An exit code of `-1` (signal death) or a non-zero
`attachErr` marks an abnormal end. Separately, the serve daemon's death is detectable through
the existing service path (`ensureDetachedService` fails fast on `JobInfo.ExitCode != nil`); a
run that finds the daemon already dead should record the same signal.

**(c) OOM attribution.** On an abnormal end, decide whether memory was the cause, from the
watcher summary plus the persisted guest kernel log:
- read `handle.Logs(LogSourceSystem, tail)` and look for the kernel OOM line
  (`Out of memory: Killed process …`). **Confirmed on a KVM host** that the guest printk reaches
  this source, and it names the victim (`Killed process <pid> (<comm>)`), so the launcher can
  attribute which process died (the harness/serve daemon vs a transient child). This is the
  primary, *direct* detector. Because a saturated guest OOM-kills even diagnostic execs, the read
  must be from the persisted log, never an in-guest probe.
- the watcher summary (below) is the secondary/fallback attribution when no log line is found
  yet (e.g. a stopped VM, or a crash without an OOM).
- if neither is conclusive, do not propose (avoid false positives).

**(d) Persistence of the signal + next-launch fallback.** On a conclusive detection, record a
lightweight per-project marker (`HomeState` field, e.g. `resource_pressure`) capturing what was
hit, when, and the evidence class (log vs headroom) — so a proposal survives to the next launch
if the in-session prompt cannot run. The same check runs at the next launch in the `prepare`
plan-reconfig stage, which already holds the live VM handle: it re-reads the
kernel log and the marker, and proposes. A fresh metrics read there is never a hit by itself (a
busy VM another client uses is not a limit hit); headroom counts only as the trip the session
watcher witnessed. The marker records the newest observed hit and whether
it was answered; answering a proposal (accepted or declined) advances it so the same hit is not
re-offered, while a hit only observed (a non-interactive run, or a mere offer nobody answered)
stays pending and is proposed on the next interactive launch. Clearing pressure alone never
replays an already-answered kill.

**(e) Disk pressure.** Memory OOM ends the session cleanly (the harness dies, attach returns), so
its offer happens right after. Disk pressure usually does **not** end the session — a command
fails with `ENOSPC`/`EDQUOT` and the agent keeps running — so disk is probed after **every** attach
end, clean or abnormal (skipped when a signal ended it), and recorded in a per-kind marker
(`HomeState.DiskPressure`) together with the measured size and free space. The next launch offers
only a recorded, unanswered disk hit, built from those recorded values; it does not probe, because
the VM may be stopped. A raise of the root disk or `/tmp` starts from the larger of the configured and
the measured size, so a VM larger than its config (created with a flag, run without it) is never
offered a shrink that the next launch would rebuild to. `/workspace`'s mount size is the host
repository's content plus the quota (confirmed on a KVM host), so it is judged and raised against the
configured quota instead: its free space is what is left of the quota. The probe runs under its own
timeout so a wedged guest cannot hang the end of a session. The marker writers are guarded under the state lock: recording keeps a
hit another session stored first (answered or not), and clearing drops only the hit that recovered.
`--serve-only` (no attach) is not covered.
- **Signal: one in-guest `df -Pk / /workspace /tmp`** over `sb.Exec`, for all three limits.
  Confirmed on a KVM host: it reports them immediately — `/` is the overlay at the root-disk size,
  `/workspace` is virtiofs whose size is the guest-write budget (existing host content counts as
  used, the quota comes on top), `/tmp` is the tmpfs — and keeps working with a mount at 100 %
  (disk-full does not kill the probe, unlike memory pressure). `Metrics.UpperFreeBytes` is not used:
  it is sampled periodically and lags a write by several seconds.
- **Apply:** the root disk grows live (§5); the `/workspace` quota and `/tmp` change only by a
  **recreate** (the modify API has no mount field), so their prompt frames it as a VM rebuild.
- `/tmp` is RAM-backed: its contents count against the VM's memory.

**Threshold.** Disk has no crash event, so a threshold decides it: offer when free space is below
**`min(15 % of the limit, 2 GiB)`** — small mounts trip at 85 % used, large ones at 2 GiB free, so a
2 GiB `/tmp` does not trip half full and a 64 GiB root does not trip with 9 GiB free. Anti-nag
hysteresis: after an offer, do not re-offer until free space is back above **twice** the trip level
and it trips again. `/workspace`'s limit is its guest-write budget, not the host filesystem's size.

The memory headroom fallback keeps its own threshold: offer when usage is **≥ 90 % of the limit, or
free memory is ≤ 1 GiB** (first to trip), and re-arm only when the condition has fully cleared —
usage **below 80 %** *and* free memory above the floor. (Requiring both matters because the floor
trips independently of the ratio: for a small limit a snapshot can sit below 80 % and still be under
pressure, and re-arming on the ratio alone would re-offer every launch.)

### 2. Terminal recovery

- After `AttachWith` ends abnormally (a signal death, an attach error, or a signal to the
  launcher) and before any launcher prompt or message, emit a terminfo reset sequence to the
  terminal (`rmcup`-equivalent: leave alternate screen, mouse off, show cursor,
  SGR reset). The SDK restores host termios itself on normal return, but the guest TUI's
  emulator modes (alternate screen, mouse) survive an OOM-killed TUI — that residue is what
  forces the manual `reset`. Emit it **only when the destination is a terminal** (the writer is
  an `*os.File` tied to a TTY): a redirected or captured stdout must never receive raw escape
  bytes. The check is a substitutable seam so both paths are testable without a real TTY.
- Add SIGTERM/SIGINT handling around the attach so the launcher restores the terminal before
  exiting on those signals. The handling covers the attach call only: a signal-ended attach skips
  the memory offer, and the post-attach prompt keeps the default Ctrl-C behavior. SIGKILL remains uncatchable; that case is covered by the next-launch
  fallback.
- Exact escape sequence and whether it clears the harnesses' residue **needs a KVM host** to
  confirm; prefer deriving it from terminfo at runtime over a hardcoded string.

### 3. Proposal prompt

- Built on `termio.Prompter` (`IsInteractive`, `Select`, `Input`, `Confirm`). Non-interactive
  invocations (`--yes`, piped input, no TTY) never block: they log what was detected and defer
  to the next launch or leave the marker.
- Flow (in-session, after terminal reset): explain what happened ("the VM ran out of memory and
  the harness was killed"), then offer to raise `--memory`. `Select` for the decision, `Input`
  for the value, pre-filled with a suggested increment (e.g. current + 25%, rounded to GiB) and
  bounded above by the boot maximum (host RAM) and, for the host-bound warning, by available
  host memory.
- The same prompt shape is reused next launch for a persisted marker.
- Disk: each tripped limit is offered on its own (one probe can trip several). The prompt names
  the mount and its free space and says whether the raise applies live (root disk) or by rebuilding
  the VM (`/workspace` quota, `/tmp`; the home volume is kept). The suggestion is +50 % rounded up to
  a whole GiB, at least +1 GiB. A `/tmp` raise is capped at the largest whole GiB strictly below
  `memory` (falling back to `memory-1` when no whole GiB fits, i.e. memory below 1 GiB), so an
  accepted raise stays below the memory limit `options.ValidateSizes` enforces; a refusal names the
  tmp ceiling below the VM memory limit. The root disk and the quota have no hard cap, but the prompt
  warns when the host filesystem holding the runtime home, respectively the repo, has less free space
  than the raise. The persisted keys are `disk-size`, `workspace-quota` and `tmp-size`, with the
  same owner-layer and shadowing rules as `memory` (§4).

### 4. Persistence and precedence

- **Accepted raise is written to the highest-precedence *file* layer that already sets the key,
  defaulting to the per-project user config** (`configpaths.UserSlugConfigDir(slug)`,
  `LayerNamePerSlugUserConfig`), which is user-local, per-project, trusted, and already in the
  resolution chain (`viperconfig.ConfigLayers`).
- **Project config must not silently shadow it.** Precedence is per-slug user config < project
  config. So before writing, determine which file layer owns the key: if a project config sets
  `memory` (or the disk key), write the raise **there** instead — otherwise the user-config
  write would be overridden. This needs a small "which file layer sets key K" helper (parsing
  each candidate layer file for the key), since only `docker` currently carries source tracking
  (`Config.dockerSource`). Keys are matched **case-insensitively**, mirroring viper's resolution:
  a file spelling `Memory:` sets the effective `memory` and is the owner, and the in-place edit
  replaces that key's value while preserving the user's spelling.
- **A non-file source wins.** If `memory` is supplied by env (`AGENTS_SANDBOX_MEMORY`, or the
  deprecated `OPENCODE_SANDBOX_MEMORY` alias) or a flag, a persisted config value cannot take
  effect. In that case, tell the user the value comes from a higher-precedence source instead of
  prompting, do not write a shadowed file value, and count the hit as answered. An accepted raise
  names the value and the config directory it was written to.
- **Project config is untrusted** (`specs/configuration/project-config-trust.md`): a raise written to the
  project config also extends the checkout's approval by that key (`projecttrust.ApproveSettingWrite`), so
  the next run does not ask about the launcher's own change; if the approval cannot be recorded, nothing
  is written and the raise reports an error.
- Formatting/writing: **there is no launcher-config writer on `main`** — config files are read-only
  (`configpaths.FindConfigFile`). Define one: preserve the target file's format and its unrelated
  keys and comments (a `.json5`/`.jsonc` file must not be rewritten as YAML), write atomically
  (`internal/atomicwrite`) keeping the file's permissions (a created file is 0600; the rename
  leaves a hard link to the old file on the old content), and create `config.yaml` only when the
  layer dir has no config file.
  Resolve a symlinked config to its target before the atomic rename so the link survives, and
  refuse to replace an anchored value another key aliases. When a format-preserving edit is not
  feasible, do not rewrite — log and skip persistence (surface the miss, never corrupt the user's file).

### 5. Apply

- Live where possible: `vm.reconcileResourceConfig` applies the persisted memory on the next
  prepare. A raise accepted in the next-launch offer is applied by that same run: the offer runs in
  plan-reconfig before the reconfig decision, and the accepted value replaces `opts.Memory` for
  the decision and the VM creation or live modify.
- **Memory**: live `Modify` up to `MaxMemoryMiB` (host RAM at creation). Above that, a recreate
  applies the raise only when a fresh VM would boot with a higher maximum (the host gained RAM,
  `ChangeFlags.BootMaxMemoryGrew`); otherwise the value is clamped live to the maximum, because a
  recreate would hit the same maximum and repeat on every launch. A new VM boots with its memory
  clamped to the maximum too. The offer never suggests or accepts more than the boot maximum, and
  a VM already at it gets an explanation instead of a prompt. A live
  memory resize is **asynchronous** (confirmed: `Modify` returns `STATE=converging` and the limit
  lags), so confirm convergence via `Metrics.MemoryLimitBytes` before reporting the raise applied,
  and do not read once. Convergence is the **exact** requested limit: a live limit above it is the
  old, larger value a decrease never unplugs, so a resize that does not land exactly — a decrease
  in particular — is reported as **not applied**, never as success.
- **Root disk**: `buildModifyOptions` stages `ModifyOptions.RootDiskSizeMiB` when the desired size
  exceeds the current one (`SandboxConfig.RootDisk.SizeMiB`), and `reconcileResourceConfig` applies
  it live; only a decrease, or a VM whose root disk size is not recorded, is a recreate
  (`rootDiskChange`). Confirmed on a KVM host that even a
  default (no `--disk-size`) VM has a managed root (4 GiB default) that grows live, and that the
  grown size is persisted.
- **Disk raises apply at the next launch**, like memory: an accepted value is persisted, and a raise
  accepted in the next-launch offer replaces the run's value before the reconfig decision, so that
  run applies it (root live, quota and `/tmp` through the drift → rebuild path with its keep/rebuild
  prompt when other clients are attached). There is no immediate post-attach apply.
- **`/workspace` quota and `/tmp`**: recreate-only (confirmed — the modify API has no mount field
  and `msb modify` exposes no mount flag), so the prompt frames the raise as a VM rebuild rather
  than a live change.

### 6. Limits covered

- **Memory** — primary, first stage.
- **Disk** — all three limits detected by the in-guest `df`; root applies **live grow**, quota/tmp
  are a **recreate**. Second stage.
- **CPU** — no kill, only throttling and no `cpu.stat` exposure through `Metrics`
  (`CPUPercent` only). Out of the first delivery; noted as a possible "sustained cap" advisory.

### 7. Non-interactive and host-bound

- Non-interactive: never block; record the observed hit (so it survives) but leave the proposal
  unanswered, so the next interactive run offers it.
- Host-bound: a memory raise is bounded by `MaxMemoryMiB` (host RAM). An accepted raise whose
  increase (new minus current limit) exceeds the host's available memory is still persisted, with a
  warning naming both figures: spare VM memory is backed only as touched, so a raise is cheap until
  the guest uses it, but then the host may swap or OOM-kill. The figure is
  `sysinfo.AvailableMemoryMiB` (Linux `MemAvailable`); macOS reports none — its free-page count
  leaves out the memory it reclaims on demand and would understate what is available — so no
  warning is given there.

### 8. Design decisions settled during review (builder calls)

These are the author's calls, not user decisions; a builder may follow them as written.

- **Metrics vs attach concurrency.** Assume `Metrics` may be called concurrently with `AttachWith`
  on the same `Sandbox`: the notify watcher (`session.startNotifyWatcher` →
  `notify.SandboxStreamOpener`) already streams from the same sandbox for the whole attach. Confirm
  in the real-VM test before relying on it; if it is unsafe, give the watcher its own
  `SandboxHandle.Connect()` + `SandboxHandle.Metrics` lifecycle.
- **Anti-nag dedup.** The `HomeState` marker is the dedup key, not the log. Next-launch detection
  proposes when the newest OOM kill is **newer** than the marker's recorded kill, or when a recorded hit was **never answered** (a non-interactive launch records
  the hit but does not answer it, so the next interactive run offers it); answering a proposal
  (accept or decline) advances the marker so the same kill is not re-offered, but only for the hit the
  offer named — a newer hit another session records while the prompt is open stays unanswered. An
  unmarked old OOM never re-proposes. The re-arm rule (§1: fully clear — below 80% *and* above the floor) governs
  re-offering a *headroom* condition
  after an answered offer, but the recorded kill timestamp is retained, so clearing pressure and a
  later trip never replays an OOM kill that was already answered.
- **OOM fingerprint.** A kill is identified by its boot's start (the runtime's persisted
  `sandbox starting` line, the latest one logged before the kill) plus the kernel uptime in the
  line, never by the log entry's timestamp. Confirmed on a KVM host: the runtime stamps a kernel
  line when it writes it and re-writes a boot's kernel lines with the stop time when the VM stops,
  so the same kill reads differently on a running and a stopped VM; a restarted VM no longer
  carries the previous boot's kernel lines at all (the marker covers that). When the tail holds an
  OOM line, a second read up to it (no tail limit) supplies the boot start. Without a boot start or
  an uptime prefix, the entry timestamp stands in.
- **env/flag provenance.** Reuse the derived key registry's env-var name (see
  `specs/configuration/config-keys.md`) — which includes the deprecated `OPENCODE_SANDBOX_` alias bound
  for the same key — plus whether the key's own flag was set explicitly
  (`options.RunOptions.ExplicitFlags`, recorded by `runoptions.Build` for `--memory`,
  `--disk-size`, `--workspace-quota` and `--tmp-size`); when either supplies the key, do not
  write a shadowed config value and tell the user the source. `resourceproposal.PersistSetting`
  and `ShadowSource` apply this to every limit key.
- **Detection scope.** The watcher + post-attach handling live in the shared attach path so both
  `Run` and `Shell` get them. The detached serve-daemon death is handled at the `SetUpSandbox` /
  `ensureDaemon` re-ensure (next launch, via marker + log), not by a second watcher.
  `--serve-only` (no attach) is next-launch only.
- **Timing.** Post-attach detection, the prompt, and the marker write run inside `runAttach`'s
  closure, before `withClientSession` releases the lease and reaps, so the VM is still alive and
  the terminal is ours. An accepted post-attach raise states that it applies on the **next** run.
- **huh** stays out of scope (see Non-goals); the prompt is built on `termio.Prompter` only.

## Seams changed (summary)

- `msb.Sandbox` and/or `msb.SandboxHandle`: add `Metrics` and `Logs` (real + mock).
- `session`: a metrics watcher; abnormal-end classification; terminal reset emission; signal
  handling around attach; the post-attach memory-raise offer; the post-attach disk probe.
- `prepare`: the next-launch memory and disk offers (in the plan-reconfig stage, before the
  reconfig decision) and the plan's resource limits that ensure-vm applies.
- `resourceproposal`: the shared `DetectAndOfferMemoryRaise` and `DetectAndOfferDiskRaise` cores
  (classify, dedup against the marker, offer, persist) used by both paths, with one raise prompt.
- `pressure`: the OOM and headroom classifiers; the `df` probe command, its parser and the disk
  threshold.
- `vm`: the live root-disk grow in `buildModifyOptions`; `reconfig.rootDiskChange` recreates only
  for a decrease or an unrecorded disk.
- `configpaths`/`viperconfig`: a "which file layer owns key K" helper; write the raise to the
  right layer.
- `state`: `HomeState` markers for the memory hit and per disk limit.
- `sysinfo`: available host memory (`AvailableMemoryMiB`) and host free disk space
  (`FreeDiskMiB`).

## Testing

- Unit: watcher sampling/stop/summary; OOM attribution (log line present/absent, headroom
  threshold, inconclusive → no proposal); layer-ownership selection; persistence round-trip;
  prompt + non-interactive fallback; terminal reset emission; the `Metrics`/`Logs` seams via the
  msb mocks.
- CLI: an OOM-like abnormal end, a next-launch marker, a full `/tmp` after a clean exit and a
  disk size set by its flag are exercised in the `cmd/agents-sandbox/cli_*_test.go` style with
  mocked seams.
- Real VM (`msb` tag / KVM host): that an OOM writes a parseable kernel-log line; that
  `Metrics` headroom reflects a near-limit guest; that the `df` probe reports all three disk limits
  and a filled `/tmp` trips; that a live root-disk grow applies and persists; that the emitted
  reset clears the actual harness residue.

## Staging

1. **Memory detection + propose + terminal recovery**: seams, watcher, attribution, prompt,
   owner-layer persistence, terminal reset, next-launch fallback, host-memory warning. Landed.
2. **Disk**: the `df` probe, per-limit markers, root live grow + quota/tmp recreate proposals,
   host free-space warning. Landed.
3. **(Later, separate)** huh backend; mid-session auto-raise; process protection.

## Open questions

Resolved on a KVM host (msb 0.7.11):
- Guest OOM `printk` **does** reach `kernel.log`/`LogSourceSystem` and names the victim. Residual:
  whether the SDK reads it from a *stopped* sandbox (the CLI read it while running).
- `Metrics` semantics: `memory_bytes` is guest-in-use, `memory_available_bytes` tracks it, and a
  live resize is asynchronous (`STATE=converging`, the limit lags the call).
- A default root disk (no `--disk-size`) is a managed 4 GiB disk and **does** grow live, so no
  create-time sizing is required.

Resolved on a KVM host (msb 0.7.13):
- `SandboxHandle.Config().RootDisk.SizeMiB` reports the root size for a default VM too (4096 MiB),
  and after a live grow (`Modify{RootDiskSizeMiB}`, `disposition: live`) a fresh handle reports the
  new size, also after stop and restart, so drift detection can rely on it.
- `/workspace` quota and `/tmp` pressure are measurable with an in-guest `df` (§1e).
