# Commands Reference

This document lists all opencode-sandbox subcommands, aliases, and flags.

## Global Flags

These flags are available on every command.

| Flag         | Short | Default | Purpose                        |
|--------------|-------|---------|--------------------------------|
| `--yes`      | `-y`  | `false` | Assume yes to all prompts      |
| `--verbose`  | `-v`  | `false` | Show debug-level output        |
| `--quiet`    | `-q`  | `false` | Suppress non-error output      |

## Commands

### run

Run opencode in a microsandbox VM. This is the default command — `opencode-sandbox` with no subcommand is equivalent to `opencode-sandbox run`.

```console
opencode-sandbox [ARGS...]                          # default: run opencode
opencode-sandbox -w bugfix-fix-thing [ARGS...]            # worktree session
opencode-sandbox --dry-run                          # validate only
opencode-sandbox -m 8G -c 4 -- -c "fix bug"         # CPU/memory + ops
opencode-sandbox -- -c "fix bug"                    # arguments to opencode
```

Arguments after `--` are forwarded to opencode. Arguments before `--` that don't match flags are also forwarded.

**Flags:**

| Flag           | Short | Default  | Purpose                                                                                                                                    |
|----------------|-------|----------|--------------------------------------------------------------------------------------------------------------------------------------------|
| `--worktree`   | `-w`  | `""`     | Isolated opencode worktree named <name>, optionally starting from base ref <name>:<base>                                  |
| `--rebuild`    | `-r`  | `false`  | Rebuild runner image before starting                                                                                                       |
| `--dry-run`    | `-n`  | `false`  | Validate setup without running opencode                                                                                                    |
| `--cpus`       | `-c`  | `0`      | vCPUs for the sandbox (0 = all)                                                                                                            |
| `--memory`     | `-m`  | `4G`     | Memory limit, e.g. `4G`, `512M`                                                                                                            |
| `--disk-size`  | —     | `""`     | Project VM root disk size (e.g. 16G). Empty = microsandbox runtime default (~4 GiB). Applied at VM creation; a change triggers recreation. An invalid value is rejected with an error. |
| `--tmp-size`   | —     | `2G`     | Size of `/tmp` tmpfs in the sandbox. An invalid value is rejected with an error. |
| `--dry-run-vm` | —     | `false`  | Skip VM lifecycle but prepare everything else                                                                                              |
| `--serve-only` | `-s`  | `false`  | Start opencode server published on host loopback (no in-VM TUI); press `Ctrl-D` to exit. Set `OPENCODE_SERVER_PASSWORD` for basic auth. |

**Aliases:** `sandbox run`

---

### shell

Start a sandbox VM and open an interactive shell. Useful for debugging the sandbox environment. Shares the common run/shell flags with `run`.

```console
opencode-sandbox shell
opencode-sandbox shell -w bugfix-fix-thing
```

**Flags:**

| Flag       | Short | Default | Purpose                                                          |
|------------|-------|---------|------------------------------------------------------------------|
| `--root`   | —     | `false` | Attach the shell as root (debug/maintenance). Only available on shell. |

**Aliases:** `sh`, `sandbox shell`

---

### build

Build or rebuild the runner Docker image. If `.opencode-sandbox/Dockerfile` exists in the project directory, it's layered on top of the base image.

```console
opencode-sandbox build        # build or update if needed
opencode-sandbox build -r     # force clean rebuild
```

**Flags:**

| Flag            | Short | Default  | Purpose                  |
|-----------------|-------|----------|--------------------------|
| `--rebuild`     | `-r`  | `false`  | Force a clean rebuild    |
| `--dry-run`     | `-n`  | `false`  | Dry run without building |

**Aliases:** `image build`

---

### stop

Gracefully stop the project VM. State remains for future reuse.

```console
opencode-sandbox stop
opencode-sandbox stop -f     # stop and remove VM state
```

**Aliases:** `sandbox stop`

**Flags:**

| Flag        | Short | Default | Purpose                                     |
|-------------|-------|---------|---------------------------------------------|
| `--force`   | `-f`  | `false` | Remove VM's persisted state                 |
| `--dry-run` | `-n`  | `false` | Show what would be stopped without stopping |

---

### kill

Force-kill the project VM. Equivalent to powering off. State may be corrupted.

```console
opencode-sandbox kill
opencode-sandbox kill -f     # kill and remove VM state
```

**Aliases:** `sandbox kill`

**Flags:**

