---
description: Design contract for making project/launcher state handling structurally correct — one locked transaction primitive per document (HomeState, RootState), no exported lock-free writers, recovery as the only wholesale transaction, lock files that are never deleted, and a reentrancy fail-fast. Option B from the #225 state-robustness review; option C (splitting the document) is rejected.
read_if: Changing anything that reads or writes the per-project HomeState or the launcher-global RootState, the state locking primitives, state recovery, or the writers in vm/volume/runtimemigration.
---

# Project state: locked transactions

## Intent

State handling has been reworked repeatedly (#225 review rounds), yet Greptile keeps finding defects. The root cause is
that correctness is per-call-site discipline: every writer must independently (a) pick the right lock and acquisition
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
- `~/.operator-shared/specs/test-isolation.md` fail-fast seams and the near-100% coverage bar apply.

## Current failure surface (what this fixes)

1. **Lock-file deletion breaks mutual exclusion.** `RemoveState` (`internal/sandbox/state/state.go:161`) does
   `os.RemoveAll(KeyDir(k))`, deleting `home.lock`, and is called by pruning
   (`internal/sandbox/pruning/volumes.go:107`). A holder keeps its flock on the unlinked inode while a new writer locks
   a fresh file: two "holders".
2. **TOCTOU on unlocked reads.** `ResolveHomeVolume` reads state unlocked (`internal/sandbox/volume/state.go:33`);
   the write lands much later (`volume/operations.go:196`→`:250`, `RecordHomeImage` `state.go:168`→`:174`). The lock
   covers a single write, not read→decide→write.
3. **Field preservation is convention, not structure.** One wide `HomeState` (volume + digest + 3 upgrade fields +
   `offered_versions` + 4 fingerprints) is rewritten whole by every writer; three review rounds were "writer dropped a
   field".
4. **Recovery can silently drop fields.** `recoverOrCreateHome` with `prior==nil` (corrupt/missing) loses
   `agent_version`/source/`offered_versions` (`volume/state.go:58`); `EnsureNewHome` drops all upgrade fields;
   `ReplaceHomeState` is exported so any caller can wholesale-write.
5. **Lock-free writers are exported.** `WriteState`, `WriteRootState`, and deliberately lock-free `UpdateRootState`
   (`root.go:146`) enforce correctness by comment only.
6. **No reentrancy / lock-order guard.** flock is not reentrant and `UpdateHomeState` blocks, so a nested call
   self-deadlocks; root→home is intended but unchecked.
7. **`--dry-run` side-effect-freedom is spread across call paths** (`vm/run_orchestrate.go:206`, `:313`), not one guard.
   This change only documents the two existing gates; making `--dry-run` genuinely side-effect-free (today it can still
   build an image, create a home volume, and write state) is tracked in a separate issue and is **out of scope** here.

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

`EnsureNewHome` intentionally writes a fresh fingerprint-seeded state with no upgrade carry-forward: it runs only when a
readable state's recorded volume is gone **and** no live volume can be recovered, so there is no prior home to preserve.
The resulting `agent_version` loss self-heals via `recordImageProvenance` on the next boot, and `offered_versions` loss is
the same accepted at-most-one-extra-prompt cost as total loss. Finding 4's `EnsureNewHome` clause is therefore
intentional, not a regression.

### Lock boundary for multi-step flows

The home lock is **never held across external msb I/O** (volume creation, prefill, or a whole-home copy, which can take
minutes). Those flows keep I/O outside the lock and close the TOCTOU with a compare-and-set guard inside the final
transaction: re-read, verify the precondition still holds (e.g. `cur.HomeVolume == oldVolume`), and either commit the
new value or fail with a "state changed concurrently" error the caller surfaces. This closes the lost-update in finding 2
without a minutes-long lock, and is why the callback returns an error rather than only a bool.

Resolution reads (`ResolveHomeVolume`, `volumeOp`, `RecordHomeImage`) stay lock-free *snapshots*: the volume flows are
already fenced by `checkForActiveVMs`, and every write they inform is guarded by the CAS above.

### Root transaction

The copy campaign legitimately checkpoints (writes after each volume) so a crash resumes; simple writers write once. One
primitive serves both by exposing an explicit commit within a held lock:

```go
type RootTxn struct { /* current *RootState; exists bool */ }
func (t *RootTxn) State() *RootState
func (t *RootTxn) Exists() bool
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
- corrupt or missing: rebuild from reality — `agent_version` from the live image label (`image.ExistingAgentVersion`),
  fingerprints seeded from the desired set (`#183`) — rather than writing blanks. `agent_source`/`docker_source` require
  a running sandbox to read (`/etc/agents-sandbox/agent-source`) and are not re-read here; they self-heal when
  `recordImageProvenance` next runs on boot. `offered_versions` is not recoverable and is left empty (accepted cost).

`ReplaceHomeState` is unexported, so no other package can wholesale-write a `HomeState` except through
`RecoverHomeState`.

### Locks are never deleted, and removal is locked

Lock files move to a dedicated `<root>/locks/` directory (`locks/root.lock`, `locks/<key>.lock`) that `RemoveState`,
pruning, and every other path never touch. `RemoveState` continues to remove the key's state directory (state file,
`clients/`, `claims/`) but never the lock. (This changes the lock path once; there are no cross-change holders.)

