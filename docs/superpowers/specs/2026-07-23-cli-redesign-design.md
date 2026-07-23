# CLI Redesign Design

Date: 2026-07-23
Status: Approved (pending user spec review)

## Context

The current CLI (`cmd/opencode-msb/cli.go`) uses cobra with two subcommands (`doctor`, `run`) where `run` is implicit. The `run` command has 9 flags, several of which are action-flags masquerading as run config (`--image-rebuild`, `--reset-home`, `--test-run`). BACKLOG.md:10 already flags this: *"refactor cli, subcommands for image rebuilding, ...? maybe remove some flags like --reset-home?"*.

The CLI library is `github.com/spf13/cobra v1.10.2`. The tool targets consistency with `msb` (microsandbox CLI), which the users already work with daily.

## Decisions

- **Backward compatibility:** Clean break. Pre-1.0, old flag names do not need to keep working.
- **Command style:** Aligned with msb — flat verbs for primary operations, noun groups for entity types with multiple operations, flat↔noun aliases where both forms work.
- **Bare invocation:** `opencode-msb` with no subcommand (or flags-only) implicitly dispatches to `run`.
- **`clean` subcommand:** Deferred to a future design. `--reset-home` removed now with no replacement.
- **Volume fallback:** Removed entirely (flag + automatic fallback). Fail early if msb volume creation fails.

## Command Tree

```
opencode-msb                              # implicit run
opencode-msb run        [flags] [ARGS...]  # run opencode in the sandbox VM
opencode-msb doctor                        # check prerequisites
opencode-msb build      [-r]               # build/rebuild runner image
opencode-msb list                          # list sandboxes for this host
opencode-msb shell      [flags]            # start sandbox, exec a shell (debug)
opencode-msb config     show               # print merged opencode config
opencode-msb image      list (ls)          # list cached runner images
opencode-msb volume     list (ls)          # list managed volumes
```

### Implicit-run rule

Bare `opencode-msb`, or `opencode-msb <flags>...`, dispatches to `run`. This is implemented by checking whether `args[0]` matches a registered subcommand name (or is `help`/`--help`/`-h`/`--version`/`--tree`). The current `isKnownSubcommand` switch is replaced by querying cobra's own command registry so new subcommands are automatically recognized.

### Flat ↔ noun alias pairs

Both forms dispatch identically:

| Flat verb | Noun form |
|---|---|
| `run` | `sandbox run` |
| `shell` | `sandbox shell` |
| `list` | `sandbox list` |
| `build` | `image build` |

Additional aliases: `list` also has `ls`.

### Noun-only commands (no flat alias)

| Command | Why no flat alias |
|---|---|
| `image list` | Would conflict with `list` (which lists sandboxes) |
| `volume list` | Would conflict with `list` |
| `config show` | `config` is already a noun; `show` is the only verb for now |

### Standalone (no noun equivalent)

