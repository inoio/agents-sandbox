# AGENTS3.md

## Project

`inoio-sandbox`: a standalone launcher that runs opencode inside an ephemeral
microsandbox (msb) microVM. The host launcher (Python + click) manages VM
lifecycle; the project is exposed as a git worktree and opencode state persists
per-project in msb named volumes. Devs invoke it through an `opencode` shell
alias so VM lifecycle is invisible.

## Code style (MVP)

- Write self-explanatory code.
- Keep abstractions minimal; do not introduce layers until they are clearly needed.
- Add inline comments only when the code is not self-explanatory.
- Disregard the existing German inline documentation and coding style from the POC spike.
- Prefer small, focused files with one clear responsibility.

## Constraints (MVP)

- **Platform:** Linux only (KVM). macOS deferred (libkrun path).
- **Secrets:** API keys forwarded via `msb --secret` so the real value never
  enters the VM. Never hardcode keys (the POC's `run-sandbox.sh` line 72 is a
  known leak to fix).
- **Network:** No egress rules; full egress so web search and package installs
  work.
- **State:** msb named volumes per project, with host-directory fallback if
  volumes prove unreliable.
- **Session:** One ephemeral VM per `opencode` invocation. No session tracking.
- **Workspace:** Git worktree of the host project, mounted read-write.
- `.envrc` secrets in the project directory are not hidden from the VM
  (documented MVP limitation).
- Launcher written in Python with `click`.

## Design decisions

- Standalone install (tool in PATH) via pipx/venv; bootstrap installer offers
  the `opencode` shell alias.
- Per-project opt-in via `<repo>/.sandbox/{Dockerfile,env}` (versioned in repo).
  Absent ⇒ use shipped default image.
- Image build is Dockerfile-first and hash-cached: `docker build | msb load`.
  Tag: `inoio-sandbox/runner:<sha256-short>`.
- opencode config: host `~/.config/opencode` mounted read-only; inoio LiteLLM
  provider/models injected via `OPENCODE_CONFIG_CONTENT`.
- `OPENCODE_CONFIG_CONTENT` deep-merges with the user's `opencode.jsonc`.

## Documentation

- Keep docs minimal and concise. A short README with a HOWTO is enough for this
  phase.

## Current limitations

- `.envrc` secrets in the project directory are not hidden from the VM.
- Network egress is unrestricted.
- macOS not supported yet.
- In-session changes to `~/.config/opencode` are not persisted (read-only mount).

## Design spec

`docs/superpowers/specs/2026-07-20-inoio-sandbox-merged-design.md`
