# Dry-Run Flag Refactor Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Refactor `--dry-run` and `--dry-run-vm` flags to have clear, consistent semantics, remove unused flags from commands, and centralize the implication logic in cli.go.

**Architecture:** Centralize flag implication in cli.go (if dryRun then dryRunVM = true), remove --dry-run-vm from build/stop/kill/prune commands, standardize message format across all operations.

**Tech Stack:** Go, cobra CLI framework

## Global Constraints

- `--dry-run` implies `--dry-run-vm` (centralized in cli.go)
- Message format: `dry-run: Would <action> <target>`
- Clear if/else branching for dry-run vs actual operations
- Read-only commands (list, config show, image list, volume list, doctor) have no dry-run flags

---

### Task 1: Add flag implication logic in cli.go run/shell commands

**Files:**
- Modify: `cmd/opencode-msb/cli.go`

**Interfaces:**
- Consumes: `--dry-run` and `--dry-run-vm` flag values from cobra
- Produces: Modified `opts.DryRunVM` in RunOptions (set to true if opts.DryRun is true)

- [ ] **Step 1: Modify buildRunCmd to add flag implication**

In the RunE function, after parsing flags and before calling `sandbox.Run`, add:

```go
// --dry-run implies --dry-run-vm
if opts.DryRun {
    opts.DryRunVM = true
}
```

Location: Around line 318-319, after parsing flags, before calling `sandbox.Run`.

- [ ] **Step 2: Modify buildShellCmd to add flag implication**

In the RunE function, after parsing flags and before calling `sandbox.Shell`, add:

```go
// --dry-run implies --dry-run-vm
if opts.DryRun {
    opts.DryRunVM = true
}
```

Location: Around line 471-472, remove the existing implication check (lines 473-475) and replace with the above.

- [ ] **Step 3: Run tests to verify existing behavior still works**

```bash
go test ./cmd/opencode-msb/... -v
```

Expected: All tests pass.

- [ ] **Step 4: Commit**

```bash
git add cmd/opencode-msb/cli.go
git commit -m "feat: add --dry-run implies --dry-run-vm logic in cli.go"
```

---

### Task 2: Remove --dry-run-vm flag from build command

**Files:**
- Modify: `cmd/opencode-msb/cli.go`
- Modify: `internal/sandbox/image.go`
- Modify: `internal/sandbox/runner.go`

**Interfaces:**
- Consumes: Flag parsing from cobra
- Produces: Simplified BuildImage signature (remove dryRunVM parameter)

- [ ] **Step 1: Remove --dry-run-vm flag from buildBuildCmd**

Remove line 302:
```go
cmd.Flags().Bool("dry-run-vm", false, "Skip VM lifecycle (no effect on build)")
```

Update the flag parsing in RunE (around line 294-295):
```go
// BEFORE:
dryRun, _ := cmd.Flags().GetBool("dry-run")
dryRunVM, _ := cmd.Flags().GetBool("dry-run-vm")
return sandbox.BuildImage(cmd.Context(), force, dryRun, dryRunVM, logger)

// AFTER:
dryRun, _ := cmd.Flags().GetBool("dry-run")
return sandbox.BuildImage(cmd.Context(), force, dryRun, logger)
```

- [ ] **Step 2: Update BuildImage function signature**

In `internal/sandbox/runner.go`, line 384:

```go
// BEFORE:
func BuildImage(ctx context.Context, force, dryRun, dryRunVM bool, ui *stdio.IO) error

// AFTER:
func BuildImage(ctx context.Context, force, dryRun bool, ui *stdio.IO) error
```

Update the dry-run check (lines 385-388):

```go
// BEFORE:
if dryRun || dryRunVM {
    logger.Infof("dry run: would build runner image")
    return nil
}

// AFTER:
if dryRun {
    logger.Infof("dry-run: Would build runner image")
    return nil
}
```

- [ ] **Step 3: Run tests**

```bash
go test ./... -v
```

Expected: All tests pass.

- [ ] **Step 4: Commit**

```bash
git add cmd/opencode-msb/cli.go internal/sandbox/runner.go
git commit -m "refactor: remove --dry-run-vm from build command"
```

---

### Task 3: Remove --dry-run-vm flag from stop and kill commands

**Files:**
- Modify: `cmd/opencode-msb/cli.go`
- Modify: `internal/sandbox/projectvm.go`

