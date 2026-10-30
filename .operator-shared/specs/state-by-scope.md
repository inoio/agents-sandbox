---
description: Contract for organizing launcher-global persisted state by ownership scope (issue #141): root `state.yaml` is the single launcher-global document, and an `updates:` namespace folds the retired `upgrade.json` (launcher self-upgrade) and the legacy top-level `agents:` key (agent update checks) behind silent backwards-compatible folds. Default-behaviour change, so no deprecation-register entry.
read_if: Adding or changing launcher-global persisted state, the root `state.yaml` document shape, the `updates.launcher`/`updates.agents` keys, the `internal/upgrade` state seam, or the `upgrade.json`/`updater.yaml` folds.
---

# State by ownership scope

Date: 2026-10-21. Issue: #141.

## The rule

Launcher-global state is **one document**, root `state.yaml`. A new
launcher-global decision becomes a **key/section inside that document, never a
new file**. This reverses the per-feature-file trend (each new concern used to
add `upgrade.json`, `updater.yaml`, …) so the root file count stays fixed as
features are added.

| Scope | Cardinality | Where |
|---|---|---|
| launcher-global | 1 | root `state.yaml` |
| per-agent | few | `<root>/<agent>/…` |
| per-project+agent | grows with projects | `<root>/<slug>/<agent>/state.yaml` (already scope-based) |

The per-project+agent layout and the per-agent `config-migration.yaml` scope
(`<root>/<agent>/config-migration.yaml`) are unchanged — `config-migration.yaml`
is already scope-based and needs no move.

## Root document shape

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

- One `updates:` namespace, children `launcher` (agents-sandbox itself, from the
  retired `upgrade.json`) and `agents` (update-check state for the tools baked
  into the runner image).
- Field name is `last_checked` everywhere; `upgrade.json`'s `last_check` is gone.
- `dismissed_versions` ("the user said do not notify me") and the per-agent
  `latest_version` cache keep their existing semantics.
- `updates:` and its `launcher`/`agents` children omit themselves when empty via
  an `IsZero()` method (yaml.v3 honours `IsZeroer` for `omitempty` on a value
  struct; `time.Time` is handled natively).

This spec owns the **shape and placement** of launcher-global state.
`upgrade-state-scoping.md` owns the **semantics** of the agent check state
(cadence, why it is machine-global, the legacy `updater.yaml` fold);
`project-state-transactions.md` owns the **mutation API** (`WithRootState`).
Their `RootState.agents` field path becomes `RootState.updates.agents` here;
their semantics are unchanged.

## Backwards compatibility (silent, no user interaction)

Three legacy sources fold into `updates:`. Nothing prompts. For scalars a
present new value wins over the legacy one; for the list-valued
`dismissed_versions` the fold is a **per-element union**, so a user's "don't ask
again for this version" is never lost by the migration (the fold runs before
anything writes `updates.launcher`, so the union is normally a straight copy).

1. **`upgrade.json` → `updates.launcher`.** A one-time fold owned by
   `internal/upgrade`, mirroring the retired-`updater.yaml` fold
   (`internal/sandbox/vm/upgrade_migration.go`): read the legacy JSON, fold any
   `last_checked`/`dismissed_versions` not already present, delete the file on a
   successful commit, and leave it in place on a contended root lock or a failed
   write so a later run retries. A missing, empty, or corrupt file folds nothing
   but is still removed. The fold runs **only when a real store is injected**:
   with a nil `Store` the check runs against an ephemeral in-memory store, so
   folding would delete the real file after a "write" that cannot outlive the
   process. The caller, which alone knows whether persistence is real, guards it.
   A fold that fails **defers the whole check**, not just the migration: the
   process's state view is then known to be missing the legacy dismissals, and
   checking anyway could re-offer a version the user already dismissed. This
   bounds to the *automatic* check: `agents-sandbox upgrade` installs directly
   and never folds or reads this state, so a stuck fold cannot block an upgrade.
2. **Legacy top-level `state.yaml` `agents:` → `updates.agents`.** Handled in the
   pure parse step: it recognises the old key and normalises it in memory, so the
   next `Commit` writes the new shape and the old key disappears. Purely a
   read-side normalization; no separate migration file. The key is decoded into
   its own single-field struct, **never a mirror of `RootState`**: a mirror
   silently drops every field it does not restate, which is how the
   `adopted_home_volumes` registry (#144) would have been lost on the next write
   when it was added to `RootState`. The new shape wins wholesale — a non-empty
   `updates.agents` means the legacy key is ignored and never decoded, so a
   malformed legacy value fails the read only when `updates.agents` is empty.
3. **Retired `updater.yaml`** (already folded, #225): its fold target moves from
   the top-level `agents:` key to `updates.agents`. No new file, only a retarget.

## `internal/upgrade` state seam

`internal/upgrade` used to own its own JSON file via `Options.StatePath`. It now
takes an injected `Store` (`Load`, `Update`) so it does **not** import
`internal/sandbox/state`; `cmd` wires `launcherStore`, which maps the seam onto
`updates.launcher` through `state.WithRootState`. A nil `Store` falls back to an
in-memory one, preserving the unlimited test seam. A `Load` error is non-fatal:
`Check` reports it verbose and returns no result rather than failing the run.

`Update(mutate func(*LauncherState)) error` is the **only** mutation, and it
applies `mutate` to the state as it exists at write time, inside one locked
transaction. There is deliberately no `Save`: a caller that loaded a snapshot and
wrote it back later would clobber a concurrent launcher's newer dismissal or
check time — unrecoverable for the fold, whose legacy file is already deleted.
Every writer (the fold's merge, the check's persist) therefore merges through
`Update`.

## Deprecation

`upgrade.json` and `updater.yaml` are **internal state files**, not user-facing
config, so this change needs **no entry in the user-facing deprecation register**
(which covers flags, config keys, env vars, commands). The retired files are
noted in `CHANGELOG.md` and here, following the `updater.yaml` precedent (#225).

## Invariants

- A `--dry-run` writes nothing: the folds run only on the paths that already
  mutate state, never in the side-effect-free preview.
- A corrupt root document is never clobbered: `WithRootState` returns an error
  and the closure never runs.
- Every root-state mutation goes through `WithRootState`; contention defers
  (`blocking=false`) rather than blocking startup or prompting.
- **One binary per install.** A binary that predates this change does not know
  the `updates:` key, so if it writes the root document it drops that section
  (the upgrade-check cache and dismissals), though the sections it does know
  (`migrations:`) survive. This is accepted — the state root is internal 0.x
  state and the alternative (a transitional dual-write of the legacy keys) would
  contradict the one-document rule. No dual-write.

## Left to the builder

- The exact shape of the `internal/upgrade` store seam and its wiring in `cmd`.
- The call site and ordering of the `upgrade.json` fold relative to
  `upgrade.Check` and `migrateLegacyUpdaterState`.
- Docs sync, if any user-facing page describes the state layout.
