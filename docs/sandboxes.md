# Sandboxes

opencode-msb manages sandboxes as ephemeral microsandbox VMs with persistent home volumes. This document explains the
sandbox lifecycle and management.

## Lifecycle Overview

Each opencode-msb invocation follows this lifecycle:

```
- Auto-prune: Remove stale VMs, volumes, and images
- Pre-flight checks (doctor): Docker, KVM, Git, msb
- Image verify/build: `.opencode-msb/Dockerfile` (or default image) → Docker image
- Home volume verify/build: Persistent msb volume (`opencode-msb-home-<slug>-<hash>`) per project
- VM creation or reuse: New or existing project-specific VM
- Provisioning: Copy opencode config etc.
- Daemon verify/start: Ensure/start microsandbox internal services
- Branch resolve: If `-b` specified, create opencode VM-internal worktree via daemon API
- Opencode run: Execute `opencode attach` against the worktree inside the sandbox
- Cleanup: Detach, prune host worktrees
```

## VM Identity

Each project gets a singular VM identified by the project slug (derived from the git remote URL):

```
opencode-msb-vm-<project-slug>
```

For example, a repo at `https://gitlab.inoio.de/inoio/myproject.git` gets:

```
opencode-msb-vm-myproject-<full-path-hash>
```

The VM is **per-project/-directory, not per-invocation**. Subsequent runs connect to the existing VM (or start it if
stopped) rather than creating a new one.

## Volume Lifecycle

Home volumes are persistent storage for the VM's `$HOME` directory. They survive VM destruction and Dockerfile changes.

Home volumes are named `opencode-msb-home-<slug>-<timestamp>`.

When the Dockerfile changes (new image digest), the next run prompts you to keep, migrate, or reset your home volume. Migrate copies files from the old volume; reset creates a fresh volume from the image.

List all volumes:

```console
opencode-msb volume list
```

Manual management:

```console
opencode-msb volume migrate   # new volume, copy files on top
opencode-msb volume reset     # fresh volume from image
opencode-msb volume edit      # new volume alongside old, manual transfer
```

Home volumes are pruned when:

- You run `volume migrate|reset` with `--rm` (removes the old volume)
- `prune` is run with an age threshold older than the volume's last use
- The state file is missing and a fresh volume is created (old volumes become orphaned)

## Idle Timeout

VMs have a 30-second idle timeout by default. If no opencode session is attached, the VM transitions to a stopped state.
When you run opencode-msb again, the launcher detects the stopped VM and starts it.

To stop a VM immediately:

```console
opencode-msb stop
```

To stop and remove state:

```console
opencode-msb stop -f
```

## Concurrency

Only one VM is created per project. Concurrent invocations of opencode-msb share the same VM — additional sessions
connect to the existing VM rather than creating one.

A host-side file lock (`~/.local/state/opencode-msb/vm-ensure/<slug>.lock`) prevents race conditions during first boot
creation.

## Manual Pruning

```console
opencode-msb prune                       # use manual-prune-age from config (default: 7d)
opencode-msb prune -a 24h                # 24-hour threshold
opencode-msb prune --dry-run             # preview only
opencode-msb prune --force               # skip confirmation
```

Pruning removes:

- Stale VMs (stopped/crashed beyond the age threshold)
- Orphaned home volumes
- Unused Docker images
- Unused msb images
- Stale clone volumes

Auto-pruning runs before every command via `sync.Once`. It uses a fixed default threshold of 30 days unless
`auto-prune-age` is set in config.
