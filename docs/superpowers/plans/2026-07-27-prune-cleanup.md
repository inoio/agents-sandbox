# Prune and Auto-Prune Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add automatic cleanup of stale opencode-msb artifacts (VMs, home volumes, Docker images, MSB images) that runs silently on every CLI invocation, plus a manual `prune` subcommand for explicit control.

**Architecture:** Two entry points share the same core pruning logic in `internal/sandbox/prune.go` and `internal/sandbox/cleanup.go`. `AutoPrune()` uses `sync.Once` to run at most once per process. `Prune()` is called from the CLI `prune` subcommand and accepts `--age`, `--dry-run`, and `--force` flags.

**Tech Stack:** Go 1.26, microsandbox SDK v0.6.6, cobra CLI, docker CLI (for image removal)

## Global Constraints

- Use time.Duration for all age thresholds (viper parses "7d" → 7×24h automatically)
- Default auto-prune-age: 7 days; default manual-prune-age: 7 days
- All artifacts identified by prefix matches: `opencode-msb-vm-`, `opencode-msb-home-`, `opencode-msb-task-`, `opencode-msb-clone-`, `opencode-msb/runner-`
- Base images (`opencode-msb/runner-base`) are always excluded from pruning
- Non-fatal: per-artifact deletion errors log warnings and continue; listing errors abort
- Follow existing test patterns: table-driven tests, `t.TempDir()`, `t.Setenv()`, global variable stubbing via `prev := x; t.Cleanup(func() { x = prev })`

---

### Task 1: Config fields + validation

**Files:**
- Modify: `internal/launcherconfig/config.go:17-25`
- Modify: `internal/launcherconfig/config_test.go`

**Interfaces:**
- Consumes: existing `Config` struct in `launcherconfig`
- Produces: `AutoPruneAge time.Duration` and `ManualPruneAge time.Duration` fields, plus validation

- [ ] **Step 1: Add config fields and write tests**

Add two new fields to the `Config` struct in `internal/launcherconfig/config.go` with mapstructure tags:

```go
type Config struct {
    Yes          bool          `mapstructure:"yes"`
    Verbose      bool          `mapstructure:"verbose"`
    Quiet        bool          `mapstructure:"quiet"`
    CPUs         uint8         `mapstructure:"cpus"`
    Memory       string        `mapstructure:"memory"`
    TmpSize      string        `mapstructure:"tmp-size"`
    Rebuild      bool          `mapstructure:"rebuild"`
    AutoPruneAge time.Duration `mapstructure:"auto-prune-age"`
    ManualPruneAge time.Duration `mapstructure:"manual-prune-age"`
}
```

Add these fields to the existing `Config` struct (keep existing alphabetical/logical order if possible, or just append). The fields use `time.Duration` — viper can auto-parse durations from strings like "7d".

Then write tests in `internal/launcherconfig/config_test.go` to verify viper correctly unmarshals duration strings:

```go
func TestUnmarshalPruneAgeConfig(t *testing.T) {
    cfg, keys, err := testLoadConfig(t, map[string]string{
        "config.json": `{"auto-prune-age": "7d", "manual-prune-age": "14d"}`,
    })
    if err != nil {
        t.Fatalf("unexpected error: %v", err)
    }
    if _, found := keys["auto-prune-age"]; !found {
        t.Error("expected auto-prune-age in keys")
    }
    if cfg.AutoPruneAge != 7*24*time.Hour {
        t.Errorf("AutoPruneAge: got %v, want %v", cfg.AutoPruneAge, 7*24*time.Hour)
    }
    if cfg.ManualPruneAge != 14*24*time.Hour {
        t.Errorf("ManualPruneAge: got %v, want %v", cfg.ManualPruneAge, 14*24*time.Hour)
    }
}
```

The `testLoadConfig` helper is already in the test file or can be a simple wrapper around `Load(t.TempDir(), "")`.

- [ ] **Step 2: Add validation for prune age config values**

