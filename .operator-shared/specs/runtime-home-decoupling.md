---
description: Design for decoupling agents-sandbox's microsandbox runtime home from any user-installed microsandbox: a private XDG data home, no user-facing msb, retired version-matching recovery (a stale runtime our own previous release left in the private home is silently force-reinstalled, partial pairs removed first), and an opt-in copy of legacy home volumes whose progress is tracked in a host-owned `migrations` registry (`runtime_home` entry with `copying`/`completed`/`dismissed` status, a `copied` set retained at a terminal word, and transient `kept`/`excluded` sets) in root `state.yaml`. The offer surfaces present private volumes of unclear status, and `volume remove [--force]` is the sanctioned cleanup path. A one-time, acknowledged post-migration notice tells upgraders their system-wide microsandbox is now optional; it treats a legacy volume as safe to remove only when it is recorded `copied` and present in the private home, so an unverified `kept` volume is still named (instruct-only, never deleted).
read_if: Implementing or reviewing runtime-home decoupling, the private `MSB_HOME`, removal of the runtime mismatch recovery menu or the silent stale-runtime repair, the legacy home-volume copy prompt and its persisted migration state, the ambiguous-state offer, the post-migration legacy-runtime notice, or the `volume remove` cleanup command.
---

# Runtime home decoupling

Date: 2026-10-15

