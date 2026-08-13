# Sandboxes

opencode-sandbox manages sandboxes as ephemeral microsandbox VMs with persistent home volumes. This document explains the
sandbox lifecycle and management.

## Lifecycle Overview

Each opencode-sandbox invocation follows this lifecycle:

```
- Auto-prune: Remove stale VMs, volumes, and images
- Pre-flight checks (doctor): Docker, KVM, Git, msb
- Image verify/build: `.opencode-sandbox/Dockerfile` (or default image) → Docker image -> microsandbox image
- Home volume verify/build: Persistent msb volume (`opencode-sandbox-home-<slug>-<timestamp>`) per project
- VM creation or reuse: New or existing project-specific VM
- Provisioning: Copy opencode config etc. (updated configs are re-provisioned).
- Daemon verify/start: Ensure/start microsandbox internal services
- Branch resolve: If `--worktree` (`-w`) specified, reuse or create an opencode VM-internal worktree via daemon API
- Opencode run: Execute `opencode attach` against the worktree inside the sandbox
- Cleanup: Detach
```

## VM Identity

Each project gets a singular VM identified by the project slug (derived from the git remote URL):

```
opencode-sandbox-vm-<project-slug>
```

The project slug consists of the repository name and a hash of the repository host and full path. For example, a repo
cloned from `git@gitlab.inoio.de:inoio/myproject.git` gets:

```
opencode-sandbox-vm-myproject-<hash>
```

Where `<hash>` is the hash of `gitlab.inoio.de:inoio/myproject` (username and extension removed). Every clone and linked
worktree of the same remote shares the same project (and therefore the same VM, volume, and image names).

The VM is **per-project/-directory, not per-invocation**. Subsequent runs connect to the existing VM (or start it if
stopped) rather than creating a new one. When the Docker image's Dockerfile has changed, the existing VM has to be
recreated, but it keeps its name. The home volume is untouched, so no user state is lost (see [Volumes](#volumes)).

## Volumes

### Volume Lifecycle

Home volumes are persistent storage for the VM's `$HOME` directory. They survive VM destruction and Dockerfile changes.

Home volumes are named `opencode-sandbox-home-<slug>-<timestamp>`. Information which home volume is the current one is
stored in a state file (under `~/.local/state/<slug>/state.yaml`).

When the Dockerfile changes (new image digest), most of the times the current home volume can continue to be used. Only
if the Dockerfile changes lead to changes in the home directory, keeping the current home volume might be an issue.
Therefore, when a Dockerfile change actually leads to a VM rebuild (and not just the VM continuing on the current image,
e.g. when the rebuild is deferred to a later run), opencode-sandbox prompts you to decide what to do with the home volume:

- *keep*: Keep on using the home volume
- *reset*: Build a completely new home volume from the new VM's home directory, discarding the current home (e.g. losing opencode session history)
- *migrate*: Build a new home volume like reset, but afterwards copy all files from the current home on top.


### Volume Management

List all volumes:

```console
opencode-sandbox volume list
```

Manual management:

```console
opencode-sandbox volume migrate   # new volume, copy old volume files on top
opencode-sandbox volume reset     # fresh volume from image
opencode-sandbox volume edit      # new volume alongside old, manual transfer in a shell environment
```

Home volumes are pruned when:

- You run `volume migrate|reset` with `--rm` (removes the old volume)
- `prune` is run with an age threshold older than the volume's last use
- The state file is missing and a fresh volume is created (old volumes become orphaned)

## Idle Timeout

By default, `auto-stop-timeout` is set to 10s. When the last opencode client detaches from a project VM, the VM is **not
immediately stopped**. Instead, the launcher evaluates the `auto-stop-on-active-sessions` setting:

- **Default** (`auto-stop-on-active-sessions: false`) — wait-for-quiescence mode. The VM is held while in-flight
  opencode sessions are active. A session is considered quiescent when no session is stuck in `retry` beyond
  `auto-stop-max-session-retries` (default 10). A `busy` session is also treated as quiescent while it has a pending,
  unanswered question (the agent is waiting for user input rather than doing work). Other `busy` sessions are never cut
  off by the reaper (they block the stop). Once all sessions are quiescent, the VM is allowed to stop via the idle
  timeout. Graceful stop preserves the VM and home volume for a fast (<1s) restart.

- **`auto-stop-on-active-sessions: true`** — stop immediately mode. The VM is not held for in-flight agent work; it
  stops promptly via the idle timeout even while sessions are busy.

The idle timeout is configured via `auto-stop-timeout` in launcher config (no CLI flag).
See [Configuration](./configuration.md)
for field details.

A `busy` session that is blocked without an associated pending question (and with no client connected) waits under wait
mode (consistent with "don't interrupt"). Sessions blocked on a pending question are treated as quiescent. Clean up
stale VMs via `prune` or a manual `stop`.

To stop a VM immediately:

```console
opencode-sandbox stop
```

To stop and remove state:

```console
opencode-sandbox stop -f
```

## Concurrency

Only one VM is created per project. Concurrent invocations of opencode-sandbox share the same VM — additional sessions
connect to the existing VM rather than creating one.

A host-side file lock (`~/.local/state/opencode-sandbox/vm-ensure/<slug>.lock`) prevents race conditions during first boot
creation.

When another client is attached, config changes that would restart the opencode daemon or recreate the VM trigger a
prompt asking whether to keep the running server/VM (defer), proceed, or quit (abort the apply). The default is always
to defer — existing sessions are never cut off. See [Configuration](./configuration.md#resource-config-application)
for details on how resource changes are applied and their reconnect semantics.

## Manual Pruning

```console
opencode-sandbox prune                       # use manual-prune-age from config (default: 7d)
opencode-sandbox prune -a 24h                # 24-hour threshold
opencode-sandbox prune --dry-run             # preview only
opencode-sandbox prune --force               # skip confirmation
```

Pruning removes:

- Stale VMs (stopped/crashed beyond the age threshold) and everything they reference
- Orphaned artifacts: home volumes, msb images, and Docker images whose project has no VM at all
- Surplus images for a project that still has a VM: digests other than the VM's current image and `:latest`
- Stale clone volumes

Home volumes, msb images, and Docker images are age-gated like VMs: they are only removed once they are older than
the pruning threshold. Recently-created home volumes and recently-loaded runner images are preserved even if their
project currently has no running VM, so the cached runner image and home state survive startup and the image does not
need to be loaded into the sandbox again.

Auto-pruning runs before every command via `sync.Once`. It uses a fixed default threshold of 30 days unless
`auto-prune-age` is set in config.
