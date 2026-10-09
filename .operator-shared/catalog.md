# Shared Partition Catalog

## Tree

- `operator.md`
  - Description: Operator Instructions for this partition. Currently a stub; rules/style remain private and `AGENTS.md` is authoritative.
  - Read If: Auto-injected.
- `catalog.md`
  - Description: This catalog.
  - Read If: Auto-injected.
- `README.md`
  - Description: Operator Memory overview and install instructions.
  - Read If: Onboarding to Operator / setting up the plugin.

### `specs/` - System contracts for this partition

- `runner-image-build.md`
  - Description: Agent version selection precedence (`--agent-version`, upgrade check, user-provided), when the runner image is (re)built, and when `BuildOptions.Force` bypasses the Docker layer cache.
  - Read If: Changing image build/caching, `BuildOptions`, `--rebuild`, `--agent-version`, agent upgrade, or volume subcommands' image handling.
- `guided-onboarding.md`
  - Description: Design contract for first-run onboarding: credential kinds (static vs login; never placeholder-migrate refreshable OAuth), host determination, derived onboarding state, always-explicit `Guide` outcomes, the tiered setup wizard, and `config harden`.
  - Read If: Working on config migration, first-run guidance, credential/auth handling, network egress derivation, the setup wizard, or `config harden`.
- `termio-ui-backends.md`
  - Description: Contract for termio's UI backends: the decision to confine huh to modal prompts (spinners/tables/log output stay hand-rolled), the `termio.UI` mockability seam, the `OPENCODE_SANDBOX_PROMPT` prompt switch, the accessibility/non-TTY invariant (huh implements accessible by bypassing bubbletea), and why the huh spinner was rejected (bubbletea diff renderer corrupts under concurrent output).
  - Read If: Changing interactive prompts or spinners, scoping a UI library (huh, bubbletea, ...), or reasoning about mockability, non-TTY/accessibility, or concurrent-output behavior.
- `image-composition.md`
  - Description: Contract for composing the per-project runner Dockerfile into per-tool multistage stages (base/node/agent/docker) merged into the runner via `COPY`, so tool-owned layers cache independently of the user body and each other; the `/opt/agents-sandbox` artifact contract, adoption-time user-provided detection, the docker marker as a copy/adopt point, forcing vfs via the dockerd flag instead of `daemon.json`, the `dind` → `docker` rename with deprecated aliases, and login-shell PATH handling (`AGENTS_SANDBOX_IMAGE_PATH` re-merged via the `/etc/profile.d` script, whose content is folded into the dockerfile-id).
  - Read If: Changing `RenderDockerfile`/stage composition, tool install paths, layer caching, the docker/dind switch or marker, custom-base handling, login-shell PATH handling (`AGENTS_SANDBOX_IMAGE_PATH`, the `/etc/profile.d` merge script, dockerfile-id identity), or the dockerd storage driver.
- `login-shell-path.md`
  - Description: Contract for keeping the runner image's composed PATH (custom Dockerfile `ENV PATH`, `/opt/agents-sandbox/bin`, project toolchains) effective in login-shell contexts by re-merging dropped entries from `AGENTS_SANDBOX_IMAGE_PATH` through a `/etc/profile.d` drop-in (devcontainers-style mergePaths); supersedes the `/usr/local/bin` symlink stopgap. Acceptance target is resolution equivalence, not byte-identical PATH.
  - Read If: Working on runner-image PATH handling, login-shell (`-l`) sessions, `run`/`shell` attach, custom Dockerfile `ENV PATH`, or the `/etc/profile.d` merge helper.

### `plans/` - TDD implementation plans

- `2026-10-07-guided-onboarding-a-credential-kind.md`
  - Description: Plan A (credential kinds, explicit `Guide`, network-deny handling) — implemented on `issue-99-guided-onboarding-a`.
  - Read If: Reviewing or resuming Plan A of `specs/guided-onboarding.md`.
- `2026-10-07-guided-onboarding-b-state-machine.md`
  - Description: Plan B (onboarding state machine: persisted `manual`/`login-pending`, verification loop, non-interactive reminder) — implemented on `issue-99-guided-onboarding-b`.
  - Read If: Implementing or resuming Plan B of `specs/guided-onboarding.md`.
- `2026-10-08-guided-onboarding-c-tiered-wizard.md`
  - Description: Plan C (tiered setup wizard: `internal/configfile` writer extraction, `internal/configwizard` catalog/runner/state, viperconfig higher-scope detection, `config wizard [--tier=N]`, first-run integration) — implemented and merged into `issue-99-guided-config-setup`.
  - Read If: Implementing or resuming Plan C of `specs/guided-onboarding.md`, or working on the setup wizard.
- `2026-10-09-login-shell-path.md`
  - Description: Plan for `specs/login-shell-path.md`: the POSIX-sh `mergePaths` profile.d script (+ sh behaviour tests), image wiring (`AGENTS_SANDBOX_IMAGE_PATH`, build-context `COPY`), removing the `/usr/local/bin` symlink stopgap, an end-to-end login-shell integration test, and docs/spec/CHANGELOG updates.
  - Read If: Implementing or reviewing the login-shell PATH (mergePaths) work.
