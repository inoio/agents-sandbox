---
description: Design contract for making project/launcher state handling structurally correct — one locked transaction primitive per document (HomeState, RootState), no exported lock-free writers, recovery as the only wholesale transaction, lock files that are never deleted, and a reentrancy fail-fast. Option B from the #225 state-robustness review; option C (splitting the document) is rejected.
read_if: Changing anything that reads or writes the per-project HomeState or the launcher-global RootState, the state locking primitives, state recovery, or the writers in vm/volume/runtimemigration.
---

# Project state: locked transactions

Correctness is structural, not per-call-site discipline: one locked transaction per document holds the lock across
read → mutate → write, so no caller has to pick the right lock, remember it, or carry forward another writer's fields by
convention.

## Contract

- One locked transaction primitive per document (`WithHomeState`, `WithRootState`) that holds the lock across
  read → mutate → write.
- No exported lock-free write path. `WriteState`, `WriteRootState`, `UpdateRootState`, `LockHomeState`, `LockRootState`,
  and `ReplaceHomeState` are not part of the mutation API.
- Recovery is the only wholesale writer, expressed as a transaction so it is auditable and carries forward the fields it
  does not own.
- Lock files are never deleted, so a held flock can never be bypassed by an unlink.
- A same-process double-lock fails fast instead of self-deadlocking.
- The read→decide→write window closes: a writer either reads inside the transaction or guards the final write with a
  compare-and-set precondition checked inside it.
- `HomeState` stays one file; field preservation is enforced by the read-modify-write transaction, not by separate
  per-concern files (the document split, option C, is rejected).
- Lock granularity is unchanged: root stays one machine-global lock; home stays one lock per `(slug, agent)`. Locks are
  not reentrant, per-field, or optimistic.
- Lock acquisition order is fixed: root → per-key (home, then project-VM); no path takes a per-key lock and then the
  root lock.
- No on-disk state schema change (the lock *path* changes; the documents do not), and no user-facing behavior change
  other than removing spurious/racy writes.
- `offered_versions` does not survive a total state loss; the accepted cost is at most one extra ordinary upgrade prompt.
  No second document or `RootState` keyed by `(slug, agent)` is introduced.
- The primitive is a function that scopes a lock, not a storage abstraction, repository pattern, or event log.

## Constraints

- flock is not reentrant: a single call path must never take the same key's lock twice.
- The root lock is held for a long time by an in-flight runtime-home copy campaign (minutes); non-blocking acquirers
  must defer, not queue.
- Many test packages seed state directly with `state.WriteState`; the migration to the transaction API touches them.
- The `.operator-shared/specs/test-isolation.md` fail-fast seams and the near-100% coverage bar apply.

## Design

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

Every existing writer maps to one call:

| Current writer | Transaction callback |
|---|---|
| `UpdateHomeState(k, mutate)` (create-if-missing) | ignore `exists`; `mutate(cur)`; return `true, nil` |
| `UpdateExistingHomeState(k, mutate)` (skip-if-missing) | `if !exists { return false, nil }`; `mutate(cur)`; return `true, nil` |
| `ReplaceHomeState(k, hs)` (recovery) | `RecoverHomeState`; build recovered state into `cur`; return `true, nil` |
| pre-resolution offered/version recovery | `if !exists { return false, nil }`; mutate; return `true, nil` |
| recovery carry-forward | `RecoverHomeState`; if `exists`, carry every field not owned; else rebuild from image facts |

Because `fn` mutates the *current* document in place, a writer that touches only one field cannot drop another's — the
concurrency half of field preservation is gone by construction. `RecoverHomeState` is the sole wholesale/repair entry, so
"recovery is the only wholesale writer" is enforced at the API, not by convention.

