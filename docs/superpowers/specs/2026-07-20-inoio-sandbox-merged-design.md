# inoio-sandbox: Merged Design Spec

A standalone launcher that runs opencode inside an ephemeral microsandbox
(msb) microVM, binding the project as a git worktree and persisting opencode
state in msb named volumes. API keys reach the VM via `msb --secret` so the
real values never enter the VM.

This spec merges `2026-07-20-opencode-msb-design.md` and
`2026-07-20-inoio-sandbox-design.md`. Resolved conflicts are called out in
each section.

## Decisions

| Area | Decision | Source / rationale |
|---|---|---|
| Project name | `inoio-sandbox` (launcher command + shell alias `opencode`). | Spec 2 |
| Platform | Linux only (KVM). macOS deferred (libkrun path). | Spec 2 |
| Launcher language | Python 3 + click. | Both |
| Secrets | `msb --secret` — real API keys never enter the VM. | Spec 1 |
| Session model | One ephemeral VM per `opencode` invocation. No session tracking, no snapshots. | Spec 1 |
| Workspace | Git worktree of the host project, mounted read-write. | Spec 1 |
| State persistence | msb named volumes per project; host-directory fallback if volumes prove unreliable. | Spec 1 |
| State scope | Per project (all sessions for the same repo share state; worktrees do not duplicate state). | Spec 1 |
| Distribution | Standalone install (tool in PATH); per-project opt-in via `<repo>/.sandbox/`. | Spec 2 |
| Image | `.sandbox/Dockerfile` if present, else shipped default; hash-cached via `docker build \| msb load`. | Hybrid |
| opencode config | Host `~/.config/opencode` mounted read-only; provider/models injected via `OPENCODE_CONFIG_CONTENT`. | Both |
| Network | No rules for the MVP; full egress so web search and package installs work. | Both |

## Architecture

```text
Host
├── inoio-sandbox (Python/click launcher)
│   ├── image.py      — build/load (hash-cached)
│   ├── worktree.py   — find/create worktree
│   ├── volumes.py    — ensure msb volumes; host-directory fallback
│   ├── config.py     — provider fragment + OPENCODE_CONFIG_CONTENT
│   ├── secrets.py    — map host env to msb --secret flags
│   └── doctor.py     — preflight checks
├── Dockerfile (default) → inoio-sandbox/runner:<hash>
├── provider-config.json (inoio LiteLLM fragment)
├── per-project msb volumes
│   ├── <project>-opencode-local
│   └── <project>-opencode-cache
├── host ~/.config/opencode mounted read-only
├── per-project worktrees
│   └── ~/.local/share/inoio-sandbox/worktrees/<project>/<name>/
└── <repo>/.sandbox/{Dockerfile,env}   (optional, versioned)

MicroVM (one per invocation)
├── runner image (default or .sandbox/Dockerfile)
├── worktree → /home/dev/workspace
├── host config → /home/dev/.config/opencode (read-only)
├── volumes → /home/dev/.local, /home/dev/.cache
└── opencode with OPENCODE_CONFIG_CONTENT
```

## Components

| Component | Responsibility |
|---|---|
| `inoio-sandbox` CLI | Parse args and dispatch subcommands. |
| `image.py` | Build `.sandbox/Dockerfile` (or default) and load into msb when hash not cached. |
| `worktree.py` | Find an existing worktree or create one under the launcher data directory. |
| `volumes.py` | Ensure msb named volumes exist; provide host-directory fallback. |
| `config.py` | Load repo provider fragment and prepare `OPENCODE_CONFIG_CONTENT`. |
| `secrets.py` | Map host env vars to `msb --secret` flags. |
| `doctor.py` | Verify `msb`, Docker, git, Python, and `/dev/kvm` are available. |
| `Dockerfile` | Default runner image (minimal: debian + opencode). |
| `provider-config.json` | Shared inoio LiteLLM provider and model catalog. |
| `install.sh` | Bootstrap installer: pipx/venv install, verify `msb`, offer `opencode` shell alias. |
| `<repo>/.sandbox/{Dockerfile,env}` | Optional per-project image customization and extra env vars. |

## Data flow

1. User runs `opencode` (alias → `inoio-sandbox`) in a git repository.
2. Launcher resolves the project slug (a short hash of the repo path returned
   by `git rev-parse --git-common-dir`; falls back to a hash of CWD with a
   warning if not in a git repo) and the current branch.
