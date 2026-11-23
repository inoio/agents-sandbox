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
- `guides/kvm-agent-testing.md`
  - Description: Manual host-accept procedure for the KVM-guest agent environment - the ordered checks that `make kvm-agent`, `make kvm-agent-provision`, and `agents-sandbox config provision` behave on a real KVM host.
  - Read If: Accepting the KVM-guest agent work on a host; verifying an agent runs on the guest, gets the launcher's provisioned config, and can build the launcher and run the `msb` suites; or debugging a guest missing opencode2, tmux, or the toolchain.
- `guides/kvm-guest.md`
  - Description: The host-only libvirt KVM guest for running the msb test suites and an agent that works on this repo - why the development VM cannot run them, the `scripts/kvm-guest.sh` provisioner and its `make kvm-up`/`kvm-sync`/`kvm-prime`/`kvm-run`/`kvm-agent`/`kvm-agent-provision`/`kvm-sync-back`/`kvm-down` targets, the host git remote the guest pulls from and pushes to over a `git-shell`-restricted key, the disk-backed reuse root the guest pre-creates, the nested-KVM prerequisites, and the volatile host-firewall fix guest egress needs.
  - Read If: Running the msb suites (`make test-msb`, `make test-msb-journeys`) and needing a KVM host; running an agent (opencode2) on the guest so it can build the launcher and drive real VMs (`make kvm-agent`, `make kvm-agent-provision`) or pushing its branches to the host; provisioning or destroying the libvirt guest; priming the guest's runner-image/msb cache from the host (`make kvm-prime`) or diagnosing a `prime` that copies nothing; diagnosing a guest with no DNS/egress behind Docker's `ip filter` table; the guest's host git remote (`make kvm-sync`, guest `git push`, the `git-shell`-restricted key) or a sync that refuses; or changing `scripts/kvm-guest.sh`.
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
  - Description: How the microVM enforces memory/disk/CPU limits, why an OOM kill is invisible from the launcher, how a sandbox can report no live metrics slot, the detect-and-propose mitigation path, and the host procedure for diagnosing a missing metrics slot.
  - Read If: Working on VM OOM/crash handling, a memory/disk/CPU limit-hit detector or "raise the limit" proposal, terminal corruption after an in-VM kill, a `Metrics` read that never succeeds (`no verified live metrics slot`), or the daemon/dockerd re-ensure and supervision behaviour.

### `specs/` - System contracts for this partition

#### `specs/agents/` - Agent profiles and their daemons

