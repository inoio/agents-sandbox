# Commands Reference

This document lists all opencode-msb subcommands, aliases, and flags.

## Global Flags

These flags are available on every command.

| Flag         | Short | Default | Purpose                        |
|--------------|-------|---------|--------------------------------|
| `--yes`      | `-y`  | `false` | Assume yes to all prompts      |
| `--verbose`  | `-v`  | `false` | Show debug-level output        |
| `--quiet`    | `-q`  | `false` | Suppress non-error output      |
| `--tree`     | —     | `false` | Print the full command tree    |

## Commands

### run

Run opencode in a microsandbox VM. This is the default command — `opencode-msb` with no subcommand is equivalent to `opencode-msb run`.

```console
opencode-msb [ARGS...]                          # default: run opencode
opencode-msb -b my-feature [ARGS...]            # branch session
opencode-msb --dry-run                          # validate only
opencode-msb -m 8G -c 4 -- -c "fix bug"         # CPU/memory + ops
opencode-msb -- -c "fix bug"                    # arguments to opencode
```

Arguments after `--` are forwarded to opencode. Arguments before `--` that don't match flags are also forwarded.

**Flags:**

| Flag            | Short | Default  | Purpose                                    |
|-----------------|-------|----------|--------------------------------------------|
| `--branch`      | `-b`  | `""`     | Isolated git worktree for the given branch |
| `--rebuild`     | `-r`  | `false`  | Rebuild runner image before starting       |
| `--dry-run`     | `-n`  | `false`  | Validate setup without running opencode    |
| `--cpus`        | `-c`  | `0`      | vCPUs for the sandbox (0 = all)            |
| `--memory`      | `-m`  | `4G`     | Memory limit, e.g. `4G`, `512M`            |
| `--tmp-size`    | —     | `2G`     | Size of `/tmp` tmpfs in the sandbox        |
| `--user`        | `-u`  | `dev`*   | Username or UID inside the sandbox         |
| `--no-auto`     | —     | `false`  | Do not pass `--auto` to opencode           |
| `--dry-run-vm`  | —     | `false`  | Skip VM lifecycle but prepare everything else |

<!-- markdownlint-disable-next-line no-trailing-punctuation -->
* Effective default: `dev`. The CLI flag defaults to empty string, but the launcher applies `dev` when blank.

**Aliases:** `sandbox run`

---

### shell

Start a sandbox VM and open an interactive shell. Useful for debugging the sandbox environment. Same flags as `run` but without `--dry-run` and `--no-auto`.

```console
opencode-msb shell
opencode-msb shell -b my-feature
```

**Aliases:** `sandbox shell`

---

### build

Build or rebuild the runner Docker image. If `.opencode-msb/Dockerfile` exists in the project directory, it's layered on top of the base image.

```console
opencode-msb build        # build or update if needed
opencode-msb build -r     # force clean rebuild
```

**Flags:**

| Flag            | Short | Default | Purpose                      |
|-----------------|-------|---------|------------------------------|
| `--rebuild`     | `-r`  | `false`  | Force a clean rebuild        |
| `--dry-run`     | `-n`  | `false`  | Dry run without building     |

**Aliases:** `image build`

---

### stop

Gracefully stop the project VM. State remains for future reuse.

```console
opencode-msb stop
opencode-msb stop -f     # stop and remove VM state
```

**Aliases:** `sandbox stop`

**Flags:**

| Flag        | Short | Default | Purpose                        |
|-------------|-------|---------|--------------------------------|
| `--force`   | `-f`  | `false` | Remove VM's persisted state    |
| `--dry-run` | `-n`  | `false` | Show what would be stopped without stopping |

---

### kill

Force-kill the project VM. Equivalent to powering off. State may be corrupted.

```console
opencode-msb kill
opencode-msb kill -f     # kill and remove VM state
```

**Aliases:** `sandbox kill`

**Flags:**

| Flag        | Short | Default | Purpose                        |
|-------------|-------|---------|--------------------------------|
| `--force`   | `-f`  | `false` | Remove VM's persisted state    |
| `--dry-run` | `-n`  | `false` | Show what would be killed without killing |

---

### prune

Remove stale VMs, volumes, and images. Staleness is determined by age — resources older than the threshold are pruned.

```console
opencode-msb prune                       # default 7-day threshold
opencode-msb prune -a 24h                # 24-hour threshold
opencode-msb prune --dry-run             # preview only
opencode-msb prune --force               # skip confirmation
```

**Flags:**

| Flag          | Short | Default | Purpose                               |
|---------------|-------|---------|---------------------------------------|
| `--age`       | `-a`  | `7d`    | Prune threshold (e.g. `24h`, `7d`)    |
| `--dry-run`   | `-n`  | `false` | Preview what would be pruned          |
| `--force`     | `-f`  | `false` | Skip confirmation prompt              |
| `--dry-run-vm`| —     | `false` | Suppress VM deletion during prune     |

---

### list

List all sandboxes for the current project.

```console
opencode-msb list
opencode-msb ls
opencode-msb sandbox list
```

**Aliases:** `ls`, `sandbox list`

---

### doctor

Check that all host prerequisites are met (Docker, KVM, Git, msb CLI).

```console
opencode-msb doctor
```

---

### config show

Print the merged opencode configuration with source file paths.

```console
opencode-msb config show
```

---

### image list

List cached runner Docker images with references and digests.

```console
opencode-msb image list
opencode-msb image ls
```

**Aliases:** `image ls`

---

### volume list

List managed home volumes with names and paths.

```console
opencode-msb volume list
opencode-msb volume ls
```

**Aliases:** `volume ls`

---

### sandbox

Parent command that groups sandbox-related subcommands. Individual commands (`run`, `shell`, `stop`, `kill`, `list`) are also available at the top level.

```console
opencode-msb sandbox run
opencode-msb sandbox list
opencode-msb sandbox shell
opencode-msb sandbox stop
opencode-msb sandbox kill
```