3. Launcher checks the cached msb image for the current Dockerfile hash;
   builds and loads if missing. Uses `.sandbox/Dockerfile` if present,
   else the shipped default.
4. Launcher finds or creates a worktree for the branch.
5. Launcher ensures the two msb named volumes for the project.
6. Launcher reads `provider-config.json` and sets `OPENCODE_CONFIG_CONTENT`.
7. Launcher runs:

   ```bash
   msb run \
     -v <worktree>:/home/dev/workspace \
     -v ~/.config/opencode:/home/dev/.config/opencode:ro \
     -v <project>-opencode-local:/home/dev/.local \
     -v <project>-opencode-cache:/home/dev/.cache \
     -w /home/dev/workspace \
     -e OPENCODE_CONFIG_CONTENT=<fragment> \
     --secret LITELLM_API_KEY@litellm.inoio.de \
     inoio-sandbox/runner:<hash> \
     -- opencode
   ```

8. opencode runs interactively; when it exits, the ephemeral VM is removed.

## State management

opencode uses standard XDG paths. The user's config is mounted read-only from
the host; runtime state and cache live in msb named volumes:

| Mount | Guest path | Contents |
|---|---|---|
| `~/.config/opencode:ro` | `/home/dev/.config/opencode` | user's `opencode.jsonc`, personal settings |
| `<project>-opencode-local` | `/home/dev/.local` | `share/opencode`, `state/opencode`, shell history |
| `<project>-opencode-cache` | `/home/dev/.cache` | package/plugin cache |

Keeping `.local` and `.cache` separate makes it easy to reset cache without
losing state.

### Fallback

If msb volumes are unreliable, replace the two volume mounts with host
directories under `~/.local/share/inoio-sandbox/state/<project>/{local,cache}`.
The host config mount stays read-only. Only the mount source changes; the rest
of the launcher stays the same.

## Worktree management

- If the current directory is already a git worktree, use it.
- If `--worktree <name>` is given, use/create that worktree under
  `~/.local/share/inoio-sandbox/worktrees/<project>/<name>/`.
- Otherwise create a default worktree named after the branch.
- The worktree is mounted read-write at `/home/dev/workspace`.
- Worktrees are ordinary host directories that persist across invocations, so
  multiple invocations on the same branch reuse the same worktree directory.
  Each invocation still gets its own ephemeral VM; there is no VM session
  reuse. Sessions on different branches are isolated.

## Image build

- The launcher hashes the contents of the Dockerfile being used
  (`.sandbox/Dockerfile` if present, else the shipped default).
- Image tag: `inoio-sandbox/runner:<sha256-short>`.
- If `msb images` does not contain the tag:
  1. `docker build -t inoio-sandbox/runner:<hash> .`
  2. `docker save inoio-sandbox/runner:<hash> | msb load --tag inoio-sandbox/runner:<hash>`
- `--image-rebuild` forces a rebuild.

## Provider config

- The repo ships `provider-config.json` with the inoio LiteLLM provider,
  base URL, and model catalog.
- The launcher reads this file and passes it as `OPENCODE_CONFIG_CONTENT`.
- The fragment contains `"apiKey": "{env:LITELLM_API_KEY}"` so opencode
  resolves the key from the VM environment.
- `OPENCODE_CONFIG_CONTENT` deep-merges with the user's existing
  `~/.config/opencode/opencode.jsonc`, so personal settings are preserved while
  the provider section is overridden/extended.

## Secrets

- The launcher reads `LITELLM_API_KEY` from the host environment and passes
  `--secret LITELLM_API_KEY@litellm.inoio.de` to `msb run`.
- Inside the VM, `LITELLM_API_KEY` is set to the placeholder
  `$MSB_LITELLM_API_KEY`.
- opencode sends the placeholder to `litellm.inoio.de`; microsandbox swaps it
  for the real key at the host boundary.
- Additional secrets (e.g. `GITHUB_TOKEN`) can be added to a whitelist in the
  launcher.

## Network

- Default egress is allowed.
- No `--no-net` or `--net-rule` flags for the MVP.

## Session model

- One fresh VM per `opencode` invocation.
- No session tracking, no snapshots, no "last session" logic.
- opencode state survives because it lives in msb volumes, not in the VM.

