---
title: Commands
layout: default
nav_order: 60
---
# Commands Reference

This document lists all agents-sandbox subcommands, aliases, and flags.

## Global Flags

These flags are available on every command.

| Flag          | Short | Default | Purpose                                                         |
|---------------|-------|---------|-----------------------------------------------------------------|
| `--yes`       | `-y`  | `false` | Assume yes to all prompts                                       |
| `--quiet`     | `-q`  | `false` | Suppress stdout output                                          |
| `--log-level` | `-l`  | `info`  | Minimum log level to show (`error`, `warning`, `info`, `verbose`) |

## Commands

### run

Run a coding agent in a microsandbox VM. This is the default command — `agents-sandbox` with no subcommand is equivalent to `agents-sandbox run`.

```console
agents-sandbox [ARGS...]                          # default: run the configured agent
agents-sandbox -w bugfix-fix-thing [ARGS...]            # worktree session
agents-sandbox --dry-run                          # validate only
agents-sandbox -m 8G -c 4 -- -c "fix bug"         # CPU/memory + ops
agents-sandbox -- -c "fix bug"                    # arguments to the agent
```

Arguments after `--` are forwarded to the agent. Arguments before `--` that don't match flags are also forwarded.

**Flags:**

| Flag           | Short | Default  | Purpose                                                                                                                                    |
|----------------|-------|----------|--------------------------------------------------------------------------------------------------------------------------------------------|
| `--worktree`   | `-w`  | `""`     | Isolated agent worktree named <name>, optionally starting from base ref <name>:<base>                                  |
| `--rebuild`    | `-r`  | `false`  | Rebuild runner image before starting                                                                                                       |
| `--dry-run`    | `-n`  | `false`  | Validate setup without running the agent                                                                                                    |
| `--cpus`       | `-c`  | `0`      | vCPUs for the sandbox (0 = all)                                                                                                            |
| `--memory`     | `-m`  | `4G`     | Memory limit, e.g. `4G`, `512M`                                                                                                            |
| `--disk-size`  | —     | `""`     | Project VM root disk size (e.g. 16G). Empty = microsandbox runtime default (~4 GiB). Applied at VM creation; a change triggers recreation. An invalid value is rejected with an error. |
| `--tmp-size`   | —     | `2G`     | Size of `/tmp` tmpfs in the sandbox. An invalid value is rejected with an error. |
| `--workspace-quota` | — | `16G` | Guest-write quota for the `/workspace` bind mount (e.g. `32G`), bounding writes on top of the host repo. Applied at VM creation; a change triggers recreation. An invalid value is rejected with an error. |
| `--dry-run-vm` | —     | `false`  | Skip VM lifecycle but prepare everything else                                                                                              |
| `--serve-only` | `-s`  | `false`  | Start the agent server published on host loopback at a dynamically allocated host port (no in-VM TUI); press `Ctrl-D` to exit. The printed `http://127.0.0.1:<port>` URL is authoritative — use it (rather than assuming a fixed port) to connect from clients like Opencode Desktop. Set `OPENCODE_SERVER_PASSWORD` for basic auth. |
| `--agent`      | —     | `opencode` | Coding-agent profile to run: `opencode` (default), `opencode2`, `pi`, or `claude-code`.                                                                |
| `--notify`     | —     | `off`     | Notify on session status: `on`, `off`, `desktop`, or `audio` (bare `--notify` = `on`). Overridable via `OPENCODE_SANDBOX_NOTIFY`. Only applies to the opencode agent (the only agent with a session event stream). |
| `--dind`       | —     | `false`  | Enable Docker-in-Docker in the runner image                                                                                                |

**Aliases:** `sandbox run`

---

### shell

Start a sandbox VM and open an interactive shell. Useful for debugging the sandbox environment. Shares the common run/shell flags with `run`.

```console
agents-sandbox shell
agents-sandbox shell -w bugfix-fix-thing
```

**Flags:**

| Flag       | Short | Default | Purpose                                                          |
|------------|-------|---------|------------------------------------------------------------------|
| `--root`   | —     | `false` | Attach the shell as root (debug/maintenance). Only available on shell. |
| `--dind`   | —     | `false` | Enable Docker-in-Docker in the runner image                      |

