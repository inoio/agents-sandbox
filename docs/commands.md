---
title: Commands
description: "Reference for every agents-sandbox subcommand, its aliases, its flags, and the global options shared by all commands."
layout: default
parent: Reference
nav_order: 10
---
# Commands

This page lists every agents-sandbox subcommand, alias, and flag. Deprecated flags, config keys, and environment
variables are listed in [Deprecations]({% link deprecations.md %}).

The commands are grouped by area; jump to a group:

- **Runtime:** [`run`](#run), [`shell`](#shell), [`stop`](#stop), [`kill`](#kill), [`list`](#list), [`tree`](#tree), [`doctor`](#doctor), [`version`](#version), [`upgrade`](#upgrade), [`completion`](#completion)
- **Build and images:** [`build`](#build), [`image`](#image)
- **Pruning:** [`prune`](#prune), [`image prune`](#image-prune), [`volume prune`](#volume-prune), [`sandbox prune`](#sandbox-prune)
- **Storage:** [`df`](#df), [`storage`](#storage)
- **Configuration:** [`config`](#config), [`config paths`](#config-paths)
- **Agents:** [`config agent`](#config-agent-name), [`config home`](#config-home)
- **Volumes:** [`volume`](#volume)
- **Sandboxes:** [`sandbox`](#sandbox)

## Global flags

These flags are available on every command.

| Flag          | Short | Default | Purpose                                                         |
|---------------|-------|---------|-----------------------------------------------------------------|
| `--yes`       | `-y`  | `false` | Assume yes to all prompts; authorizes destructive commands non-interactively |
| `--quiet`     | `-q`  | `false` | On list commands, print identifiers only; otherwise suppress stdout output |
| `--log-level` | `-l`  | `info`  | Minimum log level to show (`error`, `warning`, `info`, `verbose`) |

## Commands

### run

Run a coding agent in a microsandbox VM. This is the default command — `agents-sandbox` with no subcommand is equivalent to `agents-sandbox run`.

```console
agents-sandbox [ARGS...]                          # default: run the configured agent
agents-sandbox -w bugfix-fix-thing [ARGS...]            # worktree session
agents-sandbox --dry-run                          # read-only preview
agents-sandbox -m 8G -c 4 -- -c "fix bug"         # CPU/memory + ops
agents-sandbox -- -c "fix bug"                    # arguments to the agent
```

Arguments after `--` are forwarded to the agent. Arguments before `--` that don't match flags are also forwarded.

**Flags:**

| Flag           | Short | Default  | Purpose                                                                                                                                    |
|----------------|-------|----------|--------------------------------------------------------------------------------------------------------------------------------------------|
| `--worktree`   | `-w`  | `""`     | Isolated agent worktree named <name>, optionally starting from base ref <name>:<base>                                  |
| `--rebuild`    | `-r`  | `false`  | Rebuild runner image before starting (clean rebuild; still offers an agent upgrade if one is available)                                                                                                       |
| `--agent-version` | —  | `""`     | Pin the version agents-sandbox installs into the runner image (default: latest). Ignored for a user-provided agent. |
| `--dry-run`    | `-n`  | `false`  | Read-only preview of a run: report the agent version (or that it resolves at build time), whether the runner image would build (with the reasons a real build logs) or be reused, and whether the home volume would be created or reused — without building, creating a volume, writing state, starting a VM, attaching, or making any network request. When the reported version is a known, non-pinned one, its line notes `(upgrade check not run)` |
| `--cpus`       | `-c`  | `0`      | vCPUs for the sandbox (0 = all)                                                                                                            |
| `--memory`     | `-m`  | `4G`     | Memory limit, e.g. `4G`, `512M`                                                                                                            |
| `--disk-size`  | —     | `""`     | Project VM root disk size (e.g. 16G). Empty = microsandbox runtime default (~4 GiB). Applied at VM creation; a change triggers recreation. An invalid value is rejected with an error. |
| `--tmp-size`   | —     | `2G`     | Size of `/tmp` tmpfs in the sandbox. An invalid value is rejected with an error. |
| `--workspace-quota` | — | `16G` | Guest-write quota for the `/workspace` bind mount (e.g. `32G`), bounding writes on top of the host repo. Applied at VM creation; a change triggers recreation. An invalid value is rejected with an error. |
| `--dry-run-vm` | —     | `false`  | Skip VM lifecycle but prepare everything else                                                                                              |
| `--serve-only` | `-s`  | `false`  | Start the agent server published on host loopback at a dynamically allocated host port (no in-VM TUI); press `Ctrl-D` to exit. The printed `http://127.0.0.1:<port>` URL is authoritative — use it (rather than assuming a fixed port) to connect from clients like Opencode Desktop. Set `OPENCODE_SERVER_PASSWORD` for basic auth (opencode); opencode2's server requires the baked credential `opencode:opencode` (username:password). |
| `--agent`      | —     | `opencode2` | Coding-agent profile to run: `opencode`, `opencode2` (default), `pi`, or `claude-code`.                                                                |
| `--notify`     | —     | `off`     | Notify on session status: `on`, `off`, `desktop`, or `audio` (bare `--notify` = `on`). Overridable via `AGENTS_SANDBOX_NOTIFY`. Only applies to the `opencode` and `opencode2` agents (the only agents with a session event stream). |
| `--docker`     | —     | `false`  | Bake the Docker engine into the runner image (`--dind` is a deprecated alias)                                                               |

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
| `--docker` | —     | `false` | Bake the Docker engine into the runner image (`--dind` is a deprecated alias) |

**Aliases:** `sh`, `sandbox shell`

---

### build

Build or rebuild the runner Docker image. If `.agents-sandbox/Dockerfile` exists in the project directory, it's layered on top of the base image. In a bare checkout the project directory is the bare root next to `.git`, so the Dockerfile is read from there; a worktree-level `.agents-sandbox/` is ignored, with a warning.

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
| `--agent`            | —     | `opencode2` | Coding-agent profile to build: `opencode`, `opencode2` (default), `pi`, or `claude-code`. |
| `--agent-version`    | —     | `""`     | Pin the version agents-sandbox installs into the image (default: latest) |
| `--docker`           | —     | `false`  | Bake the Docker engine into the runner image (`--dind` is a deprecated alias) |

> The deprecated `--opencode-version` flag remains as an alias for `--agent-version`.
> The version flag does not replace an agent already supplied by a custom base or project Dockerfile.

**Aliases:** `image build`

#### build dockerfile

Print the runner Dockerfile exactly as it would be built for the current project, without invoking docker. The output reflects the selected agent profile and the docker switch, and layers the project's `.agents-sandbox/Dockerfile` (if any) on top of the base image. In a bare checkout the project Dockerfile is read from the bare root next to `.git`, not the launched worktree.

```console
agents-sandbox build dockerfile            # default agent, no docker
agents-sandbox build dockerfile --docker   # with the Docker engine block
agents-sandbox build dockerfile --agent claude-code
```

**Flags:**

| Flag              | Short | Default    | Purpose                                                     |
|-------------------|-------|------------|-------------------------------------------------------------|
| `--agent`         | —     | `opencode2` | Coding-agent profile to build: `opencode`, `opencode2`, `pi`, or `claude-code`. |
| `--agent-version` | —     | `""`       | Pin the agent version rendered into the preview (default: no pinned default) |
| `--docker`        | —     | `false`    | Bake the Docker engine into the runner image (`--dind` is a deprecated alias) |

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
| `--agent`   | —     | `opencode2`  | Coding-agent profile to stop                |

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
| `--agent`   | —     | `opencode2`  | Coding-agent profile to kill              |

---

### prune

Remove stale VMs, volumes, and images in one pass. Staleness is determined by age — resources older than the threshold
are pruned. The summary line reads `Pruned N VMs, N home volumes, N docker images, N msb images`. To prune a single
artifact type, use the `image prune`, `volume prune`, or `sandbox prune` subcommands.

Unless `--dry-run` is set, prune asks for confirmation before removing anything (defaulting to no). Pass the global
`--yes` to skip the prompt; non-interactively, `--yes` is required or the command fails fast instead of blocking.
`--dry-run` only previews and never prompts.

```console
agents-sandbox prune                       # use --older-than, else manual-prune-age from config, else 7d
agents-sandbox prune --older-than 24h      # 24-hour threshold
agents-sandbox prune --yes                 # skip the confirmation prompt
agents-sandbox prune --dry-run             # preview only (no prompt)
agents-sandbox prune --format json         # machine-readable report on stdout
```

**Flags:**

| Flag           | Short | Default | Purpose                            |
|----------------|-------|---------|------------------------------------|
| `--older-than` | —     | `config` | Prune threshold. Falls back to `manual-prune-age` from config, then to `7d` (e.g. `24h`, `7d`). |
| `--age`        | `-a`  | —       | Deprecated alias for `--older-than`. Hidden; still honored but warns. Setting both is an error. |
| `--dry-run`    | `-n`  | `false` | Preview what would be pruned (no confirmation) |
| `--format`     | —     | `""`    | Output format. `json` prints a structured report on stdout. |

---

### image prune

Prune cached runner images. Images of stale projects (older than the threshold) are removed entirely; for projects with a
surviving VM, the per-agent `-latest` tags and any image a kept VM currently references are retained, while surplus
digest refs are removed. Native microsandbox image pruning also reclaims unreferenced manifests and layers left behind by
refreshing an image tag while an older VM still points at the previous manifest.

```console
agents-sandbox image prune                      # use manual-prune-age from config (default: 7d)
agents-sandbox image prune --older-than 24h     # 24-hour threshold
agents-sandbox image prune --yes                # skip the confirmation prompt
agents-sandbox image prune --dry-run            # preview only
agents-sandbox image prune --format json        # machine-readable report on stdout
```

**Flags:**

| Flag           | Short | Default | Purpose                                                                             |
|----------------|-------|---------|-------------------------------------------------------------------------------------|
| `--older-than` | —     | `config` | Prune threshold. Falls back to `manual-prune-age` from config, then to `7d` (e.g. `24h`, `7d`). |
| `--age`        | `-a`  | —       | Deprecated alias for `--older-than`. Hidden; still honored but warns. Setting both is an error. |
| `--dry-run`    | `-n`  | `false` | Show what would be pruned without deleting (no confirmation)                        |
| `--format`     | —     | `""`    | Output format. `json` prints a structured report on stdout.                         |

---

### volume prune

Prune home volumes of stale projects (older than the threshold). When a project's last home volume is removed, its
state file is removed too.

```console
agents-sandbox volume prune                     # use manual-prune-age from config (default: 7d)
agents-sandbox volume prune --older-than 24h    # 24-hour threshold
agents-sandbox volume prune --yes               # skip the confirmation prompt
agents-sandbox volume prune --dry-run           # preview only
agents-sandbox volume prune --format json       # machine-readable report on stdout
```

**Flags:**

| Flag           | Short | Default | Purpose                                                                             |
|----------------|-------|---------|-------------------------------------------------------------------------------------|
| `--older-than` | —     | `config` | Prune threshold. Falls back to `manual-prune-age` from config, then to `7d` (e.g. `24h`, `7d`). |
| `--age`        | `-a`  | —       | Deprecated alias for `--older-than`. Hidden; still honored but warns. Setting both is an error. |
| `--dry-run`    | `-n`  | `false` | Show what would be pruned without deleting (no confirmation)                        |
| `--format`     | —     | `""`    | Output format. `json` prints a structured report on stdout.                         |

---

### sandbox prune

Prune stale sandboxes and leftover task workers. Task sandboxes fold into the VM count.

```console
agents-sandbox sandbox prune                    # use manual-prune-age from config (default: 7d)
agents-sandbox sandbox prune --older-than 24h   # 24-hour threshold
agents-sandbox sandbox prune --yes              # skip the confirmation prompt
agents-sandbox sandbox prune --dry-run          # preview only
agents-sandbox sandbox prune --format json      # machine-readable report on stdout
```

**Flags:**

| Flag           | Short | Default | Purpose                                                                             |
|----------------|-------|---------|-------------------------------------------------------------------------------------|
| `--older-than` | —     | `config` | Prune threshold. Falls back to `manual-prune-age` from config, then to `7d` (e.g. `24h`, `7d`). |
| `--age`        | `-a`  | —       | Deprecated alias for `--older-than`. Hidden; still honored but warns. Setting both is an error. |
| `--dry-run`    | `-n`  | `false` | Show what would be pruned without deleting (no confirmation)                        |
| `--format`     | —     | `""`    | Output format. `json` prints a structured report on stdout.                         |

---

### df

Show storage usage of agents-sandbox's private runtime home, grouped into the categories microsandbox reports
(`images`, `snapshots`, `branch memory`, `snapshot memory`, `sandboxes`, `volumes`) plus the host-side `runtime`
install (`bin`/`lib`). The table mirrors `msb df`; `-` means unknown. `--verbose` adds each category's objects with
their host paths, retention reasons, and accounting details. `--format json` prints a structured report shaped like
microsandbox's storage report (unknown counters are `null`). `--format json` and `--quiet` are mutually exclusive.
Only the private runtime home is observed; a user's own `~/.microsandbox` is never touched. When the backend cannot
report storage, the categories show as unknown and a note explains why.

```console
agents-sandbox df                     # category totals
agents-sandbox df --verbose           # per-object rows and host paths
agents-sandbox df --format json       # machine-readable report on stdout
agents-sandbox df --quiet             # category names only
```

**Flags:**

| Flag        | Short | Default | Purpose                                           |
|-------------|-------|---------|---------------------------------------------------|
| `--verbose` | —     | `false` | List per-object rows and host paths               |
| `--format`  | —     | `""`    | Output format. `json` prints a structured report. |

---

### storage

Storage maintenance group. `storage df` and `storage prune` are aliases for the top-level [`df`](#df) and
[`prune`](#prune) commands.

```console
agents-sandbox storage df --verbose
agents-sandbox storage prune --older-than 24h --yes
```

---

### list

List all sandboxes on this host (across all projects).

```console
agents-sandbox list
agents-sandbox ls
agents-sandbox sandbox list
agents-sandbox list --quiet              # names only
agents-sandbox list --format json        # machine-readable output
```

**Aliases:** `ls`, `sandbox list`

Prints a header row followed by one line per agents-sandbox VM with columns
`NAME`, `IMAGE`, `STATUS`, and `CREATED`. `CREATED` uses `YYYY-MM-DD HH:MM:SS`,
matching microsandbox's own sandbox listing. The `STATUS` cell is colored like
microsandbox when color is enabled (`running` green, `stopped`/`created` dim,
transitional states yellow, `crashed` red); with color disabled it renders as
plain text.

The default output is the human table. Pass `-q`/`--quiet` to print identifiers
only (one sandbox name per line), or `--format json` for structured output (a
top-level array on stdout). The two are mutually exclusive.

**Flags:**

| Flag         | Short | Default | Purpose                                                                                                  |
|--------------|-------|---------|----------------------------------------------------------------------------------------------------------|
| `--label`    | —     | —       | Filter to sandboxes carrying the given `KEY=VALUE` label. Repeatable; labels are AND-matched.            |
| `--limit`    | —     | `0`     | Limit the number of sandboxes listed (`0` = no limit).                                                   |
| `--running`  | —     | `false` | Only list running sandboxes.                                                                             |
| `--stopped`  | —     | `false` | Only list stopped sandboxes.                                                                            |
| `--names`    | —     | `false` | Deprecated alias for `--quiet`. Hidden; still honored but warns.                                         |
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

Show the merged snippet config, the verbatim config-dir mirror files, and the host files drop-in-provisioned into the VM for an agent (default: the configured agent, `opencode2`).

```console
agents-sandbox config agent opencode
agents-sandbox config agent --agent pi
```

**Flags:**

| Flag       | Short | Default    | Purpose                                                                 |
|------------|-------|------------|-------------------------------------------------------------------------|
| `--agent`  | —     | `opencode2` | Coding-agent profile to inspect: `opencode`, `opencode2` (default), `pi`, or `claude-code`. |

The agent is resolved from the `--agent` flag, then the positional `[name]`, then the configured agent, then `opencode2`. Passing both `--agent` and a positional `[name]` (conflicting values) returns an "ambiguous" error.

The output names the launcher (host) config dirs actually read, as absolute paths:

```text
config dir: user="/home/me/.config/agents-sandbox/opencode2"  project="/work/repo/.agents-sandbox/opencode2"  (primary)
```

The trailing label is `primary` when the agent's own namespace is used, or `legacy fallback "<dir>"` when the agent is reading a shared legacy namespace (see [agent configuration]({% link configuration/agent.md %})). These are host launcher-config dirs; the guest agent config path is always `~/.config/opencode` regardless.

Each host file is listed as `merged` (its VM path is the merged config path or part of the removed config-file family) or `not merged` (copied verbatim). Verbatim mirror files are listed as their host source path → VM path.

#### config paths

Print the launcher-config layers in precedence order (lowest to highest), the resolved path of each, and the repo slug.

```console
agents-sandbox config paths
```

Each file layer is shown with its path, flagged `(missing)` when the file does not exist; the two environment layers show
their variable-name globs, and `flags` is the CLI-flag layer:

```text
config layers (lowest to highest precedence):
  generic user config  /home/me/.config/agents-sandbox
  per-project user config  /home/me/.config/agents-sandbox/myproject-<hash>  (missing)
  project config  /work/repo/.agents-sandbox  (missing)
  legacy env  OPENCODE_SANDBOX_*
  env  AGENTS_SANDBOX_*
  flags
repo slug:  myproject-<hash>
```

The layer lines are stdout; a guidance line naming the committed project config (`.agents-sandbox/config.yaml`) and the
generic user config is stderr and survives `--quiet`. The command takes no flags (the global `--quiet` suppresses the
layer block). The per-slug user config dir is keyed by the **repo** slug, so every clone of one remote shares it; that is
the slug reported here (see [Per-slug configuration]({% link configuration/launcher.md %}#per-slug-configuration)).

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
agents-sandbox image list --quiet            # references only
agents-sandbox image list --format json      # machine-readable output
```

**Aliases:** `image ls`

The default output is the human table. Pass `-q`/`--quiet` to print references
only (one per line), or `--format json` for structured output (a top-level array
on stdout). The two are mutually exclusive.

#### image inspect

Show the full detail of a cached runner image: its reference, manifest digest,
architecture, OS, size, creation time, the parsed OCI config (env, cmd,
entrypoint, working dir, user, labels, stop signal), and every layer (diff ID,
blob digest, media type, compressed and erofs sizes).

```console
agents-sandbox image inspect myproj-aBc1234D:opencode-latest
agents-sandbox image inspect <ref> --format json   # structured output
agents-sandbox image inspect <ref> --quiet         # reference only
```

- **Args:** `ref` — the image reference
- **Flags:**
  - `--format json` — structured output on stdout
  - `-q`/`--quiet` — print the reference only

#### image remove

Remove one or more cached runner images by reference. Only runner images are
accepted — the prefix-free microsandbox reference (`<slug>:<agent>-latest`) or a
reference created by an earlier version (`agents-sandbox/runner-<slug>:<agent>-latest`);
any other reference is refused, and `--force` does not override that ownership
boundary. Refuses when a runner
image is still in use — recorded by a project's state file or held by a live
project/task sandbox — naming the referencing projects, so an in-use image is
never silently deleted; pass `--force` to remove it anyway. Prompts for
confirmation unless the global `--yes` is set; non-interactively, `--yes` is
required.

```console
agents-sandbox image remove myproj-aBc1234D:opencode-latest
agents-sandbox image remove <ref>... --force   # remove even when still in use
```

- **Args:** one or more `ref`
- **Flags:**
  - `-f`/`--force` — remove even when a project or sandbox still references the image
  - `--yes` (global) — skip the confirmation prompt

**Aliases:** `image rm`

#### image build

Build or rebuild the runner image. Equivalent to the top-level `build` command.

```console
agents-sandbox image build
```

---

### sandbox

Parent command that groups sandbox-related subcommands. Most (`run`, `shell`, `stop`, `kill`, `list`) are also available at the top level; `sandbox inspect` and `sandbox remove` live only under this group.

```console
agents-sandbox sandbox run
agents-sandbox sandbox list
agents-sandbox sandbox shell
agents-sandbox sandbox stop
agents-sandbox sandbox kill
agents-sandbox sandbox inspect myproj-aBc1234D-opencode
```

**Aliases:** `sb`

#### sandbox inspect

Show the full detail of a sandbox VM: its name, stable ID, status, image,
backend kind, creation and update times, labels, mounts (guest path, kind, and
source), and resources (memory and CPUs).

```console
agents-sandbox sandbox inspect myproj-aBc1234D-opencode
agents-sandbox sandbox inspect <name> --format json   # structured output
agents-sandbox sandbox inspect <name> --quiet         # name only
```

- **Args:** `name` — the sandbox name
- **Flags:**
  - `--format json` — structured output on stdout
  - `-q`/`--quiet` — print the name only

#### sandbox remove

Remove a project sandbox VM by name. Only project VMs are accepted — the
prefix-free `<slug>-<agent>` name or a name created by an earlier version
(`agents-sandbox-vm-*`). Refuses a running VM unless `--force` is
set, which stops it first. The project's persisted state is left intact, so the
next run recreates the VM against the same recorded home volume. Prompts for
confirmation unless the global `--yes` is set; non-interactively, `--yes` is
required.

```console
agents-sandbox sandbox remove myproj-aBc1234D-opencode
agents-sandbox sandbox remove <name> --force   # stop a running VM first
```

- **Args:** `name` — the sandbox name
- **Flags:**
  - `-f`/`--force` — remove a running VM (stopping it first)
  - `--yes` (global) — skip the confirmation prompt

**Aliases:** `sandbox rm`

---

### tree

Print the full command tree, showing every subcommand, alias, and flag.

```console
agents-sandbox tree
```

---

### doctor

Check the host prerequisites — Docker and the platform's VM support (KVM on Linux, Apple Silicon on macOS) — and
ensure the private microsandbox runtime is provisioned, then exit. Run automatically before `run` and `shell`; a
failing check prints the command that fixes it and links to the matching troubleshooting section.

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
version takes effect on the next invocation. For a Homebrew-managed install the command instead prints
`brew update && brew upgrade agents-sandbox`, so Homebrew's version bookkeeping stays accurate (see
[Self-upgrade]({% link configuration/self-upgrade.md %})).

```console
agents-sandbox upgrade
agents-sandbox upgrade --channel canary     # one-shot override: upgrade from the canary line
```

`--channel` accepts `stable` or `canary` and applies to that invocation only, leaving the configured `upgrade.channel`
unchanged.

---

### volume

The volume group provides manual home volume management.

**Aliases:** `vol`

#### `agents-sandbox volume list`

List all managed home volumes.

```console
agents-sandbox volume list
agents-sandbox volume list --quiet           # names only
agents-sandbox volume list --format json     # machine-readable output
```

Columns: `NAME`, `KIND`, `SIZE`, `CREATED` (`YYYY-MM-DD HH:MM:SS`). `SIZE` shows capacity for disk volumes and quota for directory volumes, or `-` when unavailable.

The default output is the human table. Pass `-q`/`--quiet` to print names only
(one per line), or `--format json` for structured output (a top-level array on
stdout). The two are mutually exclusive.

**Aliases:** `volume ls`

#### `agents-sandbox volume inspect <name>`

Show the full detail of a home volume: its name, kind, host path, quota, used
bytes, capacity, disk format, disk fstype, labels, and creation time.

```console
agents-sandbox volume inspect myproj-aBc1234D-opencode-20260812T123456
agents-sandbox volume inspect <name> --format json   # structured output
agents-sandbox volume inspect <name> --quiet         # name only
```

- **Args:** `name` — the volume name
- **Flags:**
  - `--format json` — structured output on stdout
  - `-q`/`--quiet` — print the name only

#### `agents-sandbox volume migrate [volume-name]`

Create a new home volume and copy files from the old volume on top of it.

- **Args:**
  - `[volume-name]` — optional; defaults to volume in state file
- **Flags:**
  - `--rm` — remove old volume after successful migration
  - `--dry-run` — show what would be done
  - `--agent` — coding-agent profile to provision (`opencode2` default)

#### `agents-sandbox volume reset [volume-name]`

Create a new home volume from the image contents only (fresh, no copy).

- **Args:**
  - `[volume-name]` — optional; defaults to volume in state file
- **Flags:**
  - `--rm` — remove old volume after reset
  - `--dry-run` — show what would be done
  - `--agent` — coding-agent profile to provision (`opencode2` default)

#### `agents-sandbox volume edit [volume-name]`

Create a new volume alongside the old one, for manual data transfer.

- **Args:**
  - `[volume-name]` — optional; defaults to volume in state file
- **Flags:**
  - `--rm` — remove old volume after you exit (you are responsible for confirming)
  - `--dry-run` — show what would be done

#### `agents-sandbox volume remove <name>...`

Remove one or more home volumes by name. Refuses when a volume is still
referenced by a project's state file (naming the referencing projects), so a
project's recorded home is never silently orphaned; pass `--force` to remove it
anyway. Prompts for confirmation unless the global `--yes` is set;
non-interactively, `--yes` is required. The next run of an affected project then
starts a fresh home.

```console
agents-sandbox volume remove myproj-aBc1234D-opencode-20260812T123456
```

- **Args:** one or more `volume-name`
- **Flags:**
  - `-f`/`--force` — remove even when a project still references the volume
  - `--yes` (global) — skip the confirmation prompt

**Aliases:** `volume rm`
