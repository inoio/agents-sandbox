---
description: How the microsandbox VM enforces memory/disk/CPU limits (virtio-mem, no swap, guest global OOM killer), how a guest OOM kill or crash surfaces to the launcher (exit code -1, no OOM flag, kernel.log), how host-terminal corruption arises and is recovered, and the ranked detect-and-propose mitigation path for the launcher.
read_if: Working on VM OOM/crash handling, a memory/disk/CPU limit-hit detector or "raise the limit" proposal, terminal corruption after an in-VM kill, or the daemon/dockerd re-ensure and supervision behaviour.
---

# VM OOM and crash resilience

Research for the launcher-side problem: when an in-VM process (the agent harness or the serve
daemon) is OOM-killed, (a) the host terminal is left broken (stray input, wrong modes) until the
user clears it and runs `reset`, and (b) the attached session dies and the user has to relaunch.
The preferred direction is **detect-and-propose**: notice the VM limit being hit, propose raising
the matching setting, and catch the adverse effects of the hit. This file records the mechanics
each half needs, then a ranked option list.

Primary sources: the live dev VM (an agents-sandbox guest, kernel `6.12.113`, hostname
`agents-sandbox-…-opencode2`), the pinned Go SDK
`github.com/superradcompany/microsandbox/sdk/go@v0.7.11`, the microsandbox Rust source at tag
`v0.7.11` (raw URLs below), the microsandbox docs site, and Linux kernel documentation.
Repository code is referenced by package/symbol, not line.

## 1. The VM memory/OOM model

**It is a hypervisor-capped microVM, not a cgroup- or container-limited process.** Each sandbox is
its own libkrun microVM with its own kernel (docs "Isolation boundary": "vCPU count and memory are
capped at VM creation and enforced by the VMM"). The host-side process needs only `/dev/kvm` and
gets no elevated privilege.

**Memory is virtio-mem hotplug; the `memory` limit is the plugged ceiling.** Verified in the
running guest: the kernel log shows `virtio_mem virtio1: region size: 0x880000000`, `device block
size: 0x200000`, `plugged size` matching `MemTotal` (≈7.78 GiB for this 8 GiB guest). The Go SDK
documents the pair as `WithMemory` = "memory limit in MiB (default 512MiB)" and `WithMaxMemory` =
"boot-time maximum hotpluggable memory in MiB" (`options.go`). The docs describe `max_memory` as
"Boot-time maximum hotpluggable memory" reserved for live resize headroom, and note `cpus`/`memory`
"are limits, not reservations. Guest memory is allocated as the VM touches pages"
(`sandboxes/overview.md`, `sandboxes/tuning.md`). In `vm.createProjectVM` the launcher sets
`WithMemory(opts.Memory)` (default `4G`, `options.DefaultMemoryMiB = 4096`) and
`WithMaxMemory(ParseMemoryGiB(sysinfo.TotalMemoryGiB()))` — so **the boot maximum is host physical
RAM**, and the same is true for CPUs (`WithMaxCPUs(sysinfo.NumCPUs())`).

**There is no swap and no zram, and overcommit is the default heuristic.** Verified live in the
guest: `/proc/swaps` does not exist, no `zram*` block device, `vm.swappiness = 60` (inert without
swap), `vm.overcommit_memory = 0`. The guest kernel is upstream Linux (in the microsandbox repo,
`vendor/libkrunfw` pins `6.12.113`, per its `.github/actions/cache-libkrunfw-kernel/action.yml`).
With no swap the guest cannot relieve anonymous-memory pressure.

**The OOM killer is the guest kernel's global killer, not a cgroup killer.** Verified live: the
guest cgroup v2 root exposes the memory controller (`/sys/fs/cgroup/cgroup.controllers` lists
`memory`) but **no** `memory.max`, `memory.high`, `memory.current`, `memory.events`, or
`memory.oom.group` anywhere; `/sys/fs/cgroup/microsandbox-workload/` has no `memory.*` at all. So
the `memory.events`/`memory.oom.group` detection surface described in the kernel's
`Documentation/admin-guide/cgroup-v2.rst` is **not available inside this guest** — there is no
per-cgroup limit and no cgroup OOM accounting. The cap comes from how much memory the VMM has
plugged, so when the guest exhausts it the standard global OOM killer fires. Victim selection is
the kernel's `oom_badness` (RSS + swap + page tables, adjusted by `oom_score_adj`); all processes
default to `oom_score_adj = 0` (verified for the running `opencode2 serve`, PID 999). Sysctls are
`panic_on_oom = 0` and `oom_kill_allocating_task = 0`, i.e. the default "pick the worst offender".
Swap contributes nothing, so resident memory dominates.

**An OOM kill is not distinguishable from any other signal death by exit code.** In the guest
agent, `crates/agentd/lib/process.rs` `exit_code()` returns `WEXITSTATUS` for a normal exit and
**`-1` for any `WIFSIGNALED` death** — so a SIGKILL-9 OOM victim reports `-1`, exactly like
SIGSEGV or any other signal. The Go SDK documents the same contract for `ExecOutput.ExitCode`:
"`-1` if the guest did not report one (e.g. the process was killed by a signal)" (`exec.go`). The
detached-job record `msbSdk.JobInfo` carries `ExitCode *int`, `TimedOut`, `State`, and `Failure
*ExecFailure`, but **no OOM field** (`jobs.go`); `ExecFailure` is only populated for a
never-started exec (`ExecEventFailed`, e.g. binary missing), not for a killed process
(`exec.go`). So `Job.Inspect` can tell the launcher *that* the daemon died, and its `-1` exit code
that it died by signal, but not *that it was OOM*.

**The runtime exposes no OOM indication.** No OOM/stats string surfaced in the FFI library, and no
launcher-visible API reports it (see §3 for what is available). Treat "was it OOM?" as something
the launcher must infer from memory pressure or guest kernel logs, not read from a field.

## 2. Host terminal corruption: mechanism and recovery

**The launcher does not own the host terminal; the Rust SDK does, for the duration of `Attach`.**
`session.runAttach` calls `sb.AttachWith(ctx, "/bin/bash", [...])` over the `internal/sandbox/msb`
seam. For the Unix path the SDK's `sdk/rust/lib/sandbox/attach.rs` (identical at `v0.7.11` and
`main`) does, in `agent::attach`:

- `crossterm::terminal::enable_raw_mode()`, then
  `scopeguard::guard((), |_| disable_raw_mode())` — the **only** restore path;
- a `tokio::signal::unix::signal(SignalKind::window_change())` for SIGWINCH; **no** SIGINT/SIGTERM
  handler;
- it writes guest output straight to stdout and reads raw stdin bytes itself.

Two consequences:

1. **Host termios is restored on any normal return or panic unwind** (guest process exited, guest
   agent connection closed, detach key, or a Rust panic), because the scopeguard drops. That path
   is robust to a *guest* OOM kill: the guest's death surfaces as `AttachMessage::Exited`/a closed
   connection, `attach` returns, and `disable_raw_mode()` runs.
2. **Host termios is NOT restored if the attach code never returns** — i.e. the launcher process is
   `SIGKILL`ed, aborts, or calls `os.Exit`/`process::exit`. Rust's guard does not run on SIGKILL,
   and there is no SIGTERM/SIGINT handler in `attach.rs`, so an external SIGTERM (or the *host*
   OOM killer choosing the launcher) also leaves the user's terminal raw.

**The SDK itself never enters the alternate screen or enables mouse/bracketed-paste.** Grep of
`attach.rs` finds only crossterm raw mode, SIGWINCH, and cursor-free passthrough — no `\e[?1049`,
mouse, or bracketed-paste sequence. Those terminal-*emulator* modes are set by the **guest TUI**
(opencode/opencode2/…), which also normally resets them on exit. When the TUI is OOM-killed it
cannot emit its teardown (`rmcup`, disable mouse, show cursor), so the modes stay on at the
emulator even though the host termios was correctly restored. That gap — emulator modes and any
input typed into the broken TUI — is exactly what forces the manual `reset`.

