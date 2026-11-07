---
description: Contract for how the launcher persists state - launcher-global state is one root `state.yaml` document (with the `updates:` namespace and silent legacy folds), every read-modify-write goes through one locked transaction per document (`WithHomeState`/`RecoverHomeState`/`WithRootState`/`WithKeyLocks`/`RemoveHomeStateIf`), locks live under `<root>/locks/` and are never deleted, and `internal/atomicwrite` makes each write crash-durable with an installed-write error marker.
read_if: Adding or changing launcher-global persisted state or the root `state.yaml` shape; changing any project/launcher state read-modify-write; the state locking primitives or recovery; `internal/upgrade`'s state seam; the `upgrade.json`/`updater.yaml` folds; or `internal/atomicwrite` and the callers that roll back a resource after a failed state write (the volume home actions).
---

# State model

## Documents and scope

Launcher-global state is **one document**, root `state.yaml`. A new launcher-global decision becomes a **key/section inside that document, never a new file**, so the root file count stays fixed as features are added.

| Scope | Cardinality | Where |
|---|---|---|
| launcher-global | 1 | root `state.yaml` |
| per-agent | few | `<root>/<agent>/…` |
| per-project+agent | grows with projects | `<root>/<slug>/<agent>/state.yaml` |

The per-project+agent layout and the per-agent `config-migration.yaml` scope (`<root>/<agent>/config-migration.yaml`) are unchanged — `config-migration.yaml` is already scope-based.

### Root document shape

```yaml
migrations:                 # unchanged; runtime_home, legacy_runtime_notice
  runtime_home: { … }
updates:
  launcher:                 # was upgrade.json (internal/upgrade)
    last_checked: "2026-10-21T09:00:00Z"
    dismissed_versions: ["1.5.2"]
  agents:                   # was the top-level `agents:` key
    opencode:
      last_checked: "2026-10-21T09:00:00Z"
      latest_version: "0.8.0"
```

- One `updates:` namespace, children `launcher` (agents-sandbox itself, from the retired `upgrade.json`) and `agents` (update-check state for the tools baked into the runner image).
- Field name is `last_checked` everywhere; `upgrade.json`'s `last_check` is gone.
- `dismissed_versions` ("the user said do not notify me") and the per-agent `latest_version` cache keep their existing semantics.
- `updates:` and its `launcher`/`agents` children omit themselves when empty via an `IsZero()` method (yaml.v3 honours `IsZeroer` for `omitempty` on a value struct; `time.Time` is handled natively).

This document owns the **shape and placement** of launcher-global state; `upgrade-state.md` owns the **semantics** of the agent check state. The `RootState.updates.agents` field path is the current shape.

### Backwards compatibility (silent, no user interaction)

Three legacy sources fold into `updates:`. Nothing prompts. For scalars a present new value wins over the legacy one; for the list-valued `dismissed_versions` the fold is a **per-element union**, so a user's "don't ask again for this version" is never lost by the migration (the fold runs before anything writes `updates.launcher`, so the union is normally a straight copy).

1. **`upgrade.json` → `updates.launcher`.** A one-time fold owned by `internal/upgrade`, mirroring the retired-`updater.yaml` fold (`internal/sandbox/prepare/upgrade_migration.go`): read the legacy JSON, fold any `last_checked`/`dismissed_versions` not already present, delete the file on a successful commit, and leave it in place on a contended root lock or a failed write so a later run retries. A missing, empty, or corrupt file folds nothing but is still removed. The fold runs **only when a real store is injected**: with a nil `Store` the check runs against an ephemeral in-memory store, so folding would delete the real file after a "write" that cannot outlive the process. The caller, which alone knows whether persistence is real, guards it. A fold that fails **defers the whole check**: the process's state view is then known to be missing the legacy dismissals, and checking anyway could re-offer a version the user already dismissed. This bounds to the *automatic* check: `agents-sandbox upgrade` installs directly and never folds or reads this state, so a stuck fold cannot block an upgrade.
2. **Legacy top-level `state.yaml` `agents:` → `updates.agents`.** Handled in the pure parse step: it recognises the old key and normalises it in memory, so the next `Commit` writes the new shape and the old key disappears. Purely a read-side normalization; no separate migration file. The key is decoded into its own single-field struct, **never a mirror of `RootState`**: a mirror silently drops every field it does not restate, which is how the `adopted_home_volumes` registry would have been lost on the next write. The new shape wins wholesale — a non-empty `updates.agents` means the legacy key is ignored and never decoded, so a malformed legacy value fails the read only when `updates.agents` is empty.
3. **Retired `updater.yaml`** (already folded): its fold target moves from the top-level `agents:` key to `updates.agents`. No new file, only a retarget. The fold is detailed in `upgrade-state.md`.

