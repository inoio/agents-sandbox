# Decoupled Per-Type Pruning + Artifact Prune Subcommands — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the monolithic `pruning` pipeline with three independent per-artifact pruners backed by a shared `LiveState` snapshot, and expose `image prune`, `volume prune`, and `sandbox prune` subcommands that map 1:1 onto them.

**Architecture:** A small `LiveState` snapshot (which project slugs have a surviving VM) is built once; `PruneVMs`/`PruneVolumes`/`PruneImages` each list their own artifacts and apply their own keep-set predicate. The aggregate `prune` composes all three and merges typed reports into a reduced `StaleReport`.

**Tech Stack:** Go, cobra, spf13/viper, microsandbox SDK (via the `msb` wrapper), moby docker client.

**Spec:** `docs/superpowers/specs/image-prune-subcommand.md`

## Global Constraints

- Target platforms: Linux (KVM) and macOS (Apple Silicon).
- Run the linter (`golangci-lint run`) after each edit; run `make check` when finalizing.
- Use `make fmt`/`golangci-lint fmt` for formatting — never `go fmt`.
- TDD: write the failing test first, then implement.
- No inline comments unless the code cannot be self-explanatory.
- Image, VM, and volume prune-age defaults are identical: `manual-prune-age` (7d) for manual prunes, 30d `auto-prune-age` for auto-prune; `--age` overrides the manual default.
- Clone-volume code is dead and must be removed (no create site).

---

### Task 1: Introduce the `LiveState` snapshot

**Files:**
- Create: `internal/sandbox/pruning/snapshot.go`
- Test: `internal/sandbox/pruning/snapshot_test.go`

**Interfaces:**
- Consumes: `msb.Client` (has `ListSandboxes(ctx) ([]SandboxHandle, error)`), `msb.SandboxHandle` (`Name()`, `Status()`, `UpdatedAt()`, `Image()`), `msb.IsSandboxActive(status) bool`, `naming.VmPrefix`, `naming.TaskPrefix`, `naming.ArtifactFor(name).Slug`.
- Produces:
  ```go
  type LiveState struct {
      ActiveVMs map[string]string // slug -> current image digest, for RUNNING VMs
      AllVMs    map[string]bool   // slug with a kept VM (running, or stopped-but-not-stale)
  }
  func BuildLiveState(ctx context.Context, client msb.Client, threshold time.Duration) (LiveState, error)
  ```
  Callers get the `LiveState` from `BuildLiveState` and pass it to the pruners.

- [ ] **Step 1: Write the failing test**

`internal/sandbox/pruning/snapshot_test.go`:

```go
package pruning

import (
    "context"
    "testing"
    "time"

    msbSdk "github.com/superradcompany/microsandbox/sdk/go"

    "gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/msb"
)

func TestBuildLiveState(t *testing.T) {
    recent := time.Now().Add(-5 * time.Minute)
    stale := time.Now().Add(-15 * 24 * time.Hour)

    client := &msb.MockMsbClient{
        Sandboxes: []msb.SandboxHandle{
            // running -> active + all
            &msb.MockSandboxHandle{
                Name_: "opencode-sandbox-vm-proj1-1mjusbm3wikhb0", Status_: msbSdk.SandboxStatusRunning,
                UpdatedAt_: recent, Image_: "opencode-sandbox/runner-proj1-1mjusbm3wikhb0:abc123",
            },
            // stopped but fresh -> all only (kept for restart)
            &msb.MockSandboxHandle{
                Name_: "opencode-sandbox-vm-proj2-1mjusbm3wikhb0", Status_: msbSdk.SandboxStatusStopped,
                UpdatedAt_: recent, Image_: "opencode-sandbox/runner-proj2-1mjusbm3wikhb0:def456",
            },
            // stale stopped -> neither (will be pruned)
            &msb.MockSandboxHandle{
                Name_: "opencode-sandbox-vm-proj3-1mjusbm3wikhb0", Status_: msbSdk.SandboxStatusStopped,
                UpdatedAt_: stale,
            },
            // task sandbox -> never a keep-set member
            &msb.MockSandboxHandle{
                Name_: "opencode-sandbox-task-fill-proj", Status_: msbSdk.SandboxStatusStopped,
                UpdatedAt_: stale,
            },
        },
    }

    snap, err := BuildLiveState(context.Background(), client, 7*24*time.Hour)
    if err != nil {
        t.Fatalf("BuildLiveState: %v", err)
    }
    if got := snap.ActiveVMs["proj1-1mjusbm3wikhb0"]; got != "abc123" {
        t.Errorf("ActiveVMs[proj1] = %q, want abc123", got)
    }
    if snap.AllVMs["proj1-1mjusbm3wikhb0"] != true {
        t.Error("AllVMs[proj1] = false, want true")
    }
    if snap.AllVMs["proj2-1mjusbm3wikhb0"] != true {
        t.Error("AllVMs[proj2] = false, want true")
    }
    if snap.AllVMs["proj3-1mjusbm3wikhb0"] {
        t.Error("AllVMs[proj3] = true, want false (stale)")
    }
    if snap.ActiveVMs["proj2-1mjusbm3wikhb0"] != "" {
        t.Errorf("ActiveVMs[proj2] = %q, want empty (not running)", snap.ActiveVMs["proj2-1mjusbm3wikhb0"])
    }
    if _, ok := snap.ActiveVMs["fill-proj"]; ok {
        t.Error("task sandbox must not be an ActiveVM")
    }
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/sandbox/pruning/ -run TestBuildLiveState`
Expected: FAIL — `undefined: BuildLiveState`

- [ ] **Step 3: Write the implementation**

`internal/sandbox/pruning/snapshot.go`:

```go
package pruning

import (
    "context"
    "time"

    "gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/msb"
    "gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/naming"
)

// LiveState is a point-in-time view of which project slugs have a surviving VM.
// A slug is kept only if it has a VM that PruneVMs will not remove.
type LiveState struct {
    ActiveVMs map[string]string // slug -> current image digest, for RUNNING VMs
    AllVMs    map[string]bool   // slug with a kept VM (running, or stopped-but-not-stale)
}

// BuildLiveState lists sandboxes and records which slugs have a kept VM.
// A stopped/crashed VM is kept (in AllVMs) only when younger than threshold;
// a stale stopped VM is excluded so its volumes/images become prunable.
// Task sandboxes (transient workers) are never keep-set members.
func BuildLiveState(ctx context.Context, client msb.Client, threshold time.Duration) (LiveState, error) {
    snap := LiveState{
        ActiveVMs: make(map[string]string),
        AllVMs:    make(map[string]bool),
    }
    handles, err := client.ListSandboxes(ctx)
    if err != nil {
        return LiveState{}, err
    }
    for _, h := range handles {
        name := h.Name()
        if !hasPrefix(name, naming.VmPrefix) {
            continue // ignore task sandboxes and anything else
        }
        slug := naming.ArtifactFor(name).Slug
        if slug == "" {
            continue
        }
        if msb.IsSandboxActive(h.Status()) {
            snap.ActiveVMs[slug] = imageDigest(h.Image())
            snap.AllVMs[slug] = true
            continue
        }
        if time.Since(h.UpdatedAt()) <= threshold {
            snap.AllVMs[slug] = true
        }
    }
    return snap, nil
}
```

