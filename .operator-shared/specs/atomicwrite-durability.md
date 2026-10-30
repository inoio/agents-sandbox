---
description: Contract for the atomic writer's crash durability and its installed-write error marker: the temp file is flushed before the rename, the leaf directory after, and the parent of every directory `MkdirAll` created; a post-rename durability error wraps `ErrInstalled` (surfaced as `state.WriteInstalled`) so a caller never rolls back a resource the installed state names (#336).
read_if: Changing internal/atomicwrite or the state writers that use it, reasoning about crash durability of state/config writes, or handling a state-write error in a caller that deletes or rolls back a resource (volume migrate/reset/run home actions).
---

# Atomic writes: durability and the installed-write marker

## Intent

`internal/atomicwrite.WriteFile` replaces a state or config file so a reader never sees a partial document and a
crash cannot lose the latest one. The atomicity half (unique temp file, rename) is old; this contract covers the
durability half and the error a caller sees when durability fails after the swap.

## Write sequence

`WriteFile(path, data)`:

1. Creates `path`'s directory tree (mode 0700) if missing, then flushes the parent of **every** directory it
   created, shallowest first. The leaf's parent alone is not enough: the entry naming a fresh `<slug>`/`<agent>`
   directory in its parent must be durable too, or a power loss can drop the tree the file lives in.
2. Writes a uniquely named `.basename.*` temp file in the destination directory and flushes it through
   `os.File.Sync` — `fsync` on Linux, the stronger `F_FULLFSYNC` on darwin. There is deliberately no weaker
   fallback.
3. Closes the temp file and renames it over `path` (last writer wins; unique names keep writers from colliding).
4. Flushes the destination directory, making the rename's directory entry durable.

The temp file is removed on every error path before step 4.

## Installed-write marker

Steps 1 and 4 fail only in situations (full disk, vanished directory) a test filesystem cannot force, so they run
through the package's test seams.

An error from step 4 is raised **after** the rename already put the new content at `path`. `WriteFile` wraps it
with the sentinel `atomicwrite.ErrInstalled`, so the caller can tell it apart from a failure before the swap. The
`state` package surfaces this as `state.WriteInstalled(err) bool`, recognized through the wrapping the state
writer adds.

## Caller rule

Every error before step 4 leaves the destination untouched; an `ErrInstalled` error means the on-disk document is
the new one and only its durability is unproven.

A caller that cleans up external resources after a failed state write — the home-volume callers in `volume`
(`ApplyHomeAction`, `volumeOp`, `EnsureNewHome`), which would otherwise delete the freshly created volume — **must not**
roll back when `state.WriteInstalled` is true. Deleting the volume then would leave the just-installed state pointing at
a removed volume. `Manager.cleanupUncommittedVolume` is the single implementation of that check for the volume paths.

The error is still returned (durability was not proven); only the destructive rollback is suppressed. Post-commit
best-effort steps skipped on that path (retiring a spent adoption claim, removing the old volume) are safe to
skip: the old volume and a stale claim are harmless.

## Limits

- Only the parents of newly created directories are flushed; pre-existing ancestors are not re-flushed.
- Durability is not verified by a read-back after the syncs.