**What `reset`/`stty sane`/`tput reset` actually do.** `reset` is a link to ncurses `tset`: it
writes the terminal's initialization/reset strings from terminfo (`is*`/`rs1`/`rs2`, the latter
including `rmcup` for the alternate screen and mouse-off sequences) and calls `tcsetattr` to sane
modes; `stty sane` only fixes termios (the line-discipline flags), not emulator modes; `tput reset`
emits the terminfo `rs1` string without the termios reset (`man reset`, `man tset`, `man stty`,
`man terminfo`). So a host-side auto-recovery that only restores termios (Go `term.Restore`, or the
SDK's own guard) does **not** clear the guest TUI's alternate-screen/mouse state; that needs an
`rmcup`-equivalent escape emitted to the terminal, which is what `reset` provides.

**Recovery options that do not require the user to type `reset`:**

- **Always-restore wrapper around attach.** In `session.runAttach`, take the host terminal state
  (`golang.org/x/term` `MakeRaw`/`GetState` returns a `*State` whose `Restore()` is the exact
  inverse) before the SDK call, and `defer`/`defer recover()` a restore. This covers panics but is
  largely redundant with the SDK's own guard; its real value is defence against `os.Exit` on the
  post-attach path (`reapOnLastClient`, exit rendering).
- **SIGTERM/SIGINT handler.** The launcher can `signal.Notify` SIGTERM/SIGINT and restore termios
  before exiting (SIGKILL is uncatchable). Today the launcher's only signal handling is
  `signal.NotifyContext(base, os.Interrupt, syscall.SIGTERM)` in
  `cmd/agents-sandbox/commands_cli.go`, wired for serve-only, not for attach.
- **Emit a reset sequence after attach returns.** After `AttachWith` returns and the host terminal
  is ours again, write the terminfo reset sequence (`tput reset` via `infocmp`/hardcoded
  `\e[?1049l\e[?1000l\e[?1006l\e[?25h` + SGR reset) to stdout before any launcher message. Cheap,
  no signalling, catches the guest-TUI-modes residue. Needs KVM host to confirm it clears the exact
  residue the harness leaves.
- **Run the attach under a pty multiplexer** (`tmux`, `script`). The guest TUI's modes then live on
  tmux's pty, not the user's; tmux restores the outer terminal on detach/kill. This is the most
  robust fix but changes the UX (a tmux layer the user must know about) and is a bigger surface.
- **Proactive detection of a non-restored terminal.** The launcher can check `term.IsTerminal` and
  compare termios after attach (via `term.GetState`) and, if still raw, restore it. Hard to do
  soundly while the SDK owns the fd; low value over the emit-reset option.

Analogies worth keeping in mind: OpenSSH's client puts the local tty in raw mode and restores it in
its cleanup (`clientloop.c` `enter_raw_mode`/`leave_raw_mode`, restored on a broken remote
connection); the Docker CLI does the same around `-it` hijack (`RestoreTerminal` on a deferred
path). Both restore **termios only** — none restores a guest application's alternate-screen state,
which is why "wrong modes after a killed TUI" is a class every one of these tools has.

## 3. Detection surfaces (the "detect" half)

**Memory/CPU/disk usage — the SDK `Metrics` API is the primary source and the launcher does not
use it yet.** `msbSdk.Sandbox.Metrics`/`SandboxHandle.Metrics` return a `Metrics` snapshot
(`sandbox.go`): `CPUPercent`, `VCPUTimeNs`, `MemoryBytes`, `MemoryAvailableBytes *uint64`,
`MemoryHostResidentBytes *uint64`, `MemoryLimitBytes`, `DiskReadBytes`, `DiskWriteBytes`,
`NetRxBytes`, `NetTxBytes`, `UpperUsedBytes *uint64`, `UpperFreeBytes *uint64`,
`UpperHostAllocatedBytes *uint64`, `Uptime`; `Sandbox.MetricsStream(ctx, interval)` delivers
snapshots continuously (`sandbox.go`). The docs (`sandboxes/metrics.md`) state the memory limit
"follow[s] live memory resizes", that `memory_bytes` is "the guest memory in use, measured against
the live memory size", and that the optional `upper_*` fields are the OCI writable-root
capacity/usage (so `upper_free_bytes → 0` is a root-disk-full signal). Verified: the repo has **no**
`.Metrics(`/`MetricsStream` usage (`internal/`, `cmd/`), and the launcher's `msb.SandboxHandle`
interface (`internal/sandbox/msb/msb.go`) does **not** declare `Metrics` or `Logs`. Wiring
detection therefore extends that seam. **Confirmed on a KVM host (msb 0.7.11):** on a 1 GiB idle
guest `memory_bytes`≈48 MB, `memory_available_bytes`≈1.03 GB, `memory_limit_bytes`=1 GiB; after a
300 MB tmpfs write `memory_bytes` rose to ≈393 MB and `memory_available_bytes` fell to ≈680 MB — so
`memory_bytes` is guest-in-use and `memory_available_bytes` tracks it. The `upper_*` trio was
**null while the disk was untouched** and populated (`upper_free_bytes`≈3.85 GiB, `upper_used_bytes`
=61 kB) only after the first write, so a disk signal must tolerate null. `msb modify -m 2G` returned
immediately with `STATE=converging` and a *stale* `memory_limit_bytes` (still 1 GiB on the next
read), so a live resize is **asynchronous**: a post-resize read must wait for convergence, not read
once.

**Guest kernel OOM messages — persisted logs via the `system` source.** The SDK exposes
`SandboxHandle.Logs(ctx, LogOptions)`/`Sandbox.Logs` with `LogSourceSystem`, which the docs describe
as "Lifecycle markers plus runtime and kernel diagnostics"; `logs.md` maps the on-disk files
`sandbox-dir/logs/kernel.log` = "Guest kernel and agent console output" and `runtime.log` =
"Sandbox runtime diagnostics". `handle.Logs` "works for running and stopped sandboxes and does not
require guest-agent protocol traffic" (`logs.go`). A guest kernel OOM kill is a `printk`, so it
should appear in `kernel.log`; the classic line is `Out of memory: Killed process …`. **Confirmed on
a KVM host (msb 0.7.11) that it reaches `LogSourceSystem`**: `msb logs --source system --grep`
returned `Out of memory: Killed process 468 (cat) …` and `… 469 (dmesg) …` (both `oom_score_adj:0`),
while the same lines are absent from the host-runtime stream. This is the only *direct* "it was OOM"
detector, and it names the victim (`Killed process <pid> (<comm>)`), so the launcher can attribute
*which* process died. Corollary from the same test: once the guest is saturated even a diagnostic
exec (`dmesg`) is OOM-killed, so a post-mortem read must use the persisted log, never an in-guest
probe. Still unverified: reading it through the SDK's `handle.Logs(LogSourceSystem)` from a
*stopped* sandbox (the CLI read it while running).

**Dead daemon / job crash — already partly covered.** `ensureDetachedService`
(`internal/sandbox/vm/detached_service.go`) polls the service health and fails fast on
`JobInfo.ExitCode != nil` (crash detection), and `vm.SetUpSandbox` calls `ensureDaemon(... false …)`
and `startDockerdIfPresent` on **every** `run` (not only on boot), so a daemon or dockerd that died
while no client was attached **is already re-launched on the next launch**. What is missing is
(a) the *reason* it died being surfaced, and (b) any recovery *during* an attached session.

**Disk pressure.** Three different limits with three different signals:
- **Root disk (managed OCI upper):** `Metrics.UpperUsedBytes`/`UpperFreeBytes` (guest-visible) and
  `UpperHostAllocatedBytes` (host-observed) are the usage signal; a full disk returns `ENOSPC` in
  the guest. The SDK supports **live grow-only root-disk resize** via
  `ModifyOptions.RootDiskSizeMiB` (`sandboxes/tuning.md` "Grow the root disk"). **Confirmed on a KVM
  host (msb 0.7.11):** a VM created with *no* `--root-disk` already has a **managed root disk
  (default 4 GiB)**, and `msb modify --root-disk 8G` grew it live (plan `BEFORE 4 GiB → AFTER 8 GiB`,
  EFFECT `live`, exit 0); a `--root-disk 2G` VM likewise grew live to 4 GiB. So grow does **not**
  require `--disk-size`. The launcher gap is only that `vm.createProjectVM` sets no root disk unless
  `--disk-size` is given, so the read-back `SandboxConfig.RootDisk` is not consulted and
  `reconfig.rootDiskChange` classifies a size change as recreate-only. Residual: whether the Go SDK's
  `SandboxConfig` surfaces the managed root's size for a default VM (msb's own `inspect` does).