`upgrade.json` and `updater.yaml` are **internal state files**, not user-facing config, so this needs **no entry in the user-facing deprecation register** (which covers flags, config keys, env vars, commands); the retired files are noted in the changelog.

### `internal/upgrade` state seam

`internal/upgrade` used to own its own JSON file via `Options.StatePath`. It now takes an injected `Store` (`Load`, `Update`) so it does **not** import `internal/sandbox/state`; `cmd` wires `launcherStore`, which maps the seam onto `updates.launcher` through `state.WithRootState`. A nil `Store` falls back to an in-memory one, preserving the unlimited test seam. A `Load` error is non-fatal: `Check` reports it verbose and returns no result rather than failing the run.

`Update(mutate func(*LauncherState)) error` is the **only** mutation, and it applies `mutate` to the state as it exists at write time, inside one locked transaction. There is deliberately no `Save`: a caller that loaded a snapshot and wrote it back later would clobber a concurrent launcher's newer dismissal or check time — unrecoverable for the fold, whose legacy file is already deleted. Every writer (the fold's merge, the check's persist) therefore merges through `Update`.

### Invariants

- A `--dry-run` writes nothing: the folds run only on the paths that already mutate state, never in the side-effect-free preview.
- A corrupt root document is never clobbered: `WithRootState` returns an error and the closure never runs.
- Every root-state mutation goes through `WithRootState`; contention defers (`blocking=false`) rather than blocking startup or prompting.
- **One binary per install.** A binary that predates this change does not know the `updates:` key, so if it writes the root document it drops that section (the upgrade-check cache and dismissals); the sections it does know (`migrations:`) survive. Accepted — the state root is internal 0.x state and the alternative (a transitional dual-write of the legacy keys) would contradict the one-document rule. No dual-write.

## Locked transactions

Correctness is structural, not per-call-site discipline: one locked transaction per document holds the lock across read → mutate → write, so no caller has to pick the right lock, remember it, or carry forward another writer's fields by convention.

- One locked transaction primitive per document (`WithHomeState`, `WithRootState`) that holds the lock across read → mutate → write.
- No exported lock-free write path. `WriteState`, `WriteRootState`, `UpdateRootState`, `LockHomeState`, `LockRootState`, and `ReplaceHomeState` are not part of the mutation API.
- Recovery is the only wholesale writer, expressed as a transaction so it is auditable and carries forward the fields it does not own.
- Lock files are never deleted, so a held flock can never be bypassed by an unlink.
- A same-process double-lock fails fast instead of self-deadlocking.
- The read→decide→write window closes: a writer either reads inside the transaction or guards the final write with a compare-and-set precondition checked inside it.
- `HomeState` stays one file; field preservation is enforced by the read-modify-write transaction, not by separate per-concern files.
- Lock granularity is unchanged: root stays one machine-global lock; home stays one lock per `(slug, agent)`. Locks are not reentrant, per-field, or optimistic.
- Lock acquisition order is fixed: root → per-key (home, then project-VM); no path takes a per-key lock and then the root lock.
- No on-disk state schema change (the lock *path* changes; the documents do not), and no user-facing behavior change other than removing spurious/racy writes.
- `offered_versions` does not survive a total state loss; the accepted cost is at most one extra ordinary upgrade prompt. No second document or `RootState` keyed by `(slug, agent)` is introduced.
- The primitive is a function that scopes a lock, not a storage abstraction, repository pattern, or event log.

Constraints: flock is not reentrant, so a single call path must never take the same key's lock twice; the root lock is held for a long time by an in-flight runtime-home copy campaign (minutes), so non-blocking acquirers must defer, not queue; many test packages seed state directly through the transaction API; the `testing/test-isolation.md` fail-fast seams and the near-100% coverage bar apply.

### Home transaction