| Flag        | Short | Default | Purpose                                   |
|-------------|-------|---------|-------------------------------------------|
| `--force`   | `-f`  | `false` | Remove VM's persisted state               |
| `--dry-run` | `-n`  | `false` | Show what would be killed without killing |

---

### prune

Remove stale VMs, volumes, and images. Staleness is determined by age — resources older than the threshold are pruned.

```console
opencode-sandbox prune                       # use --age, else manual-prune-age from config, else 7d
opencode-sandbox prune -a 24h                # 24-hour threshold
opencode-sandbox prune --dry-run             # preview only
opencode-sandbox prune --force               # skip confirmation
```

**Flags:**

| Flag           | Short | Default | Purpose                            |
|----------------|-------|---------|------------------------------------|
| `--age`        | `-a`  | config  | Prune threshold. Falls back to `manual-prune-age` from config, then to `7d` (e.g. `24h`, `7d`). |
| `--dry-run`    | `-n`  | `false` | Preview what would be pruned       |
| `--force`      | `-f`  | `false` | Skip confirmation prompt           |
| `--dry-run-vm` | —     | `false` | Suppress VM deletion during prune  |

---

### list

List all sandboxes on this host (across all projects).

```console
opencode-sandbox list
opencode-sandbox ls
opencode-sandbox sandbox list
```

**Aliases:** `ls`, `sandbox list`

---

### config

Inspect opencode and home configuration.

```console
opencode-sandbox config
opencode-sandbox cfg
```

**Aliases:** `cfg`

#### config show

Print the snippet files that were merged and the resulting opencode configuration (provisioned to `.config/opencode/opencode.json`).

```console
opencode-sandbox config show
```

#### config home

List the resolved home-file mappings from the `home.yaml` manifest (VM target path ← host source path).

```console
opencode-sandbox config home
```

---

### completion

Generate the autocompletion script for the specified shell.

```console
opencode-sandbox completion bash         # bash completions (fish, powershell, zsh work the same)
opencode-sandbox completion fish
opencode-sandbox completion powershell
opencode-sandbox completion zsh
```

---

### image

Manage runner images.

```console
opencode-sandbox image
opencode-sandbox img
```

**Aliases:** `img`

#### image list

List cached runner Docker images with references and digests.

```console
opencode-sandbox image list
opencode-sandbox image ls
```

**Aliases:** `image ls`

#### image build

Build or rebuild the runner image. Equivalent to the top-level `build` command.

```console
opencode-sandbox image build
```

---

### sandbox

Parent command that groups sandbox-related subcommands. Individual commands (`run`, `shell`, `stop`, `kill`, `list`) are also available at the top level.

```console
opencode-sandbox sandbox run
opencode-sandbox sandbox list
opencode-sandbox sandbox shell
opencode-sandbox sandbox stop
opencode-sandbox sandbox kill
```

**Aliases:** `sb`

---

### tree

Print the full command tree, showing every subcommand, alias, and flag.

```console
opencode-sandbox tree
```

---

### doctor

Check prerequisites (Docker, KVM, Git, msb) and exit.

```console
opencode-sandbox doctor
```

---

### version

Print version.

```console
opencode-sandbox version
```

---

### `opencode-sandbox volume <subcommand>`

The volume group provides manual home volume management.

**Aliases:** `vol`

#### `opencode-sandbox volume list`

List all managed home volumes.

```console
opencode-sandbox volume list
```

**Aliases:** `volume ls`

#### `opencode-sandbox volume migrate [volume-name]`

Create a new home volume and copy files from the old volume on top of it.

- **Args:**
  - `[volume-name]` — optional; defaults to volume in state file
- **Flags:**
  - `--rm` — remove old volume after successful migration
  - `--dry-run` — show what would be done
  - `--rebuild` — rebuild runner image before migrating

#### `opencode-sandbox volume reset [volume-name]`

Create a new home volume from the image contents only (fresh, no copy).

- **Args:**
  - `[volume-name]` — optional; defaults to volume in state file
- **Flags:**
  - `--rm` — remove old volume after reset
  - `--dry-run` — show what would be done
  - `--rebuild` — rebuild runner image before resetting

#### `opencode-sandbox volume edit [volume-name]`

Create a new volume alongside the old one, for manual data transfer.

- **Args:**
  - `[volume-name]` — optional; defaults to volume in state file
- **Flags:**
  - `--rm` — remove old volume after you exit (you are responsible for confirming)
  - `--dry-run` — show what would be done