- **`/workspace` bind quota** (`--workspace-quota`, default 16 GiB; `options.DefaultWorkspaceQuotaMiB`):
  a virtiofs guest-write quota; over-quota writes fail with `EDQUOT`/`ENOSPC` in the guest. No msb
  metric, and **not runtime-changeable** — `msbSdk.Mount.Bind{QuotaMiB}` is a creation-time mount
  property, `ModifyOptions`/`modifyPatch` carry no mount field, and `msb modify` has no mount/quota
  flag. Changing it requires a VM recreate (`reconfig.workspaceQuotaChange` adds recreate).
- **`/tmp` tmpfs** (`--tmp-size`, default 2 GiB): an in-guest tmpfs
  (`msbSdk.Mount.Tmpfs{SizeMiB}`); full → `ENOSPC` in the guest. No msb metric, and **not
  runtime-changeable** by the same API limit, so a size change is a recreate
  (`reconfig.tmpSizeChange`). (An in-guest `mount -o remount,size=…` works at the kernel level but is
  unsupported, non-persistent, and reverts on recreate.)

**Where a proposal can be surfaced.** The attached TUI owns the terminal for the whole session, so
mid-session prompting is not viable. Two viable points:
- **At next launch**, in `prepare.resolveReconfigDecision` — it already holds the live VM `handle`
  (`liveProjectVM.handle`) and already drives the `reconfig.PromptA`/`PromptB` apply/keep choice;
  this is the natural home for a "you hit the memory limit; raise `--memory`?" prompt.
