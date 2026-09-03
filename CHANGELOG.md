# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project adheres to
[Semantic Versioning](https://semver.org/spec/v2.0.0.html). Version tags use a `v` prefix (e.g. `v0.1.0`); the `version`
command reports the bare version (e.g. `0.1.0`).

## [Unreleased]

### Changed

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

### Added

- Behavior: the `<agent>` config dirs (`~/.config/opencode-sandbox/<agent>/`, `.opencode-sandbox/<agent>/`) now act as a
  verbatim mirror of the agent's VM config directory. Files matching the agent's snippet pattern are still deep-merged;
  every other file and subdirectory is provisioned verbatim (e.g. `tui.json`, `AGENTS.md`, `agents/`, `commands/`,
  `themes/`). Precedence: `home.yaml` > merged snippet config > mirror > drop-in provisioning. `config agent` lists the
  mirrored files. Previously non-pattern files in `<agent>/` were silently ignored.

- CLI: a new `build dockerfile` subcommand prints the runner Dockerfile exactly as it would be built (`--agent`, `--dind`), without invoking docker. Also available as `image build dockerfile`.
- Project toolchain: PlantUML (standalone jar, pinned version) plus `default-jre`, installed in `.opencode-sandbox/Dockerfile`. The launcher merges the microsandbox egress CA into a user-writable JVM truststore so HTTPS `!include` fetches in diagrams trust the injected cert.
- Docs: The "System Context" section in Getting Started now uses a server-rendered PlantUML C4 container diagram (SVG), replacing the Mermaid variants. The `docs` workflow renders `docs/diagrams/*.puml` (excluding the vendored `C4*` library files) to SVG before the Jekyll build; `make docs-diagrams` does the same locally.
* A global `--quiet`/`-q` flag suppresses stdout output (results and table output that `--log-level` does not gate). It can also be set via the `quiet` config key and `OPENCODE_SANDBOX_QUIET` env var.
- Docs: The VM lifecycle and configuration-precedence diagrams are now server-rendered PlantUML (SVG) instead of client-side Mermaid. New `docs/diagrams/lifecycle.puml` and `docs/diagrams/config-precedence.puml` replace the Mermaid blocks in `sandboxes.md` and `configuration.md` respectively. The lifecycle diagram reflects the actual source flow (`internal/sandbox/vm/run_orchestrate.go`): it splits the verify/build steps, details opencode-version resolution, and shows every user prompt (opencode-upgrade rebuild, home-volume keep/migrate/reset/quit, VM-recreate keep/quit, and daemon-restart keep/restart/quit) plus the reconfig decision, image load, startup hooks, dockerd startup on fresh boot only, the interactive opencode session, and reap-on-last-client.
- Docs: The roadmap now lives as GitHub issues. `ROADMAP.md` is a pointer to the issue-driven process, with labels (`priority:critical`, `priority:high`, `priority:low`, `chore`, `upstream`) and filtered search links.
- Docs: New `Recipes` page with a hands-on how-to for connecting Opencode Desktop via `run --serve-only`; moved out of the README. The comparison matrix also names Docker Sandboxes and adds a "Why not just use Docker Sandboxes?" callout.
- Docs: `Recipes` is now a parent overview page (with `has_children`); the Connect Opencode Desktop recipe lives on a nested child page, ready for more recipes to be added.
- Docs: Sidebar submenus are expanded by default via a small script in `docs/_includes/head_custom.html`.
- Added `--notify` / `OPENCODE_SANDBOX_NOTIFY` / `notify:` config to alert the user on opencode session status (input needed, done, error) via desktop and/or audio notifications.
- Configurable host-directory bind mounts (`mounts`), including `~/` source expansion and read-only mounts. Mount changes are tracked via a persisted fingerprint and recreate the project VM.
- Self-upgrade: `run`/`shell` check GitHub for a newer opencode-sandbox release (throttled to `upgrade.interval`, default `1d`, minimum `1h`). `upgrade.mode` controls behavior: `prompt` (default; continue / don't-ask-again / upgrade & continue / upgrade & exit, falling back to a notice when non-interactive), `notify`, `auto`, or `auto-exit`. A new `upgrade` command installs the latest release on demand. Checks are skipped for `dev` builds and offline failures are ignored.

- CLI: a new `--agent <name>` flag on `run`, `build`, and the `volume` subcommands selects the coding-agent profile to run/build/provision. The registry abstraction keeps existing usage fully backward compatible (`opencode` remains the default).
- Agents: built-in `pi` (`@earendil-works/pi-coding-agent`) and `claude-code` (`@anthropic-ai/claude-code`) profiles, alongside the default `opencode`. Both run interactively via the TUI (no daemon), support snippet config merge into `~/.pi/agent/settings.json` / `~/.claude/settings.json`, and resolve latest versions for `build` (pi via `pi.dev`, claude-code via the npm registry). `--worktree` and `--serve-only` are rejected for agents without a daemon.
- Image: the runner Dockerfile installs Node.js before the agent install block so npm-based agents (pi, claude-code) can be installed with `npm install -g`.
- Config: the agent-specific config family (which files the merged config supersedes) is now driven per agent rather than hardcoded to opencode, so merged config detection and `config agent` output are correct for pi and claude-code.
- CLI: a new `--agent-version` flag pins the agent version baked into the runner image (default: latest release).
- Config: the agent can be selected via the `agent` launcher config key and `OPENCODE_SANDBOX_AGENT` env var, in addition to the `--agent` flag.
- Behavior: default drop-in provisioning — when running, the launcher now copies the active agent's config + credential files from the host into the VM by default, driven by per-agent gitignore-style include-list manifests (`ProvisionRules`). For opencode this copies `~/.config/opencode/**` (excluding `node_modules`, `package*.json`, `.gitignore`) and `~/.local/share/opencode/auth.json`.
  - **Security note:** the opencode `auth.json` credential file is now copied into the VM by default. Users who prefer to deliver credentials via the env-secret mechanism should opt out of the file copy — see the docs. The env-secret channel is unchanged and still supported.
- CI: test results are now uploaded to Codecov Test Analytics. The test job runs `gotestsum` to emit JUnit XML (`junit.xml`) alongside `coverage.out` in a single test run and uploads it via `codecov/codecov-action` with `report_type: test_results`; the new `make coverage-junit` target reproduces this locally.
- Image: per-image provenance labels (`org.opencode-sandbox.agent`, `org.opencode-sandbox.base=<ref>@sha256:<digest>`)
  and in-image source files (`/etc/opencode-sandbox/agent-source`, `/etc/opencode-sandbox/docker-source`).
- Self-upgrade: upgrade state is now tracked per agent. `updater.yaml` stores a per-agent map (each agent's
  `last_checked`/`current_version`/`agent_source`/`docker_source`/`offered_versions`), so each agent records its own
  version, source, and check window independently. Legacy flat state migrates into the `opencode` entry on load.
- Image: runner tags are now per agent on both docker and microsandbox
  (`opencode-sandbox/runner-<slug>:<agent>-latest`). The docker build is skipped when the baked
  `org.opencode-sandbox.dockerfile-id` label matches the current content; `EnsureLoaded` verifies the microsandbox cache
  content (config digest or the `dockerfile-id` label) before skipping a load and reloads on mismatch; pruning retains
  one `-latest` image per agent per live project and reclaims all other refs.
- CLI: `config agent` now accepts a `--agent <name>` flag, consistent with `run`/`shell`/`build`. Resolution is
  `--agent` flag → positional `[name]` → configured agent → `opencode`; conflicting `--agent` + positional returns an
  "ambiguous" error.

### Changed

- **Breaking** Image build: opencode-sandbox now builds a single runner image per project instead of
  `opencode-sandbox/runner-base` and `opencode-sandbox/runner-base-dind`. Docker-in-Docker is enabled with the new
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
* **Breaking** CLI: the global `--verbose`/`--error` flags are replaced by a single monotonic `--log-level` flag (`error` | `warning` | `info` | `verbose`, default `info`, short `-l`). The `verbose`/`error` launcher-config keys and `OPENCODE_SANDBOX_VERBOSE`/`OPENCODE_SANDBOX_ERROR` env vars are replaced by `log-level` and `OPENCODE_SANDBOX_LOG_LEVEL`. The level selects the minimum severity shown on the console; `error < warning < info < verbose`, so a higher level is never hidden while a lower one is shown.
* **Breaking** CLI: `sandbox list --quiet`/`-q` (names-only mode) is renamed to the long-only `--names` flag. The `-q` shorthand now selects the global `--quiet` stdout-suppression flag.
- Docs: The README's Documentation section now links directly to the hosted GitHub Pages docs.
- Docs: Reworked the landing-page intro in `README.md` and the docs home page — a tightened tagline, prose leading with hardware-isolated VM isolation, and a comparison matrix (bare opencode / bubblewrap & seatbelt / docker / plain VM / generic VM-based sandbox) highlighting where opencode-sandbox differs.
- Bugfix: A change to a secret's allowed hosts (or other secret properties such as placeholder) now triggers a VM recreate; previously only the secret value was part of the change hash, so host changes were silently ignored.
- Bugfix: After a home-volume `reset` or `migrate`, restarting now recreates the project VM so the new home volume is actually mounted; previously the running VM kept serving the old volume because the mounted home volume was not compared during reconfiguration.
- Changed: The runner-image opencode version is now resolved once, up front, before the image is built. An explicitly pinned version skips the update check entirely; otherwise an available upgrade is offered before building so a normal run no longer rebuilds the image twice (once for the current version, once for the upgrade).
- Note: Images built before the opencode-version label existed are no longer force-rebuilt to pin/relabel opencode; if you have such an image, run `opencode-sandbox build` once to re-pin it.
* **Breaking** CLI: `--opencode-version` is renamed to `--agent-version`. The old `--opencode-version` flag remains as a deprecated alias.
* **Breaking** config merge: opencode config snippet files must now match the pattern `opencode-*.json*` (i.e. `opencode-<name>.json`/`.jsonc`/`.json5`). A file named exactly `opencode.json` no longer merges by default. Snippet parsing also supports YAML patterns for agents whose pattern includes them.
- Bugfix: Host config files in `~/.config/opencode/` (e.g. `opencode.jsonc`) no longer override the merged config provisioned from snippets. The merged config is now written to `opencode.jsonc`, the last file opencode loads (config.json < opencode.json < opencode.jsonc), and when snippets exist the whole config-file family is removed from the VM so host config cannot deep-merge into it.
- New config key `provision-host-config` (default `true`): set to `false` to skip the default drop-in copy of the agent's host config (and credentials) into the VM entirely. Existing home volumes are cleaned of any previously drop-in-copied config and credential files on the next run.
* **Breaking** CLI: `config show` is replaced by `config agent [name]`, which shows the active agent's merged snippet config and the host drop-in files (each marked `merged` or `not merged`). `config home` is unchanged.

## [0.1.0] - 2026-08-??

### Added

- everything ;)

### Changed

- lives & experiences, hopefully :)
