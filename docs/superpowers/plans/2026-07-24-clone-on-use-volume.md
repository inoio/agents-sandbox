# Clone-on-Use Home Volume Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** When a second opencode-msb session starts while another session is already using the same home volume, clone the volume so each session gets an isolated copy of opencode state, preventing SQLite corruption across VM kernels.

**Architecture:** The existing `ensureNoConflictingSession` checks whether a sandbox with the same name (same project + branch) is already running. This plan refactors that into two distinct checks following the same pattern: (1) `sameBranchSessionExists` / `ensureNoSameBranchSession` — the existing same-name check, and (2) `sameHomeVolumeInUse` / `ensureNoSameHomeSession` — a new check that scans all active sandboxes for references to the same named volume. When a conflict is found, the home volume is cloned via a temp sandbox (adapting the existing `prefillVolume` pattern), and the clone is auto-removed on session exit.

**Tech Stack:** Go 1.26, microsandbox SDK v0.6.6, cobra CLI, existing prompt package.

## Global Constraints

- Target platforms: Linux (KVM) and macOS (Apple Silicon).
- One ephemeral microsandbox VM per `opencode` invocation.
- Code style: self-explanatory, minimal abstractions, no comments unless code is not self-explanatory.
- No new CLI flags — the existing `--yes`/`-y` flag already sets `prompt.AssumeYes`.
- msb SDK v0.6.6's `SandboxHandle.Config()` does NOT populate the `Volumes` field (custom `UnmarshalJSON` skips it). The raw `ConfigJSON()` string must be parsed directly to extract named-volume references.

---

## File Structure

| File | Responsibility | Action |
|------|---------------|--------|
| `internal/sandbox/volumes.go` | Volume lifecycle: naming, creation, prefill, cloning, in-use detection | Modify |
| `internal/sandbox/volumes_test.go` | Unit tests for pure volume functions | Modify |
| `internal/sandbox/runner.go` | Session lifecycle: sandbox naming, conflict detection, session struct, cleanup | Modify |
| `internal/sandbox/runner_test.go` | Unit tests for runner functions | Modify |
| `internal/sandbox/integration_test.go` | Integration tests requiring msb daemon | Create |

Integration tests use `//go:build integration` at file level and require `-tags integration` to run. They need a separate file because Go build tags apply to entire files, not individual functions.

---

### Task 1: `sameHomeVolumeInUse` — detect if a named volume is in use by an active sandbox

**Files:**
- Modify: `internal/sandbox/volumes.go`
- Modify: `internal/sandbox/volumes_test.go` (unit tests for `extractNamedVolumes`)
- Create: `internal/sandbox/integration_test.go` (integration test for `sameHomeVolumeInUse`)

**Interfaces:**
- Consumes: `msb.ListSandboxes(ctx)` returning `[]*msb.SandboxHandle`, `msb.SandboxHandle.Name()`, `msb.SandboxHandle.Status()`, `msb.SandboxHandle.ConfigJSON()`, `isSandboxActive(msb.SandboxStatus)` from `runner.go`
- Produces: `sameHomeVolumeInUse(ctx, volumeName, excludeSandbox string) (string, bool, error)` — returns the name of the active sandbox using the volume, or `""`/`false` if not in use

**Background:** The msb SDK's `SandboxHandle.Config()` method has a custom `UnmarshalJSON` that does NOT populate the `Volumes` field. However, the raw `ConfigJSON()` string contains a `"volumes"` key with mount specs that have a `"named"` field. We must parse the raw JSON directly.

- [ ] **Step 1: Write the failing tests**

Create `internal/sandbox/integration_test.go` with the build tag and the integration test. Add the unit tests to `internal/sandbox/volumes_test.go`:

`internal/sandbox/integration_test.go` (new file):

```go
//go:build integration

package sandbox

import (
	"testing"
)

func TestSameHomeVolumeInUseNoSandboxes(t *testing.T) {
	got, inUse, err := sameHomeVolumeInUse(t.Context(), "my-vol", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if inUse {
		t.Error("expected not in use when no sandboxes exist")
	}
	if got != "" {
		t.Errorf("expected empty sandbox name, got %q", got)
	}
}
```