Status: **implemented**; the copy state model is the host-owned `migrations`
registry described in "Persisted state". An earlier design tracked completion
with an in-volume sentinel and no cross-process locking; it was unsafe
(guest-controlled completion, a symlink-followable marker write, concurrent
sessions deleting each other's copy) and the registry replaced it. The registry
also never adopts a present private volume of unclear status: the "no unusable
partial" invariant, the `copied`/`kept`/`excluded` split, and the ambiguous-state
offer below are the resolution. The one successful manual ownership-mapping
verification is recorded under "Verification".

## Intent

agents-sandbox manages a microsandbox (`msb`) runtime today by sharing the
global `~/.microsandbox` home with whatever the user may have installed there.
That coupling is the source of the version-matching machinery: because both
sides share one runtime database, one binary, and one set of artifacts, a
user's `msb` and the SDK linked into agents-sandbox must agree, and the tool
resorts to upgrading, downgrading, or "brave mode" when they don't.

This design fully decouples the two. agents-sandbox keeps its runtime in a
private home of its own; a user's own microsandbox install lives side by side in
`~/.microsandbox`, and neither can see or disturb the other. The user may run
any `msb` version they like without affecting agents-sandbox, and vice versa.

## Goals

- agents-sandbox provisions and uses the exact runtime its linked SDK requires,
  in a home it owns, with no dependency on any user-installed runtime.
- The user's `~/.microsandbox` is never read or written by normal operation.
- No user-facing `msb` command is installed or advertised by agents-sandbox.
- The version-mismatch recovery menu is retired, because the mismatch it
  handled can no longer arise from the user's side; a runtime our own previous
  release left in the private home is repaired silently instead (see
  "Retire version-matching recovery, repair our own stale runtime").
- Existing users get a one-time, opt-in path to carry their old sandbox home
  volumes into the new private home, without forcing it.

## Non-goals

- Installing, exposing, or managing an `msb` for the user to run.
- Reading or writing the user's `~/.microsandbox` outside the opt-in copy
  (which reads only, and only our own name-prefixed volumes).
- A shared runtime database or any artifact crossing between the two homes.
- Matching versions between our runtime and the user's.

## Anti-goals

- Something that works, but couples the two homes such that a user's `msb`
  version change can break agents-sandbox (or the reverse).
- A user-visible `msb` binary that agents-sandbox owns and the user may come to
  depend on.
- Any prompt or behavior that nags, blocks, or loses the user's data silently.

## Constraints

- Target platforms: Linux (KVM) and macOS (Apple Silicon).
- The SDK's FFI extraction root and other operations key off `$MSB_HOME`;
  runtime resolution and installation additionally accept an explicit per-call
  home via `RuntimeConfig.Home`.

## Current state (grounding)

- `internal/sandbox/runtime/runtime.go` inspects the selected runtime before any
  SDK call and drives a recovery menu (`recoverRuntimeMismatch`) offering
  launcher upgrade, `msb` upgrade/downgrade, brave mode, issue, or quit.
- `selectRuntime` treats `MSB_HOME` as the managed home and only marks a runtime
  "external" via `MSB_PATH` / `MSB_LIBKRUNFW_PATH` / persisted paths.
- `internal/sandbox/runtime` and `internal/sandbox/msb` name the private home
  explicitly when calling `ResolveRuntime` / `EnsureRuntime` / `InstallRuntime`
  (`RuntimeConfig{Home: configpaths.MSBHome()}`), so host-runtime resolution is
  deterministic and order-independent rather than depending on `MSB_HOME` having
  been applied first (issue #160).
- The SDK's `installDir()` (`setup.go`) and the native resolver both read
  `$MSB_HOME`, defaulting to `~/.microsandbox`.
- `internal/sandbox/doctor/install.go` prints PATH hints so the user can invoke
  the installed `msb`.
- A home volume is a `VolumeKindDir` — a plain host directory at
  `$MSB_HOME/volumes/<name>`; its host-facing path is exposed by `Volume.Path()`.
- Home volumes are named `agents-sandbox-home-<slug>-<agent>-<timestamp>`
  (`internal/sandbox/volume`), and the project → volume mapping lives in
  `~/.local/state/agents-sandbox/<slug>/<agent>/state.yaml`, independent of the
  msb home.
- Launcher-global state currently sprawls across feature-named files at the
  state root (`upgrade.json`, `updater.yaml`); making root `state.yaml` the one
  launcher-global document is a separate concern, tracked in issue #141.

## Decisions

### Private runtime home

- Location: the tool's data dir, `$XDG_DATA_HOME/agents-sandbox/msb`
  (`~/.local/share/agents-sandbox/msb`). `configpaths.UserDataDir()` resolves
  `$XDG_DATA_HOME/agents-sandbox`; the private home is its `msb` subdirectory.
  Volumes are persistent user data, not
  cache, so the data dir is the right scope (not the cache dir; the state dir is
  semantically for state).
- agents-sandbox sets `MSB_HOME` to this directory at CLI startup, before any
  SDK call, so the FFI cache, runtime database, VM records, volumes, and images
  all live there. The SDK-pinned runtime is provisioned into it via the existing
  `EnsureRuntime` path.
- The private home is unconditional: there is **no external escape hatch**. The
  user cannot point agents-sandbox at another runtime, so `MSB_PATH`,
  `MSB_LIBKRUNFW_PATH`, persisted runtime paths, and the `External` /
  `Incomplete` runtime concepts are dropped. This is deliberate — supporting an
  externally configured runtime would reintroduce exactly the coupling this work
  removes. (A future opt-in could revisit it, but not now.)
- Runtime setup calls pass the private home explicitly through
  `RuntimeConfig.Home` (`configpaths.MSBHome()`), alongside `ApplyHome()` setting
  `MSB_HOME`. The explicit config makes resolution/installation deterministic and
  order-independent; `MSB_HOME` remains the home for the FFI extraction cache and
  every non-setup SDK operation. `configpaths.MSBHome` is the single source of
  truth for the private home; `runtime.PrivateHome` delegates to it so the
  low-level `msb` adapter can resolve the same home without importing the runtime
  package (issue #160).

### No user-facing msb

- agents-sandbox stops installing or advertising an `msb` for the user. The
  `doctor` PATH hints and "install msb for the user" behavior are removed. If
  agents-sandbox is the only thing installed, there is no `msb` command — that
  is expected, not a defect.

### Retire version-matching recovery, repair our own stale runtime

- The mismatch recovery menu is removed: a mismatch from the user's side cannot
  arise, because the private home is ours alone and the user's `~/.microsandbox`
  is never consulted.
- A mismatch *can* still arise from our own side, when an agents-sandbox upgrade
  bumps the SDK pin over a runtime a previous release installed into the private
  home. At startup, any non-equal relation (older, newer, unknown, or an
  incomplete pair) is repaired silently by force-installing the SDK-pinned
  runtime — the upstream installer replaces a complete pair under `Force`.
- The upstream installer **refuses to repair an incomplete pair** (`Force` does
  not either), so a stray file from a partial install is removed before the
  force-install. Removing the partial first is safe: it is unusable, and the
  installer publishes the replacement pair atomically.
- Repair is serialized across processes by a host-only advisory lock
  (`state.FlockExclusive`, `$MSB_HOME/.agents-sandbox/runtime.lock`), and the
  runtime is re-inspected under it before any removal or install. Without the
  lock, a second session acting on a stale inspection could delete the pair a
  concurrent session just repaired. The flock auto-releases on process exit, so a
  crashed holder never pins repair.
- This is the one surviving piece of version handling, and it is a repair, not a
  recovery flow: no prompt, no choice, no error under normal conditions.
- **A silent repair is still logged at verbose level** (one concise line naming
  the old and new relation), so a mismatch that triggers an unexpected
  reinstall is diagnosable without a prompt. The retirement of the recovery menu
  loses an *interaction*, not the ability to see that a repair happened (#204).

### Clean break, with stale-state safety

- On first run after the change, a project's recorded home volume no longer
  resolves in the private home. The code must detect "recorded volume missing in
  this home" and start fresh (create a new home volume), not error.
- The detection is by the recorded name not resolving in the private home, not by
  comparing against the legacy dir.
- The clean-break overwrite must not happen before the copy decision is made, or
  it would orphan the old data: resolving the home volume (which starts fresh)
  runs only after the offer below has been resolved. While the offer is pending
  and undismissed, a non-interactive session blocks before this step (see below)
  rather than overwriting the recorded reference.

### Opt-in copy of legacy home volumes

- Offered via a prompt (no dedicated subcommand). Offered on every interactive
  session start (`run`/`shell`) while the offer is both *necessary* and
  *undismissed*. Not offered on unrelated runtime-touching commands (`list`,
  `prune`, `volume`, `image`, ...): the offer only matters where a home volume is
  about to be used.
- The choice is **mandatory**: **copy**, **start fresh**/**dismiss**, or
  **quit**. There is no "decide later". The session cannot proceed with the
  decision unmade, because proceeding resolves the home volume and would
  overwrite the project's recorded reference before the old data could be copied.
  Quitting the prompt (or a prompt failure) aborts the session without a decision
  and re-offers next time.
- The opt-out option is worded **"Start fresh"** only when the migration is
  `pending` and no private volume of unclear status is present; otherwise it is
  **"Dismiss"**, because it also removes those unclear volumes (below).
- Copies **all** necessary legacy home volumes in one acceptance (the offer is
  global, not per project). Names are preserved, so the existing
  project → volume mapping in `~/.local/state` resolves unchanged in the private
  home.

#### Ambiguous state: present private volumes of unclear status

- A legacy-named volume can only exist in the private home because the campaign
  copied it (fresh homes are timestamped), and a campaign only creates it under
  `status: copying`. So a legacy-named private volume observed while the
  migration is `pending` means the registry entry was lost (e.g. root `state.yaml`
  deleted), and its contents — complete or a partial — cannot be trusted.
- When such a volume exists, the offer says so (the prompt names the situation and
  the count) and shows **two** copy/dismiss outcomes:
  - **Copy the missing volumes; dismiss the existing private volumes from the
    migration** — copies only the absent legacy targets; each present private
    volume is recorded as **kept** and left untouched (never re-copied, never
    asked about again). Terminal: `completed`.
  - **Dismiss** — removes every unclear private volume, then records `dismissed`.
- The prompt also tells the user they can **quit** and clean up manually: remove
  the private copies they want fully re-migrated with
  `agents-sandbox volume remove --force <name>` (the copy is still referenced by
  its project's state, so `--force` is required), then run again to re-copy them;
  keep the copies they do not want migrated.
- The above describes the **pending** state only. While a campaign is already
  `copying`, a present-unaccounted private volume is an unambiguous interrupted
  partial (see "Crash safety"), not a lost-registry ambiguity: resuming the copy
  discards and re-copies it. The offer therefore words the copy option as a
  **resume** ("Resume the copy — discard and re-copy the interrupted volumes, then
  copy the rest") and its notice says those volumes will be re-copied; it must not
  show the pending ambiguous-state "left untouched" promise or the
  `volume remove --force` cleanup instructions, which would misstate what the
  resume does.
- Never copies binaries, `libkrunfw`, the FFI cache, the runtime database, VM
  records, or image caches — those are re-provisioned/rebuildable. The buildkit
  cache volume (`agents-sandbox-buildkit-cache`) is likewise excluded.
- The copy is a host-side byte copy: locate the source directory, copy into a
  volume created in our private home. No shell-out to any `msb`, and no throwaway
  VM — a foreign home's volume cannot be mounted by our runtime anyway.
- Source selection is by name, not by our per-project state: any directory under
  `~/.microsandbox/volumes/` matching `agents-sandbox-home-*` that is absent from
  the private home (`runtime.LegacyHome()` resolves the legacy home, `~/.microsandbox`).
  No layout guessing beyond this convention.
- Failure scope governs exclusion (see also the "no unusable partial" invariant
  under "Host-side copy mechanics"):
  - A **volume-scoped** failure (source unreadable, a file's permission denied,
    ENOSPC, or a chmod/copy error on one volume) is recoverable: the volume's
    partial destination is removed, the volume is **excluded** from the campaign,
    the remaining volumes are copied, and the campaign can still reach
    `completed`. The legacy source is never written, so an exclusion costs only
    that volume's data. At finalization the campaign warns, naming the volumes it
    could not copy.
  - A **campaign-scoped** failure (writing `state.yaml`, taking the lock, or
    listing the homes) aborts the session and leaves the campaign open
    (`copying`) to resume next time. A campaign-scoped failure aborts rather than
    excluding the volume, because it cannot guarantee the partial destination was
    removed.
- A clean break (user chooses "Start fresh"/"Dismiss") is recorded as the
  dismissal: the legacy volumes remain, so without a persisted decision the offer
  would keep returning.
- `--yes` and non-interactive runs never accept the copy and cannot make the
  decision, so while the migration has volumes to copy or unclear volumes present
  and is undismissed they **block before starting a fresh home** (returning
  `runtimemigration.ErrMigrationPending`), printing
  guidance to run an interactive `run`/`shell` once or clean up with
  `agents-sandbox volume remove`. `--yes` is treated as non-interactive for this
  offer, matching the precedent that `--yes` does not auto-select the (now
  removed) runtime recovery action. A multi-gigabyte copy must never be triggered
  implicitly, and the recorded reference must never be silently overwritten.

### `volume remove` (cleanup path)

- `agents-sandbox volume remove <name>...` (alias `rm`) deletes private home
  volumes by name. It never prompts, and refuses when a volume is still
  referenced by a project `state.yaml` (naming the referencing projects), so a
  project's recorded home is never silently orphaned; `--force` overrides. This
  command exists so the ambiguous-state prompt has a sanctioned way to remove a
  copy that the user wants fully re-migrated. The reference scan is fail-closed:
  an unreadable or corrupt project state file blocks the removal (use `--force`).

### Host-side copy mechanics

- The copy runs as the invoking user and does **not** chown on the host. A host
  `chown` would require root and a host `dev` user and is impossible on macOS, so
  it cannot be the mechanism here.
- This aligns with the rest of the design: the runner image creates `dev` with
  the **host's** uid/gid (`USER_UID`/`USER_GID` build args fed from
  `os.Getuid()`/`os.Getgid()` in `internal/sandbox/image`), so host-owned files
  and in-VM `dev` share a uid — the same reason the `/workspace` bind mount needs
  no chown. The home volume is mounted without an `Owner` override
  (`internal/sandbox/vm`), so the guest owner presented for host files comes from
  the runtime's mount default (stat-virtualization overlay / fallback owner).
- **Verified manually (2026-10-16):** this presentation does make a copied
  volume readable and writable as `dev` end-to-end, so no ownership-mapping step
  is needed (see "Verification"; a single successful KVM run, not an automated
  proof). The `chown -R dev:dev` in the throwaway-VM copy path
  (`internal/sandbox/volume/volume.go`) is a **guest-side** normalization to
  `dev` after a guest-side copy — consistent with `dev` being the owner, not
  evidence of a host→guest translation.
- A recursive copy preserves file mode and modification time; symlinks are
  recreated as symlinks, never followed.
- **No unusable partial (invariant):** the home resolver (`ResolveHomeVolume`)
  adopts any present private volume whose name matches a project's recorded home,
  so the migration must never leave a private volume under a migration-target name
  that is neither a finished copy nor a deliberate keep. Concretely:
  - An **automated** exclusion (a copy failure) is recorded in `excluded` **only
    after** the partial destination is confirmed removed; if removal fails, the
    campaign aborts (campaign-scoped, stays `copying`) rather than completing with
    a usable partial left behind.
  - A **user-chosen** keep (`kept`, from "copy the missing volumes") deliberately
    leaves the present volume in place; it is disclosed in the option label. Its
    contents are unverified, so the notice still warns about the legacy volume
    behind it.
  - **Start fresh / Dismiss** removes every present target not in `copied` and not
    in `kept` (i.e. the unclear partials) before recording `dismissed`; a removal
    failure aborts the dismissal.
  - A campaign reaches `completed` only when no such partial remains.
- Interruption handling: a volume-scoped copy failure removes that volume's
  partial destination and records the volume in `excluded`, so it is not retried
  this campaign while the others proceed. Previously completed volumes are
  recorded in `copied` and simply stop being necessary; user-kept volumes are
  recorded in `kept`.
- Cross-process safety: the campaign holds the shared host-only root-state lock
  (`state.WithRootState`, the same lock every launcher-global writer takes) for
  its duration and (re-)derives necessity under the
  lock, so a second session sees the in-flight status and never discards or
  re-copies a volume another session is copying. The root lock is deliberately
  held across the msb copy I/O, so a non-blocking acquirer (the upgrade check,
  the legacy updater fold) defers to a later run rather than racing a live copy.
- Crash safety: a copy stopped by a crash or kill runs no cleanup, but the
  persisted status is `copying`, so the next session treats every present
  private volume not in `copied`/`kept`/`excluded` as an unambiguous partial: it
  discards and re-copies exactly those, and leaves accounted-for volumes alone.
  Completion is a host-owned record, never a file inside the guest-writable
  volume.
- Symlink safety: the copy recreates symlinks without following them, and after
  the copy the host performs **no** further write into the copied tree (the
  completion record is host-side), so a symlink planted in a legacy volume
  cannot redirect a host write onto a host file outside the volume.
- Listing failure while a campaign is open: during a `copying` campaign a legacy
  `volumes/` directory that is *missing* counts as empty, so the campaign may
  finalize `completed`. A `volumes/` directory that exists but cannot be listed
  (permission denied, not-a-directory, or an I/O error) is a **campaign-scoped**
  failure: the campaign must not finalize, leaves `status: copying`, and retries
  next session. The offer explains the situation and tells the user to fix it
  manually and run the migration again (the same "quit and retry" semantics).
  This keeps a transient permission problem from permanently marking an
  under-migrated campaign complete. (Outside a campaign, in the `pending` state, a
  missing or unreadable legacy home still counts as "nothing usable to copy", and
  the offer warns and skips rather than blocking.)

### Persisted state

- **Necessity is derived, never stored.** Legacy necessity means a directory
  matching `agents-sandbox-home-*` under `~/.microsandbox/volumes/` whose name is
  not completed in the private home. The migration *status* only changes what
  "necessary" means (below); it is not itself a necessity record.
- The migration decision and any in-flight progress live in root
  `~/.local/state/agents-sandbox/state.yaml`, under a **`migrations` registry**
  keyed by migration name, so a future one-time migration adds a key without
  changing `RootState`'s shape:

  ```yaml
  migrations:
    runtime_home:
      status: copying        # copying | completed | dismissed
      copied:                # fully migrated; retained at a terminal word
        - agents-sandbox-home-1mjusbm3wikhb0
      kept:                  # present only while status: copying; user-dismissed, left in place
        - agents-sandbox-home-1mjusbm3wikhb1
      excluded:              # present only while status: copying; copy failed, destination removed
        - agents-sandbox-home-1mjusbm3wikhb2
  ```

- `status` is the only mandatory field; **absence of the `runtime_home` entry is
  the `pending` state** and is always derived, never written:
  - **pending** (absent) — offer if any legacy name is not present in the private
    home, or any present private volume is a legacy target (unclear status).
  - **copying** — a campaign is open. Necessary = legacy names minus the
    accounted-for names (`copied` ∪ `kept` ∪ `excluded`). A present private
    volume not accounted for is a partial: discard and re-copy. `copied` gives
    exact per-volume resume; `kept` records user-dismissed volumes; `excluded`
    stops a failed volume from being retried.
  - **completed** — campaign over, no offer, permanent.
  - **dismissed** — user chose start-fresh/dismiss, no offer, permanent.
- `kept` and `excluded` are transient: written only while `copying` and dropped
  when the campaign reaches a terminal word. `copied` is retained at the terminal
  word as the host-owned record of the volumes that were fully migrated, which
  the post-migration notice uses to tell which legacy volumes are safe to remove.
  Terminal words are permanent with no re-arm (unlike onboarding's source-hash
  re-arm); after `completed` or `dismissed`, agents-sandbox never re-offers and
  never re-probes.
- Concurrent sessions are serialized by the shared host-only root-state lock
  (`state.WithRootState`; the private `migration.lock` was retired in its favor).
  The flock auto-releases on
  process exit, so a crashed holder never pins the campaign. Necessity is
  (re-)derived under the lock; a second session therefore sees `copying` and
  never clobbers a live copy. The two terminal writes — the finalize to
  `completed` and the `dismissed` write — take the same lock and re-read the entry
  before writing, so a dismissal never overwrites a campaign that just finished
  (and vice versa).
- Completion is **host-owned**, never a file inside the guest-writable volume.
  The original design's in-volume `.agents-sandbox-copy-complete` sentinel is
  removed: it lived in a directory the guest can write, could be deleted to make
  a finished home look interrupted (a silent data-loss path), and its
  `os.WriteFile` followed a copied symlink to truncate a host file. The
  `migrations` entry replaces it as the sole completion record.
- Only the terminal words are long-lived (one line per migration the tool ships,
  not per run). There is no new state file: the registry lives in the already-
  existing root `state.yaml`, and the campaign reuses the shared root-state lock
  rather than adding a path. Because the PR had not shipped, the previous
  `runtime_home_migration.dismissed` key is replaced outright — no compat
  shim.

### Post-migration legacy-runtime notice

- Purpose: tell a ≤0.5.0 upgrader that agents-sandbox now runs its own isolated runtime
  and no longer uses or requires `~/.microsandbox`, so their system-wide microsandbox is
  now optional. This is the payoff of the whole decoupling and the only place a user
  learns it while it is true and they are present.
- **Audience: only upgraders with evidence agents-sandbox used the legacy home.** The
  notice never fires for a fresh install, nor for a user whose `~/.microsandbox` is their
  own unrelated microsandbox. Detection is **file-based** over the legacy home: it fires
  when `<legacyHome>/volumes` contains an `agents-sandbox-home-*` entry, or when
  `<legacyHome>/sandboxes` contains an `agents-sandbox-vm-*` / `agents-sandbox-task-*`
  entry (microsandbox's default per-sandbox state directory, `paths.sandboxes`). A missing
  or unreadable directory counts as absent; a bare `~/.microsandbox` with none of our
  artifacts is silent.
- **Instruct-only; never deletes.** agents-sandbox must not delete the legacy home: it is
  shared and may hold the user's own microsandbox data, and silent data loss is an
  anti-goal. The notice names both tiers and gives the exact commands, so the user acts
  deliberately: our own `~/.microsandbox/volumes/agents-sandbox-home-*` and
  `~/.microsandbox/sandboxes/agents-sandbox-{vm,task}-*` are safe to remove (selective
  `rm -rf`), whereas the whole `~/.microsandbox` is removable only if microsandbox was
  installed solely for agents-sandbox (full `rm -rf`). Only the tier(s) actually present
  are shown.
- **Explicit acknowledgment.** The notice requires the user to react (press a key) — it is
  a deliberate, once-ever moment, not scroll-past output. It needs a new "acknowledge"
  primitive on the `termio.UI` seam (real printer + `Mock`); `Confirm`/`Input` semantics
  do not fit. The primitive prints unconditionally: it is not gated by the level or
  `--quiet`, so a once-ever, interaction-required moment is never silently suppressed. It is
  the single presenter for both modes: interactive prints the full notice and waits,
  non-interactive prints the one-line form and returns immediately.
- **Trigger points** (interactive only):
  - immediately after a copy campaign finalizes `completed` (`finalizeMigration`);
  - immediately after the user chooses dismiss (`dismissMigration`);
  - on a later session when the migration is already terminal but our artifacts remain in
    the legacy home and the notice was never acknowledged;
  - in the `pending` state when there is nothing to copy but our artifacts are present.

  All four live in `runtimemigration.Offer`: it no longer returns early on a terminal
  migration, and runs the notice after the copy/dismiss decisions and on the terminal /
  nothing-to-copy fallthrough. The notice is best-effort — a state-read, detection, or
  acknowledgment failure warns and lets the session continue.
- **Uncopied volumes.** A legacy volume is *uncopied* unless the migration recorded it
  `copied` **and** its destination still exists in the private home. That covers a volume
  with no private counterpart (a failed copy, or one never attempted) and a volume whose
  private counterpart exists but was not recorded as `copied` — a user-chosen `kept`
  volume, whose contents are unverified. **Presence in the private home is not proof of a
  copy**: a `kept` volume shares its name with a present, unverified private volume, which
  is exactly the state the ambiguous-state copy path creates. Requiring both signals is
  what keeps the notice from advising the deletion of the only trustworthy copy. This is
  derived at render time from the two home listings and the retained `copied` record —
  never from the transient `kept`/`excluded` sets, which are dropped at the terminal write
  and are unavailable on a later session or on the dismiss path. The notice leads with a
  "do not remove" paragraph naming the uncopied volumes as not copied and still holding
  data, left in place, and then
  suppresses every command that would delete them: both the wholesale
  `rm -rf ~/.microsandbox` and the selective `~/.microsandbox/volumes/agents-sandbox-home-*`
  glob match an uncopied volume. The safe sandbox globs are still offered, and the volumes
  glob is replaced by a note to remove the remaining volumes individually. A failure to list
  the private home suppresses the destructive commands (treats every legacy volume as
  uncopied) rather than risk printing one that would delete data. An unreadable legacy
  `volumes` directory is handled conservatively for the same reason: it still counts as
  absent for *firing* detection, but while it cannot be listed the notice withholds the
  wholesale command too, because unlisted home volumes may still hold data. The notice
  reads the persisted registry itself at render time rather than a snapshot taken by
  `Offer`, so a campaign that finalized moments earlier in the same session is reflected
  (no over-warning) and the derivation still works on the dismiss path and on later
  sessions. Names printed by the notice are sanitized (control characters, including
  newlines and ESC, are stripped), so a hostile legacy-home directory name cannot forge
  notice lines or emit terminal escapes.
- **Persisted once-ever flag.** A second `migrations` registry entry,
  `legacy_runtime_notice`, records acknowledgment as a new terminal `acknowledged`
  status; absence is the pending state. This keeps the "absent = pending, terminal =
  permanent" semantics of the registry rather than adding a bool to `runtime_home`. The
  value's presence is the signal — `Offer` reads the entry only for its existence, never its
  status — so `acknowledged` documents intent; it is never written to the `runtime_home`
  entry. The acknowledgment is written in its own root-state transaction *after* the
  keypress (re-reading first, and a no-op when the entry already exists), so the root lock
  is never held across the user interaction.
- **Non-interactive sessions.** They cannot acknowledge, so they print a bounded one-line
  informational form (no removal commands) through the same ungated primitive and must not
  mark the notice acknowledged; an interactive session still surfaces the full notice. This
  applies under `--yes` too, which is non-interactive: it gets the line, never the prompt.
- Discoverable homes for the same fact (for users who never see the notice): a dedicated
  `docs/install/upgrading.md` page (the "Upgrading to 0.6" page, which owns the migration
  mechanics and the uninstall payoff — issue #208), README positioning ("no system-wide
  microsandbox required"), the CHANGELOG entry, and `doctor` reporting the private home.

### State-file placement (related, separate)

- Folding launcher-global state into a single scope-based `state.yaml` is issue
  #141. This work only adds its `migrations.runtime_home` entry to root
  `state.yaml`; it must not introduce a feature-named file of its own.

## Left to the builder

- Exact wiring of `MSB_HOME` at startup and any interaction with the SDK FFI
  auto-loader; confirm nothing in the tool's own code still assumes
  `~/.microsandbox`.
- The precise provisioning call and where it runs in the startup sequence.
- Removing the external-runtime handling (`MSB_PATH` / `MSB_LIBKRUNFW_PATH` /
  persisted paths / `External` / `Incomplete`) and the recovery menu, while
  keeping any internal sanity checks that should remain.
- Where the copy prompt hooks into the session start (`run`/`shell`) path.
- The stale-state detection ("recorded volume missing") and where it lives.
- `doctor` changes after removing the runtime prerequisite/hints.
- macOS-specific path and behavior confirmation.
- Testing strategy, including a fake legacy home for the copy path.
- Docs and `CHANGELOG.md` updates per the repo's documentation rules.

## Verification

- **Manually verified on KVM (2026-10-16), single run:** a legacy home volume
  copied host-side (no `chown`) was readable and writable by the in-VM `dev`
  user, and the 0.5.0 upgrade → migration-offer → copy path completed. This
  confirms the build-time `dev` uid/gid alignment + mount owner presentation
  assumption; no ownership-mapping step is needed. The evidence is one
  successful manual run, not a repeated or automated proof.
- Remaining (optional hardening, not release-blocking): an automated KVM
  integration test covering the cross-uid case (an image built under one host
  uid reused by a runner under another) and the default `HostPermissions:
  Private` mount policy.
- Tests that resolve the private home isolate `configpaths` (`configpaths.
  WithMockConfigPaths(t)`), **not** just `HOME`. Resolution is XDG-based, so a
  host `XDG_DATA_HOME` that points at a real `agents-sandbox/msb` would
  otherwise satisfy inspection and bypass the path under test.

## Open question deferred to the builder

- Whether a terminal decision should ever be reversible in-product. This design
  says no; the `migrations.runtime_home` entry already carries a `status`, so a
  future re-arm is a status change (e.g. back to `pending`), not a new field or
  file.

## Relationship to other specs and issues

- Supersedes the runtime-sharing assumptions behind the mismatch-recovery
  behavior described in the troubleshooting docs.
- Depends on the state-by-scope refactor only in that its key lives in root
  `state.yaml`; the key itself can be added before #141 lands if the root file
  is created for it.
- Issue #141: organize persisted state by ownership scope.
