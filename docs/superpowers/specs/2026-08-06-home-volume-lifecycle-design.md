# Home Volume Lifecycle Design

The home volume is no longer tied to the Docker image digest. It is independent, identified by project slug only, and persists user data across image changes.

---

## Problem

The home volume name is constructed from the project slug and the Docker image digest (`opencode-msb-home-{slug}-{digestHash}`). When the image changes (Dockerfile update, base image change), a new volume is created and the old volume — which may contain tools, configs, and history the user installed — becomes obsolete.

This is wasteful and disruptive. Users expect their home directory to survive image updates.

## Decision

Home volumes are now owned by the project slug, not by the image digest. When the image changes, the user is presented with a choice: keep the existing volume, migrate/reset to a fresh one, or quit. An old volume is only deleted when no VM in the system references it.

---

## Storage

### State file

A per-project state file lives in `~/.local/state/opencode-msb/{project-slug}/home-volume` and contains one line: the name of the current home volume.

The state directory is created on first run and removed during prune when its volume is deleted.

### Volume naming

Volume names remain `opencode-msb-home-{slug}-{digestHash}`. They are differentiated by slug, not by digest. The digest hash in the name is informational (tells which image was current at creation time). Multiple volumes per slug can exist, each from a different image build.

---

## Resolution flow

1. On `run`, compute new image digest and call `ResolveHomeVolume()`.
2. `ResolveHomeVolume()` loads the state file. If found, it references the existing volume. If not found, it calls `EnsureNewHome()` which creates a new volume from the image and writes the state file.
3. `ResolveHomeAction()` runs the prompt when the image digest differs from the one stored at the time the state file was written:
   - **1) keep** — continue with existing volume, no state change
   - **2) migrate** — create new volume from image, copy all files from old volume
   - **3) reset** — create new volume from image, discard old volume contents
   - **4) quit** — exit session, hint about `volume migrate|reset|edit`

Non-interactive mode (CI, scripts, `--yes`): default is "keep" (use existing volume).

### Example interaction

```
$ opencode-msb run
Docker image changed for project. The image's home directory is different from your current one.
  1) keep      — continue with existing home volume
  2) migrate   — create fresh volume, copy all files on top
  3) reset     — replace with fresh volume from image (lose local changes)
  4) quit      — exit without starting a session
  default [1]:
```

---

## New CLI: `opencode-msb volume`

A subcommand group for manual home volume operations.

### `volume migrate [volume-name]`

1. If no volume-name given, load from state file and warn if missing.
2. Create new home volume: name = `opencode-msb-home-{slug}-{newDigestHash}`, contents = image's `/home/dev` + all files from old volume copied on top (user files win).
3. Update state file to point to new volume.
4. `--rm` flag: delete old volume after successful migration.

### `volume reset [volume-name]`

1. If no volume-name given, load from state file and warn if missing.
2. Create new home volume: name = `opencode-msb-home-{slug}-{newDigestHash}`, contents = image's `/home/dev` only.
3. Update state file to point to new volume.
4. `--rm` flag: delete old volume after reset.

### `volume edit [volume-name]`

1. If no volume-name given, load from state file and warn if missing.
2. Create new volume at the image's default path.
3. Spawn a shell session with both old and new volumes available for manual transfer.
4. `--rm` flag: delete old volume after user exits shell.

### `volume list` (existing)

Already exists, lists all home volumes with name, path, kind.

### `volume info`

Optional future extension: show last-used volume, all volumes, age, and state file status.

---

## Pruning

### Removed logic

Remove `pruneActiveVMHomeVolumes()`. It no longer prunes non-matching-digest volumes for active VMs — those volumes are legitimate and may be in use.

### New orphan cleanup

`pruneOrphanSlug()` already cleans ALL home volumes for project slugs with no VM. This naturally catches displaced volumes when a user migrates or resets — the old volume has no VM referencing it and will be cleaned in the next prune run.

### State file cleanup

When a home volume is deleted by any prune phase, also delete:
1. The state file at `~/.local/state/opencode-msb/{slug}/home-volume`
2. The state directory `~/.local/state/opencode-msb/{slug}/`
3. The parent `~/.local/state/opencode-msb/` if empty

No orphan state files.

### Active VM cleanup

Each active VM tracks its own home volume. On prune, check if the active VM's home volume exists. If not (e.g. user ran `volume reset` externally), create a new one. Do not touch other home volumes for the same slug.

### Prune catalog

Extend `PruningCatalog` to also include stale volumes whose associated VMs have all been removed.

---

## Active session check

When the user chooses migrate/reset, or runs the `volume migrate/reset/edit` CLI, check for active or stale VMs using that project slug. If any exist: block with an error and hint to quit all sessions. No `--yes` bypass for this — safety matters.

This check reuses `prune.listActiveVMs()` and `prune.listStaleVMs()` (or equivalent).

---

## Error handling

| Scenario | Action |
|---|---|
| State file missing | Treat as first-ever run, create fresh from image, write state |
| State file corrupted | Warn "corrupted state file, creating fresh", create from image |
| Volume referenced by state does not exist | Create fresh from image, warn, continue |
| Docker image build fails for prefill | Fail with clear error, leave existing volume intact |
| Permission denied on volume mount/create | Surface msb error, no retry |
| Active/stale VMs when migrating | Error: "session still running, quit first", abort |
| No matching volume found after image change | Prompt as usual with full choice set |

Recovery: corrupted state can always be fixed by manually removing `~/.local/state/opencode-msb/{slug}/`.

---

## Testing

### Unit tests

- `TestResolveHomeVolume_new` — no state file, creates from image
- `TestResolveHomeVolume_existing` — state file found, returns existing volume
- `TestResolveHomeAction_migrate` — prompt shows, user selects migrate
- `TestResolveHomeAction_keep` — prompt shows, user selects keep
- `TestResolveHomeAction_quit` — prompt shows, user selects quit, session exits
- `TestResolveHomeAction_interactive_fallback` — non-interactive mode respects --yes/--no
- `TestVolumeMigrate` — creates new volume, copies files
- `TestVolumeReset` — creates new volume, no copy
- `TestVolumeEdit` — creates new volume, spawns shell with both mounted
- `TestVolumeCLI_noArgs_stateNotFound` — no args, state file missing, warns
- `TestPrune_orphanHomeVolume` — old volume with no VM is cleaned up

### Integration tests (in existing `integration_test.go`)

- First run: image change triggers prompt (use Mock UI with fixed selection)
- Non-image-change run: no prompt, existing volume reused
- After `volume migrate`, session picks up new volume on next run
- After `volume reset`, old volume is gone, state file updated
- Prune: displaced volume (no VM) is cleaned, state file removed

### CLI tests (in `cli_volume_test.go`)

- `volume migrate --help` passes
- `volume migrate` with state file and `--rm` deletes old volume
- `volume reset` with state file and `--rm` deletes old volume
- `volume edit` with state file creates new volume, spawns shell

---

## Files changed

| File | Change |
|---|---|
| `internal/sandbox/volumes.go` | Home volume resolution, create, state file management, migrate/reset/edit logic |
| `internal/sandbox/prune.go` | Remove digest-matching cleanup, add state file cleanup |
| `cmd/opencode-msb/volume.go` | CLI subcommands wiring |
| `cmd/opencode-msb/cli_lifecycle_test.go` | CLI tests for new volume subcommands |
| `internal/termio/prompt.go` | No changes, `Select()` is already suitable |
| `internal/sandbox/runner.go` | Wire prompt call into `prepareSandbox()` |
| `docs/superpowers/specs/` | This design doc |