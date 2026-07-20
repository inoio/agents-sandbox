# opencode-msb: Design Spec

A minimal launcher that runs opencode inside an ephemeral microsandbox VM,
binding the project as a git worktree and persisting opencode state in msb
named volumes.

This spec is intentionally separate from `2026-07-20-inoio-sandbox-design.md`;
both can be reviewed and merged later.

## Decisions

| Area | Decision |
|---|---|
| Distribution | Defer final decision; support both global install and per-project embedding via submodule/copy. |
| Platform | Linux (KVM) and macOS (Apple Silicon). |
| Launcher language | Python 3 + click. |
| Base image | Dockerfile-first; built with Docker and loaded into msb via `docker save \| msb load`. |
| Session model | One ephemeral VM per `opencode` invocation. No persistent per-project VM, no snapshots. |
| Workspace | Git worktree of the host project, mounted read-write. |
| State persistence | msb named volumes per project; host-directory fallback if volumes prove unreliable. |
| State scope | Per project (all sessions for the same repo share state; worktrees do not duplicate state). |
| opencode config | Host `~/.config/opencode` mounted read-only; the repo's provider fragment is injected via `OPENCODE_CONFIG_CONTENT`. |
| Secrets | `msb --secret` to keep real API keys out of the VM. |
| Network | No rules for the MVP; full egress so web search and package installs work. |

## Goals

- A dev can run `opencode-msb` (or an `opencode` alias) in any project and get
  an isolated VM with inoio models already configured.
- Image rebuilds do not destroy opencode state.
- Concurrent sessions are possible via git worktrees.
- The launcher is small, testable, and easy to understand.

## Non-goals (MVP)

- Network egress rules.
- Hiding `.envrc` secrets from the VM (documented limitation).
- Snapshot-based persistence.
- Multi-key secret management UI.
- Windows support.

## Architecture

```text
Host
├── opencode-msb (Python/click launcher)
│   ├── image build/load
│   ├── worktree find/create
│   ├── volume ensure
│   └── msb run
├── Dockerfile → opencode-msb/runner:<hash>
├── provider-config.json (inoio LiteLLM fragment)
├── per-project msb volumes
│   ├── <project>-opencode-local
│   └── <project>-opencode-cache
├── host `~/.config/opencode` mounted read-only
└── per-project worktrees
    └── ~/.local/share/opencode-msb/worktrees/<project>/<name>/

MicroVM (one per invocation)
├── runner image
├── worktree → /home/dev/workspace
├── host config → /home/dev/.config/opencode (read-only)
├── volumes  → /home/dev/.local, /home/dev/.cache
└── opencode with OPENCODE_CONFIG_CONTENT
```

## Components

| Component | Responsibility |
|---|---|
| `opencode-msb` CLI | Parse args and dispatch. |
| `image.py` | Build `Dockerfile` and load the image into msb when the hash is not cached. |
| `worktree.py` | Find an existing worktree or create one under the launcher data directory. |
| `volumes.py` | Ensure msb named volumes exist; provide host-directory fallback. |
| `config.py` | Load the repo provider fragment and prepare `OPENCODE_CONFIG_CONTENT`. |
| `secrets.py` | Map host env vars to `msb --secret` flags. |
| `doctor.py` | Verify `msb`, Docker, git, Python, and `/dev/kvm` (Linux) are available. |
| `Dockerfile` | Defines the runner image. |
| `provider-config.json` | Shared inoio LiteLLM provider and model catalog. |
| `.gitlab-ci.yml` | CI pipeline: lint, unit tests, and an optional integration test. |

## Data flow

