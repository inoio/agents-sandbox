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
   `$AGENTS_SANDBOX_IMAGE_PATH` entry missing from the reset `PATH`.

This is the ecosystem-standard `mergePaths` approach (devcontainers CLI reference behaviour; ported by crib, hermes-agent):
the login shell's own additions keep their precedence, and the image's entries are guaranteed present. No launcher or
session code changes are needed, because every affected context is a login shell and login shells source
`/etc/profile.d`.

## User Stories

1. As a developer, I want toolchains I declare via `ENV PATH` in my project Dockerfile to be usable in `agents-sandbox
   shell`, so that my interactive session has the same tools as the agent.
2. As a developer, I want `!` shell commands in opencode to see the same toolchain as the agent, so that I can run builds
   and tests interactively the same way the agent does.
3. As a developer, I want `opencode`/`node`/`npm` installed by agents-sandbox into `/opt/agents-sandbox/bin` to be
   resolvable in my interactive shell, so that I can drive the agent CLI myself.
4. As a developer, I want the agent's own command execution to keep working unchanged, so that this fix does not regress
   the context that already works.
5. As a developer on a distro whose base image resets `PATH` in `/etc/profile`, I want the merge to work without me
   editing the base image.
6. As a developer, I want my `~/.profile`/`~/.bashrc` customisations to remain effective, so that keeping the login shell
   does not break personal setup.
7. As a maintainer, I want the base image's `sbin` directories and other image entries to remain consistent across
   contexts, so that behaviour is not silently context-dependent.
8. As a developer whose base image already provides a tool (node, the agent, docker), I want the base copy to keep
   precedence, so that the merge does not shadow it.
9. As a developer, I do not want duplicate `PATH` entries after the merge, so that my shell environment stays clean.
10. As a developer, I do not want the merge to run twice / compound when shells nest, so that repeated login shells are
    idempotent.
11. As a developer, I want the merge to leave `/.msb/scripts` and any login-shell additions untouched, so that
    microsandbox and profile tooling keep working.
12. As a maintainer, I want a single, testable implementation of the merge, so that behaviour cannot drift between
    contexts.
13. As a maintainer, I want the image's authoritative `PATH` to be inspectable (`docker inspect` / exec env), so that I
    can debug resolution without shelling into an interactive session.
14. As a maintainer, I want the previous `/usr/local/bin` symlink stopgap removed, so that there is one mechanism for
    this problem rather than two.
15. As a developer on macOS (Apple Silicon) and Linux (KVM) alike, I want identical behaviour, since the merge is
    image-side and platform-independent.
16. As a developer, I want a clear failure/behaviour boundary on exotic custom bases that do not source
    `/etc/profile.d`, documented, so that I know the limitation.

## Implementation Decisions

**Authoritative PATH env var.** Name it `AGENTS_SANDBOX_IMAGE_PATH`. It is set in the final image stage immediately after
the final `ENV PATH="/home/dev/.local/bin:${PATH}"`, as `ENV AGENTS_SANDBOX_IMAGE_PATH="${PATH}"`, so its value is the
fully composed image `PATH`. Docker evaluates `${PATH}` to the current stage value at that point. The variable is
inherited by every exec (profiles reset only `PATH`) and is visible to `docker inspect`. It is also copied into the VM
env by the existing image-ENV plumbing, so no new launcher plumbing is required.

**Merge script.** A single POSIX-sh script, sourced by `/etc/profile.d`:
- If `AGENTS_SANDBOX_IMAGE_PATH` is unset/empty, it is a no-op.
- It appends each entry of `$AGENTS_SANDBOX_IMAGE_PATH` that is **not already present** in `$PATH`, in the image's
  order, and exports `PATH`. Existing entries keep their position; only missing image entries are appended.
- It is idempotent (running it again adds nothing) and never rebuilds `PATH`, so login-shell additions such as
  `/.msb/scripts` and profile-derived directories survive.
- It runs for all users (root and `dev`); unlike the devcontainers reference it does not skip `sbin` entries, because the
  acceptance target is that the same entries resolve in every context, including the agent context (which carries sbin).

**Delivery.** The script is embedded in the binary (`go:embed`) and shipped as a build-context file: the synthetic build
context produced for `docker build` gains the script, and the rendered final stage emits a `COPY` to
`/etc/profile.d/agents-sandbox-path.sh`. The classic builder (used by `buildImage`) cannot express a multi-line heredoc
in `RUN`, so a context `COPY` is used rather than writing the file with `printf`. The `COPY` layer is body-independent and
caches. Docker creates missing parent directories, so this also works on custom bases.