Add these two small helpers in the same package (new file `internal/sandbox/pruning/helpers.go`, not task-scoped; used across pruners):

```go
package pruning

import "strings"

func hasPrefix(s, prefix string) bool { return strings.HasPrefix(s, prefix) }

// imageDigest returns the digest tag (after the last ':') of an image reference,
// or "" when absent.
func imageDigest(ref string) string {
    if i := strings.LastIndex(ref, ":"); i >= 0 {
        return ref[i+1:]
    }
    return ""
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/sandbox/pruning/ -run TestBuildLiveState`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/pruning/snapshot.go internal/sandbox/pruning/snapshot_test.go internal/sandbox/pruning/helpers.go
git commit -m "feat(pruning): add LiveState snapshot of surviving VMs"
```

---

### Task 2: `PruneVMs` — VM + task-sandbox pruning

**Files:**
- Create: `internal/sandbox/pruning/vms.go`, `internal/sandbox/pruning/vms_test.go`

**Interfaces:**
- Consumes: `LiveState` (Task 1), `msb.Client.RemoveSandbox(ctx, name) error`, `msb.IsSandboxActive`, `naming.VmPrefix`/`TaskPrefix`, `naming.ArtifactFor(name).Slug`, `StaleEntry`, `StaleReport`.
- Produces:
  ```go
  type VMReport struct {
      VMsPruned  int
      Details    []StaleEntry
  }
  func PruneVMs(ctx context.Context, snap LiveState, threshold time.Duration, dryRun bool, ui termio.UI) (VMReport, error)
  ```

- [ ] **Step 1: Write the failing test**

`internal/sandbox/pruning/vms_test.go`:

```go
package pruning

import (
    "context"
    "testing"
    "time"

    msbSdk "github.com/superradcompany/microsandbox/sdk/go"

    "gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/msb"
    "gitlab.inoio.de/inoio/opencode-sandbox/internal/termio"
)