1. User runs `opencode-msb` in a git repository.
2. Launcher resolves the project slug and current branch.
3. Launcher checks the cached msb image for the current Dockerfile hash;
   builds and loads if missing.
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
     opencode-msb/runner:<hash> \
     -- opencode
   ```

8. opencode runs interactively; when it exits, the ephemeral VM is removed.

## State management

opencode uses standard XDG paths. The user's config is mounted read-only from the
host; runtime state and cache live in msb named volumes:

| Mount | Guest path | Contents |
|---|---|---|
| `~/.config/opencode:ro` | `/home/dev/.config/opencode` | user's `opencode.jsonc`, personal settings |
| `<project>-opencode-local` | `/home/dev/.local` | `share/opencode`, `state/opencode`, shell history |
| `<project>-opencode-cache` | `/home/dev/.cache` | package/plugin cache |

Keeping `.local` and `.cache` separate makes it easy to reset cache without
losing state.

### Fallback (checkpoint C)

If msb volumes are unreliable, replace the two volume mounts with host
directories under `~/.local/share/opencode-msb/state/<project>/{local,cache}`.
The host config mount stays read-only. Only the mount source changes; the rest
of the launcher stays the same.

## Worktree management

- If the current directory is already a git worktree, use it.
- If `--worktree <name>` is given, use/create that worktree under
  `~/.local/share/opencode-msb/worktrees/<project>/<name>/`.
- Otherwise create a default worktree named after the branch.
- The worktree is mounted read-write at `/home/dev/workspace`.
- Sessions on the same branch share the same worktree; sessions on different
  branches are isolated.

## Image build

- The launcher hashes the contents of `Dockerfile`.
- Image tag: `opencode-msb/runner:<sha256-short>`.
- If `msb images` does not contain the tag:
  1. `docker build -t opencode-msb/runner:<hash> .`
  2. `docker save opencode-msb/runner:<hash> | msb load --tag opencode-msb/runner:<hash>`
- `--image-rebuild` forces a rebuild.

## Provider config

- The repo ships `opencode-msb/provider-config.json` with the inoio LiteLLM
  provider, base URL, and model catalog.
- The launcher reads this file and passes it as `OPENCODE_CONFIG_CONTENT`.
- The fragment contains `"apiKey": "{env:LITELLM_API_KEY}"` so opencode resolves
  the key from the VM environment.
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

Decision is deferred. The launcher will be implemented so that both options are
possible:

- **Global install:** install the Python package (pipx/uv/pip) and optionally add
  an `opencode` shell alias.
- **Per-project:** copy or submodule the repository and invoke the launcher from
  the project directory.

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

## Testing

- **Unit tests (pytest):**
  - Project slug and branch resolution.
  - Dockerfile hash calculation.
  - Volume name generation.
  - Provider fragment loading.
  - Secret flag generation.
- **Integration test (manual, host-only, needs KVM):**
  - Run `opencode-msb` in a temporary git repo.
  - Verify the VM boots, opencode starts, files written in the VM appear on the
    host worktree, and the project volumes exist.
- **Smoke test:** `opencode-msb doctor` passes on a clean CI runner.
- **CI:** `.gitlab-ci.yml` pipeline with stages for lint, unit tests, and an
  optional host-runner integration smoke test.

  Example stages:

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

  The integration test needs a GitLab runner with KVM and `msb` installed;
  it is disabled by default until a suitable runner is available.

## Security notes

- Real API keys are never passed as plain `-e` env vars into the VM.
- The user's personal `~/.config/opencode` is mounted read-only from the host;
  each project shares the same personal config.
- `.envrc` files in the project directory may still be visible inside the VM
  because `--rm` does not affect bind mounts. This is a documented MVP
  limitation.

## Open questions / risks

1. **Config changes inside the VM:** Because `~/.config/opencode` is mounted
   read-only, any settings changed by opencode during a session are not
   persisted. This is acceptable for the MVP because runtime state lives in
   `.local` and `.cache`.
2. **`--secret` with open egress:** Verify that `--secret` works while default
   egress is allowed.
3. **macOS Docker path:** Docker Desktop on macOS may require different image
   build/load steps. Needs a real macOS test.
4. **Plugin cache sharing:** Per-project volumes isolate plugin caches. This is
   safer but may duplicate downloads; revisit if startup is slow.
5. **Provider fragment size:** `OPENCODE_CONFIG_CONTENT` will be a large env
   var (~15 KB). Verify it does not hit shell/env limits on target platforms.