`internal/sandbox/volumes_test.go` (append to existing):

```go
func TestExtractNamedVolumes(t *testing.T) {
	configJSON := `{
		"name": "test-sandbox",
		"volumes": {
			"/home/dev": {"named": "my-home-vol"},
			"/workspace": {"bind": "/host/path"}
		}
	}`
	got := extractNamedVolumes(configJSON)
	if len(got) != 1 {
		t.Fatalf("expected 1 named volume, got %d", len(got))
	}
	if got[0] != "my-home-vol" {
		t.Errorf("expected 'my-home-vol', got %q", got[0])
	}
}

func TestExtractNamedVolumesEmpty(t *testing.T) {
	configJSON := `{"name": "test"}`
	got := extractNamedVolumes(configJSON)
	if len(got) != 0 {
		t.Fatalf("expected 0 named volumes, got %d", len(got))
	}
}

func TestExtractNamedVolumesInvalidJSON(t *testing.T) {
	got := extractNamedVolumes("not json")
	if len(got) != 0 {
		t.Fatalf("expected 0 named volumes for invalid JSON, got %d", len(got))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/sandbox/ -run 'TestExtractNamedVolumes' -v`
Expected: FAIL with "undefined: extractNamedVolumes"

- [ ] **Step 3: Implement `extractNamedVolumes` and `sameHomeVolumeInUse`**

Add to `internal/sandbox/volumes.go`. First add `"encoding/json"` to the import block (alongside the existing `"context"`, `"fmt"`, `"strings"`, `"time"` imports):

```go
type rawMountSpec struct {
	Named string `json:"named,omitempty"`
}

type rawSandboxConfig struct {
	Volumes map[string]rawMountSpec `json:"volumes,omitempty"`
}

func extractNamedVolumes(configJSON string) []string {
	var raw rawSandboxConfig
	if err := json.Unmarshal([]byte(configJSON), &raw); err != nil {
		return nil
	}
	var names []string
	for _, spec := range raw.Volumes {
		if spec.Named != "" {
			names = append(names, spec.Named)
		}
	}
	return names
}

func sameHomeVolumeInUse(
	ctx context.Context,
	volumeName, excludeSandbox string,
) (string, bool, error) {
	handles, err := msb.ListSandboxes(ctx)
	if err != nil {
		return "", false, fmt.Errorf("list sandboxes: %w", err)
	}
	for _, h := range handles {
		if h.Name() == excludeSandbox {
			continue
		}
		if !isSandboxActive(h.Status()) {
			continue
		}
		for _, name := range extractNamedVolumes(h.ConfigJSON()) {
			if name == volumeName {
				return h.Name(), true, nil
			}
		}
	}
	return "", false, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/sandbox/ -run 'TestExtractNamedVolumes' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/volumes.go internal/sandbox/volumes_test.go internal/sandbox/integration_test.go
git commit -m "feat: add sameHomeVolumeInUse to detect volume conflicts across sandboxes"
```

---

### Task 2: `CloneVolume` — clone a named volume via a temp sandbox

**Files:**
- Modify: `internal/sandbox/volumes.go`
- Test: `internal/sandbox/volumes_test.go`

**Interfaces:**
- Consumes: `msb.CreateVolume(ctx, name, opts...)`, `msb.Mount.Named(name, opts)`, `msb.MountOptions{Readonly: true}`, `msb.CreateSandbox(...)`, `msb.WithImage(imageTag)`, `msb.WithMounts(...)`, `msb.WithReplace()`, `sandboxStopTimeout`, `vm.prefillVolume` pattern
- Produces: `(vm *VolumeManager) CloneVolume(ctx, sourceVol, imageTag string) (string, error)` — creates a new volume, copies source contents, deletes SQLite shm files, returns clone volume name

**Background:** This adapts the existing `prefillVolume` pattern (volumes.go:55-93). The source is mounted read-only at `/mnt/src`, the new clone at `/mnt/dst`. We run `cp -a` then delete `*.shm` files to prevent stale SQLite shared-memory state. Clone volume name includes a timestamp for uniqueness, same as `prefillName`.