**Aliases:** `sh`, `sandbox shell`

---

### build

Build or rebuild the runner Docker image. If `.agents-sandbox/Dockerfile` exists in the project directory, it's layered on top of the base image.

```console
agents-sandbox build        # build or update if needed
agents-sandbox build -r     # force clean rebuild
agents-sandbox build --agent-version 0.5.0  # pin a specific agent version
```

**Flags:**

| Flag                 | Short | Default  | Purpose                                                         |
|----------------------|-------|----------|-----------------------------------------------------------------|
| `--rebuild`          | `-r`  | `false`  | Force a clean rebuild                                           |
| `--dry-run`          | `-n`  | `false`  | Dry run without building                                        |
| `--agent`            | —     | `opencode` | Coding-agent profile to build: `opencode` (default), `opencode2`, `pi`, or `claude-code`. |
| `--agent-version`    | —     | `""`     | Pin the agent version baked into the image (default: latest)    |
| `--dind`             | —     | `false`  | Enable Docker-in-Docker in the runner image                     |

> The deprecated `--opencode-version` flag remains as an alias for `--agent-version`.

**Aliases:** `image build`

#### build dockerfile

Print the runner Dockerfile exactly as it would be built for the current project, without invoking docker. The output reflects the selected agent profile and the dind switch, and layers the project's `.agents-sandbox/Dockerfile` (if any) on top of the base image.

```console
agents-sandbox build dockerfile            # default agent, no dind
agents-sandbox build dockerfile --dind     # with Docker-in-Docker block
agents-sandbox build dockerfile --agent claude-code
```

**Flags:**

| Flag      | Short | Default    | Purpose                                                     |
|-----------|-------|------------|-------------------------------------------------------------|
| `--agent` | —     | `opencode` | Coding-agent profile to build: `opencode`, `pi`, or `claude-code`. |
| `--dind`  | —     | `false`    | Enable Docker-in-Docker in the runner image                 |

**Aliases:** `image build dockerfile`

---

### stop

Gracefully stop the project VM. State remains for future reuse.

```console
agents-sandbox stop
agents-sandbox stop -f     # stop and remove VM state
```

**Aliases:** `sandbox stop`

**Flags:**

| Flag        | Short | Default     | Purpose                                     |
|-------------|-------|-------------|---------------------------------------------|
| `--force`   | `-f`  | `false`     | Remove VM's persisted state                 |
| `--dry-run` | `-n`  | `false`     | Show what would be stopped without stopping |
| `--agent`   | —     | `opencode`  | Coding-agent profile to stop                |

---

### kill

Force-kill the project VM. Equivalent to powering off. State may be corrupted.

```console
agents-sandbox kill
agents-sandbox kill -f     # kill and remove VM state
```

**Aliases:** `sandbox kill`

**Flags:**

| Flag        | Short | Default     | Purpose                                   |
|-------------|-------|-------------|-------------------------------------------|
| `--force`   | `-f`  | `false`     | Remove VM's persisted state               |
| `--dry-run` | `-n`  | `false`     | Show what would be killed without killing |
| `--agent`   | —     | `opencode`  | Coding-agent profile to kill              |

---

### prune

Remove stale VMs, volumes, and images in one pass. Staleness is determined by age — resources older than the threshold
are pruned. The summary line reads `Pruned N VMs, N home volumes, N docker images, N msb images`. To prune a single
artifact type, use the `image prune`, `volume prune`, or `sandbox prune` subcommands below.

```console
agents-sandbox prune                       # use --age, else manual-prune-age from config, else 7d
agents-sandbox prune -a 24h                # 24-hour threshold
agents-sandbox prune --dry-run             # preview only
```

**Flags:**

| Flag           | Short | Default | Purpose                            |
|----------------|-------|---------|------------------------------------|
| `--age`        | `-a`  | config  | Prune threshold. Falls back to `manual-prune-age` from config, then to `7d` (e.g. `24h`, `7d`). |
| `--dry-run`    | `-n`  | `false` | Preview what would be pruned       |

---

### image prune

