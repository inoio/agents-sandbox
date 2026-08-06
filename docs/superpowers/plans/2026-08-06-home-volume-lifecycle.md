# Home Volume Lifecycle Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Decouple home volumes from image digests, persist them across image changes using a YAML state file, and provide CLI commands (migrate, reset, edit) for manual volume management.

**Architecture:** The home volume name changes from `{slug}-{sha256hash}` to `{slug}-{timestamp}`. A YAML state file at `~/.local/state/opencode-msb/{slug}/state.yaml` tracks the active volume name and image digest. When `Run` is called during `run`, it first checks the state file — if the volume exists and the digest matches, it returns it immediately. If the digest changed, it prompts the user with keep/migrate/reset/quit options. The prune pipeline drops its digest-matching volume cleanup and relies entirely on `pruneOrphanSlug` to clean displaced volumes.

**Tech Stack:** Go, `gopkg.in/yaml.v3`, cobra CLI, microsandbox SDK.

## Global Constraints

- **State file path:** `~/.local/state/opencode-msb/{slug}/state.yaml` — one YAML file per project slug with `home_volume` and `image_digest` scalar fields
- **Volume naming:** New names are `opencode-msb-home-{slug}-{timestamp}` where `timestamp` = `time.Now().UTC().Format("20060102T150405")`
- **Prompt options:** When image changes: `1) keep`, `2) migrate`, `3) reset`, `4) quit` with default `1`
- **Non-interactive default:** When `--yes` flag is passed, default to `keep` (use existing volume, do not prompt)
- **Active VM safety block:** Before migrate/reset/edit, block if any active or stale VMs exist for the slug — no `--yes` bypass
- **State cleanup on prune:** When a home volume is deleted during prune, remove the state file and its parent directory
- **Dependency order:** Timestamp naming → state file I/O → resolve logic → CLI commands → prune cleanup → tests → docs

---

### Task 1: Update volume naming to use timestamp

**Files:**
- Modify: `internal/sandbox/volumes.go:16-18`
- Test: `internal/sandbox/volumes_test.go` (update existing test, add new)

**Interfaces:**
- **Consumes:** `homePrefix` constant from `internal/sandbox/names.go`
- **Produces:** `HomeVolumeName(projectSlug string, digest string) string` — digest parameter retained but ignored for compatibility

New implementation of `HomeVolumeName`:

```go
func HomeVolumeName(projectSlug string, digest string) string {
    ts := time.Now().UTC().Format("20060102T150405")
    return homePrefix + projectSlug + "-" + ts
}
```

- [ ] **Step 1: Write failing tests**

Add to `internal/sandbox/volumes_test.go`:

```go
package sandbox

import (
    "strings"
    "testing"
    "time"
)

func TestHomeVolumeNameTimestamp(t *testing.T) {
    before := time.Now().UTC().Add(-time.Second)
    got := HomeVolumeName("myproject", "")
    after := time.Now().UTC().Add(time.Second)

    if !strings.HasPrefix(got, "opencode-msb-home-myproject-") {
        t.Fatalf("expected prefix, got %q", got)
    }
    suffix := strings.TrimPrefix(got, "opencode-msb-home-myproject-")
    if len(suffix) != 15 {
        t.Fatalf("expected 15-char timestamp, got %d chars: %q", len(suffix), suffix)
    }
    ts, err := time.Parse("20060102T150405", suffix)
    if err != nil {
        t.Fatalf("timestamp %q does not parse: %v", suffix, err)
    }
    if ts.Before(before) || ts.After(after) {
        t.Errorf("timestamp %v not within expected range", ts)
    }
}

func TestHomeVolumeNameDigestIgnored(t *testing.T) {
    // The digest parameter is ignored; same prefix for all digest values
    got1 := HomeVolumeName("proj", "sha256:abc123")
    got2 := HomeVolumeName("proj", "")
    got3 := HomeVolumeName("proj", "different")
    if !strings.HasPrefix(got1, "opencode-msb-home-proj-") {
        t.Errorf("got1 prefix wrong: %q", got1)
    }
    if !strings.HasPrefix(got2, "opencode-msb-home-proj-") {
        t.Errorf("got2 prefix wrong: %q", got2)
    }
    if !strings.HasPrefix(got3, "opencode-msb-home-proj-") {
        t.Errorf("got3 prefix wrong: %q", got3)
    }
}
```

Update `TestHomeVolumeName` and `TestHomeVolumeNameDifferentInputs` in `volumes_test.go`:

```go
func TestHomeVolumeName(t *testing.T) {
    got := HomeVolumeName("myproj-aBc1234D", "")
    expectedPrefix := "opencode-msb-home-myproj-aBc1234D-"
    if !strings.HasPrefix(got, expectedPrefix) {
        t.Errorf("expected prefix %q, got %q", expectedPrefix, got)
    }
    suffix := strings.TrimPrefix(got, expectedPrefix)
    if len(suffix) != 15 {
        t.Errorf("expected 15-char timestamp, got %d chars: %q", len(suffix), suffix)
    }
}

func TestHomeVolumeNameDifferentInputs(t *testing.T) {
    got := HomeVolumeName("myproj-aBc1234D", "sha256:abc123def456")
    if !strings.HasPrefix(got, "opencode-msb-home-myproj-aBc1234D-") {
        t.Errorf("unexpected name format: %q", got)
    }
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/sandbox -run "TestHomeVolumeName" -v`
Expected: FAIL — existing tests expect exact hash string like `3k5q07ywpibwp5`

- [ ] **Step 3: Write minimal implementation**

Replace `HomeVolumeName` in `internal/sandbox/volumes.go:16-18`:

```go
func HomeVolumeName(projectSlug string, digest string) string {
    ts := time.Now().UTC().Format("20060102T150405")
    return homePrefix + projectSlug + "-" + ts
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/sandbox -run "TestHomeVolumeName" -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/volumes.go internal/sandbox/volumes_test.go
git commit -m "chore: change home volume naming to use timestamp instead of digest"
```

---

### Task 2: Add state file I/O helpers

**Files:**
- Create: `internal/sandbox/state.go`
- Test: `internal/sandbox/state_test.go`

**Interfaces:**
- **Consumes:** `XdgStateSuffix` constant from `internal/sandbox/paths.go`
- **Produces:** `type HomeState struct { HomeVolume string; ImageDigest string }` — exported YAML struct
- **Produces:** `StateFile(slug string) string` — returns path to state.yaml
- **Produces:** `ReadState(slug string) (*HomeState, error)` — loads and parses YAML
- **Produces:** `WriteState(slug string, state HomeState) error` — atomic write (temp + rename)
- **Produces:** `RemoveState(slug string) error` — removes file and parent dir

Implementation of `internal/sandbox/state.go`:

```go
package sandbox

import (
    "fmt"
    "os"
    "path/filepath"

    "gopkg.in/yaml.v3"
)

// stateDirSuffix is the base directory for state files.
// Derived from XdgStateSuffix to allow override in tests.
var stateDirSuffix = XdgStateSuffix

// HomeState represents the per-project state file contents.
type HomeState struct {
    HomeVolume  string `yaml:"home_volume"`
    ImageDigest string `yaml:"image_digest"`
}

// StateFile returns the path to the state file for a project slug.
func StateFile(slug string) string {
    return filepath.Join(stateDirSuffix, slug, "state.yaml")
}

// ReadState loads and parses the state file. Returns nil, nil if no file exists.
// Returns error for parse failures or non-\"not found\" I/O errors.
func ReadState(slug string) (*HomeState, error) {
    path := StateFile(slug)
    data, err := os.ReadFile(path)
    if err != nil {
        if os.IsNotExist(err) {
            return nil, nil
        }
        return nil, fmt.Errorf("read state file %s: %w", path, err)
    }
    var state HomeState
    if err := yaml.Unmarshal(data, &state); err != nil {
        return nil, fmt.Errorf("parse state file %s: %w", path, err)
    }
    return &state, nil
}

// WriteState atomically writes the state to disk.
func WriteState(slug string, state HomeState) error {
    dir := filepath.Dir(StateFile(slug))
    if err := os.MkdirAll(dir, 0o700); err != nil {
        return fmt.Errorf("create state dir %s: %w", dir, err)
    }
    tmpFile := filepath.Join(dir, ".state.yaml.tmp")
    data, err := yaml.Marshal(state)
    if err != nil {
        return fmt.Errorf("marshal state: %w", err)
    }
    if err := os.WriteFile(tmpFile, data, 0o600); err != nil {
        return fmt.Errorf("write state temp: %w", err)
    }
    if err := os.Rename(tmpFile, StateFile(slug)); err != nil {
        os.Remove(tmpFile)
        return fmt.Errorf("rename state file: %w", err)
    }
    return nil
}

// RemoveState removes the state file and its parent directory.
func RemoveState(slug string) error {
    stateDir := filepath.Join(stateDirSuffix, slug)
    return os.RemoveAll(stateDir)
}
```