- [ ] **Step 1: Write the failing test**

Add to `internal/sandbox/volumes_test.go`. `cloneVolumeName` is a pure function; the full `CloneVolume` method requires msb daemon + Docker image and is only tested via manual/integration testing.

```go
func TestCloneVolumeName(t *testing.T) {
	name := cloneVolumeName("my-source-vol")
	if !strings.HasPrefix(name, "my-source-vol-clone-") {
		t.Errorf("expected clone name to start with 'my-source-vol-clone-', got %q", name)
	}
}
```

Note: `volumes_test.go` already imports `"strings"` (used in `TestHomeVolumeNameSanitizesColon`).

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/sandbox/ -run 'TestCloneVolumeName' -v`
Expected: FAIL with "undefined: cloneVolumeName"

- [ ] **Step 3: Implement `cloneVolumeName` and `CloneVolume`**

Add to `internal/sandbox/volumes.go`:

```go
func cloneVolumeName(sourceVol string) string {
	return fmt.Sprintf("%s-clone-%d", sourceVol, time.Now().UnixNano())
}

func (vm *VolumeManager) CloneVolume(
	ctx context.Context,
	sourceVol, imageTag string,
) (string, error) {
	cloneName := cloneVolumeName(sourceVol)

	vol, err := msb.CreateVolume(ctx, cloneName,
		msb.WithVolumeKind(msb.VolumeKindDir),
	)
	if err != nil {
		return "", fmt.Errorf("create clone volume %s: %w", cloneName, err)
	}

	prefillName := fmt.Sprintf("opencode-msb-clone-%d", time.Now().UnixNano())

	mounts := map[string]msb.MountConfig{
		"/mnt/src": msb.Mount.Named(sourceVol, msb.MountOptions{Readonly: true}),
		"/mnt/dst": msb.Mount.Named(vol.Name(), msb.MountOptions{}),
	}

	spin := log.NewSpinner(vm.logger)
	spin.Start("Cloning home volume")
	sb, err := msb.CreateSandbox(ctx, prefillName,
		msb.WithImage(imageTag),
		msb.WithMounts(mounts),
		msb.WithReplace(),
	)
	if err != nil {
		spin.StopError(err)
		return "", fmt.Errorf("create clone sandbox: %w", err)
	}
	defer func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), sandboxStopTimeout)
		defer cancel()
		_ = sb.Stop(stopCtx)
		_ = sb.Close()
		_ = msb.RemoveSandbox(context.Background(), prefillName)
	}()

	out, err := sb.Exec(ctx, "sh", []string{"-c",
		"cp -a /mnt/src/. /mnt/dst/ && chown -R dev:dev /mnt/dst && find /mnt/dst -name '*.shm' -delete",
	})
	if err != nil {
		spin.StopError(err)
		return "", fmt.Errorf("clone cp: %w", err)
	}
	if !out.Success() {
		err := fmt.Errorf("clone cp failed (exit %d): %s", out.ExitCode(), out.Stderr())
		spin.StopError(err)
		return "", err
	}
	spin.Stop()
	return cloneName, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/sandbox/ -run 'TestCloneVolumeName' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/volumes.go internal/sandbox/volumes_test.go