Prune cached runner images. Images of stale projects (older than the threshold) are removed entirely; for projects with a
surviving VM, the per-agent `-latest` tags and any image a kept VM currently references are retained, while surplus
digest refs are removed.

```console
agents-sandbox image prune                      # use manual-prune-age from config (default: 7d)
agents-sandbox image prune -a 24h               # 24-hour threshold
agents-sandbox image prune --dry-run            # preview only
```

**Flags:**

| Flag        | Short | Default | Purpose                                                                             |
|-------------|-------|---------|-------------------------------------------------------------------------------------|
| `--age`     | `-a`  | config  | Prune threshold. Falls back to `manual-prune-age` from config, then to `7d` (e.g. `24h`, `7d`). |
| `--dry-run` | `-n`  | `false` | Show what would be pruned without deleting                                          |

---

### volume prune

Prune home volumes of stale projects (older than the threshold). When a project's last home volume is removed, its
state file is removed too.

```console
agents-sandbox volume prune                     # use manual-prune-age from config (default: 7d)
agents-sandbox volume prune -a 24h              # 24-hour threshold
agents-sandbox volume prune --dry-run           # preview only
```

**Flags:**

| Flag        | Short | Default | Purpose                                                                             |
|-------------|-------|---------|-------------------------------------------------------------------------------------|
| `--age`     | `-a`  | config  | Prune threshold. Falls back to `manual-prune-age` from config, then to `7d` (e.g. `24h`, `7d`). |
| `--dry-run` | `-n`  | `false` | Show what would be pruned without deleting                                          |

---

### sandbox prune

Prune stale sandboxes and leftover task workers. Task sandboxes fold into the VM count.

```console
agents-sandbox sandbox prune                    # use manual-prune-age from config (default: 7d)
agents-sandbox sandbox prune -a 24h             # 24-hour threshold
agents-sandbox sandbox prune --dry-run          # preview only
```

**Flags:**

| Flag        | Short | Default | Purpose                                                                             |
|-------------|-------|---------|-------------------------------------------------------------------------------------|
| `--age`     | `-a`  | config  | Prune threshold. Falls back to `manual-prune-age` from config, then to `7d` (e.g. `24h`, `7d`). |
| `--dry-run` | `-n`  | `false` | Show what would be pruned without deleting                                          |

---

### list

List all sandboxes on this host (across all projects).

```console
agents-sandbox list
agents-sandbox ls
agents-sandbox sandbox list
```

**Aliases:** `ls`, `sandbox list`

Prints a header row followed by one line per agents-sandbox VM with columns
`NAME`, `IMAGE`, `STATUS`, and `CREATED`. `CREATED` uses `YYYY-MM-DD HH:MM:SS`,
matching microsandbox's `msb list` output. The `STATUS` cell is colored like
microsandbox when color is enabled (`running` green, `stopped`/`created` dim,
transitional states yellow, `crashed` red); with color disabled it renders as
plain text.

**Flags:**

| Flag         | Short | Default | Purpose                                                                                                  |
|--------------|-------|---------|----------------------------------------------------------------------------------------------------------|
| `--label`    | —     | —       | Filter to sandboxes carrying the given `KEY=VALUE` label. Repeatable; labels are AND-matched.            |
| `--limit`    | —     | `0`     | Limit the number of sandboxes listed (`0` = no limit).                                                   |
| `--running`  | —     | `false` | Only list running sandboxes.                                                                             |
| `--stopped`  | —     | `false` | Only list stopped sandboxes.                                                                            |
| `--names`    | —     | `false` | Print names only (no header, no status, image, or created columns).                                     |
| `--format`   | —     | `""`    | Output format. `json` prints a top-level array of `{name,status,image,created,updated,labels}` objects. |

`--running` wins over `--stopped` when both are set.

---

### config

Inspect agent and home configuration.

```console
agents-sandbox config
agents-sandbox cfg
```

**Aliases:** `cfg`

#### config agent [name]

Show the merged snippet config, the verbatim config-dir mirror files, and the host files drop-in-provisioned into the VM for an agent (default: the configured agent, `opencode`).

```console
agents-sandbox config agent opencode
agents-sandbox config agent --agent pi
```

**Flags:**

