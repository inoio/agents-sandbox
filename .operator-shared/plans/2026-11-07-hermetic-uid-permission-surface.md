# Make the strict hermetic envelope green: the uid/permission surface — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Remove the effective-uid dependence from the six tests the strict envelope runs as uid 0, so `make test-hermetic` is green, then harden the strict run against a toolchain download.

**Architecture:** Each failing test forces a read/write error with `chmod 000` or a read-only directory, which uid 0 (`unshare --net --map-root-user`) bypasses, so the expected error never occurs. Replace the permission trick with the filesystem seams the packages already tend toward: `configmerge` gains a `readFile` var beside its `readDir` var; `runtimemigration` gains a `readDir` var (its `copy.go` seam block is the precedent); `internal/sandbox/state` gains one write var with an exported override hook, since the failing writers live in another package (`vm`). Tests stub the seam to return the same error the chmod produced. The envelope then pins `GOTOOLCHAIN=local` so the strict run cannot try to download a toolchain inside the namespace.

**Tech Stack:** Go 1.27, the repo's package-level test seams, `scripts/test-envelope.sh`.

**Spec:** `.operator-shared/plans/2026-11-07-hermetic-test-suite-design.md`; the surface is recorded in `.operator-shared/plans/2026-11-07-hermetic-inventory.md`. Follows `specs/testing/test-isolation.md` (seams restored, tests off host state). This is Plan 2 of the design; the identity/CWD, host-figure sentinel, wall-clock, and cadence surfaces remain separate plans.

## Global Constraints

- No test may depend on the effective uid: a green `make test-hermetic` (which runs as uid 0) is the acceptance.
- Seam overrides are restored in `t.Cleanup`, so the suite stays idempotent under `-count>1` (`specs/testing/test-isolation.md`).
- The stub must return the same error class the permission trick produced — `fs.ErrPermission` and, for the claim-commit test, an `*os.PathError` — so the production error contract is unchanged.
- Production changes are indirection only (a call through a var); no behavior changes.
- Format/lint with `make fmt`/`make lint` (golangci-lint), never `gofmt`/`go vet`.
- If a source file's test-only surface is pinned by an API-contract test, update that test in the same task.

## Review Focus

1. **uid-independence** — each rewritten test passes under `make test-hermetic` (uid 0) and under `make test` (uid 1000). Task 5.
2. **Seams restored** — running a fixed package twice (`go test -count=2 ./<pkg>/`) is green; no leak into a sibling. Tasks 1-3.
3. **Error branch still exercised** — the stub returns `fs.ErrPermission`/`*os.PathError` for the exact target, not a generic error, so the production branch under test is the one the chmod reached. Tasks 1-3.
4. **No production behavior change** — the only diff in production files is `os.X(...)` → `xVar(...)`. Tasks 1-3.
5. **`GOTOOLCHAIN=local` reaches the child** — the `--print` output shows it and the allowlist test asserts it. Task 4.

---

### Task 1: `configmerge` `readFile` seam

**Files:**
- Modify: `internal/configmerge/merge.go`
- Test: `internal/configmerge/merge_test.go`

**Interfaces:**
- Produces: `var readFile = os.ReadFile` in `merge.go` (package-level, beside `var readDir = os.ReadDir` at merge.go:89); `scanSnippets` calls `readFile(snippetPath)` at merge.go:121.

- [ ] **Step 1: Rewrite the test to deny reads through seams**

Replace `TestScanMirrorSkipsUnreadableEntries` (merge_test.go:662-687). Keep the `locked/secret.json` (unreadable directory) and `noread.json` (unreadable file) intents, but induce the failures with the seams instead of `chmod`:

```go
func TestScanMirrorSkipsUnreadableEntries(t *testing.T) {
	user := t.TempDir()
	dest := t.TempDir()
	testutil.WriteFile(t, user, "ok.json", `{"ok": 1}`)

	locked := filepath.Join(user, "locked")
	if err := os.MkdirAll(locked, 0o755); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, locked, "secret.json", `{"s": 1}`)
	noread := filepath.Join(user, "noread.json")
	testutil.WriteFile(t, user, "noread.json", `{}`)

	// Deny an unlistable directory and an unreadable file through the package
	// seams: chmod 000 no longer denies once the suite runs as uid 0.
	restoreDir := readDir
	readDir = func(dir string) ([]os.DirEntry, error) {
		if dir == locked {
			return nil, fs.ErrPermission
		}
		return os.ReadDir(dir)
	}
	t.Cleanup(func() { readDir = restoreDir })
	restoreFile := readFile
	readFile = func(path string) ([]byte, error) {
		if path == noread {
			return nil, fs.ErrPermission
		}
		return os.ReadFile(path)
	}
	t.Cleanup(func() { readFile = restoreFile })

	entries := ScanMirror("opencode*.json*", nil, []string{user, ""}, dest)
	if len(entries) != 1 || entries[0].HostPath != filepath.Join(user, "ok.json") {
		t.Errorf("entries = %+v, want only ok.json", entries)
	}
}
```

Add the `io/fs` import if absent.

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test -tags microsandbox_ffi_path -run TestScanMirrorSkipsUnreadableEntries ./internal/configmerge/`
Expected: FAIL — `noread.json` is now readable, and nothing reads through `readFile` yet, so `entries` includes it.

- [ ] **Step 3: Use the `readFile` var in production**

In `merge.go`, after `var readDir = os.ReadDir` (line 89):

```go
var readFile = os.ReadFile
```

Change `data, err := os.ReadFile(snippetPath)` (line 121) to `data, err := readFile(snippetPath)`.

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test -tags microsandbox_ffi_path -count=2 ./internal/configmerge/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/configmerge/merge.go internal/configmerge/merge_test.go
git commit -m "test(configmerge): induce unreadable entries through seams, not chmod"
```

---

### Task 2: `runtimemigration` `readDir` seam

**Files:**
- Modify: `internal/sandbox/runtimemigration/legacy.go`
- Test: `internal/sandbox/runtimemigration/legacy_test.go`, `internal/sandbox/runtimemigration/notice_test.go`

**Interfaces:**
- Produces: `var readDir = os.ReadDir` in `legacy.go`; `matchingDirNames` calls `readDir(dir)` at legacy.go:187. The two tests keep calling the shared `makeVolumeDirUnreadable(t, home)` helper, whose body changes.

- [ ] **Step 1: Rewrite the helper to deny listing through the seam**

Replace `makeVolumeDirUnreadable` (legacy_test.go:296-307):

```go
// makeVolumeDirUnreadable makes <home>/volumes fail to list with a permission
// error through the readDir seam: the tolerant listing treats that as empty,
// while the strict listing reports it. chmod 000 would not deny once the suite
// runs as uid 0.
func makeVolumeDirUnreadable(t *testing.T, home string) {
	t.Helper()
	volumes := filepath.Join(home, "volumes")
	restore := readDir
	readDir = func(dir string) ([]os.DirEntry, error) {
		if dir == volumes {
			return nil, fs.ErrPermission
		}
		return os.ReadDir(dir)
	}
	t.Cleanup(func() { readDir = restore })
}
```

Add the `io/fs` import if absent. Both `TestDetectLegacyArtifactsMarksUnreadableVolumes` and `TestShowNoticeUnreadableVolumesSuppressesWholesaleCommand` then exercise the seam unchanged.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test -tags microsandbox_ffi_path -run 'TestDetectLegacyArtifactsMarksUnreadableVolumes|TestShowNoticeUnreadableVolumesSuppressesWholesaleCommand' ./internal/sandbox/runtimemigration/`
Expected: FAIL — `<home>/volumes` is now listable (no chmod), and nothing reads through `readDir` yet, so `VolumesUnreadable` is false and the notice fires wholesale.

- [ ] **Step 3: Use the `readDir` var in production**

In `legacy.go`, add a seam block near the other package seams (the `copy.go` block at copy.go:18-26 is the precedent), or immediately above `matchingDirNames`:

```go
// readDir is os.ReadDir; tests substitute it to force an unlistable directory
// without relying on permission bits, which an effective uid of 0 bypasses.
var readDir = os.ReadDir
```

Change `entries, err := os.ReadDir(dir)` (legacy.go:187) to `entries, err := readDir(dir)`.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -tags microsandbox_ffi_path -count=2 ./internal/sandbox/runtimemigration/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/runtimemigration/legacy.go internal/sandbox/runtimemigration/legacy_test.go internal/sandbox/runtimemigration/notice_test.go
git commit -m "test(runtimemigration): induce unlistable volumes through a seam, not chmod"
```