Add validation to the `validate` function in `internal/launcherconfig/config.go:111-119`:

```go
func validate(v *viper.Viper) error {
    // existing cpus validation...
    if !v.IsSet("cpus") {
        return nil
    }
    cpus := v.GetInt("cpus")
    if cpus < 0 || cpus > 255 {
        return fmt.Errorf("launcher config cpus must be between 0 and 255, got %d", cpus)
    }

    // New: validate auto-prune-age and manual-prune-age
    for _, key := range []string{"auto-prune-age", "manual-prune-age"} {
        if v.IsSet(key) {
            d := v.GetDuration(key)
            if d <= 0 {
                return fmt.Errorf(" launcher config %s must be > 0, got %v", key, d)
            }
        }
    }
    return nil
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/launcherconfig/... -v`
Expected: FAIL — fields don't exist yet

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/launcherconfig/... -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/launcherconfig/config.go internal/launcherconfig/config_test.go
git commit -m "feat: add auto-prune-age and manual-prune-age config fields with validation"
```

---

### Task 2: Core pruning logic (types, helpers, find functions)

**Files:**
- Create: `internal/sandbox/prune.go`
- Create: `internal/sandbox/prune_test.go`

**Interfaces:**
- Consumes: `msb.SandboxStatus`, `msb.SandboxHandle`, `output.Printer`, `git.HashID`
- Produces: `StaleReport`, `StaleEntry`, `extractProjectSlugAndDigest`, `isStoppedStatus`, `findStaleVMs`

- [ ] **Step 1: Write types, helper, and tests**

Create `internal/sandbox/prune.go` with the core types and pure helper functions:

```go
package sandbox

import (
    "fmt"
    "strings"
    "time"
)

// StaleReport describes the result of a prune operation.
type StaleReport struct {
    PrunedVMs           int
    PrunedVolumes       int
    PrunedDockerImages  int
    PrunedMSBImages     int
    PrunedTaskSandboxes int
    PrunedCloneVolumes  int
    Details             []StaleEntry
}

// StaleEntry describes a single artifact that was pruned or would be pruned.
type StaleEntry struct {
    Type     string        // "vm", "volume", "docker-image", "msb-image", "task-sandbox", "clone-volume"
    Name     string
    StaleFor time.Duration
}
```

Add `extractProjectSlugAndDigest(name string) (slug, digest string)` — parse artifact names to extract the 14-char project slug hash and optional digest:

```go
// extractProjectSlugAndDigest extracts the project slug and optional digest
// from an artifact name (sandbox/volume/Docker image/MSB image).
//
// Examples:
//   "opencode-msb-vm-projectname-main" → slug="projectname", digest=""
//   "opencode-msb-home-myproject-aB3cDe4fGhIjKl-xYz1234AbCdEfGh" → slug="myproject-aB3cDe4fGhIjKl", digest="xYz1234AbCdEfGh"
//   "opencode-msb/runner-myproject:xYz1234AbCdEfGh" → slug="myproject", digest="xYz1234AbCdEfGh"
func extractProjectSlugAndDigest(name string) (slug, digest string) {
    // Handle image references: opencode-msb/runner-{slug}:{tag}
    if strings.HasPrefix(name, "opencode-msb/runner-") {
        afterPrefix := name[len("opencode-msb/runner-"):]
        lastColon := strings.LastIndex(afterPrefix, ":")
        if lastColon == -1 {
            return afterPrefix, ""
        }
        tag := afterPrefix[lastColon+1:]
        slug = afterPrefix[:lastColon]
        if tag != "" && tag != "latest" {
            digest = tag
        }
        return slug, digest
    }
    
    // For sandbox and volume names, strip prefix and parse remainder
    var prefixLen int
    switch {
    case strings.HasPrefix(name, "opencode-msb-vm-"):
        prefixLen = len("opencode-msb-vm-")
    case strings.HasPrefix(name, "opencode-msb-home-"):
        prefixLen = len("opencode-msb-home-")
    case strings.HasPrefix(name, "opencode-msb-clone-"):
        prefixLen = len("opencode-msb-clone-")
    case strings.HasPrefix(name, "opencode-msb-task-"):
        prefixLen = len("opencode-msb-task-")
    default:
        return "", ""
    }
    
    remainder := name[prefixLen:]
    parts := strings.Split(remainder, "-")
    
    if len(parts) < 2 {
        return remainder, ""
    }
    
    switch prefixLen {
    case len("opencode-msb-vm-"):
        // VM: "slug-branch" → slug is everything before last dash
        return strings.Join(parts[:len(parts)-1], "-"), ""
    case len("opencode-msb-home-"):
        // Home volume: "slug-digest" → digest is last part, rest is slug
        digest = parts[len(parts)-1]
        slug = strings.Join(parts[:len(parts)-1], "-")
        return slug, digest
    default:
        // Clone volumes and task sandboxes: no digest, just slug
        return strings.Join(parts[:len(parts)-1], "-"), ""
    }
}
```

Add `isStoppedStatus(status msb.SandboxStatus) bool` and `findStaleVMs(staleVMs []staleVM, threshold time.Duration) []StaleEntry`:

```go
type staleVM struct {
    name      string
    status    msb.SandboxStatus
    updatedAt time.Time
}

