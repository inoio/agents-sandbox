# Upgrade State Scoping Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Split agent upgrade/provenance state by application so the upgrade prompt reads the launched project's version (machine-global network cadence/cache vs per-`(slug, agent)` image facts), and retire the global `updater.yaml` via a silent fold of `last_checked`.

**Architecture:** `state.RootState` (machine-global `<root>/state.yaml`) gains a per-agent `AgentCheckState` (network cadence + cached latest version) written under one shared host-only flock. `state.HomeState` (per `(slug, agent)`) gains the image-describing fields (`agent_version`, `agent_source`, `docker_source`, `offered_versions`). A one-time, legacy-file-triggered, non-interactive fold copies `last_checked` into the root state and deletes `updater.yaml`.

**Tech Stack:** Go 1.26, `gopkg.in/yaml.v3`, `syscall.Flock`, `internal/sandbox/{state,volume,vm}`, `internal/sandbox/runtimemigration`.

**Spec:** `.operator-shared/specs/upgrade-state-scoping.md`

## Global Constraints

- Idiomatic Go; self-documenting, non-abbreviated identifiers. No inline comments unless the "why" is non-obvious.
- Follow `AGENTS.md`: run `make lint`/`golangci-lint` after edits, `make fmt`, `make test`, and `make check` before finalizing. Do not use `go vet`.
- This branch is based on `main` and deliberately re-delivers PR #225's prompt-wording improvement together with the correct version source; PR #225 may be closed in its favour.
- flock is **not** reentrant: never acquire the root lock while already holding it.
- `--dry-run` (`opts.DryRun`, implies `DryRunVM`) must be side-effect-free: no network check, no state persistence, no `updater.yaml` deletion.
- The runner image is per `(slug, agent)` (`naming.RunnerImageRef`); the prompt must never name another project's version.
- Existing HomeState preservation invariant (fingerprints) is extended to the new upgrade fields.

## Review Focus

- A long `runtimemigration` campaign holding the root lock → the upgrade check must defer silently (skip, no error), not block or clobber. (Task 6, Task 7)
- Concurrent `pendingUpgrade` and `runtimemigration` writes to the same `state.yaml` → neither map may be lost. (Task 2, Task 6, Task 7)
- `--dry-run` with a recorded/`--agent-version` — must not touch the network, persist, or delete `updater.yaml`. (Task 6, Task 7)
- A volume op or lost-state recovery silently resetting the version baseline → must carry the upgrade fields forward. (Task 4)
- A missing/empty/corrupt/legacy-flat `updater.yaml` → tolerate and still delete; a present `last_checked` in root state wins. (Task 7)

---

### Task 1: Global per-agent network-check state in `RootState`

**Files:**
- Modify: `internal/sandbox/state/root.go`
- Test: `internal/sandbox/state/root_test.go`

**Interfaces:**
- Consumes: existing `RootState`, `ReadRootState`, `WriteRootState`, `UpdateRootState`, `stateRoot()`.
- Produces:
  - `type AgentCheckState struct { LastChecked time.Time; LatestVersion string }` (yaml `last_checked`, `latest_version`).
  - `func (c AgentCheckState) DueForCheck(t time.Time) bool`
  - `RootState.Agents map[string]AgentCheckState` (yaml `agents,omitempty`)
  - `func (s *RootState) AgentCheck(name string) AgentCheckState`
  - `func (s *RootState) SetAgentCheck(name string, c AgentCheckState)`
  - `const AgentCheckInterval = 24 * time.Hour`

- [ ] **Step 1: Write failing tests** in `internal/sandbox/state/root_test.go`.