| Flag       | Short | Default    | Purpose                                                                 |
|------------|-------|------------|-------------------------------------------------------------------------|
| `--agent`  | —     | `opencode` | Coding-agent profile to inspect: `opencode` (default), `pi`, or `claude-code`. |

The agent is resolved from the `--agent` flag, then the positional `[name]`, then the configured agent, then `opencode`. Passing both `--agent` and a positional `[name]` (conflicting values) returns an "ambiguous" error.

Each host file is listed as `merged` (its VM path is the merged config path or part of the removed config-file family) or `not merged` (copied verbatim). Verbatim mirror files are listed as their host source path → VM path.

#### config home

List the resolved home-file mappings from the config `home:` key (VM target path ← host source path).

```console
agents-sandbox config home
```

---

### completion

Generate the autocompletion script for the specified shell.

```console
agents-sandbox completion bash         # bash completions (fish, powershell, zsh work the same)
agents-sandbox completion fish
agents-sandbox completion powershell
agents-sandbox completion zsh
```

---

### image

Manage runner images.

```console
agents-sandbox image
agents-sandbox img
```

**Aliases:** `img`

#### image list

List cached runner Docker images with reference, digest, size, and creation time. The
reference ends in the short content hash the image is keyed under in microsandbox; the
digest column shows the short form (`sha256:` followed by 12 hex chars) as microsandbox
reports it.

```console
agents-sandbox image list
agents-sandbox image ls
```

**Aliases:** `image ls`

#### image build

Build or rebuild the runner image. Equivalent to the top-level `build` command.

```console
agents-sandbox image build
```

---

### sandbox

Parent command that groups sandbox-related subcommands. Individual commands (`run`, `shell`, `stop`, `kill`, `list`) are also available at the top level.

```console
agents-sandbox sandbox run
agents-sandbox sandbox list
agents-sandbox sandbox shell
agents-sandbox sandbox stop
agents-sandbox sandbox kill
```

**Aliases:** `sb`

---

### tree

Print the full command tree, showing every subcommand, alias, and flag.

```console
agents-sandbox tree
```

---

### doctor

Check prerequisites (Docker, KVM, Git, msb) and exit.

```console
agents-sandbox doctor
```

---

### version

Print version.

```console
agents-sandbox version
```

---

### upgrade

Check for and install the latest release, independent of the `upgrade.mode`/`upgrade.interval` settings that govern the
automatic check on `run`/`shell`. Replaces the running executable with the release binary for your platform; the new
version takes effect on the next invocation.

```console
agents-sandbox upgrade
```

---

### `agents-sandbox volume <subcommand>`

The volume group provides manual home volume management.

**Aliases:** `vol`

#### `agents-sandbox volume list`

List all managed home volumes.

```console
agents-sandbox volume list
```

Columns: `NAME`, `KIND`, `SIZE`, `CREATED` (`YYYY-MM-DD HH:MM:SS`). `SIZE` shows capacity for disk volumes and quota for directory volumes, or `-` when unavailable.

**Aliases:** `volume ls`

#### `agents-sandbox volume migrate [volume-name]`

Create a new home volume and copy files from the old volume on top of it.

- **Args:**
  - `[volume-name]` — optional; defaults to volume in state file
- **Flags:**
  - `--rm` — remove old volume after successful migration
  - `--dry-run` — show what would be done
  - `--rebuild` — rebuild runner image before migrating
  - `--agent` — coding-agent profile to provision (`opencode` default)

#### `agents-sandbox volume reset [volume-name]`

Create a new home volume from the image contents only (fresh, no copy).

- **Args:**
  - `[volume-name]` — optional; defaults to volume in state file
- **Flags:**
  - `--rm` — remove old volume after reset
  - `--dry-run` — show what would be done
  - `--rebuild` — rebuild runner image before resetting
  - `--agent` — coding-agent profile to provision (`opencode` default)

#### `agents-sandbox volume edit [volume-name]`

Create a new volume alongside the old one, for manual data transfer.

- **Args:**
  - `[volume-name]` — optional; defaults to volume in state file
- **Flags:**
  - `--rm` — remove old volume after you exit (you are responsible for confirming)
  - `--dry-run` — show what would be done
