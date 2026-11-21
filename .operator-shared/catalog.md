# Shared Partition Catalog

## Tree

- `operator.md`
  - Description: Shared agent instructions not covered by `AGENTS.md` (the approval norm, the subagent model rule, a pointer to `guides/operator-brain-maintenance.md`, the skills policy, and the shared/private policy).
  - Read If: Auto-injected.
- `catalog.md`
  - Description: This catalog.
  - Read If: Auto-injected.
- `README.md`
  - Description: Operator Memory overview and install instructions.
  - Read If: Onboarding to Operator / setting up the plugin.

### `adr/` - Architecture decision records

- `adr/0001-yaml-handling.md`
  - Description: Decision record: the YAML write path (`oneyaml.dev/go`, `go.yaml.in/yaml/v4`) and the 16 MiB cap on untrusted config/secret/snippet reads (`internal/configread`).
  - Read If: Changing `viperconfig.WriteSetting`, the YAML edit path, or the `configread` bound; evaluating oneyaml vs goccy; touching `go.mod`'s YAML dependency or the `yaml/v3`↔`v4` choice; or triaging a `FuzzParseState` serialization-stability failure.
- `0002-rootless-docker-engine.md`
  - Description: Decision record: run the in-VM Docker engine rootless as `dev` rather than grant the root-equivalent `docker` group, and the prerequisites that decision pins.
  - Read If: Changing how the in-VM Docker engine is installed, started, or reached (`dockerAdoptionBlock`, `vm/dockerd.go`), its prerequisites, or evaluating podman/a daemonless builder/an authz proxy instead.
- `adr/TEMPLATE.md`
  - Description: Template for an ADR in this directory (Context / Options / Decision / Consequences).
  - Read If: Writing a new ADR.

### `guides/` - How-to and reference notes for this partition