```go
func TestRootStateAgentCheckRoundTrip(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	if err := WriteRootState(RootState{Agents: map[string]AgentCheckState{
		"opencode": {LastChecked: time.Unix(100, 0).UTC(), LatestVersion: "2.0.0"},
	}}); err != nil {
		t.Fatalf("WriteRootState: %v", err)
	}
	got, err := ReadRootState()
	if err != nil {
		t.Fatalf("ReadRootState: %v", err)
	}
	c := got.AgentCheck("opencode")
	if c.LatestVersion != "2.0.0" || c.LastChecked.Unix() != 100 {
		t.Fatalf("AgentCheck = %+v", c)
	}
}

func TestRootStateAgentCheckMissingIsZero(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	got, _ := ReadRootState()
	if c := got.AgentCheck("opencode"); !c.LastChecked.IsZero() || c.LatestVersion != "" {
		t.Fatalf("missing AgentCheck = %+v, want zero", c)
	}
}

func TestAgentCheckDueForCheck(t *testing.T) {
	now := time.Unix(1000, 0)
	if !(AgentCheckState{}).DueForCheck(now) {
		t.Error("zero LastChecked must be due")
	}
	if (AgentCheckState{LastChecked: now.Add(-AgentCheckInterval + time.Minute)}).DueForCheck(now) {
		t.Error("recent check must not be due")
	}
	if !(AgentCheckState{LastChecked: now.Add(-AgentCheckInterval)}).DueForCheck(now) {
		t.Error("check at the interval boundary must be due")
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/sandbox/state/ -run 'TestRootStateAgentCheck|TestAgentCheckDueForCheck' -v`
Expected: FAIL — `AgentCheckState`/`AgentCheck` undefined.

- [ ] **Step 3: Implement** in `root.go`: add `AgentCheckInterval = 24 * time.Hour`; the `AgentCheckState` struct with `DueForCheck` (`c.LastChecked.IsZero() || t.Sub(c.LastChecked) >= AgentCheckInterval`); the `Agents` field on `RootState`; and `AgentCheck`/`SetAgentCheck` (the setter initializes `Agents` when nil, mirroring `SetMigration`).

- [ ] **Step 4: Run to verify pass**

Run: `go test ./internal/sandbox/state/ -run 'TestRootStateAgentCheck|TestAgentCheckDueForCheck' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/state/root.go internal/sandbox/state/root_test.go
git commit -m "feat(state): add per-agent check state to root state"
```

---

### Task 2: Shared host-only `RootState` lock

**Files:**
- Create: `internal/sandbox/state/rootlock.go`
- Create: `internal/sandbox/state/rootlock_test.go`
- Modify: `internal/sandbox/runtimemigration/lock.go`
- Modify: `internal/sandbox/runtimemigration/lock_test.go`

**Interfaces:**
- Consumes: `stateRoot()`, `FlockExclusive`, `FlockExclusiveNB`.
- Produces: `func LockRootState(blocking bool) (release func(), ok bool, err error)`. `ok` is false only for a held lock with `blocking == false` (returns nil release). Holding process pid-locks release on process exit.

- [ ] **Step 1: Write failing tests** in `rootlock_test.go`.

```go
func TestLockRootStateNonBlockingSkipsWhenHeld(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	release, ok, err := LockRootState(true)
	if err != nil || !ok {
		t.Fatalf("first lock: ok=%v err=%v", ok, err)
	}
	defer release()
	second, ok2, err2 := LockRootState(false)
	if err2 != nil {
		t.Fatalf("second lock error: %v", err2)
	}
	if ok2 || second != nil {
		t.Fatalf("second non-blocking lock: ok=%v release=%v, want ok=false", ok2, second)
	}
}

func TestLockRootStateReleasesForReacquire(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	release, ok, err := LockRootState(false)
	if err != nil || !ok {
		t.Fatalf("acquire: ok=%v err=%v", ok, err)
	}
	release()
	again, ok, err := LockRootState(false)
	if err != nil || !ok {
		t.Fatalf("reacquire: ok=%v err=%v", ok, err)
	}
	again()
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/sandbox/state/ -run TestLockRootState -v`
Expected: FAIL — `LockRootState` undefined.

- [ ] **Step 3: Implement** `rootlock.go`: `MkdirAll(stateRoot(), 0o700)`; `os.OpenFile(filepath.Join(stateRoot(), "root.lock"), O_CREATE|O_RDWR, 0o600)`; blocking → `FlockExclusive`, non-blocking → `FlockExclusiveNB`, mapping `isLockContention` to `ok=false` with a nil release. The release closes the file.

- [ ] **Step 4: Run to verify pass**

Run: `go test ./internal/sandbox/state/ -run TestLockRootState -v`
Expected: PASS.

- [ ] **Step 5: Make `runtimemigration` adopt the shared lock.** Replace the body of `acquireMigrationLock()` in `internal/sandbox/runtimemigration/lock.go` so it calls `state.LockRootState(true)` and returns the release; drop the private `migration.lock` path and the `runtime`/`os` imports it no longer needs. Update `lock_test.go`: keep the happy-path acquire test; move/remove the error-injection cases that relied on the deleted `flockExclusive` seam (they are now covered by `rootlock_test.go`).