Only home resolution and recovery may **create** the document, because they are the writers that own the volume-naming
write. Every other writer takes the `UpdateExistingHomeState` shape (`if !exists { return false, nil }`), including the
two that run after a VM is created: `prepare.persistFingerprints` (the post-creation fingerprint record) and
`vm.recordImageProvenance` (the boot provenance record). Creating a document there would persist
`home_volume: ""` — `HomeVolume` has no `omitempty` — and the next run's home resolution would then try to resolve the
empty name, hard-failing on a non-absence error or warning about a missing `""` volume (#430).

`EnsureNewHome` intentionally writes a fresh fingerprint-seeded state with no upgrade carry-forward: it runs only when a
readable state's recorded volume is gone **and** no live volume can be recovered, so there is no prior home to preserve.
The resulting `agent_version` loss self-heals via `recordImageProvenance` on the next boot, and `offered_versions` loss is
the same accepted at-most-one-extra-prompt cost as total loss, so `EnsureNewHome`'s drop of the upgrade fields is
intentional, not a regression.

### Lock boundary for multi-step flows

The home lock is **never held across external msb I/O** (volume creation, prefill, or a whole-home copy, which can take
minutes). Those flows keep I/O outside the lock and close the TOCTOU with a compare-and-set guard inside the final
transaction: re-read, verify the precondition still holds (e.g. `cur.HomeVolume == oldVolume`), and either commit the
new value or fail with a CAS error (`home volume changed concurrently (want …, have …)`) the caller surfaces. This closes the lost-update
without a minutes-long lock, and is why the callback returns an error rather than only a bool.

`EnsureNewHome` applies the same guard with an adopt-instead-of-fail outcome (#431): its snapshot is the
`home_volume` that home resolution observed when it decided a fresh volume is needed (`""` for a missing or unreadable
state), passed in rather than re-read, so a winner committing between that decision and the creation is still seen. When the commit transaction finds a different, non-empty `home_volume`, a
concurrent session has committed its own fresh volume. Failing would abort a run that has a perfectly usable home, so it
writes nothing, returns the committed state, and removes the volume it just created. Its generated name has second
granularity, so a create that fails with `VolumeAlreadyExists` waits for the next second and retries (bounded).

Resolution reads (`ResolveHomeVolume`, `volumeOp`, `RecordHomeImage`) stay lock-free *snapshots*: the volume flows are
already fenced by `checkForActiveVMs`, and every write they inform is guarded by the CAS above.

### Root transaction

The copy campaign legitimately checkpoints (writes after each volume) so a crash resumes; simple writers write once. One
primitive serves both by exposing an explicit commit within a held lock:

```go
type RootTxn struct { /* current *RootState */ }
func (t *RootTxn) State() *RootState
func (t *RootTxn) Commit() error // write current; repeatable

// WithRootState acquires the root lock (blocking or non-blocking) for the
// closure. ok=false when non-blocking and contended; the caller defers.
func WithRootState(blocking bool, fn func(*RootTxn) error) (ok bool, err error)
```

- `pendingUpgrade` / `migrateLegacyUpdaterState`: `blocking=false`; `fn` mutates `State()` and `Commit()`s once.
- `CopyVolumes`: `blocking=true`; `fn` runs the whole campaign and `Commit()`s after each volume (preserving exact
  crash-resume); `saveMigration` becomes a `Commit`.
- `finalizeCompletedCampaign` / `dismissMigration`: `blocking=true`, re-read under the lock, commit once.

### Recovery

Recovery is the only wholesale writer, expressed as a `RecoverHomeState` transaction. The callback lives in the `volume`
package (which already has msb/image access, via the existing `recoveredHomeState`); the `state` package only owns the
lock-and-write, so there is no import cycle.

- `exists` (readable prior): carry `agent_version`, `agent_source`, `docker_source`, `offered_versions`, and the
  fingerprints forward; overwrite `home_volume`/`image_digest` with the recovered values.
- corrupt or missing: rebuild from reality — `home_volume`/`image_digest` from the recovered values, fingerprints
  seeded from the desired set (`#183`) — rather than writing blanks. `agent_version` is left empty here (this callback
  does not read the image label) and self-heals via `recordImageProvenance` on the next boot; `agent_source`/`docker_source`
  likewise require a running sandbox to read (`/etc/agents-sandbox/agent-source`) and self-heal the same way.
  `offered_versions` is not recoverable and is left empty (accepted cost).

`ReplaceHomeState` is unexported, so no other package can wholesale-write a `HomeState` except through
`RecoverHomeState`.

### Locks are never deleted, and removal is locked

Lock files live in a dedicated `<root>/locks/` directory (`locks/root.lock`, `locks/<slug>/<agent>.lock` — or
`locks/<slug>/legacy.lock` for an agent-less legacy key — plus `locks/<slug>/<agent>/ensure-vm.lock` for the project-VM
creation lock, composed by `state.ProjectVMLockDir` and `state.ProjectVMLockName`), which `RemoveHomeStateIf`, pruning,
and every other path never touch. `RemoveHomeStateIf` removes the key's state directory (the state file and `clients/`)
but never the lock; `claims/` is per-slug, outside the key dir, and is untouched.

The project-VM creation lock that serializes `EnsureProjectVM` also lives under `<root>/locks/`, at
`locks/<slug>/<agent>/ensure-vm.lock` — keyed per agent as well as slug, since agents of one project use separate VMs
and must not serialize on each other's creation — so a prune cannot unlink the lock a concurrent creation holds
(#330).

Because the lock now outlives the state directory, removal must itself take the home lock: `RemoveHomeStateIf(k, shouldRemove)` reads the current document, asks `shouldRemove`, and removes under the home lock (`withHomeLock`) directly, so a prune cannot delete the directory between a writer's read and its `atomicwrite` (which would recreate the directory and resurrect a just-pruned key). Legacy agent-less keys and `clients/` cleanup keep their current semantics, ordered inside the lock.

The guarded form is what makes pruning's state removal safe against a concurrently created resource (#335). Prune decides from a snapshot of `msb` volumes taken *before* it sweeps, so a home volume created after that snapshot is invisible to the counts; removing the state from the stale counts alone would delete a state that (once the writer commits) names the new volume, orphaning it. Because the writer that creates a volume commits the state naming it inside the same home-lock transaction (and only after the volume exists), reading the document under that lock closes the race: prune keeps a state that names a home volume, and a state with no home volume yet is one the writer will recreate. The check is the document's content, not a re-listing of `msb` volumes, so the home lock is still never held across external `msb` I/O.

### Reentrancy fail-fast

A process-local set of currently-held lock paths is checked before flock; a second acquisition of the same path in the
same process returns a descriptive error instead of deadlocking. This covers both recursive calls and two goroutines in
one process. The guard applies to **blocking** acquires only: a non-blocking self-acquire still returns `ok=false`
rather than an error, because it cannot deadlock and is the contention-deferral path callers rely on.

### Preview boundary

The execution mode lives at the session/CLI boundary (`options.Mode`, resolved by `runoptions.ResolveMode`), and the
preview returns before `PrepareSandbox`, so that hub needs no dry-run gates. The full `--dry-run` contract — a read-only
preview that returns before `PrepareSandbox` — lives in [dry-run-preview.md](dry-run-preview.md). No new mechanism here.

### API after this change

- Exported reads (lock-free snapshots, safe): `ReadState`, `ReadRootState`.
- Exported mutations: `WithHomeState`, `RecoverHomeState`, `WithRootState`, `WithKeyLocks`, `RemoveHomeStateIf`.
- `WithKeyLocks` is the multi-key transaction primitive: it holds the per-key home and project-VM locks for a
  set of `(slug, agent)` keys, taken sorted by `(slug, agent)` (home before project-VM within a key) under the
  root lock, and runs one closure. It is the only point at which two per-key locks are held at once, so a
  multi-key operation (identity adoption) cannot be interleaved by a single-key state writer or VM creation.
- Unexported: `writeState`, `writeRootState`, lock acquisition primitives.
- **No exported raw write path.** Test seeding uses the transaction API itself; a deliberately corrupt fixture is written
  as raw bytes with `os.WriteFile(filepath.Join(state.KeyDir(k), "state.yaml"), …)`, which needs no exported writer.

## Edge cases

- Missing state file, `WithHomeState`: `exists=false`; the writer decides (create or skip).
- Missing root file, `WithRootState`: `fn` sees a zero `RootState` (there is no existence accessor).
- Present-but-corrupt file, `WithHomeState`: returns an error, writes nothing (never clobbers an unknown document).
- Present-but-corrupt file, `RecoverHomeState`: hands `fn` an empty document; recovery rebuilds and repairs the file.
- Present-but-corrupt root file: `ReadRootState` wraps `ErrStateUnparsable` too — both documents share the sentinel — and
  `WithRootState` returns that error and never calls `fn`.
- Concurrent campaign holds the root lock: non-blocking upgrade/fold acquirers get `ok=false` and retry next run.
- Double-lock in one process: descriptive error, not a deadlock.
- Lock directory deleted by a user: recreated on demand; flock identity is per-file and there are no cross-change
  holders.