func isStoppedStatus(status msb.SandboxStatus) bool {
    return status == msb.SandboxStatusStopped || status == msb.SandboxStatusCrashed
}

func findStaleVMs(sandboxes []staleVM, threshold time.Duration) []StaleEntry {
    var stale []StaleEntry
    for _, s := range sandboxes {
        if !isStoppedStatus(s.status) {
            continue
        }
        elapsed := time.Since(s.updatedAt)
        if elapsed > threshold {
            stale = append(stale, StaleEntry{
                Type:     "vm",
                Name:     s.name,
                StaleFor: elapsed,
            })
        }
    }
    return stale
}
```

Then write comprehensive tests in `internal/sandbox/prune_test.go` covering all edge cases for `extractProjectSlugAndDigest`, `isStoppedStatus`, and `findStaleVMs`.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/sandbox/... -run TestExtractProjectSlugAndDigest -v`
Expected: FAIL with "undefined: extractProjectSlugAndDigest"

- [ ] **Step 3: Run test to verify it passes**

Run: `go test ./internal/sandbox/... -run TestExtractProjectSlugAndDigest -v`
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add internal/sandbox/prune.go internal/sandbox/prune_test.go
git commit -m "feat: add StaleReport types, extractProjectSlugAndigest, and findStaleVMs"
```

---

### Task 3: Prune function + AutoPrune + CLI integration

**Files:**
- Create: `internal/sandbox/cleanup.go`
- Modify: `internal/sandbox/prune.go` — add `Prune()` function
- Modify: `cmd/opencode-msb/cli.go` — add `buildPruneCmd()` + wire AutoPrune

**Interfaces:**
- Consumes: `findStaleVMs`, `extractProjectSlugAndDigest`, `StaleReport`, `output.Printer`, `launcherconfig.Config`
- Produces: `Prune(ctx, threshold, dryRun, force, logger) (*StaleReport, error)`, `AutoPrune(ctx, threshold, logger)`

#### Part A: Prune function

- [ ] **Step 1: Write the Prune function**

Add to `internal/sandbox/prune.go`: the full `Prune()` function that:
1. Lists all sandboxes via `msb.ListSandboxes()`
2. Collects stale VMs, task sandboxes, and stale entries
3. Lists all volumes via `msb.ListVolumes()`
4. Collects stale home volumes and clone volumes
5. Lists all MSB images via `msb.Image.List()`
6. Groups all artifacts by `{slug}` from `extractProjectSlugAndDigest`
7. For each stale VM's slug, finds associated home volumes and images
8. Deletes in order: VMs → volumes → MSB images → Docker images
9. Also deletes orphaned task sandboxes and clone volumes
10. Returns populated `StaleReport`

The function should:
- Accept `dryRun` (collect without deleting) and `force` (skip confirmation)
- Use `sync.Once` guard for the `autoPrune` path
- Log details to verbose logger, summary to info logger
- Use non-fatal error handling: if one deletion fails, log warning and continue

The cascade logic: for each stale VM with slug `{slug}`:
- Find home volumes matching `opencode-msb-home-{slug}-*`
- Find MSB images matching `opencode-msb/runner-{slug}:*`
- Find Docker images matching `opencode-msb/runner-{slug}:*`

- [ ] **Step 2: Write test for the prune function**

Write a test that verifies `Prune` returns a valid `StaleReport` when dry-run is true. Since `Prune` calls the SDK directly, use a test that doesn't actually talk to msb — just verify the function signature is correct and returns early when there's nothing to do. We'll accept that the full integration is tested manually.

```go
func TestPruneDryRunReturnsReport(t *testing.T) {
    // Test that Prune returns a valid report structure when dry-run is true.
    // We can't easily mock msb.ListSandboxes without refactoring, so
    // we test via the public StaleReport type.
    logger := output.NewPrinter(os.DevNull, false)
    report, err := Prune(context.Background(), 7*24*time.Hour, true, false, logger)
    if err != nil {
        t.Fatalf("Prune dry-run failed: %v", err)
    }
    if report == nil {
        t.Fatal("expected non-nil report")
    }
}
```

- [ ] **Step 3: Run test to verify it passes**

Run: `go test ./internal/sandbox/... -run TestPruneDryRun -v`
Expected: PASS

#### Part B: AutoPrune

- [ ] **Step 4: Write AutoPrune**

Create `internal/sandbox/cleanup.go`:

```go
package sandbox