---

### Task 3: `state` write seam and the three `vm` tests

**Files:**
- Modify: `internal/sandbox/state/state.go`, `internal/sandbox/state/root.go`
- Test: `internal/sandbox/vm/adopt_test.go`, `internal/sandbox/vm/provenance_test.go`
- Possibly modify: `internal/sandbox/state/api_contract_test.go`

**Interfaces:**
- Produces, in `internal/sandbox/state`:
  ```go
  var writeStateFile = atomicwrite.WriteFile

  // OverrideStateWriteForTest replaces the writer the state writers use and
  // returns a restore function. Tests outside this package use it to force a
  // write failure without permission bits, which an effective uid of 0 bypasses.
  func OverrideStateWriteForTest(write func(path string, data []byte) error) func()
  ```
  `writeState` (state.go:213) and `writeRootState` (root.go:287) call `writeStateFile(...)`.

- [ ] **Step 1: Rewrite the three tests to deny writes through the hook**

In `provenance_test.go`, `TestRecordImageProvenanceWarnsWhenStateCannotBeWritten` (provenance_test.go:42-66): keep the `seedHomeState` line so the read succeeds; replace the `chmod(dir, 0o555)` + cleanup with:

```go
	k := state.Key{Slug: "proj", Agent: "opencode"}
	if err := seedHomeState(k, state.NewHomeState("vol", "digest")); err != nil {
		t.Fatal(err)
	}
	restore := state.OverrideStateWriteForTest(func(string, []byte) error {
		return &os.PathError{Op: "write", Path: state.KeyDir(k), Err: fs.ErrPermission}
	})
	t.Cleanup(restore)
```

The rest (the `msb.NewTestFS` reader mock and the `"could not persist project state"` assertion) is unchanged.

In `adopt_test.go`, `TestAdoptLegacyIdentityClaimCommitFailureFailsClosed` (adopt_test.go:176-223): drop the read-only-root setup (the lock pre-creation and `chmod(root, 0o555)`, lines 184-198); the root write is the first durable step, so a failing writer aborts there. Insert:

```go
	restore := state.OverrideStateWriteForTest(func(path string, _ []byte) error {
		return &os.PathError{Op: "rename", Path: path, Err: fs.ErrPermission}
	})
	t.Cleanup(restore)
```

and keep the assertions (the `errors.As(err, new(*os.PathError))`, VM survives, no marker) unchanged.

In `adopt_test.go`, `TestAdoptLegacyIdentityPersistRecoveredVolumeFailureFailsClosed` (adopt_test.go:131-171): only the checkout **marker** write must fail (the root claim must commit). Keep `os.MkdirAll(keyDir, 0o700)` (the read must see a missing file) but drop the `chmod(keyDir, 0o555)`. Insert:

```go
	markerPath := filepath.Join(state.KeyDir(adoptCheckoutKey()), "state.yaml")
	restore := state.OverrideStateWriteForTest(func(path string, data []byte) error {
		if path == markerPath {
			return &os.PathError{Op: "write", Path: path, Err: fs.ErrPermission}
		}
		return atomicwrite.WriteFile(path, data)
	})
	t.Cleanup(restore)
```

Add the `internal/atomicwrite` and `io/fs` imports where needed.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test -tags microsandbox_ffi_path -run 'TestAdoptLegacyIdentityPersistRecoveredVolumeFailureFailsClosed|TestAdoptLegacyIdentityClaimCommitFailureFailsClosed|TestRecordImageProvenanceWarnsWhenStateCannotBeWritten' ./internal/sandbox/vm/`
Expected: FAIL to build — `state.OverrideStateWriteForTest` is undefined.

- [ ] **Step 3: Add the seam**

In `state.go`, beside `writeState`:

```go
var writeStateFile = atomicwrite.WriteFile