- [ ] **Step 6: Run the packages**

Run: `go test ./internal/sandbox/state/ ./internal/sandbox/runtimemigration/ -v`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/sandbox/state/rootlock.go internal/sandbox/state/rootlock_test.go internal/sandbox/runtimemigration/lock.go internal/sandbox/runtimemigration/lock_test.go
git commit -m "feat(state): serialize root-state writes under one host flock"
```

---

### Task 3: `HomeState` upgrade fields and offered-set helpers

**Files:**
- Modify: `internal/sandbox/state/state.go`
- Test: `internal/sandbox/state/state_test.go`

**Interfaces:**
- Consumes: `HomeState`, `ReadState`, `WriteState`.
- Produces: `HomeState` fields `AgentVersion string` (`agent_version,omitempty`), `AgentSource string` (`agent_source,omitempty`), `DockerSource string` (`docker_source,omitempty`), `OfferedVersions []string` (`offered_versions,omitempty`); `func (s *HomeState) Offered(v string) bool`; `func (s *HomeState) MarkOffered(v string)`.

- [ ] **Step 1: Write failing tests** in `state_test.go`.

```go
func TestHomeStateUpgradeFieldsRoundTrip(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := Key{Slug: "proj", Agent: "opencode"}
	if err := WriteState(k, HomeState{
		HomeVolume: "vol", ImageDigest: "d1",
		AgentVersion: "1.2.3", AgentSource: "tool", DockerSource: "user",
		OfferedVersions: []string{"2.0.0"},
	}); err != nil {
		t.Fatalf("WriteState: %v", err)
	}
	got, err := ReadState(k)
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if got.AgentVersion != "1.2.3" || got.AgentSource != "tool" || got.DockerSource != "user" {
		t.Fatalf("upgrade fields = %+v", got)
	}
	if !got.Offered("2.0.0") || got.Offered("3.0.0") {
		t.Fatalf("OfferedVersions = %v", got.OfferedVersions)
	}
}

