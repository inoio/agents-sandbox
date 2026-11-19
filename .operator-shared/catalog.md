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
  - Description: Decision to adopt `oneyaml.dev/go` for the config write path (`viperconfig.WriteSetting`), to serialize on `go.yaml.in/yaml/v4` for every path we own (viper keeps its read-only `go.yaml.in/yaml/v3`), and to cap untrusted config/secret/snippet reads at 16 MiB (`internal/configread`); `goccy/go-yaml` rejected, the owner scan and JSON5 depth recorded.
  - Read If: Changing `viperconfig.WriteSetting`, the YAML edit path, or the `configread` bound; evaluating oneyaml vs goccy; touching `go.mod`'s YAML dependency or the `yaml/v3`↔`v4` choice; or triaging a `FuzzParseState` serialization-stability failure.
- `0002-rootless-docker-engine.md`
  - Description: Decision to run the agent's Docker engine rootless as `dev` instead of granting the root-equivalent `docker` group; records the spike evidence, the fail-the-build prerequisite policy, the data root on the VM root disk, the `/var/run/docker.sock` symlink, and the `/dev/fuse`/`/dev/net/tun` permission fix.
  - Read If: Changing how the in-VM Docker engine is installed, started, or reached (`dockerAdoptionBlock`, `vm/dockerd.go`), its prerequisites, or evaluating podman/a daemonless builder/an authz proxy instead.
- `adr/TEMPLATE.md`
  - Description: Template for an ADR in this directory (Context / Options / Decision / Consequences).
  - Read If: Writing a new ADR.

### `guides/` - How-to and reference notes for this partition