- `specs/agents/`
  - Description: Agent-profile contracts: `hermes-agent.md`, `notify.md`, `opencode2.md`.
  - Read If: Adding or changing an agent profile (the `hermes` install/`OwnsToolTree`, or opencode2's config-namespace fork / `ConfigDirAliaser` / `OPENCODE_SERVER_PASSWORD` / `DaemonHealthCmd`); the notify decode seam (`agent.EventStreamSpec`/`Parse`, `SessionListSpec`/`SessionRef`), `parentID` subagent filtering, the subtree done reasons (`DoneReasons`/`SubtreeDoneStates`, `notify.on-done-subagents-running`); or diagnosing a dead-looking opencode2 daemon, a `--worktree` that 404s, "notify does nothing on opencode2", or a Hermes launch that fails before its REPL.

#### `specs/architecture/` - Cross-cutting package seams and run orchestration

- `specs/architecture/`
  - Description: Cross-cutting contracts: `client-injection.md`, `dry-run-preview.md`, `msb-seam.md`, `package-boundaries.md`, `termio-ui.md`.
  - Read If: Changing a `docker.Get()`/`configpaths.Get()` call site or threading a client/path resolver; the `--dry-run`/`--dry-run-vm` or `options.Mode` boundary; the `msb` role interfaces/composites/mock or how a consumer depends on the seam; moving a Printer/report DTO between `cliout` and `view` or run-preparation code between `prepare` and `vm`; or a termio role interface, `termio.UI`/`New`, prompts/spinners, or non-TTY behavior.

#### `specs/ci/` - CI pipeline and quality gates

- `specs/ci/`
  - Description: CI and gate contracts: `ci-pipeline.md`, `mutation-testing.md`, `quality-gates.md`.
  - Read If: Changing `.github/workflows/*.yml`, CI triggers, the merge queue/`merge_group`, the required-check set or `protect main` ruleset, the `changes` gate, the `run-integration*` labels or the integration jobs' `GH_TOKEN`, or changelog authoring; the `make mutation` target/`GOMUTANTS_VERSION` or the advisory `mutation` workflow; or `.golangci.yml`, the `lint` build-tag set, the dead-code/doc-link/`shellcheck` gates, the `actionlint` pin, or the guard-consistency checker (`ci/check-ci-guards.sh`, the exempt-path lists, the `github.event_name`/`on.pull_request.types` invariants).

#### `specs/configuration/` - Config scopes, keys, decoding, validation, project-config root and trust

- `specs/configuration/`
  - Description: Config contracts: `config-file-decoding.md`, `config-keys.md`, `config-scopes.md`, `config-value-validation.md`, `main-config-ownership.md`, `project-config-root.md`, `project-config-trust.md`.
  - Read If: Config-file reading/classification/JSON5, a config key's env var or CLI flag, config-scope order or `configpaths.ConfigPaths`/`ConfigScopes`, VM size/duration validation, the bare-layout project-config root/`core.bare`, or the project-config trust gate (`config harden`, provenance); the agent's main config in the VM (`reconcileMainConfig`, `configmerge.Reconcile`); or diagnosing a file one loader accepts and another rejects, an in-VM agent setting lost between runs, a silently ignored env var, a rejected size, or a run that refuses to start because `.agents-sandbox` changed.

#### `specs/conventions/` - Repo-wide conventions

- `specs/conventions/`
  - Description: `deprecation-policy.md` — how a flag/config key/env var/command is deprecated, and the register.
  - Read If: Deprecating or removing a CLI flag, config key, env var, or command; deciding whether a change needs a shim or a clean break; or enforcing the deprecation register in tests.

#### `specs/dev-image/` - This repo's dev-VM image composition

- `specs/dev-image/`
  - Description: `composition.md` — the dev-VM image's multistage `.agents-sandbox/Dockerfile` layout and per-stage version pins.
  - Read If: Editing `.agents-sandbox/Dockerfile` or the dev-VM toolchain; bumping a pinned dev tool or adding/removing an apt package; or diagnosing a dev-image rebuild that re-ran more than the changed component.

#### `specs/identity/` - Project, artifact, and sandbox identity and naming

- `specs/identity/`
  - Description: Identity contracts: `artifact-naming.md`, `checkout-identity.md`, `sandbox-identity.md`.
  - Read If: Changing `internal/sandbox/naming` or the name/reference constructors/parsers, or artifact discovery; the repo-vs-checkout slug derivation or the checkout-identity migration; the msb sandbox create/remove/attach paths, `EnsureProjectVM`, prune, the reaper, or reconfig-replace; or diagnosing a legacy artifact that stopped being found, `ErrSandboxReplaced`, or two checkouts sharing one VM.

#### `specs/network-and-secrets/` - Network profiles and secret scoping

- `specs/network-and-secrets/`
  - Description: `network-profiles.md`, `secret-header-scoping.md`, `interception-and-proxy.md`.
  - Read If: Changing `network.Policy`, profile parsing/validation, the `network.profile`/`--network`/`AGENTS_SANDBOX_NETWORK_PROFILE` surface, or network fingerprinting; per-secret header substitution or `envsecret.SecretSpec`/`BuildSecretsFromSpecs`; or the `network.intercept-tls`/`network.tls-bypass`/`network.disable-strict`/`network.proxy.*` surfaces.

#### `specs/onboarding/` - First-run guidance and learn mode

- `specs/onboarding/`
  - Description: `onboarding.md` (the first-run contract) and the learn-mode hub `learn-mode.md` with subspecs `learn-mode/observation-channel.md`, `learn-mode/home-read-back.md`, and `learn-mode/findings-and-apply.md`.
  - Read If: Working on `internal/firstrun` (the `Offer`/`Prompt` menu, `BuildFacts`, the UC4 `SeedFromHost`) or its `RootState` decision record, `provision-host-config`, `config harden`, first-run guidance, credential/auth handling, the doctor output/troubleshooting anchors, or the Get started docs; or on learn mode — the after-run analyser, a `run --learn`/`config learn` surface, a learned network policy or secret proposal, the observation channel, the home read-back, or the finding and applier contract.

#### `specs/release/` - Release branching, assets, self-upgrade, darwin cross-build

- `specs/release/`
  - Description: Release contracts: `darwin-cross-compile.md`, `release-asset-integrity.md`, `release-branching.md`, `self-upgrade-channels.md`.
  - Read If: The macOS/darwin cross-compile path, `make bootstrap`, or the zig stub setup; the self-update download/verify/replace path or the release checksums/asset publishing; where a release tag or a fix lands, cutting a patch release, or backporting; or `upgrade.channel`, canary resolution, the `--channel` switch, or the release CI's prerelease classification.

#### `specs/resources/` - Resource-limit detection and proposal

- `specs/resources/`
  - Description: `resource-limit-proposal.md` — detecting a project-VM memory-OOM or full-disk limit hit and proposing a raise.
  - Read If: Working on the OOM/disk limit-hit detector or raise proposal, the `pressure`/`resourceproposal` packages, the `msb.Sandbox.Metrics`/`Logs` seam, persistence of `memory`/`disk-size`/`workspace-quota`/`tmp-size`, the live root-disk grow, or the automatic terminal reset after an attach.

#### `specs/runner-image/` - Runner image build, composition, PATH, CA trust

- `specs/runner-image/`
  - Description: Runner-image contracts: `buildkit-build.md`, `ca-trust-helper.md`, `image-build.md`, `image-composition.md`, `login-shell-path.md`.
  - Read If: The BuildKit builder selection/classic fallback or `DialHijack`, `image-cache-from`/`image-cache-to`; TLS interception or secret-substitution failures in an app (Java/Gradle, Chrome), or the `msb-trust` helper; image build/caching or `BuildOptions.Force`/`--rebuild`, `image.EnsureImage`/`Build`/`EnsureLoaded`; `RenderDockerfile`/stage composition, layer caching, agent install methods, the `dind`/`docker` switch, or custom-base handling; or runner-image PATH/login-shell (`-l`) handling.

#### `specs/state/` - Persisted state model and upgrade/provenance state

- `specs/state/`
  - Description: State contracts: `state-model.md` and `upgrade-state.md`.
  - Read If: Adding or changing launcher-global persisted state or the `state.yaml` shape; any state read-modify-write, the locking primitives, or recovery; `internal/atomicwrite`; or the upgrade prompt, `pendingUpgrade`/`resolveBuildVersion`/`recoverAgentVersion`, the `agent-version` label, `--agent-version`, or the `updater.yaml` migration.

#### `specs/testing/` - Test coverage, isolation, and harness smoke tests

- `specs/testing/`
  - Description: Test contracts: `test-coverage.md`, `test-isolation.md`, `harness-smoke-tests.md`, `msb-journeys.md`, `release-surface-testing.md`, `shell-script-harnesses.md`.
  - Read If: Deciding what to test or triaging a failing `codecov/patch`/`codecov/project`; adding a test package/`TestMain`/double, debugging a test that reads real host state or fails under `-count>1`, the `//testenv:allow` ambient gate, or a state/config write to an unresolved root; the harness smoke tests (`make test-msb`/`test-msb-cold`) or the harness journeys suite (`test/msb/journeys`, `make test-msb-journeys`); a Go `test/scripts` harness or the `RunScript`/`gittest`/`fakecmd` helpers; or verifying a release before tagging or running the surface sweep.

#### `specs/vm/` - VM lifecycle and per-VM behaviours

- `specs/vm/`
  - Description: Per-VM contracts: `lifecycle.md`, `detached-jobs.md`, `idle-timeout.md`, `live-mount-readback.md`, `reconfig-decision.md`, `session-reaper.md`, `startup-hooks.md`, `workspace-bind-permissions.md`.
  - Read If: Any change to a run/shell/session lifecycle stage or cross-stage value (`prepare.PrepareSandbox`, `session.Run`/`Shell`, the `--dry-run*`/`--serve-only` modes); VM dockerd/agent-serve startup, readiness, or the idle timeout; reading a live VM's mounts or ports (an empty `cfg.Volumes`/`mounts (0)`); the reconfig decision or `ensure-vm.lock`; the reaper's `waitQuiescent`/`ReapPolicy`; a startup hook's interactivity or the `HookInteractive` seam; or the `/workspace` bind permission policy.

#### `specs/volumes/` - Home volume recovery/decoupling and volume commands

- `specs/volumes/`
  - Description: Volume contracts: `home-volume-recovery.md`, `runtime-home-decoupling.md`, `runtime-management-commands.md`.
  - Read If: Home-volume resolution (`ResolveHomeVolume`/`liveHomeVolume`) or diagnosing a duplicate/orphaned volume after state loss; the private `MSB_HOME`, the legacy home-volume migration prompt/notice, or `volume remove`; or volume/image/sandbox inspection, removal, disk compaction, or the `df`/`storage` accounting commands.

#### `specs/worktrees/` - The `/workspace` bind rule

- `specs/worktrees/`
  - Description: `worktree-layout.md` — the `/workspace` bind rule and guest-CWD derivation.
  - Read If: Changing the `/workspace` bind-source resolution, guest-CWD derivation, the worktree mount rule, or the outside-worktree shared-git-dir mount; or diagnosing a session that hides the worktree it was launched from, a `git` command that cannot resolve inside the VM, or the `--worktree` host git-dir side effect.

### `plans/` - TDD implementation plans

- `plans/2026-11-15-learn-mode-spike.md`
  - Description: Validation spike for learn mode: the host-run experiments behind the observation channel, with S1's per-harness home read-back still open (opencode2 only). Harness: `spike/learn-mode/`.
  - Read If: Picking up the unrun per-harness read-back (S1), rerunning an experiment, or checking an unverified observation-channel assumption (proxy reachability under `none`, egress bypass, denial logging, DNS coverage, per-harness credential layout).
- `plans/2026-11-20-harness-promotion-e2e.md`
  - Description: Manual e2e runbook and promotion bar for dropping the experimental flag from claude-code, codex, and pi for 0.8 (#783): isolated journeys A–I, known blockers (#767, #768), and the results table.
  - Read If: Running or resuming the 0.8 harness e2e pass, or deciding whether an agent profile may drop `Experimental()`.
- `plans/2026-11-21-msb-journeys.md`
  - Description: Implementation plan for #793, the first slice of the harness-journeys acceptance harness: host-side libvirt KVM-guest tooling (`scripts/kvm-guest.sh` + `make kvm-*`), the `test/harness`→`test/msb/startup` rename with the shared untagged `test/msb/driver`, the gate-scope changes (dead-code `test/` filter, ambient scan over non-test `.go` under `test/`, the production-closure contract test), `make test-msb-journeys` with the named-step journey model, and the learn-mode journeys (UC1, UC4, `config learn`, attribution) plus the surface-sweep block.
  - Read If: Executing #793; working on `scripts/kvm-guest.sh` or the `make kvm-*` targets; the `test/msb/{driver,startup,journeys}` rename and layout; the journeys' `Journey`/`Step` model; or the dead-code/ambient `test/` gate scope.
- `plans/2026-11-22-msb-smoke-remedies.md`
  - Description: Implementation plan for the in-guest smoke-suite reliability remedies: the start-of-run sandbox sweep, the `docker-ready-timeout`/`daemon-ready-timeout` config keys threaded into `internal/sandbox/vm`, the `AGENTS_SANDBOX_MSB_TIMEOUT_FACTOR` ceiling scaler, and the stale daemon-timeout marker.
  - Read If: Working on the `test-msb-in-vm` guest target, the smoke suite's readiness/quit ceilings or start sweep, the VM readiness-timeout config keys, or the `MSB_GUEST_*` make variables.
- `plans/2026-11-22-kvm-guest-agent-design.md`
  - Description: Design for running an agent (opencode2) directly on the KVM guest so it can build the launcher and drive real VMs: provisioning the guest toolchain, materializing the launcher's provisioned agent config into the guest (`config provision`), attaching a durable session, credential handling, and the git return path.
  - Read If: Working on `make kvm-agent`/`kvm-agent-provision`, the `agents-sandbox config provision` command, or how an agent works on this repo from the guest.
- `plans/2026-11-22-kvm-guest-sync-back.md`
  - Description: Implementation plan for the guest git identity and `make kvm-sync-back`: `kvm-agent-provision` writes the host's git `user.name`/`user.email` into the guest (later narrowed from a whole-`~/.gitconfig` copy; see `guides/kvm-guest.md`), and the new sync-back subcommand fetches the guest's branches back (all into `refs/remotes/kvm-guest/*`, or one into a local branch).
  - Read If: Working on `make kvm-sync-back`, the guest git identity provisioning, or `scripts/kvm-guest.sh`'s `sync_back`/`agent_provision`.
- `plans/2026-11-22-kvm-agent-up-design.md`
  - Description: Design for the one-command KVM-guest agent bring-up (`make kvm-agent-up`, the `agent-up` subcommand): idempotent ensure-running, the cloud-init gate, and credential passthrough.
  - Read If: Working on `make kvm-agent-up` or `scripts/kvm-guest.sh`'s `agent_up`/`ensure_running`/`wait_for_cloud_init`.
- `plans/2026-11-22-kvm-agent-up.md`
  - Description: Implementation plan for the same one-command bring-up (its tasks and tests); retire when folded into `guides/kvm-guest.md`.
  - Read If: Executing or reviewing the `kvm-agent-up` plan.
- `plans/2026-11-22-kvm-guest-dedicated-cpu.md`
  - Description: Contingency plan for making the in-guest `msb` suites reliable on a CPU-shared host: measure `steal`, then pin the guest's vCPUs (libvirt `cputune`) or dedicate host cores, versus keeping the timing/factor approach. Triggered only if the factor-3 sweep is not stable.
  - Read If: The in-guest `make test-msb-in-vm` runs are still unstable after `MSB_GUEST_TIMEOUT_FACTOR=3`, or you are evaluating CPU pinning/dedication for the KVM guest.
- `plans/2026-11-22-kvm-guest-host-git-remote-design.md`
  - Description: Design for making the host the guest's git remote: host-generated `git-shell`-restricted key, the guest's single `origin` routed to the host by a `url.<host>.insteadOf` rewrite, `kvm-sync` as a guest-side git pull, guest push; keeps `gh` and its sign-in, drops its GitHub `insteadOf` rewrites.
  - Read If: Reworking `scripts/kvm-guest.sh`'s `sync_in`/`sync_back`/`gh_auth` or the guest git remote, or reconciling the kvm-guest sync/return path.
- `plans/2026-11-22-kvm-guest-host-remote.md`
  - Description: Implementation plan for the host git remote: gateway/repo-path resolution, the guest key and `git-shell` authorization, the `origin` routing rewrite, `kvm-sync` as a guest-side git pull, and the test/guide/changelog changes.
  - Read If: Executing or reviewing the kvm-guest host-git-remote work.