- `guides/agent-link-opening.md`
  - Description: How each agent opens a host link from inside the VM, the OSC 8 wrinkle, and the network-free VM→host opener transport.
  - Read If: Working on opening links/URLs on the host from inside the VM (#273), an in-VM `xdg-open` shim, agent opener behavior, or any VM→host action channel.
- `guides/branch-worktree-cleanup.md`
  - Description: How `scripts/cleanup-merged-branches.sh` (via `make cleanup-branches`) removes merged worktrees/branches and reports the rest, with its merge-detection tiers and safety guards.
  - Read If: Running or changing local repository maintenance, cleaning up worktrees or merged branches, or debugging a branch the sweep kept or removed.
- `guides/buildkit-engine-compatibility.md`
  - Description: Which Docker-API engines survive each BuildKit mechanism when building the runner image, per engine family, plus the render's feature-safety and BuildKit-only syntax.
  - Read If: Considering support for another Docker-API engine (or a builder-selection/fallback change) by evaluating which engines survive each BuildKit mechanism, or diagnosing a runner-image build that fails on a non-Docker engine.
- `guides/dead-code-detection-go.md`
  - Description: How to run x/tools `deadcode` to find production code only tests keep alive, and why staticcheck U1000 cannot.
  - Read If: Looking for dead or unreferenced production code, auditing whether a seam is still wired, or evaluating a dead-code linter; for the gate, the pin, or the `//deadcode:ignore` marker, read `specs/ci/quality-gates.md`.
- `guides/dev-environment.md`
  - Description: The development VM's installed CLI tools, its `GOCACHE` placement, and its standing environment limitations.
  - Read If: Choosing a CLI tool for a task, or hitting an environment limitation such as an unusable SSH remote, an absent CI run on a conflicted PR, a self-signed-cert docker build failure, a "catalog ancestor ... permits path substitution" error from the launcher's runtime home, or a mutation sweep that OOMs the VM; or questioning why `GOCACHE` is not on the home volume.
- `guides/github-issue-workflow.md`
  - Description: How this repo tracks work on GitHub with the `gh` CLI, including the triage label vocabulary.
  - Read If: Creating, reading, labeling, or closing a GitHub issue; resolving the label vocabulary the `maintain` skill uses; or working through a skill that publishes to or reads from the issue tracker.
- `guides/github-merge-driver-support.md`
  - Description: Verdict, with primary sources: GitHub ignores `.gitattributes` merge drivers server-side, so `CHANGELOG.md merge=union` does not resolve a conflict.
  - Read If: Considering `merge=union`, a custom merge driver, or any `.gitattributes` merge attribute to auto-resolve conflicts on GitHub; or explaining why a server-side conflict persists when the CLI git merges cleanly.
- `guides/github-pr-workflow.md`
  - Description: The PR mechanics `guides/github-issue-workflow.md` does not cover: landing through the `main` merge queue, the `GITHUB_TOKEN` bot-PR approval, and naming a changelog fragment's slug.
  - Read If: Opening, merging, or troubleshooting a PR; enqueueing one into the merge queue; a bot PR that reports no checks; or naming a `changelog.d/` fragment before the PR number is known.
- `guides/go-version-support.md`
  - Description: The module's minimum Go version and why it cannot be lower, with the evidence and primary sources.
  - Read If: Changing the `go` directive, `.go-version`, or a Go pin; asking whether a lower Go version can be advertised; or diagnosing a dependency/promoted-field/GOTOOLCHAIN "requires a newer Go" error.
- `guides/mutation-testing.md`
  - Description: Mutation testing for Go in this repo: the gomutants choice, how to close survivors, and the multi-turn sweep runbook.
  - Read If: Evaluating or changing a Go mutation-testing tool or the `make mutation` target; triaging LIVED mutants from a sweep; picking a `SINCE` scope for a branch/PR sweep; coordinating a #268 survivor-reduction pass; or deciding whether a sweep may run in the VM (it may not - it OOMs).
- `guides/operator-brain-maintenance.md`
  - Description: Conventions for keeping this brain small and findable, and the structural rules `make brain-check` enforces.
  - Read If: Adding or editing a catalog entry, spec, plan, guide, or skill; writing a new freeform document; curating/retiring the brain; or a red `make brain-check` run.
- `guides/podman-engine-testing.md`
  - Description: How the alternative-engine claim is verified: the `integration-podman` job, rootless podman's unqualified-image refusal, and the dev VM's inability to run an engine build.
  - Read If: Verifying podman (or another Docker-API engine) against the integration suite, or investigating a build failure naming an unqualified image.
- `guides/secret-placeholder-transcript-trap.md`
  - Description: Why a session dies once a microsandbox secret placeholder reaches the model context, how to observe the block, and the launcher's repair.
  - Read If: Diagnosing a model turn blocked by a secret violation, working on `secrets.common.allow-placeholder-for`/`BuildSecretsFromSpecs`/`placeholderHostsFor`, or envsecret passthrough hosts.
- `guides/test-runtime-cost.md`
  - Description: Where `go test ./...` wall time goes and the seams that set failure latency.
  - Read If: Speeding up `make test`, diagnosing a slow or hanging test run, adding a polling/wait seam or a `TestMain`, adding a real-SDK test, or deciding whether a test may consume a production timeout budget.
- `guides/vm-oom-crash-resilience.md`
  - Description: How the microVM enforces memory/disk/CPU limits, why an OOM kill is invisible from the launcher, and the detect-and-propose mitigation path.
  - Read If: Working on VM OOM/crash handling, a memory/disk/CPU limit-hit detector or "raise the limit" proposal, terminal corruption after an in-VM kill, or the daemon/dockerd re-ensure and supervision behaviour.

### `specs/` - System contracts for this partition

#### `specs/agents/` - Agent profiles and their daemons

- `specs/agents/`
  - Description: Agent-profile contracts: `hermes-agent.md` (the Hermes profile and installer), `notify.md` (the desktop-notify event watcher), `opencode2.md` (the opencode2 profile).
  - Read If: Adding or changing an agent profile (the `hermes` install/`OwnsToolTree`, or the opencode2 config-namespace fork / `ConfigDirAliaser` / `OPENCODE_SERVER_PASSWORD` / `DaemonHealthCmd`); the notify decode seam (`agent.EventStreamSpec`/`Parse`/`SessionListSpec`/`SessionRef`), `parentID` subagent filtering, the subtree done reasons (`DoneReasons`/`SubtreeDoneStates`/`notify.on-done-subagents-running`), or the message format; or diagnosing a dead-looking opencode2 daemon, an opencode2 provider without a connection, a `--worktree` that 404s, "notify does nothing on opencode2", a done firing while a subagent runs, or a Hermes launch that fails before its REPL.

#### `specs/architecture/` - Cross-cutting package seams and run orchestration

- `specs/architecture/`
  - Description: Cross-cutting contracts: `client-injection.md` (resolve external clients once and thread them), `dry-run-preview.md` (`--dry-run` makes no changes), `msb-seam.md` (the `internal/sandbox/msb` SDK seam), `package-boundaries.md` (`cliout`/`view` and the run layering), `termio-ui.md` (termio's UI seam).
  - Read If: Changing a `docker.Get()`/`configpaths.Get()` call site or threading a client/path resolver; `--dry-run`/`--dry-run-vm` or the `options.Mode` boundary; the `msb` role interfaces/composites/mock or how a consumer depends on the seam; moving a Printer/report DTO between `cliout` and `view` or run-preparation code between `prepare` and `vm`; a termio role interface, `termio.UI`/`New`, prompts/spinners, or non-TTY behavior.

#### `specs/ci/` - CI pipeline and quality gates

- `specs/ci/`
  - Description: CI and gate contracts: `ci-pipeline.md` (workflows, required checks, merge queue, changelog fragments, the build cache, the `changes` gate), `mutation-testing.md` (the `make mutation` practice), `quality-gates.md` (the lint/dead-code/doc-link/shellcheck gates and the guard-consistency checker).
  - Read If: Changing `.github/workflows/*.yml`, CI triggers, the merge queue/`merge_group`, the required-check set or `protect main` ruleset, the `changes` gate, the `run-integration*` labels or the integration jobs' `GH_TOKEN`, or changelog authoring; the `make mutation` target/`GOMUTANTS_VERSION`/triage convention or the advisory `mutation` workflow; `.golangci.yml`, the `lint` build-tag set, the dead-code gate (`make deadcode`/`//deadcode:ignore`), the doc-link gate, the `actionlint` pin, the `make shellcheck` gate, or the guard-consistency checker (`ci/check-ci-guards.sh`, the exempt-path lists, the `github.event_name`/`on.pull_request.types` invariants).

#### `specs/configuration/` - Config scopes, keys, decoding, validation, project-config root and trust

- `specs/configuration/`
  - Description: Config contracts: `config-file-decoding.md` (the `configdecode`/`configread` core), `config-keys.md` (the key registry), `config-scopes.md` (path resolution and scope order), `config-value-validation.md` (VM size/duration bounds), `project-config-root.md` (the bare-checkout root), `project-config-trust.md` (the approve-on-change gate).
  - Read If: Config-file reading/classification/JSON5, a config key's env var or CLI flag, config-scope order or `configpaths.ConfigPaths`/`ConfigScopes`, VM size/duration validation or the tmp-vs-memory relation, the bare-layout project-config root/`core.bare`, or the project-config trust gate (`config harden`, provenance); or diagnosing a file one loader accepts and another rejects, a silently ignored env var, a rejected size, or a run that refuses to start because `.agents-sandbox` changed.

#### `specs/conventions/` - Repo-wide conventions

- `specs/conventions/`
  - Description: `deprecation-policy.md` — how a flag/config key/env var/command is deprecated and the current deprecation register.
  - Read If: Deprecating or removing a CLI flag, config key, env var, or command; deciding whether a change needs a shim or a clean break; or enforcing the deprecation register in tests.

#### `specs/dev-image/` - This repo's dev-VM image composition

- `specs/dev-image/`
  - Description: `composition.md` — the dev-VM image's multistage `.agents-sandbox/Dockerfile` layout and per-stage version pins.
  - Read If: Editing `.agents-sandbox/Dockerfile` or the dev-VM toolchain; bumping a pinned dev tool or adding/removing an apt package; or diagnosing a dev-image rebuild that re-ran more than the changed component.

#### `specs/identity/` - Project, artifact, and sandbox identity and naming

- `specs/identity/`
  - Description: Identity contracts: `artifact-naming.md` (prefix-free sandbox/volume/image names; Docker keeps the prefix), `checkout-identity.md` (the repo slug vs the checkout slug), `sandbox-identity.md` (a sandbox's mutable name vs its stable identity).
  - Read If: Changing `internal/sandbox/naming` or the name/reference constructors/parsers, or artifact discovery; the repo-vs-checkout slug derivation, the runner-image tag identity, the `config paths` command, or the checkout-identity migration; the msb sandbox create/remove/attach paths, `EnsureProjectVM`, prune, the reaper, or reconfig-replace; or diagnosing a legacy artifact that stopped being found, `ErrSandboxReplaced`, or two checkouts sharing one VM.

#### `specs/network-and-secrets/` - Network profiles and secret scoping

- `specs/network-and-secrets/`
  - Description: `network-profiles.md` (composable profiles and fingerprinting), `secret-header-scoping.md` (header-scoped placeholder substitution), `interception-and-proxy.md` (TLS-interception control and the outbound proxy).
  - Read If: Changing `network.Policy`, profile parsing/validation, the `network.profile`/`--network`/`AGENTS_SANDBOX_NETWORK_PROFILE` surface, or network fingerprinting; per-secret header substitution, `envsecret.SecretSpec`/`BuildSecretsFromSpecs`, the `header_fields` docs, or `state.SecretsContentHash`; or the `network.intercept-tls`/`network.tls-bypass`/`network.disable-strict`/`network.proxy.*` surfaces.

#### `specs/onboarding/` - First-run guidance and learn mode

- `specs/onboarding/`
  - Description: `onboarding.md` — the first-run onboarding contract (the three first-run choices, the `provision-host-config: false` default, the credential kinds, `config harden`); `learn-mode.md` — the learn-mode hub, with subspecs `learn-mode/observation-channel.md`, `learn-mode/home-read-back.md`, and `learn-mode/findings-and-apply.md`.
  - Read If: Working on `internal/firstrun` (the `Offer`/`Prompt` menu, `BuildFacts`, the UC4 `SeedFromHost`) or its `RootState` decision record, `provision-host-config`, `config harden`, first-run guidance, credential/auth handling, the doctor output/troubleshooting anchors, or the Get started docs; or on learn mode — the after-run analyser, a `run --learn`/`config learn` surface, a learned network policy or secret proposal, the observation channel (destinations, attribution, the terminating proxy), the home read-back or its manifest, or the finding and applier contract.

#### `specs/release/` - Release branching, assets, self-upgrade, darwin cross-build

- `specs/release/`
  - Description: Release contracts: `darwin-cross-compile.md` (the zig prerequisite and macOS linker stubs), `release-asset-integrity.md` (checksum verification before self-update), `release-branching.md` (the `release/<major>.<minor>` branches), `self-upgrade-channels.md` (the `upgrade.channel` design).
  - Read If: The macOS/darwin cross-compile path, `make bootstrap`, or the zig stub setup; the self-update download/verify/replace path or the release checksums/asset publishing; where a release tag or a fix lands, cutting a patch release, or backporting; `upgrade.channel`, canary resolution, the `--channel` switch, or the release CI's prerelease classification.

#### `specs/resources/` - Resource-limit detection and proposal

- `specs/resources/`
  - Description: `resource-limit-proposal.md` — detecting a project-VM memory-OOM or full-disk limit hit and prompting a raise, with the host terminal recovered automatically.
  - Read If: Working on the OOM/disk limit-hit detector or raise proposal, the `pressure`/`resourceproposal` packages, the `msb.Sandbox.Metrics`/`Logs` seam, persistence of `memory`/`disk-size`/`workspace-quota`/`tmp-size`, the live root-disk grow, or the automatic terminal reset after an attach.

#### `specs/runner-image/` - Runner image build, composition, PATH, CA trust

- `specs/runner-image/`
  - Description: Runner-image contracts: `buildkit-build.md` (BuildKit builder selection, the classic fallback, and the build-cache surface), `ca-trust-helper.md` (the `msb-trust` helper), `image-build.md` (build/caching and the msb cache), `image-composition.md` (multistage Dockerfile composition and tool-tree ownership), `login-shell-path.md` (PATH in login shells).
  - Read If: The BuildKit build/selection, `DialHijack`, `image-cache-from`/`image-cache-to`, or the classic fallback; TLS interception, secret-substitution failures in an app (Java/Gradle, Chrome), or the `msb-trust` helper; image build/caching, `BuildOptions.Force`/`--rebuild`, `image.EnsureImage`/`Build`/`EnsureLoaded`/`ImageInfo`, or `naming.RunnerImageRef`/`ParseImageTag`; `RenderDockerfile`/stage composition, layer caching, agent install methods, `COPY --chown`, the `dind`/`docker` switch, or custom-base handling; or runner-image PATH/login-shell (`-l`) handling.

#### `specs/state/` - Persisted state model and upgrade/provenance state

- `specs/state/`
  - Description: State contracts: `state-model.md` (the root `state.yaml` document, locked transactions, locks, `atomicwrite`) and `upgrade-state.md` (the agent upgrade-check and provenance state).
  - Read If: Adding or changing launcher-global persisted state or the `state.yaml` shape; any state read-modify-write, the locking primitives, or recovery; `internal/atomicwrite`; the upgrade prompt or `pendingUpgrade`/`resolveBuildVersion`/`recoverAgentVersion`/`recordImageProvenance`, the `agent-version` label, `--agent-version`, or the `updater.yaml` migration.

#### `specs/testing/` - Test coverage, isolation, and harness smoke tests

- `specs/testing/`
  - Description: Test contracts: `test-coverage.md` (the coverage bar and codecov thresholds), `test-isolation.md` (keeping tests off real host state and the hermetic envelope), `harness-smoke-tests.md` (the `msb`-tagged harness smoke tests), `msb-journeys.md` (the second `msb` suite that drives full journeys through real harnesses, the shared `test/msb/driver`, the `test/msb/startup` rename, and the surface-sweep promotion flow), `release-surface-testing.md` (the manual release-surface sweep: journeys, isolation, execution strategy, findings), `shell-script-harnesses.md` (the shell harnesses' host-git-config isolation and the shared Go harness helpers).
  - Read If: Deciding what to test or triaging a failing `codecov/patch`/`codecov/project`; adding a test package/`TestMain`/double, debugging a test that reads real host state or fails under `-count>1`, the `//testenv:allow` ambient gate, or a state/config write to an unresolved root; the harness smoke tests, `make test-msb`/`make test-msb-cold`, `AGENTS_SANDBOX_MSB_PARALLEL`/`AGENTS_SANDBOX_SMOKE_ROOT`, or a harness TUI's startup/quit behavior; the harness journeys suite (`test/msb/journeys`, `make test-msb-journeys`), the shared `test/msb/driver`, the `test/msb/startup` rename, or the surface-sweep promotion flow; a Go `test/scripts` harness (or the exec-git porcelain helper it drives a real-git fixture through) that prompts for an ssh passphrase or fails with `failed to write commit object`, the `RunScript`/`gittest`/`fakecmd` helpers, or the `test/scripts` Go harness package; or verifying a release before tagging, running the surface sweep, adding a surface journey, or building its isolation helper.

#### `specs/vm/` - VM lifecycle and per-VM behaviours

- `specs/vm/`
  - Description: Per-VM contracts: `lifecycle.md` (the canonical run/shell stage model), `detached-jobs.md` (dockerd/agent-serve as runtime-owned jobs), `idle-timeout.md`, `live-mount-readback.md`, `reconfig-decision.md`, `session-reaper.md`, `startup-hooks.md` (the hook terminal-interactivity predicate and the streaming-exec requirement), `workspace-bind-permissions.md`.
  - Read If: Any change to a run/shell/session lifecycle stage or cross-stage value (`cmd.runFunc`, `prepare.PrepareSandbox`, `session.Run`/`Shell`, the `--dry-run*`/`--serve-only` modes); VM dockerd/agent-serve startup, readiness, or restart, or the idle timeout; reading a live VM's mounts or ports (an empty `cfg.Volumes`/`mounts (0)`); the reconfig decision (`readLiveProjectVM`/`resolveReconfigDecision`/`PlanReconfig`) or `ensure-vm.lock`; the reaper's `waitQuiescent`/`sessionPoll`/`ReapPolicy`; a startup hook's interactivity or the `HookInteractive` seam; or the `/workspace` bind permission policy.

#### `specs/volumes/` - Home volume recovery/decoupling and volume commands

- `specs/volumes/`
  - Description: Volume contracts: `home-volume-recovery.md` (reuse the live VM's volume when state is lost), `runtime-home-decoupling.md` (the private `MSB_HOME` and legacy-volume migration), `runtime-management-commands.md` (the volume/image/sandbox inspection/removal/compaction surface).
  - Read If: Home-volume resolution (`resolveHomeVolumePlan`/`ResolveHomeVolume`/`liveHomeVolume`) or diagnosing a duplicate/orphaned volume after state loss; the private `MSB_HOME`, the legacy home-volume copy prompt/migration state, the post-migration notice, or `volume remove`; or volume/image/sandbox inspection, removal, disk compaction, or the `df`/`storage` accounting commands.

#### `specs/worktrees/` - The `/workspace` bind rule

- `specs/worktrees/`
  - Description: `worktree-layout.md` — the `/workspace` bind-source rule (bind the smallest enclosing checkout), guest-CWD derivation, and the outside-worktree case.
  - Read If: Changing the `/workspace` bind-source resolution, guest-CWD derivation, the worktree mount rule, or the outside-worktree shared-git-dir mount; or diagnosing a session that hides the worktree it was launched from, a `git` command that cannot resolve inside the VM, or the `--worktree` host git-dir side effect.

### `plans/` - TDD implementation plans

- `plans/2026-11-07-hermetic-test-suite-design.md`
  - Description: Design for making the default `go test` suite hermetic: the host-figure sentinel pass, the wall-clock and ambient-identity rules, and the release-to-default cadence.
  - Read If: Working on test decoupling from the execution environment, the host-figure sentinel pass, or the hermetic suite's cadence (the landed envelope contract is in `specs/testing/test-isolation.md`).
- `plans/2026-11-07-hermetic-inventory.md`
  - Description: First-run envelope record: the green scrubbed run and the six uid/permission tests the strict run fails as root.
  - Read If: Picking up a hermetic surface fix, choosing the next hermetic task, or checking the envelope's measured runtime against `make test`.
- `plans/2026-11-07-hermetic-uid-permission-surface.md`
  - Description: Plan 2 of the hermetic design: remove the effective-uid dependence from the six tests the strict envelope runs as uid 0, and make `make test-hermetic` green.
  - Read If: A red `make test-hermetic` run under uid 0, the six uid/permission-dependent tests, or adding a read/write failure seam to `configmerge`, `runtimemigration`, or `state`.
- `plans/2026-11-15-learn-mode-spike.md`
  - Description: Validation spike for learn mode: the host-run experiments behind the observation channel, with S1's per-harness home read-back still open (opencode2 only). Harness: `spike/learn-mode/`.
  - Read If: Picking up the unrun per-harness read-back (S1), rerunning an experiment, or checking an unverified observation-channel assumption (proxy reachability under `none`, egress bypass, denial logging, DNS coverage, per-harness credential layout).