## Distribution

Standalone install (tool in PATH). The launcher is installable via pipx, uv, or
a venv managed by the bootstrap installer (`install.sh`). The installer also
offers to add an `opencode` (sandboxed opencode) shell alias that points at the launcher.

Per-project opt-in via `<repo>/.sandbox/`:
- `Dockerfile` — overrides the default Dockerfile.
- `env` — extra `KEY=VAL` lines merged into the VM environment.

Absent `.sandbox/` ⇒ use the shipped default Dockerfile to build an image.

## Error handling

| Failure | Behavior |
|---|---|
| `msb` missing | `doctor` prints install link and exits. |
| Docker missing | Image build fails with a clear error message. |
| `/dev/kvm` missing (Linux) | Error with instructions to join `kvm` group or load modules. |
| Dockerfile build fails | Surface Docker output and exit non-zero. |
| Worktree creation fails | Surface git output and exit. |
| Volume creation fails | Use host-directory fallback if enabled, else exit. |
| Secret env var missing | Warn; opencode will fail to authenticate, which is visible to the user. |
| opencode crashes | VM exits with opencode's exit code and is cleaned up. |
| Stale VM from host crash | If the host crashed mid-session, a VM may remain. On launch, detect via `msb ls`; offer `--force` to `msb stop && msb rm` before starting. |

## Testing

- **Unit tests (pytest):**
  - Project slug and branch resolution.
  - Dockerfile hash calculation.
  - Volume name generation.
  - Provider fragment loading.
  - Secret flag generation.
  - `.sandbox/env` merging.
- **Integration test (host-only, needs `/dev/kvm`):**
  - Run `inoio-sandbox` in a temporary git repo.
  - Verify the VM boots, opencode starts, files written in the VM appear on the
    host worktree, and the project volumes exist.
- **Smoke test:** `inoio-sandbox doctor` passes on a clean CI runner.
- **CI:** `.gitlab-ci.yml` with stages for lint (ruff), unit tests, and an
  optional host-runner integration smoke test (disabled by default until a
  KVM-enabled runner is available).

  ```yaml
  stages:
    - lint
    - test

  lint:
    stage: lint
    image: python:3.12-slim
    script:
      - pip install ruff
      - ruff check .

  unit-tests:
    stage: test
    image: python:3.12-slim
    script:
      - pip install -e ".[test]"
      - pytest tests/unit
  ```

## Security notes

- Real API keys are never passed as plain `-e` env vars into the VM (via
  `msb --secret`).
- The user's personal `~/.config/opencode` is mounted read-only from the host;
  each project shares the same personal config.
- `.envrc` files in the project directory may still be visible inside the VM
  because bind mounts are not masked by `--rm`. This is a documented MVP
  limitation.
- The POC's `run-sandbox.sh` hardcodes `LITELLM_API_KEY=sk-...` (line 72) — a
  real leak. The launcher must read keys from host env only, never hardcode.

## Scope

### In (MVP)

- Install script (bash)
- Launcher (Python + click): repo-key resolution, state-dir setup, image
  build, worktree management, VM run, provider + secret injection
- Default Dockerfile image
- Provider config fragment
- README HOWTO (minimal, concise)

### Out (deferred)

- Network egress rules.
- Hiding `.envrc` secrets from the VM.
- Snapshot-based persistence.
- macOS support (libkrun path documented for later).

## Open questions / risks

1. **Config changes inside the VM:** Because `~/.config/opencode` is mounted
   read-only, any settings changed by opencode during a session are not
   persisted. Acceptable for MVP because runtime state lives in `.local` and
   `.cache`.
2. **`--secret` with open egress:** Verify that `--secret` works while default
   egress is allowed.
3. **`OPENCODE_CONFIG_CONTENT` deep-merge:** Verify whether opencode deep-merges
   (preserving personal providers) or replaces the `provider` section. If it
   merges, consider stripping the user's provider entries from the mounted
   config before mount, or accept the inoio provider as default for MVP.
4. **Provider fragment size:** `OPENCODE_CONFIG_CONTENT` may be ~15 KB. Verify
   it does not hit shell/env limits on Linux.
5. **Plugin cache sharing:** Per-project volumes isolate plugin caches. This is
   safer but may duplicate downloads; revisit if startup is slow.
