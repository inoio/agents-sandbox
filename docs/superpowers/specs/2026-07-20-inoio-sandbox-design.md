# inoio-sandbox: Design Spec

Transform the microsandbox (msb) proof-of-concept into an adoptable company
tool that runs opencode inside an ephemeral microVM, with per-repo state
persistence on the host.

## Decisions

| Area | Decision |
|---|---|
| Distribution | Standalone install (tool in PATH); per-project opt-in via `<repo>/.sandbox/` |
| Platform | Linux only (KVM). macOS later via libkrun. |
| Launcher language | Python 3 + click |
| opencode config | Host `~/.config/opencode` mounted RO; provider/models injected via `OPENCODE_CONFIG_CONTENT` |
| State model | Approach A: host-mounted per-repo state, no snapshots |
| State scope | Per-repo (git common-dir key; worktrees share, repos isolated) |
| Base image | Dev-defined via `.sandbox/Dockerfile`; default is minimal (opencode only) |
| Network | No rules (full egress; web search works) |

## Architecture

A host-side **launcher** orchestrates `msb`: it resolves the git repo (for
state keying), ensures the image, starts an **ephemeral per-working-dir VM**
with the project + per-repo state bind-mounted, runs `opencode` interactively
via `msb exec -t`, and stops the VM when the last session exits. State lives on
the host (per-repo), so VMs are disposable and images are freely rebuildable.
Devs invoke it through a shell **alias** (`opencode` → launcher) so VM lifecycle
is invisible.

## Components

1. **Bootstrap installer** (bash): one-shot install. Installs the launcher
   (pipx or venv), verifies `msb` is present (or instructs), offers to add the
   `opencode` alias to shell rc. For source builds: `pip install -e .` or
   equivalent.

2. **Launcher** (Python + click): repo-key resolution, state-dir setup, image
   ensure/build, VM start/exec/stop, session tracking.

3. **Default image** (Dockerfile): minimal — debian + opencode. Built/loaded
   on first run if no `.sandbox/Dockerfile` exists.

4. **Per-project `.sandbox/`** (optional, versioned in repo): `Dockerfile`
   (defines the VM image) + `env` (extra `KEY=VAL` lines). Absent ⇒ use default
   image.

5. **Curated provider catalog** (shipped with tool): JSON for
   `OPENCODE_CONFIG_CONTENT` defining the inoio LiteLLM provider + model list.
   Updates by upgrading the tool. Launcher injects it + `LITELLM_API_KEY`.

## Data flow (dev runs `opencode`)

1. Resolve repo root via `git rev-parse --git-common-dir` → repo-key (hash).
   No git ⇒ CWD key + warning.
2. State dir = `~/.local/share/inoio-sandbox/repos/<repo-key>/{state,cache}`
   (created if absent).
3. VM name = `inoio-<hash(cwd)>` (per working dir, so each worktree gets its
   own VM; **state is shared per-repo**).
4. `msb ls | grep <vm>`: if running → attach; if absent → ensure image (build
   `.sandbox/Dockerfile` if present else default) → `msb run -d` with mounts:
   - `<cwd>:/home/dev/workspace`
   - state dir → `/home/dev/.local/state/opencode`
   - cache dir → `/home/dev/.cache/opencode`
   - `~/.config/opencode:ro`
   - env: `OPENCODE_CONFIG_CONTENT`, `LITELLM_API_KEY` (from host env),
     `.sandbox/env`
5. Track session; `msb exec -t <vm> -u dev -- opencode`.
6. On exit: decrement session; if 0 → `msb stop && msb rm`. State persists on
   host.

### Session tracking (open spike)

Cannot verify `msb`'s session-tracking capabilities from inside the VM.
Implementation spike: check if `msb exec` sessions are tracked/listable
(`msb ls` output, `msb exec --help`). If yes, use native; if no, fall back to
host-side counter (flock + PID file per VM).

## File layout

```
~/.local/share/inoio-sandbox/
  lib/default/Dockerfile        # default minimal image
  lib/provider.json             # curated inoio provider+models
  repos/<repo-key>/{state,cache}
<repo>/.sandbox/{Dockerfile,env}   # optional, versioned
```

Launcher entry point location depends on install method (pipx → `~/.local/bin/`,
venv → venv bin). The `~/.local/share/inoio-sandbox/` path holds data and
shipped assets only, not the launcher binary.

## Scope

### In (MVP)

- Install script (bash)
- Launcher (Python + click): repo-key resolution, state-dir setup, image
  build, VM start/exec/stop, session tracking, provider injection
- README HOWTO (minimal, concise)
- AGENTS.md

### Out (deferred)

- Network rules / egress control
- Secrets-in-working-dir masking (`--rm` doesn't work on bind mounts)
- `msb --secret` injection for API keys
- macOS support
- Snapshot-based persistence
- Multi-key management UI

## Error handling

- `msb` not on PATH → error + install link.
- `/dev/kvm` missing → error + instructions (join `kvm` group / load module).
- `.sandbox/Dockerfile` build fails → surface `docker build` output, exit
  non-zero.
- VM start fails → surface `msb` error, exit.
- `opencode` exits / crashes → decrement session count; if last, stop+rm VM.
- Stale VM from host crash → on launch, detect stale lock; offer `--force` to
  clean.

## Testing

- Unit (pytest): repo-key resolution, state-dir paths, provider-config
  generation, env-merging.
- Integration (host-only, needs `/dev/kvm`): full VM start→exec→stop cycle.
- Smoke: `opencode` launches and exits cleanly in a fresh VM.

## Security notes

- The POC's `run-sandbox.sh` hardcodes `LITELLM_API_KEY=sk-...` (line 72) — a
  real leak. The launcher must read keys from host env only, never hardcode.
- `OPENCODE_CONFIG_CONTENT` injection: verify whether opencode deep-merges
  (personal provider entries would leak through) or replaces the `provider`
  section. If it merges, drop the user's provider entries from the mounted
  config before mount, or accept the inoio provider as default for MVP.
- Personal `~/.config/opencode` mounted RO: agent can read personal config
  (skills, memory, tui settings) but provider section is overridden.