```go
// WithHomeState holds the (slug,agent) home lock across read -> fn -> one write.
// exists reports whether a readable state file was present. A corrupt/unreadable
// document is an error and is never written. fn returns whether to write and may
// fail, so a caller can abort a stale write; a false/nil write is a read-only no-op.
func WithHomeState(k Key, fn func(cur *HomeState, exists bool) (write bool, err error)) error

// RecoverHomeState is WithHomeState with repair semantics: a missing OR corrupt
// document hands fn an empty HomeState, so recovery can rebuild and repair it.
// It is the only path that may discard an unreadable document; recovery owns it.
func RecoverHomeState(k Key, fn func(cur *HomeState, exists bool) (write bool, err error)) error
```

Every writer maps to one call:

| Writer | Transaction callback |
|---|---|
| `UpdateHomeState(k, mutate)` (create-if-missing) | ignore `exists`; `mutate(cur)`; return `true, nil` |
| `UpdateExistingHomeState(k, mutate)` (skip-if-missing) | `if !exists { return false, nil }`; `mutate(cur)`; return `true, nil` |
| `ReplaceHomeState(k, hs)` (recovery) | `RecoverHomeState`; build recovered state into `cur`; return `true, nil` |
| pre-resolution offered/version recovery | `if !exists { return false, nil }`; mutate; return `true, nil` |
| recovery carry-forward | `RecoverHomeState`; if `exists`, carry every field not owned; else rebuild from image facts |

Because `fn` mutates the *current* document in place, a writer that touches only one field cannot drop another's — the concurrency half of field preservation is gone by construction. `RecoverHomeState` is the sole wholesale/repair entry, so "recovery is the only wholesale writer" is enforced at the API.

Only home resolution and recovery may **create** the document, because they own the volume-naming write. Every other writer takes the `UpdateExistingHomeState` shape (`if !exists { return false, nil }`), including the two that run after a VM is created: `prepare.persistFingerprints` and `vm.recordImageProvenance`. Creating a document there would persist `home_volume: ""` — `HomeVolume` has no `omitempty` — and the next run's home resolution would then try to resolve the empty name, hard-failing on a non-absence error or warning about a missing `""` volume.

`EnsureNewHome` intentionally writes a fresh fingerprint-seeded state with no upgrade carry-forward: it runs only when a readable state's recorded volume is gone **and** no live volume can be recovered, so there is no prior home to preserve. The resulting `agent_version` loss self-heals via `recordImageProvenance` on the next boot, and `offered_versions` loss is the same accepted at-most-one-extra-prompt cost as total loss.

### Lock boundary for multi-step flows

The home lock is **never held across external msb I/O** (volume creation, prefill, or a whole-home copy, which can take minutes). Those flows keep I/O outside the lock and close the TOCTOU with a compare-and-set guard inside the final transaction: re-read, verify the precondition still holds (e.g. `cur.HomeVolume == oldVolume`), and either commit the new value or fail with a CAS error the caller surfaces (`home volume changed concurrently (want …, have …)`). This closes the lost-update without a minutes-long lock, and is why the callback returns an error rather than only a bool.

`EnsureNewHome` applies the same guard with an adopt-instead-of-fail outcome: its snapshot is the `home_volume` that home resolution observed when it decided a fresh volume is needed (`""` for a missing or unreadable state), passed in rather than re-read, so a winner committing between that decision and the creation is still seen. When the commit transaction finds a different, non-empty `home_volume`, a concurrent session has committed its own fresh volume; failing would abort a run that has a perfectly usable home, so it writes nothing, returns the committed state, and removes the volume it just created. Its generated name has second granularity, so a create that fails with `VolumeAlreadyExists` waits for the next second and retries (bounded).

Resolution reads (`ResolveHomeVolume`, `volumeOp`, `RecordHomeImage`) stay lock-free *snapshots*: the volume flows are already fenced by `checkForActiveVMs`, and every write they inform is guarded by the CAS above.

### Root transaction

The copy campaign legitimately checkpoints (writes after each volume) so a crash resumes; simple writers write once. One primitive serves both by exposing an explicit commit within a held lock:

```go
type RootTxn struct { /* current *RootState */ }
func (t *RootTxn) State() *RootState
func (t *RootTxn) Commit() error // write current; repeatable

// WithRootState acquires the root lock (blocking or non-blocking) for the
// closure. ok=false when non-blocking and contended; the caller defers.
func WithRootState(blocking bool, fn func(*RootTxn) error) (ok bool, err error)
```

