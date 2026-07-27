# One VM per project — shared history + parallel branch isolation

## Problem

Every `opencode-msb` invocation currently spawns its own ephemeral microsandbox
VM but **shares the same home volume** (`/home/dev`) across sessions for a
project. opencode stores session state in a SQLite database in the home dir and
locks it with `fcntl()`. `fcntl()` locks do **not** work across separate VM
kernels, so parallel sessions corrupt the SQLite db and break opencode in the
VMs.

The current workaround is **clone-on-use**: when a second session starts for
the same project while another is using the home volume, the home volume is
snapshotted into a clone; the clone session's history is **not** persisted.
History is shared across branches/worktrees only because every session
bind-mounts its working tree at `/workspace` (same cwd → same opencode
project_id).

## Goal

Move to **one long-lived VM per project** that hosts multiple parallel opencode
sessions, while:

- preserving **shared session history across branches/worktrees** (current
  behavior the user values), and
- enabling **parallel branch isolation** (two agents on two branches at once
  without file conflicts).

Good UX is valued above implementation complexity.

## Key findings (grounding)

Traced in opencode source (`anomalyco/opencode`, branch `dev`) and the msb SDK:

1. **opencode keys sessions by `project_id`**, resolved from the working
   directory (the git worktree path) via `Project.fromDirectory` →
   `projectV2.resolve` (`packages/opencode/src/project/project.ts`). Different
   working directories → different projects → no shared history. Issue
   [#15797](https://github.com/anomalyco/opencode/issues/15797) shows even
   `opencode import` is broken, so export/import is not a viable workaround.

2. **The only mechanism that yields both parallel branch isolation AND shared
   history** is opencode's experimental **workspaces** feature
   (`OPENCODE_EXPERIMENTAL_WORKSPACES`). Its `worktree` module
   (`packages/opencode/src/worktree/index.ts`):
   - creates **linked git worktrees** under
     `~/.local/share/opencode/worktree/<project-id>/<name>` (created from the
     project's primary worktree, so they share the repo's `.git`),
   - registers each worktree as a **"sandbox" of the same project** via
     `project.addSandbox(ctx.project.id, info.directory)` → same `project_id` →
     **shared session history**,
   - boots a **separate opencode instance per worktree** (`InstanceStore.load`),
   - tracks workspaces per project (`WorkspaceTable` with `project_id`).

3. **Within one VM/kernel, multiple opencode processes sharing one SQLite db is
   safe** — `fcntl()` locks work across processes in the same kernel. This is
   the root fix for the corruption.

4. **msb natively supports the one-VM-per-project model**
   (`microsandbox/sdk/go` v0.6.6, `sandbox.go`):
   - `SandboxHandle.Connect(ctx)` reattaches to a **running** VM and returns a
     live `*Sandbox` with `Attach`/`Exec`/`FS` (sandbox.go:589).
   - `WithDetached()` / `StartSandboxDetached` keep a VM alive in the
     background after the handle is released; `Sandbox.Detach` releases a
     handle without stopping the VM (sandbox.go:344, :704).
   - `WithIdleTimeout` (consumed as `IdleTimeoutSecs`, sandbox.go:74) +
     `Touch` (:580) provide auto-shutdown / keepalive.
   - Multiple concurrent `Connect`+`Attach` calls yield concurrent interactive
     PTY sessions into one VM (separate SDK handles / host processes attaching
     to the same running sandbox).

## Decision

Approach 1 with **clients in the VM (1a)**:

- One long-lived VM per project; `opencode serve` daemon (workspaces on) owns
  the SQLite db + MCP servers + workspaces control-plane.
- Each invocation attaches a thin **TUI client running in the VM** via msb
  `Attach` → `opencode attach http://127.0.0.1:4096 --dir <target>`. The host
  runs only `opencode-msb`; no host opencode install, no host↔VM networking.
- Branch isolation via opencode's native linked worktrees (same project_id →
  shared history). opencode-msb's host-side managed-clone/merge machinery is
  removed; `-b <branch>` becomes a thin wrapper over opencode worktree
  create/open.

Rejected alternatives: per-invocation opencode processes with no daemon
(Approach 3 — relies on multi-process SQLite, per-invocation cold boot),
clone-into-VM repo (Approach 2 — host edits not instantly visible). The
opencode server always runs in the VM; tools execute there against the project
dir, so the repo stays in the VM regardless of where the TUI client runs.

---

## Section 1 — Validation spike (go/no-go gate)

Everything rests on the experimental workspaces feature behaving as traced
above. A minimal spike de-risks it **before** building the full design. In
**one** VM (home volume + bind-mounted `/workspace`), with
`OPENCODE_EXPERIMENTAL_WORKSPACES=true`:

1. Start `opencode serve`. Create a worktree (workspace) for branch A; start an
   instance in it.
2. Create a second worktree for branch B; start an instance.
3. **Shared history:** confirm a session created in worktree A is visible in
   worktree B, and both worktrees resolve to the **same `project_id`** (check
   `opencode db` / `project` table).
4. **SQLite safety:** run both instances concurrently; verify db integrity
   afterward.