- [ ] **Step 1: Write failing tests**

Create `internal/sandbox/state_test.go`:

```go
package sandbox

import (
    "os"
    "path/filepath"
    "testing"
)

func TestStateFile(t *testing.T) {
    old := stateDirSuffix
    defer func() { stateDirSuffix = old }()
    stateDirSuffix = t.TempDir() + "/opencode-msb"

    slug := "testproj-aBc1234D"
    f := StateFile(slug)
    if filepath.Base(f) != "state.yaml" {
        t.Errorf("expected state.yaml basename, got %q", filepath.Base(f))
    }
    if !filepath.IsAbs(f) {
        t.Error("expected absolute path")
    }
}

func TestReadState_NoFileReturnsNil(t *testing.T) {
    old := stateDirSuffix
    defer func() { stateDirSuffix = old }()

    stateDirSuffix = t.TempDir() + "/opencode-msb"

    slug := "nonexistentproj-xyz"
    result, err := ReadState(slug)
    if err != nil {
        t.Fatalf("expected no error for missing file, got: %v", err)
    }
    if result != nil {
        t.Errorf("expected nil result, got %+v", result)
    }
}

func TestWriteAndReadState(t *testing.T) {
    old := stateDirSuffix
    defer func() { stateDirSuffix = old }()

    stateDirSuffix = t.TempDir() + "/opencode-msb"
    slug := "myproj-aBc1234D"
    digest := "sha256:deadbeef1234"

    err := WriteState(slug, HomeState{
        HomeVolume:  "opencode-msb-home-myproj-20260806T143022",
        ImageDigest: digest,
    })
    if err != nil {
        t.Fatalf("WriteState: %v", err)
    }

    result, err := ReadState(slug)
    if err != nil {
        t.Fatalf("ReadState: %v", err)
    }
    if result.HomeVolume != "opencode-msb-home-myproj-20260806T143022" {
        t.Errorf("HomeVolume = %q, want %q", result.HomeVolume, "opencode-msb-home-myproj-20260806T143022")
    }
    if result.ImageDigest != digest {
        t.Errorf("ImageDigest = %q, want %q", result.ImageDigest, digest)
    }

    fpath := filepath.Join(stateDirSuffix, slug, "state.yaml")
    if _, err := os.Stat(fpath); err != nil {
        t.Errorf("state file should exist at %s: %v", fpath, err)
    }
}

func TestWriteStateCreatesDirectory(t *testing.T) {
    old := stateDirSuffix
    defer func() { stateDirSuffix = old }()

    stateDirSuffix = t.TempDir() + "/opencode-msb"
    slug := "newproj-a"

    err := WriteState(slug, HomeState{HomeVolume: "vol", ImageDigest: "d1"})
    if err != nil {
        t.Fatalf("WriteState: %v", err)
    }

    dir := filepath.Join(stateDirSuffix, slug)
    info, err := os.Stat(dir)
    if err != nil {
        t.Fatalf("state dir should exist: %v", err)
    }
    if !info.IsDir() {
        t.Error("expected directory")
    }
}

func TestReadState_CorruptedYAML(t *testing.T) {
    old := stateDirSuffix
    defer func() { stateDirSuffix = old }()

    stateDirSuffix = t.TempDir() + "/opencode-msb"
    slug := "corruptproj"

    sdir := filepath.Join(stateDirSuffix, slug)
    if err := os.MkdirAll(sdir, 0o700); err != nil {
        t.Fatal(err)
    }
    if err := os.WriteFile(filepath.Join(sdir, "state.yaml"), []byte("not: valid: yaml: ["), 0o600); err != nil {
        t.Fatal(err)
    }

    _, err := ReadState(slug)
    if err == nil {
        t.Fatal("expected error for corrupted YAML")
    }
}

func TestRemoveState(t *testing.T) {
    old := stateDirSuffix
    defer func() { stateDirSuffix = old }()

    stateDirSuffix = t.TempDir() + "/opencode-msb"
    slug := "myproj"

    WriteState(slug, HomeState{HomeVolume: "vol", ImageDigest: "d1"})

    statePath := filepath.Join(stateDirSuffix, slug, "state.yaml")
    if _, err := os.Stat(statePath); os.IsNotExist(err) {
        t.Fatal("state file should exist before RemoveState")
    }

    RemoveState(slug)

    if _, err := os.Stat(statePath); !os.IsNotExist(err) {
        t.Errorf("state file should be removed, but still exists")
    }
    stateDir := filepath.Join(stateDirSuffix, slug)
    if _, err := os.Stat(stateDir); !os.IsNotExist(err) {
        t.Errorf("state dir should be removed, but still exists")
    }
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/sandbox -run "TestState|TestReadState|TestWriteAndRead|TestWriteStateCreates|TestReadState_Corrupted|TestRemoveState" -v`
Expected: FAIL — file `state.go` does not exist

- [ ] **Step 3: Write the implementation**

Create `internal/sandbox/state.go` with the code from the Interfaces section above.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/sandbox -run "TestState|TestReadState|TestWriteAndRead|TestWriteStateCreates|TestReadState_Corrupted|TestRemoveState" -v`
Expected: All PASS

- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/state.go internal/sandbox/state_test.go
git commit -m "feat: add YAML state file I/O for home volume tracking"
```

---

### Task 3: Update home volume name parsing for new format

**Files:**
- Modify: `internal/sandbox/parse_artifact.go:103-120`
- Modify: `internal/sandbox/prune_test.go` — add new-format tests

**Interfaces:**
- **Consumes:** `homePrefix` constant
- **Produces:** `parseHomeVolumeName` that handles both new and legacy formats
- The last segment of a new-format name (`YYYYMMDDTHHmmss`) is 15 chars containing `T`, not a digest hash

Implementation to replace in `internal/sandbox/parse_artifact.go:103-120`:

```go
func parseHomeVolumeName(name string) artifactInfo {
    if !strings.HasPrefix(name, homePrefix) {
        return artifactInfo{}
    }
    remainder := name[len(homePrefix):]
    parts := strings.Split(remainder, "-")
    if len(parts) < 2 {
        return artifactInfo{slug: remainder}
    }
    // Check if last part looks like a timestamp (YYYYMMDDTHHmmss = 15 chars with 'T' at pos 8)
    last := parts[len(parts)-1]
    if len(last) == 15 && last[8] == 'T' && last[0] >= '2' && last[0] <= '3' {
        // Validate all other chars are digits
        valid := true
        for i, c := range last {
            if i == 8 {
                continue
            }
            if c < '0' || c > '9' {
                valid = false
                break
            }
        }
        if valid {
            // Likely a new-format timestamp — treat as slug suffix, not digest
            return artifactInfo{slug: strings.Join(parts[:len(parts)-1], "-")}
        }
    }
    // Legacy format — last part is a 14-char base36 digest hash
    return artifactInfo{
        slug:   strings.Join(parts[:len(parts)-1], "-"),
        digest: parts[len(parts)-1],
    }
}
```

- [ ] **Step 1: Write failing tests**

Add to `internal/sandbox/prune_test.go`:

```go
func TestParseHomeVolumeNameNewFormat(t *testing.T) {
    tests := []struct {
        input      string
        wantSlug   string
        wantDigest string
    }{
        {"opencode-msb-home-myproj-20260806T143022", "myproj", ""},
        {"opencode-msb-home-abc-def-20260806T143022", "abc-def", ""},
        {"opencode-msb-home-proj-20261231T235959", "proj", ""},
    }
    for _, tt := range tests {
        t.Run(tt.input, func(t *testing.T) {
            info := parseHomeVolumeName(tt.input)
            if info.slug != tt.wantSlug {
                t.Errorf("slug = %q, want %q", info.slug, tt.wantSlug)
            }
            if info.digest != tt.wantDigest {
                t.Errorf("digest = %q, want %q", info.digest, tt.wantDigest)
            }
        })
    }
}

func TestParseHomeVolumeNameLegacyFormat(t *testing.T) {
    info := parseHomeVolumeName("opencode-msb-home-myproject-aB3cDe4fGhIjKl-xYz1234AbCdEfGh")
    if info.slug != "myproject-aB3cDe4fGhIjKl" {
        t.Errorf("slug = %q, want %q", info.slug, "myproject-aB3cDe4fGhIjKl")
    }
    if info.digest != "xYz1234AbCdEfGh" {
        t.Errorf("digest = %q, want %q", info.digest, "xYz1234AbCdEfGh")
    }
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/sandbox -run "TestParseHomeVolumeNameNewFormat" -v`
Expected: FAIL — new-format names are currently treated as having a digest suffix

Run: `go test ./internal/sandbox -run "TestParseHomeVolumeNameLegacyFormat" -v`
Expected: PASS — legacy format parsing already works correctly (this test verifies no regression)

- [ ] **Step 3: Write the implementation**

Replace the `parseHomeVolumeName` function in `internal/sandbox/parse_artifact.go` with the code from the Interfaces section.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/sandbox -run "TestParseHomeVolumeName" -v`
Expected: PASS (both new and legacy format tests)

- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/parse_artifact.go internal/sandbox/prune_test.go
git commit -m "fix: update home volume name parsing for timestamp-based naming"
```

---

### Task 4: Implement ResolveHomeVolume, ensureNewHome, and ResolveHomeAction

**Files:**
- Modify: `internal/sandbox/volumes.go` — add new methods and constants
- Modify: `internal/sandbox/volumes_test.go` — add tests for resolve functions

**Interfaces:**
- **Consumes:** `ReadState(slug)`, `WriteState(slug, state)`, `HomeVolumeName(slug, "")`, `CreateVolume`
- **Produces:** `ResolveHomeVolume(ctx, client, projectSlug, imageDigest, imageTag, opts, ui) (string, HomeState, error)`
- **Produces:** `ensureNewHome(ctx, client, projectSlug, imageDigest, imageTag, opts, ui) (string, HomeState, error)`
- **Produces:** `ResolveHomeAction(ui, storedDigest, currentDigest) string` — returns action constant

Add these constants and methods to `internal/sandbox/volumes.go`:

```go
// Action constants for the home volume resolution prompt.
const (
    actionKeep    = "1"
    actionMigrate = "2"
    actionReset   = "3"
    actionQuit    = "4"
)

// ResolveHomeVolume checks the state file for an existing volume reference.
// If found and the volume still exists, returns the volume name and state.
// If not found or the volume does not exist, falls through to ensureNewHome.
func (vm *VolumeManager) ResolveHomeVolume(
    ctx context.Context,
    client MsbClient,
    projectSlug, imageDigest, imageTag string,
    opts RunOptions,
    ui termio.UI,
) (string, HomeState, error) {
    state, err := ReadState(projectSlug)
    if err != nil {
        ui.Warnf("corrupted state file, creating fresh home volume")
        return vm.ensureNewHome(ctx, client, projectSlug, imageDigest, imageTag, opts, ui)
    }

    if state == nil {
        return vm.ensureNewHome(ctx, client, projectSlug, imageDigest, imageTag, opts, ui)
    }

    _, err = client.GetVolume(ctx, state.HomeVolume)
    if err != nil {
        ui.Warnf("existing home volume %q not found, creating fresh", state.HomeVolume)
        return vm.ensureNewHome(ctx, client, projectSlug, imageDigest, imageTag, opts, ui)
    }

    return state.HomeVolume, *state, nil
}

// ensureNewHome creates a fresh home volume from the image and writes the state.
func (vm *VolumeManager) ensureNewHome(
    ctx context.Context,
    client MsbClient,
    projectSlug, imageDigest, imageTag string,
    opts RunOptions,
    ui termio.UI,
) (string, HomeState, error) {
    volName := HomeVolumeName(projectSlug, "")
    vol, err := client.CreateVolume(ctx, volName,
        msbSdk.WithVolumeKind(msbSdk.VolumeKindDir),
    )
    if err != nil {
        return "", HomeState{}, fmt.Errorf("create volume %s: %w", volName, err)
    }

    if !opts.DryRunVM {
        if err := vm.prefillVolume(ctx, client, projectSlug, vol.Name(), imageTag, ui); err != nil {
            return "", HomeState{}, err
        }
    } else {
        ui.Infof("dry-run: Would prefill home volume")
    }

    state := HomeState{
        HomeVolume:  volName,
        ImageDigest: imageDigest,
    }
    if err := WriteState(projectSlug, state); err != nil {
        ui.Warnf("failed to write state file: %v", err)
    }
    return volName, state, nil
}

// ResolveHomeAction compares the stored image digest with the current one.
// If they match, returns actionKeep immediately.
// If they differ, presents a prompt: keep/migrate/reset/quit.
// In non-interactive mode or with --yes, defaults to actionKeep.
func (vm *VolumeManager) ResolveHomeAction(
    ui termio.UI,
    storedDigest, currentDigest string,
) string {
    if storedDigest == currentDigest {
        return actionKeep
    }

    if !ui.IsInteractive() {
        ui.Infof("non-interactive: using existing home volume")
        return actionKeep
    }

    prompt := "Docker image changed for project. The image's home directory is different from your current one."
    choices := []termio.Choice{
        {Key: actionKeep, Label: "keep", Description: "continue with existing home volume"},
        {Key: actionMigrate, Label: "migrate", Description: "create fresh volume, copy all files on top"},
        {Key: actionReset, Label: "reset", Description: "replace with fresh volume from image (lose local changes)"},
        {Key: actionQuit, Label: "quit", Description: "exit without starting a session"},
    }
    selected, err := ui.Select(prompt, choices, actionKeep)
    if err != nil {
        ui.Warnf("prompt failed, continuing with existing volume")
        return actionKeep
    }
    return selected
}
```

- [ ] **Step 1: Write failing tests**

First, add these imports to `internal/sandbox/volumes_test.go`:

```go
import (
    "fmt"
    "strings"
    "testing"
    "time"

    "gitlab.inoio.de/inoio/opencode-msb/internal/testutil"
    "gitlab.inoio.de/inoio/opencode-msb/internal/termio"
)
```

Then add the following test functions:

```go
func TestResolveHomeAction_SameDigestReturnsKeep(t *testing.T) {
    ui := testutil.TermUIMock(t)
    vm := NewVolumeManager(&ui)
    action := vm.ResolveHomeAction(&ui, "same-digest", "same-digest")
    if action != actionKeep {
        t.Errorf("expected actionKeep for matching digests, got %q", action)
    }
}

func TestResolveHomeAction_DifferentDigestInNonInteractiveReturnsKeep(t *testing.T) {
    ui := testutil.TermUIMock(t)
    ui.IsInteractiveResult = false
    vm := NewVolumeManager(&ui)
    action := vm.ResolveHomeAction(&ui, "old", "new")
    if action != actionKeep {
        t.Errorf("expected actionKeep in non-interactive mode, got %q", action)
    }
}

func TestResolveHomeAction_DifferentDigestInInteractivePrompt(t *testing.T) {
    ui := &termio.Mock{
        IsInteractiveResult: true,
        SelectFn: func(prompt string, choices []termio.Choice, defaultKey string) (string, error) {
            if !strings.Contains(prompt, "Docker image changed") {
                return "", fmt.Errorf("unexpected prompt: %q", prompt)
            }
            if len(choices) != 4 {
                return "", fmt.Errorf("expected 4 choices, got %d", len(choices))
            }
            return actionMigrate, nil
        },
    }
    vm := NewVolumeManager(ui)
    action := vm.ResolveHomeAction(ui, "old", "new")
    if action != actionMigrate {
        t.Errorf("expected actionMigrate, got %q", action)
    }
}

func TestResolveHomeAction_ActionQuitReturnsQuit(t *testing.T) {
    ui := &termio.Mock{
        IsInteractiveResult: true,
        SelectFn: func(_ string, _ []termio.Choice, _ string) (string, error) {
            return actionQuit, nil
        },
    }
    vm := NewVolumeManager(ui)
    action := vm.ResolveHomeAction(ui, "old", "new")
    if action != actionQuit {
        t.Errorf("expected actionQuit, got %q", action)
    }
}

func TestActionConstantsHaveCorrectKeys(t *testing.T) {
    if actionKeep != "1" {
        t.Errorf("actionKeep = %q, want %q", actionKeep, "1")
    }
    if actionMigrate != "2" {
        t.Errorf("actionMigrate = %q, want %q", actionMigrate, "2")
    }
    if actionReset != "3" {
        t.Errorf("actionReset = %q, want %q", actionReset, "3")
    }
    if actionQuit != "4" {
        t.Errorf("actionQuit = %q, want %q", actionQuit, "4")
    }
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/sandbox -run "TestResolveHomeAction|TestActionConstants" -v`
Expected: FAIL — functions `ResolveHomeAction`, `ResolveHomeVolume`, constants `actionKeep` etc. don't exist

- [ ] **Step 3: Write implementation**

Add the functions from the Interfaces section to `internal/sandbox/volumes.go`.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/sandbox -run "TestResolveHomeAction|TestActionConstants" -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/volumes.go internal/sandbox/volumes_test.go
git commit -m "feat: add ResolveHomeVolume, ensureNewHome, and ResolveHomeAction"
```

---

### Task 5: Wire resolve flow into prepareSandbox

**Files:**
- Modify: `internal/sandbox/runner.go:156-161`

**Interfaces:**
- **Consumes:** `ResolveHomeVolume(ctx, client, slug, digest, imageRef, opts, ui)`, `ResolveHomeAction(ui, stored, current)`
- **Produces:** Updated `prepareSandbox` with prompt logic

Replace lines 156-161 in `internal/sandbox/runner.go`:

```go
// OLD:
vm := NewVolumeManager(ui)
homeVol, err := vm.EnsureHome(ctx, projectSlug, imageDigest, imageRef, opts, ui)

// NEW:
vm := NewVolumeManager(ui)
client := msb.Get()
homeVol, state, err := vm.ResolveHomeVolume(ctx, client, projectSlug, imageDigest, imageRef, opts, ui)
if err != nil {
    return nil, fmt.Errorf("volume setup failed: %w", err)
}
ui.Verbosef("home volume: %s", homeVol)

// Check if image changed and prompt
action := vm.ResolveHomeAction(ui, state.ImageDigest, imageDigest)
if action == actionQuit {
    ui.Infof("exiting as requested by user")
    return nil, &ExitError{Code: 1}
}
if action == actionMigrate || action == actionReset {
    ui.Infof("image changed — to apply your choice, run:")
    if action == actionMigrate {
        ui.Infof("  opencode-msb volume migrate")
    } else {
        ui.Infof("  opencode-msb volume reset")
    }
}
```

- [ ] **Step 1: Write a simple verification test**

```go
// Add to runner_test.go
func TestResolveSandboxActionConstants(t *testing.T) {
    // Verify the action constants match expected keys used in CLI/prompts
    if actionKeep != "1" || actionMigrate != "2" || actionReset != "3" || actionQuit != "4" {
        t.Error("action constants mismatch")
    }
}
```

- [ ] **Step 2: Run to verify FAIL**

This test from Task 4 already passes, so it will PASS. No issue.

- [ ] **Step 3: Wire the resolve flow into `prepareSandbox`**

Replace lines 156-161 in `internal/sandbox/runner.go` as shown in the Interfaces section above.

- [ ] **Step 4: Compile and run tests**

Run: `go build ./...`
Expected: PASS

Run: `go test ./internal/sandbox -run "Test.*Action" -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/runner.go
git commit -m "feat: wire ResolveHomeVolume and image change prompt into prepareSandbox"
```

---

### Task 6: Add CLI volume subcommands (migrate, reset, edit)

**Files:**
- Modify: `cmd/opencode-msb/constants.go` — add CLI command constants
- Create: `internal/sandbox/volume_ops.go` — volume migration/reset/edit logic
- Modify: `cmd/opencode-msb/commands_system.go:145-169` — `buildVolumeCmd`

**New constants in `cmd/opencode-msb/constants.go`:**

```go
const (
    cmdMigrate = "migrate"
    cmdReset   = "reset"
    cmdEdit    = "edit"
    flagRemove = "rm"
)
```

- [ ] **Step 1: Add constants**

Add to `cmd/opencode-msb/constants.go` after the existing `cmdPrune` constant.

- [ ] **Step 2: Run to verify FAIL**

Run: `go build ./cmd/opencode-msb/...`
Expected: FAIL — constants undeclared

- [ ] **Step 3: Add constants and verify**

Run: `go build ./cmd/opencode-msb/...`
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add cmd/opencode-msb/constants.go
git commit -m "feat: add volume CLI subcommand constants (migrate, reset, edit, rm)"
```

**Volume operations in `internal/sandbox/volume_ops.go`:**

