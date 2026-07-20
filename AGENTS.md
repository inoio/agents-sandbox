# AGENTS.md

## Project

`opencode-msb`: a minimal launcher that runs opencode inside a microsandbox VM, binding the current project directory as a git worktree and persisting opencode state in msb volumes.

## Code style (MVP)

- Write self-explanatory code.
- Keep abstractions minimal; do not introduce layers until they are clearly needed.
- Add inline comments only when the code is not self-explanatory.
- Disregard the existing German inline documentation and coding style from the POC spike.
- Prefer small, focused files with one clear responsibility.

## Constraints

- Target platforms: Linux (KVM) and macOS (Apple Silicon).
- No network rules for the MVP; default egress is allowed so web search works.
- API keys are personal. The launcher forwards them via `msb --secret` so the real value never enters the VM.
- Do not commit secrets or `.envrc` content into the working directory.
- Use Python with `click` for the launcher.

## Design decisions

- Dockerfile-first runner image.
- One ephemeral microsandbox VM per `opencode` invocation.
- The project is exposed as a git worktree, so concurrent isolated sessions are possible.
- opencode state is stored in msb named volumes per project; if msb volumes are not viable, fall back to project-local host directories.
- Shared inoio LiteLLM provider/model definitions are shipped as a repo JSON fragment and passed into opencode via `OPENCODE_CONFIG_CONTENT`.

## Development

- Use `uv` for dependency management and virtual environment operations (e.g. `uv add <package>`, `uv run`).
- The project venv is at `.venv`; run tests with `.venv/bin/python -m pytest tests/unit`.

## Documentation

- Keep docs minimal and concise. A short README with a HOWTO is enough for this phase.

## Current limitations

- `.envrc` secrets in the project directory are not hidden from the VM yet.
- Network egress is unrestricted.
