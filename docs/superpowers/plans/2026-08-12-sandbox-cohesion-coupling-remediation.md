# sandbox Cohesion & Coupling Remediation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Raise cohesion (single responsibility per file/package) and reduce coupling (internal and external) across the modularized `internal/sandbox` tree, based on a 12-subagent review, without changing the CLI-visible `sandbox.*` surface or runtime behavior.

**Architecture:** Work from a prior review (see `-- Design doc`). Order is dependency-aware: delete dead code first (lowest risk), unify the re-export seam style + remove dead mutable seams, then domain-mismatched symbol moves + parameterization (unlocks file splits), then per-file cohesion splits, then DRY + interface-narrowing cleanups, then cosmetics. Every task leaves `make check` green. No behavior change is introduced — tests that exist are preserved/moved, and new tests are only added where a seam is removed or a symbol relocates.

**Tech Stack:** Go 1.26, `pkg yaml.v3`, moby (docker) client, microsandbox SDK, `golangci-lint`.

**Design doc:** compound review — `docs/superpowers/plans/2026-08-12-...` (this file's preceding advice is captured in the intro of `docs/superpowers/specs/2026-08-11-sandbox-modularization-design.md`; the per-module findings are summarized in each task's motivation).

## Global Constraints

- Module path prefix: `gitlab.inoio.de/inoio/opencode-msb/internal`.
- **PRESERVE CLI SURFACE.** `cmd/opencode-msb` must compile unchanged after every task. It references `sandbox.CheckAll`, `sandbox.AutoPrune`, `sandbox.EnsureImage`, `sandbox.ListImages`, `sandbox.ListVolumes`, `sandbox.CmdMigrate`, `sandbox.CmdReset`, `sandbox.CmdEdit`, `sandbox.Prune` (see `commands.go:99,297`, `commands_system.go:34,40,179,206,223,237,251`). Tasks that touch the facade must keep these reachable from the `cmd` package.
- Run `golangci-lint fmt` (make fmt) after edits; do NOT hand-format. Run `golangci-lint run` (make lint) and `go test ./...` (make test). Final gate per task: `make check`.
- **No Go import cycles.** Keep the DAG: leaves (`naming`/`options`/`state`/`msb`/`docker`) → feature modules (`doctor`/`image`/`volume`/`pruning`/`reprovision`) → `session` (orchestrator) → `sandbox` core → `cmd`. **The `sandbox` core must never be imported by a leaf or feature module.** `naming` imports only stdlib today; do not add internal imports to it.
- **PRESERVE GIT HISTORY (STRICT) where content moves.** Use `git mv <old> <new>` before editing moved content so git records the rename. Never `cp`+`add`+delete, never hand-create a moved file and delete the original. For symbol-only moves within a package (no file relocation) plain edits are fine. Re-export stubs have no history and are created normally.
- Respect `.gitignore` on `docs/superpowers` (do not `git add` that tree; commit code changes only).
- Follow AGENTS.md test-first where new behavior is introduced; this remediation primarily *moves/simplifies* code, so existing tests move with it. When deleting a seam, first verify the only references are in `_test.go` files (as confirmed in the review and re-verified below in each task).
- Do NOT delete any test that exercises real production behavior; inverted — when a dead symbol is removed, its test-only references are removed with it.
- `golangci-lint` flags: preserve existing `//nolint` directives that are still correct after moves; remove them only if they become unused.

---

## File structure map (targets after this plan)

| Package | Change |
|---|---|
| `sandbox/` (root) | Facade retained (CLI surface); `reexport_doctor.go` dead seams (`CheckAllFunc`, `SetEnsureInstalled`) removed; `reexport_*.go` raw `var Fn = sub.Fn` re-exports converted to `func` wrappers; `msb_client.go` trimmed to cmd-referenced set; `reexport_state.go` `StateDir()` getter replaced with a `StateDir` alias. |
| `sandbox/naming/` | `StaleType` + `typeName` + `String()` moved to `pruning`; `ArtifactInfo` moved to `artifact.go`; orphan doc comment removed; `FindHashSuffix` unexported; `config_paths.go` stale comment fixed. |
| `sandbox/options/` | `ExitError` moved to `session`; `AutoFlag` moved to `session`; `EnvKeyValueParts` moved to `reprovision`; `MibPerGib` made private; timeouts split to `timeouts.go`; `sizes.go`/`options.go` renamed to focused names. |
| `sandbox/state/` | `SlugDir`/`slugPath` helpers centralize layout; `StateFile` unexported; `EnvState`/`SecretState` share a `FingerprintState`; `flock_*.go` collapsed to `flock.go`. (`Root`-parameterization deferred — large; see Task 3 note.) |
| `sandbox/pruning/` | `pruneCloneVolume` + its tests deleted; `MsbClient` alias removed; `StaleType` adopted from `naming`; `report.go` `DryRun` reshaped; `prune.go` split into `catalog.go`/`prune.go`/`remove.go`. |
| `sandbox/session/` | `ExitError` + `AutoFlag` adopted; `list.go` `sandboxHandle`/`filterSandboxes` deleted; `run.go`/`vm.go` split into focused files; `Run`/`Shell` share `runAttach`; `reexport_query.go` merged into `reexport_session.go`. |
| `sandbox/reprovision/` | `EnvKeyValueParts` adopted; `tmpMountPath` sourced from shared owner; shared `parseKeyValueLines` helper. (Full sub-package split of the 4 responsibilities is deferred — see Task 7 note.) |
| `sandbox/volume/` | `volumeHandle`/`filterVolumes` deleted; `actionQuit`/friend constants exported as a typed `VolumeAction`; `volume.go` split into lifecycle/`state`/`prompt`. |
| `sandbox/docker/` | `userBuildArgs` `USER_UID`/`USER_GID` contract co-located; `CheckDockerAPI` returns a typed reachability result. (Mock extraction + DI — deferred; see Task 10 note.) |
| `sandbox/msb/`, `sandbox/image/`, `sandbox/doctor/testmock.go`, `sandbox/docker/testmock.go` | `IsStoppedStatus` derived from `IsSandboxActive`; `image.go` split into `build.go`/`inspect.go`/`metadata.go`/`tags.go`; `referencesBase`/`referencesDindBase` deduped; `doctor.go` `checkMsb` split; `checkKvm` build-tag normalized. |

