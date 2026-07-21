# inoio-sandbox: Project-scoped home volume with config injection

## Summary

Replace the current per-project `.local` / `.cache` volume pair with a single
project-scoped home volume that persists all of `/home/dev`. This lets opencode
warm its own caches naturally across invocations, without us pre-populating
caches we cannot predict. Config injection is handled by merging host user,
project, and inoio-sandbox fragments into `/home/dev/.config/opencode` at VM
boot time.

## Decisions

| Area | Decision | Rationale |
|---|---|---|
| Home persistence | One named volume per project + image hash: `{project}-opencode-home-{image_hash}` mounted at `/home/dev`. | Caches `.opencode` install and naturally accumulated state; image hash keeps image updates safe. |
| `.local` / `.cache` | Folded into the home volume. | Simpler than nested volumes; opencode decides what to cache. |
| Config location | `/home/dev/.config/opencode` lives inside the home volume. | Opencode can write its own state and it persists. |
| Config injection | Host user dir `~/.config/inoio-sandbox/opencode/` and project dir `.sandbox/opencode/` are merged in. | Users and projects can add plugin config without rebuilding the image. |
| Config precedence | Image base → user injection → project injection → inoio-sandbox LiteLLM config (later wins). | Matches stated ownership: project overrides user, inoio-sandbox overrides both. |
| Config merge mechanism | Launcher merges fragments on host, injects result via `--copy-dir`, `--script` copies into home volume at setup. | Avoids writing into msb named volumes from the host; keeps merge logic in Python. |
| `OPENCODE_CONFIG_CONTENT` | Retire it; write provider config into merged `opencode.jsonc` under `provider.litellm`. | Single source of truth for opencode config inside the VM. |
| Image update handling | New image hash → new home volume. Old volumes kept until manually pruned. | Guarantees `.opencode` matches the image; avoids complex in-place upgrades. |
| Reset | `--reset-home` flag removes and recreates the project's home volume. | Escape hatch for stale state. |

## Architecture

```text
Host
├── inoio-sandbox launcher
│   ├── image.py      — build/load runner image, compute Dockerfile hash
│   ├── worktree.py   — find/create git worktree
│   ├── volumes.py    — ensure home volume, prefill from image, reset
│   ├── config.py     — merge user + project + LiteLLM config fragments
│   ├── runner.py     — assemble msb run command
│   ├── secrets.py    — map host env vars to msb --secret flags
│   └── doctor.py     — preflight checks
├── data/
│   ├── Dockerfile
│   └── provider-config.json
├── ~/.config/inoio-sandbox/opencode/     (user injection)
└── /tmp/inoio-sandbox-config-{random}/   (merged config staging, temp dir)

Project repo
└── .sandbox/
    ├── Dockerfile                        (optional)
    ├── env                               (optional)
    └── opencode/                         (project injection)

MicroVM (one per invocation)
├── runner image
├── worktree → /home/dev/workspace
├── {project}-opencode-home-{hash} → /home/dev
│   ├── .opencode/        (image-baked install, persists)
│   ├── .config/opencode/ (merged + runtime-written config)
│   ├── .cache/           (naturally warmed caches)
│   └── .local/           (naturally warmed state)
└── opencode
```

## Components

| Component | Responsibility |
|---|---|
| `image.py` | Build `.sandbox/Dockerfile` (or default), load into msb, compute short Dockerfile hash. |
| `volumes.py` | Ensure the home volume exists; prefill from image when missing; handle `--reset-home`; provide host-directory fallback. |
| `config.py` | Read user and project injection directories, merge with `provider-config.json`, write merged JSON/non-JSON files to a temporary staging directory. |
| `runner.py` | Assemble `msb run` with home volume mount, `--copy-dir` of merged config, `--script` that copies config into place, secrets, env, worktree. |
| `cli.py` | Add `--reset-home` flag; wire image hash through to volume creation. |

## Data flow

1. User runs `inoio-sandbox run [--reset-home]`.
2. Launcher resolves project slug and branch, finds/creates worktree.
3. Launcher builds/loads runner image and computes its short hash.
4. `volumes.py` ensures `{project}-opencode-home-{hash}` exists. If `--reset-home` or volume missing, it creates and prefills it from the image's `/home/dev`.
5. `config.py` builds the merged config into a temporary staging directory:
   - For every `.json` / `.jsonc` file present in any source, deep-merge nested
     dictionaries in precedence order (image base → user → project; for
     `opencode.jsonc`/`opencode.json`, also merge `provider-config.json` as
     `provider.litellm`).
   - For every non-JSON file, copy from the highest-precedence source that
     provides it (project overrides user overrides image base).
   - Write result to a temp dir such as `/tmp/inoio-sandbox-config-{random}/`.
