# Upgrade State Scoping - Review Fixes (PR #225) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Close the two P1 findings Greptile raised on the scoped-upgrade-state change: (1) a pre-existing project with no recorded `agent_version` silently rebuilds to latest (and fails offline) on its first post-migration run; (2) the new per-project `HomeState` writers can clobber a concurrent volume operation's write.

**Architecture:** Recovery reads the project's existing runner image label as a one-time `agent_version` fallback (state stays the source of truth). All `HomeState` read-modify-write moves behind one per-`(slug, agent)` host lock via a new `state.UpdateHomeState(k, mutate)` seam.

**Tech Stack:** Go 1.26, `syscall.Flock`, `internal/sandbox/{state,image,vm,volume}`.

**Spec:** `.operator-shared/specs/upgrade-state-scoping.md` (Baseline recovery, HomeState write serialization).

## Global Constraints

- Idiomatic Go; self-documenting, non-abbreviated identifiers. No inline comments unless the "why" is non-obvious.
- Follow `AGENTS.md`: `make fmt`, `make lint` (not `go vet`), `go test ./...`, `make check`.
- flock is not reentrant: never take the same key's home lock twice on one path. Lock order is root → home.
- Recovery must not make the image label the source of truth; it only bridges the gap left by dropping the global version.
- Preserve every existing writer's observable behavior except for the added serialization and the recovery prompt.

## Review Focus

- Pre-existing project (image present, no recorded version) → prompt fires naming the recovered version; offline → existing image reused, no failure. (Task 3)
- Genuine first run (no image) → no recovery, resolve at build. (Task 3)
- Concurrent volume op + upgrade/provenance write → no lost `home_volume`/`agent_version`. (Task 1, Task 4)
- Root lock held while taking the home lock in `pendingUpgrade` → no cycle (volume ops never take the root lock). (Task 4)

## Task 1: HomeState lock seam (`internal/sandbox/state`)

- [x] `homelock_test.go`: create-on-missing, preserve-other-fields, read-error-no-write, serialize (blocked while held), non-blocking skip, per-key independence, mkdir/open/flock error seams, path.
- [x] `homelock.go`: `LockHomeState(k, blocking)` + `UpdateHomeState(k, mutate)`; I/O seams `mkdirHomeLock`/`openHomeLock`/`flockHomeLock`; `homeLockPath`.

## Task 2: `image.ExistingAgentVersion` (`internal/sandbox/image`)

- [x] `existing_version_test.go`: reads the label; rejects `""`/`none`/`user-provided`; absent image; missing label.
- [x] `inspect.go`: `ExistingAgentVersion(ctx, slug, agent)` over `inspectRunnerImage`.

## Task 3: Baseline recovery (`internal/sandbox/vm`)

- [x] `upgrade_test.go`: no recorded version + image label → prompt names recovered + available, baseline persisted; recovered baseline + offline check → returns recovered version, no upgrade.
- [x] `existingAgentVersion` package seam (default no-image stub in `TestMain`).
- [x] `upgrade.go resolveBuildVersion`: recover when `current == ""`; `recoverAgentVersion` records via `UpdateHomeState`.

## Task 4: Adopt `UpdateHomeState` in all HomeState writers

- [x] `vm/run_envstate.go`, `vm/provenance.go`, `vm/upgrade.go` (offered set).
- [x] `volume/state.go` (`recoverOrCreateHome`, `EnsureNewHome`, `RecordHomeImage`), `volume/operations.go`, `volume/volume.go`.
- [x] Update the three tests asserting the old read-error wording.

## Task 5: Docs and changelog

- [x] Spec: Baseline recovery, HomeState write serialization, invariants, edge cases, revised image-label non-goal.
- [x] `CHANGELOG.md` `[Unreleased]`: Fixed (silent rebuild/offline) + Changed (home lock).
- [x] `docs/runner-image.md`: recovery/offline sentence.