// OverrideStateWriteForTest replaces the writer the state writers use and
// returns a restore function. Tests outside this package use it to force a
// write failure without permission bits, which an effective uid of 0 bypasses.
func OverrideStateWriteForTest(write func(path string, data []byte) error) func() {
	previous := writeStateFile
	writeStateFile = write
	return func() { writeStateFile = previous }
}
```

Change `atomicwrite.WriteFile(stateFile(k), data)` (state.go:213) to `writeStateFile(stateFile(k), data)`, and `atomicwrite.WriteFile(rootStateFile(), data)` (root.go:287) to `writeStateFile(rootStateFile(), data)`.

If `internal/sandbox/state/api_contract_test.go` pins the exported identifier set, add `OverrideStateWriteForTest`.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -tags microsandbox_ffi_path -count=2 ./internal/sandbox/state/ ./internal/sandbox/vm/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/state/state.go internal/sandbox/state/root.go internal/sandbox/state/api_contract_test.go internal/sandbox/vm/adopt_test.go internal/sandbox/vm/provenance_test.go
git commit -m "test(vm): deny state writes through a seam, not read-only dirs"
```

---

### Task 4: pin `GOTOOLCHAIN=local` in the envelope

**Files:**
- Modify: `scripts/test-envelope.sh`, `scripts/test-envelope_test.sh`

**Interfaces:**
- Produces: `GOTOOLCHAIN=local` in the envelope's env allowlist, so a `toolchain` directive cannot trigger a download inside the strict run.

- [ ] **Step 1: Add the failing assertion**

In `scripts/test-envelope_test.sh`, after the `-count=1` case:

```bash
# Case: the envelope pins the local toolchain (no download inside --netns).
assert_contains "GOTOOLCHAIN pinned to local" "${printed:-}" "env GOTOOLCHAIN=local"
```

Place it after the `printed` capture from the `--print` case so `${printed}` is set.

- [ ] **Step 2: Run the test to verify it fails**

Run: `./scripts/test-envelope_test.sh`
Expected: FAIL — `GOTOOLCHAIN pinned to local` missing.

- [ ] **Step 3: Add it to the allowlist**

In `scripts/test-envelope.sh`, add to `env_args`:

```bash
  "GOTOOLCHAIN=local"
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `./scripts/test-envelope_test.sh`
Expected: PASS (22 passed, 0 failed).

- [ ] **Step 5: Commit**

```bash
git add scripts/test-envelope.sh scripts/test-envelope_test.sh
git commit -m "test: pin GOTOOLCHAIN=local in the hermetic envelope"
```

---

### Task 5: Verify the strict envelope is green and record it

**Files:**
- Modify: `.operator-shared/plans/2026-11-07-hermetic-inventory.md`

- [ ] **Step 1: Run the strict envelope**

Run: `make test-hermetic`
Expected: EXIT 0 — every package `ok`, including `internal/configmerge`, `internal/sandbox/runtimemigration`, and `internal/sandbox/vm`.

- [ ] **Step 2: Confirm the default suite is unaffected**

Run: `make check`
Expected: EXIT 0.

- [ ] **Step 3: Record the resolution**

In `.operator-shared/plans/2026-11-07-hermetic-inventory.md`, mark the uid/permission surface resolved: the six tests now induce the failure through seams (`configmerge.readFile`, `runtimemigration.readDir`, `state.OverrideStateWriteForTest`), and `GOTOOLCHAIN=local` is pinned. Replace the "Follow-up scope" paragraph with the state that the strict run is green.

- [ ] **Step 4: Commit**

```bash
git add .operator-shared/plans/2026-11-07-hermetic-inventory.md
git commit -m "docs(operator): the strict envelope is green after the uid/permission fix"
```

---

## Self-Review

- **Spec coverage:** the inventory's one failing surface (6 tests, 3 packages) is Tasks 1-3; the `GOTOOLCHAIN=local` recommendation is Task 4; Task 5 is the acceptance (`make test-hermetic` green). The design's other surfaces (identity/CWD, host-figure sentinel, wall-clock, cadence) are deliberately separate plans.
- **Step scan:** each step names one action and a checkable result.
- **Type consistency:** `readFile` (configmerge), `readDir` (runtimemigration), `writeStateFile`/`OverrideStateWriteForTest` (state), `GOTOOLCHAIN=local` are used with the same names throughout.
- **Review Focus:** five lines, each with the task that pins it.
- **Seam-shape choice:** the `state` write seam is pinned as an exported override returning a restore closure because the failing tests live in `vm`; if `api_contract_test.go` pins the exported surface, Task 3 updates it.
- **Proportion:** three small production indirections, three test rewrites, one allowlist entry, and one verification.