5. **Bind-mount compatibility:** confirm linked worktrees created from a
   bind-mounted `/workspace` (host `.git`) function — worktree metadata lands in
   the bind-mounted `.git/worktrees`, working dir in `/home/dev`.
6. **Control surface:** identify the exact opencode command/API to create/open a
   worktree and attach a TUI to it (needed for the host `-b` mapping).

If shared-history or bind-mount compatibility fails, fall back to Approach 3
(per-invocation, no daemon) or revisit. **The spike is sequenced first in the
implementation plan.**

## Section 2 — Architecture

**One long-lived VM per project**, named `opencode-msb-vm-<slug>` (new `-vm-`
infix; replaces today's per-branch `-sb-` session sandboxes). Created with
`WithDetached()` + `WithIdleTimeout`; reused via `GetSandbox`→`Connect` (or
`Start` if stopped). Mounts unchanged in shape: `/home/dev` = named home volume
(existing `EnsureHome`), `/workspace` = host-repo bind mount, `/tmp` tmpfs.

**opencode serve daemon** starts on first boot (background process in the VM,
`127.0.0.1:4096`), healthchecked via `/global/health`. It owns the SQLite db +
MCP servers + the workspaces control-plane. Config provisioning
(`provisionSandbox`) adds `OPENCODE_EXPERIMENTAL_WORKSPACES=true`.

**Each invocation:** ensure VM running → ensure daemon healthy →
`Connect`+`Attach(bash -lc 'opencode attach http://127.0.0.1:4096 --dir
<target>')`. Target = `/workspace` by default, or a worktree dir for
`-b <branch>`.

**Branch isolation = opencode's native linked worktrees** (under
`~/.local/share/opencode/worktree/<project-id>/`), registered as the same
project's sandboxes → shared history; separate working trees → parallel branch
isolation. All within one kernel → fcntl-safe SQLite.

**Removed/simplified:** the clone-on-use workaround
(`ensureNoSameHomeSession`/`CloneVolume`), the host-side managed-clone/merge
machinery (`resolveWorkspace` branch path, `EnsureManagedRepoFromRef`,
`cleanupManagedRepo`, `MergeBranchInto`, managed-workspace dir), and
same-branch-VM conflict detection — replaced by "ensure one project VM running;
multiple attaches are fine."

**Concurrency model:** one daemon owns the db; N TUI clients attach (parallel
sessions); worktree instances managed by the daemon's control-plane.

**Lifecycle commands:** `run` (default), `shell` (debug, kept), new `stop` /
`kill`, `list` (filter `opencode-msb-vm-`), `build` (kept).

## Section 3 — Components (mapped to the codebase)

| Component | File | Change |
|---|---|---|
| **A. Project VM lifecycle** | new `internal/sandbox/projectvm.go` (or fold into `runner.go`) | `EnsureProjectVM`: `GetSandbox`→ NotFound=`CreateSandbox(WithDetached,WithIdleTimeout,mounts,env)` / Running=`Connect` / Stopped=`Start`. Returns live `*Sandbox`; does **not** stop on exit. `projectVMName(slug)="opencode-msb-vm-"+slug`. |
| **B. Daemon supervisor** | new `internal/sandbox/daemon.go` | `EnsureDaemon`: healthcheck via `sb.Exec("curl",…,"http://127.0.0.1:4096/global/health")`; if down, start `opencode serve --hostname 127.0.0.1 --port 4096` detached (nohup or msb `WithInit`/`Scripts`) and poll. Healthcheck runs curl **inside** the VM — no host↔VM networking. |
| **C. Invocation flow** | `runner.go` (`Run`,`Shell`,`prepareSandbox`,`sandboxSession`) | `Run`→ EnsureProjectVM+EnsureDaemon → `Attach(bash -lc 'opencode attach http://127.0.0.1:4096 --dir <target>')`. `cleanup` stops removing the VM — only `Close`/`Detach`. Drop `cloneVol`. |
| **D. Branch→worktree mapping** | new `internal/sandbox/worktree.go` | `ResolveTarget(sb,branch)`: no branch→`/workspace`; `-b <branch>`→ create/open an opencode worktree via the daemon, return its dir. Replaces `resolveWorkspace`+`EnsureManagedRepoFromRef`. |
| **E. Config provisioning** | `runner.go` (`createSandbox`,`provisionSandbox`) | Add `OPENCODE_EXPERIMENTAL_WORKSPACES=true` to env. Keep config files, `.envrc` removal, secrets, `startDockerdIfPresent`. |
| **F. Naming & filters** | `query.go`, naming strategy | Session filter `opencode-msb-sb-`→project-VM filter `opencode-msb-vm-`. `list` shows VMs. |
| **G. Lifecycle commands** | `cmd/opencode-msb` | New `stop`/`kill`; `list` filter; `run`/`shell` new flow; `-b`→Component D. |
| **Removed** | `volumes.go` (`CloneVolume`,`cloneVolumeName`,`sameHomeVolumeInUse`), `runner.go` (`ensureNoSameHomeSession`,`resolveWorkspace` branch path,`cleanupManagedRepo`,`handleUncommittedChanges`,`handleRepoCleanup`), `git.go` (`EnsureManagedRepoFromRef`,`RemoveManagedRepo`,`MergeBranchInto`,`AbortMerge`,`HasUncommittedChanges`,`CommitAll`,`DiscardAll`) | Clone-on-use + host-side managed-clone/merge machinery — replaced by opencode worktrees. |