- `pendingUpgrade` / `migrateLegacyUpdaterState`: `blocking=false`; `fn` mutates `State()` and `Commit()`s once.
- `CopyVolumes`: `blocking=true`; `fn` runs the whole campaign and `Commit()`s after each volume (preserving exact crash-resume); `saveMigration` becomes a `Commit`.
- `finalizeCompletedCampaign` / `dismissMigration`: `blocking=true`, re-read under the lock, commit once.

### Recovery

Recovery is the only wholesale writer, expressed as a `RecoverHomeState` transaction. The callback lives in the `volume` package (which already has msb/image access, via `recoveredHomeState`); the `state` package owns only the lock-and-write, so there is no import cycle.

- `exists` (readable prior): carry `agent_version`, `agent_source`, `docker_source`, `offered_versions`, and the fingerprints forward; overwrite `home_volume`/`image_digest` with the recovered values.
- corrupt or missing: rebuild from reality — `home_volume`/`image_digest` from the recovered values, fingerprints seeded from the desired set — rather than writing blanks. `agent_version` is left empty here (this callback does not read the image label) and self-heals via `recordImageProvenance` on the next boot; `agent_source`/`docker_source` likewise require a running sandbox to read (`/etc/agents-sandbox/agent-source`) and self-heal the same way. `offered_versions` is not recoverable and is left empty (accepted cost).

`ReplaceHomeState` is unexported, so no other package can wholesale-write a `HomeState` except through `RecoverHomeState`.

### Locks are never deleted, and removal is locked

Lock files live in a dedicated `<root>/locks/` directory (`locks/root.lock`, `locks/<slug>/<agent>.lock` — or `locks/<slug>/legacy.lock` for an agent-less legacy key — plus `locks/<slug>/<agent>/ensure-vm.lock` for the project-VM creation lock, composed by `state.ProjectVMLockDir` and `state.ProjectVMLockName`), which `RemoveHomeStateIf`, pruning, and every other path never touch. `RemoveHomeStateIf` removes the key's state directory (the state file and `clients/`) but never the lock; `claims/` is per-slug, outside the key dir, and is untouched.

The project-VM creation lock that serializes `EnsureProjectVM` also lives under `<root>/locks/`, at `locks/<slug>/<agent>/ensure-vm.lock` — keyed per agent as well as slug, since agents of one project use separate VMs and must not serialize on each other's creation — so a prune cannot unlink the lock a concurrent creation holds.

Because the lock now outlives the state directory, removal must itself take the home lock: `RemoveHomeStateIf(k, shouldRemove)` reads the current document, asks `shouldRemove`, and removes under the home lock (`withHomeLock`) directly, so a prune cannot delete the directory between a writer's read and its `atomicwrite` (which would recreate the directory and resurrect a just-pruned key). Legacy agent-less keys and `clients/` cleanup keep their current semantics, ordered inside the lock.

The guarded form is what makes pruning's state removal safe against a concurrently created resource. Prune decides from a snapshot of `msb` volumes taken *before* it sweeps, so a home volume created after that snapshot is invisible to the counts; removing the state from the stale counts alone would delete a state that (once the writer commits) names the new volume, orphaning it. Because the writer that creates a volume commits the state naming it inside the same home-lock transaction (and only after the volume exists), reading the document under that lock closes the race: prune keeps a state that names a home volume, and a state with no home volume yet is one the writer will recreate. The check is the document's content, not a re-listing of `msb` volumes, so the home lock is still never held across external `msb` I/O.

### Reentrancy fail-fast

A process-local set of currently-held lock paths is checked before flock; a second acquisition of the same path in the same process returns a descriptive error instead of deadlocking. This covers both recursive calls and two goroutines in one process. The guard applies to **blocking** acquires only: a non-blocking self-acquire still returns `ok=false` rather than an error, because it cannot deadlock and is the contention-deferral path callers rely on.

### Preview boundary

The execution mode lives at the session/CLI boundary (`options.Mode`, resolved by `runoptions.ResolveMode`), and the preview returns before `PrepareSandbox`, so that hub needs no dry-run gates. The full `--dry-run` contract lives in `architecture/dry-run-preview.md`.

### API after this change

