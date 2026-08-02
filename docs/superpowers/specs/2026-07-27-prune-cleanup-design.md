# Prune and Auto-Prune Design

## Purpose

Automatic cleanup of stale opencode-msb artifacts (VMs, home volumes, Docker images, MSB images) and manual `prune` command for explicit control.

## Problem

The launcher creates Docker images, MSB images, host-side volumes, and VM sandboxes. When projects change or sessions end, these artifacts accumulate until disk space runs low. No mechanism exists to clean them up.

## Goals

- Auto-prune runs once per CLI invocation, silently removing stale artifacts
- `opencode prune` command with `--dry-run` and `--force` flags for explicit control
- Cascade deletion: removing a VM also removes its home volume, MSB image, and Docker image
- Configurable thresholds via launcher config (`auto-prune-age`, `manual-prune-age`)
- Non-fatal errors: if one artifact fails to delete, continue with the rest

## Artifact Types and Linkage

### Naming Convention

All artifacts are linked by project slug (extracted from name) and image digest hash (last 14 base36 chars in volume/image references).

| Entity | Prefix | Naming Pattern | Example |
|--------|--------|---------------|---------|
| Project VM | `opencode-msb-vm-{slug}-{branchSlug}` | `opencode-msb-vm-proj-aB3cDe4fGhIjKl-main` |
| Home volume | `opencode-msb-home-{slug}-{digest}` | `opencode-msb-home-proj-aB3cDe4fGhIjKl-xYz1234AbCdEfGh` |
| MSB image | `opencode-msb/runner-{slug}:{digest}` | `opencode-msb/runner-proj-aB3cDe4fGhIjKl:xYz1234AbCdEfGh` |
| Docker image | `opencode-msb/runner-{slug}:{latest,\|digest}` | `opencode-msb/runner-proj-aB3cDe4fGhIjKl:latest` |

### Cascade Chain

```
Project VM → Home volume → MSB image → Docker image
(stops VM and removes state)
  ↓
(deletes volume)
  ↓
(removes MSB image)
  ↓
(rm docker image by tag)
```

## Configuration

Settings in launcher config (`~/.config/opencode-msb/config.json5` or `.opencode-msb/config.json5`):

```json
{
  "auto-prune-age": "7d",
  "manual-prune-age": "7d"
}
```

Both default to 7 days. Added to `internal/launcherconfig.Config` struct with `mapstructure` tags.

## Pruning Logic Per Artifact Type

### Project VMs (`opencode-msb-vm-*`)

- **Condition:** status is `Stopped`/`Crashed` AND `UpdatedAt` exceeds threshold
- **Action:** `handle.Delete(ctx)` (stops + removes persisted state)

### Home Volumes (`opencode-msb-home-*`)

- **Condition:** matching stale VM's `{slug}` AND no active VM uses it (active = `UpdatedAt` within threshold)
- **Action:** `handle.Delete(ctx)`

### Task Sandboxes (`opencode-msb-task-*`)

- **Condition:** any `opencode-msb-task-*` sandbox exists (all are crash leftovers)
- **Action:** `handle.Delete(ctx)`

### Clone Volumes (`opencode-msb-clone-*`)

- **Condition:** any `opencode-msb-clone-*` volume exists (deprecated, orphaned since clone-on-use removal)
- **Action:** `handle.Delete(ctx)`

### MSB Images (`opencode-msb/runner-{slug}:*`)

- **Condition:** No VM exists for the project `{slug}` (Case 3 - orphan cleanup), or unused digest when VM exists (Case 2 - active VM cleanup), or cascade from stale VM (Case 1 - stale VM cascade)
- **Action:** `msb.Image.Remove(ctx, ref)`, fallback: `msb.Image.Remove(ctx, ref+"@"+digest)`
- **:latest behavior:** Kept when any VM exists for the slug; deleted when no VM exists for the slug

### Docker Images (`opencode-msb/runner-{slug}:*`)

- **Condition:** No VM exists for the project `{slug}` (Case 3 - orphan cleanup), or unused digest when VM exists (Case 2 - active VM cleanup), or cascade from stale VM (Case 1 - stale VM cascade)
- **Action:** `docker image rm -f` for both `:latest` and `:{digest}` tags (only delete `:latest` if it still tags the same image reference)
- **:latest behavior:** Kept when any VM exists for the slug; deleted when no VM exists for the slug