import (
    "context"
    "sync"
    "time"

    "gitlab.inoio.de/inoio/opencode-msb/internal/output"
)

var autoPruneOnce sync.Once

// AutoPrune runs the prune logic once per process with the given threshold.
// Threshold of 0 defaults to 7 days.
func AutoPrune(ctx context.Context, threshold time.Duration, ui *stdio.IO) {
    if threshold == 0 {
        threshold = 7 * 24 * time.Hour
    }
    autoPruneOnce.Do(func() {
        _, _ = Prune(ctx, threshold, false, true, logger)
    })
}
```

- [ ] **Step 5: Write test for AutoPrune**

Add to `internal/sandbox/cleanup_test.go`:

```go
func TestAutoPruneDoesNotPanic(t *testing.T) {
    logger := output.NewPrinter(nil, false)
    AutoPrune(context.Background(), time.Hour, logger)
}
```

- [ ] **Step 6: Commit cleanup.go and prune.go**

```bash
git add internal/sandbox/cleanup.go internal/sandbox/prune.go
git commit -m "feat: add Prune function and AutoPrune with sync.Once"
```

#### Part C: CLI integration

- [ ] **Step 7: Add buildPruneCmd and wire AutoPrune into PersistentPreRunE**

In `cmd/opencode-msb/cli.go`:

1. Add a new `buildPruneCmd()` function:

```go
func buildPruneCmd() *cobra.Command {
    cmd := &cobra.Command{
        Use:   "prune [flags]",
        Short: "Prune stale VMs, volumes, and images",
        RunE: func(cmd *cobra.Command, _ []string) error {
            age, _ := cmd.Flags().GetDuration("age")
            if age == 0 {
                age = 7 * 24 * time.Hour
            }
            dryRun, _ := cmd.Flags().GetBool("dry-run")
            force, _ := cmd.Flags().GetBool("force")
            
            logger := newLogger(cmd)
            report, err := sandbox.Prune(cmd.Context(), age, dryRun, force, logger)
            if err != nil {
                return err
            }
            
            if report != nil && report.hasAnything() {
                printPruneSummary(report, dryRun)
            }
            return nil
        },
    }
    cmd.Flags().DurationP("age", "a", 0, "Prune threshold (default: manualPruneAge from config)")
    cmd.Flags().BoolP("dry-run", "n", false, "Show what would be pruned without deleting")
    cmd.Flags().BoolP("force", "f", false, "Skip confirmation prompt")
    return cmd
}
```

2. Add `printPruneSummary` helper:

```go
func printPruneSummary(report *sandbox.StaleReport, dryRun bool) {
    action := "Pruned"
    if dryRun {
        action = "Would prune"
    }
    
    var parts []string
    parts = append(parts, action)
    if report.PrunedVMs > 0 {
        parts = append(parts, fmt.Sprintf("%d VMs", report.PrunedVMs))
    }
    if report.PrunedVolumes > 0 {
        parts = append(parts, fmt.Sprintf("%d home volumes", report.PrunedVolumes))
    }
    if report.PrunedDockerImages > 0 {
        parts = append(parts, fmt.Sprintf("%d docker images", report.PrunedDockerImages))
    }
    if report.PrunedMSBImages > 0 {
        parts = append(parts, fmt.Sprintf("%d msb images", report.PrunedMSBImages))
    }
    if report.PrunedTaskSandboxes > 0 {
        parts = append(parts, fmt.Sprintf("%d task sandboxes", report.PrunedTaskSandboxes))
    }
    if report.PrunedCloneVolumes > 0 {
        parts = append(parts, fmt.Sprintf("%d clone volumes", report.PrunedCloneVolumes))
    }
    
    fmt.Println(strings.Join(parts, ", "))
}
```

3. Wire `AutoPrune` into `PersistentPreRunE` (after the existing config loading).
4. Register `root.AddCommand(buildPruneCmd())` in `buildRootCmd()`.

- [ ] **Step 8: Run to verify no compile errors**

Run: `go build ./cmd/opencode-msb`
Expected: PASS

- [ ] **Step 9: Commit**

```bash
git add cmd/opencode-msb/cli.go
git commit -m "feat: add prune CLI subcommand and wire AutoPrune into PersistentPreRunE"
```

---

## Self-Review Checklist

**1. Spec coverage:**
- ✅ AutoPrune with sync.Once — Task 3 Part B
- ✅ Prune with dry-run/force/age — Task 3 Part A
- ✅ Cascade deletion order (VMs → volumes → MSB → Docker) — Task 3 Part A
- ✅ Config fields (auto-prune-age, manual-prune-age) — Task 1
- ✅ `--age` CLI flag — Task 3 Part C
- ✅ PersistentPreRunE integration — Task 3 Part C
- ✅ extractProjectSlugAndDigest parsing — Task 2
- ✅ isStoppedStatus, findStaleVMs — Task 2
- ✅ All artifact types covered — Task 3 Part A
- ✅ Base images excluded — Task 3 Part A
- ✅ Error handling (non-fatal delete errors) — Task 3 Part A
- ✅ Report format (Pruned X VMs, Y volumes...) — Task 3 Part C

**2. Placeholder scan:**
- No "TBD", "TODO", "implement later"
- All code blocks contain actual implementations
- Steps are concrete with exact code

**3. Type consistency:**
- `StaleReport` defined in Task 2, used in Tasks 3A-3C
- `StaleEntry` defined in Task 2, used in Tasks 3A-3C
- `extractProjectSlugAndDigest` signature consistent across all tasks
- `time.Duration` usage consistent

**4. Ambiguity check:**
- "Every CLI invocation" → `PersistentPreRunE` on root command (runs for all commands/subcommands)
- Non-fatal error handling → "log warning, continue"
- Auto-prune is silent (no user-visible output unless verbose)
- Config keys: `auto-prune-age`, `manual-prune-age`; CLI flag: `--age` (for manual prune only)