- `guides/agent-link-opening.md`
  - Description: What each agent shells out to when it opens a link on Linux (opencode `xdg-open`, opencode2's `open` package, pi's `openBrowser`, claude-code's scheme allowlist), the OSC 8 wrinkle, and the network-free VM→host transport for a host-side opener (#273).
  - Read If: Working on opening links/URLs on the host from inside the VM (#273), an in-VM `xdg-open` shim, agent opener behavior, or any VM→host action channel.
- `guides/branch-worktree-cleanup.md`
  - Description: How `scripts/cleanup-merged-branches.sh` (via `make cleanup-branches`) removes merged worktrees/branches and reports the rest; the three merge-detection tiers, the fresh-session worktree guard, the config-mirror protection, the named plan printed before the gate, dry-run skipping the remote refresh, the `--force-with-lease` origin deletion, and the host/VM worktree safety rules.
  - Read If: Running or changing local repository maintenance, cleaning up worktrees or merged branches, or debugging a branch the sweep kept or removed.
- `guides/buildkit-engine-compatibility.md`
  - Description: Which engines agents-sandbox claims would survive switching the runner-image build to BuildKit, per mechanism (moby `/build?version=2` with no session vs a real BuildKit client via `/grpc`+`/session`): the dockerd family survives with a registry-resolution/auth caveat, podman ignores `version` (Buildah, not BuildKit) under mechanism 1 and breaks under mechanism 2, containerd modes stay unsupported; plus the render's feature-safety and user BuildKit-only syntax.
  - Read If: Considering support for another Docker-API engine (or a builder-selection/fallback change) by evaluating which engines survive each BuildKit mechanism, or diagnosing a runner-image build that fails on a non-Docker engine.
- `guides/dead-code-detection-go.md`
  - Description: Using x/tools `deadcode` to find production code only tests keep alive: why staticcheck U1000 cannot, the default vs `-test` modes, the test-support paths to filter, and `-whylive` for seam-vs-accident triage.
  - Read If: Looking for dead or unreferenced production code, auditing whether a seam is still wired, or evaluating a dead-code linter; for the gate, the pin, or the `//deadcode:ignore` marker, read `specs/ci/quality-gates.md`.
- `guides/dev-environment.md`
  - Description: The development VM's installed CLI tools, its `GOCACHE`-on-`/var/tmp` build-cache placement (the virtiofs home volume is ~10-35x slower on small files), and its standing environment limitations - remote git over SSH (HTTPS works), CI `pull_request` workflows absent on a conflicted PR, TLS-interception self-signed-cert docker builds, and the group-writable-ancestor storage-root refusal on the launcher's private runtime home (umask 002).
  - Read If: Choosing a CLI tool for a task, or hitting an environment limitation such as an unusable SSH remote, an absent CI run on a conflicted PR, a self-signed-cert docker build failure, or a "catalog ancestor ... permits path substitution" error from the launcher's runtime home; or questioning why `GOCACHE` is not on the home volume.
- `guides/github-issue-workflow.md`
  - Description: How this repo tracks work on GitHub with the `gh` CLI: the issue CRUD commands, the triage label vocabulary (`bug`/`enhancement`/`chore`/`documentation`, the `needs-triage`/`needs-info`/`ready-for-agent`/`ready-for-human`/`wontfix` states, the `upstream` marker and its exemption from the state role, the orthogonal `priority:*` set), and that external PRs are not a request surface.
  - Read If: Creating, reading, labeling, or closing a GitHub issue; resolving the label vocabulary the `maintain` skill uses; or working through a skill that publishes to or reads from the issue tracker.
- `guides/github-merge-driver-support.md`
  - Description: Verdict, with primary sources, that GitHub's server-side merge and merge-queue build ignore `.gitattributes` merge drivers, so `CHANGELOG.md merge=union` does not resolve a conflict; libgit2 does implement `union` but GitHub does not consult it, and GitLab dropped it in 17.1.
  - Read If: Considering `merge=union`, a custom merge driver, or any `.gitattributes` merge attribute to auto-resolve conflicts on GitHub; or explaining why a server-side conflict persists when the CLI git merges cleanly.
- `guides/github-pr-workflow.md`
  - Description: The PR mechanics the issue guide does not cover: landing through the `main` merge queue (`gh pr merge` is refused; enqueue via the `enqueuePullRequest` GraphQL mutation or "Merge when ready", or `scripts/enqueue-pr.sh`), the `GITHUB_TOKEN`-authored bot PR parked at `action_required` until a maintainer approves its run, and guessing a changelog fragment's PR-number slug.
  - Read If: Opening, merging, or troubleshooting a PR; enqueueing one into the merge queue; a bot PR that reports no checks; or naming a `changelog.d/` fragment before the PR number is known.
- `guides/go-version-support.md`
  - Description: The module's minimum Go version (`go 1.27.0`, set by our own use of `strings.CutLast` and promoted struct-literal keys, above the 1.26.8 dependency floor) and why it cannot be lower; the evidence and the primary sources.
  - Read If: Changing the `go` directive, `.go-version`, or a Go pin; asking whether a lower Go version can be advertised; or diagnosing a dependency/promoted-field/GOTOOLCHAIN "requires a newer Go" error.
- `guides/mutation-testing.md`
  - Description: Mutation testing for Go in this repo - the tool survey and why gomutants was chosen, how to close gomutants survivors (recurring mutant types, the equivalent-mutant traps, the record-with-a-reason fallback), and the multi-turn sweep runbook for the #268 survivor tracker.
  - Read If: Evaluating or changing a Go mutation-testing tool or the `make mutation` target; triaging LIVED mutants from a sweep; picking a `SINCE` scope for a branch/PR sweep; or coordinating a #268 survivor-reduction pass.
- `guides/operator-brain-maintenance.md`
  - Description: Conventions for keeping this brain small and findable: one-line catalog Descriptions, anchor-first `Read If` lines, the `specs/<subsystem>/` layout, drop-not-point for facts covered by a skill or merged spec, retiring a plan once its work lands, and the structural gate `make brain-check` enforces.
  - Read If: Adding or editing a catalog entry, spec, plan, guide, or skill; writing a new freeform document; curating/retiring the brain; or a red `make brain-check` run.
- `guides/podman-engine-testing.md`
  - Description: How the alternative-engine claim is verified: the `integration-podman` CI job runs the docker-build suite against a rootless podman socket, podman's unqualified-image-name refusal is the hazard the runner image avoids by qualifying its base, and the dev VM cannot run an engine build at all.
  - Read If: Verifying podman (or another Docker-API engine) against the integration suite, or investigating a build failure naming an unqualified image.
- `guides/secret-placeholder-transcript-trap.md`
  - Description: Why a session dies once a microsandbox secret placeholder reaches the model context - the LLM transport POSTs the whole conversation, so a placeholder anywhere in history blocks every later turn; how to observe the block in `runtime.log`; and the launcher's config-file-only `secrets.common.allow-placeholder-for` repair.
  - Read If: Diagnosing a model turn blocked by a secret violation, working on `secrets.common.allow-placeholder-for`/`BuildSecretsFromSpecs`/`placeholderHostsFor`, or envsecret passthrough hosts.
- `guides/test-runtime-cost.md`
  - Description: Where `go test ./...` wall time goes (test-binary linking, not test bodies), how `-tags microsandbox_ffi_path` drops the SDK's ~36 MB embedded native library, the test-body hotspots, and the polling/timeout seams that set failure latency.
  - Read If: Speeding up `make test`, diagnosing a slow or hanging test run, adding a polling/wait seam or a `TestMain`, adding a real-SDK test, or deciding whether a test may consume a production timeout budget.
- `guides/vm-oom-crash-resilience.md`
  - Description: How the microsandbox microVM enforces memory/disk/CPU limits (virtio-mem plug cap, no swap/zram, guest global OOM killer), why an OOM kill is invisible from the launcher, the host-terminal corruption and recovery, the `Metrics`/`LogSourceSystem` detection surface, the async live memory resize, and the ranked detect-and-propose mitigation path.
  - Read If: Working on VM OOM/crash handling, a memory/disk/CPU limit-hit detector or "raise the limit" proposal, terminal corruption after an in-VM kill, or the daemon/dockerd re-ensure and supervision behaviour.

### `specs/` - System contracts for this partition

#### `specs/agents/` - Agent profiles and their daemons

- `specs/agents/`
  - Description: Agent-profile contracts: `hermes-agent.md` (the Hermes profile, its installer/tool-store, `HERMES_HOME`), `notify.md` (the desktop-notify event watcher on opencode/opencode2: the decode seam, the subtree-aware done model, shells, titles, and the `agents-sandbox:` messages), `opencode2.md` (the opencode2 profile: config namespace, basic auth, credential store, v2 worktree API).
  - Read If: Adding or changing an agent profile (the `hermes` install/`OwnsToolTree`, or the opencode2 config-namespace fork / `ConfigDirAliaser` / `OPENCODE_SERVER_PASSWORD` / `DaemonHealthCmd`); the notify decode seam (`agent.EventStreamSpec`/`Parse`/`SessionListSpec`/`SessionRef`), `parentID` subagent filtering, the subtree done reasons (`DoneReasons`/`SubtreeDoneStates`/`notify.on-done-subagents-running`), or the message format; or diagnosing a dead-looking opencode2 daemon, an opencode2 provider without a connection, a `--worktree` that 404s, "notify does nothing on opencode2", a done firing while a subagent runs, or a Hermes launch that fails before its REPL.

#### `specs/architecture/` - Cross-cutting package seams and run orchestration

- `specs/architecture/`
  - Description: Cross-cutting contracts: `client-injection.md` (resolve external clients once and thread them), `dry-run-preview.md` (`--dry-run` makes no changes), `msb-seam.md` (the `internal/sandbox/msb` SDK seam), `package-boundaries.md` (`cliout`/`view` and the run layering), `termio-ui.md` (termio's UI seam).
  - Read If: Changing a `docker.Get()`/`configpaths.Get()` call site or threading a client/path resolver; `--dry-run`/`--dry-run-vm` or the `options.Mode` boundary; the `msb` role interfaces/composites/mock or how a consumer depends on the seam; moving a Printer/report DTO between `cliout` and `view` or run-preparation code between `prepare` and `vm`; a termio role interface, `termio.UI`/`New`, prompts/spinners, or non-TTY behavior.

#### `specs/ci/` - CI pipeline and quality gates

- `specs/ci/`
  - Description: CI and gate contracts: `ci-pipeline.md` (workflows, required checks, merge queue, changelog fragments, the integration build cache, the `changes` gate's four exempt-path copies and the `actionlint` workflow lint), `mutation-testing.md` (the on-demand `make mutation` practice), `quality-gates.md` (golangci-lint, dead-code, doc-link, `actionlint`, the `make shellcheck` shell-script gate, and the `ci/check-ci-guards.sh` guard-consistency gate).
  - Read If: Changing `.github/workflows/*.yml`, CI triggers, the merge queue/`merge_group`, the required-check set or `protect main` ruleset, the `changes` gate, the `run-integration*` labels or the integration jobs' `GH_TOKEN`, or changelog authoring; the `make mutation` target/`GOMUTANTS_VERSION`/triage convention or the advisory `mutation` workflow; `.golangci.yml`, the `lint` build-tag set, the dead-code gate (`make deadcode`/`//deadcode:ignore`), the doc-link gate, the `actionlint` pin, the `make shellcheck` gate, or the guard-consistency checker (`ci/check-ci-guards.sh`, the exempt-path lists, the `github.event_name`/`on.pull_request.types` invariants).

#### `specs/configuration/` - Config scopes, keys, decoding, validation, project-config root and trust

- `specs/configuration/`
  - Description: Config contracts: `config-file-decoding.md` (the `configdecode`/`configread` core), `config-keys.md` (the key registry), `config-scopes.md` (path resolution and scope order), `config-value-validation.md` (VM size/duration bounds), `project-config-root.md` (the bare-checkout root), `project-config-trust.md` (the approve-on-change gate).
  - Read If: Config-file reading/classification/JSON5, a config key's env var or CLI flag, config-scope order or `configpaths.ConfigPaths`/`ConfigScopes`, VM size/duration validation or the tmp-vs-memory relation, the bare-layout project-config root/`core.bare`, or the project-config trust gate (`config harden`, provenance); or diagnosing a file one loader accepts and another rejects, a silently ignored env var, a rejected size, or a run that refuses to start because `.agents-sandbox` changed.

#### `specs/conventions/` - Repo-wide conventions

- `specs/conventions/`
  - Description: `deprecation-policy.md` — how a flag/config key/env var/command is deprecated, how long it survives, how it is surfaced, and the current deprecation register with replacement/removal targets.
  - Read If: Deprecating or removing a CLI flag, config key, env var, or command; deciding whether a change needs a shim or a clean break; or enforcing the deprecation register in tests.

#### `specs/identity/` - Project, artifact, and sandbox identity and naming

- `specs/identity/`
  - Description: Identity contracts: `artifact-naming.md` (prefix-free sandbox/volume/image names; Docker keeps the prefix), `checkout-identity.md` (the repo slug vs the checkout slug), `sandbox-identity.md` (a sandbox's mutable name vs its stable identity).
  - Read If: Changing `internal/sandbox/naming` or the name/reference constructors/parsers, or artifact discovery; the repo-vs-checkout slug derivation, the runner-image tag identity, the `config paths` command, or the checkout-identity migration; the msb sandbox create/remove/attach paths, `EnsureProjectVM`, prune, the reaper, or reconfig-replace; or diagnosing a legacy artifact that stopped being found, `ErrSandboxReplaced`, or two checkouts sharing one VM.

#### `specs/network-and-secrets/` - Network profiles and secret scoping

- `specs/network-and-secrets/`
  - Description: `network-profiles.md` (composable public/private/host profiles and fingerprinting), `secret-header-scoping.md` (scoping placeholder substitution to named request-header fields via `header_fields`), and `interception-and-proxy.md` (TLS-interception control via `network.intercept-tls`/`network.tls-bypass`/`network.disable-strict`, and the outbound proxy with its egress-policy composition rule).
  - Read If: Changing `network.Policy`, profile parsing/validation, the `network.profile`/`--network`/`AGENTS_SANDBOX_NETWORK_PROFILE` surface, or network fingerprinting; per-secret header substitution, `envsecret.SecretSpec`/`BuildSecretsFromSpecs`, the `header_fields` docs, or `state.SecretsContentHash`; or the `network.intercept-tls`/`network.tls-bypass`/`network.disable-strict`/`network.proxy.*` surfaces.

#### `specs/onboarding/` - First-run guidance and config migration

- `specs/onboarding/`
  - Description: `onboarding.md` — guiding a fresh install to a working sandbox: safe config migration, the tiered `config wizard`, `config harden`, and the doctor-remediation + Get started docs flow; `learn-mode.md` — the hub for learn mode (learning the configuration from a real session instead of declaring it: intent, user-facing vocabulary, the four journeys, the invariants, and the phasing), with subspecs `learn-mode/observation-channel.md` (destinations and credential attribution, the approaches, topology, capture artefact, CA trust), `learn-mode/home-read-back.md` (the config and secret facts, per-harness layouts, the baseline manifest), and `learn-mode/findings-and-apply.md` (the finding shape, scopes, apply, `config harden`, after-run ownership).
  - Read If: Working on `config migrate`/`config wizard`/`config harden`, first-run guidance, credential/auth handling, network egress derivation, the `Guide` outcomes, the doctor output/troubleshooting anchors, or the Get started docs; or on learn mode — the after-run analyser, a `run --learn`/`config learn` surface, a learned network policy or secret proposal, the observation channel (destinations, attribution, the terminating proxy), the home read-back or its manifest, or the finding and applier contract.

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
  - Description: Test contracts: `test-coverage.md` (the coverage bar and codecov thresholds), `test-isolation.md` (keeping tests off real host state), `harness-smoke-tests.md` (the `msb`-tagged harness smoke tests), `release-surface-testing.md` (the manual release-surface sweep: journeys, isolation, execution strategy, findings), `shell-script-harnesses.md` (the shell harnesses' host-git-config isolation and the shared Go harness helpers).
  - Read If: Deciding what to test or triaging a failing `codecov/patch`/`codecov/project`; adding a test package/`TestMain`/double, debugging a test that reads real host state or fails under `-count>1`, the `//testenv:allow` ambient gate, or a state/config write to an unresolved root; the harness smoke tests, `make test-msb`/`make test-msb-cold`, `AGENTS_SANDBOX_MSB_PARALLEL`/`AGENTS_SANDBOX_SMOKE_ROOT`, or a harness TUI's startup/quit behavior; a Go `test/scripts` harness (or the exec-git porcelain helper it drives a real-git fixture through) that prompts for an ssh passphrase or fails with `failed to write commit object`, the `RunScript`/`gittest`/`fakecmd` helpers, or the `test/scripts` Go harness package; or verifying a release before tagging, running the surface sweep, adding a surface journey, or building its isolation helper.

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
  - Description: Design for making the default `go test` suite hermetic: the acceptance envelope (module copy outside any checkout + scrubbed env + network namespace), the host-figure sentinel pass on injected seams, the wall-clock rule, and the release-to-default cadence.
  - Read If: Working on test decoupling from the execution environment, the hermetic envelope targets, the host-figure sentinel pass, or the hermetic suite's cadence.
- `plans/2026-11-07-hermetic-inventory.md`
  - Description: First-run envelope record: the runtime budget (envelope ~1.5× an uncached `go test`, ~4.5× a cached `make test`), the green scrubbed run, and the one failing surface (6 uid/permission `chmod`-based tests the strict run runs as root).
  - Read If: Picking up a hermetic surface fix, choosing the next hermetic plan's tasks, or checking the envelope's measured runtime against `make test`.
- `plans/2026-11-07-hermetic-uid-permission-surface.md`
  - Description: Plan 2 of the hermetic design: remove the effective-uid dependence from the six tests the strict envelope runs as uid 0 (via `configmerge.readFile`, `runtimemigration.readDir`, and a `state.OverrideStateWriteForTest` hook), pin `GOTOOLCHAIN=local`, and verify `make test-hermetic` is green.
  - Read If: A red `make test-hermetic` run under uid 0, the six uid/permission-dependent tests, or adding a read/write failure seam to `configmerge`, `runtimemigration`, or `state`.
- `plans/2026-11-15-learn-mode-spike.md`
  - Description: Validation spike for learn mode - experiments deciding whether the launcher can observe what a session actually used (home-volume read-back per harness, host DNS resolver under each profile, outbound-proxy reachability and completeness, denial observability, TLS-termination prerequisites, streaming cost). Run 2026-11-15 on the host; S1 is still partial (opencode2 only). The host-runnable harness is `spike/learn-mode/`.
  - Read If: Picking up the unrun part of the learn-mode spike (S1's per-harness home read-back), rerunning an experiment, or checking an unverified observation-channel assumption (proxy reachability under `none`, egress bypass, denial logging, DNS coverage, per-harness credential layout).
- `plans/2026-11-16-notify-done-states.md`
  - Description: TDD plan for the subtree-aware done notification on opencode2 - the pending reasons (`subagents`, `commands`), the new `on-done-*` config keys, the agent decode fields (`title`, shell state, `SessionRef`), the reconnect reset, and the `agents-sandbox:` message format.
  - Read If: A task in `internal/notify`/`internal/agent`/`internal/viperconfig` for the notify change; the symbols `DoneReasons`, `SubtreeDoneStates`, `ResetTransient`, `SessionRef`, or the `on-done-*` keys; or a session where a done fires while a subagent runs, a blocked subagent never alarms, or a done stays silent after a reconnect.
- `plans/2026-11-17-learn-mode-program.md`
  - Description: Program index for learn mode (#697): the two-track DAG (channel and learn), the frozen cross-subplan contracts (`InterceptMode`, `Proxy` and its `msb` projection, and the S5/S6 placeholders), shared constraints and enablers (E1 sqlite, E2 #119, E3 #252, E4 microsandbox#2080, the deferred #54/#99/#118 retirement), and the per-subplan rollout. Not executable.
  - Read If: Orchestrating learn mode; picking the next subplan; or checking a cross-subplan interface before writing a dependent subplan.
- `plans/2026-11-17-learn-mode-settled-design.md`
  - Description: Docs subplan (S0): land the settled learn-mode contract from `origin/spike/learn-mode` and split `learn-mode.md` into a hub plus `learn-mode/{observation-channel,home-read-back,findings-and-apply}.md` and a new `network-and-secrets/interception-and-proxy.md`, with the section-to-destination mapping.
  - Read If: Executing S0; or deciding where a learn-mode contract sentence belongs (hub vs subspec).
- `plans/2026-11-17-learn-mode-sdk-semantics.md`
  - Description: Host-gated spike subplan (S1) settling the four channel questions - topology T1/T2, `intercept-tls: off` semantics, the proxy-reachability composition rule, and the #151 strict-hostname bypass - with the throwaway probe and the decision-record steps.
  - Read If: Running or revising the SDK channel-semantics spike; or implementing S2's `off`/`tls-bypass` contract or S3's composition guard.
- `plans/2026-11-17-learn-mode-tls-interception.md`
  - Description: SDD subplan for #151: `network.InterceptMode`/`TLSBypass`/`DisableStrict`, the `msb` projection to `NetworkConfig.TLS`, the `network.intercept-tls`/`network.tls-bypass`/`network.disable-strict` keys, the recreate pin, the docker CA recipe, a human accept step, and docs.
  - Read If: You are executing S2; or an in-VM `docker build` breaks behind the interception CA, or the interception fingerprint/recreate is suspect.
- `plans/2026-11-17-learn-mode-outbound-proxy.md`
  - Description: SDD subplan for #149: `network.ProxyProtocol`/`Proxy` and the composition guard, the `msb` `WithProxy` projection with the `Proxy` classification move, the `network.proxy.*`/`--proxy` surface with SOCKS4 credential fail-fast, reachability verification, and docs.
  - Read If: You are executing S3; or a corporate egress proxy is being set up, or egress under `none` is denied unexpectedly.
- `plans/2026-11-17-learn-mode-readback.md`
  - Description: SDD subplan (S5) for the passive read-back: `HomeVolumePath`, the `Manifest`/`ManifestEntry` baseline, per-agent `ReadBackDescriber` paths with static-vs-login classification, the opencode2 SQLite `credential` probe (E1 `modernc.org/sqlite`), and the host verification.
  - Read If: You are executing S5; or you need to know how a finished sandbox session's credentials and config are discovered (the analyser's fact source), or where a new harness's read-back paths belong.
- `plans/2026-11-18-concise-check-output.md`
  - Description: TDD plan for a concise `make check` - `scripts/check.sh` captures each target, prints one PASS/FAIL line per target, and dumps failing logs last; heavy targets serial / light parallel; `VERBOSE`/`SERIAL`; plus quieting the fixture harnesses' per-case lines.
  - Read If: Executing the concise-check-output change; or working on `scripts/check.sh`, the `CHECK_HEAVY_TARGETS` split, the `check`/`verify` targets, the `VERBOSE`/`SERIAL` flags, or a fixture harness's `ok()`/section output.
- `plans/2026-11-17-learn-mode-observation-channel.md`
  - Description: SDD subplan (S4) for the channel *library*: the `elazarl/goproxy` terminating proxy and per-session CA, the `TLSConfig.UpstreamCACerts` projection (T2, the runtime trusts our CA), the in-memory `network.Policy` overlay value, the scrubbed `Observation` artefact and its store, and the `Observer` facade. S7 owns the invocation (start before prepare, teardown collect).
  - Read If: You are executing S4; or working on the learn-mode proxy, its CA trust, the observation overlay or its fingerprint, the capture artefact or its scrubbing, or why a learning session recreated the VM.
- `plans/2026-11-17-learn-mode-analyser.md`
  - Description: SDD subplan (S6) for the finding model and applier: `analyser.{Finding,Evidence,Write,Scope}` with per-write scope and a removal form (`Finding.Writes`), the observation/readback findings and the never-widen diff, the `env.secret.yaml` writer/remover, `viperconfig.RemoveSetting`, the slug-keyed decline memory (a `RootState` section), and the interactive applier (per-slug default, project scope via the trust gate, project-owned-key skip); it also fills `readback.Credential.Hosts` and adds `readback.Credential.EnvVar`. The config-only (harden) finder is S8's. Its declarations carry temporary `//deadcode:ignore` markers until S7 wires `run --learn`.
  - Read If: You are executing S6; or working on the learn-mode proposal/findings, the per-slug apply, the decline memory, harden's scoped write/removal primitives, or `config harden`'s engine.
- `plans/2026-11-17-learn-mode-run.md`
  - Description: SDD subplan (S7) for the invocation: the `--learn` flag and `RunOptions.Learn`, `state.LearnSession` (a `HomeState` schema bump), `learn.{Begin,Overlay,CaptureBaseline,Analyse,Finish}` wired around `prepareSandbox`/`runAttach` (channel overlay before prepare, the session record, the after-run), the exit prompt (Review and save / Save all / Not now via `ApplyOptions.AcceptAll`), `config learn`, the artefact save/delete/30-day prune, and the deletion of the S4/S5/S6 dead-code markers.
  - Read If: You are executing S7; or working on the `run --learn` / `config learn` invocation, the session record, the channel overlay or its recreate, the after-run or its exit prompt, or the S4/S5/S6 dead-code markers.
- `plans/2026-11-17-config-harden.md`
  - Description: SDD subplan (S8) for `config harden`: the config-only finder `analyser.ConfigFindings` (a project secret moved to the per-slug scope, an over-broad secret scope or egress profile, a wildcard egress entry, a secret-file mode, host-config provisioning), each write scoped to the key's owner, the `config harden` command, and the `analyser.FileModeWrite` the applier gains.
  - Read If: You are executing S8; or working on `config harden`, the config-only findings, the owner-scope rule, or the file-mode write.
- `plans/2026-11-17-agent-config-writeback.md`
  - Description: SDD subplan (S10) for the agent-config half of the proposal: `envsecret.PlaceholderFor` and `SecretSpec.Placeholder`, the `ManagedConfig` write and `ApplyOptions.Agent`, `analyser.ManagedConfigFindings` (the changed agent config files sanitized of static secret values), and the after-run wiring; depends on S7.
  - Read If: You are executing S10; or working on copying a session's agent config back into managed config, the `${ENVVAR}` placeholder convention, or excluding a credential store from the write-back.
- `plans/2026-11-19-shell-harness-go-migration.md`
  - Description: Migration plan for moving the git-fixture shell harnesses to a Go `test/scripts` package: the shared helpers (`GitIsolationEnvironment` in `testutil`; `RunScript`, `gittest`, and `fakecmd` as `*_helpers_test.go` in `test/scripts`), the pilot (`open-backport-pr`), the per-harness waves, and the Makefile/`changed-targets.sh` rewiring, with `rebase-status` and `cleanup-merged-branches` ported through one shared exec-git porcelain helper.
  - Read If: Executing the shell-harness-to-Go migration; adding or rewiring a `scripts/test-*.sh`→`test/scripts` harness; the `RunScript`/`gittest`/`fakecmd` helper surface; or the ambient-`//testenv:allow` question for a harness that shells out to git.