- Exported reads (lock-free snapshots, safe): `ReadState`, `ReadRootState`.
- Exported mutations: `WithHomeState`, `RecoverHomeState`, `WithRootState`, `WithKeyLocks`, `RemoveHomeStateIf`.
- `WithKeyLocks` is the multi-key transaction primitive: it holds the per-key home and project-VM locks for a set of `(slug, agent)` keys, taken sorted by `(slug, agent)` (home before project-VM within a key) under the root lock, and runs one closure. It is the only point at which two per-key locks are held at once, so a multi-key operation (identity adoption) cannot be interleaved by a single-key state writer or VM creation.
- Unexported: `writeState`, `writeRootState`, lock acquisition primitives.
- **No exported raw write path.** Test seeding uses the transaction API itself; a deliberately corrupt fixture is written as raw bytes with `os.WriteFile(filepath.Join(state.KeyDir(k), "state.yaml"), …)`.

### Edge cases

- Missing state file, `WithHomeState`: `exists=false`; the writer decides (create or skip).
- Missing root file, `WithRootState`: `fn` sees a zero `RootState` (there is no existence accessor).
- Present-but-corrupt file, `WithHomeState`: returns an error, writes nothing.
- Present-but-corrupt file, `RecoverHomeState`: hands `fn` an empty document; recovery rebuilds and repairs the file.
- Present-but-corrupt root file: `ReadRootState` wraps `ErrStateUnparsable` too — both documents share the sentinel — and `WithRootState` returns that error and never calls `fn`.
- Concurrent campaign holds the root lock: non-blocking upgrade/fold acquirers get `ok=false` and retry next run.
- Double-lock in one process: descriptive error, not a deadlock.
- Lock directory deleted by a user: recreated on demand; flock identity is per-file and there are no cross-change holders.

## Atomic-write durability

`internal/atomicwrite.WriteFile` replaces a state or config file so a reader never sees a partial document and a crash cannot lose the latest one. The atomicity half (unique temp file, rename) is old; this contract covers the durability half and the error a caller sees when durability fails after the swap.

### Write sequence

`WriteFile(path, data)` (mode 0600; `WriteFileMode(path, data, perm)` sets `perm` exactly, regardless of the umask, for an edited user file that must keep its permissions):

1. Creates `path`'s directory tree (mode 0700) if missing, then flushes the parent of **every** directory it created, shallowest first. The leaf's parent alone is not enough: the entry naming a fresh `<slug>`/`<agent>` directory in its parent must be durable too, or a power loss can drop the tree the file lives in.
2. Writes a uniquely named `.basename.*` temp file in the destination directory and flushes it through `os.File.Sync` — `fsync` on Linux, the stronger `F_FULLFSYNC` on darwin. There is deliberately no weaker fallback.
3. Closes the temp file and renames it over `path` (last writer wins; unique names keep writers from colliding).
4. Flushes the destination directory, making the rename's directory entry durable.

The temp file is removed on every error path before step 4.

### Installed-write marker

Steps 1 and 4 fail only in situations (full disk, vanished directory) a test filesystem cannot force, so they run through the package's test seams.

An error from step 4 is raised **after** the rename already put the new content at `path`. `WriteFile` wraps it with the sentinel `atomicwrite.ErrInstalled`, so the caller can tell it apart from a failure before the swap. The `state` package surfaces this as `state.WriteInstalled(err) bool`, recognized through the wrapping the state writer adds.

### Caller rule

Every error before step 4 leaves the destination untouched; an `ErrInstalled` error means the on-disk document is the new one and only its durability is unproven.

A caller that cleans up external resources after a failed state write — the home-volume callers in `volume` (`ApplyHomeAction`, `volumeOp`, `EnsureNewHome`), which would otherwise delete the freshly created volume — **must not** roll back when `state.WriteInstalled` is true. Deleting the volume then would leave the just-installed state pointing at a removed volume. `Manager.cleanupUncommittedVolume` is the single implementation of that check for the volume paths.

The error is still returned (durability was not proven); only the destructive rollback is suppressed. Post-commit best-effort steps skipped on that path (retiring a spent adoption claim, removing the old volume) are safe to skip: the old volume and a stale claim are harmless.

Limit: only the parents of newly created directories are flushed; pre-existing ancestors are not re-flushed, and durability is not verified by a read-back after the syncs.
