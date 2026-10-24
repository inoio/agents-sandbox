# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project adheres to
[Semantic Versioning](https://semver.org/spec/v2.0.0.html). Version tags use a `v` prefix (e.g. `v0.1.0`); the `version`
command reports the bare version (e.g. `0.1.0`).

## [Unreleased]

### Added

- Runtime: on the first interactive `run`/`shell`, agents-sandbox offers once to copy legacy
  `~/.microsandbox/volumes/agents-sandbox-home-*` volumes into its private runtime home. The choice is mandatory. You can copy,
  start fresh/dismiss, or quit. Choice is tracked in a `migrations.runtime_home` entry in the launcher's root `state.yaml`
  (`~/.local/state/agents-sandbox/state.yaml`): a `copying` status with transient `copied`/`kept`/`excluded` lists,
  `completed`, or `dismissed`. While legacy volumes
  remain, non-interactive runs (including `--yes`) never copy and stop with a message to run one interactive session, so
  a project's recorded home volume is never overwritten before the copy happens. When a private volume already exists
  under a legacy name, the offer says so and lets you copy only the missing volumes (leaving the existing ones
  untouched) or dismiss, pointing at `volume remove` for manual cleanup.
- After the one-time migration, an interactive `run`/`shell` shows a one-time notice that agents-sandbox runs its own
  microsandbox runtime and a system-wide microsandbox is no longer required. It names which parts of `~/.microsandbox`
  are ours and safe to remove (with the commands) versus your own data, warns about any legacy volume that was not
  verifiably copied, is acknowledged with a keypress, and never deletes anything. A non-interactive session prints only
  a one-line pointer, the full notice still appears the next time you are interactive (#207).
- CLI: `volume inspect <name>`, `image inspect <ref>`, and `sandbox inspect <name>` show the full detail of a home
  volume, a cached runner image, or a project sandbox — identity, storage and image configuration, mounts, and
  resources. Each supports `--format json` and the global `-q`/`--quiet` (the identifier only). #165.
- CLI: `df` reports storage usage of the private runtime home by category — the categories microsandbox reports plus the
  host runtime install — mirroring `msb df`. `--verbose` adds per-object rows and host paths, `--format json` prints a
  structured report (unknown counters are `null`; items are always present), and the global `-q` prints category names
  only. The `storage` group aliases the commands as `storage df` and `storage prune`. #165.
- CLI: `image remove <ref>...` (`rm`) deletes cached runner images by reference and `sandbox remove <name>` (`rm`)
  removes a project sandbox. `image remove` accepts only `agents-sandbox/runner-*` references and refuses an in-use
  image unless `-f`/`--force` (naming the referencing projects); `sandbox remove` refuses a running sandbox unless
  `-f`/`--force` (which stops it first) and leaves the project's persisted state intact, so the recorded home volume
  survives. Both prompt unless the global `--yes` is set. #165.
- `volume remove <name>...` (alias `rm`) removes home volumes by name. It prompts for confirmation (pass `--yes` to skip
  it non-interactively) and refuses when a project's state still references the volume, unless `-f`/`--force` is given.
- CLI: `sandbox list`, `volume list`, and `image list` accept `--format json` (structured output on stdout) and the
  global `-q`/`--quiet` (identifiers only — sandbox and volume names, or image references) in addition to the default
  human table; the two are mutually exclusive.
- CLI: `prune` and the `image`/`volume`/`sandbox` `prune` subcommands accept `--format json` (a structured report on
  stdout) and `--older-than` (the canonical age flag, with human-duration syntax such as `7d` or `2w`).
- CLI: `config paths` prints the launcher-config layers in precedence order (each file layer's resolved path, flagged
  `(missing)` when absent) and the resolved repo slug, making the per-slug user config dir discoverable. The layer block
  goes to stdout; the guidance naming the committed project config and the generic user config goes to stderr and
  survives `--quiet`. #144.
- Runner image: the in-VM tools the selected agent's runtime needs are declared per agent and checked at build time. A
  daemon agent (`opencode`, `opencode2`) needs `curl`, `nohup`, and `pkill`; `pi` and `claude-code` need none. A custom
  base missing one now fails the build naming the tool, instead of the failure surfacing later as a daemon that never
  becomes healthy. The list lives in Go (`agent.GuestToolRequirer`), so it cannot drift from the launcher's in-guest
  commands; removing the `curl` requirement altogether is tracked as
  [#263](https://github.com/inoio/agents-sandbox/issues/263).
- Runner image: per-tool multistage composition, so the tool layers (CLI tools, Node, agent, and the optional Docker
  engine) cache independently of the user's Dockerfile body and of each other, and are reused across projects on one
  machine.
- Runner image: install microsandbox's application certificate trust helper as `/usr/local/bin/msb-trust`, for
  applications whose TLS trust is separate from the guest's system store. `msb-trust java` imports the interception CA
  into a JDK's `cacerts`, `msb-trust chrome` into Chrome/Chromium's NSS store, and `msb-trust all` configures every
  supported application whose tools are installed. `curl`, `git`, Python, Go, and Node already trust the CA through the
  system store. The script is vendored verbatim from microsandbox v0.7.8 (`data/msb-trust.LICENSE` records the source,
  the hash, and the re-sync command), its bytes participate in the runner image's content identity, and its Apache-2.0
  license ships at `/usr/share/licenses/msb-trust/LICENSE` (#152).
- Custom project Dockerfiles can place a `# agents-sandbox:docker` marker line in the final stage to control where the
  Docker install block is injected, for example to run `buildx`/`docker compose` setup after the engine is installed.
  Without the marker the block is appended after the Dockerfile body. The legacy `# agents-sandbox:dind` marker is
  still recognized.
- Runner image: the `agents-sandbox/runner-base-docker` base reference is recognized, alongside the deprecated
  `agents-sandbox/runner-base-dind`.
- `run` and `shell` now accept `--agent-version` to pin the agent version baked into the runner image, matching `build`.
- Verbose output now explains why the runner image is rebuilt on every rebuild path: the Docker build names the changed
  identity inputs (project Dockerfile, agent version, Docker-in-Docker) or `--rebuild`/a missing image, the microsandbox
  load reports whether the cached image is absent or stale, and a project sandbox recreate or daemon restart lists the
  config changes that triggered it.
- Verbose output for the agent serve daemon now explains why it is not healthy: each `Polling for daemon health` line
  carries the probe's exit code, parse error, or body, and a health timeout appends the runner's daemon log tail, so a
  daemon that never comes up can be diagnosed without leaving the session.
- Sessions started inside a git worktree now bind the enclosing checkout at `/workspace` and start the agent in that
  worktree, so bare checkouts whose worktrees live inside the checkout work host-and-VM-consistently. A worktree outside
  its checkout keeps the current behavior and warns.
- Notifications: notify now supports the `opencode2` agent (its v2 session event stream) in addition to `opencode`, and
  is skipped for subagent sessions — only top-level sessions notify.
- Self-upgrade: the new `upgrade.channel` setting (`stable` default, `canary`) lets an install opt into canary
  prereleases. The canary channel follows the newest release eligible for it — the newest canary prerelease, and then
  the real release once it ships — so testers roll forward automatically. `agents-sandbox upgrade` accepts a one-shot
  `--channel` override. Homebrew installs are unaffected. Set the channel back to `stable` to leave the canary line at
  the next release.
- Networking: the `network.profile` key (and `--network` / `AGENTS_SANDBOX_NETWORK_PROFILE`) now accepts a list, so the
  composable `public`, `private`, and `host` profiles can be combined, e.g. `profile: [public, host]` to let a VM reach
  both the internet and a service on the host such as a local MCP bridge. `none` remains a terminal, allowlist-only
  policy and cannot be combined with another profile. Existing single-profile configuration is unchanged (#289).

### Changed

- The default agent is now `opencode2`. A bare `agents-sandbox` and every command whose `--agent` flag is left unset
  (`run`/`shell`, `build`, `build dockerfile`, `stop`, `kill`, `config agent`, and `volume` migrate/reset/edit) select
  `opencode2` instead of `opencode`; pass `--agent opencode` to keep using the v1 agent.
- Network: the default egress profile is now `public`, so a fresh sandbox reaches the internet (agent APIs, package
  registries) with no configuration — a first run no longer has an unreachable agent (#279). The allowlist-only `none`
  profile is unchanged and available explicitly, and any sandbox with no configured profile is recreated with the new
  default on its next run, as any network-policy change already triggers a recreate.
- **Breaking** Project identity is split by scope: a checkout-scoped slug (the resolved git common dir) keys the state
  directory, sandbox name, home volume, locks, and runner image tag, while a repo-scoped slug (the origin URL) keys the
  per-slug user config dir and the `org.agents-sandbox.project` grouping label. Two clones of one remote now get
  separate sandboxes and home volumes, sharing a runner image only best-effort through the build cache; a checkout and
  the worktrees inside it keep sharing everything; a worktree outside its checkout gets its own identity instead of
  being refused. The first post-upgrade run migrates automatically with a notice: it destroys the legacy origin-keyed
  sandbox of every agent whose state the move carries, then recursively merges the state directory onto the checkout
  slug without overwriting an existing file. A bare root also adopts its pre-split path-derived identity, so a
  bare-checkout user's saved home volume and per-slug config are not stranded, and a surviving legacy sandbox is
  adopted even when its state file was lost — its mounted home volume is recorded in the checkout state before the
  sandbox is destroyed, so the previous credentials and files are reused instead of a fresh volume being minted. A
  shared legacy volume is claimed under the legacy identity in the launcher-global root state, so a second clone cannot
  recover it. A `--dry-run` leaves the legacy layout in place. #144.
- Runtime: agents-sandbox now provisions and uses its own private microsandbox runtime home at
  `$XDG_DATA_HOME/agents-sandbox/msb` (default `~/.local/share/agents-sandbox/msb`), selected via `MSB_HOME` at startup
  and fully decoupled from any user-installed microsandbox. `~/.microsandbox` is never read or written by normal
  operation, no user-facing `msb` is installed or advertised, and the runtime version-mismatch recovery menu is retired.
  When the private home holds a runtime other than the SDK-pinned one (for example one left by a previous release),
  agents-sandbox silently reinstalls the pinned runtime instead of prompting. A project whose recorded home volume is
  absent from the private home starts a fresh one automatically, so existing users keep working without migrating.
- Runtime: runtime artifacts are now named without the vendor prefix. A project sandbox is `<checkout-slug>-<agent>`, a
  transient task sandbox `<checkout-slug>-<agent>-<nanos>` (labelled `org.agents-sandbox.task`), a home volume
  `<checkout-slug>-<agent>-<timestamp>`, and the microsandbox runner image reference `<checkout-slug>:<agent>-latest`;
  the Docker image tag keeps its `agents-sandbox/runner-` prefix so it stays distinguishable in the system Docker
  daemon. This keeps the private runtime home readable instead of repeating `agents-sandbox-` on every object. The
  change is backward compatible: artifacts created by an earlier version are still found and reused, so an upgrade never
  rebuilds a sandbox, reimports an image, or recreates a home volume, and only newly created artifacts use the
  prefix-free names. New sandboxes also carry their identity as labels (`org.agents-sandbox.type`, `.slug`, `.agent`),
  which is authoritative where the name shape is ambiguous; the name is the fallback for pre-label sandboxes. A
  leftover stopped task sandbox no longer counts as its project being stale, so it can no longer make the next
  auto-prune reclaim the live project's runner image or an unrecorded saved home volume.
- Runner image: `opencode`, `opencode2`, and `claude-code` images are now node-free. The `agents-sandbox-node` stage is
  emitted only for agents that need node (today only `pi`, an npm-installed node bundle), and opencode2 and
  `claude-code` install from their upstream native installers instead of npm. This cuts image size and cold-build time;
  version pinning, upgrade resolution, and `dockerfile-id` identity are unchanged (#133).
- Runner image: the node/agent/docker install stages now build on a tool-owned `agents-sandbox-toolbase`
  (`debian:trixie-slim` plus `curl`/`ca-certificates`/`tar`) instead of the user's base. A custom base no longer needs
  `curl` or `tar` inside the image, the tool layers cache across bases, and the Docker runtime-prerequisite check no
  longer requires `curl`/`tar`. Fixes #143.
- Runner image: a rebuild triggered by a stale image (changed Dockerfile, agent version, or base) reuses Docker's layer
  cache; only an explicit `--rebuild` bypasses it. An accepted agent upgrade is a version change, so it rebuilds the
  install layer while keeping earlier layers cached. `build dockerfile` gained `--agent-version` to render a pinned
  agent version in the preview.
- Runner image: each tool stage pins its version as an `ARG` default in the rendered Dockerfile (Node, agent, Docker),
  so a version bump changes the build content and reliably invalidates that stage's install layer. Previously only the
  agent version build arg carried the version, which could leave the agent layer cached — and the image on the old
  release — on builders that do not fold the build arg into the cache key.
- Runner image: `vfs` is now forced by adding `--storage-driver=vfs` to the dockerd start command inside the guest,
  instead of writing `/etc/docker/daemon.json` at image build time, so your edits to `daemon.json` are preserved.
- Runner image: the `dind` capability is renamed to `docker` — canonical `--docker` (on `build`, `run`, `shell`, and
  `build dockerfile`), the `docker` config key, and `AGENTS_SANDBOX_DOCKER`. The old spellings remain as deprecated
  aliases with lower precedence and a one-time warning, and the legacy `# agents-sandbox:dind` marker and a
  `FROM .../runner-base-dind` base still imply docker.
- Runner image: the dev user's login shell is now the first of `bash`, `zsh`, or `sh` found in the base image (falling
  back to `/bin/sh`), widening custom-base compatibility.
- CLI: `prune` and its `image`/`volume`/`sandbox` subcommands now ask for confirmation before removing anything,
  defaulting to no. Pass the global `--yes` to skip the prompt — required in non-interactive contexts, which otherwise
  fail fast rather than block; `--dry-run` only previews and never prompts.
- `--dry-run` is now a read-only preview. It reports the agent version (or that the version resolves at build time),
  whether the runner image would build (with the same reasons a real build logs) or be reused, and whether the home
  volume would be created or reused — without building the runner image, creating a home volume, writing project state,
  starting a sandbox, attaching, or making any network request. The `volume migrate`/`reset`/`edit` `--dry-run`
  operations likewise no longer ensure the image.
- `--rebuild` on `run`/`shell` now only forces a clean rebuild; it no longer suppresses the agent upgrade check. A
  pinned `--agent-version` is ignored for a user-provided agent, matching the documented contract.
- Bare checkouts (a bare `.git` with worktrees inside it) now read project config — `.agents-sandbox/Dockerfile`,
  `config.yaml`, `env`, `env.secret`, home sources, and agent config — from the bare root next to `.git`, as the
  [bare-checkout recipe](https://inoio.github.io/agents-sandbox/recipes/bare-worktrees.html) intends. A
  `.agents-sandbox/` inside the launched worktree is ignored, with a warning naming it. Non-bare checkouts (including
  their subdirectories) and non-repo folders are unchanged.
- `opencode2` now reads its agent config from `~/.config/agents-sandbox/opencode2/` (and the project equivalent
  `.agents-sandbox/opencode2/`) instead of the shared `opencode` dir. While no `opencode2` dir exists it falls back to
  the old `opencode` dir — which the `opencode` agent also uses — and warns on every run while it does; creating the
  `opencode2` dir switches to it (copy, not move, if you also use `opencode`). The guest config path and host drop-in
  are unchanged (`~/.config/opencode/`).
- Upgrade prompt: the agent-upgrade offer on `run`/`shell` now names both the available release and the version
  currently baked into this project's image (for example `Upgrade opencode 1.0.0 → 2.0.0`), and reads the "current"
  version from this project's own state so it can never name another project's version. Upgrade checks are scoped by
  application: the check cadence and cached latest version live in the launcher's root `state.yaml`, while the current
  version, sources, and offered set live in each project's per-agent state. The legacy global `updater.yaml` is retired,
  folding its check cadence into the root state (#225). When no version is recorded yet, the baseline is recovered once
  from the existing image's `agent-version` label so the prompt still fires.
- Launcher environment variables now use the `AGENTS_SANDBOX_` prefix instead of the historical `OPENCODE_SANDBOX_`. The
  old prefix still works as a deprecated alias, resolving with lower precedence and printing a one-time warning, so
  existing setups keep working; migrate your configuration to `AGENTS_SANDBOX_*` (#120).
- Maintenance: bump Go module dependencies — go-git v5.19.3, go-billy v5.9.2, moby api v1.56.1, moby client v0.6.1,
  and the microsandbox Go SDK v0.7.7, plus transitive updates (ProtonMail go-crypto v1.5.2, OpenTelemetry v1.47.0,
  golang.org/x/crypto v0.57.0, golang.org/x/net v0.59.0, golang.org/x/text v0.42.0).
- Internal: launcher-global state now lives in one document, the root `state.yaml`, under an `updates:` namespace —
  `updates.launcher` (the retired `upgrade.json`, whose `last_check` is now `last_checked`) and `updates.agents` (agent
  update checks). The old top-level `agents:` key and `upgrade.json` fold in silently on first run (#141).
- Internal: every per-project `state.yaml` read-modify-write now serializes on a single host-only lock per
  `(slug, agent)`, so a volume operation and a concurrent upgrade or provenance write can no longer clobber each
  other's fields. State writes are one locked transaction per document (read → mutate → write), lock-free writers are
  no longer exported, and two failures that were previously warned-and-continued now surface as errors — a reconfig that
  cannot persist its new home volume, and `volume migrate`/`volume reset` whose final state write fails (which
  previously reported success).
- Internal: runtime setup now passes the private home explicitly via the SDK's `RuntimeConfig.Home`
  (`configpaths.MSBHome()`), so it no longer depends on `MSB_HOME` having been applied first; `MSB_HOME` still governs
  the FFI cache and non-setup SDK operations (#160).
- Doctor: a failing prerequisite check now prints the command that fixes it — start the Docker daemon, add your user to
  the `kvm` group, or download the right architecture — followed by a link to the matching troubleshooting section.
  The checks still run automatically before `run` and `shell`.

### Deprecated

- The `dind` capability is renamed to `docker`: `--dind` → `--docker`, the `dind` config key → `docker`, and
  `AGENTS_SANDBOX_DIND` → `AGENTS_SANDBOX_DOCKER`; the `agents-sandbox/runner-base-dind` base image is renamed to
  `agents-sandbox/runner-base-docker`, and the `# agents-sandbox:dind` marker to `# agents-sandbox:docker`. All old
  spellings still work with lower precedence and a one-time warning, and warn whenever the project Dockerfile is used to
  ensure the runner image. Removable at their 0.8.0 eligibility.
- The `OPENCODE_SANDBOX_*` environment-variable prefix is deprecated in favour of `AGENTS_SANDBOX_*`. The old prefix
  still works with lower precedence and a one-time warning (#120). Removable at its 0.8.0 eligibility.
- `sandbox list --names` is deprecated in favour of the global `-q`/`--quiet` (identifiers only). It is hidden and
  still honored, but warns when used. Removable at its 0.8.0 eligibility.
- `prune --age` (and its `-a` short form) is deprecated in favour of `--older-than`. It is hidden and still honored,
  but warns when used; setting both flags together is an error. Removable at its 0.8.0 eligibility.
- `--opencode-version` is deprecated in favour of `--agent-version`. It is hidden and still honored, but warns when
  used. Removable at its 0.7.0 eligibility.
- `volume migrate`/`reset`/`edit` `--rebuild` is restored as a hidden deprecated no-op that warns — the volume
  operations ensure the runner image but never force a clean rebuild. Run `agents-sandbox build` first to force one.
  Removable at its 0.8.0 eligibility.

### Docs

- A new [Upgrading to 0.6](https://inoio.github.io/agents-sandbox/install/upgrading.html) page documents the private
  runtime home, the legacy home-volume migration, the uninstall payoff for a system-wide microsandbox, and the 0.6
  renames (#208).
- A first-run tutorial walks from install to a first `run`, and a new `STYLEGUIDE.md` defines the
  documentation voice, structure, terminology, and formatting rules.
- The site is regrouped into six navigation sections and every page is rewritten to `STYLEGUIDE.md`, with the prune and
  home-volume explanations de-duplicated and the retired `docs/install.html` URL redirecting to the new Get started
  page.
- The [Deprecations](https://inoio.github.io/agents-sandbox/deprecations.html) page now states the full policy — the
  replacement always wins, renames are shimmed while only default-behaviour changes make a clean break, and a
  user-facing form is never removed without a deprecation cycle — and lists every deprecated flag, config key,
  environment variable, and project-Dockerfile spelling with its replacement and removal target (#205).
- SEO: a canonical URL, a published `sitemap.xml`, and a descriptive home-page title, site description, and
  link-preview title (`og:title`).
- The Get started flow no longer opens with a separate prerequisites page. The system requirements (platforms and
  Docker) and the `doctor` verification now live in Installation, whose last section points either to switching an
  existing agent or to a first sandbox; the tutorial starts directly at running an agent.

### Fixed

- Worktrees: `--worktree` for `opencode2` ran its worktree commands through `jq` inside the guest, so a custom runner
  base without `jq` failed with `jq: command not found`. The launcher now parses the worktree API responses in Go, so
  the worktree path needs no `jq` (only `curl`). A malformed worktree list now fails the run for both `opencode` and
  `opencode2` instead of reading as "no worktrees" and silently creating a second worktree (#257).
- opencode2: the serve daemon no longer fails to become healthy. v2's `serve` enforces HTTP basic auth on every
  `/api/*` route and auto-generates a random password when `OPENCODE_SERVER_PASSWORD` is unset, so the launcher's
  unauthenticated health probe always returned 401 and `run`/`shell` polled until timeout. The runner image now bakes a
  fixed `OPENCODE_SERVER_PASSWORD` (v2 hardcodes the basic-auth username to `opencode`), and the health probe, worktree
  API calls, and in-VM attach client all use it. The probe now polls `/api/info` — v2's actual health endpoint — instead
  of `/api/health`, which matches no route, and the attach client passes the working directory as v2's positional
  argument instead of the removed `--dir` flag.
- opencode2: `--worktree` created worktrees through the v1 routes, which v2 does not serve, so a worktree session always
  failed with 404. It now resolves the project via `/api/location`, lists with `GET /api/worktree?projectID=`, and
  creates with `POST /api/worktree`. For both agents a `:<base>` ref is validated before a worktree is created, so an
  unresolvable ref fails without leaving an orphan worktree, the v2 requests are bounded in time and keep the daemon's
  HTTP error in the failure message, and for opencode2 the detached-HEAD hint is printed only when a worktree is
  created, not on reuse (#235).
- opencode2: version resolution now queries the opencode update API's `latest` channel
  (`https://opencode.ai/update/api/latest/cli/npm`), the same source the v2 installer uses, instead of the npm `beta`
  dist-tag of the deprecated `@opencode-ai/cli` package. That stale tag resolved to a version whose tarball ships
  `bin/opencode2`, which the v2 installer (expecting `bin/opencode`) could not install, failing the runner-image build.
- opencode2: the `--serve-only` banner now names the credential an external client needs (`opencode:opencode`). The
  previous opt-in `OPENCODE_SERVER_PASSWORD`/`OPENCODE_SERVER_USERNAME` guidance was valid only for opencode v1 and left
  the client unable to connect.
- Notifications: a second permission or form request inside one turn now notifies, even when both are asked back to
  back in a single step. After the first request the session stayed in its awaiting-input state, so a later request in
  the same turn was silent; each request is now identified by its event id, so distinct requests alert while a replayed
  one does not. v1's seed request is time-limited, so a stalled `/session` can no longer stop the notify watcher for the
  rest of the run, and the seed no longer needs `jq` in the runner image, so child-session filtering also works on
  custom bases.
- Home volume: a lost `state.yaml` (deleted by hand, a partial or corrupt write, or a crash before the state write) no
  longer mints a duplicate home volume or orphans the original. When the project sandbox still mounts a home volume that
  exists, that volume is recovered and state is rewritten to point at it, seeded with the desired fingerprints (or the
  prior state's when it was still readable); a fresh volume is created only when no surviving volume can be confirmed
  (#219).
- Reconfiguration: a changed `--tmp-size` or `--workspace-quota`, and a home-volume switch, now actually recreate an
  existing project sandbox. Those three detectors compared against a sandbox config the microsandbox Go SDK always
  leaves empty when reading a sandbox back (its decode drops the persisted mounts), so they never fired and the change
  was silently ignored until an unrelated recreate. The live sandbox's mounts are now read from its persisted config.
  The same empty config also silently disabled two other readers: home-volume recovery after a lost `state.yaml` never
  found the surviving volume, and `sandbox inspect` reported `mounts (0)` for a fully mounted sandbox (#223).
- Reconfiguration: a freshly created home volume's state is now seeded with the desired env/secret/network/mount
  fingerprints in the same write that records the volume, so a persisted state is never fingerprint-less while its
  sandbox exists. This removes the spurious "network policy changed" rebuild or restart prompt a concurrent session
  could trigger by reading the empty state between creation and fingerprint persistence, and the same spurious detection
  after a crash between the two (#183).
- `/workspace` bind mount: guest `rwx` permission bits now mirror onto the real host files
  (`host-permissions=mirror`), so an in-guest operation that rewrites a tracked executable (for example `git checkout`,
  `reset`, or `stash`) no longer silently drops the executable bit on the host while the guest reports a clean tree.
  Ownership and setuid/setgid are never propagated, and configured mounts keep microsandbox's `private` default. The
  policy applies when the project sandbox is created, so an existing sandbox picks it up on its next recreate.
- Agent upgrade: a project that already has a runner image but no recorded agent version (for example the first run
  after the `updater.yaml` migration) no longer silently rebuilds to the latest release on an unpinned `run`/`shell`,
  and no longer fails offline. The baseline is recovered from the existing image's `agent-version` label so the normal
  upgrade prompt fires (#225).
- Sandboxes: removal and cleanup are now fail-closed against a sandbox name that was removed and recreated (a same-name
  replacement). Every destructive and attach path goes through the identity-aware handle or live sandbox it already
  holds instead of a bare name — prune skips and reports a replaced or already-gone sandbox, throwaway volume sandboxes
  (prefill/copy/edit) destroy through the sandbox they created, a reconfig replace recreates when the old identity is
  already gone and fails closed when it was replaced, and the reaper stops waiting for a sandbox that no longer exists.
  Defense in depth for the shared-sandbox-name collision (#144).
- Volume: `volume migrate`/`reset` on the image-change path now preserves the persisted env/secret/network/mount
  fingerprints. They describe the live sandbox, not the home volume, so dropping them made the next run treat every
  setting as changed and blame a spurious rebuild on, for example, the network policy.
- Runner image: the microsandbox load decision now compares the cached image's content digest against the Docker image
  ID directly, so a rebuilt image that keeps the same `dockerfile-id` source signature (for example a base tag move
  followed by `--rebuild`) is reloaded instead of silently skipped. It no longer inspects host Docker, so a transient
  host Docker inspect failure no longer forces a redundant `docker save` + `ImageLoad`; the `dockerfile-id` label is now
  only a fallback for a legacy cached image that carries no digest (#186).
- Runner image: pinned Node.js to the v24 LTS line (`v24.21.0`) instead of v26, whose official tarballs require
  `libatomic.so.1` that the managed base does not provide. The npm-installed agents (opencode2, pi, claude-code) now
  build and run on the managed base; previously the agent install failed and `node`/`npm` could not start (#132).
- Runner image: the composed `PATH` is now restored in login shells. The image records its composed `PATH` in
  `AGENTS_SANDBOX_IMAGE_PATH` and installs `/etc/profile.d/agents-sandbox-path.sh`, which `/etc/profile` sources after
  its `PATH` reset to re-append missing entries. This supersedes the `/usr/local/bin` symlink stopgap: a custom
  Dockerfile's `ENV PATH` (and the toolchain it points at) now stays effective in `shell` and `!` commands, not only in
  non-login agent-executed commands.
- Runner image: the Docker runtime-prerequisite check (`iptables`, `git`, `ps`, `xz`, `curl`, `tar`) now runs in the
  final stage after the user body instead of the Docker install stage, so a custom base that installs them in its
  Dockerfile body builds again.
- CLI: a negative or overflow-sized duration is now rejected instead of silently accepted. `--older-than` (and the
  deprecated `--age`) plus the `auto-prune-age`, `manual-prune-age`, `auto-stop-timeout`, and `upgrade-interval` config
  keys used to accept `-1d`, and an oversized day/week count such as `110000d` wrapped `int64` into a negative duration.
  A negative prune age matched every artifact, so `prune --older-than -1d` pruned everything instead of nothing (found
  by fuzzing `ParseHumanDuration`).
- CLI: a size specification above the uint32 MiB range is now rejected instead of silently wrapping. Only the
  `G`-suffixed form was bounded, so a plain or `M`-suffixed count such as `4294967296` (4 GiB written as bytes) wrapped
  to zero MiB: `--tmp-size`, `--disk-size`, and `--workspace-quota` accepted it, and `--memory` reached the sandbox as
  0 MiB. Found by CodeQL's `go/incorrect-integer-conversion`.
- Startup hooks with a portable `env` shebang (`#!/usr/bin/env bash`, `#!/usr/bin/env sh`, ...) now run correctly. The
  shebang was previously parsed on the host and passed as a single command name, which the guest could not spawn; hooks
  are now executed directly so the kernel resolves the interpreter, and hook scripts are marked executable during
  provisioning so the host source does not need the executable bit (#136).
- The progress spinner stops promptly: its animation loop now waits on an interruptible ticker instead of an
  uninterruptible `time.Sleep`, so stopping a spinner no longer blocks for up to a full animation frame.
- The agent serve-daemon readiness loop now probes health as soon as it starts the daemon, instead of waiting one poll
  interval (previously 2s) before the first check, cutting the delay before a healthy daemon is detected.
- Release CI: a `v*-canary.N` tag is now published as a GitHub prerelease and no longer bumps the Homebrew formula, so a
  canary is never served by `/releases/latest` to stable installs. The self-upgrade download is addressed by the
  resolved release tag instead of `/latest`, so a prerelease can be installed.

## [0.5.0] - 2026-10-03

### Changed

- Maintenance: updated the microsandbox Go SDK and managed `msb` runtime to v0.7.6.
- Security: VM network egress is now denied by default. Use `--network public`, an explicit `network.profile`, or another
  network profile to opt into broader access. Existing VMs without a recorded network policy are recreated to apply the secure default.
- Docs: the comparison matrix in the README and docs home page gains an "Access restrictions" row noting Docker Sandboxes' sign-in requirement.

### Fixed

- Bugfix: bind-mount sources (the workspace directory and any configured `mounts`) are now canonicalized
  (`filepath.EvalSymlinks`) before they are handed to microsandbox. A path containing a symlink (e.g. macOS
  `/tmp` → `/private/tmp`) previously made the VM fail to start with `mount workspace_...: Not a directory
  (os error 20)` (#92).
- Runner image rebuilds no longer pass the internal `user-provided` provenance marker to an agent installer as a release
  version. Matching user-provided images are reused without release lookup; if the agent is missing from a later base
  image, a real release version is resolved before installation.

## [0.4.0] - 2026-09-26

### Changed

- Maintenance: updated the microsandbox Go SDK and managed `msb` runtime to v0.7.3.
- Self-upgrade: Homebrew-managed installs (the running binary lives under a
  `Cellar` directory) are now detected automatically. The `run`/`shell` update
  check is skipped regardless of `upgrade.mode`, the `msb` runtime-mismatch
  prompt no longer offers a launcher self-upgrade, and `agents-sandbox upgrade`
  points to `brew update && brew upgrade agents-sandbox` so Homebrew's version
  bookkeeping stays accurate (#66).

### Fixed

- Bugfix: the Docker endpoint is now resolved like the `docker` CLI resolves it — `DOCKER_HOST` first, then
  the active docker context (`DOCKER_CONTEXT` or the CLI's `currentContext`), then the default socket.
  colima and Docker Desktop setups that publish their socket through a docker context no longer need a
  manual `DOCKER_HOST` export, and the `doctor` hint now points at `docker context ls` (#67).
- Bugfix: home mappings, agent-config drop-in copies, and verbatim config-directory mirrors now preserve ordinary Unix
  permission bits, including executable bits on launcher scripts.
- Bugfix: runner image builds no longer fail on macOS because the host GID 20 (`staff`)
  collides with `dialout` in the Debian base. `dev` group creation is now collision-tolerant
  (`groupadd -f`); if the host GID is taken, the group gets the next free GID (#68).

## [0.3.1] - 2026-09-24

- Maintenance: updated the microsandbox Go SDK and managed `msb` runtime to v0.7.2.
- Bugfix: runtime preflight now follows microsandbox's persisted runtime paths, avoids advertising unsupported incomplete-runtime recovery, and preserves dangerous any-host secret configuration.

## [0.3.0] - 2026-09-23

### Added

- Config: `network.dns-servers` (config `network.dns-servers`, env
  `OPENCODE_SANDBOX_NETWORK_DNS_SERVERS`, flag `--dns`) sets custom DNS upstream
  resolvers for the VM. Bare IPs get `:53` appended; `host:port` is used as-is.
  Invalid entries are rejected at config-load time. A DNS change recreates the
  VM (like other `network:` settings).
- Distribution: install and update via Homebrew with
  `brew tap inoio/agents-sandbox https://github.com/inoio/agents-sandbox` and
  `brew install agents-sandbox`. The formula (`Formula/agents-sandbox.rb`)
  installs the release binaries; the release workflow opens a formula-bump PR
  for every new release tag.

### Changed

- Bugfix: compare the selected microsandbox `msb` version with the linked SDK
  before runtime use. Mismatches now prompt before changing runtime state and
  offer safe alignment, launcher upgrade, brave mode, issue reporting, or quit.
- Behavior: microsandbox `created` and `starting` states are handled according to their lifecycle semantics; starting VMs are waited on and protected from pruning, while unknown statuses fail closed.
- Bugfix: a rebuilt runner image with the same tag but a new digest now recreates the project VM, so the VM uses the new image.
- Bugfix: refreshing a rebuilt runner image no longer tries to remove the old microsandbox manifest before loading the new
  one, avoiding foreign-key errors when an existing VM still references the image tag.
- Maintenance: image pruning now invokes microsandbox's native image-data prune to reclaim manifests and layers orphaned by
  runner-image refreshes after the referencing VM has been removed.

## [0.2.0] - 2026-09-04

### Changed

- Behavior: the home-file manifest moved from `home.yaml` files into the `home:` key of the launcher config files
  (`config.yaml`/`config.yml`/`config.json`/`config.jsonc`/`config.json5`), at the user and project level. Relative
  sources still resolve against the config file that declares them. `config home` reads the config `home:` key; the
  `home.yaml` files are no longer read.
- Behavior: a malformed `home:` section in a launcher config file is now reported at config-load time (as `mounts` is),
  not only when provisioning or running `config home`.
- Behavior: VMs, home volumes, and state are now scoped per agent (`--agent`): switching agents no longer tears down
  the project sandbox, and opencode/opencode2 can serve concurrently. `stop`/`kill` accept `--agent`. Existing
  per-project VMs/home volumes are abandoned (see docs/sandboxes.md).
- Behavior: image prune now retains any msb image a kept VM still references, not just per-agent `-latest` tags. A
  stopped-not-yet-stale VM created before the per-agent tag redesign references its runner image by digest ref; pruning
  attempted to reclaim it, which failed in msb with a `FOREIGN KEY constraint failed` error.
- Docs: `docs/configuration.md` now documents the `notify:` config block (desktop/audio channels and on-input/on-done/on-error
  triggers) in the configuration table, the environment-variable table (`OPENCODE_SANDBOX_NOTIFY`), and a new
  [Notifications](#notifications) section.
- Docs: rewrote the docs to be agent-agnostic after the rename to agents-sandbox. Landing, intro, and architecture pages
  now talk about "coding agents" (opencode, pi, claude-code) instead of opencode alone; opencode-specific functionality is
  qualified as such — session status and `--notify` are called out as opencode-only, and `--worktree`/`--serve-only` as
  daemon-based-agent features. The lifecycle and C4 diagrams were updated to match.
- Behavior: `OPENCODE_EXPERIMENTAL_WORKSPACES=true` is now baked into the runner image via the opencode agent profile
  (instead of being set at VM creation), so it is included in the image's own env set. The serve-only prompt now reads
  "Connect a client to: ...", and dry-run/daemon messages name the active agent rather than opencode.
- Internal: removed the opencode-specific `UserOpencodeConfigDir`/`ProjectOpencodeConfigDir` config-path helpers
  (use `UserAgentConfigDir`/`ProjectAgentConfigDir` with an agent), renamed the reprovision merged-config symbols
  (`ConfigFiles.OpenCode`→`Merged`, `OpenCodeConfigPath`→`AgentConfigPath`, `OpenCodeConfigEqual`→`AgentConfigEqual`,
  `ChangeFlags.OpenCodeConfig`→`AgentConfig`), and renamed `internal/configmerge/opencodeconfig.go`→`merge.go`.
- Internal: `internal/homeconfig` no longer hardcodes opencode's merged-config path. `ResolveVMTarget`,
  `BuildHomeFiles`, `DescribeManifest`, and `BuildHooks` now take a `reserved` list of home-relative targets the
  manifest must not target (empty reserves nothing). Config provisioning and `config home` reserve the active agent's
  merged-config path (e.g. opencode's `.config/opencode/opencode.jsonc`), so the reservation follows the agent.
- Internal: `internal/notify` is now agent-driven: the SSE stream command, the event-type mapping, and the
  notification titles come from the agent's `EventStreamProvider` capability (`internal/agent`) instead of
  opencode-specific constants. Only the opencode profile implements the capability today, so behavior is unchanged;
  the notify watcher is a no-op for agents without it. The temp WAV prefix is renamed `opencode-notify-`→`agent-notify-`.
- Behavior: when multiple clients watch the same project VM, each notify
  transition (done / needs-input / error) now fires a single host notification
  per opencode session. Notifications are deduplicated across clients via a
  shared per-project claim store keyed by (project, session, trigger), and the
  notify tracker now tracks each session independently. This prevents duplicate
  desktop/audio notifications when more than one `run` instance is attached.
  Deduplication is best-effort: a client whose event stream reconnects across a
  busy→done transition may suppress a single notification until the session's
  next transition.
- Behavior: the opencode awaiting-input event is now `permission.asked`
  (matching the current server event) instead of `permission.updated`.
- Internal: the notify SSE watcher now reconnects forever instead of giving up after a fixed number of
  attempts, reconnecting immediately for the first two consecutive drops then backing off exponentially
  (1s→…→30s, with jitter), resetting the backoff after a connection survives 30s. On session end it reports a
  bounded summary of drops via verbose logging only, after the TUI has closed, so nothing is written while the
  attach TUI owns the terminal.

### Added

- Behavior: the `<agent>` config dirs (`~/.config/agents-sandbox/<agent>/`, `.agents-sandbox/<agent>/`) now act as a
  verbatim mirror of the agent's VM config directory. Files matching the agent's snippet pattern are still deep-merged;
  every other file and subdirectory is provisioned verbatim (e.g. `tui.json`, `AGENTS.md`, `agents/`, `commands/`,
  `themes/`). Precedence: `home:` config key > merged snippet config > mirror > drop-in provisioning. `config agent` lists the
  mirrored files. Previously non-pattern files in `<agent>/` were silently ignored.

- CLI: a new `build dockerfile` subcommand prints the runner Dockerfile exactly as it would be built (`--agent`, `--dind`), without invoking docker. Also available as `image build dockerfile`.
- Project toolchain: PlantUML (standalone jar, pinned version) plus `default-jre`, installed in `.agents-sandbox/Dockerfile`. The launcher merges the microsandbox egress CA into a user-writable JVM truststore so HTTPS `!include` fetches in diagrams trust the injected cert.
- Docs: The "System Context" section in Getting Started now uses a server-rendered PlantUML C4 container diagram (SVG), replacing the Mermaid variants. The `docs` workflow renders `docs/diagrams/*.puml` (excluding the vendored `C4*` library files) to SVG before the Jekyll build; `make docs-diagrams` does the same locally.
-A global `--quiet`/`-q` flag suppresses stdout output (results and table output that `--log-level` does not gate). It can also be set via the `quiet` config key and `OPENCODE_SANDBOX_QUIET` env var.
- Docs: The VM lifecycle and configuration-precedence diagrams are now server-rendered PlantUML (SVG) instead of client-side Mermaid. New `docs/diagrams/lifecycle.puml` and `docs/diagrams/config-precedence.puml` replace the Mermaid blocks in `sandboxes.md` and `configuration.md` respectively. The lifecycle diagram reflects the actual source flow (`internal/sandbox/vm/run_orchestrate.go`): it splits the verify/build steps, details opencode-version resolution, and shows every user prompt (opencode-upgrade rebuild, home-volume keep/migrate/reset/quit, VM-recreate keep/quit, and daemon-restart keep/restart/quit) plus the reconfig decision, image load, startup hooks, dockerd startup on fresh boot only, the interactive opencode session, and reap-on-last-client.
- Docs: The roadmap now lives as GitHub issues. `ROADMAP.md` is a pointer to the issue-driven process, with labels (`priority:critical`, `priority:high`, `priority:low`, `chore`, `upstream`) and filtered search links.
- Docs: New `Recipes` page with a hands-on how-to for connecting Opencode Desktop via `run --serve-only`; moved out of the README. The comparison matrix also names Docker Sandboxes and adds a "Why not just use Docker Sandboxes?" callout.
- Docs: `Recipes` is now a parent overview page (with `has_children`); the Connect Opencode Desktop recipe lives on a nested child page, ready for more recipes to be added.
- Docs: Sidebar submenus are expanded by default via a small script in `docs/_includes/head_custom.html`.
- Added `--notify` / `OPENCODE_SANDBOX_NOTIFY` / `notify:` config to alert the user on opencode session status (input needed, done, error) via desktop and/or audio notifications.
- Configurable host-directory bind mounts (`mounts`), including `~/` source expansion and read-only mounts. Mount changes are tracked via a persisted fingerprint and recreate the project VM.
- Self-upgrade: `run`/`shell` check GitHub for a newer agents-sandbox release (throttled to `upgrade.interval`, default `1d`, minimum `1h`). `upgrade.mode` controls behavior: `prompt` (default; continue / don't-ask-again / upgrade & continue / upgrade & exit, falling back to a notice when non-interactive), `notify`, `auto`, or `auto-exit`. A new `upgrade` command installs the latest release on demand. Checks are skipped for `dev` builds and offline failures are ignored.

- CLI: a new `--agent <name>` flag on `run`, `build`, and the `volume` subcommands selects the coding-agent profile to run/build/provision. The registry abstraction keeps existing usage fully backward compatible (`opencode` remains the default).
- Agents: a built-in `opencode2` (opencode 2 beta) profile, installed from `@opencode-ai/cli@beta` on npm. Daemon-based like `opencode` (serve/attach on the v2 `/api` server, worktree sessions, health via `/api/health`), it shares the `opencode` config directory (v2 reads the same files as v1), resolves its latest version from the npm `beta` dist-tag, and compares `0.0.0-beta-<build>` versions numerically (plain semver mis-sorts them across digit lengths). The quiescence reaper now skips the v1 session-status wait for daemons that expose no such endpoints.
- Agents: built-in `pi` (`@earendil-works/pi-coding-agent`) and `claude-code` (`@anthropic-ai/claude-code`) profiles, alongside the default `opencode`. Both run interactively via the TUI (no daemon), support snippet config merge into `~/.pi/agent/settings.json` / `~/.claude/settings.json`, and resolve latest versions for `build` (pi via `pi.dev`, claude-code via the npm registry). `--worktree` and `--serve-only` are rejected for agents without a daemon.
- Image: the runner Dockerfile installs Node.js before the agent install block so npm-based agents (pi, claude-code) can be installed with `npm install -g`.
- Config: the agent-specific config family (which files the merged config supersedes) is now driven per agent rather than hardcoded to opencode, so merged config detection and `config agent` output are correct for pi and claude-code.
- CLI: a new `--agent-version` flag pins the agent version baked into the runner image (default: latest release).
- Config: the agent can be selected via the `agent` launcher config key and `OPENCODE_SANDBOX_AGENT` env var, in addition to the `--agent` flag.
- Behavior: default drop-in provisioning — when running, the launcher now copies the active agent's config + credential files from the host into the VM by default, driven by per-agent gitignore-style include-list manifests (`ProvisionRules`). For opencode this copies `~/.config/opencode/**` (excluding `node_modules`, `package*.json`, `.gitignore`) and `~/.local/share/opencode/auth.json`.
  - **Security note:** the opencode `auth.json` credential file is now copied into the VM by default. Users who prefer to deliver credentials via the env-secret mechanism should opt out of the file copy — see the docs. The env-secret channel is unchanged and still supported.
- CI: test results are now uploaded to Codecov Test Analytics. The test job runs `gotestsum` to emit JUnit XML (`junit.xml`) alongside `coverage.out` in a single test run and uploads it via `codecov/codecov-action` with `report_type: test_results`; the new `make coverage-junit` target reproduces this locally.
- Image: per-image provenance labels (`org.agents-sandbox.agent`, `org.agents-sandbox.base=<ref>@sha256:<digest>`)
  and in-image source files (`/etc/agents-sandbox/agent-source`, `/etc/agents-sandbox/docker-source`).
- Self-upgrade: upgrade state is now tracked per agent. `updater.yaml` stores a per-agent map (each agent's
  `last_checked`/`current_version`/`agent_source`/`docker_source`/`offered_versions`), so each agent records its own
  version, source, and check window independently. Legacy flat state migrates into the `opencode` entry on load.
- Image: runner tags are now per agent on both docker and microsandbox
  (`agents-sandbox/runner-<slug>:<agent>-latest`). The docker build is skipped when the baked
  `org.agents-sandbox.dockerfile-id` label matches the current content; `EnsureLoaded` verifies the microsandbox cache
  content (config digest or the `dockerfile-id` label) before skipping a load and reloads on mismatch; pruning retains
  one `-latest` image per agent per live project and reclaims all other refs.
- CLI: `config agent` now accepts a `--agent <name>` flag, consistent with `run`/`shell`/`build`. Resolution is
  `--agent` flag → positional `[name]` → configured agent → `opencode`; conflicting `--agent` + positional returns an
  "ambiguous" error.

### Docs

- Docs: restructured the documentation around two on-ramps — "Switch from your existing agent" (host-config drop-in) and
  "Manage config in the sandbox" (declarative config) — and split the configuration reference into per-surface subpages
  (Configuration files & Environment variables, secrets, networking, mounts, home provisioning & startup hooks, agent configuration,
  notifications, self-upgrade).

### Changed

- **Breaking** Image build: agents-sandbox now builds a single runner image per project instead of
  `agents-sandbox/runner-base` and `agents-sandbox/runner-base-dind`. Docker-in-Docker is enabled with the new
  `--dind` flag or `dind: true` config key (a project Dockerfile still starting `FROM .../runner-base-dind:latest`
  keeps working and implies it). A project Dockerfile whose `FROM` is any other image is treated as a custom base and
  gets the agent (and optional dind) layered on top. The agent version is now detected on first boot instead of being
  read from an image label; images that predate the redesign are force-rebuilt once.
- Image: the `dev` user is now created as the first instruction of the final stage instead of in the finalize block.
  Its host UID/GID is reserved before the dind docker group is added, which previously grabbed the first free GID
  (1000) and broke the build with `groupadd: GID '1000' already exists`.
- Image: a managed `FROM .../runner-base...` is now replaced in place by the embedded base tools block instead of being
  stripped and re-prepended, so multi-stage project Dockerfiles keep their base tooling and the `dev` user in the final
  stage.
- Image: bumped the pinned toolchain versions — Node.js to `v26.8.1` and the dind docker engine to `29.7.2`. The previous
  Node `v22.14.0` was below the `>=22.19.0` minimum required by `pi`, which failed the `npm install` during the image
  build with `--agent pi`.
- CLI: the `build` command now honors its `--dind` flag (previously it was registered but the build only used the `dind`
  config key); the flag still falls back to the configured `dind` when not passed.
- Bugfix: non-daemon agents (`pi`, `claude-code`) now exit cleanly on last client — the reaper's `reapOnLastClient`
  returns immediately for them instead of waiting for sessions to quiesce, so the idle timeout stops the VM.
- Docs: Pages are now published only after a successful release, rebuilding from the published tag rather than in parallel on tag push. Each page footer and the home page display the release (or branch) they were built from.
- Docs: Support for dark mode; follows the OS/browser color-scheme preference by default and expose a sun/moon toggle in the header (next to the GitHub link) that overrides and persists the choice.
-**Breaking** CLI: the global `--verbose`/`--error` flags are replaced by a single monotonic `--log-level` flag (`error` | `warning` | `info` | `verbose`, default `info`, short `-l`). The `verbose`/`error` launcher-config keys and `OPENCODE_SANDBOX_VERBOSE`/`OPENCODE_SANDBOX_ERROR` env vars are replaced by `log-level` and `OPENCODE_SANDBOX_LOG_LEVEL`. The level selects the minimum severity shown on the console; `error < warning < info < verbose`, so a higher level is never hidden while a lower one is shown.
-**Breaking** CLI: `sandbox list --quiet`/`-q` (names-only mode) is renamed to the long-only `--names` flag. The `-q` shorthand now selects the global `--quiet` stdout-suppression flag.
- Docs: The README's Documentation section now links directly to the hosted GitHub Pages docs.
- Docs: Reworked the landing-page intro in `README.md` and the docs home page — a tightened tagline, prose leading with hardware-isolated VM isolation, and a comparison matrix (bare opencode / bubblewrap & seatbelt / docker / plain VM / generic VM-based sandbox) highlighting where agents-sandbox differs.
- Bugfix: A change to a secret's allowed hosts (or other secret properties such as placeholder) now triggers a VM recreate; previously only the secret value was part of the change hash, so host changes were silently ignored.
- Bugfix: After a home-volume `reset` or `migrate`, restarting now recreates the project VM so the new home volume is actually mounted; previously the running VM kept serving the old volume because the mounted home volume was not compared during reconfiguration.
- Changed: The runner-image opencode version is now resolved once, up front, before the image is built. An explicitly pinned version skips the update check entirely; otherwise an available upgrade is offered before building so a normal run no longer rebuilds the image twice (once for the current version, once for the upgrade).
- Note: Images built before the opencode-version label existed are no longer force-rebuilt to pin/relabel opencode; if you have such an image, run `agents-sandbox build` once to re-pin it.
- **Breaking** CLI: `--opencode-version` is renamed to `--agent-version`. The old `--opencode-version` flag remains as a deprecated alias.
- Snippet parsing also supports YAML patterns for agents whose pattern includes them.
- Bugfix: Host config files in `~/.config/opencode/` (e.g. `opencode.jsonc`) no longer override the merged config provisioned from snippets. The merged config is now written to `opencode.jsonc`, the last file opencode loads (config.json < opencode.json < opencode.jsonc), and when snippets exist the whole config-file family is removed from the VM so host config cannot deep-merge into it.
- New config key `provision-host-config` (default `true`): set to `false` to skip the default drop-in copy of the agent's host config (and credentials) into the VM entirely. Existing home volumes are cleaned of any previously drop-in-copied config and credential files on the next run.
-**Breaking** CLI: `config show` is replaced by `config agent [name]`, which shows the active agent's merged snippet config and the host drop-in files (each marked `merged` or `not merged`). `config home` is unchanged.

## [0.1.0] - 2026-08-??

### Added

- everything ;)

### Changed

- lives & experiences, hopefully :)