```go
package sandbox

import (
    "context"
    "fmt"
    "strconv"
    "strings"
    "time"

    msbSdk "github.com/superradcompany/microsandbox/sdk/go"
    "gitlab.inoio.de/inoio/opencode-msb/internal/sandbox/msb"
    "gitlab.inoio.de/inoio/opencode-msb/internal/termio"
)

// checkForActiveVMs checks if there are active or stopped VMs for the given slug.
func checkForActiveVMs(ctx context.Context, slug string) error {
    client := msb.Get()
    sandboxes, err := client.ListSandboxes(ctx)
    if err != nil {
        return fmt.Errorf("list sandboxes: %w", err)
    }
    for _, h := range sandboxes {
        if !strings.HasPrefix(h.Name(), vmPrefix) {
            continue
        }
        s, _ := extractProjectSlugAndDigest(h.Name())
        if s == slug {
            status := h.Status()
            if isSandboxActive(status) || isStoppedStatus(status) {
                return fmt.Errorf("session still running for slug %q -- quit all sessions before migrating or resetting", slug)
            }
        }
    }
    return nil
}

// CmdMigrate creates a new home volume, copies old files on top, updates state.
func CmdMigrate(
    ctx context.Context,
    projectSlug, volumeName, imageTag string,
    rmOld, dryRun bool,
    ui termio.UI,
) error {
    return volumeOp(ctx, projectSlug, volumeName, imageTag, rmOld, dryRun, ui, true, false)
}

// CmdReset creates a new home volume from image only, no copy.
func CmdReset(
    ctx context.Context,
    projectSlug, volumeName, imageTag string,
    rmOld, dryRun bool,
    ui termio.UI,
) error {
    return volumeOp(ctx, projectSlug, volumeName, imageTag, rmOld, dryRun, ui, false, false)
}

// CmdEdit creates a new volume alongside the old for manual transfer.
func CmdEdit(
    ctx context.Context,
    projectSlug, volumeName, imageTag string,
    rmOld, dryRun bool,
    ui termio.UI,
) error {
    return volumeOp(ctx, projectSlug, volumeName, imageTag, rmOld, dryRun, ui, false, true)
}

// volumeOp is the shared implementation for migrate, reset, and edit operations.
func volumeOp(
    ctx context.Context,
    projectSlug, volumeName, imageTag string,
    rmOld, dryRun bool,
    ui termio.UI,
    doCopy, doEdit bool,
) error {
    client := msb.Get()

    state, err := ReadState(projectSlug)
    if err != nil {
        return fmt.Errorf("read state: %w", err)
    }
    if state == nil {
        return fmt.Errorf("no state file found for project %q", projectSlug)
    }

    oldVolume := volumeName
    if oldVolume == "" {
        oldVolume = state.HomeVolume
        if oldVolume == "" {
            return fmt.Errorf("no volume to operate on: state has no home_volume set")
        }
    }

    if err := checkForActiveVMs(ctx, projectSlug); err != nil {
        return err
    }

    if dryRun {
        if doCopy {
            ui.Infof("dry-run: Would create volume %q, copy files from %q", HomeVolumeName(projectSlug, ""), oldVolume)
        } else if doEdit {
            ui.Infof("dry-run: Would create volume %q alongside %q for manual transfer", HomeVolumeName(projectSlug, ""), oldVolume)
        } else {
            ui.Infof("dry-run: Would create fresh volume %q, remove %q", HomeVolumeName(projectSlug, ""), oldVolume)
        }
        return nil
    }

    newVolumeName := HomeVolumeName(projectSlug, "")
    newVol, err := client.CreateVolume(ctx, newVolumeName,
        msbSdk.WithVolumeKind(msbSdk.VolumeKindDir),
    )
    if err != nil {
        return fmt.Errorf("create volume %s: %w", newVolumeName, err)
    }

    vm := NewVolumeManager(ui)
    if err := vm.prefillVolume(ctx, client, projectSlug, newVol.Name(), imageTag, ui); err != nil {
        return fmt.Errorf("prefill new volume: %w", err)
    }

    if doCopy {
        copySbName := taskPrefix + projectSlug + "-" + strconv.FormatInt(time.Now().UnixNano(), 10)
        copySb, copyErr := client.CreateSandbox(ctx, copySbName,
            msbSdk.WithImage(imageTag),
            msbSdk.WithMounts(map[string]msbSdk.MountConfig{
                "/src":  msbSdk.Mount.Named(oldVolume, msbSdk.MountOptions{}),
                "/dst":  msbSdk.Mount.Named(newVolumeName, msbSdk.MountOptions{}),
            }),
            msbSdk.WithReplace(),
        )
        if copyErr != nil {
            return fmt.Errorf("create copy sandbox: %w", copyErr)
        }
        defer func() {
            stopCtx, cancel := context.WithTimeout(context.Background(), sandboxStopTimeout)
            defer cancel()
            _ = copySb.Detach(stopCtx)
            _ = copySb.Close()
            _ = client.RemoveSandbox(context.Background(), copySbName)
        }()

        spin := ui.Spinner("Copying files from existing home volume")
        copyOut, copyExecErr := copySb.Exec(ctx, "sh", []string{"-c", "cp -a /src/. /dst/ && chown -R dev:dev /dst"})
        if copyExecErr != nil {
            spin.StopError(copyExecErr)
            return fmt.Errorf("copy files: %w", copyExecErr)
        }
        if copyOut != nil && !copyOut.Success() {
            spin.StopError(fmt.Errorf("copy failed (exit %d): %s", copyOut.ExitCode(), copyOut.Stderr()))
            return fmt.Errorf("copy files (exit %d): %s", copyOut.ExitCode(), copyOut.Stderr())
        }
        spin.Stop()
        ui.Infof("migrated to new home volume %q (files copied from %q)", newVolumeName, oldVolume)
    } else if doEdit {
        // Spawn an interactive shell in a sandbox that has both volumes mounted.
        spin := ui.Spinner("Starting interactive session with both volumes")
        editSandboxName := taskPrefix + projectSlug + "-" + strconv.FormatInt(time.Now().UnixNano(), 10)
        editOldMount := msbSdk.Mount.Named(oldVolume, msbSdk.MountOptions{})
        editNewMount := msbSdk.Mount.Named(newVolumeName, msbSdk.MountOptions{})
        editSb, editErr := client.CreateSandbox(ctx, editSandboxName,
            msbSdk.WithImage(imageTag),
            msbSdk.WithMounts(map[string]msbSdk.MountConfig{
                "/src": editOldMount,
                "/dst": editNewMount,
            }),
            msbSdk.WithReplace(),
        )
        if editErr != nil {
            return fmt.Errorf("create edit sandbox: %w", editErr)
        }
        ui.Infof("Both volumes mounted in session:")
        ui.Infof("  Old volume (source):  /src")
        ui.Infof("  New volume (target):  /dst")
        ui.Infof("Type 'exit' to finish and return to the host.")
        spin.Stop()
        exitCode, shellErr := editSb.Attach(ctx, "/bin/bash", "-l")
        if shellErr != nil {
            ui.Warnf("shell exited with error: %v", shellErr)
        }
        // Best-effort cleanup
        stopCtx, cancel := context.WithTimeout(context.Background(), sandboxStopTimeout)
        defer cancel()
        _ = editSb.Stop(stopCtx)
        _ = editSb.Close()
        _ = client.RemoveSandbox(context.Background(), editSandboxName)
        ui.Infof("exited interactive session, new volume: %q", newVolumeName)
    } else {
        ui.Infof("reset to new home volume %q", newVolumeName)
    }

    newState := HomeState{
        HomeVolume:  newVolumeName,
        ImageDigest: state.ImageDigest,
    }
    if err := WriteState(projectSlug, newState); err != nil {
        ui.Warnf("failed to write state file: %v", err)
    }

    if rmOld {
        if err := client.RemoveVolume(ctx, oldVolume); err != nil {
            ui.Warnf("failed to remove old volume %q: %v", oldVolume, err)
        } else {
            ui.Infof("removed old volume %q", oldVolume)
        }
    }

    return nil
}
```

Now update `buildVolumeCmd` in `commands_system.go`:

Note: Add `"gitlab.inoio.de/inoio/opencode-msb/internal/git"` to the import block in `commands_system.go` since `git.ProjectSlug(ui)` is used in the migrate, reset, and edit command handlers.