- **Immediately after `AttachWith` returns**, in `session.runAttach`/`withClientSession` — the
  launcher has the terminal back and can print what happened (the daemon's `-1` exit, low headroom)
  before reaping. This is the "catch the adverse effect in-session" surface.

## 4. Host-capacity bound

`WithMaxMemory(ParseMemoryGiB(sysinfo.TotalMemoryGiB()))` makes host physical RAM the hard ceiling a
live memory raise can reach (`vm.createProjectVM`), and `vm.reconcileResourceConfig` already clamps
above it with "memory limited to N MiB (boot maximum)". Because spare memory is "backed … only when
the guest uses it" (`sandboxes/tuning.md`), *raising* the limit is cheap and does not immediately
consume host RAM; the risk is realized only when the guest actually touches the new pages. If the
host itself is over-committed (multiple project VMs each near their ceiling), the host kernel's OOM
killer picks a victim among host processes — which can be an msb runtime process (killing a whole
VM) or the launcher (leaving the terminal raw, §2). A detect-and-propose raise must therefore be
bounded by *available* host memory, not physical RAM, and should warn when other VMs or the current
host usage leave little headroom. (`sysinfo` is the existing source; no per-host pressure probe
exists today.)

## Mitigation options for this project

Ranked; each names the seam, cost/risk, and whether it is verified or needs a KVM host.

1. **(Lead) Detect-and-propose above the limit, catching the hit — `prepare.resolveReconfigDecision`
   + `reconfig` + `session.runAttach`.** Add a resource-pressure detector that: samples
   `handle.Metrics()` at prepare time and compares `MemoryBytes`/`MemoryLimitBytes` (and
   `UpperFreeBytes`) against a threshold; reads `handle.Logs(LogSourceSystem, tail)` for an OOM line;
   and records a per-project "limit hit" marker in `HomeState` so a proposal shows once. The
   proposal reuses the existing prompt machinery (`reconfig.PromptA`/`PromptB` / `ResolveReconfig`)
   to offer raising `--memory` (live, within `MaxMemoryMiB`, cheap) or `--disk-size` (recreate; or
   live grow via `ModifyOptions.RootDiskSizeMiB` for a managed root). The adverse effects are caught
   in `session.runAttach` after the attach returns: print the `-1` exit / low-headroom reason and
   emit a terminfo reset sequence. *Cost:* new `msb.SandboxHandle.Metrics`/`Logs` seam methods
   (with mocks), a new `HomeState` field, a new prompt path, and a threshold policy. *Risk:* a false
   proposal from transient high memory use; the `MemoryLimitBytes` semantics after a live resize
   ("usage can briefly be off or missing", `metrics.md`) need care. *Verified:* the API shapes and
   the seam gap. *Needs KVM host:* that a real OOM writes a parseable `kernel.log` line, and the
   threshold that predicts a kill.

2. **Re-ensure the daemon/dockerd on next launch with a reason (mostly present).**
   `vm.SetUpSandbox` → `ensureDaemon`/`startDockerdIfPresent` already restart a dead service on the
   next `run`, keyed off the health pre-check and `JobInfo.ExitCode != nil`. *Gap to close:* log the
   dead job's recorded command and `-1` exit code coherently ("the serve daemon exited by signal
   since the last run"), and warn that any running containers were stopped with a restarted dockerd
   (`detached-jobs.md` already warns on last-client detach). *Cost:* low. *Risk:* none material.
   *Verified:* the re-ensure path exists; *Needs KVM host:* an actual OOM-killed daemon being
   restarted cleanly.

3. **Protect the daemon relative to other guest processes (`oom_score_adj` / cgroup weight).**
   Raising the serve daemon's priority so the global OOM killer prefers a transient child is the
   one change that could let the session survive. *Caveat:* in opencode's model the "heavy" process
   is the serve daemon itself, so protecting it may just relocate the kill. *Cost/risk:* the
   detached-job ABI has `WithJobRlimits` (POSIX rlimits, e.g. `nofile`) but no `oom_score_adj`
   control, and the no-shell-fallback rule (`detached-jobs.md`) forbids the obvious
   `sh -c 'echo -500 >/proc/self/oom_score_adj; exec …'` wrapper because it breaks the command-line
   marker identity. A tiny dedicated helper binary would be needed. *Needs KVM host:* whichever
   process the killer actually picks, and whether protecting the daemon helps.

4. **An in-guest supervisor / init.** microsandbox's default guest PID 1 is its minimal agent
   (`init.krun` in this guest), not systemd; `--init auto` can hand PID 1 to systemd, s6, etc. A
   systemd unit `Restart=always` (or `earlyoom`/`systemd-oomd`, or a tiny loop) could restart the
   daemon without a host round-trip. *Cost/risk:* changes the base-image/init contract, duplicates
   the host-side `ensureDetachedService` supervision, and still cannot reconnect a client whose TUI
   already died; the detached-job model already means jobs die with the VM. *Needs KVM host:* the
   whole approach. Prefer (2) unless mid-session auto-restart is genuinely required.

5. **Terminal auto-recovery wrapper — `session.runAttach`.** Wrap the attach so the host terminal is
   restored and a reset sequence emitted on return, and catch SIGTERM/SIGINT to restore before
   exiting. *Cost:* low. *Risk:* double-restore with the SDK's own guard is harmless; SIGKILL stays
   uncatchable. *Verified:* the SDK guard, the absence of SDK alt-screen use, and the termios/terminfo
   split. *Needs KVM host:* that the emitted sequence clears the specific residue the harnesses
   leave.

6. **Raise the default `--memory` (and document it).** The cheapest likelihood reduction, but it
   only moves the cliff and increases host pressure; better as a *consequence* of (1)'s proposal
   than as a blanket default. `options.DefaultMemoryMiB` is `4096`.

7. **Live root-disk grow on root-disk-full detection.** Fold into (1): a full `UpperFreeBytes` can
   propose `ModifyOptions.RootDiskSizeMiB` (live, grow-only) rather than a recreate. **Confirmed on a
   KVM host:** a default VM already has a managed 4 GiB root and grows live (see §3). *Gap:* the
   launcher does not consult `SandboxConfig.RootDisk` for a default VM and `reconfig.rootDiskChange`
   still classifies a size change as recreate-only.

### Honest uncertainties

- ~~Whether guest-kernel OOM `printk` reaches `kernel.log`/`LogSourceSystem`.~~ Resolved: it does
  (see §3). The residual is only whether the SDK reads it from a *stopped* sandbox.
- ~~`Metrics.MemoryBytes` semantics for the OOM decision and behaviour right after a live resize.~~
  Resolved: `memory_bytes` is guest-in-use and `memory_available_bytes` tracks it; a live resize is
  asynchronous (`STATE=converging`, limit lags), so a post-resize read must wait (see §3).
- The SDK's own host-terminal restore (scopeguard) means a *guest* OOM kill should already restore
  termios; if the reported breakdown is reproducible with the launcher alive, the residual cause is
  the guest TUI's emulator modes, which needs host reproduction to confirm.
- The `microsandbox-workload` cgroup's exact role in the guest (it holds the workload PIDs but has
  no memory controller) — enough to conclude no in-guest memcg limit, not enough to explain why the
  controller is attached but not delegated.