func TestHomeStateMarkOffered(t *testing.T) {
	var hs HomeState
	hs.MarkOffered("2.0.0")
	hs.MarkOffered("2.0.0")
	if !hs.Offered("2.0.0") {
		t.Error("expected 2.0.0 offered")
	}
	if len(hs.OfferedVersions) != 1 {
		t.Errorf("MarkOffered must not duplicate, got %v", hs.OfferedVersions)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/sandbox/state/ -run 'TestHomeStateUpgradeFieldsRoundTrip|TestHomeStateMarkOffered' -v`
Expected: FAIL — undefined fields/methods.

- [ ] **Step 3: Implement** the fields and helpers in `state.go`. `Offered` uses `slices.Contains`; `MarkOffered` appends only when `!Offered(v)`.

- [ ] **Step 4: Run to verify pass**

Run: `go test ./internal/sandbox/state/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/state/state.go internal/sandbox/state/state_test.go
git commit -m "feat(state): add upgrade fields to HomeState"
```

---

### Task 4: Preserve upgrade fields through volume ops and recovery

**Files:**
- Modify: `internal/sandbox/volume/operations.go` (`volumeOp`, ~line 249)
- Modify: `internal/sandbox/volume/volume.go` (`ApplyHomeAction`, ~line 152)
- Modify: `internal/sandbox/volume/state.go` (`recoveredHomeState`, ~line 108)
- Test: `internal/sandbox/volume/*_test.go` (add assertions where the fingerprint-preservation is already tested)

**Interfaces:**
- Consumes: `state.HomeState.AgentVersion/AgentSource/DockerSource/OfferedVersions`.
- Produces: nothing new; asserts the invariant.

- [ ] **Step 1: Add failing assertions.** In the existing tests that assert fingerprint preservation for `volumeOp`/`ApplyHomeAction`/`recoveredHomeState`, seed the prior state with upgrade fields and assert they survive. Example for `ApplyHomeAction`:

```go
prior := state.HomeState{AgentVersion: "1.2.3", AgentSource: "tool", OfferedVersions: []string{"2.0.0"}}
// ... set up prior state, run the action ...
after, _ := state.ReadState(k)
if after.AgentVersion != "1.2.3" || after.AgentSource != "tool" || len(after.OfferedVersions) != 1 {
	t.Fatalf("upgrade fields not preserved: %+v", after)
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/sandbox/volume/ -v`
Expected: FAIL — upgrade fields dropped.

- [ ] **Step 3: Implement.** In each of the three writers, after copying the fingerprints, also copy `AgentVersion`, `AgentSource`, `DockerSource`, `OfferedVersions` from `st`/`prior`. In `recoveredHomeState`, do this only in the `prior != nil` branch.

- [ ] **Step 4: Run to verify pass**

Run: `go test ./internal/sandbox/volume/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/volume/
git commit -m "fix(volume): preserve upgrade state across home-volume rewrites"
```

---

### Task 5: Provenance writes the project's `HomeState`

**Files:**
- Modify: `internal/sandbox/vm/provenance.go`
- Modify: `internal/sandbox/vm/reconfig.go` (`setUpSandbox` signature and its `recordImageProvenance` call)
- Modify: `internal/sandbox/vm/run_orchestrate.go` (the `setUpSandbox` call site, passing `k`)
- Test: `internal/sandbox/vm/provenance_test.go` (and update the `setUpSandbox` callers in `vm/*_test.go`)

**Interfaces:**
- Consumes: `state.Key`, `state.HomeState`, `agentSourceTool/agentSourceUser`, `agent.AsVersionProvider`.
- Produces: `func recordImageProvenance(ctx context.Context, k state.Key, a agent.Agent, sb msb.Sandbox, ui termio.UI)`; `func setUpSandbox(ctx context.Context, k state.Key, sb msb.Sandbox, opts options.RunOptions, cfs *reprovision.ConfigFiles, ui termio.UI, restart bool, boot vmBoot, guestCWD string) (string, error)`.

- [ ] **Step 1: Rewrite the failing tests** in `provenance_test.go` to call the new signature with a `state.Key`, then read `state.ReadState(k)`.

```go
k := state.Key{Slug: "proj", Agent: "opencode"}
recordImageProvenance(context.Background(), k, opencodeAgent(t), sb, &termio.Mock{})
hs, err := state.ReadState(k)
if err != nil { t.Fatalf("ReadState: %v", err) }
if hs.AgentSource != agentSourceTool || hs.DockerSource != agentSourceUser || hs.AgentVersion != "0.5.0" {
	t.Fatalf("provenance = %+v", hs)
}
```
Keep the user-agent and file-absent cases, asserting `AgentVersion == ""`.

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/sandbox/vm/ -run TestRecordImageProvenance -v`
Expected: FAIL — signature mismatch / old assertion.

- [ ] **Step 3: Implement.** `recordImageProvenance` reads `state.ReadState(k)` (treat `ErrStateNotFound` as a fresh `HomeState`), sets `AgentSource`/`DockerSource`, and for a tool agent writes the detected version to `AgentVersion`, then `state.WriteState`. Add `k state.Key` as the first parameter of `setUpSandbox` and pass it through; update the `run_orchestrate.go` call site (`k` is already in scope).

- [ ] **Step 4: Update the remaining `setUpSandbox` call sites** in `internal/sandbox/vm/*_test.go` (7 calls: `agent_threading_test.go`, `create_project_vm_test.go`, `setupsandbox_test.go`) to pass a `state.Key`.

- [ ] **Step 5: Run to verify pass**

Run: `go test ./internal/sandbox/vm/ -run 'TestRecordImageProvenance|TestSetUpSandbox' -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/sandbox/vm/
git commit -m "feat(vm): record image provenance into the project HomeState"
```

---

### Task 6: Upgrade flow reads `RootState` + `HomeState`

**Files:**
- Modify: `internal/sandbox/vm/upgrade.go`
- Modify: `internal/sandbox/vm/upgrade_state.go` (remove the legacy `upgradeState` model; keep `now`)
- Modify: `internal/sandbox/vm/run_orchestrate.go` (thread `k`, add the dry-run branch)
- Modify: `internal/sandbox/vm/upgrade_test.go`, `upgrade_more_test.go`, `upgrade_state_test.go`, `upgrade_state_extra_test.go`

**Interfaces:**
- Consumes: `state.RootState.AgentCheck`/`SetAgentCheck`, `state.AgentCheckState`, `state.LockRootState`, `state.HomeState.Offered`/`MarkOffered`/`AgentVersion`/`AgentSource`.
- Produces:
  - `func resolveBuildVersion(ctx context.Context, k state.Key, a agent.Agent, ui termio.UI, opts options.RunOptions) (string, bool, error)`
  - `func pendingUpgrade(ctx context.Context, a agent.Agent, k state.Key, ui termio.UI, currentVersion string) (string, bool)`
  - `func currentAgentVersion(k state.Key) string`; `func currentAgentSource(k state.Key) string` (read `HomeState`).

- [ ] **Step 1: Rewrite tests.** In the `resolveBuildVersion` tests, replace `saveUpgradeState(upgradeState{...})` setup with `state.WriteState(k, state.HomeState{...})` and pass `k` as the new argument; where the test drives the network path, seed the cached latest via `state.WriteRootState(state.RootState{Agents: map[string]state.AgentCheckState{...}})`. Add a cross-project regression test:

```go
func TestResolveBuildVersionPromptNamesThisProjectsVersion(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	// latest known version cached globally; this project's image baseline is 1.0.0
	state.WriteRootState(state.RootState{Agents: map[string]state.AgentCheckState{
		"opencode": {LastChecked: now().Add(-time.Hour), LatestVersion: "2.0.0"},
	}})
	k := state.Key{Slug: "proj", Agent: "opencode"}
	state.WriteState(k, state.HomeState{AgentVersion: "1.0.0", AgentSource: agentSourceTool})
	agentLatestVersion = func(context.Context, agent.Agent) (string, error) { return "2.0.0", nil }
	var prompt string
	ui := &termio.Mock{IsInteractiveResult: true, SelectFn: func(p string, _ []termio.Choice, _ string) (string, error) { prompt = p; return "k", nil }}
	if _, _, err := resolveBuildVersion(context.Background(), k, opencodeAgent(t), ui, options.RunOptions{}); err != nil {
		t.Fatalf("resolveBuildVersion: %v", err)
	}
	if !strings.Contains(prompt, "1.0.0") || !strings.Contains(prompt, "2.0.0") {
		t.Errorf("prompt %q must name both 1.0.0 and 2.0.0", prompt)
	}
}
```
Add a lock-held test: acquire `state.LockRootState(true)`, call `pendingUpgrade`, assert no offer and no prompt. Add a HomeState-`Offered` gating test.

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/sandbox/vm/ -run 'TestResolve|TestPendingUpgrade' -v`
Expected: FAIL — signature/model mismatch.

- [ ] **Step 3: Implement `pendingUpgrade`.** Acquire `state.LockRootState(false)`; if `err != nil || !ok`, return `("", false)`. Read `state.ReadRootState()` (warn + zero on error). If `entry.DueForCheck(now())`: call `agentLatestVersion`; on error warn and return no offer; on success set `LastChecked = now()` and `LatestVersion = latest`, `SetAgentCheck`, `WriteRootState`. Then `latest := entry.LatestVersion`; return no offer if empty, if `!checker.NewerThan(latest, currentVersion)`, or if `HomeState(k).Offered(latest)`. Otherwise `MarkOffered` and `WriteState`, return the offer.

- [ ] **Step 4: Implement the accessors and `resolveBuildVersion`.** `currentAgentVersion`/`currentAgentSource` read `state.ReadState(k)` (return "" on any error). `resolveBuildVersion` takes `k` and uses those instead of the deleted `currentUpgradeVersion`/`currentAgentSource`. Update the other `currentAgentSource(a)` call site — `buildOptions(...)` in `run_orchestrate.go` (~line 126) — to `currentAgentSource(k)`.

- [ ] **Step 5: Remove the legacy model** from `upgrade_state.go`: delete `upgradeState`, `agentUpgradeState`, `upgradeStateFile`, `upgradeStatePath`, `loadUpgradeState`, `saveUpgradeState`, `migrateLegacy`, `currentUpgradeVersion`, `currentAgentSource`, `upgradeCheckInterval`, `DueForCheck`, `offered`, `markOffered`; keep `now = time.Now`. Delete `upgrade_state_test.go`/`upgrade_state_extra_test.go` or fold their still-relevant cases into `upgrade_test.go`.

- [ ] **Step 6: Wire `run_orchestrate.go`.** Thread `k` into the `resolveBuildVersion` call. Under `opts.DryRun`, skip the call, emit `ui.Verbosef("dry-run: would resolve build version")`, and set `agentVersion = opts.AgentVersion`, falling back to `currentAgentVersion(k)` when empty; keep `shallUpgrade = false`.

- [ ] **Step 7: Run to verify pass**

Run: `go test ./internal/sandbox/vm/ -v`
Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/sandbox/vm/
git commit -m "feat(vm): resolve upgrade version from per-project state"
```

---

### Task 7: Legacy `updater.yaml` fold + delete

**Files:**
- Create: `internal/sandbox/vm/upgrade_migration.go`
- Create: `internal/sandbox/vm/upgrade_migration_test.go`
- Modify: `internal/sandbox/vm/run_orchestrate.go` (call the migration before `resolveBuildVersion`, gated on `!opts.DryRun`)

**Interfaces:**
- Consumes: `state.LockRootState`, `state.ReadRootState`, `state.WriteRootState`, `state.AgentCheckState`, `configpaths.UserStateDir()`.
- Produces: `func migrateLegacyUpdaterState(ui termio.UI)`; `func legacyUpdaterStatePath() string`.

- [ ] **Step 1: Write failing tests** in `upgrade_migration_test.go`.

```go
func TestMigrateLegacyUpdaterStateFoldsLastCheckedAndDeletes(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	if err := os.WriteFile(legacyUpdaterStatePath(), []byte("agents:\n  opencode:\n    last_checked: 2020-01-01T00:00:00Z\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	migrateLegacyUpdaterState(&termio.Mock{})
	if _, err := os.Stat(legacyUpdaterStatePath()); !os.IsNotExist(err) {
		t.Fatal("updater.yaml must be deleted")
	}
	root, _ := state.ReadRootState()
	if root.AgentCheck("opencode").LastChecked.IsZero() {
		t.Fatal("last_checked not folded")
	}
}

func TestMigrateLegacyUpdaterStatePresentValueWins(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	state.WriteRootState(state.RootState{Agents: map[string]state.AgentCheckState{
		"opencode": {LastChecked: time.Unix(5000, 0).UTC()},
	}})
	os.WriteFile(legacyUpdaterStatePath(), []byte("agents:\n  opencode:\n    last_checked: 2020-01-01T00:00:00Z\n"), 0o600)
	migrateLegacyUpdaterState(&termio.Mock{})
	root, _ := state.ReadRootState()
	if root.AgentCheck("opencode").LastChecked.Unix() != 5000 {
		t.Fatal("present last_checked must win over the legacy value")
	}
}

func TestMigrateLegacyUpdaterStateCorruptIsDeleted(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	os.WriteFile(legacyUpdaterStatePath(), []byte("not: valid: yaml: ["), 0o600)
	migrateLegacyUpdaterState(&termio.Mock{})
	if _, err := os.Stat(legacyUpdaterStatePath()); !os.IsNotExist(err) {
		t.Fatal("corrupt updater.yaml must be deleted")
	}
}

func TestMigrateLegacyUpdaterStateSkippedWhenLockHeld(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	os.WriteFile(legacyUpdaterStatePath(), []byte("agents:\n  opencode:\n    last_checked: 2020-01-01T00:00:00Z\n"), 0o600)
	release, ok, err := state.LockRootState(true)
	if err != nil || !ok { t.Fatalf("lock: ok=%v err=%v", ok, err) }
	defer release()
	migrateLegacyUpdaterState(&termio.Mock{})
	if _, err := os.Stat(legacyUpdaterStatePath()); err != nil {
		t.Fatal("updater.yaml must survive when the lock is held")
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/sandbox/vm/ -run TestMigrateLegacyUpdaterState -v`
Expected: FAIL — undefined.

- [ ] **Step 3: Implement** `upgrade_migration.go`. Define a private legacy shape (`legacyUpdaterState` with `Agents map[string]legacyAgent` and the legacy flat fields, plus the flat→`opencode` fold). Read the file; `os.IsNotExist` → return. Acquire `state.LockRootState(false)`; on error warn+return, on `!ok` return. Read root state; for each legacy agent with a non-zero `LastChecked`, fold only when `root.AgentCheck(name).LastChecked.IsZero()`. Write the root state; on success `os.Remove(path)` (best-effort). Missing/empty/corrupt folds nothing but still deletes.

- [ ] **Step 4: Wire the call.** In `PrepareSandbox`, before the `resolveBuildVersion` step, call `migrateLegacyUpdaterState(ui)` when `!opts.DryRun`.

- [ ] **Step 5: Run to verify pass**

Run: `go test ./internal/sandbox/vm/ -run 'TestMigrateLegacyUpdaterState|TestPrepareSandbox' -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/sandbox/vm/upgrade_migration.go internal/sandbox/vm/upgrade_migration_test.go internal/sandbox/vm/run_orchestrate.go
git commit -m "feat(vm): fold legacy updater.yaml into root state and delete it"
```

---

### Task 8: Prompt wording, docs, changelog

**Files:**
- Modify: `internal/sandbox/vm/upgrade.go` (`promptUpgrade`)
- Modify: `internal/sandbox/vm/upgrade_test.go`
- Modify: `docs/runner-image.md`
- Modify: `CHANGELOG.md`

**Interfaces:**
- Consumes: nothing new.
- Produces: nothing new.

- [ ] **Step 1: Write the failing prompt test** (delivered by PR #225, adapted to the state source).

```go
func TestPromptUpgradeNamesBothVersions(t *testing.T) {
	var prompt string
	var choices []termio.Choice
	ui := &termio.Mock{IsInteractiveResult: true, SelectFn: func(p string, c []termio.Choice, _ string) (string, error) { prompt, choices = p, c; return "k", nil }}
	if _, err := promptUpgrade(ui, opencodeAgent(t), "1.0.0", "2.0.0"); err != nil {
		t.Fatalf("promptUpgrade: %v", err)
	}
	for _, want := range []string{"1.0.0", "2.0.0"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt %q must name %s", prompt, want)
		}
	}
	rebuild := slices.IndexFunc(choices, func(c termio.Choice) bool { return c.Key == "r" })
	if rebuild < 0 || !strings.Contains(choices[rebuild].Description, "1.0.0") || !strings.Contains(choices[rebuild].Description, "2.0.0") {
		t.Errorf("Rebuild choice must name both versions: %+v", choices)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/sandbox/vm/ -run TestPromptUpgradeNamesBothVersions -v`
Expected: FAIL — the current prompt names only `latest`.

- [ ] **Step 3: Implement the PR #225 wording** in `promptUpgrade`: prompt `"Rebuild the runner image with %s %s? (current image has %s)"` (agent, latest, current); Rebuild description `"Upgrade <agent> <current> → <latest>"`; Keep description `"Keep <agent> <current>, don't remind again"`.

- [ ] **Step 4: Run to verify pass**

Run: `go test ./internal/sandbox/vm/ -run TestPromptUpgradeNamesBothVersions -v`
Expected: PASS.

- [ ] **Step 5: Update docs and changelog.** In `docs/runner-image.md`, state that the prompt names both the available release and the version currently baked into this project's image. Add a `CHANGELOG.md` `[Unreleased]` entry describing the prompt change and the state-scoping/`updater.yaml` retirement.

- [ ] **Step 6: Commit**

```bash
git add internal/sandbox/vm/upgrade.go internal/sandbox/vm/upgrade_test.go docs/runner-image.md CHANGELOG.md
git commit -m "docs(upgrade): prompt names current and available versions"
```

---

### Task 9: Whole-branch verification

**Files:** none (verification only).

- [ ] **Step 1: Format and lint**

Run: `make fmt && make lint`
Expected: no changes, no diagnostics.

- [ ] **Step 2: Full test + compile**

Run: `make test && make compile-all`
Expected: PASS.

- [ ] **Step 3: Full check**

Run: `make check`
Expected: PASS (fmt, lint, test, docs linkcheck).

- [ ] **Step 4: Confirm no dangling legacy references**

Run: `rg -n "upgradeState|updater.yaml|loadUpgradeState|saveUpgradeState|currentUpgradeVersion|migration.lock" internal/`
Expected: only `upgrade_migration.go`/`upgrade_migration_test.go` reference `updater.yaml` (migration) and no code references the deleted `upgradeState` model or `migration.lock`.

- [ ] **Step 5: Finalize the branch** via superpowers:finishing-a-development-branch (push and open the PR; mention that PR #225 is superseded).
