---
description: Contract for keeping the runner image's composed PATH (custom Dockerfile `ENV PATH`, `/opt/agents-sandbox/bin`, project toolchains like go/zig) effective in login-shell contexts by re-merging dropped entries from `AGENTS_SANDBOX_IMAGE_PATH` through a `/etc/profile.d` drop-in (devcontainers-style mergePaths). Supersedes the `/usr/local/bin` symlink stopgap.
read_if: Working on runner-image PATH handling, login-shell (`-l`) sessions, `run`/`shell` attach, custom Dockerfile `ENV PATH`, or the `/etc/profile.d` merge helper.
---

# Login-shell PATH preservation (mergePaths)

## Problem Statement

A user who selects their toolchain via `ENV PATH=...` in their project Dockerfile finds the toolchain missing in
interactive work. `agents-sandbox` attaches to the VM through a **login** shell (`/bin/bash -l`), and on Debian and most
distributions `/etc/profile` *replaces* `PATH` with a hard-coded default before any profile script runs. Every `PATH`
entry the image composed via `ENV` — the appended `/opt/agents-sandbox/bin`, a project Dockerfile's `/usr/local/go/bin`
or `/usr/local/zig`, the base image's sbin dirs — is discarded. Only entries a profile file happens to re-add (e.g. the
Debian skeleton `~/.profile` re-adding `$HOME/.local/bin`) survive.

The three execution contexts therefore disagree:

| Context | Observed PATH (trimmed) |
|---|---|
| `shell` (login) | `~/.local/bin:/.msb/scripts:/usr/local/bin:/usr/bin:/bin:/usr/local/games:/usr/games` |
| `!` command in opencode | same as `shell` (inherits the attach client) |
| agent-executed command | `/.msb/scripts:<full image ENV PATH including /opt/agents-sandbox/bin, /usr/local/go/bin, /usr/local/zig, sbin>` |

The agent context is correct because the agent daemon is started through a **non-login** shell that honours the image
`ENV`. The user-visible failure is that a toolchain declared in their Dockerfile works for the agent but not for them.

## Solution

Keep the login shell (so `/etc/profile`, `~/.profile` and `/etc/profile.d/*` are still sourced), but make the image's
composed `PATH` survive the reset:

1. The image records its composed `PATH` in a durable env var, `AGENTS_SANDBOX_IMAGE_PATH`, at build time — after the
   final `ENV PATH` directive, so it captures base + `/opt/agents-sandbox/bin` + project `ENV` + `~/.local/bin`.
2. The image installs a POSIX-sh helper at `/etc/profile.d/agents-sandbox-path.sh`. `/etc/profile` sources
   `/etc/profile.d/*.sh` **after** its `PATH` reset, so the helper runs at the right moment and re-appends any
   `$AGENTS_SANDBOX_IMAGE_PATH` entry missing from the reset `PATH`. The helper is embedded in the binary (`go:embed`,
   package-private `pathMergeScript()`) and shipped as a build-context file `agents-sandbox-path.sh`, copied in by the
   final stage (the classic builder cannot express a multi-line heredoc in `RUN`, so a context `COPY` is used). It is a
   no-op when `AGENTS_SANDBOX_IMAGE_PATH` is unset/empty; it appends missing entries in image order, matched
   colon-delimited, and is idempotent, so existing entries (including `/.msb/scripts` and profile-derived dirs) keep
   their position and nothing is duplicated. It runs for all users and does not skip sbin entries, because the
   acceptance target is that the same entries resolve in every context — including the agent context, which carries sbin.

This is the ecosystem-standard `mergePaths` approach (devcontainers CLI reference behaviour): the login shell's own
additions keep their precedence, and the image's entries are guaranteed present. No launcher or session code changes are
needed, because every affected context is a login shell and login shells source `/etc/profile.d`.

A custom base is expected to source `/etc/profile.d/*.sh` from its login profile; one that resets `PATH` without sourcing
`/etc/profile.d` remains unsupported for interactive PATH merging. Acceptance is **resolution equivalence, not byte
identity**: after the change every `AGENTS_SANDBOX_IMAGE_PATH` entry is present in all three contexts, while the login
contexts' extra profile-derived entries are tolerated. The earlier `/usr/local/bin` symlink stopgap
(`toolBinLinksBlock`) is removed: the merge restores the full composed `PATH`, not only the tool binaries, and keeps
precedence governed by the image `PATH` order.

## Out of Scope

- Making `PATH` byte-identical across contexts (would require discarding profile sourcing).
- Non-login shell behaviour, the agent daemon, and process environment handling outside `PATH`.
- Profile-sourced `PATH` on exotic custom bases that do not source `/etc/profile.d`.
- Other environment variables: only `PATH` is reset by `/etc/profile`, so only `PATH` needs restoration.
- microsandbox's `/.msb/scripts` injection mechanism.
- General PATH-management features beyond the runner image (e.g. `~/go/bin` is only in scope insofar as a Dockerfile
  declares it via `ENV`).