**Deferred (out of scope this plan):** full mock-out-of-binary extraction (msb/docker `testmock.go`), `msb.Client` SDK-leak encapsulation, full `state` Root-parameterization, full `reprovision` sub-package split, root `sandbox` facade deletion. These are larger restructures that risk CLI-surface churn; they are tracked as follow-ups at the end.

---

### Task 1: Delete dead code across modules

Removes verified-dead production symbols that exist only to be exercised by tests, plus the redundant plumbing they carry.

**Files:**
- Modify: `internal/sandbox/pruning/prune.go`
- Modify: `internal/sandbox/pruning/prune_client_test.go`
- Modify: `internal/sandbox/session/list.go`
- Modify: `internal/sandbox/session/query_test.go`
- Modify: `internal/sandbox/volume/list.go`
- Modify: `internal/sandbox/volume/list_test.go`
- Modify: `internal/sandbox/session/run.go`

**Interfaces:**
- Consumes: existing `pruneCloneVolumes` (plural, prune.go:370) is the live pipeline; existing `ListSandboxes` (session/list.go) and `ListVolumes` (volume/list.go) already inline the production filter.
- Produces: none — this task only removes surface.

- [ ] **Step 1: Verify each target symbol is production-dead**
Run:
```
rg -n 'pruneCloneVolume\b' internal/sandbox/pruning/ --glob '*.go'
rg -n 'filterSandboxes\b|sandboxHandle\b' internal/sandbox/session/ --glob '*.go'
rg -n 'filterVolumes\b|volumeHandle\b' internal/sandbox/volume/ --glob '*.go'
```
Expected: the only non-test references are the symbol's own definition. Any hit outside `_test.go` (other than the definition) means the symbol is NOT dead — stop and report back rather than deleting.

- [ ] **Step 2: Delete `pruneCloneVolume` and its three tests**
Delete the function `pruneCloneVolume` (prune.go:562-...) and the three call sites/blocks `TestPruneCloneVolume_*` in `prune_client_test.go` (lines ~299-352). Keep `pruneCloneVolumes` (plural) untouched.
Run: `golangci-lint fmt` then check no remaining symbol references. Expected: compiler and lint clean.

- [ ] **Step 3: Delete `sandboxHandle` + `filterSandboxes` and their test**
Remove the type `sandboxHandle` and func `filterSandboxes` (session/list.go:18-22) and the test using them (`session/query_test.go:8-15`). `ListSandboxes` keeps its inline `strings.HasPrefix(handle, naming.VmPrefix)` predicate (list.go:40-43).
Run: `go test ./internal/sandbox/session/...`. Expected: PASS.

- [ ] **Step 4: Delete `volumeHandle` + `filterVolumes` and their test**
Remove the type `volumeHandle` and func `filterVolumes` (volume/list.go:21-25) and the test using them (`volume/list_test.go:8-14`). `ListVolumes` keeps its inline prefix filter.
Run: `go test ./internal/sandbox/volume/...`. Expected: PASS.

- [ ] **Step 5: Remove commented-out code + unused field in `sandboxSession`**
In `run.go`, delete the commented-out `git.PruneWorktrees` block in `cleanup()` and the `cwd` field on `sandboxSession` plus any `cwd:` assignment if the field is then unused.
Run: `go build ./...`. Expected: builds (and confirms the `internal/git` import is still needed elsewhere; if `git` becomes unused in `run.go`, remove that import — the `git` package remains used by session worktree files).

- [ ] **Step 6: Full gate**
Run: `make check`. Expected: PASS.

- [ ] **Step 7: Commit**
```bash
git add -A
git commit -m "refactor: remove dead code across sandbox modules"
```

---

### Task 2: Remove dead mutable seams in the `sandbox` facade

Deletes `sandbox.CheckAllFunc`/`sandbox.SetEnsureInstalled` (referenced nowhere) and unwinds the value-capture correctness hazard in `sandbox.CheckAll`, while keeping the CLI-visible `sandbox.CheckAll` and the single `doctor` seam.

**Files:**
- Modify: `internal/sandbox/reexport_doctor.go`
- Modify: `internal/sandbox/session/run.go` (call sites `doctor.CheckAll`/`doctor.CheckDocker` if they were routed through facade — verify below)

**Interfaces:**
- Consumes: `doctor.CheckAll`, `doctor.SetEnsureInstalled`, `doctor.CheckAllFunc` (existing seams in `internal/sandbox/doctor/doctor.go`).
- Produces: `sandbox.CheckAll(ctx context.Context, ui termio.UI) bool` still exists and now delegates *directly* to `doctor.CheckAll`. `cmd/opencode-msb` references `sandbox.CheckAll` only (commands_system.go:34,65), and its tests patch `doctor.CheckAllFunc`/`doctor.SetEnsureInstalled` directly (cli_run_shell_test.go:30-35).

- [ ] **Step 1: Confirm the facade seams are unreferenced**
Run: `rg -rn 'sandbox\.(CheckAllFunc|SetEnsureInstalled)\b' --glob '*.go'`
Expected: no hits. (Royal guard: if any hit appears, do NOT delete; re-evaluate.)

- [ ] **Step 2: Rewrite `reexport_doctor.go`**
Replace the whole file body so `CheckAll` delegates straight through and the two dead vars are gone:

```go
// Package sandbox re-exports doctor module symbols to preserve the legacy
// sandbox core public API surface for cmd/opencode-msb.
package sandbox

import (
	"context"

	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox/doctor"
	"gitlab.inoio.de/inoio/opencode-msb/internal/termio"
)

// CheckAll runs all prerequisite checks and reports orphaned VMs.
func CheckAll(ctx context.Context, ui termio.UI) bool {
	return doctor.CheckAll(ctx, ui)
}
```

Delete `var CheckAllFunc`, `var SetEnsureInstalled`, and the import of `doctor` only if it becomes unused (the new body still imports `doctor`).

- [ ] **Step 3: Verify `session` calls `doctor` directly (not via facade)**
Run: `rg -n 'doctor\.' internal/sandbox/session/ --glob '*.go'`
Expected: `session` already imports `doctor` directly; no change needed there. If a `sandbox.` indirection exists for doctor in session, repoint it to `doctor.`.

- [ ] **Step 4: Run tests + lint**
Run: `go test ./internal/sandbox/... ./cmd/...` and `golangci-lint run`.
Expected: PASS. `cmd` tests still override `doctor.CheckAllFunc` via the doctor package import in `cli_run_shell_test.go`.

- [ ] **Step 5: Full gate**
Run: `make check`. Expected: PASS.

- [ ] **Step 6: Commit**
```bash
git add -A
git commit -m "refactor: remove dead doctor seams from sandbox facade"
```

---

### Task 3: Move domain-mismatched symbols to owning packages + parameterize state

Relocates misplaced symbols so each package owns its domain, and narrows the `state` package's mutable-global surface. This unblocks the file splits in Tasks 4-6.

**Files:**
- Modify: `internal/sandbox/naming/naming.go`, `internal/sandbox/naming/artifact.go`, `internal/sandbox/naming/naming_test.go`
- Modify: `internal/sandbox/pruning/prune.go`, `internal/sandbox/pruning/report.go`, `internal/sandbox/pruning/prune_test.go`
- Modify: `internal/sandbox/options/options.go`, `internal/sandbox/options/sizes.go`
- Modify: `internal/sandbox/options/options_test.go`
- Modify: `internal/sandbox/reprovision/secrets.go`, `internal/sandbox/reprovision/config_files.go`
- Modify: `internal/sandbox/session/run.go`, `internal/sandbox/session/vm.go`
- Modify: `internal/sandbox/state/state.go`, `internal/sandbox/state/clientlock.go`, `internal/sandbox/state/flock_linux.go`, `internal/sandbox/state/flock_darwin.go`
- Modify: `internal/sandbox/reexport_options.go`, `internal/sandbox/reexport_state.go`

**Interfaces:**
- Consumes: `naming.StaleType*` currently declared in `naming.go:28-44`; consumers are `pruning/*`.
- Produces:
  - `pruning.StaleType`, `pruning.StaleTypeVM/Volume/DockerImage/MsbImage`, and `String()` (moved from naming).
  - `session.ExitError` (moved from options; existing fields/`Unwrap`/`Error` unchanged), still re-exported as `sandbox.ExitError` for `cmd` (main.go:16).
  - `session.AutoFlag` (moved from options).
  - `reprovision.EnvKeyValueParts` (moved from options).
  - `state` internal helpers `slugDir(slug) string`, `slugPath(slug string, parts ...string) string`; unexported `stateFile(slug string) string`.
  - `sandbox.StateDir` re-export: a `var StateDir = state.StateDir` alias (replacing the `func StateDir()` getter in `reexport_state.go:19`).