### Excluded

Base images (`opencode-msb/runner-base`) are **not** pruned — shared across all projects.

## Deletion Order

1. First pass: identify all stale VMs and collect active VM image refs
2. Group artifacts by project slug and digest:
   - `homeBySlugDigest`: home volumes grouped by slug → digest
   - `msbImagesBySlug`: MSB images grouped by slug → digest
3. Delete in three distinct cases:

### Case 1: Stale VM exists (cascade)
- Delete the stale VM
- Cascade-delete all its home volumes for this slug
- Delete all MSB images for this slug (including :latest)
- Delete all Docker images for this slug

### Case 2: Active VM exists (delete unused artifacts only)
- Extract active VM slugs → digest mappings
- For each active VM:
  - Home volumes: delete those NOT matching the VM's digest
  - MSB images: delete unused ones; keep :latest and matching digest
  - Docker images: same as MSB images

### Case 3: No VM for slug (orphan cleanup, no age threshold)
- For slugs with no VM at all:
  - Delete all home volumes
  - Delete all MSB images (including :latest)
  - Delete all Docker images

4. Task sandboxes and clone volumes deleted independently of VM cascade

## Command Interface

### Manual `prune` command

```
opencode prune [flags]
  --age DURATION     Manual prune threshold (default: value of manualPruneAge config, 7d)
  --dry-run          Show what would be deleted without deleting
  --force            Skip confirmation prompt
```

### Auto-prune trigger

- Runs once per CLI invocation via `sync.Once` in `internal/sandbox/cleanup.go`
- Triggered by **every** CLI command (root `PersistentPreRunE` in `cli.go`)
- Details logged to verbose logger, summary logged to info level
- Same logic as manual prune, but silently executes

### Report format

```
Pruned 2 VMs, 2 home volumes, 3 docker images, 0 msb images
Deleted 2 orphaned task sandboxes
Deleted 1 orphaned clone volume
```

## Error Handling

- Listing errors: abort — cannot prune without data
- Per-artifact deletion errors: log warning, continue with rest, report success/failure per item
- Dry-run: no-delete mode, populate `StaleReport` only

## Implementation Structure

```
internal/sandbox/
  cleanup.go    ← AutoPrune() + sync.Once guard
  prune.go      ← Prune() + per-type pruning functions, StaleReport, StaleEntry
```

### `prune.go` exported API

```go
type StaleReport struct {
    PrunedVMs         int
    PrunedVolumes     int
    PrunedDockerImages int
    PrunedMSBImages   int
    PrunedTaskSandboxes int
    PrunedCloneVolumes  int
    Details           []StaleEntry
}

type StaleEntry struct {
    Type     string
    Name     string
    StaleFor time.Duration
}

func Prune(ctx context.Context, threshold time.Duration, dryRun, force bool, ui *stdio.IO) (*StaleReport, error)
```

### `cleanup.go` exported API

```go
func AutoPrune(ctx context.Context, threshold time.Duration, ui *stdio.IO)
```

### Helper Functions

```go
func extractProjectSlugAndDigest(name string) (slug, digest string)
// "opencode-msb-vm-project-aB3cDe4fGhIjKl-main" → slug="project-aB3cDe4fGhIjKl", digest="main"
// "opencode-msb-vm-saife-1mjusbm3wikhb0" → slug="saife-1mjusbm3wikhb0", digest=""
// "opencode-msb/home-myproject-aB3cDe4fGhIjKl-xYz1234AbCdEfGh" → slug="myproject-aB3cDe4fGhIjKl", digest="xYz1234AbCdEfGh"
// "opencode-msb/runner-project-aB3cDe4fGhIjKl:xYz1234AbCdEfGh" → slug="project-aB3cDe4fGhIjKl", digest="xYz1234AbCdEfGh"
```

## Test Strategy

Unit tests on pure functions with mocks:
- `findStaleVMs()` — no stale, some stale, all stale
- `findStaleVolumes()` — matching volumes found, no matching volumes, volumes still in use
- `findStaleDockerImages()` — images exist, images gone, shared base images excluded
- `findStaleMSBImages()` — images exist, images gone
- `extractProjectSlugAndDigest()` — edge cases: multiple dashes in slug, branch names with hyphens

---
