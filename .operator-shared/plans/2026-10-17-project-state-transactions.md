# Project State Transactions Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make project/launcher state handling structurally correct by replacing the exported lock-free writers and per-call-site locking with one locked transaction primitive per document.

**Architecture:** Introduce `WithHomeState` / `RecoverHomeState` / `WithRootState` / `RemoveHomeState` as the entire mutation API, holding one host-only flock across read → mutate → write. Move lock files under a never-deleted `<root>/locks/` tree, add a same-process reentrancy fail-fast, express recovery as the only wholesale transaction, and use compare-and-set guards instead of holding the home lock across msb I/O. Remove the old writers once callers are migrated.

**Tech Stack:** Go 1.26, `syscall.Flock`, `gopkg.in/yaml.v3`, `atomicwrite`, `testify`, `golangci-lint`.

**Spec:** `.operator-shared/specs/project-state-transactions.md`

## Global Constraints

- Work on the `improve_upgrade_prompt` / PR #225 branch worktree (`~/.local/share/opencode/worktree/upgrade-state-scoping`).
- No new module dependencies; no on-disk schema changes (only the lock *path* moves).
- Use `make fmt` / `golangci-lint fmt` and `make lint` / `golangci-lint run`; never `go vet`, never manual `go fmt`.
- Every test package isolates host state via `configpaths.WithMockConfigPaths(t)` and the fail-fast mocks (`termio.InitFailFastMocks` where a `TestMain` exists).
- Keep the near-100% coverage bar: exercise every new error branch.
- flock is **not reentrant**; root lock order is root → home.
- `--dry-run` purity is out of scope (issue #228); only document the two existing gates.

## Review Focus

- **Concurrent writers of *different* fields of one `HomeState` both survive.** Two processes/goroutines updating `home_volume` and `agent_version` concurrently must not clobber each other. (Tasks 3, 6, 7.)
- **A corrupt state file is never overwritten by a normal writer, but recovery repairs it.** (Tasks 3, 4.)
- **Removing a key cannot be resurrected by a racing writer, and the lock file survives.** (Task 1.)
- **A double lock in one process fails fast instead of deadlocking.** (Task 2.)
- **A long root-lock holder (copy campaign) makes the upgrade path defer, not block or corrupt.** (Tasks 5, 6.)

---

### Task 1: Lock files live under `<root>/locks/`; locked `RemoveHomeState`

**Files:**
- Modify: `internal/sandbox/state/homelock.go` (`homeLockPath`)
- Modify: `internal/sandbox/state/rootlock.go` (extract `rootLockPath`, use it)
- Modify: `internal/sandbox/state/state.go` (`RemoveState` → `RemoveHomeState`, under the home lock)
- Modify: `internal/sandbox/pruning/volumes.go:107`
- Test: `internal/sandbox/state/homelock_test.go`, `internal/sandbox/state/state_test.go`, `internal/sandbox/pruning/volumes_test.go`, `internal/sandbox/vm/upgrade_migration_test.go`, `internal/sandbox/vm/upgrade_test.go`, `internal/sandbox/runtimemigration/lock_test.go`

**Interfaces:**
- Consumes: `KeyDir(k)`, `homeLockPath(k)`, `withHomeLock(k, fn)` (existing), `stateRoot()`.
- Produces: `func locksDir() string` (unexported, `<root>/locks`); `func rootLockPath() string` (unexported); `func homeLockPath(k Key) string` → `locksDir()/<slug>/<agent>.lock` (or `legacy.lock` when `Agent==""`); `func RemoveHomeState(k Key) error`.

- [ ] **Step 1: Write the failing tests**

```go
// homelock_test.go
func TestHomeLockPathLivesUnderLocksDir(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := testKey()
	want := filepath.Join(configpaths.Get().UserStateDir(), "locks", k.Slug, k.Agent+".lock")
	if got := homeLockPath(k); got != want {
		t.Fatalf("homeLockPath = %q, want %q", got, want)
	}
	if strings.HasPrefix(homeLockPath(k), KeyDir(k)) {
		t.Fatalf("home lock must not live in the removable key dir")
	}
}

// state_test.go
func TestRemoveHomeStateRemovesStateButKeepsLock(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := testKey()
	if err := WriteState(k, HomeState{HomeVolume: "vol"}); err != nil {
		t.Fatal(err)
	}
	if err := RemoveHomeState(k); err != nil {
		t.Fatalf("RemoveHomeState: %v", err)
	}
	if _, err := ReadState(k); !errors.Is(err, ErrStateNotFound) {
		t.Fatalf("state still present: %v", err)
	}
	if _, err := os.Stat(homeLockPath(k)); err != nil {
		t.Fatalf("lock file must survive removal: %v", err)
	}
}

```

`RemoveHomeState` takes the **blocking** home lock (`withHomeLock`), like every other mutation. Its same-process fail-fast assertion lives in Task 2, where the reentrancy guard exists.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/sandbox/state/ -run 'TestHomeLockPathLivesUnderLocksDir|TestRemoveHomeStateRemovesStateButKeepsLock' -v`
Expected: FAIL (`homeLockPath` still `KeyDir`-based; `RemoveHomeState` undefined).

- [ ] **Step 3: Implement**

Add `locksDir()`; change `homeLockPath`; make `rootLockPath` return `filepath.Join(locksDir(), "root.lock")` and use it in `LockRootState`. Rename `RemoveState` to `RemoveHomeState`, wrap the existing removal body in `withHomeLock(k, func() error { ... })` (create the locks dir first so a first-ever removal can lock), and keep the agent-less legacy branch. Update `pruning/volumes.go` and all test fixtures that reference `root.lock` or `home.lock` to the new paths (use `filepath.Join(stateRoot, "locks", ...)`; for the "path is a directory" error fixtures, `os.Mkdir(filepath.Join(stateRoot, "locks", "root.lock"), 0o700)` after `os.MkdirAll(filepath.Dir(...))`).

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/sandbox/state/ ./internal/sandbox/pruning/ ./internal/sandbox/runtimemigration/ ./internal/sandbox/vm/ -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/state internal/sandbox/pruning internal/sandbox/vm internal/sandbox/runtimemigration
git commit -m "refactor(state): move lock files to a never-deleted locks dir and make removal locked"
```

---

### Task 2: Same-process reentrancy fail-fast

**Files:**
- Create: `internal/sandbox/state/lockregistry.go`
- Test: `internal/sandbox/state/lockregistry_test.go`
- Modify: `internal/sandbox/state/homelock.go`, `internal/sandbox/state/rootlock.go` (wire the guard at the flock point)

**Interfaces:**
- Produces: `func holdLockPath(path string) error` (unexported; returns a descriptive error if this process already holds `path`), `func releaseLockPath(path string)` (unexported).
- Consumes in: `LockHomeState`, `LockRootState`.

- [ ] **Step 1: Write the failing test**

```go
func TestLockRegistryFailsFastOnDoubleHold(t *testing.T) {
	if err := holdLockPath("/tmp/x.lock"); err != nil {
		t.Fatalf("first hold: %v", err)
	}
	err := holdLockPath("/tmp/x.lock")
	if err == nil || !strings.Contains(err.Error(), "already held") {
		t.Fatalf("second hold = %v, want an already-held error", err)
	}
	releaseLockPath("/tmp/x.lock")
	if err := holdLockPath("/tmp/x.lock"); err != nil {
		t.Fatalf("re-hold after release: %v", err)
	}
	releaseLockPath("/tmp/x.lock")
}

func TestRemoveHomeStateFailsFastWhileHomeLockHeld(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := testKey()
	if err := WriteState(k, HomeState{HomeVolume: "vol"}); err != nil {
		t.Fatal(err)
	}
	release, _, err := LockHomeState(k, true)
	if err != nil {
		t.Fatalf("hold: %v", err)
	}
	defer release()
	if err := RemoveHomeState(k); err == nil {
		t.Fatal("RemoveHomeState proceeded while the blocking home lock was held")
	}
	if _, rerr := ReadState(k); rerr != nil {
		t.Fatalf("state must survive the refused removal: %v", rerr)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/sandbox/state/ -run TestLockRegistryFailsFastOnDoubleHold -v`
Expected: FAIL (undefined).

- [ ] **Step 3: Implement**

Back `lockregistry.go` with a `sync.Mutex` + `map[string]bool`. **The guard applies to blocking acquires only** — a non-blocking self-acquire must keep returning `ok=false` (existing `TestLock*StateNonBlockingSkipsWhenHeld` rely on it, and it cannot deadlock). In `LockHomeState`/`LockRootState`: when `blocking` is true, after computing `path` and *before* `flock`, call `holdLockPath(path)`; on error return it. On any failure path after holding, `releaseLockPath(path)`; defer `releaseLockPath(path)` alongside the file close in the returned release func. When `blocking` is false, do **not** touch the registry. Add (a) a test that holding `LockHomeState(k, true)` then calling it again in the same process returns the already-held error (not a hang), and that a released lock is reacquirable; and (b) a test that `LockHomeState(k, false)` while the same process holds the blocking lock still returns `ok=false, err=nil`.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/sandbox/state/ -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/state
git commit -m "feat(state): fail fast on a same-process double lock instead of deadlocking"
```

---

### Task 3: `WithHomeState` transaction and `ErrStateUnparsable`

**Files:**
- Create: `internal/sandbox/state/home_txn.go`
- Modify: `internal/sandbox/state/state.go` (add `ErrStateUnparsable`; wrap parse errors in `ReadState`)
- Test: `internal/sandbox/state/home_txn_test.go`

**Interfaces:**
- Produces: `var ErrStateUnparsable = errors.New("state file is unparsable")`; `ReadState` wraps parse failures as `fmt.Errorf("parse state file %s: %w: %w", path, ErrStateUnparsable, err)`; `func WithHomeState(k Key, fn func(cur *HomeState, exists bool) (write bool, err error)) error`.
- Semantics: blocking home lock; missing → `exists=false`, `cur=&HomeState{}`; unreadable/parse → return the error, never call `fn`; else `exists=true`. `fn` returns `(write, err)`; a non-nil `err` returns without writing; `write=false` returns without writing.

- [ ] **Step 1: Write the failing tests**

```go
func TestWithHomeStateCreatesWhenMissing(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := testKey()
	if err := WithHomeState(k, func(hs *HomeState, exists bool) (bool, error) {
		if exists {
			t.Fatal("exists = true, want false")
		}
		hs.HomeVolume = "vol-1"
		return true, nil
	}); err != nil {
		t.Fatalf("WithHomeState: %v", err)
	}
	got, _ := ReadState(k)
	if got == nil || got.HomeVolume != "vol-1" {
		t.Fatalf("state = %+v, want vol-1", got)
	}
}

func TestWithHomeStateCorruptReturnsErrorWithoutWriting(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := testKey()
	if err := os.MkdirAll(KeyDir(k), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stateFile(k), []byte("{ corrupt: [\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	called := false
	err := WithHomeState(k, func(*HomeState, bool) (bool, error) { called = true; return true, nil })
	if !errors.Is(err, ErrStateUnparsable) {
		t.Fatalf("err = %v, want ErrStateUnparsable", err)
	}
	if called {
		t.Fatal("fn called on a corrupt document")
	}
	if _, statErr := os.Stat(stateFile(k)); statErr != nil {
		t.Fatalf("corrupt file was removed: %v", statErr)
	}
}

func TestWithHomeStateNoWriteIsNoop(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := testKey()
	if err := WithHomeState(k, func(*HomeState, bool) (bool, error) { return false, nil }); err != nil {
		t.Fatalf("WithHomeState: %v", err)
	}
	if _, err := ReadState(k); !errors.Is(err, ErrStateNotFound) {
		t.Fatalf("read = %v, want ErrStateNotFound", err)
	}
}

func TestWithHomeStateCallbackErrorDoesNotWrite(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := testKey()
	wantErr := errors.New("boom")
	err := WithHomeState(k, func(*HomeState, bool) (bool, error) { return true, wantErr })
	if !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want %v", err, wantErr)
	}
	if _, rerr := ReadState(k); !errors.Is(rerr, ErrStateNotFound) {
		t.Fatalf("read = %v, want ErrStateNotFound", rerr)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/sandbox/state/ -run TestWithHomeState -v`
Expected: FAIL (undefined).

- [ ] **Step 3: Implement**

`WithHomeState` = `withHomeLock` + read classification + `fn` + conditional `WriteState`. Add the sentinel and parse wrap in `ReadState` (a `TestReadStateCorruptIsUnparsable` asserting `errors.Is(err, ErrStateUnparsable)` and that not-found still yields `ErrStateNotFound`).

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/sandbox/state/ -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/state
git commit -m "feat(state): add WithHomeState transaction and an unparsable-state sentinel"
```

---

### Task 4: `RecoverHomeState` and recovery through it

**Files:**
- Modify: `internal/sandbox/state/home_txn.go` (add `RecoverHomeState`)
- Modify: `internal/sandbox/volume/state.go` (`recoverOrCreateHome`, `EnsureNewHome`)
- Test: `internal/sandbox/state/home_txn_test.go`, `internal/sandbox/volume/volume_test.go`, `internal/sandbox/volume/fingerprint_seed_test.go`

**Interfaces:**
- Produces: `func RecoverHomeState(k Key, fn func(cur *HomeState, exists bool) (write bool, err error)) error` — like `WithHomeState` but a missing **or** `ErrStateUnparsable` document yields `exists=false, cur=&HomeState{}` (repair); any other read error is returned.
- Consumes: `recoveredHomeState(recoveredVolume, info, prior, fingerprints)` (existing pure helper), `state.Fingerprints`.

- [ ] **Step 1: Write the failing tests**

```go
// home_txn_test.go
func TestRecoverHomeStateRepairsCorrupt(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := testKey()
	if err := os.MkdirAll(KeyDir(k), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stateFile(k), []byte("{ corrupt: [\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := RecoverHomeState(k, func(hs *HomeState, exists bool) (bool, error) {
		if exists {
			t.Fatal("exists = true, want false")
		}
		hs.HomeVolume = "recovered"
		return true, nil
	}); err != nil {
		t.Fatalf("RecoverHomeState: %v", err)
	}
	got, err := ReadState(k)
	if err != nil || got.HomeVolume != "recovered" {
		t.Fatalf("state = %+v err=%v, want repaired volume", got, err)
	}
}
```

Keep the existing recovery behavior tests green: a readable prior carries `agent_version`/sources/`offered_versions`/fingerprints forward; a lost/corrupt state seeds fingerprints and leaves the upgrade fields empty (self-healed on boot; `offered_versions` loss is the accepted cost).

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/sandbox/state/ -run TestRecoverHomeState -v`
Expected: FAIL (undefined).

- [ ] **Step 3: Implement**

Add `RecoverHomeState`. Rewrite `recoverOrCreateHome` to write through `RecoverHomeState(k, ...)`, letting the callback read `cur`/`exists` and call `recoveredHomeState(recovered, info, priorOrNil(cur, exists), fingerprints)`; drop the `prior *state.HomeState` parameter from `recoverOrCreateHome` and its caller in `ResolveHomeVolume`. Rewrite `EnsureNewHome`'s state write to `RecoverHomeState` with a callback that sets the fresh fingerprint-seeded state. Keep volume creation/prefill outside the transaction.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/sandbox/volume/ ./internal/sandbox/state/ -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/state internal/sandbox/volume
git commit -m "feat(state): add RecoverHomeState and route home recovery through it"
```

---

### Task 5: `WithRootState` transaction and root-writer migration

**Files:**
- Create: `internal/sandbox/state/root_txn.go`
- Modify: `internal/sandbox/runtimemigration/lock.go`, `copy.go`, `offer.go`
- Modify: `internal/sandbox/vm/upgrade_migration.go`
- Test: `internal/sandbox/state/root_txn_test.go`, `internal/sandbox/runtimemigration/*_test.go`, `internal/sandbox/vm/upgrade_migration_test.go`

**Interfaces:**
- Produces: `type RootTxn` with `func (t *RootTxn) State() *RootState`, `func (t *RootTxn) Exists() bool`, `func (t *RootTxn) Commit() error`; `func WithRootState(blocking bool, fn func(*RootTxn) error) (ok bool, err error)` — blocking root lock for the whole closure; non-blocking returns `ok=false` when contended; a missing file yields a zero state with `Exists()==false`; a corrupt file returns an error.
- Consumes: `LockRootState(blocking)`, `ReadRootState`, `WriteRootState`.

- [ ] **Step 1: Write the failing tests**

```go
func TestWithRootStateNonBlockingDefersWhenHeld(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	held, ok, err := LockRootState(true)
	if err != nil || !ok {
		t.Fatalf("hold: ok=%v err=%v", ok, err)
	}
	defer held()
	ran := false
	ok, err = WithRootState(false, func(*RootTxn) error { ran = true; return nil })
	if err != nil || ok || ran {
		t.Fatalf("ok=%v err=%v ran=%v, want deferred", ok, err, ran)
	}
}

func TestWithRootStateCommitsState(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ok, err := WithRootState(true, func(tx *RootTxn) error {
		tx.State().SetAgentCheck("opencode", AgentCheckState{LatestVersion: "2.0.0"})
		return tx.Commit()
	})
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	got, _ := ReadRootState()
	if got.AgentCheck("opencode").LatestVersion != "2.0.0" {
		t.Fatalf("not committed: %+v", got)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/sandbox/state/ -run TestWithRootState -v`
Expected: FAIL (undefined).

- [ ] **Step 3: Implement**

Add `root_txn.go`. Replace `runtimemigration.acquireMigrationLock`/`withMigrationLock` with `WithRootState(true, ...)`; make `CopyVolumes` run its whole campaign inside the closure and `saveMigration` commit via `tx.Commit()` (still after each volume, preserving exact resume). Convert `finalizeCompletedCampaign`/`dismissMigration` to `WithRootState(true, ...)`. Convert `vm.migrateLegacyUpdaterState` to `WithRootState(false, ...)` (defer when held).

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/sandbox/state/ ./internal/sandbox/runtimemigration/ ./internal/sandbox/vm/ -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/state internal/sandbox/runtimemigration internal/sandbox/vm
git commit -m "feat(state): add WithRootState transaction and migrate root-state writers"
```

---

### Task 6: Migrate the vm home-state writers

**Files:**
- Modify: `internal/sandbox/vm/upgrade.go` (`pendingUpgrade`, `recoverAgentVersion`)
- Modify: `internal/sandbox/vm/run_orchestrate.go` (`persistOfferedVersion`)
- Modify: `internal/sandbox/vm/provenance.go` (`recordImageProvenance`)
- Modify: `internal/sandbox/vm/run_envstate.go` (`persistEnvSecrets`, `persistNetworkState`, `persistMountState`)
- Test: `internal/sandbox/vm/*_test.go`

**Interfaces:**
- Consumes: `state.WithHomeState`, `state.WithRootState`.
- Produces: no new API. Semantics: `pendingUpgrade` takes the root lock non-blocking for the check and a **separate** home transaction for the offered mark (never nested).

- [ ] **Step 1: Write/adjust the failing tests**

Re-point the existing writer tests (`upgrade_test.go`, `provenance_test.go`, `run_envstate_test.go`) at `state.WithHomeState`. Add:

```go
func TestPendingUpgradeOfferedWritePreservesFingerprints(t *testing.T) {
	// seed a state with EnvState set and no offered version; run pendingUpgrade;
	// assert EnvState survives and the offered version is recorded.
}

// Property guard: every vm writer preserves every field it does not own.
// Use the actual struct fields from internal/sandbox/state/state.go; the
// literals below are illustrative.
func TestHomeWritersPreserveUnrelatedFields(t *testing.T) {
	seed := HomeState{
		HomeVolume: "vol", ImageDigest: "d",
		AgentVersion: "1.0.0", AgentSource: "tool", DockerSource: "tool",
		OfferedVersions: []string{"1.1.0"},
		EnvState:        EnvState{Hash: "e"}, SecretState: SecretState{Hash: "s"},
		NetworkState:    NetworkState{Hash: "n"}, MountState: MountState{Hash: "m"},
	}
	writers := map[string]func(k state.Key){
		"persistEnvSecrets": func(k state.Key) {
			_ = persistEnvSecrets(k, EnvState{Hash: "e2"}, SecretState{Hash: "s2"})
		},
		"persistNetworkState": func(k state.Key) { _ = persistNetworkState(k, NetworkState{Hash: "n2"}) },
		"persistMountState":   func(k state.Key) { _ = persistMountState(k, MountState{Hash: "m2"}) },
		"recordImageProvenance": func(k state.Key) {
			// provenance uses msb; assert via a direct RMW equivalent
			_ = state.WithHomeState(k, func(hs *state.HomeState, _ bool) (bool, error) {
				hs.AgentSource, hs.DockerSource = "user", "user"
				return true, nil
			})
		},
	}
	for name, run := range writers {
		t.Run(name, func(t *testing.T) {
			k := seedKey(t, seed)
			run(k)
			got, _ := state.ReadState(k)
			// assert the fields NOT owned by this writer are unchanged, and the
			// owned field advanced.
		})
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/sandbox/vm/ -run 'PendingUpgrade|Provenance|PersistEnv' -v`
Expected: FAIL (test references the new API / old behavior).

- [ ] **Step 3: Implement**

Replace `UpdateExistingHomeState` uses with `WithHomeState(k, func(hs, exists){ if !exists { return false, nil }; ... return true, nil })`; replace `UpdateHomeState` uses with `WithHomeState(... return true, nil)`. Ensure no `WithRootState` closure calls a home transaction (lock-order contract).

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/sandbox/vm/ -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/vm
git commit -m "refactor(vm): write HomeState/RootState through the transaction primitives"
```

---

### Task 7: Migrate the volume writers with compare-and-set guards

**Files:**
- Modify: `internal/sandbox/volume/operations.go` (`volumeOp`)
- Modify: `internal/sandbox/volume/volume.go` (`ApplyHomeAction`)
- Modify: `internal/sandbox/volume/state.go` (`RecordHomeImage`)
- Test: `internal/sandbox/volume/*_test.go`

**Interfaces:**
- Consumes: `state.WithHomeState`.
- Produces: no new API. `volumeOp` and `ApplyHomeAction` guard their final write with `if cur.HomeVolume != expected { return false, fmt.Errorf("home volume changed concurrently (want %q, have %q)", expected, cur.HomeVolume) }` where `expected` is the value read in the snapshot; `RecordHomeImage` becomes `if !exists { return false, nil }`.

- [ ] **Step 1: Write the failing test**

```go
func TestVolumeOpAbortsWhenHomeVolumeChangedConcurrently(t *testing.T) {
	// snapshot oldVolume=vol-1, then have another writer set home_volume=vol-2
	// before the final transaction; assert volumeOp returns a concurrent-change
	// error and state.HomeVolume stays vol-2.
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/sandbox/volume/ -run TestVolumeOpAbortsWhenHomeVolumeChangedConcurrently -v`
Expected: FAIL (no guard today).

- [ ] **Step 3: Implement**

Wrap the three writers in `WithHomeState`; add the CAS guard; keep all msb I/O outside the lock. Surface a CAS failure as an error (warn/return per the operation's existing error contract).

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/sandbox/volume/ -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/volume
git commit -m "refactor(volume): write HomeState through transactions with a CAS guard"
```

---

### Task 8: Retract the old mutation API and migrate tests

**Files:**
- Modify: `internal/sandbox/state/state.go` (unexport `WriteState`; rename `RemoveState` residue)
- Modify: `internal/sandbox/state/root.go` (unexport `WriteRootState`, delete `UpdateRootState`)
- Modify: `internal/sandbox/state/homelock.go`, `rootlock.go` (unexport `LockHomeState`/`LockRootState`; delete `UpdateHomeState`/`UpdateExistingHomeState`/`ReplaceHomeState`)
- Modify: all `*_test.go` that seed with `state.WriteState` / `state.ReadRootState` writes
- Test: whole `internal/` and `cmd/`

**Interfaces:**
- Produces: the final API — exported reads `ReadState`, `ReadRootState`; exported mutations `WithHomeState`, `RecoverHomeState`, `WithRootState`, `RemoveHomeState`. No exported raw writer.

- [ ] **Step 1: Write the failing guard test**

Add `internal/sandbox/state/api_test.go`:

```go
func TestNoExportedRawWriters(t *testing.T) {
	// reflect over the state package's exported funcs; fail if any of
	// WriteState, WriteRootState, UpdateRootState, UpdateHomeState,
	// UpdateExistingHomeState, ReplaceHomeState, RemoveState, LockHomeState,
	// LockRootState is exported.
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/sandbox/state/ -run TestNoExportedRawWriters -v`
Expected: FAIL (old names still exported).

- [ ] **Step 3: Implement**

Unexport/delete the old writers and locks. Migrate every test seeding call: for the `state` package's own tests, seed via `RecoverHomeState`/`WithHomeState` (or raw `os.WriteFile` for corrupt fixtures); for other packages, replace `state.WriteState(k, hs)` with
`state.RecoverHomeState(k, func(cur *state.HomeState, _ bool) (bool, error) { *cur = hs; return true, nil })`
(fold a tiny local helper where a file repeats it). Replace `state.ReadRootState`-then-`WriteRootState` test setup with `state.WithRootState(true, ...)`.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./... -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal cmd
git commit -m "refactor(state): remove the exported raw writers and migrate tests to transactions"
```

---

### Task 9: Docs, changelog, and dry-run gate documentation

**Files:**
- Modify: `internal/sandbox/vm/run_orchestrate.go` (comment the two dry-run gates as the only ones; reference issue #228)
- Modify: `CHANGELOG.md`
- Modify: `.operator-shared/specs/upgrade-state-scoping.md` (note its writer-serialization clause is superseded by `project-state-transactions.md`)
- Modify: `.operator-shared/catalog.md` if descriptions drifted

- [ ] **Step 1: Write the change**

Add a `CHANGELOG.md` `[Unreleased]` line: internal state-handling hardening (single locked transaction per document, no exported raw writers, locked removal, reentrancy fail-fast); no user-facing behavior change. Document `determineAgentVersion` and `prepareHomeVolume` as the only `--dry-run` gates, pointing at #228.

- [ ] **Step 2: Verify docs**

Run: `make check`
Expected: PASS (fmt, lint, test, docs linkcheck).

- [ ] **Step 3: Commit**

```bash
git add CHANGELOG.md internal/sandbox/vm .operator-shared
git commit -m "docs(state): record the transaction contract and the dry-run gate scope"
```

---

### Task 10: Whole-branch verification

- [ ] **Step 1: Run the full check**

Run: `make check && make compile-all`
Expected: PASS.

- [ ] **Step 2: Confirm no stale references**

Run: `rg -n 'UpdateHomeState|UpdateExistingHomeState|ReplaceHomeState|WriteRootState|UpdateRootState|WriteState|RemoveState|LockHomeState|LockRootState' internal cmd --glob '!*_test.go'`
Expected: no matches outside `internal/sandbox/state`.

- [ ] **Step 3: Update the plan status and hand off**

Mark tasks complete; report the branch and the diff summary to the user for PR finalization.
