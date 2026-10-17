# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project adheres to
[Semantic Versioning](https://semver.org/spec/v2.0.0.html). Version tags use a `v` prefix (e.g. `v0.1.0`); the `version`
command reports the bare version (e.g. `0.1.0`).

## [Unreleased]

### Added

- CLI: `image remove <ref>...` (`rm`) deletes cached runner images by reference and `sandbox remove <name>` (`rm`)
  removes a project VM. `image remove` accepts only `agents-sandbox/runner-*` references and refuses an in-use image
  unless `-f`/`--force` (naming the referencing projects); `sandbox remove` refuses a running VM unless
  `-f`/`--force` (which stops it first) and leaves the project's persisted state intact so the recorded home volume
  survives. Both prompt unless the global `--yes` is set. Slice 4 of the runtime-management epic (#165).
- CLI: `df` reports storage usage of the private runtime home by category — the categories microsandbox reports plus the
  host runtime install — mirroring `msb df`. `--verbose` adds per-object rows and host paths, `--format json` prints a
  structured report (unknown counters are `null`, items always present), and the global `-q` prints category names only.
  The `storage` group aliases the same commands as `storage df` and `storage prune`. Slice 5 of the runtime-management
  epic (#165).
- Runner image: per-tool multistage composition so tool layers (apt tools, Node, agent, Docker) cache independently of
  the user body and each other, reusing the same tool layers across projects on one machine.
- Runner image: the `agents-sandbox/runner-base-docker` base reference is recognized (alongside the deprecated
  `agents-sandbox/runner-base-dind`).
- Verbose output now states why the runner image is rebuilt on every rebuild path: the Docker image build names the
  changed identity inputs (project Dockerfile, agent version, Docker-in-Docker) or `--rebuild`/missing image, the
  microsandbox load reports whether the cached image is absent or stale, and a project VM recreate/daemon restart lists
  the config changes that triggered it.
- Internal: add `make test-integration` — runs every `integration`-tagged test across the module (excluded from the main
  suite), currently the docker-build tests that build a real image for every `RenderDockerfile` composition case (no
  project Dockerfile, managed base, and custom Fedora base, each with and without docker). Wired into CI as a selective
  job (main/release/manual).
- Internal: add `make compile-all` — compiles every test binary including `integration`-tagged files without a docker
  daemon, and run it in the CI lint job so a tagged-only compile regression fails on PRs instead of only on main.
- Internal: raise the `make test-integration` timeout to 60m. The docker-build suite legitimately runs ~10 minutes on a
  cold CI runner and was tripping Go's default 10m per-package limit.
- Internal: `devUserBlock` now sets the dev login shell to the first of `bash`, `zsh`, `sh` found (falling back to
  `/bin/sh`) instead of hardcoding bash, widening custom-base compatibility.
- Internal: generalize the fail-closed reference scan behind `volume remove` into a shared `projectStateReferences`
  walk, and add `state.ImageReferences` (the per-agent runner-image analogue of `state.HomeVolumeReferences`) plus
  `image.InUseImageReferences`, which unions the project-state scan with the runner image references held by kept sandboxes
  so a later `image remove` can refuse an in-use runner image. Slice 1 of the runtime-management epic (#165).
- Internal: add `internal/sandbox/storage`, a no-command seam that accounts for the private runtime home's storage
  categories — the six categories microsandbox reports (missing counters stay unknown, an unsupported observation
  degrades to unknown) plus the runtime/FFI artifacts under the home's `bin`/`lib`. It is the data source for a future
  `df`/`storage` report. Slice 2 of the runtime-management epic (#165).
- Custom project Dockerfiles can place a `# agents-sandbox:docker` marker line in the final stage to control where the
  Docker install block is injected, e.g. to run `buildx`/`docker compose` setup after the engine is installed (the legacy
  `# agents-sandbox:dind` marker is still recognized). Without the marker the block is still appended after the
  Dockerfile body.
- `run` and `shell` now accept `--agent-version` to pin the agent version baked into the runner image, matching `build`.
- Runtime: on the first interactive `run`/`shell`, agents-sandbox offers once to copy legacy
  `~/.microsandbox/volumes/agents-sandbox-home-*` volumes into its private runtime home. The choice is now mandatory:
  copy, start fresh/dismiss, or quit. The decision and any in-flight progress are tracked in a `migrations.runtime_home`
  entry in `~/.local/state/agents-sandbox/state.yaml` — a `status` of `copying` (with transient
  `copied`/`kept`/`excluded` volume lists), `completed`, or `dismissed`; an absent entry means the migration is still
  pending. While legacy volumes remain, `--yes` and non-interactive runs never copy and stop with a message to run an
  interactive session once, so the project's recorded home-volume reference is never overwritten before the copy can
  happen. When a private volume already exists under a legacy name (a prior copy of unclear status), the offer says so
  and lets you copy only the missing volumes (leaving the existing ones untouched) or dismiss; the prompt points at
  `volume remove` for manual cleanup.
- `volume remove <name>...` (alias `rm`) removes home volumes by name. It prompts for confirmation (pass `--yes` to
  skip it non-interactively) and refuses when a project's state still references a volume, unless `-f`/`--force` is
  given.
- CLI: `sandbox list`, `volume list`, and `image list` accept `--format json` (structured output on stdout) and the
  global `-q`/`--quiet` (identifiers only — sandbox/volume names or image references), in addition to the default human
  table; the two are mutually exclusive.
- CLI: `prune` and the `image`/`volume`/`sandbox` `prune` subcommands accept `--format json` (a structured report on
  stdout) and `--older-than` (canonical age flag, human-duration syntax such as `7d`/`2w`).
- CLI: `volume inspect <name>`, `image inspect <ref>`, and `sandbox inspect <name>` show the full detail of a home
  volume, a cached runner image, or a sandbox VM (identity, storage/image configuration, mounts, resources). Each
  supports `--format json` (structured output on stdout) and the global `-q`/`--quiet` (the identifier only). Slice 3 of
  the runtime-management epic (#165).
- Sessions started inside a git worktree now bind the enclosing checkout at `/workspace` and start the agent in that
  worktree, so bare checkouts whose worktrees live inside the checkout work host-and-VM-consistently; a worktree outside
  its checkout keeps the current behavior and warns.
- Internal: runtime resolution and installation now pass the private home explicitly via the SDK's `RuntimeConfig.Home`
  (`configpaths.MSBHome()`), so they no longer depend on `MSB_HOME` having been applied first; `MSB_HOME` still governs
  the FFI cache and non-setup SDK operations. `configpaths.MSBHome` is the single source of truth and
  `runtime.PrivateHome` delegates to it. #160.
- Internal: add a regression test asserting every deprecated alias in the deprecation register still resolves and emits
  exactly one warning — the CLI flags `--dind`, `--opencode-version`, `--age`/`-a`, and `sandbox list --names`, plus the
  `OPENCODE_SANDBOX_` env prefix, `AGENTS_SANDBOX_DIND`, and the `dind` config key (#202).

### Fixed

- Agent upgrade: a project that already has a runner image but no recorded agent version (e.g. the first run after the
  `updater.yaml` migration) no longer silently rebuilds to the latest release on an unpinned `run`/`shell`, and no longer
  fails offline. The baseline is recovered from the existing image's `agent-version` label so the normal upgrade prompt
  fires and the existing image is reused when the check is offline (#225).
- `/workspace` bind mount: guest `rwx` permission bits now mirror onto the real host files (`host-permissions=mirror`),
  so an in-VM operation that rewrites a tracked executable (e.g. `git checkout`/`reset`/`stash`) no longer silently drops
  the executable bit on the host while the VM reports a clean tree. Ownership and setuid/setgid are never propagated, and
  configured mounts keep microsandbox's `private` default. The policy is applied when the project VM is created, so an
  existing VM picks it up on its next recreate.
- Reconfiguration: a freshly created home volume's state is now seeded with the desired env/secret/network/mount
  fingerprints in the same write that records the volume, so a persisted state is never fingerprint-less while its VM
  exists. This removes the spurious "network policy changed" rebuild/restart prompt — and, via the one-time network
  migration, a silent rebuild — that a concurrent session could trigger by reading the empty state between VM creation
  and fingerprint persistence, and the same spurious detection after a crash between creation and persistence (#183).
- Home volume: a lost `state.yaml` (deleted by hand, a partial/corrupt write, or a crash before the state write) no longer
  mints a duplicate home volume or orphans the original. When the project VM still mounts a home volume that exists, that
  volume is recovered and state is rewritten to point at it, seeded with the desired fingerprints (or the prior state's
  when it was still readable); a fresh volume is created only when no surviving volume can be confirmed (#219).
- Runner image: the microsandbox load decision now compares the cached image's content digest against the Docker image ID
  directly, so a rebuilt image that keeps the same `dockerfile-id` source signature (e.g. a base tag move followed by
  `--rebuild`) is reloaded instead of silently skipped. It no longer inspects host Docker, so a transient host Docker
  inspect failure no longer forces a redundant `docker save` + `ImageLoad`; the `dockerfile-id` label is now only a
  fallback for a legacy cached image that carries no digest (#186).
- Internal: the rebuilt-VM change-detection inputs are now order-stable by construction. `reprovision.ConfigFiles.Keys`
  has a single ordering authority (the merged config leads, the remainder sorted) instead of being built from unordered
  maps and then sorted again, and `envsecret.BuildSecretsFromSpecs` emits secret entries in sorted `EnvVar` order. The
  decision itself was already order-independent — `state.SecretsContentHash`/`EnvContentHash` sort their inputs and
  `reprovision.AgentConfigEqual` compares by map lookup — so this removes redundant ordering rather than fixing a
  varying decision.
- Volume: `volume migrate`/`reset` on the image-change path now preserves the persisted env/secret/network/mount
  fingerprints. They describe the live VM, not the home volume, so dropping them made the next run treat every setting as
  changed and blame a spurious rebuild on (for example) the network policy.
- Sandboxes: removal and cleanup are now fail-closed against a sandbox name that was removed and recreated (a same-name
  replacement). Every destructive/attach path goes through the identity-aware handle or live sandbox it already holds
  instead of a bare name — prune skips-and-reports a replaced or already-gone sandbox, throwaway volume sandboxes
  (prefill/copy/edit) destroy through the sandbox they created, a reconfig replace recreates when the old identity is
  already gone and fails closed when it was replaced, and the reaper stops waiting for a sandbox that no longer exists.
  Defense-in-depth for the shared-VM-name collision (#144).
- State: launcher-global `state.yaml` (the legacy-migration dismissal lives here) is now written through a uniquely named
  temp file, so two concurrent sessions choosing "Start fresh" no longer collide on one shared temp name and abort a
  session. All persistent state/config writers (the launcher's project/root state writers, `vm.saveUpgradeState`, and
  `upgrade.saveState`) now share one atomic-write helper (`internal/atomicwrite`).
- Runtime: the legacy home-volume copy is now crash-safe and concurrency-safe. Completion is recorded host-side in the
  `migrations.runtime_home` state (never a file inside the guest-writable volume), and the copy campaign holds a
  host-only lock, so a second session cannot discard a copy that is still in flight. A copy interrupted by a crash
  resumes exactly — volumes already recorded as copied are left alone, while any present private volume not recorded as
  copied is treated as a partial and re-copied instead of being used as a truncated home. A volume that cannot be copied
  is excluded from the campaign and recorded, so the remaining volumes still migrate and the campaign can still complete.
- Runtime: a legacy `~/.microsandbox/volumes` directory that exists but is transiently unlistable during a copy campaign
  no longer silently finalizes the migration as complete; the campaign stays `copying`, explains the situation, and
  retries next session.
- Runtime: the legacy home-volume copy no longer overwrites or silently adopts a present private volume of unclear
  status. Such a volume is recorded as `kept` (left untouched) when you choose to copy only the missing volumes, and is
  removed when you dismiss. A campaign fully discards a failed volume's partial before excluding it, and never reaches
  `completed` while a usable partial destination remains, so the home resolver can never adopt a half-copied home.
  Finalization warns about any legacy volumes that could not be copied.
- Runner image: each tool stage now pins its version as an `ARG` default in the rendered Dockerfile (Node, agent, Docker),
  so a version bump changes the build content and reliably invalidates that stage's install layer. Previously only the
  agent version build arg carried the version, which could leave the agent layer cached (and the image on the old release)
  on builders that do not fold the build arg into the cache key. `build dockerfile` gained `--agent-version` to render a
  pinned agent version in the preview.
- Internal: the host home is now resolved through the config-paths seam. `ConfigPaths` gained `UserHomeDir`, and the
  host-home consumers (`reprovision.LoadConfigFiles`, the `config agent` command, `homeconfig.ResolveManifestSource`, and
  the runtime's `LegacyHome` / path sanitizing) resolve it through the seam instead of calling `os.UserHomeDir`.
  `configpaths.WithMockConfigPaths` now also redirects `$HOME` into the per-test temp tree, so a test that installs it
  cannot read or write the developer's real home. Previously `TestConfigAgentPrintsMergedAndHostFiles` wrote `{"model":"host"}` into the
  real `~/.config/opencode/opencode.jsonc` during `go test`; a later launcher start read it as the merged agent config,
  found it different from the host snippet merge, and prompted to restart the server. Every test package without one now
  installs the fail-fast seam mocks (`termio.InitFailFastMocks`) in its `TestMain`, so any test that reaches real
  config-path, Docker, doctor, or msb resolution fails fast instead of silently touching the host. The `notify` backend
  now also installs a fail-fast default for its notification-command seam, and `options.FirstFreeHostPort` probes through
  a `probeHostPort` seam so tests need not bind a real socket.

### Changed

- Upgrade prompt: the agent-upgrade offer on `run`/`shell` now names both the available release and the version
  currently baked into this project's image (e.g. `Upgrade opencode 1.0.0 → 2.0.0`), and the "current" version is read
  from this project's own state so it can never name another project's version. Upgrade checks are now scoped by
  application: machine-global network facts (check cadence and cached latest version) live in the launcher's root
  `state.yaml`, while image facts (current version, sources, offered set) live in each project's per-agent state. The
  legacy global `updater.yaml` is retired, folding its check cadence into the root state (#225). When no version is
  recorded yet (the first run after that migration), the baseline is recovered once from the existing image's
  `agent-version` label so the prompt still fires.
- State: every per-project `state.yaml` read-modify-write now serializes on a single host-only lock per `(slug, agent)`,
  so a volume operation and a concurrent upgrade/provenance write can no longer clobber each other's fields (e.g. an
  upgrade write restoring a stale `home_volume` after a volume op committed a new one).
- Internal: state writes are now structurally correct — one locked transaction per document (`WithHomeState`,
  `WithRootState`) holds the lock across read → mutate → write, raw lock-free writers are no longer exported, removal
  takes the lock, and a same-process double-lock fails fast instead of self-deadlocking. Two failures that were
  previously warned-and-continued now surface as errors, so persisted state can no longer silently diverge from the VM:
  a reconfig that cannot persist its new home volume, and `volume migrate`/`volume reset` whose final state write fails
  (it previously reported success without persisting the new home volume).
- Internal: project-VM reconfiguration now replaces the two-step `Stop` + `Remove` with the SDK's single atomic,
  identity-safe `Destroy` on the `SandboxHandle` seam (#162).
- Runner image: the node/agent/docker install stages now build on a tool-owned `agents-sandbox-toolbase`
  (`debian:trixie-slim` plus `curl`/`ca-certificates`/`tar`) instead of the user's base, so a custom base no longer needs
  `curl`/`tar` inside the image and the tool layers cache across bases. The docker runtime-prerequisite check no longer
  requires `curl`/`tar`. Fixes #143.
- CLI: `prune` and its `image`/`volume`/`sandbox` subcommands now ask for confirmation before removing anything,
  defaulting to no. Pass the global `--yes` to skip the prompt — required in non-interactive contexts, which otherwise
  fail fast rather than block; `--dry-run` only previews and never prompts.
- Runtime: agents-sandbox now provisions and uses its own private microsandbox runtime home at
  `$XDG_DATA_HOME/agents-sandbox/msb` (default `~/.local/share/agents-sandbox/msb`), selected via `MSB_HOME` at startup and
  fully decoupled from any user-installed microsandbox. The user's `~/.microsandbox` is never read or written by normal
  operation; no user-facing `msb` is installed or advertised; and the runtime version-mismatch recovery menu is retired.
  When the private home holds a runtime other than the SDK-pinned one — for example an older runtime left by a previous
  agents-sandbox release — agents-sandbox silently reinstalls the pinned runtime instead of prompting or erroring. A
  project whose recorded home volume is absent from the private home starts a fresh home volume automatically, so
  existing users keep working without migrating.
- Runner image: `opencode`, `opencode2`, and `claude-code` images are now node-free. The `agents-sandbox-node` stage is
  emitted only for agents that need node (today only `pi`, an npm-installed node bundle), and opencode2 and claude-code
  install from their upstream native installers instead of npm. This cuts image size and cold-build time; version pinning,
  upgrade resolution, and `dockerfile-id` identity are unchanged (#133).
- Runner image: renamed the `dind` capability to `docker` — canonical `--docker` (on `build`, `run`, `shell`, and
  `build dockerfile`), the `docker` config key, and `AGENTS_SANDBOX_DOCKER`. The old `--dind` flag, `dind` config key,
  and `AGENTS_SANDBOX_DIND` env var remain as deprecated aliases with lower precedence and a one-time warning. The legacy
  `# agents-sandbox:dind` marker and a `FROM .../runner-base-dind` still imply docker without a warning.
- Runner image: `vfs` is now forced by adding `--storage-driver=vfs` to the dockerd start command inside the VM, instead
  of writing `/etc/docker/daemon.json` at image build time, so a user's edits to `daemon.json` are preserved.
- Launcher environment variables now use the `AGENTS_SANDBOX_` prefix instead of the historical
  `OPENCODE_SANDBOX_`. The old prefix still works as a deprecated alias — it resolves with lower
  precedence than `AGENTS_SANDBOX_` and prints a one-time warning — so existing setups keep working;
  migrate your configuration to `AGENTS_SANDBOX_*` (#120).
- Maintenance: bump Go module dependencies — direct bumps to go-git v5.19.3, go-billy v5.9.2, moby api v1.56.1,
  moby client v0.6.1, and the microsandbox Go SDK v0.7.7, plus transitive updates (ProtonMail go-crypto v1.5.2,
  OpenTelemetry v1.47.0, golang.org/x/crypto v0.57.0, golang.org/x/net v0.59.0, golang.org/x/text v0.42.0).
- Internal: split the `reprovision` package by concern. VM reconfiguration planning moved to
  `internal/sandbox/reconfig`, host env/secret loading to `internal/sandbox/envsecret`, and the
  env/secret/network/mount fingerprint change detection to `internal/sandbox/state`; `reprovision`
  now owns config provisioning and live-VM config comparison only. No behavior change.
- Internal: cut `//nolint` directives from ~180 to 19 by disabling `gochecknoglobals`, using per-type `//exhaustruct:ignore`
  and config `ignore-patterns`, extracting complexity in `PlanReconfig`, `ensureProjectVM`, `Provision`, and
  `LoadConfigFilesForHost`, and removing stale directives for disabled linters.
- Internal: `Provision` now surfaces a failure to create a provisioned parent directory instead of silently ignoring it,
  and added test coverage for the extracted `Provision`/`LoadConfigFilesForHost` helpers.
- Internal: the dev user block is now inserted after the embedded base tools (or after the final `FROM` for a custom base)
  instead of as the first instruction of the final stage. A host UID/GID change no longer invalidates the cached base tools
  layer.
- Internal: the `org.agents-sandbox.dockerfile-id` label moved to the finalization block, after the agent install, so an
  agent-version bump no longer invalidates the cached Node.js and agent install layers.
- Internal: `RenderDockerfile` now splits the project's final stage into earlier stages, base, and body and concatenates the
  tool-owned blocks around them, replacing the FROM/insertion helpers with `splitFinalStage`, `injectDockerBlock`, and `joinBlocks`.
- Internal: a rebuild triggered by a stale image (changed Dockerfile, agent version, or base) now reuses Docker's layer cache;
  only an explicit `--rebuild` bypasses it. An accepted agent upgrade is a version change, so it rebuilds the install layer
  while keeping earlier layers cached.
- Internal: `volume migrate`, `volume reset`, and `volume edit` no longer expose `--rebuild`; they ensure the runner image but
  never force a clean rebuild.
- `--rebuild` on `run`/`shell` now only forces a clean rebuild; it no longer suppresses the agent upgrade check. A pinned
  `--agent-version` is now ignored for a user-provided agent, matching the documented contract.
- Development: add a `make bootstrap` target that installs the pinned toolchain (Go from `.go-version` via goenv, plus golangci-lint and Zig for cross-compilation) and a read-only `make verify` target for CI parity. Bump the toolchain to Go 1.27.1, golangci-lint 2.14.0, gotestsum 1.13.0, and Zig 0.17.0. The `.agents-sandbox/Dockerfile` pins matching versions for the runtime runner image.
- Development: move the macOS linker stubs from `ci/builder/stubs/` to `.github/stubs/` and drop the now-unused `ci/builder/Dockerfile`.
- Docs: improve SEO, e.g. canonical URL to inoio.github.io/agents-sandbox, publish a `sitemap.xml`, home page with descriptive title, site description and link-preview title (`og:title`).
- Docs: `make check` now validates documentation links and linking style via `ci/check-docs.sh` (with tests in
  `ci/test-check-docs.sh`); root-level files link into the docs with rendered HTML links and `docs/` pages link with Jekyll
  `{% link %}` tags.
- Internal: collapse run/shell option resolution into `viperconfig.Resolver.BuildRunOptions` (no behavior change).

### Deprecated

- CLI: `sandbox list --names` is deprecated in favour of the global `-q`/`--quiet` (identifiers only). It is hidden and
  still honored, but warns when used.
- CLI: `prune --age` (and its `-a` short form) is deprecated in favour of `--older-than`. It is hidden and still
  honored, but warns when used; setting both flags together is an error.

### Fixed

- Startup hooks with a portable `env` shebang (`#!/usr/bin/env bash`, `#!/usr/bin/env sh`, ...) now run correctly.
  The shebang was previously parsed on the host and passed as a single command name (e.g. `/usr/bin/env bash`), which
  the VM could not spawn; hooks are now executed directly in the VM so the kernel resolves the interpreter, and hook
  scripts are marked executable during provisioning so the host source does not need the executable bit (#136).

- Runner image: pinned Node.js to the v24 LTS line (`v24.21.0`) instead of v26, whose official tarballs require
  `libatomic.so.1` that the managed base does not provide. The npm-installed agents (opencode2, pi, claude-code) now build
  and run on the managed base — previously the agent install failed and `node`/`npm` could not start. `opencode` (native)
  was unaffected (#132).
- The progress spinner stops promptly. Its animation loop now waits on an interruptible ticker instead of an
  uninterruptible `time.Sleep`, so stopping a spinner no longer blocks for up to a full animation frame.
- The agent serve-daemon readiness loop now probes health as soon as it starts the daemon, instead of waiting one poll
  interval (previously 2s) before the first check, cutting the delay before a healthy daemon is detected.
- CI: the `release` job now depends on the `integration` job, so tagged releases wait for the full-image integration
  tests before publishing.
- CI: the docs deployment now runs on the release tag push and reports that tag in the site footer. Previously it relied
  on the `release: published` event, which the release-creation step's `GITHUB_TOKEN` suppresses, so the docs only
  redeployed on a manual dispatch and the footer read `main`.
- Runner image: the composed `PATH` is now restored in login shells. The image records its composed `PATH` in
  `AGENTS_SANDBOX_IMAGE_PATH` and installs `/etc/profile.d/agents-sandbox-path.sh`, which `/etc/profile` sources after its
  `PATH` reset and uses to re-append missing entries. This supersedes the `/usr/local/bin` symlink stopgap; a custom
  Dockerfile's `ENV PATH` (and the toolchain it points at) now stays effective in `shell` and in `!` commands, not only in
  non-login agent-executed commands.
- Runner image: the Docker runtime prerequisite check (`iptables`, `git`, `ps`, `xz`, `curl`, `tar`) now runs in the final
  stage after the user body instead of the Docker install stage, so a custom base that installs them in its Dockerfile body
  builds again. `curl` and `tar` are still required in the base image for the download.
- Docs: `ci/check-docs.sh` no longer reports false link violations from local, gitignored build artefacts (`docs/_site/`
  Jekyll output and `docs/vendor/` gem bundle), and `docs/_config.yml` excludes the untracked `superpowers/` and `vendor/`
  trees from the deployed site.
- Internal: the `integration` docker-build tests no longer wipe the classic builder's layer cache when they clean up.
  Removing an image with `PruneChildren: true` also deletes the untagged parent images that are the cache, so every
  subtest (and every rerun) rebuilt the node/agent/docker installs from scratch; cleanup now removes only the tag.
  `TestRealDockerClientImagePrune` now prunes with a filter that matches nothing, so it exercises the delegation path
  without deleting that cache (or a developer's unrelated dangling images/build cache) from the shared daemon.

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