Because the lock now outlives the state directory, removal must itself take the home lock: `RemoveHomeState(k)` removes
under `WithHomeState`, so a prune cannot delete the directory between a writer's read and its `atomicwrite` (which would
recreate the directory and resurrect a just-pruned key). Legacy agent-less keys and `clients/`/`claims/` cleanup keep
their current semantics, ordered inside the lock.

### Reentrancy fail-fast

A process-local set of currently-held lock paths is checked before flock; a second acquisition of the same path in the
same process returns a descriptive error instead of deadlocking. This covers both recursive calls and two goroutines in
one process.

### `--dry-run` guard

Keep `determineAgentVersion` as the single branch that skips `resolveBuildVersion` and the legacy fold, and
`prepareHomeVolume` as the single branch that skips the migration offer; document the known `--dry-run` gates:
`determineAgentVersion` and `prepareHomeVolume`, plus `volume.ApplyHomeAction` (skips the migrate/reset action) and
`session.Run`/`session.Shell` (skip the attach). No new mechanism.

### API after this change

- Exported reads (lock-free snapshots, safe): `ReadState`, `ReadRootState`.
- Exported mutations: `WithHomeState`, `RecoverHomeState`, `WithRootState`, `RemoveHomeState`.
- Unexported: `writeState`, `writeRootState`, lock acquisition primitives.
- **No exported raw write path.** Test seeding uses the transaction API itself; a deliberately corrupt fixture is written
  as raw bytes with `os.WriteFile(filepath.Join(state.KeyDir(k), "state.yaml"), …)`, which needs no exported writer.

## Edge cases

- Missing state file, `WithHomeState`: `exists=false`; the writer decides (create or skip).
- Present-but-corrupt file, `WithHomeState`: returns an error, writes nothing (never clobbers an unknown document).
- Present-but-corrupt file, `RecoverHomeState`: hands `fn` an empty document; recovery rebuilds and repairs the file.
- Concurrent campaign holds the root lock: non-blocking upgrade/fold acquirers get `ok=false` and retry next run.
- Double-lock in one process: descriptive error, not a deadlock.
- Lock directory deleted by a user: recreated on demand; flock identity is per-file and there are no cross-change
  holders.

## Testing

- Transaction primitive: absent / present / corrupt × `WithHomeState` / `RecoverHomeState` × commit / no-commit, plus
  contention (`ok=false`), reentrancy fail-fast, and lock-file persistence across `RemoveState`.
- Property test per writer: applying it preserves every field it does not own (the finding-3 guard).
- Recovery: readable-prior carry-forward; corrupt rebuild-from-image; `offered_versions` documented-empty.
- Migrate existing state-seeding tests off `WriteState` onto the transaction API (unchanged `ReadState`/`ReadRootState`
  reads are fine); corrupt fixtures via raw `os.WriteFile`.
- CAS guard: a volume flow whose snapshot precondition no longer holds fails with a "state changed concurrently" error
  and writes nothing.
- `RemoveHomeState` under lock: a prune racing a writer cannot resurrect pruned state.
- `make check` (fmt, lint, test, docs linkcheck) green; `make compile-all` green.

## Docs and changelog

- `CHANGELOG.md` `[Unreleased]`: internal state-handling hardening; no user-facing behavior change.
- No `docs/` page change expected (internal); update the neighbor spec `upgrade-state-scoping.md` if its
  writer-serialization clause is superseded.

## Calls made by the author (not open)

- Rides on the `improve_upgrade_prompt` / PR #225 branch.
- Test seeding moves to the transaction API; no lint-guarded raw writer remains.
- The home lock is never held across external msb I/O; multi-step flows use a compare-and-set guard.
- `agent_source`/`docker_source` on a lost/corrupt state are left to self-heal via `recordImageProvenance` on the next
  boot, not re-read from the image during recovery.
- Removal (`RemoveHomeState`) takes the home lock.

## Resolved: `--dry-run` scope

`--dry-run` purity is **documentation-only here (option A)**. Finding 7 is out of scope: this change documents the known
`--dry-run` gates — `determineAgentVersion`, `prepareHomeVolume`, `volume.ApplyHomeAction`, and
`session.Run`/`session.Shell` — and adds no commit-suppression. Making `--dry-run` genuinely side-effect-free (it can
still build an image, create a home volume, and write state, because `prepareSandbox` runs before the dry-run
early-return in `session/run.go`) is tracked in
[issue #228](https://github.com/inoio/agents-sandbox/issues/228).
