---
description: Design contract for making project/launcher state handling structurally correct — one locked transaction primitive per document (HomeState, RootState), no exported lock-free writers, recovery as the only wholesale transaction, lock files that are never deleted, and a reentrancy fail-fast. Option B from the #225 state-robustness review; option C (splitting the document) is rejected.
read_if: Changing anything that reads or writes the per-project HomeState or the launcher-global RootState, the state locking primitives, state recovery, or the writers in vm/volume/runtimemigration.
---

# Project state: locked transactions

## Intent

The root cause of the recurring state-handling defects is that correctness is per-call-site discipline: every writer
must independently (a) pick the right lock and acquisition
mode, (b) pick between `UpdateHomeState` / `UpdateExistingHomeState` / `ReplaceHomeState` / lock-free `WriteState`, and
(c) carry forward every field of a wide document it does not own. Good call sites exist, but correctness depends on every
caller getting all three right, so each round finds a new one that did not.

This change makes correctness structural: a single locked transaction per document, no exported lock-free writers, and
recovery as the only wholesale writer.

## Goals

- One locked transaction primitive per document (`WithHomeState`, `WithRootState`) that holds the lock across
  read → mutate → write, so no caller can choose the wrong lock or forget it.
- No exported lock-free write path. `WriteState`, `WriteRootState`, `UpdateRootState`, `LockHomeState`, `LockRootState`,
  and `ReplaceHomeState` stop being part of the mutation API.
- Recovery becomes the only wholesale writer, expressed as a transaction so it is auditable and carries forward the
  fields it does not own.
- Lock files are never deleted, so a held flock can never be bypassed by an unlink.
- A same-process double-lock fails fast instead of self-deadlocking.
- The read→decide→write window closes: writers either read inside the transaction, or guard the final write with a
  compare-and-set precondition checked inside it.

## Non-goals

- **No document split (option C rejected).** `HomeState` stays one file; field preservation is enforced by the
  read-modify-write transaction, not by separate per-concern files.
- **No new lock granularity.** Root stays one machine-global lock; home stays one lock per `(slug, agent)`.
- **No reentrant locks.** Reentrancy would hide the bug class; a held-lock guard fails fast instead.
- **No `offered_versions` survival across a total state loss.** Accepted cost: at most one extra ordinary upgrade
  prompt (see below). A second document or a RootState keyed by `(slug, agent)` is not introduced.
- No change to on-disk state schema (the lock *path* changes; the documents do not).
- No change to user-facing behavior other than removing spurious/racy writes.

## Anti-goals

- Introducing a generic storage abstraction layer, repository pattern, or event log. The primitive is a function that
  scopes a lock, not a framework.
- Making locks reentrant, per-field, or optimistic.

## Constraints

- flock is not reentrant: a single call path must never take the same key's lock twice.
- The root lock is held for a long time by an in-flight runtime-home copy campaign (minutes); non-blocking acquirers
  must defer, not queue.
- Many test packages seed state directly with `state.WriteState`; the migration to the transaction API touches them.
- The `.operator-shared/specs/test-isolation.md` fail-fast seams and the near-100% coverage bar apply.

## Current failure surface (what this fixes)