6. `runner.py` builds the msb command:
   ```bash
   msb run \
     -v {project}-opencode-home-{hash}:/home/dev \
     --copy-dir {config-tmp}:/tmp/inject/opencode \
     --script setup='mkdir -p /home/dev/.config/opencode && cp -r /tmp/inject/opencode/. /home/dev/.config/opencode/ && (chown -R dev:dev /home/dev/.config/opencode || true)' \
     ... {image} -- opencode
   ```
7. MicroVM boots. The `--script` runs after volume mounts and copies the
   injected config from the rootfs into the persistent home volume.
8. `opencode` runs with merged config and a warm home directory.

## Volume lifecycle

### Create and prefill

If the volume does not exist:

```bash
msb volume create {project}-opencode-home-{hash}
msb run -v {project}-opencode-home-{hash}:/mnt/home {image} -- /bin/sh -c \
  'cp -a /home/dev/. /mnt/home/ && chown -R dev:dev /mnt/home'
```

This seeds the volume with the image's home directory, including the
`.opencode` install, and ensures the volume root is owned by `dev` so later
runs can write to it.

### Normal use

Mount the existing volume at `/home/dev`. No prefill step.

### Reset

`--reset-home` removes the volume and recreates it:

```bash
msb volume remove {project}-opencode-home-{hash}
# then create + prefill as above
```

### Image updates

A new Dockerfile hash produces a new volume name. The launcher treats it like a
first run for that project/image combination. Old image-hash volumes are **not**
auto-pruned; the user can remove them with `msb volume remove` when desired.

## Config merge rules

### Sources

| Source | Path | Precedence |
|---|---|---|
| Image base | captured from `/home/dev/.config/opencode` during prefill | lowest |
| User injection | `~/.config/inoio-sandbox/opencode/` | middle |
| Project injection | `.sandbox/opencode/` | higher |
| inoio-sandbox LiteLLM | `data/provider-config.json` | highest |

### JSON files (`.json`, `.jsonc`)

- For every JSON/JSONC filename that exists in at least one source, load all
  available versions.
- Deep-merge nested dictionaries in precedence order: image base → user →
  project.
- Arrays are replaced, not merged.
- For `opencode.jsonc` or `opencode.json`, additionally deep-merge
  `provider-config.json` as the value of `provider.litellm` after the project
  source, so inoio-sandbox provider definitions take precedence.
- Write merged JSON files as plain JSON (valid JSONC) preserving the original
  filename.

### Non-JSON files

- Copy each non-JSON file from the highest-precedence source that provides it.
- Project overrides user; user overrides image base.

### LiteLLM provider config

`data/provider-config.json` is merged as the value of `provider.litellm` in the
final `opencode.jsonc`. This preserves any other keys the user or project set,
while ensuring inoio-sandbox model definitions take precedence.

## CLI changes

- Add `--reset-home` flag to `inoio-sandbox run`.
- No change to other commands.

## Security considerations

- API keys continue to use `msb --secret`; they do not enter the VM as literals.
- `.envrc` files in the worktree are still removed from the VM via `--rm`.
- User injection directory is read by the launcher on the host only; only the
  merged result enters the VM.
- Project injection comes from the repo worktree, which is already trusted since
  it is mounted into the VM.

## Trade-offs

| Pros | Cons |
|---|---|
| Natural cache warming; no need to predict opencode's cache layout | First start for a new project or image hash is cold |
| Single volume per project simplifies state management | Home volume can accumulate stale state; `--reset-home` needed |
| Config injection supports user and project customization | Old image-hash volumes need manual pruning |
| Opencode can persist its own config/state | Image updates briefly lose cache warmth until new volume warms |
| Removes the 64 MB read-only host `~/.config/opencode` mount | `--script` must run after volume mounts for config injection to work |

## Alternatives considered

- **Persistent warm VM**: faster but breaks ephemerality and requires lifecycle management.
- **Separate `.local` / `.cache` volumes with config overlay**: keeps current model but does not cache image-baked `.opencode` state or home-level files.
- **Read-only host config overlay**: simpler launcher but config would not persist inside the home volume; opencode runtime writes would need a separate path.

## Open questions

1. Does `--script` run after volume mounts and with sufficient permissions to
   write into `/home/dev/.config/opencode`? If not, the design must fall back
   to a wrapper command.
2. *(Resolved)* No auto-pruning of old image-hash home volumes for the MVP.
3. *(Resolved)* No separate `--reset-config` flag for the MVP; `--reset-home`
   recreates the entire volume including config.