**Contexts.** `run` attach (`bash -l -c "<agent> attach …"`), interactive `shell` (`bash -l`), and `!` commands (which
inherit the attach client) all become login shells that source `/etc/profile.d`; they are fixed by the image alone. The
agent daemon is already non-login and already correct; it is not touched. No changes to `internal/sandbox/session`.

**Supersedes the symlink stopgap.** The `/usr/local/bin` symlink block (`toolBinLinksBlock`) and the associated
`image-composition.md` clause are removed. The merge restores the full composed `PATH`, not just the tool binaries, and
keeps precedence controlled by the image `PATH` order, so the symlinks are redundant. (If the two are ever both wanted,
that is a separate decision; this spec chooses one mechanism.)

**Custom-base requirement.** A custom base is expected to source `/etc/profile.d/*.sh` from its login profile (Debian,
Ubuntu, Fedora, Alpine and the supported custom bases do). This is added to the custom-base requirements list. Bases that
reset `PATH` but do not source `/etc/profile.d` remain unsupported for interactive PATH merging.

**Acceptance target — resolution equivalence, not byte identity.** After the change, every `AGENTS_SANDBOX_IMAGE_PATH`
entry is present in all three contexts. The login contexts may still contain additional profile-derived entries (e.g.
`/usr/local/games`, `~/.local/bin`) and may order the entries differently from the non-login agent context, because D
deliberately keeps profile sourcing. Byte-for-byte identical `PATH` strings across contexts are explicitly *not* a goal
(that would require discarding profile sourcing, i.e. option C).

## Testing Decisions

Test external behaviour, not the exact script text.

- **Primary seam — merge script under a real POSIX shell.** The merge is a pure function of
  (`PATH`, `AGENTS_SANDBOX_IMAGE_PATH`). Test it by executing the embedded script with `sh` and asserting the resulting
  `PATH`: missing image entries are appended in image order; present entries are not duplicated; `/.msb/scripts` and
  other pre-existing entries are preserved in place; empty/unset `AGENTS_SANDBOX_IMAGE_PATH` is a no-op; sourcing twice
  is idempotent. Prior art: tests that shell out to the system `sh`; this is one new, small seam and the highest one that
  captures the actual logic.
- **Render seam — wiring.** Extend `render_test.go` to assert the rendered final stage contains the
  `ENV AGENTS_SANDBOX_IMAGE_PATH="${PATH}"` **after** the final `ENV PATH`, and the `COPY` to
  `/etc/profile.d/agents-sandbox-path.sh`. Prior art: the existing `render_test.go` stage/block assertions.
- **Integration seam — end to end.** Extend `render_integration_test.go`: build a rendered image, then run
  `bash -lc 'printf %s "$PATH"'` in a container from it and assert the output contains `/opt/agents-sandbox/bin` (the
  entry the login reset currently drops) and, for a project that sets one, a project `ENV PATH` entry. This is the only
  seam that proves the profile.d wiring actually fires in a login shell.

The to-spec seam check is presented to the user before implementation; the above is the proposed set.

## Out of Scope

- Making `PATH` byte-identical across contexts (would require dropping profile sourcing — option C).
- Non-login shell behaviour, the agent daemon, and process environment handling outside `PATH`.
- Profile-sourced `PATH` on exotic custom bases that do not source `/etc/profile.d`.
- Other environment variables: only `PATH` is reset by `/etc/profile`, so only `PATH` needs restoration.
- microsandbox's `/.msb/scripts` injection mechanism.
- General PATH-management features beyond the runner image (e.g. `~/go/bin` is only in scope insofar as a Dockerfile
  declares it via `ENV`).

## Further Notes

- Prior art / references: devcontainers CLI `mergePaths` (the reference implementation); crib commit `74aa72e`; hermes-agent
  PRs #56642 and #64849 (which combine a probe-layer merge with a `/etc/profile.d` drop-in); VS Code devcontainers issue
  #5032 (established `/etc/profile` resetting `PATH` and the `profile.d` restore).
- Rejected alternatives: **C** (drop `-l`) — simplest and yields identical `PATH`, but discards profile sourcing that
  was added deliberately (commit `a3383e9`) and inverts the failure mode; **B** (symlink tool binaries into
  `/usr/local/bin`) — shipped as a stopgap, guarantees resolution but not the full composed `PATH` and pollutes
  `/usr/local/bin`; **A** (write the composed `PATH` into `/etc/profile` itself) — fragile, and the same profile.d
  mechanism with a separate env var is cleaner.
- The session comment at `internal/sandbox/session/run.go` that lists `~/.microsandbox/bin` as a reason for the login
  shell is stale (that path is a host install location in `internal/sandbox/doctor/install.go`); correct it while here.