1. **Lock-file deletion breaks mutual exclusion.** The state-removal path does
   `os.RemoveAll(KeyDir(k))`, deleting `home.lock`, and is called by pruning
   (`internal/sandbox/pruning/volumes.go:107`). A holder keeps its flock on the unlinked inode while a new writer locks
   a fresh file: two "holders". The project-VM creation lock (`ensure-vm.lock`) had the same defect — `prune`
   concurrent with `run` could unlink a held lock and let a second invocation create a duplicate or clobbering VM
   (#330); it lives under `locks/<slug>/ensure-vm.lock` now too.
2. **TOCTOU on unlocked reads.** `ResolveHomeVolume` reads state unlocked (`internal/sandbox/volume/state.go:33`);
   the write lands much later (`volume/operations.go:196`→`:250`, `RecordHomeImage` `state.go:168`→`:174`). The lock
   covers a single write, not read→decide→write.
3. **Field preservation is convention, not structure.** One wide `HomeState` (volume + digest + 3 upgrade fields +
   `offered_versions` + 4 fingerprints) is rewritten whole by every writer; a dropped field is a recurring failure
   mode.
4. **Recovery can silently drop fields.** `recoverOrCreateHome` with `prior==nil` (corrupt/missing) loses
   `agent_version`/source/`offered_versions` (`volume/state.go:58`); `EnsureNewHome` drops all upgrade fields;
   `ReplaceHomeState` is exported so any caller can wholesale-write.
5. **Lock-free writers are exported.** `WriteState`, `WriteRootState`, and deliberately lock-free `UpdateRootState`
   (`root.go:146`) enforce correctness by comment only.
6. **No reentrancy / lock-order guard.** flock is not reentrant and `UpdateHomeState` blocks, so a nested call
   self-deadlocks; the acquisition order is **root → per-key (home, then project-VM)**, and no path takes a
   per-key lock and then the root lock. `WithKeyLocks` is the exported transaction primitive for a multi-key
   operation and the only place two per-key locks are held at once: it acquires them sorted by `(slug, agent)`
   (home before project-VM within a key) and releases everything on a failed acquire. Identity adoption
   (`vm.AdoptLegacyIdentity`) is its consumer — it holds every moved key's locks across the destroy and the
   directory move (#333). The lock set covers per-`(slug, agent)` keys only: the agent-less legacy key
   (`Key{Slug, Agent: ""}`, whose `state.yaml` the directory move also relocates and whose lock is
   `locks/<slug>/legacy.lock`, taken by prune's `RemoveHomeStateIf`) is deliberately not locked — no current path
   writes the agent-less `HomeState`, so the only outcomes are a benign ENOENT or adoption's existing loud
   arrival failure.
7. **`--dry-run` side-effect-freedom was spread across call paths** (`vm/run_orchestrate.go`), not one guard.
   It is now a genuine read-only preview (no image build, volume create, or state write); see
   [dry-run-preview.md](dry-run-preview.md).

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
concurrency half of finding 3 is gone by construction. `RecoverHomeState` is the sole wholesale/repair entry, so
"recovery is the only wholesale writer" is enforced at the API, not by convention.

Only home resolution and recovery may **create** the document, because they are the writers that own the volume-naming
write. Every other writer takes the `UpdateExistingHomeState` shape (`if !exists { return false, nil }`), including the
two that run after a VM is created: `vm.persistFingerprints` (the post-creation fingerprint record) and
`vm.recordImageProvenance` (the boot provenance record). Creating a document there would persist
`home_volume: ""` — `HomeVolume` has no `omitempty` — and the next run's home resolution would then try to resolve the
empty name, hard-failing on a non-absence error or warning about a missing `""` volume (#430).

`EnsureNewHome` intentionally writes a fresh fingerprint-seeded state with no upgrade carry-forward: it runs only when a
readable state's recorded volume is gone **and** no live volume can be recovered, so there is no prior home to preserve.
The resulting `agent_version` loss self-heals via `recordImageProvenance` on the next boot, and `offered_versions` loss is
the same accepted at-most-one-extra-prompt cost as total loss. Finding 4's `EnsureNewHome` clause is therefore
intentional, not a regression.

### Lock boundary for multi-step flows

The home lock is **never held across external msb I/O** (volume creation, prefill, or a whole-home copy, which can take
minutes). Those flows keep I/O outside the lock and close the TOCTOU with a compare-and-set guard inside the final
transaction: re-read, verify the precondition still holds (e.g. `cur.HomeVolume == oldVolume`), and either commit the
new value or fail with a CAS error (`home volume changed concurrently (want …, have …)`) the caller surfaces. This closes the lost-update in finding 2
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

Lock files move to a dedicated `<root>/locks/` directory (`locks/root.lock`, `locks/<slug>/<agent>.lock` — or
`locks/<slug>/legacy.lock` for an agent-less legacy key, plus `locks/<slug>/ensure-vm.lock` for the project-VM
creation lock) that `RemoveHomeStateIf`, pruning, and every other path never touch. `RemoveHomeStateIf` continues to
remove the key's state directory (the state file and `clients/`) but never the lock; `claims/` is per-slug, outside
the key dir, and is untouched. (This changes the lock path once; there are no cross-change holders.)

The project-VM creation lock that serializes `ensureProjectVM` also lives under `<root>/locks/`, at
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

## Testing

- Transaction primitive: absent / present / corrupt × `WithHomeState` / `RecoverHomeState` × commit / no-commit, plus
  contention (`ok=false`), reentrancy fail-fast, and lock-file persistence across `RemoveHomeStateIf`.
- Property test per writer: applying it preserves every field it does not own (the finding-3 guard).
- Recovery: readable-prior carry-forward; corrupt rebuild-from-image; `offered_versions` documented-empty.
- Migrate existing state-seeding tests off `WriteState` onto the transaction API (unchanged `ReadState`/`ReadRootState`
  reads are fine); corrupt fixtures via raw `os.WriteFile`.
- CAS guard: a volume flow whose snapshot precondition no longer holds fails with a
  `home volume changed concurrently` error and writes nothing.
- `RemoveHomeStateIf` under lock: a prune racing a writer cannot resurrect pruned state, and a state naming a home
  volume created since the prune snapshot is kept, not orphaned (#335).
- Same-process lock tests assert the reentrancy fail-fast; true cross-process *blocking* waiting is flock's contract and
  is not directly tested in-process (a subprocess test is a possible future addition).
- `make check` (fmt, lint, test, docs linkcheck) green; `make compile-all` green.

## Docs and changelog

- `CHANGELOG.md` `[Unreleased]`: internal state-handling hardening. Two intended user-facing behavior changes: a reconfig
  and `volume migrate`/`volume reset` whose final state write fails now surface an error instead of reporting success
  without persisting (so persisted state and the VM cannot diverge).
- No `docs/` page change expected (internal); update the neighbor spec `upgrade-state-scoping.md` if its
  writer-serialization clause is superseded.

## Calls made by the author (not open)

- Rides on the `improve_upgrade_prompt` / PR #225 branch.
- Test seeding moves to the transaction API; no lint-guarded raw writer remains.
- The home lock is never held across external msb I/O; multi-step flows use a compare-and-set guard.
- `agent_source`/`docker_source` on a lost/corrupt state are left to self-heal via `recordImageProvenance` on the next
  boot, not re-read from the image during recovery.
- Removal (`RemoveHomeStateIf`) takes the home lock.

## Resolved: `--dry-run` scope

Finding 7 is out of scope here and resolved by [dry-run-preview.md](dry-run-preview.md): `--dry-run` is now a genuine
read-only preview — no image build, volume creation, or state write — so this change documents no `--dry-run` gates.