## Section 4 — Data flow

- **First run (no VM):** `EnsureImage` → `EnsureHome` → `GetSandbox`=NotFound →
  `CreateSandbox(WithDetached,WithIdleTimeout,mounts,env)` (returns a live
  handle) → `provisionSandbox` + `startDockerdIfPresent` → `EnsureDaemon`
  (start `opencode serve`, poll health) → `ResolveTarget` →
  `Attach(opencode attach --dir <target>)` → on exit `Detach` (VM persists in
  the background).
- **Subsequent run (VM running):** `GetSandbox`→Running→`Connect` →
  `EnsureDaemon` (healthcheck, restart if down) → `ResolveTarget` → `Attach` →
  `Close`.
- **Parallel sessions:** N invocations each `GetSandbox`→`Connect`→`Attach`
  independently against the same daemon; shared db (one kernel). **First-boot
  race** (two invocations both see NotFound and `CreateSandbox` clobber via
  `WithReplace`): guarded by a **per-project host-side flock** in `StateDir`
  around ensure-create.
- **VM stopped (idle) then run:** `GetSandbox`→Stopped→`Start`→`EnsureDaemon`
  restarts the daemon (it did not survive VM stop).
- **`-b <branch>`:** `ResolveTarget`→ create/open worktree via daemon →
  `Attach(opencode attach --dir <worktree>)`. Worktree persists in `/home/dev`
  across sessions.

## Section 5 — Error handling

- **VM died/crashed:** `GetSandbox`→Stopped/Crashed→`Start` (reboot);
  `EnsureDaemon` restarts the daemon. Unrecoverable bad state → `stop`/`kill`
  + recreate.
- **Daemon unresponsive:** healthcheck fails → kill stale `opencode serve`
  (pidfile or `pkill`) and restart; poll with backoff. Repeated failure surfaces
  a clear error (e.g. opencode version lacks workspaces — spike territory).
- **Worktree creation failure:** surface error; MVP aborts with a clear message.
  Exact failure modes come from the spike.
- **Bind-mount `.git` incompatibility (spike #5):** if opencode can't make
  linked worktrees from a bind-mounted `/workspace`, fall back to cloning
  `/workspace` into `/home/dev` as the primary worktree (so `.git` is fully
  in-VM). Spike-gated.
- **First-boot race:** per-project host-side flock in `StateDir` around
  ensure-create (Section 4).
- **Idle-timeout race (VM auto-stops mid-`EnsureDaemon`):** retry the
  ensure-state once on transient `Connect`/`Start` failure.
- **Attach errors:** non-zero `opencode attach` exit → surface as `ExitError`
  (like today).
- **Multiple projects:** independent VMs (distinct name, home volume, image) —
  no cross-project interference. Secrets/`.envrc` handling unchanged.

## Section 6 — Testing

- **Unit tests on pure functions with mocks** (per AGENTS.md):
  `projectVMName`; the ensure-state decision (NotFound/Running/Stopped → action)
  given a mocked handle/status; `ResolveTarget` (no-branch→`/workspace`);
  env-map building (assert `OPENCODE_EXPERIMENTAL_WORKSPACES=true`); filter
  prefixes.
- **Daemon supervisor:** healthcheck logic with a mocked `sb.Exec`
  (healthy/unhealthy) and start+poll retry/backoff.
- **Integration tests** (`//go:build integration`, require msb — pattern from
  `integration_test.go`): create project VM → ensure running → connect → ensure
  daemon health → attach a trivial command → stop/reconnect. The spike doubles
  as the core integration validation (worktrees share `project_id`).
- **Verify:** `go test ./...`, `golangci-lint fmt`, `golangci-lint run`,
  `go run ./cmd/opencode-msb --dry-run` (note: `--dry-run` semantics shift to
  "ensure VM + daemon, skip attach").

## Section 7 — Migration, cleanup, open questions

- **Obsolete artifacts:** clone volumes (`opencode-msb-clone-`), per-branch
  session sandboxes (`opencode-msb-sb-`), managed-workspace dirs under
  `StateDir`. `doctor` warns about orphans; no auto-migration.
- **Naming strategy:** document the new `-vm-` infix; update the
  naming-strategy spec's table.
- **`-b` UX:** keep `-b <branch>` as a thin wrapper over opencode worktree
  create/open (familiar); branch removal via opencode's `worktree remove`;
  merge-back is a git op the user does (opencode-msb stops doing managed-clone
  merge). Optional `opencode-msb worktree remove` later (out of MVP).
- **Open risks (spike-gated):** experimental workspaces stability; workspaces
  runtime model (one server vs multiple); worktree-with-bind-mount (#5); pin an
  opencode version that ships the workspaces feature in the runner image.
- **Unchanged limitations:** `.envrc` not hidden from VM; network egress
  unrestricted.