| Command | Why |
|---|---|
| `doctor` | Health check, not an entity operation (matches msb's `doctor`) |

## New Commands

### `build`

Builds the runner image only (no sandbox, no opencode). Uses the existing `EnsureImage` logic. `--rebuild` forces a clean rebuild (passes `NoCache: true` to Docker). Without `--rebuild`, builds only if the image is missing.

Replaces the need to run with `--image-rebuild` just to refresh the image. Also available as `image build` (noun alias).

### `list`

Lists sandboxes created by opencode-msb on this host. Calls the msb SDK to list sandboxes and filters to names with the `opencode-msb-` prefix (the naming convention from `sandboxName` in runner.go:84). Shows: sandbox name, project, branch, status. The code already detects conflicting sessions internally (`runningSandboxExists`); this exposes it as a user-facing query.

Also available as `sandbox list` / `ls`.

### `shell`

Starts the sandbox the same way `run` does (resolve workspace, build image, setup volumes, create sandbox, provision config) but execs `/bin/bash` instead of opencode. Useful for debugging the image/environment/config without launching opencode. Does not perform managed-repo cleanup afterward (the sandbox is ephemeral; the managed repo is left for the user to handle or re-run `run`).

Also available as `sandbox shell`.

### `config show`

Prints the merged opencode config that would be injected into the sandbox. For each resolved file, shows:
- The output filename (e.g. `opencode.jsonc`)
- Source path(s): user config dir, project config dir, embedded provider config
- For JSON files: how the merge was applied (base + override)
- For non-JSON files: which source was picked

This is a diagnostic command — helps debug config issues without starting a sandbox. Calls the existing `loadConfigFiles` logic and prints the results.

### `image list`

Lists runner images cached in msb, filtered to the `opencode-msb/runner` tag prefix. Shows: image reference, digest, size. Calls the msb SDK image listing API (or equivalent).

### `volume list`

Lists msb volumes managed by opencode-msb, filtered to names with the `opencode-msb-` prefix (or `<project>-opencode-home-` pattern from `HomeVolumeName` in volumes.go:20). Shows: volume name, project, associated image digest. Calls the msb SDK volume listing API (or equivalent).

## Flags

### Global persistent flags

Available on all commands (registered on the root command as persistent flags):

| Flag | Short | Default | Purpose |
|---|---|---|---|
| `--yes` | `-y` | `false` | Assume yes to all prompts (elevated from run-only to persistent) |
| `--verbose` | `-v` | `false` | Show debug-level output |
| `--quiet` | `-q` | `false` | Suppress non-error output (errors and warnings only) |
| `--tree` | — | `false` | Print the full command tree and exit (analogous to msb `--tree`) |

### `run` and `shell` shared flags

| Flag | Short | Default | Purpose |
|---|---|---|---|
| `--branch` | `-b` | `""` | Run/shell in an isolated git clone for the given branch |
| `--cpus` | `-c` | `0` (all) | vCPUs for the sandbox |
| `--memory` | `-m` | `4G` | Memory limit (e.g. `4G`, `512M`) |
| `--rebuild` | `-r` | `false` | Rebuild the runner image before starting |

### `run`-only flags

| Flag | Short | Default | Purpose |
|---|---|---|---|
| `--dry-run` | `-n` | `false` | Validate full setup (image, volumes, sandbox) without running opencode |
| `--no-auto` | — | `false` | Do not pass `--auto` to opencode |

### `build` flags

| Flag | Short | Default | Purpose |
|---|---|---|---|
| `--rebuild` | `-r` | `false` | Force a clean rebuild (otherwise builds only if missing) |

### Commands with no flags (beyond global)

`doctor`, `list`, `config show`, `image list`, `volume list`

## Removed Flags

| Old flag | Replaced by | Why |
|---|---|---|
| `--image-rebuild` | `--rebuild`/`-r` (on run) + `build` subcommand | Renamed + standalone action available |
| `--volume-fallback` | Removed entirely | Automatic fallback removed; fail early if msb volume creation fails |
| `--reset-home` | Nothing (deferred to future `clean` subcommand) | Destructive action belongs in its own command |
| `--test-run` | `--dry-run`/`-n` | Standard naming convention |

## Flag Shortcuts

Shortcuts align with msb where applicable:

| Flag | Short | msb equivalent | Match? |
|---|---|---|---|
| `--yes` | `-y` | `-y` (msb doctor/self) | Yes |
| `--verbose` | `-v` | (msb uses `--debug` etc.) | N/A — different approach, simpler |
| `--quiet` | `-q` | `-q` | Yes |
| `--branch` | `-b` | (msb has no branch concept) | N/A |
| `--cpus` | `-c` | `-c` | Yes |
| `--memory` | `-m` | `-m` | Yes |
| `--rebuild` | `-r` | (msb uses `-f` for force) | Different but `-r` is intuitive for rebuild |
| `--dry-run` | `-n` | (msb uses `--dry-run` no shortcut) | `-n` is standard unix convention (`make -n`, `rsync -n`) |
| `--no-auto` | — | (no msb equivalent) | No shortcut — rarely used flag |
| `--tree` | — | `--tree` | Yes |

## Verbose/Quiet Message Levels

The logger (`internal/log/log.go`) currently has only `Info`, `Warn`, `Error`. A new `Debug` method is added for verbose output. A level field controls which messages are emitted. The spinner respects the level too (hidden in quiet mode).

### Normal (default) — what users see today

Spinner steps (with elapsed time):
- "Building base runner image" (image.go)
- "Building runner image" (image.go)
- "Loading image into microsandbox" (image.go)
- "Preparing home volume" (volumes.go)
- "Checking microsandbox runtime" (runner.go)
- "Starting sandbox VM" (runner.go)

Warnings:
- "not inside a git repo; using CWD hash as project slug." (git.go)
- "{envVar} not set; related provider/API may fail." (secrets.go)
- "kept managed repo '...' on branch '...' with uncommitted changes" (runner.go)
- "failed to remove envrc {name}: {err}" (runner.go)

Errors:
- Doctor failures: docker/kvm/git/msb not found (doctor.go)

Info (final results):
- "{prompt}: using default '{key}'" — non-interactive prompt fallback (prompt.go)

### Verbose (`--verbose`/`-v`) — adds debug detail

| Message | When |
|---|---|
| Resolved workspace path + branch slug | After `resolveWorkspace` |
| Managed repo path + whether created or reused | After `EnsureManagedRepoFromRef` |
| Image digest + image ref + cached or rebuilt | After `EnsureImage` |
| Home volume name + existed or created | After `EnsureHome` |
| Sandbox name + CPUs + memory + mount paths | Before `createSandbox` |
| Config files: filename → source path(s) for each | After `loadConfigFiles` |
| Secrets detected (names only, never values) | After `BuildSecrets` |
| Env file: path + key count (not values) | After `readSandboxEnv` |
| Docker build stream output (currently discarded) | During `scanBuildOutput` |
| Git ops: commit, merge target, discard, remove | During cleanup |

Messages moved from normal to verbose:
- "test run: setup validated, skipping opencode execution" → Debug (verbose only)
- "no changes to commit; continuing cleanup" → Debug (verbose only)

### Quiet (`--quiet`/`-q`) — errors and warnings only

- Suppresses all spinner steps and info messages.
- Shows only warnings and errors.
- Fatal error always printed (already happens in main.go).

## Volume Fallback Removal

### Current behavior (to be removed)

`VolumeManager` (volumes.go) has a `fallback` bool, set from the `--volume-fallback` flag. In `EnsureHome`:
1. If `fallback` is true (flag set): skip msb, go to host-directory path.
2. Otherwise: try msb named volume. If creation fails, log warning, set `fallback = true`, fall back to host-directory path automatically.

The host-directory fallback creates a dir under `~/.local/state/opencode-msb/state/<project>/home/<digest>/` and prefills it from the runner image.

### New behavior

- `--volume-fallback` flag removed.
- Automatic fallback removed. If msb volume creation fails, return an error immediately (fail early, fail explicit).
- `VolumeManager.fallback` field deleted.
- `ensureFallbackHome`, `prefillFallback`, `fallbackHomePath` methods deleted.
- `VolumeManager` struct simplifies — may not need to be a struct (could be plain functions with stateDir + logger params).
- `prefill` logic stays (needed for msb volumes — copies `/home/dev/` from runner image into the volume via a temporary sandbox).

## Clean Deferral

### This phase

- `--reset-home` removed with no replacement.
- `RunOptions.ResetHome` field deleted.
- Its usage in `EnsureHome` (the `if reset { msb.RemoveVolume }` branch) deleted.
- No `clean` subcommand.

### Future `clean` design (noted, not implemented)

When `clean` is added, it should address:

**Entities to clean:**
- Home volumes (msb named volumes, keyed by `<project>-opencode-home-<digest>`)
- Managed repos (independent git clones under `~/.local/state/opencode-msb/isolated-workspaces/`)
- Stale sandboxes (stopped/crashed sandboxes with `opencode-msb-` prefix)

**Filters:**
- `--project` (default: current project) — scope to one project
- `--branch` — scope to one branch within a project
- `--all` — across all projects

### Open question: home-per-image-digest

Currently, `HomeVolumeName(projectSlug, imageDigest)` creates a new home volume for every image digest change. This means every image rebuild loses all opencode state (history, sessions, cache). This may be too aggressive — most image rebuilds change tooling, not opencode compatibility.

Options to explore in the future `clean` phase:
- Key the home volume by project only (not digest) — state persists across image changes
- Key by digest but provide `clean home` to reset when needed
- Migrate home volume content when digest changes

For now: note as a known issue, don't change the behavior.

## README CLI Documentation Rework

### Current problems

1. Usage section only shows 4 examples — doesn't reflect the new command tree.
2. Flags table is a flat list — no distinction between global and per-command flags, no shortcuts shown, mixes action-flags with config-flags.
3. No command reference — `doctor` is mentioned in usage but not documented; `build`, `list`, `shell`, `config` aren't mentioned at all.
4. Branch sessions section documents `--yes` behavior inline but it's now a global flag.

### Proposed README structure

```markdown
## Usage

opencode-msb                    # run opencode in a microsandbox VM
opencode-msb -b my-feature      # run in an isolated git clone
opencode-msb doctor             # check prerequisites
opencode-msb build -r           # rebuild the runner image
opencode-msb list               # list running sandboxes

## Commands

| Command | Aliases | Purpose |
|---|---|---|
| `run` (default) | `sandbox run` | Run opencode in the sandbox VM |
| `doctor` | — | Check host prerequisites (docker, kvm, git, msb) |
| `build` | `image build` | Build or rebuild the runner image |
| `list` | `ls`, `sandbox list` | List sandboxes for this host |
| `shell` | `sandbox shell` | Start sandbox and open a shell (debug) |
| `config show` | — | Print merged opencode config (debug) |
| `image list` | `image ls` | List cached runner images |
| `volume list` | `volume ls` | List managed volumes |

## Flags

### Global

| Flag | Short | Default | Purpose |
|---|---|---|---|
| `--yes` | `-y` | false | Assume yes to all prompts |
| `--verbose` | `-v` | false | Show debug-level output |
| `--quiet` | `-q` | false | Suppress non-error output |
| `--tree` | — | false | Print the full command tree and exit |

### Run / Shell

| Flag | Short | Default | Purpose |
|---|---|---|---|
| `--branch` | `-b` | "" | Isolated git clone for the given branch |
| `--cpus` | `-c` | 0 (all) | vCPUs for the sandbox |
| `--memory` | `-m` | 4G | Memory limit (e.g. 4G, 512M) |
| `--rebuild` | `-r` | false | Rebuild the runner image before starting |

### Run only

| Flag | Short | Default | Purpose |
|---|---|---|---|
| `--dry-run` | `-n` | false | Validate setup without running opencode |
| `--no-auto` | — | false | Do not pass --auto to opencode |

### Build

| Flag | Short | Default | Purpose |
|---|---|---|---|
| `--rebuild` | `-r` | false | Force a clean rebuild |
```

### Key changes from current README

1. Usage updated to show new commands with shortcuts.
2. Commands table added — shows command, aliases, purpose. Replaces the old implicit examples-only approach.
3. Flags restructured into subsections: Global, Run/Shell (shared), Run-only, Build. No more flat table mixing everything.
4. Removed flags (`--image-rebuild`, `--volume-fallback`, `--reset-home`, `--test-run`) no longer appear.
5. Branch sessions section stays but updated: `--yes` is referenced as a global flag, not a run flag. The `-y` shortcut is noted.
6. Shortcuts are shown in every table — currently they're invisible.

## Files Affected

- `cmd/opencode-msb/cli.go` — main rewrite: new commands, flag restructuring, alias registration, `--tree` support, implicit-run fix
- `cmd/opencode-msb/main.go` — no changes expected
- `internal/sandbox/runner.go` — `RunOptions` struct updated (remove `ResetHome`, `VolumeFallback`, `TestRun`; rename `ImageRebuild` → `Rebuild`); `Run` function updated for new flag handling
- `internal/sandbox/volumes.go` — remove `VolumeManager.fallback` field, `ensureFallbackHome`, `prefillFallback`, `fallbackHomePath`; simplify `EnsureHome` to fail on msb error
- `internal/log/log.go` — add `Debug` method, add level field, add level-aware filtering
- `internal/log/spinner.go` — respect log level (hide in quiet mode)
- `README.md` — full CLI documentation rework
- `BACKLOG.md` — mark CLI refactor item as done, update clean/volume items

## Out of Scope

- `clean` subcommand (deferred to future design)
- Home-per-image-digest redesign (noted as future work)
- Config file for CLI settings in `.opencode-msb` (BACKLOG.md:13, separate effort)
- Making CLI output "pretty and UX'd" (BACKLOG.md:14, separate effort)
