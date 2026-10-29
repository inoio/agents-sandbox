---
description: Contract for the Hermes Agent profile (Nous `hermes-agent`, MIT): a CLI-only, daemon-less integration whose install runs the official installer at a pinned release tag into a baked Python/Node tool store outside the home volume, whose state lives in `HERMES_HOME`, and whose `config.yaml` snippets merge as YAML.
read_if: Adding or changing the `hermes` agent profile, its installer/tool-store stage, version pinning or the latest-release fetch, `HERMES_HOME` or tool-store placement, YAML config provisioning, the `OwnedToolPaths` handover, or the Hermes harness smoke entry; or diagnosing a Hermes launch that fails before its REPL or a first-boot dependency sync.
---

# Hermes Agent integration

Tracking: [inoio/agents-sandbox#249](https://github.com/inoio/agents-sandbox/issues/249) (harness-expansion tracker).

Hermes Agent (Nous Research, `hermes-agent`, MIT) is a Python coding agent. Unlike every other integrated agent it is
not a self-contained binary: it ships as a source checkout plus a dependency manager that owns its own pinned Python,
its tool binaries, and its per-checkout Python environments. That single fact drives most of the decisions below.

## Profile surface: CLI-only, no daemon

The `hermes` profile implements the core interfaces a daemon-less interactive agent needs and none of the daemon ones, so
the behaviour below falls out of the type assertions already in place:

- `--worktree` and `--serve-only` are rejected for `hermes`, exactly as for `pi`, `claude-code`, and `codex` (no daemon).
- Notify has no event stream for `hermes`; the `opencode`/`opencode2` event path is the only one.
- No guest-tool requirement block is rendered, matching the other no-daemon agents.

Hermes has server surfaces (`hermes serve`, `hermes gateway`, `hermes acp`, `hermes proxy`, `hermes chat --format
stream-json`). They are out of scope; see [Non-goals](#non-goals).

## Install and toolchain

The install lands the **code** and its **tool store** in the image and the **state** in the home volume, and a fresh
home volume must boot without network.

- **Code:** the immutable checkout lives at `/opt/agents-sandbox/hermes`, outside `/home/dev` — the runtime home volume
  would otherwise hide it.
- **Tool store:** `/opt/agents-sandbox/hermes-home` holds the managed Python/Node runtime, the `uv` cache, and the
  dependency environments under `installs/`. Hermes keys dependency state per home, so with `HERMES_HOME` on the volume
  it would reinstall its dependencies on first launch (needing network). Keeping the store in the image and linking it
  into the home is what makes a fresh volume start offline.
- **Installer:** the official POSIX installer, run non-interactively:
  `install.sh --commit "v$HERMES_VERSION" --dir /opt/agents-sandbox/hermes --hermes-home /opt/agents-sandbox/hermes-home
  --non-interactive --skip-browser --skip-computer-use`. No dedicated Python tool stage is needed: the agent stage
  installs `git` (the installer clones the release tag) and `libatomic1` (a native dependency the installer's package
  manager would otherwise fetch through sudo, which the tool base does not have), and Hermes provisions everything else
  itself. `libatomic1` is also required in the *runner* image (Hermes's provisioned Node.js links `libatomic.so.1` on
  glibc), which the agent stage cannot supply; `ImageSpec.RuntimeLibs` declares it so the runner's managed base installs
  it and a custom base is checked at build time.
- **Launcher shim:** the installer's published launcher is replaced by a small shim at `/opt/agents-sandbox/bin/hermes`
  that keeps user state in `$HERMES_HOME` (default `/home/dev/.hermes`) and symlinks `tools`, `cache`, and `installs`
  back to the baked store when absent, so a fresh home volume starts Hermes without reinstalling its dependencies.
- **Ownership:** both trees are declared through `ImageSpec.OwnedToolPaths` and chowned to the runner's `dev` user
  (`chown -R dev:dev`) after the tool tree is copied in and before the image switches to `USER dev`. The chown names the
  user and group rather than the `USER_UID`/`USER_GID` build args, because `devUserBlock`'s `groupadd -f` can leave the
  `dev` group on a different GID. Hermes writes its dependency ledger and bytecode markers there, and git rejects a
  checkout owned by another user.
- **Version pin:** the one resolved semver is passed as `--commit v<version>`, the release tag that `git checkout
  --detach` and the installer's ancestry check both accept.

## Version resolution and upgrade checks

- **Latest version:** the `tag_name` of `repos/NousResearch/hermes-agent/releases/latest`, stripped of its leading `v`,
  through the shared `latestGitHubReleaseTag` fetch. `hermes-agent` is also on PyPI, but the image installs from the git
  tag, so the release tag is authoritative.
- **Comparison:** semver, via the shared `newerVersionThan`.
- **Installed version:** `hermes --version`; `ParseVersion` extracts the base `X.Y.Z` through the shared semver-token
  extraction.
- **No self-update:** the sandbox pins the baked version and resolves upgrades itself; `hermes update` is never invoked.

## Config, provisioning, and credentials

Hermes's config home is **not** XDG: it is `HERMES_HOME` (default `/home/dev/.hermes`), containing `config.yaml`
(settings, schema-versioned), `.env` (secrets), `auth.json` (OAuth credentials), `SOUL.md`, `memories/`, `skills/`,
`sessions/`, `state.db`, and `logs/`.

- **Launcher config dir and mirror:** `hermes` under the agents-sandbox config base — user
  `~/.config/agents-sandbox/hermes/`, project `.agents-sandbox/hermes/`. The mirror and the merged-config machinery are
  both driven by the `ConfigMerger` capability, and the mirror root is `filepath.Dir(VMConfigPath(vmHome))`. The
  contract:
  - `VMConfigPath(home)` = `~/.hermes/config.yaml` — the file whose directory is the mirror root.
  - `SnippetPattern()` = `config*.yaml`: snippets merge into `~/.hermes/config.yaml`, written (and compared) as YAML,
    because the file is both Hermes's input and something users edit.
  - `ConfigFileNames()` = `["config.yaml"]`.
  - `ProvisionRules()` copies only `~/.hermes/config.yaml` from the host; the rest of the home is runtime state
    (`state.db`, `sessions/`, `memories/`, `skills/`), which belongs to the sandbox.
- **Credentials:** supplied as env secrets (Hermes's provider variables, e.g. `OPENROUTER_API_KEY`,
  `ANTHROPIC_API_KEY`, `OPENAI_API_KEY`). Hermes reads the process environment below `config.yaml` in precedence, so
  env secrets work without writing `.env`; neither `.env` nor `auth.json` is ever copied.

## Launch

- **Attach command:** `hermes <quoted args>`. Forwarded arguments are shell-quoted like every other profile.
- **Interface:** the classic CLI renders into the terminal's normal buffer.

## Harness smoke entry

`test/harness` carries a `hermes` entry (the suite asserts its key set equals `agent.Names()`):

- `KeepsNormalBuffer: true` — Hermes' classic CLI renders into the terminal's normal buffer, so the smoke test watches
  for output rather than the alternate-screen marker.
- `Network: public` — Hermes reaches its provider before the prompt; an air-gapped run would not reach the REPL.
- `QuitKey: Ctrl-D`.
- `ReadyTimeout: heavyweightSettledReadyTimeout` — the runner image carries a whole Python/Node tool store, so loading
  it into microsandbox outlasts the shared 25s settled budget (`harness-smoke-tests.md`).
- CI coverage: `TestRenderDockerfileBuildsEveryAgent` (integration) builds the runner image and runs `hermes --version`
  on the managed and a custom non-Debian base. The `integration` job is opt-in — the `run-integration` PR label, plus every
  code-affecting `main` push and release tag — not one of the required PR checks (`ci-job-gating.md`), so hermes's install
  and its provisioned Python/Node runtime are exercised there and end-to-end by this host-only `msb` smoke.

## Scope decisions

- **Install shape.** Considered: (a) `uv tool install hermes-agent==X`, (b) the official installer, (c) reimplementing
  Hermes's own Dockerfile recipe. (a) was rejected — it is not a supported path, and the dependency manager owns
  dependency selection; (c) is the most reproducible but the most code. (b) is chosen: it is the supported install, and
  a valid prepared tool store means no first-launch network sync.
- **Daemon and worktree** are out of scope, so `--serve-only`/`--worktree` are rejected as for the other no-daemon
  agents.
- **Interface** is the classic CLI only; the TUI and the Node requirement that comes with it are deferred.

## Non-goals

- No daemon surface: `serve`, `gateway`, `acp`, `proxy`, or the `chat --format stream-json` stream.
- No browser (`chromium`) or computer-use (`cua-driver`) staging.
- No TUI launch and no tool-stage node for Hermes.
- No notify event stream and no worktree support.

## Open risks

- **Offline boot** is the central risk: if the dependency-state root sits inside the home volume, a fresh volume is
  empty and the launcher attempts a network sync. The install places the prepared runtime in the baked store and links
  it into the home. Verification is **manual and documented**, not an automated gate: a `--network none` boot must reach
  the REPL with a clean `hermes doctor`.
- **Runtime availability of the installer's apt packages — resolved.** The agent stage installs `git` and `libatomic1`,
  but the runner copies only the tool tree from that stage, so both are absent from the runner image. That matters for
  `libatomic.so.1`: Hermes's own docs record that the managed Node.js links it on glibc, and minimal Debian/Ubuntu/RHEL
  images do not ship it. A `hermes --version` probe does not exercise Node, so it could not have caught this. The runner
  therefore guarantees the library instead of probing for it: the embedded managed base installs `libatomic1`, and
  `ImageSpec.RuntimeLibs` makes `runtimeLibsBlock` verify `libatomic.so.1` in the final stage, so a custom base without it
  fails the image build naming the library (`TestRenderDockerfileMissingRuntimeLibFailsBuild`) rather than failing when
  Hermes launches Node. A custom base must provide it (`libatomic1` on Debian/Ubuntu, `libatomic` on Fedora/RHEL) — the
  same custom-base contract as pi's node pin.
- **Installed-version suffix:** pinning a ref that is not exactly a release tag yields `X.Y.Z+<n>.g<sha>`. The parser
  keeps that suffix (the shared semver-token extraction does not reduce it); comparison is unaffected because semver
  ignores build metadata.
