# Sandboxes

opencode-msb manages sandboxes as ephemeral microsandbox VMs with persistent home volumes. This document explains the sandbox lifecycle and management.

## Lifecycle Overview

Each opencode-msb invocation follows this lifecycle:

```
1. Pre-flight checks (doctor): Docker, KVM, Git, msb
2. Image build/verify: `.opencode-msb/Dockerfile` → Docker image
3. Home volume: Persistent msb volume (`opencode-msb-home-<slug>-<hash>`) per project
4. VM creation or reuse: New or existing project-specific VM
5. Provisioning: Copy opencode config, remove `.envrc`
6. Daemon startup: Start microsandbox internal services
7. Branch resolve: If `-b` specified, create opencode VM-internal worktree via daemon API
8. Opencode run: Execute `opencode attach` against the worktree inside the sandbox
9. Cleanup: Detach, prune host worktrees
10. Auto-prune: Remove stale VMs, volumes, and images
```

## VM Identity

Each project gets a singular VM identified by the project slug (derived from the git remote URL):

```
opencode-msb-vm-<project-slug>
```

For example, a repo at `https://gitlab.inoio.de/inoio/myproject.git` gets:

```
opencode-msb-vm-inoio-myproject
```

The VM is **per-project, not per-invocation**. Subsequent runs connect to the existing VM (or start it if stopped) rather than creating a new one.

## Image Hashing

The home volume name and image tag include a hash of the Docker image digest:

```
opencode-msb-home-<project-slug>-<image-hash>
```

This means:
- Changing the Dockerfile → new image digest → new home volume (fresh state)
- Not changing the Dockerfile → same digest → reuse existing home volume (preserved state)

## Volume Management

Home volumes are persistent storage for the VM's `$HOME` directory. They survive VM destruction.

List all volumes:

```console
opencode-msb volume list
```

Home volumes are managed automatically. They are pruned when:
- The project's Dockerfile is modified (old volumes become orphaned)
- `prune` is run with an age threshold older than the volume's last use

## Idle Timeout

VMs have a 30-second idle timeout by default. If no opencode session is attached, the VM transitions to a stopped state. When you run opencode-msb again, the launcher detects the stopped VM and starts it.

To stop a VM immediately:

```console
opencode-msb stop
```

To stop and remove state:

```console
opencode-msb stop -f
```

## Concurrency

Only one VM is created per project. Concurrent invocations of opencode-msb share the same VM — additional sessions connect to the existing VM rather than creating one.

A host-side file lock (`~/.local/state/opencode-msb/vm-ensure/<slug>.lock`) prevents race conditions during first boot creation.

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

Auto-pruning runs before every command via `sync.Once`. It uses a fixed default threshold of 30 days unless `auto-prune-age` is set in config.