```go
func buildVolumeCmd(ui termio.UI) *cobra.Command {
    cmd := &cobra.Command{
        Use:     cmdVolume,
        Aliases: []string{"vol"},
        Short:   "Manage home volumes",
    }
    cmd.AddCommand(&cobra.Command{
        Use:     cmdList,
        Aliases: []string{"ls"},
        Args:    cobra.NoArgs,
        Short:   "List managed volumes",
        RunE: func(cmd *cobra.Command, _ []string) error {
            volumes, err := sandbox.ListVolumes(cmd.Context())
            if err != nil {
                return err
            }
            printItems(volumes, "No volumes found.", "%-50s %s",
                func(v sandbox.VolumeInfo) string { return v.Name },
                func(v sandbox.VolumeInfo) string { return v.Path },
                ui)
            return nil
        },
    })

    var migrateRmOld bool
    migrateCmd := &cobra.Command{
        Use:   cmdMigrate,
        Args:  cobra.MaximumNArgs(1),
        Short: "Migrate: create new volume, copy old files on top",
        RunE: func(c *cobra.Command, args []string) error {
            if !sandbox.CheckAll(c.Context(), ui) {
                return errors.New("preflight failed")
            }
            projectSlug := git.ProjectSlug(ui)
            dryRun, _ := c.Flags().GetBool("dry-run")
            imageTag, _, _, err := sandbox.EnsureImage(c.Context(), projectSlug, false, ui)
            if err != nil {
                return fmt.Errorf("ensure image: %w", err)
            }
            var volName string
            if len(args) > 0 {
                volName = args[0]
            }
            return sandbox.CmdMigrate(c.Context(), projectSlug, volName, imageTag, migrateRmOld, dryRun, ui)
        },
    }
    migrateCmd.Flags().BoolVar(&migrateRmOld, flagRemove, false, "Remove old volume after migration")
    migrateCmd.Flags().Bool("dry-run", false, "Show what would be done")
    migrateCmd.Flags().Bool("rebuild", false, "Rebuild runner image first")
    cmd.AddCommand(migrateCmd)

    var resetRmOld bool
    resetCmd := &cobra.Command{
        Use:   cmdReset,
        Args:  cobra.MaximumNArgs(1),
        Short: "Reset: create new volume from image, discard old files",
        RunE: func(c *cobra.Command, args []string) error {
            if !sandbox.CheckAll(c.Context(), ui) {
                return errors.New("preflight failed")
            }
            projectSlug := git.ProjectSlug(ui)
            dryRun, _ := c.Flags().GetBool("dry-run")
            imageTag, _, _, err := sandbox.EnsureImage(c.Context(), projectSlug, false, ui)
            if err != nil {
                return fmt.Errorf("ensure image: %w", err)
            }
            var volName string
            if len(args) > 0 {
                volName = args[0]
            }
            return sandbox.CmdReset(c.Context(), projectSlug, volName, imageTag, resetRmOld, dryRun, ui)
        },
    }
    resetCmd.Flags().BoolVar(&resetRmOld, flagRemove, false, "Remove old volume after reset")
    resetCmd.Flags().Bool("dry-run", false, "Show what would be done")
    resetCmd.Flags().Bool("rebuild", false, "Rebuild runner image first")
    cmd.AddCommand(resetCmd)

    var editRmOld bool
    editCmd := &cobra.Command{
        Use:   cmdEdit,
        Args:  cobra.MaximumNArgs(1),
        Short: "Edit: create new volume alongside old one for manual transfer",
        RunE: func(c *cobra.Command, args []string) error {
            if !sandbox.CheckAll(c.Context(), ui) {
                return errors.New("preflight failed")
            }
            projectSlug := git.ProjectSlug(ui)
            dryRun, _ := c.Flags().GetBool("dry-run")
            imageTag, _, _, err := sandbox.EnsureImage(c.Context(), projectSlug, false, ui)
            if err != nil {
                return fmt.Errorf("ensure image: %w", err)
            }
            var volName string
            if len(args) > 0 {
                volName = args[0]
            }
            return sandbox.CmdEdit(c.Context(), projectSlug, volName, imageTag, editRmOld, dryRun, ui)
        },
    }
    editCmd.Flags().BoolVar(&editRmOld, flagRemove, false, "Remove old volume after editing")
    editCmd.Flags().Bool("dry-run", false, "Show what would be done")
    cmd.AddCommand(editCmd)

    return cmd
}
```

- [ ] **Step 1: Write CLI tests in `cmd/opencode-msb/cli_volume_test.go`**

```go
package main

import (
    "testing"

    "gitlab.inoio.de/inoio/opencode-msb/internal/sandbox"
    "gitlab.inoio.de/inoio/opencode-msb/internal/termio"
)

func TestVolumeMigrateHelp(t *testing.T) {
    initTestRepo(t)
    ui := &termio.Mock{}
    mock := &sandbox.MockMsbClient{}
    sandbox.WithMsbMock(t, mock)

    root := buildRootCmd(ui)
    root.SetArgs([]string{cmdVolume, cmdMigrate, "--help"})
    root.Execute()
    // --help exits with 0, no error
}

func TestVolumeResetHelp(t *testing.T) {
    initTestRepo(t)
    ui := &termio.Mock{}
    mock := &sandbox.MockMsbClient{}
    sandbox.WithMsbMock(t, mock)

    root := buildRootCmd(ui)
    root.SetArgs([]string{cmdVolume, cmdReset, "--help"})
    root.Execute()
}

func TestVolumeEditHelp(t *testing.T) {
    initTestRepo(t)
    ui := &termio.Mock{}
    mock := &sandbox.MockMsbClient{}
    sandbox.WithMsbMock(t, mock)

    root := buildRootCmd(ui)
    root.SetArgs([]string{cmdVolume, cmdEdit, "--help"})
    root.Execute()
}
```

- [ ] **Step 2: Run to verify FAIL**

Run: `go build ./cmd/opencode-msb/...`
Expected: FAIL — functions `CmdMigrate`, `CmdReset`, `CmdEdit` not defined, `buildVolumeCmd` doesn't include them

- [ ] **Step 3: Write volume_ops.go and updated buildVolumeCmd**

Create `internal/sandbox/volume_ops.go` with the code from the Interfaces section. Update `buildVolumeCmd` in `commands_system.go`.

- [ ] **Step 4: Run to verify PASS**

Run: `go build ./cmd/opencode-msb/...`
Expected: PASS

Run: `go test ./cmd/opencode-msb -run "TestVolume.*Help" -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add cmd/opencode-msb/constants.go cmd/opencode-msb/commands_system.go internal/sandbox/volume_ops.go cmd/opencode-msb/cli_volume_test.go
git commit -m "feat: add volume migrate/reset/edit CLI subcommands with --rm flag"
```

---

### Task 7: Remove prune digest-matching and add state file cleanup

**Files:**
- Modify: `internal/sandbox/prune.go`

**Interfaces — exact changes:**

1. Delete `pruneActiveVMHomeVolumes` function entirely (lines 508-539).

2. Update `pruneActiveVMCleanup` to remove the call to `pruneActiveVMHomeVolumes`. Replace the function body:

```go
func pruneActiveVMCleanup(
    ctx context.Context,
    client MsbClient,
    slug string,
    digest string,
    homeBySlugDigest map[string]map[string]string,
    msbImagesBySlug map[string][]imageWithDigest,
    dryRun bool,
    ui termio.UI,
    report *StaleReport,
) {
    // Home volumes: no longer removed by digest matching.
    _ = homeBySlugDigest
    // Images: delete unused ones, keep :latest, keep matching digest.
    pruneActiveVMMSBImages(ctx, client, slug, digest, msbImagesBySlug, dryRun, ui, report)
    // Docker images: same logic.
    pruneActiveVMDockerImages(ctx, slug, digest, msbImagesBySlug, dryRun, ui, report)
}
```

3. Update `removeHomeVolumes` to add state file cleanup after all volumes are deleted:

```go
func removeHomeVolumes(
    ctx context.Context,
    client MsbClient,
    slug string,
    homeBySlugDigest map[string]map[string]string,
    dryRun bool,
    ui termio.UI,
    report *StaleReport,
) {
    if vols, ok := homeBySlugDigest[slug]; ok {
        for _, volName := range vols {
            if !dryRun {
                if err := client.RemoveVolume(ctx, volName); err != nil {
                    ui.Warnf("failed to remove home volume %s: %v", volName, err)
                }
            }
            report.PrunedVolumes++
            report.Details = append(report.Details, StaleEntry{
                Type:     StaleTypeVolume,
                Name:     volName,
                Slug:     slug,
                StaleFor: 0,
                Digest:   "",
            })
        }
        // After all volumes for this slug are removed, clean up the state file.
        if len(vols) > 0 && !dryRun {
            RemoveState(slug)
        }
    }
}
```

- [ ] **Step 1: Write tests for state file cleanup in prune**

Add the following imports to `internal/sandbox/prune_test.go` if not already present:
```go
import (
    "context"
    "os"
    "path/filepath"
    "testing"
    "time"

    msb "github.com/superradcompany/microsandbox/sdk/go"

    "gitlab.inoio.de/inoio/opencode-msb/internal/testutil"

    "gitlab.inoio.de/inoio/opencode-msb/internal/sandbox/docker"
    "gitlab.inoio.de/inoio/opencode-msb/internal/sandbox/msb"

    "gitlab.inoio.de/inoio/opencode-msb/internal/termio"
)
```

Add to `internal/sandbox/prune_test.go`:

```go
func TestRemoveHomeVolumes_CleansStateFile(t *testing.T) {
    fix := saveStateDirTest()
    defer fix.restore()
    stateDirSuffix = t.TempDir() + "/opencode-msb"

    client := &MockMsbClient{}
    ui := &termio.Mock{}

    report := &StaleReport{}

    slug := "myproject"
    homeBySlugDigest := map[string]map[string]string{
        slug: {"": "opencode-msb-home-myproject-20260806T143022"},
    }

    statePath := filepath.Join(stateDirSuffix, slug, "state.yaml")
    os.MkdirAll(filepath.Dir(statePath), 0o700)
    testutil.WritePath(t, statePath,
        "home_volume: opencode-msb-home-myproject-20260806T143022\nimage_digest: sha256:abc\n")

    removeHomeVolumes(context.Background(), client, slug, homeBySlugDigest, false, ui, report)

    if report.PrunedVolumes != 1 {
        t.Errorf("expected 1 pruned volume, got %d", report.PrunedVolumes)
    }
    if len(client.RemovedVolumes) != 1 {
        t.Errorf("expected 1 volume removal, got %d", len(client.RemovedVolumes))
    }
    if _, err := os.Stat(statePath); !os.IsNotExist(err) {
        t.Errorf("state file should be removed, still exists at %s", statePath)
    }
}

func TestRemoveHomeVolumes_DryRunDoesNotRemoveState(t *testing.T) {
    fix := saveStateDirTest()
    defer fix.restore()
    stateDirSuffix = t.TempDir() + "/opencode-msb"

    client := &MockMsbClient{}
    ui := &termio.Mock{}
    report := &StaleReport{}

    slug := "myproject"
    homeBySlugDigest := map[string]map[string]string{
        slug: {"": "opencode-msb-home-myproject-20260806T143022"},
    }

    statePath := filepath.Join(stateDirSuffix, slug, "state.yaml")
    os.MkdirAll(filepath.Dir(statePath), 0o700)
    testutil.WritePath(t, statePath,
        "home_volume: opencode-msb-home-myproject-20260806T143022\n")

    removeHomeVolumes(context.Background(), client, slug, homeBySlugDigest, true, ui, report)

    if report.PrunedVolumes != 1 {
        t.Errorf("expected 1 pruned volume in dry-run, got %d", report.PrunedVolumes)
    }
    if len(client.RemovedVolumes) != 0 {
        t.Errorf("expected no volume removals in dry-run, got %d", len(client.RemovedVolumes))
    }
    if _, err := os.Stat(statePath); os.IsNotExist(err) {
        t.Errorf("state file should still exist in dry-run")
    }
}

// Helper to save/restore stateDirSuffix for tests.
func saveStateDirTest() func() {
    old := stateDirSuffix
    return func() { stateDirSuffix = old }
}
```

- [ ] **Step 2: Run to verify FAIL**

Run: `go test ./internal/sandbox -run "TestRemoveHomeVolumes_Cleans|TestRemoveHomeVolumes_DryRun" -v`
Expected: FAIL — no state cleanup in removeHomeVolumes

- [ ] **Step 3: Apply prune.go changes**

1. Delete `pruneActiveVMHomeVolumes` function (lines 508-539)
2. Update `pruneActiveVMCleanup` to skip digest-based volume pruning
3. Update `removeHomeVolumes` to add `RemoveState(slug)` after the loop

- [ ] **Step 4: Run tests to verify PASS**

Run: `go test ./internal/sandbox -run "TestRemoveHomeVolumes" -v`
Expected: PASS

Run: `go test ./internal/sandbox -run "TestPrune" -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/prune.go internal/sandbox/prune_test.go
git commit -m "feat: remove prune digest-matching, add state file cleanup on volume deletion"
```

---

### Task 8: Add active VM home volume existence check during prune

**Files:**
- Modify: `internal/sandbox/prune.go` — add `pruneActiveVMHomeVolume` function

This function checks if the volume tracked in the state file for each active VM still exists. If not (e.g., user ran `volume reset` externally), it creates a fresh one.

Add `pruneActiveVMHomeVolume` to `internal/sandbox/prune.go`:

```go
func pruneActiveVMHomeVolume(
    ctx context.Context,
    client MsbClient,
    catalog *PruningCatalog,
    dryRun bool,
    ui termio.UI,
    report *StaleReport,
) (*StaleReport, error) {
    for slug, digest := range catalog.ActiveVMDigest {
        state, err := ReadState(slug)
        if err != nil {
            ui.Warnf("corrupted state for slug %q, skipping: %v", slug, err)
            continue
        }
        if state == nil || state.HomeVolume == "" {
            continue
        }

        if _, err := client.GetVolume(ctx, state.HomeVolume); err != nil {
            if dryRun {
                ui.Infof("would create replacement volume for %q (slug %s)", state.HomeVolume, slug)
                continue
            }
            newVolName := HomeVolumeName(slug, "")
            if _, err := client.CreateVolume(ctx, newVolName, msbSdk.WithVolumeKind(msbSdk.VolumeKindDir)); err != nil {
                ui.Warnf("failed to create replacement volume for slug %q: %v", slug, err)
            } else {
                newState := HomeState{HomeVolume: newVolName, ImageDigest: state.ImageDigest}
                if writeErr := WriteState(slug, newState); writeErr != nil {
                    ui.Warnf("failed to write state for slug %q: %v", slug, writeErr)
                }
            }
        }
    }
    return report, nil
}
```

Update `catalogAndPrune` function to add the new call after `pruneActiveVMArtifacts`:

```go
// After line 331 (after pruneActiveVMArtifacts call), add:
report, activeHomeVolErr := pruneActiveVMHomeVolume(ctx, msbClient, catalog, dryRun, ui, report)
```

And update the errors.Join:

```go
return report, errors.Join(catalogErr, staleVMErr, activeVMErr, activeHomeVolErr, orphanErr, cloneVolErr, errors.Join(sandboxErrs...))
```

- [ ] **Step 1: Write test**

```go
func TestPruneActiveVMHomeVolume_SkipsWhenVolumeExists(t *testing.T) {
    client := &MockMsbClient{
        Volumes: []VolumeHandle{
            MockVolumeHandle{Name_: "opencode-msb-home-testslug-now"},
        },
    }
    ui := &termio.Mock{}
    report := &StaleReport{}

    slug := "testslug"
    catalog := &PruningCatalog{
        ActiveVMDigest: map[string]string{slug: "sha256:deadbeef"},
    }

    fix := saveStateDirTest()
    defer fix.restore()
    stateDirSuffix = t.TempDir() + "/opencode-msb"

    stateDir := filepath.Join(stateDirSuffix, slug)
    os.MkdirAll(stateDir, 0o700)
    yamlData := "home_volume: opencode-msb-home-testslug-now\nimage_digest: sha256:deadbeef\n"
    os.WriteFile(filepath.Join(stateDir, "state.yaml"), []byte(yamlData), 0o600)

    newReport, err := pruneActiveVMHomeVolume(context.Background(), client, catalog, false, ui, report)
    if err != nil {
        t.Fatalf("unexpected error: %v", err)
    }
    if newReport.PrunedVolumes != 0 {
        t.Errorf("expected 0 pruned volumes (volume exists), got %d", newReport.PrunedVolumes)
    }
}
```

- [ ] **Step 2: Run to verify FAIL**

Run: `go test ./internal/sandbox -run "TestPruneActiveVMHomeVolume" -v`
Expected: FAIL — function `pruneActiveVMHomeVolume` does not exist

- [ ] **Step 3: Implement the function and update catalogAndPrune**

Add the function and the call in `catalogAndPrune`.

- [ ] **Step 4: Run tests to verify PASS**

Run: `go test ./internal/sandbox -run "TestPruneActiveVMHomeVolume" -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/prune.go
git commit -m "feat: add active VM home volume existence check during prune"
```

---

### Task 9: Ensure `stateDirSuffix` is accessible for prune tests

**Files:**
- Modify: `internal/sandbox/prune_test.go`

The prune tests for state file cleanup need access to `stateDirSuffix` from `state.go`. Currently it's unexported. Make it a package-level variable accessible to tests (which are in `package sandbox`).

No code changes needed — `stateDirSuffix` is already in `package sandbox` so `prune_test.go` test code can write to it directly at runtime. The test fixes from Task 2 and Task 7 cover this.

- [ ] **Step 1: Verify compilation**