func TestPruneVMs(t *testing.T) {
    stale := time.Now().Add(-15 * 24 * time.Hour)
    recent := time.Now().Add(-5 * time.Minute)

    t.Run("prunes stale vm and stopped task, skips running and fresh", func(t *testing.T) {
        client := &msb.MockMsbClient{
            Sandboxes: []msb.SandboxHandle{
                &msb.MockSandboxHandle{Name_: "opencode-sandbox-vm-proj1-1mjusbm3wikhb0", Status_: msbSdk.SandboxStatusStopped, UpdatedAt_: stale},
                &msb.MockSandboxHandle{Name_: "opencode-sandbox-vm-proj2-1mjusbm3wikhb0", Status_: msbSdk.SandboxStatusStopped, UpdatedAt_: recent},
                &msb.MockSandboxHandle{Name_: "opencode-sandbox-vm-proj3-1mjusbm3wikhb0", Status_: msbSdk.SandboxStatusRunning, UpdatedAt_: stale},
                &msb.MockSandboxHandle{Name_: "opencode-sandbox-task-fill-proj", Status_: msbSdk.SandboxStatusStopped, UpdatedAt_: stale},
                &msb.MockSandboxHandle{Name_: "opencode-sandbox-task-fill-proj2", Status_: msbSdk.SandboxStatusRunning, UpdatedAt_: stale},
            },
        }
        ui := &termio.Mock{}
        r, err := PruneVMs(context.Background(), LiveState{}, stale, false, ui)
        if err != nil {
            t.Fatalf("PruneVMs: %v", err)
        }
        // stale VM + stopped task pruned; fresh VM, running VM, running task kept
        if r.VMsPruned != 2 {
            t.Errorf("VMsPruned = %d, want 2 (stale vm + stopped task)", r.VMsPruned)
        }
        want := []string{"opencode-sandbox-vm-proj1-1mjusbm3wikhb0", "opencode-sandbox-task-fill-proj"}
        if len(client.RemovedSandboxes) != len(want) {
            t.Fatalf("RemovedSandboxes = %v, want %v", client.RemovedSandboxes, want)
        }
        for i, n := range want {
            if client.RemovedSandboxes[i] != n {
                t.Errorf("RemovedSandboxes[%d] = %q, want %q", i, client.RemovedSandboxes[i], n)
            }
        }
    })

    t.Run("dry run counts but does not delete", func(t *testing.T) {
        client := &msb.MockMsbClient{
            Sandboxes: []msb.SandboxHandle{
                &msb.MockSandboxHandle{Name_: "opencode-sandbox-vm-proj1-1mjusbm3wikhb0", Status_: msbSdk.SandboxStatusStopped, UpdatedAt_: stale},
            },
        }
        ui := &termio.Mock{}
        r, err := PruneVMs(context.Background(), LiveState{}, stale, true, ui)
        if err != nil {
            t.Fatalf("PruneVMs: %v", err)
        }
        if r.VMsPruned != 1 {
            t.Errorf("VMsPruned = %d, want 1", r.VMsPruned)
        }
        if len(client.RemovedSandboxes) != 0 {
            t.Errorf("dry run removed sandboxes: %v", client.RemovedSandboxes)
        }
    })

    t.Run("zero threshold prunes any stopped", func(t *testing.T) {
        client := &msb.MockMsbClient{
            Sandboxes: []msb.SandboxHandle{
                &msb.MockSandboxHandle{Name_: "opencode-sandbox-vm-proj1-1mjusbm3wikhb0", Status_: msbSdk.SandboxStatusStopped, UpdatedAt_: recent},
            },
        }
        ui := &termio.Mock{}
        r, err := PruneVMs(context.Background(), LiveState{}, 0, false, ui)
        if err != nil {
            t.Fatalf("PruneVMs: %v", err)
        }
        if r.VMsPruned != 1 {
            t.Errorf("VMsPruned = %d, want 1 (age 0 = no wait)", r.VMsPruned)
        }
    })
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/sandbox/pruning/ -run TestPruneVMs`
Expected: FAIL — `undefined: PruneVMs`

- [ ] **Step 3: Write the implementation**

`internal/sandbox/pruning/vms.go`:

```go
package pruning

import (
    "context"
    "time"

    "gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/msb"
    "gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/naming"
    "gitlab.inoio.de/inoio/opencode-sandbox/internal/termio"
)

// VMReport summarizes a PruneVMs run.
type VMReport struct {
    VMsPruned int
    Details   []StaleEntry
}

// PruneVMs prunes stale VMs and stopped task sandboxes. Task sandboxes have no
// age gate (transient workers) but running ones are skipped. A stopped VM is
// pruned only when older than threshold (age 0 = no wait).
func PruneVMs(ctx context.Context, snap LiveState, threshold time.Duration, dryRun bool, ui termio.UI) (VMReport, error) {
    report := VMReport{}
    handles, err := msb.Get().ListSandboxes(ctx)
    if err != nil {
        return report, err
    }
    for _, h := range handles {
        name := h.Name()
        if msb.IsSandboxActive(h.Status()) {
            continue
        }
        isTask := hasPrefix(name, naming.TaskPrefix)
        if !isTask && !hasPrefix(name, naming.VmPrefix) {
            continue
        }
        if !isTask && time.Since(h.UpdatedAt()) <= threshold {
            continue // stopped but not stale
        }
        if !dryRun {
            if err := msb.Get().RemoveSandbox(ctx, name); err != nil {
                ui.Warnf("failed to remove sandbox %s: %v", name, err)
                continue
            }
        }
        report.VMsPruned++
        report.Details = append(report.Details, StaleEntry{
            Type:     StaleTypeVM,
            Name:     name,
            Slug:     naming.ArtifactFor(name).Slug,
            StaleFor: time.Since(h.UpdatedAt()),
        })
    }
    return report, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/sandbox/pruning/ -run TestPruneVMs`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/pruning/vms.go internal/sandbox/pruning/vms_test.go
git commit -m "feat(pruning): add PruneVMs for stale VMs and task sandboxes"
```

---

### Task 3: `PruneVolumes` — home-volume pruning with keep-set

**Files:**
- Create: `internal/sandbox/pruning/volumes.go`, `internal/sandbox/pruning/volumes_test.go`

**Interfaces:**
- Consumes: `LiveState` (Task 1), `msb.Client.ListVolumes`/`RemoveVolume`, `state.RemoveState(slug) error`, `naming.HomePrefix`, `naming.ArtifactFor(name).Slug`, `StaleEntry`, `StaleReport`.
- Produces:
  ```go
  type VolumeReport struct {
      VolumesPruned int
      Details       []StaleEntry
  }
  func PruneVolumes(ctx context.Context, snap LiveState, threshold time.Duration, all, dryRun bool, ui termio.UI) (VolumeReport, error)
  ```

- [ ] **Step 1: Write the failing test**

`internal/sandbox/pruning/volumes_test.go`:

```go
package pruning

import (
    "context"
    "testing"
    "time"

    "gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/msb"
    "gitlab.inoio.de/inoio/opencode-sandbox/internal/termio"
)

func TestPruneVolumes(t *testing.T) {
    old := time.Now().Add(-15 * 24 * time.Hour)

    home := func(slug, name string) msb.VolumeHandle {
        return &msb.MockVolumeHandle{Name_: name, CreatedAt_: old}
    }

    t.Run("keeps volume when slug in AllVMs, prunes orphan slug", func(t *testing.T) {
        client := &msb.MockMsbClient{
            Volumes: []msb.VolumeHandle{
                home("kept", "opencode-sandbox-home-kept-1mjusbm3wikhb0-20260806T143022"),
                home("orphan", "opencode-sandbox-home-orphan-1mjusbm3wikhb0-20260806T143022"),
            },
        }
        snap := LiveState{AllVMs: map[string]bool{"kept-1mjusbm3wikhb0": true}}
        ui := &termio.Mock{}
        r, err := PruneVolumes(context.Background(), snap, 7*24*time.Hour, false, false, ui)
        if err != nil {
            t.Fatalf("PruneVolumes: %v", err)
        }
        if r.VolumesPruned != 1 {
            t.Errorf("VolumesPruned = %d, want 1", r.VolumesPruned)
        }
        if len(client.RemovedVolumes) != 1 || client.RemovedVolumes[0] != "opencode-sandbox-home-orphan-1mjusbm3wikhb0-20260806T143022" {
            t.Errorf("RemovedVolumes = %v, want orphan volume only", client.RemovedVolumes)
        }
    })

    t.Run("all=true uses ActiveVMs keep-set", func(t *testing.T) {
        // slug has a stopped-but-fresh VM (AllVMs) but no running VM.
        client := &msb.MockMsbClient{
            Volumes: []msb.VolumeHandle{
                home("stoppedproj", "opencode-sandbox-home-stoppedproj-1mjusbm3wikhb0-20260806T143022"),
            },
        }
        snap := LiveState{
            AllVMs:    map[string]bool{"stoppedproj-1mjusbm3wikhb0": true},
            ActiveVMs: map[string]string{},
        }
        ui := &termio.Mock{}
        r, err := PruneVolumes(context.Background(), snap, 7*24*time.Hour, true, false, ui)
        if err != nil {
            t.Fatalf("PruneVolumes: %v", err)
        }
        if r.VolumesPruned != 1 {
            t.Errorf("VolumesPruned = %d, want 1 under --all", r.VolumesPruned)
        }
    })

    t.Run("young volume not pruned even when orphaned", func(t *testing.T) {
        client := &msb.MockMsbClient{
            Volumes: []msb.VolumeHandle{
                &msb.MockVolumeHandle{Name_: "opencode-sandbox-home-orphan-1mjusbm3wikhb0-20260806T143022", CreatedAt_: time.Now()},
            },
        }
        snap := LiveState{AllVMs: map[string]bool{}}
        ui := &termio.Mock{}
        r, err := PruneVolumes(context.Background(), snap, 7*24*time.Hour, false, false, ui)
        if err != nil {
            t.Fatalf("PruneVolumes: %v", err)
        }
        if r.VolumesPruned != 0 {
            t.Errorf("VolumesPruned = %d, want 0 (recent)", r.VolumesPruned)
        }
    })
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/sandbox/pruning/ -run TestPruneVolumes`
Expected: FAIL — `undefined: PruneVolumes`

- [ ] **Step 3: Write the implementation**

`internal/sandbox/pruning/volumes.go`:

```go
package pruning

import (
    "context"
    "time"

    "gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/msb"
    "gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/naming"
    "gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/state"
    "gitlab.inoio.de/inoio/opencode-sandbox/internal/termio"
)

// VolumeReport summarizes a PruneVolumes run.
type VolumeReport struct {
    VolumesPruned int
    Details       []StaleEntry
}

// PruneVolumes prunes home volumes whose slug is not in the keep-set and that
// are older than threshold. The keep-set is AllVMs by default, ActiveVMs under
// all. When any of a slug's volumes are removed, its state file is removed too.
func PruneVolumes(ctx context.Context, snap LiveState, threshold time.Duration, all, dryRun bool, ui termio.UI) (VolumeReport, error) {
    report := VolumeReport{}
    keep := snap.AllVMs
    if all {
        keep = activeSlugs(snap)
    }
    handles, err := msb.Get().ListVolumes(ctx)
    if err != nil {
        return report, err
    }
    removedSlugs := map[string]bool{}
    for _, h := range handles {
        name := h.Name()
        if !hasPrefix(name, naming.HomePrefix) {
            continue
        }
        slug := naming.ArtifactFor(name).Slug
        if slug == "" || keep[slug] {
            continue
        }
        if time.Since(h.CreatedAt()) <= threshold {
            continue
        }
        if !dryRun {
            if err := msb.Get().RemoveVolume(ctx, name); err != nil {
                ui.Warnf("failed to remove home volume %s: %v", name, err)
                continue
            }
            removedSlugs[slug] = true
        }
        report.VolumesPruned++
        report.Details = append(report.Details, StaleEntry{
            Type:   StaleTypeVolume,
            Name:   name,
            Slug:   slug,
            Digest: naming.ArtifactFor(name).Digest,
        })
    }
    if !dryRun {
        for slug := range removedSlugs {
            if err := state.RemoveState(slug); err != nil {
                ui.Warnf("failed to remove state file for slug %s: %v", slug, err)
            }
        }
    }
    return report, nil
}

func activeSlugs(snap LiveState) map[string]bool {
    m := make(map[string]bool, len(snap.ActiveVMs))
    for slug := range snap.ActiveVMs {
        m[slug] = true
    }
    return m
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/sandbox/pruning/ -run TestPruneVolumes`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/pruning/volumes.go internal/sandbox/pruning/volumes_test.go
git commit -m "feat(pruning): add PruneVolumes with keep-set predicate"
```

---

### Task 4: `PruneImages` — MSB images + docker dangling

**Files:**
- Create: `internal/sandbox/pruning/images.go`, `internal/sandbox/pruning/images_test.go`

**Interfaces:**
- Consumes: `LiveState` (Task 1), `activeSlugs(snap) map[string]bool` (Task 3, volumes.go), `msb.Client.ImageList`/`ImageRemove(ref, force)`, `docker.Get().ImagePrune(ctx, opts)`, `moby/client.ImagePruneOptions`, `naming.ImagePrefix`, `naming.BaseSlug`, `naming.ArtifactFor(ref).Slug`/`.Digest`, `StaleReport` (for docker count), `StaleEntry`, `StaleType`.
- Produces:
  ```go
  type ImageReport struct {
      MSBImagesPruned   int
      DockerImagesPruned int
      Details           []StaleEntry
  }
  func PruneImages(ctx context.Context, snap LiveState, threshold time.Duration, all, dryRun bool, ui termio.UI) (ImageReport, error)
  ```
  `all` selects the keep-set: `snap.AllVMs` by default, `snap.ActiveVMs` when `all`.

- [ ] **Step 1: Write the failing test**

`internal/sandbox/pruning/images_test.go`:

```go
package pruning

import (
    "context"
    "testing"
    "time"

    "gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/msb"
    "gitlab.inoio.de/inoio/opencode-sandbox/internal/termio"
)

func TestPruneImages(t *testing.T) {
    old := time.Now().Add(-15 * 24 * time.Hour)

    img := func(ref string) msb.ImageHandle {
        return &msb.MockImageHandle{Reference_: ref, LastUsedAt_: old}
    }

    t.Run("prunes orphan slug and surplus digest of active slug", func(t *testing.T) {
        client := &msb.MockMsbClient{
            Images: []msb.ImageHandle{
                img("opencode-sandbox/runner-orphan-1mjusbm3wikhb0:digest1"),
                img("opencode-sandbox/runner-active-1mjusbm3wikhb0:digestCur"),
                img("opencode-sandbox/runner-active-1mjusbm3wikhb0:digestOld"),
                img("opencode-sandbox/runner-base:latest"), // base excluded
            },
        }
        snap := LiveState{
            AllVMs:    map[string]bool{"active-1mjusbm3wikhb0": true},
            ActiveVMs: map[string]string{"active-1mjusbm3wikhb0": "digestCur"},
        }
        ui := &termio.Mock{}
        r, err := PruneImages(context.Background(), snap, 7*24*time.Hour, false, false, ui)
        if err != nil {
            t.Fatalf("PruneImages: %v", err)
        }
        if r.MSBImagesPruned != 2 {
            t.Errorf("MSBImagesPruned = %d, want 2 (orphan digest1 + active digestOld)", r.MSBImagesPruned)
        }
        if len(client.RemovedImages) != 2 {
            t.Fatalf("RemovedImages = %v, want 2", client.RemovedImages)
        }
    })

    t.Run("dry run counts but does not delete", func(t *testing.T) {
        client := &msb.MockMsbClient{
            Images: []msb.ImageHandle{img("opencode-sandbox/runner-orphan-1mjusbm3wikhb0:digest1")},
        }
        ui := &termio.Mock{}
        r, err := PruneImages(context.Background(), LiveState{}, 7*24*time.Hour, false, true, ui)
        if err != nil {
            t.Fatalf("PruneImages: %v", err)
        }
        if r.MSBImagesPruned != 1 {
            t.Errorf("MSBImagesPruned = %d, want 1", r.MSBImagesPruned)
        }
        if len(client.RemovedImages) != 0 {
            t.Errorf("dry run removed images: %v", client.RemovedImages)
        }
    })

    t.Run("all=true uses ActiveVMs keep-set", func(t *testing.T) {
        client := &msb.MockMsbClient{
            Images: []msb.ImageHandle{img("opencode-sandbox/runner-stopped-1mjusbm3wikhb0:digest1")},
        }
        snap := LiveState{
            AllVMs:    map[string]bool{"stopped-1mjusbm3wikhb0": true},
            ActiveVMs: map[string]string{},
        }
        ui := &termio.Mock{}
        r, err := PruneImages(context.Background(), snap, 7*24*time.Hour, true, false, ui)
        if err != nil {
            t.Fatalf("PruneImages: %v", err)
        }
        if r.MSBImagesPruned != 1 {
            t.Errorf("MSBImagesPruned = %d, want 1 under --all", r.MSBImagesPruned)
        }
    })

    t.Run("young image kept despite orphaned slug", func(t *testing.T) {
        client := &msb.MockMsbClient{
            Images: []msb.ImageHandle{
                &msb.MockImageHandle{Reference_: "opencode-sandbox/runner-orphan-1mjusbm3wikhb0:digest1", LastUsedAt_: time.Now()},
            },
        }
        ui := &termio.Mock{}
        r, err := PruneImages(context.Background(), LiveState{}, 7*24*time.Hour, false, false, ui)
        if err != nil {
            t.Fatalf("PruneImages: %v", err)
        }
        if r.MSBImagesPruned != 0 {
            t.Errorf("MSBImagesPruned = %d, want 0 (recent image)", r.MSBImagesPruned)
        }
    })
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/sandbox/pruning/ -run TestPruneImages`
Expected: FAIL — `undefined: PruneImages`

- [ ] **Step 3: Write the implementation**

`internal/sandbox/pruning/images.go`:

```go
package pruning

import (
    "context"
    "time"

    "github.com/moby/moby/client"

    "gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/docker"
    "gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/msb"
    "gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/naming"
    "gitlab.inoio.de/inoio/opencode-sandbox/internal/termio"
)

// ImageReport summarizes a PruneImages run.
type ImageReport struct {
    MSBImagesPruned   int
    DockerImagesPruned int
    Details           []StaleEntry
}

// PruneImages prunes MSB runner images with no surviving VM (or surplus digests
// of a running VM) and host-side dangling docker images. A prunable MSB image is
// removed only when LastUsedAt is older than threshold.
func PruneImages(ctx context.Context, snap LiveState, threshold time.Duration, all, dryRun bool, ui termio.UI) (ImageReport, error) {
    report := ImageReport{}
    keep := snap.AllVMs
    if all {
        keep = activeSlugs(snap)
    }
    handles, err := msb.Get().ImageList(ctx)
    if err != nil {
        return report, err
    }
    for _, h := range handles {
        ref := h.Reference()
        if !hasPrefix(ref, naming.ImagePrefix) {
            continue
        }
        info := naming.ArtifactFor(ref)
        if info.Slug == naming.BaseSlug {
            continue
        }
        if !pruneImage(info, snap, keep) {
            continue
        }
        if time.Since(h.LastUsedAt()) <= threshold {
            continue
        }
        if !dryRun {
            if err := msb.Get().ImageRemove(ctx, ref, true); err != nil {
                ui.Warnf("failed to remove msb image %s: %v", ref, err)
                continue
            }
        }
        report.MSBImagesPruned++
        report.Details = append(report.Details, StaleEntry{
            Type:   StaleTypeMsbImage,
            Name:   ref,
            Slug:   info.Slug,
            Digest: info.Digest,
        })
    }
    report.DockerImagesPruned = pruneDockerImages(ctx, dryRun, ui)
    return report, nil
}

// pruneImage reports whether an MSB image should be removed under the given keep-set.
func pruneImage(info naming.ArtifactInfo, snap LiveState, keep map[string]bool) bool {
    if !keep[info.Slug] {
        return true // no surviving VM
    }
    if cur, ok := snap.ActiveVMs[info.Slug]; ok {
        return info.Digest != "" && info.Digest != cur // surplus digest of a running VM
    }
    return false
}

// pruneDockerImages removes dangling (untagged) docker images; skipped on dry-run.
func pruneDockerImages(ctx context.Context, dryRun bool, ui termio.UI) int {
    if dryRun {
        return 0
    }
    result, err := docker.Get().ImagePrune(ctx, client.ImagePruneOptions{Filters: client.Filters{}})
    if err != nil {
        ui.Warnf("failed to prune docker images: %v", err)
        return 0
    }
    return len(result.Report.ImagesDeleted)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/sandbox/pruning/ -run TestPruneImages`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/pruning/images.go internal/sandbox/pruning/images_test.go
git commit -m "feat(pruning): add PruneImages for MSB and docker images"
```

---

### Task 5: Reduce `StaleReport`, remove dead types, and delete the old pipeline

**Files:**
- Modify: `internal/sandbox/pruning/report.go`
- Delete: `internal/sandbox/pruning/catalog.go`, `internal/sandbox/pruning/stale.go`, `internal/sandbox/pruning/remove.go`
- Modify: `internal/sandbox/naming/artifact.go`
- Test: update `internal/sandbox/pruning/cleanup_test.go`, `internal/sandbox/pruning/prune_client_test.go`, `internal/sandbox/pruning/prune_test.go` (this is done in Tasks 6–8; this task only updates what breaks at compile time)

**Interfaces:**
- Consumes: prior tasks' report types.
- Produces: reduced `StaleReport` with only `PrunedVMs`, `PrunedVolumes`, `PrunedDockerImages`, `PrunedMSBImages`, `Details`; `StaleEntry` (unchanged shape); `StaleType` (unchanged).

- [ ] **Step 1: Write the reduced report**

Replace the body of `internal/sandbox/pruning/report.go` (keep `StaleEntry` unchanged):

```go
package pruning

import "time"

// StaleReport describes the result of an aggregate prune. Task sandboxes count
// into PrunedVMs; clone volumes are removed (dead code).
type StaleReport struct {
    PrunedVMs          int
    PrunedVolumes      int
    PrunedDockerImages int
    PrunedMSBImages    int
    Details            []StaleEntry
}

// StaleEntry describes a single artifact that was pruned or would be pruned.
type StaleEntry struct {
    Type     StaleType
    Name     string
    StaleFor time.Duration
    Slug     string
    Digest   string
}

// StaleType indicates the kind of artifact being pruned.
type StaleType int

const (
    StaleTypeVM          StaleType = iota
    StaleTypeVolume
    StaleTypeDockerImage
    StaleTypeMsbImage
)

var typeName = map[StaleType]string{ //nolint:gochecknoglobals // fmt.stringer pattern
    StaleTypeVM:          "vm",
    StaleTypeVolume:      "volume",
    StaleTypeDockerImage: "docker-image",
    StaleTypeMsbImage:    "msb-image",
}

func (ss StaleType) String() string { return typeName[ss] }
```

- [ ] **Step 2: Delete the old pipeline files**

```bash
rm internal/sandbox/pruning/catalog.go internal/sandbox/pruning/stale.go internal/sandbox/pruning/remove.go
```

This removes: `PruningCatalog`, `buildCatalog`, `findStaleVMs`, `isStaleSlug`, `staleVM`, `volumeWithAge`, `imageWithDigest`, `pruneStaleCascade`, `pruneOrphanSlug`, `pruneActiveVMCleanup`, `pruneActiveVMHomeVolumes`, `pruneActiveVMMSBImages`, `pruneCloneVolumes`, `pruneTaskSandboxes`, `removeHomeVolumes`, `removeMSBImages`, `isRecent`, and the `pruneDockerImages` duplicate (now in images.go).

- [ ] **Step 3: Remove dead clone parsing from naming**

Edit `internal/sandbox/naming/artifact.go`:

Delete `ParseCloneVolumeName` (lines 130–142) and the `ClonePrefix` case inside `ArtifactFor` (the `case strings.HasPrefix(name, ClonePrefix):` block).

- [ ] **Step 4: Update compile-broken tests**

Run: `go build ./...`
Expected: compile errors only in `internal/sandbox/pruning/*_test.go` referencing deleted identifiers. Fix them minimally by deleting the broken test functions; the authoritative replacement tests arrive in Tasks 6–8. Which functions to remove:

- `prune_test.go`: `TestPruneActiveVMHomeVolumes_*`, `TestRemoveHomeVolumes_*`, `TestStaleReport`, `TestFindStaleVMs*`, `TestParseCloneVolumeName`, `runPruneActiveVMTest`, and the `homeVolumeStateYAML`/`pruneTestSlug` consts if now unused.
- `prune_client_test.go`: `TestPruneStaleCascade_*`, `TestPruneActiveVMCleanup_*`, `TestPruneOrphanSlug_*`, `TestPruneCloneVolumes_*`, `TestPrune_WithMocks_CoversAllCases`, `TestPrune_StoppedRecentVM_PreservesImage`, `prunedCounts`/`assertReport` (replaced), `mockDockerClient` (replaced by images_test.go usage).
- `cleanup_test.go`: `TestStaleReportHasAnything` (rewritten in Task 6).

Run `go test ./internal/sandbox/pruning/ -run TestAutoPrune` to confirm the package compiles and `AutoPrune` still passes.

- [ ] **Step 5: Commit**

```bash
git add -A internal/sandbox/pruning internal/sandbox/naming/artifact.go
git commit -m "refactor(pruning): reduce StaleReport and delete monolithic pipeline"
```

---

### Task 6: Rewrite the aggregate `Prune` as a composition

**Files:**
- Modify: `internal/sandbox/pruning/prune.go`
- Test: `internal/sandbox/pruning/prune_test.go`, `internal/sandbox/pruning/cleanup_test.go`

**Interfaces:**
- Consumes: `BuildLiveState`, `PruneVMs`, `PruneVolumes`, `PruneImages` (Tasks 1–4), `StaleReport` (Task 5).
- Produces: `Prune(ctx, threshold, dryRun, autoPrune, ui) error` — merges typed reports into `StaleReport` and prints the summary. `AutoPrune` (cleanup.go) unchanged.

- [ ] **Step 1: Write the failing test**

Rewrite `internal/sandbox/pruning/cleanup_test.go` `TestStaleReportHasAnything` for the reduced report:

```go
func TestStaleReportHasAnything(t *testing.T) {
    tests := []struct {
        name   string
        report *StaleReport
        want   bool
    }{
        {"empty", &StaleReport{}, false},
        {"nil", nil, false},
        {"vms", &StaleReport{PrunedVMs: 1}, true},
        {"volumes", &StaleReport{PrunedVolumes: 3}, true},
        {"docker", &StaleReport{PrunedDockerImages: 2}, true},
        {"msb", &StaleReport{PrunedMSBImages: 1}, true},
    }
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            if got := tt.report.hasAnything(); got != tt.want {
                t.Errorf("hasAnything() = %v, want %v", got, tt.want)
            }
        })
    }
}
```

Add an aggregate parity test in `internal/sandbox/pruning/prune_test.go`:

```go
func TestPruneAggregateParity(t *testing.T) {
    old := time.Now().Add(-15 * 24 * time.Hour)
    client := &msb.MockMsbClient{
        Sandboxes: []msb.SandboxHandle{
            // stale VM -> its volumes/images are reclaimed by the other pruners
            &msb.MockSandboxHandle{Name_: "opencode-sandbox-vm-proj-1mjusbm3wikhb0", Status_: msbSdk.SandboxStatusStopped, UpdatedAt_: old},
            // active VM keeps its current image
            &msb.MockSandboxHandle{Name_: "opencode-sandbox-vm-live-1mjusbm3wikhb0", Status_: msbSdk.SandboxStatusRunning, UpdatedAt_: old, Image_: "opencode-sandbox/runner-live-1mjusbm3wikhb0:cur"},
        },
        Volumes: []msb.VolumeHandle{
            &msb.MockVolumeHandle{Name_: "opencode-sandbox-home-proj-1mjusbm3wikhb0-20260806T143022", CreatedAt_: old},
            &msb.MockVolumeHandle{Name_: "opencode-sandbox-home-live-1mjusbm3wikhb0-20260806T143022", CreatedAt_: old},
        },
        Images: []msb.ImageHandle{
            &msb.MockImageHandle{Reference_: "opencode-sandbox/runner-proj-1mjusbm3wikhb0:old", LastUsedAt_: old},
            &msb.MockImageHandle{Reference_: "opencode-sandbox/runner-live-1mjusbm3wikhb0:cur", LastUsedAt_: old},
            &msb.MockImageHandle{Reference_: "opencode-sandbox/runner-live-1mjusbm3wikhb0:old", LastUsedAt_: old},
        },
    }
    msb.WithMsbMock(t, client)
    docker.WithNoopDockerMock(t)
    cp.WithMockConfigPaths(t)

    if err := Prune(context.Background(), 7*24*time.Hour, false, false, termio.NewTestMock(t)); err != nil {
        t.Fatalf("Prune: %v", err)
    }
    // stale VM pruned; its volume pruned; its image pruned; live keeps cur, prunes old.
    if got := len(client.RemovedSandboxes); got != 1 {
        t.Errorf("RemovedSandboxes = %d, want 1", got)
    }
    if got := len(client.RemovedVolumes); got != 1 {
        t.Errorf("RemovedVolumes = %d, want 1", got)
    }
    if got := len(client.RemovedImages); got != 2 {
        t.Errorf("RemovedImages = %d, want 2 (stale proj image + live surplus)", got)
    }
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/sandbox/pruning/ -run 'TestPruneAggregateParity|TestStaleReportHasAnything'`
Expected: FAIL — `undefined: Prune` (or compile error)

- [ ] **Step 3: Write the implementation**

Rewrite `internal/sandbox/pruning/prune.go`:

```go
package pruning

import (
    "context"
    "time"

    "gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/msb"
    "gitlab.inoio.de/inoio/opencode-sandbox/internal/termio"
)

// MsbClient is a type alias for msb.Client, matching the original sandbox-level alias.
type MsbClient = msb.Client

// hasAnything reports whether the report contains any pruned items.
func (r *StaleReport) hasAnything() bool {
    if r == nil {
        return false
    }
    return r.PrunedVMs > 0 ||
        r.PrunedVolumes > 0 ||
        r.PrunedDockerImages > 0 ||
        r.PrunedMSBImages > 0
}

// Prune orchestrates all three pruners against one shared snapshot and prints a
// merged summary. Task sandboxes count into PrunedVMs.
func Prune(ctx context.Context, threshold time.Duration, dryRun, autoPrune bool, ui termio.UI) error {
    snap, err := BuildLiveState(ctx, msb.Get(), threshold)
    if err != nil {
        return err
    }
    vmReport, vmErr := PruneVMs(ctx, snap, threshold, dryRun, ui)
    volReport, volErr := PruneVolumes(ctx, snap, threshold, false, dryRun, ui)
    imgReport, imgErr := PruneImages(ctx, snap, threshold, false, dryRun, ui)

    report := &StaleReport{
        PrunedVMs:          vmReport.VMsPruned,
        PrunedVolumes:      volReport.VolumesPruned,
        PrunedDockerImages: imgReport.DockerImagesPruned,
        PrunedMSBImages:    imgReport.MSBImagesPruned,
        Details:            append(append(vmReport.Details, volReport.Details...), imgReport.Details...),
    }
    printPruneSummary(ui, report, dryRun, autoPrune)
    return errors.Join(vmErr, volErr, imgErr)
}

func printPruneSummary(ui termio.UI, report *StaleReport, dryRun, autoPrune bool) {
    if report == nil {
        return
    }
    out := ui.Outf
    action := "Pruned"
    if autoPrune {
        out = ui.Verbosef
        action = "auto-prune: Pruned"
    }
    if dryRun {
        action = "dry-run: Would prune"
    }
    if !report.hasAnything() {
        out = ui.Verbosef
    }
    out(
        "%s %d VMs, %d home volumes, %d docker images, %d msb images",
        action,
        report.PrunedVMs,
        report.PrunedVolumes,
        report.PrunedDockerImages,
        report.PrunedMSBImages,
    )
}
```

Add the `errors` import to `prune.go`.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/sandbox/pruning/ -run 'TestPruneAggregateParity|TestStaleReportHasAnything|TestAutoPrune'`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/pruning/prune.go internal/sandbox/pruning/cleanup_test.go internal/sandbox/pruning/prune_test.go
git commit -m "refactor(pruning): aggregate Prune composes the three pruners"
```

---

### Task 7: Wire `image prune`, `volume prune`, `sandbox prune` subcommands

**Files:**
- Modify: `cmd/opencode-sandbox/commands_system.go`, `cmd/opencode-sandbox/constants.go`
- Test: `cmd/opencode-sandbox/cli_prune_test.go` (update), new `cmd/opencode-sandbox/cli_image_prune_test.go`, `cli_volume_prune_test.go`, `cli_sandbox_prune_test.go`

**Interfaces:**
- Consumes: `pruning.PruneVMs/PruneVolumes/PruneImages`, `pruning.BuildLiveState`, `pruning.StaleReport`, `resolverFromContext(cmd.Context())`, `viperconfig.ParseHumanDuration`.
- Produces: three cobra subcommands registered under `image`, `volume`, `sandbox`. `image prune` has **no** `--age`; `volume prune`/`sandbox prune` have `--age`. `image prune`/`volume prune` have `--all`.

- [ ] **Step 1: Add flag name constant**

In `cmd/opencode-sandbox/constants.go`, add:

```go
flagAll = "all"
```

- [ ] **Step 2: Add a shared age resolver helper**

In `cmd/opencode-sandbox/commands_system.go`, add a helper shared by `buildPruneCmd` and the new commands:

```go
// resolvePruneAge returns the effective prune threshold for a manual prune:
// --age if set, else manual-prune-age from config, else the 7d default.
func resolvePruneAge(cmd *cobra.Command) (time.Duration, error) {
    ageStr, _ := cmd.Flags().GetString(flagAge)
    if ageStr == "" {
        if r := resolverFromContext(cmd.Context()); r != nil && r.ManualPruneAge() > 0 {
            return r.ManualPruneAge(), nil
        }
        return 7 * 24 * time.Hour, nil
    }
    d, ok := viperconfig.ParseHumanDuration(ageStr)
    if !ok {
        return 0, fmt.Errorf("invalid age %q: use a Go duration or suffix d/w (e.g. 7d, 2w)", ageStr)
    }
    return d, nil
}
```

- [ ] **Step 3: Refactor `buildPruneCmd` to use the helper**

Replace the `RunE` age-resolution block in `buildPruneCmd` with:

```go
RunE: func(cmd *cobra.Command, _ []string) error {
    age, err := resolvePruneAge(cmd)
    if err != nil {
        return err
    }
    dryRun, _ := cmd.Flags().GetBool(flagDryRun)
    return pruning.Prune(cmd.Context(), age, dryRun, false, ui)
},
```

- [ ] **Step 4: Add the three subcommands**

Add helper builders and register them:

```go
func buildImagePruneCmd(ui termio.UI) *cobra.Command {
    cmd := &cobra.Command{
        Use:   cmdPrune,
        Args:  cobra.NoArgs,
        Short: "Prune cached runner images not in use",
        RunE: func(cmd *cobra.Command, _ []string) error {
            age, err := resolvePruneAge(cmd)
            if err != nil {
                return err
            }
            dryRun, _ := cmd.Flags().GetBool(flagDryRun)
            all, _ := cmd.Flags().GetBool(flagAll)
            snap, err := pruning.BuildLiveState(cmd.Context(), msb.Get(), age)
            if err != nil {
                return err
            }
            report, err := pruning.PruneImages(cmd.Context(), snap, age, all, dryRun, ui)
            if err != nil {
                return err
            }
            printImagePruneReport(ui, report, dryRun, all)
            return nil
        },
    }
    cmd.Flags().StringP(flagAge, flagAge[:1], "", "Prune threshold (default: manualPruneAge from config)")
    cmd.Flags().BoolP(flagDryRun, flagDryRunShort, false, "Show what would be pruned without deleting")
    cmd.Flags().BoolP(flagAll, flagAll[:1], false, "Prune images of stopped-but-existing projects too")
    return cmd
}
```

For `volume prune`:

```go
func buildVolumePruneCmd(ui termio.UI) *cobra.Command {
    cmd := &cobra.Command{
        Use:   cmdPrune,
        Args:  cobra.NoArgs,
        Short: "Prune home volumes no longer referenced by a project VM",
        RunE: func(cmd *cobra.Command, _ []string) error {
            age, err := resolvePruneAge(cmd)
            if err != nil {
                return err
            }
            dryRun, _ := cmd.Flags().GetBool(flagDryRun)
            all, _ := cmd.Flags().GetBool(flagAll)
            snap, err := pruning.BuildLiveState(cmd.Context(), msb.Get(), age)
            if err != nil {
                return err
            }
            report, err := pruning.PruneVolumes(cmd.Context(), snap, age, all, dryRun, ui)
            if err != nil {
                return err
            }
            printVolumePruneReport(ui, report, dryRun, all)
            return nil
        },
    }
    cmd.Flags().StringP(flagAge, flagAge[:1], "", "Prune threshold (default: manualPruneAge from config)")
    cmd.Flags().BoolP(flagDryRun, flagDryRunShort, false, "Show what would be pruned without deleting")
    cmd.Flags().BoolP(flagAll, flagAll[:1], false, "Prune volumes of stopped-but-existing projects too")
    return cmd
}
```

For `sandbox prune`:

```go
func buildSandboxPruneCmd(ui termio.UI) *cobra.Command {
    cmd := &cobra.Command{
        Use:   cmdPrune,
        Args:  cobra.NoArgs,
        Short: "Prune stale sandboxes and leftover task workers",
        RunE: func(cmd *cobra.Command, _ []string) error {
            age, err := resolvePruneAge(cmd)
            if err != nil {
                return err
            }
            dryRun, _ := cmd.Flags().GetBool(flagDryRun)
            snap, err := pruning.BuildLiveState(cmd.Context(), msb.Get(), age)
            if err != nil {
                return err
            }
            report, err := pruning.PruneVMs(cmd.Context(), snap, age, dryRun, ui)
            if err != nil {
                return err
            }
            printVMPruneReport(ui, report, dryRun)
            return nil
        },
    }
    cmd.Flags().StringP(flagAge, flagAge[:1], "", "Prune threshold (default: manualPruneAge from config)")
    cmd.Flags().BoolP(flagDryRun, flagDryRunShort, false, "Show what would be pruned without deleting")
    return cmd
}
```

Register them in `buildImageCmd`, `buildVolumeCmd`, `buildSandboxCmd`:

```go
// in buildImageCmd:
cmd.AddCommand(buildImagePruneCmd(ui))

// in buildVolumeCmd:
cmd.AddCommand(buildVolumePruneCmd(ui))

// in buildSandboxCmd:
cmd.AddCommand(buildSandboxPruneCmd(ui))
```

Add the report printers (in `commands_system.go`):

```go
func printImagePruneReport(ui termio.UI, r pruning.ImageReport, dryRun, all bool) {
    ui.Infof("image prune: %d runner image(s), %d dangling docker image(s)", r.MSBImagesPruned, r.DockerImagesPruned)
    for _, d := range r.Details {
        ui.Verbosef("  %s (%s)", d.Name, d.Slug)
    }
}
func printVolumePruneReport(ui termio.UI, r pruning.VolumeReport, dryRun, all bool) {
    ui.Infof("volume prune: %d home volume(s)", r.VolumesPruned)
    for _, d := range r.Details {
        ui.Verbosef("  %s (%s)", d.Name, d.Slug)
    }
}
func printVMPruneReport(ui termio.UI, r pruning.VMReport, dryRun bool) {
    ui.Infof("sandbox prune: %d sandbox(es)", r.VMsPruned)
    for _, d := range r.Details {
        ui.Verbosef("  %s (%s)", d.Name, d.Slug)
    }
}
```

Add the `msb` import to `commands_system.go` (it already imports `internal/sandbox/image`; add `internal/sandbox/msb`).

- [ ] **Step 5: Write CLI tests**

Update `cmd/opencode-sandbox/cli_prune_test.go`: the aggregate summary format changed. Replace every expected summary string of the form `... task sandboxes, N clone volumes` with the new 4-part form (drop `task sandboxes` and `clone volumes`; task sandboxes now fold into the VM count). Then add:

`cmd/opencode-sandbox/cli_image_prune_test.go`:

```go
package main

import (
    "slices"
    "testing"
    "time"

    "gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/docker"
    sandboxmsb "gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/msb"
)

func TestImagePrune(t *testing.T) {
    t.Run("accepts and applies age flag", func(t *testing.T) {
        // A 2d-old orphan image: pruned with --age 7d.
        m := &sandboxmsb.MockMsbClient{
            Images: []sandboxmsb.ImageHandle{
                sandboxmsb.MockImageHandle{Reference_: "opencode-sandbox/runner-orphan:v1", LastUsedAt_: time.Now().Add(-2 * 24 * time.Hour)},
            },
        }
        cmd, ui := setupCommandFixtures(t, "image", "prune", "--age", "7d")
        sandboxmsb.WithMsbMock(t, m)
        docker.WithNoopDockerMock(t)
        if err := cmd.Execute(); err != nil {
            t.Fatalf("unexpected error: %v", err)
        }
        if !slices.Contains(ui.OutCalls, "image prune: 1 runner image(s), 0 dangling docker image(s)") {
            t.Errorf("expected prune summary, got: %v", ui.OutCalls)
        }
    })

    t.Run("prunes orphan images", func(t *testing.T) {
        m := &sandboxmsb.MockMsbClient{
            Images: []sandboxmsb.ImageHandle{
                sandboxmsb.MockImageHandle{Reference_: "opencode-sandbox/runner-orphan:v1", LastUsedAt_: time.Now().Add(-30 * 24 * time.Hour)},
            },
        }
        cmd, ui := setupCommandFixtures(t, "image", "prune")
        sandboxmsb.WithMsbMock(t, m)
        docker.WithNoopDockerMock(t)
        if err := cmd.Execute(); err != nil {
            t.Fatalf("unexpected error: %v", err)
        }
        if !slices.Contains(ui.OutCalls, "image prune: 1 runner image(s), 0 dangling docker image(s)") {
            t.Errorf("expected prune summary, got: %v", ui.OutCalls)
        }
    })
}
```

`cmd/opencode-sandbox/cli_volume_prune_test.go`:

```go
package main

import (
    "slices"
    "strings"
    "testing"
    "time"

    "gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/docker"
    "gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/msb"
)

func TestVolumePrune(t *testing.T) {
    t.Run("prunes orphan home volumes", func(t *testing.T) {
        m := &msb.MockMsbClient{
            Volumes: []msb.VolumeHandle{
                &msb.MockVolumeHandle{Name_: "opencode-sandbox-home-orphan-1mjusbm3wikhb0-20260806T143022", CreatedAt_: time.Now().Add(-30 * 24 * time.Hour)},
            },
        }
        cmd, ui := setupCommandFixtures(t, "volume", "prune")
        msb.WithMsbMock(t, m)
        docker.WithNoopDockerMock(t)
        if err := cmd.Execute(); err != nil {
            t.Fatalf("unexpected error: %v", err)
        }
        if !slices.Contains(ui.OutCalls, "volume prune: 1 home volume(s)") {
            t.Errorf("expected prune summary, got: %v", ui.OutCalls)
        }
    })

    t.Run("invalid age error", func(t *testing.T) {
        cmd, _ := setupCommandFixtures(t, "volume", "prune", "--age", "invalid")
        msb.WithMsbMock(t, &msb.MockMsbClient{})
        err := cmd.Execute()
        if err == nil {
            t.Fatal("expected error for invalid age")
        }
        if !strings.Contains(err.Error(), "invalid age") {
            t.Errorf("expected invalid age error, got: %v", err)
        }
    })
}
```

`cmd/opencode-sandbox/cli_sandbox_prune_test.go`:

```go
package main

import (
    "slices"
    "testing"
    "time"

    msbSdk "github.com/superradcompany/microsandbox/sdk/go"

    "gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/msb"
)

func TestSandboxPrune(t *testing.T) {
    t.Run("prunes stale sandbox", func(t *testing.T) {
        m := &msb.MockMsbClient{
            Sandboxes: []msb.SandboxHandle{
                &msb.MockSandboxHandle{Name_: "opencode-sandbox-vm-proj-1mjusbm3wikhb0", Status_: msbSdk.SandboxStatusStopped, UpdatedAt_: time.Now().Add(-30 * 24 * time.Hour)},
            },
        }
        cmd, ui := setupCommandFixtures(t, "sandbox", "prune")
        msb.WithMsbMock(t, m)
        if err := cmd.Execute(); err != nil {
            t.Fatalf("unexpected error: %v", err)
        }
        if !slices.Contains(ui.OutCalls, "sandbox prune: 1 sandbox(es)") {
            t.Errorf("expected prune summary, got: %v", ui.OutCalls)
        }
    })
}
```

These files reference `slices` (and `strings` for the volume test); the imports above include them.

- [ ] **Step 6: Run tests and fix**

Run: `go test ./cmd/opencode-sandbox/ -run 'TestImagePrune|TestVolumePrune|TestSandboxPrune|TestPrune'`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add cmd/opencode-sandbox/
git commit -m "feat(cli): add image/volume/sandbox prune subcommands"
```

---

### Task 8: Update docs and run full verification

**Files:**
- Modify: `docs/commands.md`, `docs/configuration.md`, `docs/sandboxes.md`, `README.md`

- [ ] **Step 1: Update docs**

- `docs/configuration.md`: under `manual-prune-age`, note it is the default for `prune`, `image prune`, `volume prune`, and `sandbox prune`. Under `auto-prune-age`, note it applies to all three artifact types at 30d.
- `docs/commands.md`: add `image prune`, `volume prune`, `sandbox prune` entries with their flags (`image prune`: `--age`, `--dry-run`, `--all`; `volume prune`: `--age`, `--dry-run`, `--all`; `sandbox prune`: `--age`, `--dry-run`). Note the aggregate `prune` summary now reads `Pruned N VMs, N home volumes, N docker images, N msb images`.
- `docs/sandboxes.md`: update the prune description (auto-prune and manual thresholds apply uniformly to VMs, volumes, and images; clone volumes removed).
- `README.md`: if it mentions prune summary format or clone volumes, align it.

- [ ] **Step 2: Run full verification**

Run: `make check`
Expected: fmt, lint, and all tests pass. If the linter flags exported methods without doc comments (e.g. `PruneImages`, `PruneVolumes`, `PruneVMs`, `BuildLiveState`, report types), add concise doc comments.

- [ ] **Step 3: Commit**

```bash
git add -A
git commit -m "docs: document artifact prune subcommands and age semantics"
```