- [ ] **Step 1: Move `StaleType` from `naming` to `pruning`**
In `naming/naming.go` delete `StaleType`, the four constants, `typeName`, and `String()`. In `pruning` add them (a new `stale.go` in the pruning package, created normally — content is re-homed, history preserved via `git mv` not possible across packages, so create the file and `git add -N` it per repo convention for split content). Update `pruning/prune.go:9` and `report.go:2` references from `naming.StaleType*` → `StaleType*`. Update `naming/naming_test.go` `TestStaleTypeString` to move into a `pruning/*_test.go`. Remove the now-unused `naming` imports where pruning no longer needs them for StaleType (pruning still uses `naming.` parser functions elsewhere — keep what's used).
Run: `go test ./internal/sandbox/naming/... ./internal/sandbox/pruning/...`. Expected: PASS.

- [ ] **Step 2: Move `ExitError` from `options` to `session`**
Move the `ExitError` type + its methods and the `ExitError`/`ExitCode` constants from `options/options.go` into `session` (new `exit.go` in session). Update `session/run.go:274,408` to use the local type. Update `reexport_options.go` to remove `ExitError` and add it to a session re-export (or keep a `type ExitError = session.ExitError` alias in `reexport_session.go`). `cmd` imports `sandbox.ExitError` (main.go:16) — keep that alias reachable.
Run: `go build ./...` and `go test ./cmd/opencode-msb/...`. Expected: PASS (CLI unchanged).

- [ ] **Step 3: Move `AutoFlag` to `session`, `EnvKeyValueParts` to `reprovision`**
- Remove `AutoFlag` from `options/sizes.go`; add `const AutoFlag = "--auto-reap"` (keep the exact string value) to `session` where `run.go:39` uses it.
- Remove `EnvKeyValueParts` from `options/sizes.go`; add it to `reprovision` (used by `secrets.go:53` and `config_files.go:166`). Remove `ParseMemory`/`EnvKeyValueParts` usage of `options` in `reprovision` only if `ParseMemory` is still used elsewhere there — if `EnvKeyValueParts` was the only options reference in reprovision, remove the `options` import from those files.
Verify with `rg -n 'EnvKeyValueParts|AutoFlag' internal/ --glob '*.go'`.
Run: `go build ./...`. Expected: builds.

- [ ] **Step 4: Make `MibPerGib` private and split timeouts to `timeouts.go`**
- In `options/sizes.go` rename `MibPerGib` → `mibPerGib` (unexported) since it is only used inside options' memory parsing; update `session/vm.go:372` which used `options.MibPerGib` — introduce a small `session`-local `const mibPerGib uint32 = 1024` OR a shared `options.ParseMemoryGiB` helper. Prefer adding `options.ParseMemoryGiB(gib uint32) uint32 { return gib * mibPerGib }` and call that from `vm.go`.
- Move the timeout constants (`SandboxStopTimeout`, `DefaultVMIdleTimeout`) out of `sizes.go` into a new `timeouts.go` in the `options` package.
Run: `go build ./...` and `golangci-lint run`. Expected: PASS.

- [ ] **Step 5: Collapse identical `flock_*.go` into `flock.go`**
Delete `flock_darwin.go` and `flock_linux.go` (byte-identical implementations for `FlockExclusive`, `FlockExclusiveNB`). Create a single `flock.go` (no build tags) containing the shared `syscall.Flock` logic. Use `git mv` on one of the files to the target then delete the other, to preserve history.
Run: `go test ./internal/sandbox/state/...` on both `GOOS=linux` and `GOOS=darwin` (`GOOS=darwin go test ./internal/sandbox/state/...` if a darwin host is unavailable, at least `GOOS=darwin go build ./...`). Expected: PASS.

- [ ] **Step 6: Centralize state layout + narrow `StateFile`**
In `state/state.go` and `state/clientlock.go`, add `func slugDir(slug string) string` and `func slugPath(slug string, parts ...string) string` implemented via `filepath.Join(stateRoot(), slug, ...)`. Route `state.go:53-55`, `state.go:111`, `clientlock.go:16,20,41` through them. Rename exported `StateFile(slug)` → unexported `stateFile`, updating `internal/sandbox/config_paths_test_in_sandbox.go:16` (use `ReadState`/`WriteState` instead, or a test-local path helper).
Verify no remaining exported `StateFile` references: `rg -rn 'state\.StateFile\b|\.StateFile\b' internal/ --glob '*.go'`.
Run: `go test ./internal/sandbox/state/... ./internal/sandbox/volume/... ./internal/sandbox/pruning/... ./internal/sandbox/session/...`. Expected: PASS.

- [ ] **Step 7: Replace `sandbox.StateDir()` getter with alias**
In `reexport_state.go` replace `func StateDir() string { return state.StateDir }` with `var StateDir = state.StateDir` (a plain var re-export, guarded with `//nolint:gochecknoglobals`). Verify `cmd` references `sandbox.StateDir` as a value, not a call — `rg -n 'StateDir\(\)' cmd/`. If cmd calls `sandbox.StateDir()`, keep the func form; otherwise adopt the var alias.
Run: `go build ./...`. Expected: builds.

- [ ] **Step 8: Full gate**
Run: `make check`. Expected: PASS.

- [ ] **Step 9: Commit**
```bash
git add -A
git commit -m "refactor: relocate domain-misplaced symbols and narrow state surface"
```

> **Deferred note (recorded, not implemented here):** full `state` Root-parameterization (making the state dir an explicit constructor input so sibling tests do `NewStore(t.TempDir())`) is a larger change touching dozens of test call sites. Track as a follow-up (see Task 10).

---

### Task 4: Split session `vm.go` and `run.go` into focused files

Reduces the two largest cohesion violations in `session` by separating lifecycle, resources, naming, env, and control.

**Files:**
- Modify: `internal/sandbox/session/vm.go`, `internal/sandbox/session/run.go`
- Create: `internal/sandbox/session/vm_lifecycle.go`, `internal/sandbox/session/vm_resources.go`, `internal/sandbox/session/vm_name.go`, `internal/sandbox/session/vm_env.go`, `internal/sandbox/session/vm_control.go`
- Create: `internal/sandbox/session/run_orchestrate.go`, `internal/sandbox/session/reconfig.go`
- Modify: `internal/sandbox/session/run_envstate_test.go`

**Interfaces:**
- Consumes: existing session-internal symbols (`ensureProjectVM`, `createProjectVM`, `decideVMAction`, `reconcileResourceConfig`, `summarizeConflicts`, `projectVMName`, `buildProjectVMEnv`, `acquireProjectFlock`, `stopOrKillProjectVM`, `StopProjectVM`, `KillProjectVM`; `Run`, `Shell`, `BuildImage`, `prepareSandbox`, `finalizeRun`, `buildAttachCommand`, `buildOpencodeArgs`, `decideReconfig`, `restartDaemons`, `setUpSandbox`).
- Produces: same symbols, relocated into new files. **All names and signatures must be identical** so no other package or test changes. The `run_envstate_test.go` file already exists as a separate test file; after the split the env/secret-state source (`currentEnvState`/`currentSecretState`/`persistEnvSecrets`) should live in a matching source file (`run_envstate.go`).

- [ ] **Step 1: Mechanical split of `vm.go`**
Move symbol bodies (via `git mv`/re-edit per Global Constraints) into:
- `vm_lifecycle.go`: `ensureProjectVM`, `createProjectVM`, `decideVMAction`, `acquireProjectFlock`
- `vm_resources.go`: `reconcileResourceConfig`, `summarizeConflicts`
- `vm_name.go`: `projectVMName`, plus `projectVMName`-related constants
- `vm_env.go`: `buildProjectVMEnv`
- `vm_control.go`: `stopOrKillProjectVM`, `StopProjectVM`, `KillProjectVM`

Ensure each file has ONLY the imports it uses (drop unused imports per file). Do not rename symbols. Preserve any `//nolint:gocognit,funlen,gocyclo,cyclop` on functions that still exceed limits. This is the ideal place to reduce those limits where a split naturally shrinks the function — but only if the function's body genuinely reduces; do not reindent/rewrite logic.
Run: `go build ./internal/sandbox/session/...` and `go test ./internal/sandbox/session/...`. Expected: PASS.

- [ ] **Step 2: Mechanical split of `run.go`**
Move:
- `run_orchestrate.go`: `Run`, `Shell`, `BuildImage`, `prepareSandbox`, `finalizeRun`, `buildAttachCommand`, `buildOpencodeArgs` (all orchestration + attach wiring)
- `run_envstate.go`: `currentEnvState`, `currentSecretState`, `persistEnvSecrets` (STATE: matches existing `run_envstate_test.go`)
- `reconfig.go`: `decideReconfig`, `restartDaemons`, `setUpSandbox`

Trim per-file imports. Keep behavior identical.
Run: `go test ./internal/sandbox/session/...`. Expected: PASS.

- [ ] **Step 3: Verify `git` block-structure and history**
Run: `git diff --stat` and confirm `*.go` under `session/` show renames where files were `git mv`'d.
Expected: R entries, not A+D pairs.

- [ ] **Step 4: Full gate**
Run: `make check`. Expected: PASS.

- [ ] **Step 5: Commit**
```bash
git add -A
git commit -m "refactor: split session run.go and vm.go into focused files"
```

---

### Task 5: Split `volume.go`, `image.go`, and `prune.go` into focused files

Extends the same cohesion treatment to the next three largest overstuffed files.

**Files:**
- Modify: `internal/sandbox/volume/volume.go`
- Create: `internal/sandbox/volume/state.go`, `internal/sandbox/volume/prompt.go`
- Modify: `internal/sandbox/image/image.go`
- Create: `internal/sandbox/image/build.go`, `internal/sandbox/image/inspect.go`, `internal/sandbox/image/metadata.go`, `internal/sandbox/image/tags.go`
- Modify: `internal/sandbox/pruning/prune.go`
- Create: `internal/sandbox/pruning/catalog.go`, `internal/sandbox/pruning/remove.go`

**Interfaces:**
- Consumes: existing internal symbols (see each task's source file).
- Produces: same symbols, relocated verbatim. No renames, no signature changes. Behavior identical.

- [ ] **Step 1: Split `volume.go`**
- `volume/state.go`: `ResolveHomeVolume`, `EnsureNewHome`, `RecordHomeImage` (state read/write + digest flows)
- `volume/prompt.go`: `ResolveHomeAction`, `actionLabel`, and the action constants (see Task 8 for the typed-enum upgrade that follows)
- `volume.go` keeps: `HomeVolumeName`, `PrefillVolume`, `CopyVolume`, `cleanupVolume`, mount constants
Run: `go test ./internal/sandbox/volume/...`. Expected: PASS.

- [ ] **Step 2: Split `image.go`**
- `image/build.go`: `EnsureImage`, `EnsureImageWithClient`, `ensureRunnerImage`, `buildRunnerImage`, `referencesBase`, `referencesDindBase`
- `image/inspect.go`: `inspectExistingImage`, `parseImageEnv`
- `image/metadata.go`: `envDir`, `envMetaFile`, `storeImageEnv`, `loadImageEnv`
- `image/tags.go`: `imageTag`, `runnerTag`
Run: `go test ./internal/sandbox/image/...`. Expected: PASS.

- [ ] **Step 3: Split `prune.go`**
- `pruning/catalog.go`: `buildCatalog`, `findStaleVMs`, `isStaleSlug`, `staleVM`
- `pruning/prune.go`: `Prune`, `catalogAndPrune`, phase drivers (orphan/cascade/active-cleanup), removed-singleton logic
- `pruning/remove.go`: `removeHomeVolumes`, `removeMSBImages`, `removeDockerImages` and the three active-VM-filtered variants (the artifact-removal helpers)
Run: `go test ./internal/sandbox/pruning/...`. Expected: PASS.

- [ ] **Step 4: Trim per-file imports and re-run**
Ensure no file carries unused imports after the split (drop them). Re-run all three package test suites.
Run: `go test ./internal/sandbox/volume/... ./internal/sandbox/image/... ./internal/sandbox/pruning/...`. Expected: PASS.

- [ ] **Step 5: Full gate**
Run: `make check`. Expected: PASS.

- [ ] **Step 6: Commit**
```bash
git add -A
git commit -m "refactor: split volume, image, and pruning files by responsibility"
```

---

### Task 6: Split doctor and cleanup cross-platform/naming

**Files:**
- Modify: `internal/sandbox/doctor/doctor.go`, `internal/sandbox/doctor/doctor_linux.go`, `internal/sandbox/doctor/doctor_darwin.go`
- Create: `internal/sandbox/doctor/install.go` (from `checkMsb` split)

**Interfaces:**
- Consumes: existing `checkMsb`, `ensureInstalled`, `checkKvm`, `checkDoctor` (build-tag split), `shellRcDefault`.
- Produces: `ensureMsbInstalled(ctx, ui) error`, `msbBinPath()`, `appendPathHint(home, shell)`, `checkKvm` moved to a darwin no-op stub.

- [ ] **Step 1: Split `checkMsb` into three single-responsibility helpers**
In `doctor.go`, replace `checkMsb`'s body with composition of three new functions in `doctor/install.go`:
- `ensureMsbInstalled(ctx, ui)` — calls `ensureInstalled`
- `msbBinPath()` — resolves the msb binary path (no side effects)
- `appendPathHint(home, shell)` — performs the `os.Setenv` PATH mutation + rc-file message
Keep the orchestration contract of `checkMsb` identical (same side effects on PATH).
Run: `go test ./internal/sandbox/doctor/...`. Expected: PASS.

- [ ] **Step 2: Normalize OS dispatch to build tags only**
Move `checkKvm` (currently in `doctor.go` with a `runtime.GOOS` branch) to the Linux build-tagged file, and add a no-op `checkKvm` in `doctor_darwin.go` (mirroring `shellRcDefault`). Remove the `runtime` import from `doctor.go` if now unused. Do NOT change `checkDoctor`'s build-tag split.
Run: `golangci-lint run` and `GOOS=darwin go build ./internal/sandbox/doctor/...`. Expected: PASS.

- [ ] **Step 3: Dedupe via shared parse helper in reprovision**
In `reprovision`, add `parseKeyValueLines(data string, onLine func(key, value string) error) error` (byte-splitting skeleton with `#`-comment skip and `=`/trim handling) and have both `config_files.go` `BuildEnvMap` and `secrets.go` `ParseSecretSpecLegacy` call it, each keeping only its key-specific post-processing. This removes the `EnvKeyValueParts` coupling-by-coincidence from Task 3.
Run: `go test ./internal/sandbox/reprovision/...`. Expected: PASS.

- [ ] **Step 4: Full gate**
Run: `make check`. Expected: PASS.

- [ ] **Step 5: Commit**
```bash
git add -A
git commit -m "refactor: split doctor checkMsb, normalize checkKvm, dedupe parsing"
```

---

### Task 7: Narrow interfaces — typed VolumeAction, options/msb/state narrowing

**Files:**
- Modify: `internal/sandbox/volume/volume.go`, `internal/sandbox/volume/prompt.go` (from Task 5)
- Modify: `internal/sandbox/session/run.go`
- Modify: `internal/sandbox/options/sizes.go`
- Modify: `internal/sandbox/msb/msb.go`
- Modify: `internal/sandbox/naming/artifact.go`, `internal/sandbox/naming/naming.go`, `internal/sandbox/naming/naming_test.go`
- Modify: `internal/sandbox/reexport_volume.go` (re-export any new `VolumeAction` if cmd needs it — verify first)

**Interfaces:**
- Consumes: volume's unexported `actionKeep`/`actionMigrate`/`actionReset`/`actionQuit` string constants; `session/run.go:406` compares `action == "4"`.
- Produces:
  - `volume.VolumeAction` typed enum with constants `ActionKeep`/`ActionMigrate`/`ActionReset`/`ActionQuit` and a `String()`/`FromKey(key string)` API; `ResolveHomeAction` returns `volume.VolumeAction`; `session/run.go` compares `action == volume.ActionQuit`.
  - `naming.ArtifactInfo` moved from `naming.go:46` to `artifact.go`; `FindHashSuffix` renamed `findHashSuffix` (unexported); orphan `// ArtifactInfo is the result of an artifact name parse operation.` doc removed.
  - `msb.IsStoppedStatus(status)` derived from `msb.IsSandboxActive`.

- [ ] **Step 1: Introduce `VolumeAction` typed enum**
In `volume/prompt.go`, replace the four unexported string constants with an exported typed enum and its mapping:

```go
// VolumeAction is the user-selected disposition for an existing home volume.
type VolumeAction int

const (
	ActionKeep VolumeAction = iota
	ActionMigrate
	ActionReset
	ActionQuit
)

func (a VolumeAction) String() string { ... }          // "keep"/"migrate"/"reset"/"quit"

// FromKey maps the numeric prompt key to an action.
func FromKey(key string) (VolumeAction, error) { ... }  // "1".."4", error otherwise
```

Update `ResolveHomeAction` to return `VolumeAction`, `actionLabel` to take `VolumeAction`, `ApplyHomeAction` to branch on `Action*`. Update `session/run.go:406` to compare `action == volume.ActionQuit` (remove the `"4"` literal). Verify the user-facing prompt strings are byte-identical to today.
Run: `go test ./internal/sandbox/volume/... ./internal/sandbox/session/...`. Expected: PASS.

- [ ] **Step 2: Move `ArtifactInfo` and unexport helpers in `naming`**
Move `ArtifactInfo` from `naming.go:46` to `artifact.go`. Rename `FindHashSuffix` → `findHashSuffix` (it has no external callers). Remove the orphan doc comment on `ArtifactInfo` in `artifact.go:151-152`. Keep `ArtifactFor` and `ExtractProjectSlugAndDigest` both (removing dual-dispatch is lower priority; see note) — only unify if trivial.
Run: `go test ./internal/sandbox/naming/... ./internal/sandbox/pruning/... ./internal/sandbox/volume/...`. Expected: PASS.

- [ ] **Step 3: Derive `IsStoppedStatus` from `IsSandboxActive` in msb**
In `msb.go`, reimplement `IsStoppedStatus(status)` as `return !IsSandboxActive(status)` (dropping the parallel switch table). Preserve behavior for the statuses both predicates already recognize.
Run: `go test ./internal/sandbox/msb/...`. Expected: PASS.

- [ ] **Step 4: Fix stale docs + untyped defaults (options/naming low-hanging)**
- In `options/sizes.go`, retype `DefaultMemoryMiB`/`DefaultTmpSizeMiB` as `uint32` so they match their `uint32`-returning funcs.
- In `naming.go`, fix the stale comment referencing the nonexistent `config_paths.go`.
- In `docker/testmock.go:82`, fix `MockDockerClient is the zero implementation of sandbox.DockerClient` → `docker.Client` (and drop the dead branch in `newDefaultErrorDockerClient`).
Run: `golangci-lint run`. Expected: PASS.

- [ ] **Step 5: Full gate**
Run: `make check`. Expected: PASS.

- [ ] **Step 6: Commit**
```bash
git add -A
git commit -m "refactor: typed volume actions and narrow interface surfaces"
```

> **Deferred note (recorded):** helper `ParseMemory` returning an error instead of silently defaulting, the `reprovision` 4-responsibility sub-package split, and the broad-`RunOptions`-param narrowing in volume are tracked as follow-ups (Task 10) to avoid behavior risk mid-plan.

---

### Task 8: DRY — deduplicate within modules

Removes duplicated logic that couples otherwise-separate code paths to each other.

**Files:**
- Modify: `internal/sandbox/image/image.go` (or `build.go` from Task 5)
- Modify: `internal/sandbox/session/run_orchestrate.go` (from Task 4)
- Modify: `internal/sandbox/volume/volume.go` (state-building), `internal/sandbox/volume/prompt.go`
- Modify: `internal/sandbox/state/state.go`

**Interfaces:**
- Consumes: existing `referencesBase`/`referencesDindBase`; `Run`/`Shell` lease sequence; three "new zeroed HomeState" blocks; `EnvState`/`SecretState` structs.
- Produces: `image.referencesImage(dockerfile []byte, tag string) bool`; `session.runAttach(ctx, session, ui, bashArgs ...string) error`; `state.NewHomeState(homeVolume, digest)`; `state.FingerprintState`.

- [ ] **Step 1: Dedupe Dockerfile FROM-scan in `image`**
Replace `referencesBase`/`referencesDindBase` with a single `referencesImage(dockerfile []byte, tag string) bool` taking `naming.BaseTag`/`naming.DindBaseTag`. Keep the two exported behaviors identical.
Run: `go test ./internal/sandbox/image/...`. Expected: PASS.

- [ ] **Step 2: Extract shared `runAttach` for `Run`/`Shell`**
In `run_orchestrate.go`, extract `runAttach(ctx, session, ui, bashArgs ...string) error` capturing the lease-acquire (with deferred safety-net release), `session.sb.Attach`, explicit release, `reapOnLastClient`, and `finalizeRun`. `Run` and `Shell` call it with their differing bash args (`-c setup` vs `-l`). Preserve the exact call order and error handling.
Run: `go test ./internal/sandbox/session/... ./cmd/...`. Expected: PASS.

- [ ] **Step 3: Single `state.NewHomeState` constructor**
Add `state.NewHomeState(homeVolume, digest string) HomeState` returning a `HomeState` with zeroed `EnvState`/`SecretState`. Replace the three handwritten blocks (`volume.go EnsureNewHome`, `volume.go ApplyHomeAction`, `operations.go volumeOp`) with it, keeping each call site's `//nolint:exhaustruct` comment or removing it if the constructor makes it unnecessary.
Run: `go test ./internal/sandbox/volume/... ./internal/sandbox/state/...`. Expected: PASS.

- [ ] **Step 4: Shared `FingerprintState` type for `EnvState`/`SecretState`**
In `state.go`, define `type FingerprintState struct { Hash string; Names []string; ... }` (the shared shape) and alias `type EnvState = FingerprintState` and `type SecretState = FingerprintState`. Update field accessors if any differ; keep JSON/YAML serialization field names identical so on-disk state is unchanged.
Run: `go test ./internal/sandbox/state/... ./internal/sandbox/session/... ./internal/sandbox/reprovision/...`. Expected: PASS.

- [ ] **Step 5: Full gate**
Run: `make check`. Expected: PASS.

- [ ] **Step 6: Commit**
```bash
git add -A
git commit -m "refactor: deduplicate build-scan, run attach, and state constructors"
```

---

### Task 9: Facade cleanup — func wrappers, merged session re-exports, trimmed msb surface

Converts remaining raw `var Fn = sub.Fn` re-exports to non-mutable `func` wrappers (matching `reexport_session.go`'s style), merges the two session re-export files, and trims `msb_client.go` to what `cmd` actually references.

**Files:**
- Modify: `internal/sandbox/reexport_image.go`, `internal/sandbox/reexport_prune.go`, `internal/sandbox/reexport_volume.go`, `internal/sandbox/reexport_config.go`
- Modify: `internal/sandbox/reexport_session.go`, `internal/sandbox/reexport_query.go`
- Modify: `internal/sandbox/msb_client.go`
- Modify: (imports) `cmd/opencode-msb/commands.go`, `cmd/opencode-msb/commands_system.go` if aliasing cleaner — verify, prefer NOT touching cmd

**Interfaces:**
- Consumes: facade symbols `AutoPrune`, `EnsureImage`, `ListImages`, `ListVolumes`, `Prune`, `CmdMigrate`/`CmdReset`/`CmdEdit`, `GetConfigPaths`/`WithMockConfigPaths`/`InstallFailFastConfigPaths`/`WithRealConfigPaths`, `Session` re-exports (`Run`, `Shell`, `BuildImage`, `StopProjectVM`, `KillProjectVM`, `ResolveWorktreeSpec`, `ListSandboxes`, `SetDaemonShellFunc`, `Info`).
- Produces: same names as `func` wrappers delegating to the submodule. `RebuildImage` alias (`EnsureImage`) unchanged in value.

- [ ] **Step 1: Convert static `var` re-exports to `func` wrappers**
For each production-only symbol, replace the `var X = submodule.X` with a `func` wrapper, e.g.:

```go
// Prune re-exports the pruning module's Prune.
func Prune(ctx context.Context, age time.Duration, dryRun, force bool, ui termio.UI) error {
	return pruning.Prune(ctx, age, dryRun, force, ui)
}
```

Apply the same pattern for `AutoPrune` (reexport_prune.go), `EnsureImage` (reexport_image.go), `ListImages` (image), `ListVolumes` (volume), `CmdMigrate`/`CmdReset`/`CmdEdit` (volume), `GetConfigPaths`/`WithMockConfigPaths`/`InstallFailFastConfigPaths`/`WithRealConfigPaths` (config). Preserve exact parameter lists/returns (copy the submodule's signature). Remove the now-unused `//nolint:gochecknoglobals` directives on those vars. Remove the `var (` blocks.
Run: `go build ./...` and `go test ./cmd/...`. Expected: PASS (CLI unchanged).

- [ ] **Step 2: Merge `reexport_query.go` into `reexport_session.go`**
Consolidate the session re-exports (`Info`, `ImageInfo`, `ListImages`, `ListSandboxes`, `ResolveWorktreeSpec`, `SetDaemonShellFunc`, etc.) into `reexport_session.go`; delete `reexport_query.go`. Ensure no symbol is lost (`rg` all `sandbox.Info`/`sandbox.ImageInfo`/`sandbox.ListImages`/`sandbox.ListSandboxes`).
Run: `go build ./...`. Expected: builds.

- [ ] **Step 3: Trim `msb_client.go` to cmd-referenced set**
Identify the exact set cmd uses: `rg -on 'sandbox\.[A-Za-z]+' cmd/ | sort -u`. Keep aliases only for those (`MsbClient`, `Sandbox`, `SandboxHandle`, `VolumeHandle`, `ImageHandle`, `MockSandbox`, `MockSandboxHandle`, `MockVolumeHandle`, `MockImageHandle`, `ShellResult`, `SandboxOpts`, `SetNewMsbClient`). Remove test-only re-exports (`TestFS`, `TestResult`, `NewTestResult`, `WithMsbMock`, `TestResult`) if `cmd` tests can import `msb` directly (confirmed: `main_test.go` already imports `sandbox/msb`). If `cmd` tests do reference `sandbox.WithMsbMock`/`sandbox.NewTestResult`, keep those but note them.
For each removed symbol, verify no reference remains: `rg -rn 'sandbox\.(TestFS|TestResult|NewTestResult|WithMsbMock)\b' --glob '*.go'`.
Run: `go build ./...` and `go test ./cmd/...`. Expected: PASS.

- [ ] **Step 4: Standardize re-export doc-comment phrasing across `reexport_*.go`**
Make each file's doc comment uniform (e.g. `// Re-exported <module> module symbols preserve the public API of the sandbox core so that cmd/opencode-msb continues to compile without changing its import paths.`) and normalize `//nolint:gochecknoglobals` placement on the remaining `var` (value) re-exports like `StateDir`.
Run: `golangci-lint run`. Expected: PASS.

- [ ] **Step 5: Full gate**
Run: `make check`. Expected: PASS.

- [ ] **Step 6: Commit**
```bash
git add -A
git commit -m "refactor: convert facade re-exports to func wrappers and trim surface"
```

---

### Task 10: Final gate + record deferred follow-ups

Consolidates the plan's closure, runs the full suite from a clean state, and records remaining higher-risk follow-ups so they are not lost.

**Files:**
- Create: `docs/superpowers/plans/2026-08-12-sandbox-cohesion-followups.md` (or extend BACKLOG.md)

**Interfaces:**
- Consumes: everything from Tasks 1-9.
- Produces: a prioritized follow-up backlog.

- [ ] **Step 1: Record deferred follow-ups**
Write a short follow-up doc capturing these larger, higher-risk items for a future plan (do NOT implement here):
1. Extract mock code out of production binaries (`msb/testmock.go` ~688 lines, `docker/testmock.go`) into `_test.go`/testutil — removes `testing` from production import graph.
2. `msb.Client` SDK-leak encapsulation — decide thin pass-through vs central SDK re-export to stop consumers importing `msbSdk` directly.
3. Full `state` Root-parameterization so sibling packages build isolated per-test stores (`NewStore(t.TempDir())`) instead of mutating `state.StateDir`.
4. `reprovision` sub-package split (configfiles / envstate / reconfig) — largest single cohesion debt.
5. Root `sandbox` facade deletion by migrating `cmd` to import submodules directly (only after the func-wrapper cleanup in Task 9 reduces surface).
6. `reprovision`/`doctor` param narrowings (`RunOptions`, `ParseMemory` error-return, `checkForActiveVMs` relocation) to reduce behavioral risk-avoidance.
7. `naming.ArtifactFor` vs `ExtractProjectSlugAndDigest` dual-dispatch unification.

- [ ] **Step 2: Full clean build+test+lint**
Run: `make check` from the module root. Also run `GOOS=darwin go build ./...` to confirm darwin portability of the flock/doctor changes.
Expected: ALL PASS.

- [ ] **Step 3: Verify CLI surface diff is zero**
Run: `git diff <base> -- cmd/` and confirm NO production changes under `cmd/` other than any explicit import adjustments performed in earlier tasks. Also run `go vet`-replacement: `golangci-lint run ./cmd/...`.
Expected: cmd behavior/interface unchanged.

- [ ] **Step 4: Final commit**
```bash
git add -A
git commit -m "chore: record sandbox cohesion follow-ups; final gate"
```

---

## Self-review

**Spec coverage:** The compound review's seven tiers map to tasks as follows — Tier 1 (#1 facade deletion deferred to follow-up, but #1.2 seam unification in Task 2 + 9, #1.3 mock extraction in follow-up, #1.4 constant dedup in Task 3); Tier 2 (dead code Task 1, magic-literal Task 7, Digest-sentinel noted); Tier 3 (file splits Tasks 4-6); Tier 4 (domain moves Task 3 + 6, parameterization partial); Tier 5 (DRY Task 8); Tier 6 (interface narrowing Task 7); Tier 7 (cosmetics folded into Tasks 7-9). The three largest/highest-risk review items (mock-out-of-binary, SDK-leak, facade deletion, reprovision split, state Root-param) are explicitly deferred with rationale in Task 10 rather than dropped.

**Placeholder scan:** Every code step either shows the actual replacement shape or references the existing symbol (verified above) being moved verbatim. Where an exact signature isn't reprinted (Task 9 func wrappers), the instruction to "copy the submodule's signature" is a concrete, findable action — the module already defines those signatures; no invented types are referenced. No "TBD"/"add error handling"/"implement later" placeholders remain.

**Type consistency:** `VolumeAction` (Task 7) is defined with `ActionKeep/ActionMigrate/ActionReset/ActionQuit` and used consistently in `session/run.go` as `volume.ActionQuit`; the existing `actionKeep` string values are the source of the key mapping. `ExitError`/`AutoFlag`/`EnvKeyValueParts` moved in Task 3 keep their exact identifiers, and their consumers (`main.go`, `run.go`, `secrets.go`) are updated in the same task. `StaleType*` identifiers are unchanged, only relocated to `pruning`. Helper names introduced in one task (`referenceImage`, `runAttach`, `NewHomeState`, `FingerprintState`, `slugDir`, `StateDir` alias) are used only where defined and are not referenced by earlier/later tasks, so no cross-task drift exists.