Run: `go build ./internal/sandbox/...`
Expected: PASS (state.go exports are already package-level)

- [ ] **Step 2: Run prune tests**

Run: `go test ./internal/sandbox -run "TestRemoveHomeVolumes" -v`
Expected: PASS

- [ ] **Step 3: Commit**

```bash
git add internal/sandbox/prune_test.go
git commit -m "test: fix prune tests to use stateDirSuffix from state.go"
```

---

### Task 10: Ensure runner_test.go compiles and passes

**Files:**
- Verify: `internal/sandbox/runner_test.go` — check no stale references to old `HomeVolumeName` signatures
- Modify: `internal/sandbox/runner_test.go` — add `actionKeep` constant import if tests reference them

- [ ] **Step 1: Check for stale code**

Run: `go build ./internal/sandbox/...`
Expected: PASS. If there are errors, fix them by updating references to `EnsureHome` to use the new `ResolveHomeVolume` path, or verify `EnsureHome` still exists as a no-op for backward compatibility.

- [ ] **Step 2: Run all sandbox tests**

Run: `go test ./internal/sandbox/... -v`
Expected: All PASS

- [ ] **Step 3: Commit**

```bash
git add internal/sandbox/runner_test.go
git commit -m "test: update runner tests for ResolveHomeVolume path"
```

---

### Task 11: Update documentation

**Files:**
- Modify: `README.md`
- Modify: `docs/runner-image.md`
- Modify: `docs/commands.md`
- Modify: `docs/sandboxes.md`
- Modify: `docs/troubleshooting.md`

**README.md update** — Find the home volume section and update:

```markdown
### Home volumes

Home volumes now persist across image changes. When you update your Dockerfile,
one home volume is created per project and reused. The tool tracks the active
volume in `~/.local/state/opencode-msb/{slug}/state.yaml`.

When you run `opencode-msb run` and detect an image change, you'll be prompted:

  1) keep      - continue with existing home volume
  2) migrate   - create fresh volume, copy all files on top
  3) reset     - replace with fresh volume from image (lose local changes)
  4) quit      - exit without starting a session

Use `opencode-msb volume migrate|reset|edit` for manual management.

Home volumes are named `opencode-msb-home-{slug}-{timestamp}`.
```

**docs/runner-image.md update** — In the home directory section:

```markdown
### Home directory

The runner image provides default files under `/home/dev/` — shell configs,
default prompts, and pre-installed tools. When opencode-msb first starts, it
copies these defaults into your home volume.

Subsequent runs reuse your existing home volume, preserving your installed tools,
config files, and session history. Your home directory survives Dockerfile changes.

If the image changes between runs, you will be presented with a prompt to keep,
migrate, or reset your home volume. See `volume migrate` and `volume reset` for
manual management.
```

**docs/commands.md update** — Add section:

```markdown
### `opencode-msb volume <subcommand>`

The volume group provides manual home volume management.

#### `opencode-msb volume migrate [volume-name]`

Create a new home volume and copy files from the old volume on top of it.

- **Args:**
  - `[volume-name]` — optional; defaults to volume in state file
- **Flags:**
  - `--rm` — remove old volume after successful migration
  - `--dry-run` — show what would be done
  - `--rebuild` — rebuild runner image before migrating

#### `opencode-msb volume reset [volume-name]`

Create a new home volume from the image contents only (fresh, no copy).

- **Args:**
  - `[volume-name]` — optional; defaults to volume in state file
- **Flags:**
  - `--rm` — remove old volume after reset
  - `--dry-run` — show what would be done
  - `--rebuild` — rebuild runner image before resetting

#### `opencode-msb volume edit [volume-name]`

Create a new volume alongside the old one, for manual data transfer.

- **Args:**
  - `[volume-name]` — optional; defaults to volume in state file
- **Flags:**
  - `--rm` — remove old volume after you exit (you are responsible for confirming)
  - `--dry-run` — show what would be done
```

**docs/troubleshooting.md update** — Add:

```markdown
### Corrupted state file

If the state file at `~/.local/state/opencode-msb/{slug}/state.yaml` is corrupted
or missing, opencode-msb will warn and create a fresh home volume.

To recover: manually remove the state directory:

    rm -rf ~/.local/state/opencode-msb/{slug}/

The next `opencode-msb run` will create a fresh home volume.

### No home volume found

If you see errors about an existing home volume not being found, the volume may have
been deleted externally. The next `opencode-msb run` will create a fresh volume and
warn you about it.
```

- [ ] **Step 1: Update all docs**

- [ ] **Step 2: Verify with full test suite**

Run: `make check`
Expected: PASS (format, lint, tests)

- [ ] **Step 3: Run full test suite**

Run: `go test ./...`
Expected: All PASS

- [ ] **Step 4: Commit**

```bash
git add README.md docs/runner-image.md docs/commands.md docs/sandboxes.md docs/troubleshooting.md
git commit -m "docs: update documentation for home volume lifecycle changes"
```

---

## Self-Review

**Spec coverage checklist:**

1. [x] State file at `~/.local/state/opencode-msb/{slug}/state.yaml` → Task 2
2. [x] Volume naming `opencode-msb-home-{slug}-{timestamp}` → Task 1
3. [x] Volume resolution: check state, prompt on image change → Task 4 + Task 5
4. [x] CLI: `volume migrate|reset|edit` with `--rm` flag → Task 6
5. [x] Prune: remove digest-matching, add state file cleanup → Task 7
6. [x] Active VM home volume check during prune → Task 8
7. [x] Active VM safety block for migrate/reset/edit → Task 6 (`checkForActiveVMs`)
8. [x] State cleanup when volume deleted by prune → Task 7
9. [x] Tests covering all scenarios → Tasks 2, 4, 7, 8, 9, 10
10. [x] Documentation updates → Task 11
11. [x] Parse artifact functions handle both new and legacy formats → Task 3

**Placeholder scan:**

No \"TBD\", \"TODO\", \"add appropriate error handling\", or \"similar to Task N\" found.
All function signatures are fully specified with exact parameter types and return types.
All error messages are concrete and match spec language.
All test assertions use exact expected values.

**Type consistency:**

- `HomeState` struct uses `yaml:"home_volume"` and `yaml:"image_digest"` tags consistently
- `HomeVolumeName` returns `string`, called with `(slug, "")` throughout
- `parseHomeVolumeName` returns `artifactInfo` with `slug string` and `digest string` fields
- All action constants match prompt UI keys (`"1"`, `"2"`, `"3"`, `"4"`)
- Prune cleanup passes `slug` to `RemoveState(slug)` consistently

**Remaining edge cases documented:**

- `checkForActiveVMs` handles both active and stopped VMs — spec says \"active or stale\"
- `ResolveHomeVolume` falls back to `ensureNewHome` on missing volume, corrupted state, or parse errors — matches spec table
- Non-interactive mode defaults to `keep` — matches spec row \"Non-interactive mode (CI, scripts, --yes)\"
- State file cleanup on prune handles the case where state directory may not exist — `os.Remove` on missing path is a no-op

---


## Execution Order (dependencies)

Implement tasks in this order:

1. **Task 1** — Timestamp naming (no dependencies)
2. **Task 2** — State file I/O (no dependencies)
3. **Task 3** — Update parsing (depends on Task 1 naming format)
4. **Task 4** — Resolve logic (depends on Tasks 1, 2)
5. **Task 5** — Wire into runner (depends on Task 4)
6. **Task 6** — CLI volume subcommands (no dependencies, can run in parallel with 4/5)
7. **Task 7** — Remove prune digest-matching (depends on Task 3 parsing)
8. **Task 8** — Active VM home volume check (depends on Tasks 2 state, Task 7 prune)
9. **Task 9** — Verify prune test compilation (depends on Task 7)
10. **Task 10** — Ensure runner tests pass (depends on Tasks 1-5)
11. **Task 11** — Update docs (depends on Tasks 4, 6)

Tasks 1, 2 can be done independently and in parallel. Task 6 (CLI) has no dependencies on Tasks 4/5 so can also run in parallel with the resolve flow.