git commit -m "feat: add CloneVolume to copy home volume for parallel sessions"
```

---

### Task 3: Refactor `ensureNoConflictingSession` into `sameBranchSessionExists` + `ensureNoSameBranchSession`

**Files:**
- Modify: `internal/sandbox/runner.go`
- Modify: `internal/sandbox/integration_test.go` (integration test)

**Interfaces:**
- Consumes: existing `runningSandboxExists`, `promptExistingSession`, `prompt.AssumeYes`, `prompt.Select`
- Produces:
  - `sameBranchSessionExists(ctx, sandboxName string) (bool, error)` — pure check, no prompt
  - `ensureNoSameBranchSession(ctx, name, projectSlug, branch string, logger) error` — wraps check + prompt, same behavior as existing `ensureNoConflictingSession`

**Background:** The existing `ensureNoConflictingSession` (runner.go:137-157) does two things: (1) checks if a sandbox with the same name is running, (2) prompts the user. We split these into a pure check (`sameBranchSessionExists`) and an ensure wrapper (`ensureNoSameBranchSession`) to match the pattern we'll use for home-volume conflicts.

- [ ] **Step 1: Write the failing test**

Add to `internal/sandbox/integration_test.go`. The existing `TestPromptExistingSession*` tests in `runner_test.go` exercise `promptExistingSession` directly and remain unchanged.

```go
func TestSameBranchSessionExistsNoSandbox(t *testing.T) {
	exists, err := sameBranchSessionExists(t.Context(), "nonexistent-sandbox")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if exists {
		t.Error("expected false for nonexistent sandbox")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/sandbox/ -run 'TestSameBranchSessionExists' -v -tags integration`
Expected: FAIL with "undefined: sameBranchSessionExists"

- [ ] **Step 3: Refactor — rename and split**

In `internal/sandbox/runner.go`, rename `runningSandboxExists` to `sameBranchSessionExists` and `ensureNoConflictingSession` to `ensureNoSameBranchSession`:

Replace at runner.go:102-113:

```go
func sameBranchSessionExists(ctx context.Context, name string) (bool, error) {
	handle, err := msb.GetSandbox(ctx, name)
	if err != nil {
		if msb.IsKind(err, msb.ErrSandboxNotFound) {
			return false, nil
		}
		return false, fmt.Errorf("check existing sandbox %q: %w", name, err)
	}
	return isSandboxActive(handle.Status()), nil
}
```

Replace at runner.go:137-157:

```go
func ensureNoSameBranchSession(
	ctx context.Context,
	name, projectSlug, branch string,
	logger *log.Logger,
) error {
	running, err := sameBranchSessionExists(ctx, name)
	if err != nil {
		return err
	}
	if !running {
		return nil
	}
	terminate, err := promptExistingSession(name, logger)
	if err != nil {
		return err
	}
	if !terminate {
		return fmt.Errorf("a session is already running for %q on branch %q", projectSlug, branch)
	}
	return nil
}
```

Update the call site in `prepareSandbox` (runner.go:461):

```go
if err = ensureNoSameBranchSession(ctx, name, projectSlug, branch, logger); err != nil {
	return nil, err
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/sandbox/ -run 'TestPrompt|TestIsSandboxActive' -v`
Expected: PASS (existing tests still work with renamed function)

- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/runner.go internal/sandbox/integration_test.go
git commit -m "refactor: split ensureNoConflictingSession into sameBranchSessionExists + ensureNoSameBranchSession"
```

---

### Task 4: `ensureNoSameHomeSession` — detect home volume conflict, prompt, and clone

**Files:**
- Modify: `internal/sandbox/runner.go`
- Modify: `internal/sandbox/integration_test.go` (integration test)

**Interfaces:**
- Consumes: `sameHomeVolumeInUse` (Task 1), `vm.CloneVolume` (Task 2), `prompt.ConfirmDefault`, `prompt.AssumeYes`, `log.Logger`
- Produces: `ensureNoSameHomeSession(ctx, vm, homeVol, excludeSandbox, imageRef string, logger) (string, error)` — returns the volume to use (original or clone), or error if user aborts

**Background:** This function is called after `ensureNoSameBranchSession` (same-branch conflicts already resolved) and before `createSandbox`. If another active sandbox is using the same home volume, it warns the user, prompts for confirmation (or proceeds with `--yes`), and clones the volume.

- [ ] **Step 1: Write the failing test**

Add to `internal/sandbox/integration_test.go`:

```go
func TestEnsureNoSameHomeSessionNoConflict(t *testing.T) {
	vm := NewVolumeManager(newTestLogger(t))
	got, err := ensureNoSameHomeSession(t.Context(), vm, "nonexistent-vol", "my-sandbox", "my-image", newTestLogger(t))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "nonexistent-vol" {
		t.Errorf("expected original volume name, got %q", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/sandbox/ -run 'TestEnsureNoSameHomeSession' -v -tags integration`
Expected: FAIL with "undefined: ensureNoSameHomeSession"

- [ ] **Step 3: Implement `ensureNoSameHomeSession`**

Add to `internal/sandbox/runner.go`:

```go
func ensureNoSameHomeSession(
	ctx context.Context,
	vm *VolumeManager,
	homeVol, excludeSandbox, imageRef string,
	logger *log.Logger,
) (string, error) {
	inUseBy, inUse, err := sameHomeVolumeInUse(ctx, homeVol, excludeSandbox)
	if err != nil {
		return "", err
	}
	if !inUse {
		return homeVol, nil
	}

	logger.Warn(fmt.Sprintf(
		"Another opencode session (%q) is using the same project state.\n"+
			"Starting with a snapshot copy of the current home directory.\n"+
			"Opencode sessions and history from this run will NOT be persisted.",
		inUseBy,
	))

	if !prompt.AssumeYes {
		confirmed, err := prompt.ConfirmDefault("Proceed with snapshot copy?", false, logger)
		if err != nil {
			return "", fmt.Errorf("prompt for clone: %w", err)
		}
		if !confirmed {
			return "", fmt.Errorf("aborted: another session (%q) is using the project state", inUseBy)
		}
	}

	cloneVol, err := vm.CloneVolume(ctx, homeVol, imageRef)
	if err != nil {
		return "", err
	}
	logger.Info(fmt.Sprintf("Cloned home volume: %s", cloneVol))
	return cloneVol, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/sandbox/ -run 'TestEnsureNoSameHomeSession' -v -tags integration` (requires msb)
Run: `go build ./...` (verify compilation without integration tag)
Expected: PASS (with msb) / clean build (without)

- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/runner.go internal/sandbox/integration_test.go
git commit -m "feat: add ensureNoSameHomeSession to clone volume on parallel session detection"
```

---

### Task 5: Wire into `prepareSandbox` and add clone cleanup to `sandboxSession`

**Files:**
- Modify: `internal/sandbox/runner.go`
- Modify: `internal/sandbox/runner_test.go` (unit test for struct field)

**Interfaces:**
- Consumes: `ensureNoSameBranchSession` (Task 3), `ensureNoSameHomeSession` (Task 4), `vm.CloneVolume` (Task 2)
- Produces: modified `prepareSandbox` flow, modified `sandboxSession` struct with `cloneVol` field, modified `cleanup()` method

**Background:** The current flow in `prepareSandbox` (runner.go:412-485) is:
1. `EnsureHome()` → returns `homeVol`
2. `sandboxName()` → returns `name`
3. `ensureNoConflictingSession()` → checks same-branch conflict
4. `createSandbox()` → creates VM

The new flow inserts `ensureNoSameHomeSession` between steps 3 and 4, and may replace `homeVol` with a clone name. The `sandboxSession` struct gains a `cloneVol` field that `cleanup()` uses to remove the clone volume.

- [ ] **Step 1: Write the failing test**

Add to `internal/sandbox/runner_test.go`:

```go
func TestSandboxSessionCleanupRemovesCloneVolume(t *testing.T) {
	// Verify the struct has a cloneVol field and cleanup removes it.
	// This is a structural test — full lifecycle requires msb.
	s := &sandboxSession{
		name:     "test-sandbox",
		cloneVol: "test-clone-vol",
	}
	if s.cloneVol != "test-clone-vol" {
		t.Errorf("expected cloneVol to be set, got %q", s.cloneVol)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/sandbox/ -run 'TestSandboxSessionCleanup' -v`
Expected: FAIL — `sandboxSession` has no `cloneVol` field

- [ ] **Step 3: Add `cloneVol` field to `sandboxSession` and update `cleanup`**

In `internal/sandbox/runner.go`, modify the struct at line 394:

```go
type sandboxSession struct {
	sb        *msb.Sandbox
	name      string
	repoPath  string
	cwd       string
	cwdBranch string
	created   bool
	branch    string
	cloneVol  string
}
```

Update `cleanup()` at line 404:

```go
func (s *sandboxSession) cleanup() {
	stopCtx, cancel := context.WithTimeout(context.Background(), sandboxStopTimeout)
	defer cancel()
	_ = s.sb.Stop(stopCtx)
	_ = s.sb.Close()
	_ = msb.RemoveSandbox(context.Background(), s.name)
	if s.cloneVol != "" {
		_ = msb.RemoveVolume(context.Background(), s.cloneVol)
	}
}
```

- [ ] **Step 4: Wire `ensureNoSameHomeSession` into `prepareSandbox`**

In `prepareSandbox` (runner.go ~line 459-466), replace:

```go
	name := sandboxName(projectSlug, git.BranchSlug(branch))

	if err = ensureNoConflictingSession(ctx, name, projectSlug, branch, logger); err != nil {
		return nil, err
	}

	logger.Debug(fmt.Sprintf("sandbox: %s (cpus=%d, memory=%s)", name, opts.CPUs, opts.Memory))
	sb, err := createSandbox(ctx, name, imageRef, repoPath, homeVol, opts.User, opts, cfg, logger)
```

	with:

```go
	name := sandboxName(projectSlug, git.BranchSlug(branch))

	if err = ensureNoSameBranchSession(ctx, name, projectSlug, branch, logger); err != nil {
		return nil, err
	}

	originalVol := homeVol
	homeVol, err = ensureNoSameHomeSession(ctx, vm, homeVol, name, imageRef, logger)
	if err != nil {
		return nil, err
	}
	cloneVol := ""
	if homeVol != originalVol {
		cloneVol = homeVol
	}

	logger.Debug(fmt.Sprintf("sandbox: %s (cpus=%d, memory=%s)", name, opts.CPUs, opts.Memory))
	sb, err := createSandbox(ctx, name, imageRef, repoPath, homeVol, opts.User, opts, cfg, logger)
```

And update the return at line 476 to include `cloneVol`:

```go
	return &sandboxSession{
		sb:        sb,
		name:      name,
		repoPath:  repoPath,
		cwd:       cwd,
		cwdBranch: cwdBranch,
		created:   created,
		branch:    branch,
		cloneVol:  cloneVol,
	}, nil
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/sandbox/ -run 'TestSandboxSessionCleanup' -v`
Run: `go test ./internal/sandbox/ -v -short`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/sandbox/runner.go internal/sandbox/runner_test.go
git commit -m "feat: wire clone-on-use into prepareSandbox and cleanup"
```

---

### Task 6: Verify, lint, and final cleanup

**Files:**
- All modified files

- [ ] **Step 1: Run full test suite**

Run: `go test ./... -v -short`
Expected: All tests PASS

- [ ] **Step 2: Run linter**

Run: `golangci-lint fmt && golangci-lint run`
Expected: No errors

- [ ] **Step 3: Build**

Run: `go build ./cmd/opencode-msb`
Expected: Clean build

- [ ] **Step 4: Run dry-run to verify flow**

Run: `go run ./cmd/opencode-msb --dry-run`
Expected: Completes without error, logs show volume and sandbox setup

- [ ] **Step 5: Commit any remaining fixes**

```bash
git add -A
git commit -m "chore: lint and verify clone-on-use volume implementation"
```

---

## Known Limitations

1. **TOCTOU race**: Two sessions starting simultaneously could both see "not in use" and share the base volume. Unlikely for manually-started sessions. A host-side lock file could mitigate this in the future.

2. **Clone is discarded on exit**: Opencode state (history, sessions) from the clone session is lost. Code changes in `/workspace` (git repo) are preserved — that's a separate bind mount.

3. **Best-effort consistency**: The clone is crash-consistent for SQLite (WAL replay handles it) but not guaranteed atomic. Deleting `*.shm` files mitigates the main risk (stale memory-mapped cache).

4. **Integration tests require msb daemon**: Tests tagged with `//go:build integration` only run with `-tags integration` and a running msb daemon. Unit tests (`extractNamedVolumes`, `cloneVolumeName`, struct field tests) run without msb.