**Interfaces:**
- Consumes: Flag parsing from cobra
- Produces: Simplified stopOrKillProjectVM and related function signatures

- [ ] **Step 1: Remove --dry-run-vm flag from buildStopCmd**

Remove line 639:
```go
cmd.Flags().Bool("dry-run-vm", false, "Show what would be stopped without stopping")
```

Update the flag parsing in RunE (around line 631-634):

```go
// BEFORE:
dryRun, _ := cmd.Flags().GetBool("dry-run")
dryRunVM, _ := cmd.Flags().GetBool("dry-run-vm")
logger := newLogger(cmd)
return sandbox.StopProjectVM(cmd.Context(), force, dryRun, dryRunVM, logger)

// AFTER:
dryRun, _ := cmd.Flags().GetBool("dry-run")
logger := newLogger(cmd)
return sandbox.StopProjectVM(cmd.Context(), force, dryRun, logger)
```

- [ ] **Step 2: Remove --dry-run-vm flag from buildKillCmd**

Remove line 660:
```go
cmd.Flags().Bool("dry-run-vm", false, "Show what would be killed without killing")
```

Update the flag parsing in RunE (around line 651-655):

```go
// BEFORE:
dryRun, _ := cmd.Flags().GetBool("dry-run")
dryRunVM, _ := cmd.Flags().GetBool("dry-run-vm")
logger := newLogger(cmd)
return sandbox.KillProjectVM(cmd.Context(), force, dryRun, dryRunVM, logger)

// AFTER:
dryRun, _ := cmd.Flags().GetBool("dry-run")
logger := newLogger(cmd)
return sandbox.KillProjectVM(cmd.Context(), force, dryRun, logger)
```

- [ ] **Step 3: Update stopOrKillProjectVM signature**

In `internal/sandbox/projectvm.go`, line 301:

```go
// BEFORE:
func stopOrKillProjectVM(
    ctx context.Context,
    remove bool,
    dryRun, dryRunVM bool,
    ui *stdio.IO,
    action, actionVerb string,
    stopFn func(*msb.SandboxHandle, context.Context) error,
) error

// AFTER:
func stopOrKillProjectVM(
    ctx context.Context,
    remove bool,
    dryRun bool,
    ui *stdio.IO,
    action, actionVerb string,
    stopFn func(*msb.SandboxHandle, context.Context) error,
) error
```

- [ ] **Step 4: Update dry-run logic in stopOrKillProjectVM**

In `internal/sandbox/projectvm.go`, lines 321-344:

```go
// BEFORE:
if dryRun {
    actionWord := "Would stop"
    if action == "kill" {
        actionWord = "Would kill"
    }
    if remove {
        logger.Infof("%s project VM: %s (also would remove persisted state)", actionWord, name)
    } else {
        logger.Infof("%s project VM: %s", actionWord, name)
    }
    return nil
}
if dryRunVM {
    actionWord := "Would stop"
    if action == "kill" {
        actionWord = "Would kill"
    }
    if remove {
        logger.Infof("%s project VM: %s (also would remove persisted state)", actionWord, name)
    } else {
        logger.Infof("%s project VM: %s", actionWord, name)
    }
    return nil
}

// AFTER:
if dryRun {
    actionWord := "Would stop"
    if action == "kill" {
        actionWord = "Would kill"
    }
    if remove {
        logger.Infof("dry-run: %s project VM: %s (also would remove persisted state)", actionWord, name)
    } else {
        logger.Infof("dry-run: %s project VM: %s", actionWord, name)
    }
    return nil
}
```

- [ ] **Step 5: Update StopProjectVM and KillProjectVM signatures**

In `internal/sandbox/projectvm.go`, lines 365 and 372:

```go
// BEFORE:
func StopProjectVM(ctx context.Context, remove, dryRun, dryRunVM bool, ui *stdio.IO) error
func KillProjectVM(ctx context.Context, remove, dryRun, dryRunVM bool, ui *stdio.IO) error

// AFTER:
func StopProjectVM(ctx context.Context, remove, dryRun bool, ui *stdio.IO) error
func KillProjectVM(ctx context.Context, remove, dryRun bool, ui *stdio.IO) error
```

Update the calls within these functions:

```go
// In StopProjectVM (line 366-367):
// BEFORE:
return stopOrKillProjectVM(ctx, remove, dryRun, dryRunVM, logger, "stop", "Stopping", ...)

// AFTER:
return stopOrKillProjectVM(ctx, remove, dryRun, logger, "stop", "Stopping", ...)

// In KillProjectVM (line 373-374):
// BEFORE:
return stopOrKillProjectVM(ctx, remove, dryRun, dryRunVM, logger, "kill", "Force-killing", ...)

// AFTER:
return stopOrKillProjectVM(ctx, remove, dryRun, logger, "kill", "Force-killing", ...)
```

- [ ] **Step 6: Run tests**

```bash
go test ./... -v
```

Expected: All tests pass.

- [ ] **Step 7: Commit**

```bash
git add cmd/opencode-msb/cli.go internal/sandbox/projectvm.go
git commit -m "refactor: remove --dry-run-vm from stop and kill commands"
```

---

### Task 4: Remove --dry-run-vm flag from prune command

**Files:**
- Modify: `cmd/opencode-msb/cli.go`
- Modify: `internal/sandbox/prune.go`

**Interfaces:**
- Consumes: Flag parsing from cobra
- Produces: Simplified Prune function signature

- [ ] **Step 1: Remove --dry-run-vm flag from buildPruneCmd**

Remove line 709:
```go
cmd.Flags().Bool("dry-run-vm", false, "Suppress VM deletion during prune")
```

Update the flag parsing in RunE (around line 693-697):

```go
// BEFORE:
dryRun, _ := cmd.Flags().GetBool("dry-run")
dryRunVM, _ := cmd.Flags().GetBool("dry-run-vm")
force, _ := cmd.Flags().GetBool("force")
logger := newLogger(cmd)
report, err := sandbox.Prune(cmd.Context(), age, dryRun, dryRunVM, force, logger)

// AFTER:
dryRun, _ := cmd.Flags().GetBool("dry-run")
force, _ := cmd.Flags().GetBool("force")
logger := newLogger(cmd)
report, err := sandbox.Prune(cmd.Context(), age, dryRun, force, logger)
```

- [ ] **Step 2: Update Prune function signature**

In `internal/sandbox/prune.go`, line 207:

```go
// BEFORE:
func Prune(
    ctx context.Context,
    threshold time.Duration,
    dryRun, dryRunVM, force bool,
    ui *stdio.IO,
) (*StaleReport, error)

// AFTER:
func Prune(
    ctx context.Context,
    threshold time.Duration,
    dryRun, force bool,
    ui *stdio.IO,
) (*StaleReport, error)
```

- [ ] **Step 3: Update prune logic to remove dryRunVM references**

In `internal/sandbox/prune.go`, find all references to `dryRunVM`:

1. Around line 361-370 (VM deletion logic):
```go
// BEFORE:
if !dryRun && !dryRunVM {
    if err := msb.RemoveSandbox(ctx, entry.Name); err != nil {
        logger.Warnf("failed to remove stale VM %s: %v", entry.Name, err)
        continue
    }
} else {
    if dryRunVM {
        logger.Debugf("VM deletion skipped (--dry-run-vm): %s", entry.Name)
    }
}

// AFTER:
if !dryRun {
    if err := msb.RemoveSandbox(ctx, entry.Name); err != nil {
        logger.Warnf("failed to remove stale VM %s: %v", entry.Name, err)
        continue
    }
}
```

2. Update all other `if !dryRun` checks to remove references to `dryRunVM`. The logic should be: if `dryRun` is true, don't delete anything; if false, delete everything.

- [ ] **Step 4: Run tests**

```bash
go test ./... -v
```

Expected: All tests pass.

- [ ] **Step 5: Commit**

```bash
git add cmd/opencode-msb/cli.go internal/sandbox/prune.go
git commit -m "refactor: remove --dry-run-vm from prune command"
```

---

### Task 5: Standardize dry-run message format

**Files:**
- Modify: `internal/sandbox/runner.go`
- Modify: `internal/sandbox/projectvm.go`
- Modify: `internal/sandbox/prune.go`
- Modify: `internal/sandbox/volumes.go`

**Interfaces:**
- Consumes: Logger and operation context
- Produces: Standardized "dry-run: Would ..." messages

- [ ] **Step 1: Update messages in runner.go**

Find and update all dry-run messages:

```go
// Line 340: Change from "dry run: would run opencode"
logger.Infof("dry-run: Would run opencode")

// Line 344: Change from "dry run: would start opencode in VM"
logger.Infof("dry-run: Would start opencode in VM")

// Line 370: Change from "dry run: would start interactive shell session"
logger.Infof("dry-run: Would start interactive shell session")

// Line 374: Change from "dry run: would start interactive shell session"
logger.Infof("dry-run: Would start interactive shell session")

// Line 386: Change from "dry run: would build runner image"
logger.Infof("dry-run: Would build runner image")
```

- [ ] **Step 2: Update messages in projectvm.go**

```go
// Line 95: Change from "dry run: VM lifecycle skipped (--dry-run-vm)"
logger.Debugf("dry-run: VM lifecycle skipped")
```

- [ ] **Step 3: Update messages in volumes.go**

```go
// Line 50: Change from "home volume prefill skipped (--dry-run-vm)"
vm.logger.Infof("dry-run: Would prefill home volume")
```

- [ ] **Step 4: Update messages in prune.go**

Update any debug/info messages related to dry-run to use the standardized format.

- [ ] **Step 5: Update printPruneSummary message prefix**

In `cmd/opencode-msb/cli.go`, line 714-730:

```go
// BEFORE:
action := "Pruned"
if dryRun {
    action = "Would prune"
}

// AFTER:
action := "Pruned"
if dryRun {
    action = "dry-run: Would prune"
}
```

- [ ] **Step 6: Run tests**

```bash
go test ./... -v
```

Expected: All tests pass.

- [ ] **Step 7: Commit**

```bash
git add cmd/opencode-msb/cli.go internal/sandbox/runner.go internal/sandbox/projectvm.go internal/sandbox/volumes.go internal/sandbox/prune.go
git commit -m "style: standardize dry-run message format to 'dry-run: Would ...'"
```

---

### Task 6: Verify clean if/else branching structure

**Files:**
- Review: All modified files

**Interfaces:**
- N/A (verification task)

- [ ] **Step 1: Review runner.go BuildImage function**

Verify the structure is:
```go
if dryRun {
    logger.Infof("dry-run: Would build runner image")
    return nil
}

// Actual build logic here
```

- [ ] **Step 2: Review runner.go Run and Shell functions**

Verify the structure has clear if/else for:
1. `if opts.DryRun` - skip everything including command execution
2. `if opts.DryRunVM && session.sb == nil` - skip VM command execution
3. Else - actual execution

- [ ] **Step 3: Review projectvm.go stopOrKillProjectVM function**

Verify the structure is:
```go
if dryRun {
    logger.Infof("dry-run: ...")
    return nil
}

// Actual stop/kill logic here
```

- [ ] **Step 4: Review prune.go**

Verify all deletion operations follow:
```go
if !dryRun {
    // Perform deletion
}
// Always update report counts
```

- [ ] **Step 5: Run final verification tests**

```bash
go test ./... -v
go run ./cmd/opencode-msb --dry-run --help  # Verify flags are present on run
go run ./cmd/opencode-msb build --help      # Verify --dry-run-vm is gone
go run ./cmd/opencode-msb stop --help      # Verify --dry-run-vm is gone
go run ./cmd/opencode-msb kill --help      # Verify --dry-run-vm is gone
go run ./cmd/opencode-msb prune --help     # Verify --dry-run-vm is gone
```

- [ ] **Step 6: Commit any final adjustments**

```bash
git commit -m "refactor: verify clean if/else branching for dry-run operations"
```

---

## Spec Self-Review

### Coverage Check
- [x] Flag implication logic in cli.go
- [x] Remove --dry-run-vm from build command
- [x] Remove --dry-run-vm from stop command
- [x] Remove --dry-run-vm from kill command
- [x] Remove --dry-run-vm from prune command
- [x] Standardize message format
- [x] Clean if/else branching structure

### Placeholder Scan
- [x] No TBDs or TODOs
- [x] All code examples are concrete
- [x] Line numbers reference current codebase

### Type Consistency
- [x] Function signatures updated consistently
- [x] Flag names consistent
- [x] Message format consistent

## Completion

After all tasks are complete, the dry-run flag refactor is finished. The implementation provides:
1. Clear semantics: --dry-run skips writing operations, --dry-run-vm skips VM lifecycle
2. Proper implication: --dry-run implies --dry-run-vm (centralized in cli.go)
3. Simplified flags: Removed --dry-run-vm from commands where it's redundant
4. Consistent UX: Standardized message format across all operations
5. Clean code: Clear if/else branching for dry-run vs actual operations
