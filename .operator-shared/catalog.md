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

- `0001-yaml-handling.md`
  - Description: Decision to adopt `oneyaml.dev/go` for the config write path (`viperconfig.WriteSetting`), to serialize on `go.yaml.in/yaml/v4` for every path we own (viper keeps its read-only `go.yaml.in/yaml/v3`), and to cap untrusted config/secret/snippet reads at 16 MiB (`internal/configread`); `goccy/go-yaml` rejected, the owner scan and JSON5 depth recorded.
  - Read If: Changing `viperconfig.WriteSetting`, the YAML edit path, or the `configread` bound; evaluating oneyaml vs goccy; touching `go.mod`'s YAML dependency or the `yaml/v3`↔`v4` choice; or triaging a `FuzzParseState` serialization-stability failure.
- `0002-rootless-docker-engine.md`
  - Description: Decision to run the agent's Docker engine rootless as `dev` instead of granting the root-equivalent `docker` group; records the spike evidence, the fail-the-build prerequisite policy, the data root on the VM root disk, the `/var/run/docker.sock` symlink, and the `/dev/fuse`/`/dev/net/tun` permission fix.
  - Read If: Changing how the in-VM Docker engine is installed, started, or reached (`dockerAdoptionBlock`, `vm/dockerd.go`), its prerequisites, or evaluating podman/a daemonless builder/an authz proxy instead.
- `TEMPLATE.md`
  - Description: Template for an ADR in this directory (Context / Options / Decision / Consequences).
  - Read If: Writing a new ADR.

### `guides/` - How-to and reference notes for this partition

- `agent-link-opening.md`
  - Description: What each agent shells out to when it opens a link on Linux (opencode `xdg-open`, opencode2's `open` package, pi's `openBrowser`, claude-code's scheme allowlist), the OSC 8 wrinkle, and the network-free VM→host transport for a host-side opener (#273).
  - Read If: Working on opening links/URLs on the host from inside the VM (#273), an in-VM `xdg-open` shim, agent opener behavior, or any VM→host action channel.
- `branch-worktree-cleanup.md`
  - Description: How `scripts/cleanup-merged-branches.sh` (via `make cleanup-branches`) removes merged worktrees/branches and reports the rest; the three merge-detection tiers, the fresh-session worktree guard, the config-mirror protection, the named plan printed before the gate, dry-run skipping the remote refresh, the `--force-with-lease` origin deletion, and the host/VM worktree safety rules.
  - Read If: Running or changing local repository maintenance, cleaning up worktrees or merged branches, or debugging a branch the sweep kept or removed.
- `dead-code-detection-go.md`
  - Description: Using x/tools `deadcode` to find production code only tests keep alive: why staticcheck U1000 cannot, the default vs `-test` modes, the test-support paths to filter, and `-whylive` for seam-vs-accident triage.
  - Read If: Looking for dead or unreferenced production code, auditing whether a seam is still wired, or evaluating a dead-code linter; for the gate, the pin, or the `//deadcode:ignore` marker, read `specs/ci/quality-gates.md`.
- `dev-environment.md`
  - Description: The development VM's installed CLI tools and its standing environment limitations - remote git over SSH (HTTPS works), CI `pull_request` workflows absent on a conflicted PR, and TLS-interception self-signed-cert docker builds.
  - Read If: Choosing a CLI tool for a task, or hitting an environment limitation such as an unusable SSH remote, an absent CI run on a conflicted PR, or a self-signed-cert docker build failure.
- `github-issue-workflow.md`
  - Description: How this repo tracks work on GitHub with the `gh` CLI: the issue CRUD commands, the triage label vocabulary (`bug`/`enhancement`/`chore`/`documentation`, the `needs-triage`/`needs-info`/`ready-for-agent`/`ready-for-human`/`wontfix` states, the `upstream` marker, the orthogonal `priority:*` set), and that external PRs are not a request surface.
  - Read If: Creating, reading, labeling, or closing a GitHub issue; resolving the label vocabulary the `maintain` skill uses; or working through a skill that publishes to or reads from the issue tracker.
- `github-merge-driver-support.md`
  - Description: Verdict, with primary sources, that GitHub's server-side merge and merge-queue build ignore `.gitattributes` merge drivers, so `CHANGELOG.md merge=union` does not resolve a conflict; libgit2 does implement `union` but GitHub does not consult it, and GitLab dropped it in 17.1.
  - Read If: Considering `merge=union`, a custom merge driver, or any `.gitattributes` merge attribute to auto-resolve conflicts on GitHub; or explaining why a server-side conflict persists when the CLI git merges cleanly.
- `guides/mutation-testing.md`
  - Description: Mutation testing for Go in this repo - the tool survey and why gomutants was chosen, how to close gomutants survivors (recurring mutant types, the equivalent-mutant traps, the record-with-a-reason fallback), and the multi-turn sweep runbook for the #268 survivor tracker.
  - Read If: Evaluating or changing a Go mutation-testing tool or the `make mutation` target; triaging LIVED mutants from a sweep; picking a `SINCE` scope for a branch/PR sweep; or coordinating a #268 survivor-reduction pass.
- `operator-brain-maintenance.md`
  - Description: Conventions for keeping this brain small and findable: one-line catalog Descriptions, anchor-first `Read If` lines, the `specs/<subsystem>/` layout, drop-not-point for facts covered by a skill or merged spec, and retiring a plan once its work lands.
  - Read If: Adding or editing a catalog entry, spec, plan, guide, or skill; writing a new freeform document; or curating/retiring the brain.
- `podman-engine-testing.md`
  - Description: How the alternative-engine claim is verified: the `integration-podman` CI job runs the docker-build suite against a rootless podman socket, podman's unqualified-image-name refusal is the hazard the runner image avoids by qualifying its base, and the dev VM cannot run an engine build at all.
  - Read If: Verifying podman (or another Docker-API engine) against the integration suite, or investigating a build failure naming an unqualified image.
- `secret-placeholder-transcript-trap.md`
  - Description: Why a session dies once a microsandbox secret placeholder reaches the model context - the LLM transport POSTs the whole conversation, so a placeholder anywhere in history blocks every later turn; how to observe the block in `runtime.log`; and the launcher's config-file-only `secrets.common.allow-placeholder-for` repair.
  - Read If: Diagnosing a model turn blocked by a secret violation, working on `secrets.common.allow-placeholder-for`/`BuildSecretsFromSpecs`/`placeholderHostsFor`, or envsecret passthrough hosts.
- `test-runtime-cost.md`
  - Description: Where `go test ./...` wall time goes (test-binary linking, not test bodies), how `-tags microsandbox_ffi_path` drops the SDK's ~36 MB embedded native library, the test-body hotspots, and the polling/timeout seams that set failure latency.
  - Read If: Speeding up `make test`, diagnosing a slow or hanging test run, adding a polling/wait seam or a `TestMain`, adding a real-SDK test, or deciding whether a test may consume a production timeout budget.
- `vm-oom-crash-resilience.md`
  - Description: How the microsandbox microVM enforces memory/disk/CPU limits (virtio-mem plug cap, no swap/zram, guest global OOM killer), why an OOM kill is invisible from the launcher, the host-terminal corruption and recovery, the `Metrics`/`LogSourceSystem` detection surface, the async live memory resize, and the ranked detect-and-propose mitigation path.
  - Read If: Working on VM OOM/crash handling, a memory/disk/CPU limit-hit detector or "raise the limit" proposal, terminal corruption after an in-VM kill, or the daemon/dockerd re-ensure and supervision behaviour.

### `specs/` - System contracts for this partition

#### `specs/agents/` - Agent profiles and their daemons

- `specs/agents/hermes-agent.md`
  - Description: Contract for the Hermes Agent profile (Nous `hermes-agent`, MIT): a CLI-only, daemon-less integration whose install runs the official installer at a pinned release tag into a baked Python/Node tool store outside the home volume, whose state lives in `HERMES_HOME`, whose `config*.yaml` snippets merge as YAML, and whose build-only trees are pruned to trim its (largest) runner image.
  - Read If: Adding or changing the `hermes` agent profile, its installer/tool-store stage, version pinning or the latest-release fetch, `HERMES_HOME` or tool-store placement, YAML config provisioning, the `OwnsToolTree` handover, or the Hermes harness smoke entry; or diagnosing a Hermes launch that fails before its REPL.
- `specs/agents/notify.md`
  - Description: Contract for the notify event watcher on opencode/opencode2 - the agent owns the harness JSON shape via `EventStreamSpec.Parse`/`SessionListSpec`, `notify` keeps only SSE framing; opencode2's authed `GET /api/event` envelope maps to the same triggers as v1, subagent sessions (`parentID` non-empty) never notify, and v2's turn-scoped errors do not latch.
  - Read If: Changing notify for opencode/opencode2, the event-stream decode seam (`agent.EventStreamSpec`/`Parse`/`SessionListSpec`), subagent filtering via `parentID`, the v1/v2 SSE event mapping, or the tracker's child/busy/idle/error state; or diagnosing "notify does nothing on opencode2".
- `specs/agents/opencode2.md`
  - Description: Contract for the opencode2 (opencode v2) agent - its own launcher config namespace (`opencode2/`) with an `opencode` fallback while the guest path stays `opencode`, the baked fixed `OPENCODE_SERVER_PASSWORD` and `opencode:<password>` basic auth on every `/api/*` call, and the v2 worktree API (project-scoped routes, the `WorktreeProvider` seam with no guest `jq`, base-ref handling).
  - Read If: Changing the opencode2 profile, its config-namespace fork / `ConfigDirAliaser` / snippet merge / mirror resolution, `OPENCODE_SERVER_PASSWORD` / `DaemonHealthCmd`, the v2 worktree routes or `WorktreeProvider` seam, or `vm.ResolveTarget`/`validateWorktreeBase`; or diagnosing a dead-looking opencode2 daemon or a `--worktree` that 404s.

#### `specs/architecture/` - Cross-cutting package seams and run orchestration

- `specs/architecture/client-injection.md`
  - Description: Contract for resolving the external clients (msb, docker, config-paths) once at an entry point and threading them explicitly, the four behavior test seams as one injected `options.Seams` value on `options.RunOptions`, the process-root `configpaths.Get()` reads that stay global by design, and the two deliberate cross-layer edges (`viperconfig`→`sandbox/mounts`+`network`, `notify`→`sandbox/state`+`msb`) after the `options`→`state` edge was removed (#532).
  - Read If: Changing a `docker.Get()`/`configpaths.Get()` call site, `image.EnsureImage*`/`EnsureLoaded`/`Build`/`PreviewImage` or the image render helpers, adding a client or path resolver parameter to a run/build/doctor/prune entry point, the image build's agent-release resolver, or a cross-layer dependency of `options`, `viperconfig`, or `notify`.
- `specs/architecture/dry-run-preview.md`
  - Description: Contract making the user-facing `--dry-run` a true make-no-changes preview of a run/shell: it inspects the existing runner image and home volume and reports would-build/would-reuse, but on the preview path performs no image build, volume creation, prefill, state write, VM start, attach, or network request. `--dry-run-vm` remains the KVM-less "prepare everything, skip the VM" flag.
  - Read If: Changing `--dry-run`/`--dry-run-vm` behavior, the `options.Mode`/`runoptions.ResolveMode` boundary, the run/shell preview, `image.EnsureImageWithClient`'s build decision, or the `agentLatestVersion` test seam.
- `specs/architecture/msb-seam.md`
  - Description: Contract for the `internal/sandbox/msb` seam (#196) - it is the launcher's port over the microsandbox SDK, not an SDK mirror: provider-owned role interfaces composed into preserved composites (`composite == union of roles`), launcher-owned option/value types on the way in and out, the consumer narrowing rule (narrow to the role used; keep the composite when a function spans roles), the `notify.StreamOpener` consumer-owned exception, the mock strategy (fields stay on `MockMsbClient`; standalone role doubles), the three upstream-sync guards, and the "no `msbSdk` outside the seam" target with its `runtime` residue.
  - Read If: Changing `internal/sandbox/msb` interfaces, roles/composites, the mock, or how a consumer package depends on it; working on #196 or its follow-up #22; or deciding whether a consumer should accept a role or the composite.
- `specs/architecture/package-boundaries.md`
  - Description: Contract for the launcher's package boundaries - `internal/cliout` stays domain-free (generic rendering only) with the sandbox-shaped renderers and report DTOs in `internal/view`, and the run orchestration lives in `internal/sandbox/prepare` with the layering `cmd` → `session` → `prepare` → `vm` and no shared mutable stage `State`.
  - Read If: Moving a Printer, report-JSON DTO, or renderer between `internal/cliout` and `internal/view`, or run-preparation code between `internal/sandbox/prepare` and `internal/sandbox/vm`; or proposing a shared context/`State` object for the stages.
- `specs/architecture/termio-ui.md`
  - Description: Contract for termio's UI seam - the role-interface split (Logger/Output/Prompter/SpinnerFactory/TerminalState/Streams) composed into `termio.UI`, plus the deferred (not-on-main) design for a huh-backed prompt switch and the accessibility/non-TTY invariants.
  - Read If: Changing a termio role interface, `termio.UI` or `New`, which UI surface a caller depends on, the termio mock, interactive prompts or spinners, swapping in or scoping a UI library, or reasoning about mockability and non-TTY/accessibility behavior.

#### `specs/ci/` - CI pipeline and quality gates

- `specs/ci/ci-pipeline.md`
  - Description: Contract for the CI pipeline - what `.github/workflows/ci.yml` runs and gates (the always-run required `lint`/`test`/`race`/`deadcode`/`changelog` set, `vulncheck`, the opt-in `integration`/`integration-podman` jobs), the `main` merge queue and `merge_group` trigger, the `changes` code-free gate, the CodeQL workflow, the fuzz workflow, and per-PR changelog fragments.
  - Read If: Changing `.github/workflows/ci.yml`/`codeql.yml`/`fuzz.yml`, the CI triggers or job conditions, the merge queue or `merge_group`, the required-check set or `protect main` ruleset, the `changes` gate or PR concurrency, the `run-integration`/`run-integration-podman` labels, the CodeQL languages/path filter/default-setup state, the `make fuzz` target or its knobs, or how changelog entries are authored/assembled/checked.
- `specs/ci/mutation-testing.md`
  - Description: Contract for the on-demand, per-package mutation-testing practice - a version-pinned `make mutation` target over gomutants, scoped by default to mutants on lines changed since the last release tag, never a required CI or `make check` gate (an advisory `mutation` workflow runs the sweep on every PR as a signal only); the kill/delete/record triage rule and the reduction-not-elimination stance.
  - Read If: Changing the `make mutation` target, the `GOMUTANTS_VERSION` pin, the triage convention for surviving mutants, or the advisory `mutation` CI workflow; or evaluating a Go mutation-testing tool (see `guides/mutation-testing.md`).
- `specs/ci/quality-gates.md`
  - Description: Contract for the repo's quality gates - the golangci-lint standard (the formatter set and linter posture in `.golangci.yml`, and the `lint` build-tag set `integration msb microsandbox_ffi_path` that makes tagged files lintable), the dead-code gate (`make deadcode` over a pinned x/tools deadcode with the reason-bearing `//deadcode:ignore` marker and `ci/check-deadcode.sh`), and the documentation-fragment gate in `ci/check-docs.sh` (the kramdown slug mirror and `internal/docslink`).
  - Read If: Changing `.golangci.yml` (formatters or linters) or the `lint` target's `--build-tags` set, the dead-code gate (`make deadcode`, the `//deadcode:ignore` marker, `ci/check-deadcode.sh`, the `DEADCODE_VERSION` pin), or the doc-link gate (`ci/check-docs.sh`, `internal/docslink`, the doctor/build-error `Docs:` links); or diagnosing a lint/format disagreement or an unmarked dead-code finding.

#### `specs/configuration/` - Config scopes, keys, decoding, validation, project-config root and trust

- `specs/configuration/config-file-decoding.md`
  - Description: Contract for reading and decoding launcher/home/agent config files - one `configdecode` core (`FormatFor`/`Decode`/`DecodeFile`) layered over `configread`'s bounded read and `configpaths`' extension vocabulary, with each loader's accepted formats (no TOML in the launcher), error label, and parse-failure policy kept explicit.
  - Read If: Changing config-file reading, classification, or JSON5/JSONC handling in `homeconfig`/`viperconfig`/`configmerge`; adding a config format; touching the launcher read path's YAML parser or the `MergeConfig` -> `MergeConfigMap` seam; or diagnosing a file one loader accepts and another rejects.
- `specs/configuration/config-keys.md`
  - Description: Contract for the launcher config-key registry: the env-var and CLI-flag key sets are derived from the `Config` struct's mapstructure tags, `env:"-"` marks a config-file-only key, and a bound flag's default type comes from the owning field's Go kind.
  - Read If: Adding or changing a launcher config key, its env var, or its CLI flag; or reasoning about why an env var is silently ignored or where `flagTypedDefault`'s type comes from.
- `specs/configuration/config-scopes.md`
  - Description: Contract for the launcher's config path resolution and scope order - `internal/configpaths` is the one authority for host paths (the `ConfigPaths` seam, home included), and `configpaths.ConfigScopes` is the one ordered list of file-based config dirs (generic user < per-slug user < project) that the launcher config file, env/secret files, `home:` sources, and agent config dirs all derive from.
  - Read If: Changing config-scope order or precedence, per-slug user config, host config/home path resolution, `configpaths.ConfigPaths`/`ConfigScopes`, `viperconfig.ConfigLayers`, `envsecret.LoadEnvAndSecrets`, `homeconfig` layer loading, or `reprovision.ResolveConfigSource`; or diagnosing a per-slug user file that is silently ignored, or a test that must not touch the real home.
- `specs/configuration/config-value-validation.md`
  - Description: Contract for validating the launcher's VM size and duration settings - memory has a 128 MiB floor and is clamped (not rejected) to the host's RAM at boot, tmp-size defaults to min(2 GiB, memory/2) and must stay below the effective memory (clamped at creation), disk-size and workspace-quota must be positive, the `/tmp` disk-raise offer caps at the largest whole GiB strictly below memory (`tmpRaiseMaximumMiB`), and the duration/retry keys keep their existing bounds.
  - Read If: Changing VM size validation, `options.ParseMemoryOK`/`ResolveTmpSizeMiB`/`ValidateSizes`/`ResolveDiskSizeMiB`/`ResolveWorkspaceQuotaMiB`, `runoptions.resolveSizes`, `resourceproposal.tmpRaiseMaximumMiB`, the `memory`/`tmp-size`/`disk-size`/`workspace-quota` bounds or the tmp-vs-memory relation, `vm.bootMemoryMiB`/`vm.effectiveTmpSizeMiB`/`reconfig.tmpSizeChange` or the tmp mount; or diagnosing a rejected size, a zero-size setting, a `/tmp` raise equal to memory, or a `/tmp` larger than the VM's memory.
- `specs/configuration/project-config-root.md`
  - Description: Contract for where a bare checkout's project config (`.agents-sandbox`) is read from: the bare root next to `.git`, selected by `core.bare`, with every project accessor routed through one base-dir resolver; a bare checkout's worktree-level `.agents-sandbox` is ignored with a warning.
  - Read If: Changing the bare-layout project-config root fix, the `core.bare` discriminator, the project config base-dir resolver, or the bare-worktrees recipe/Beta marking.
- `specs/configuration/project-config-trust.md`
  - Description: Contract for the project config trust guard: the project config layer is untrusted in its entirety, everything the launcher consumes from it (`home:`-copied project files included) requires approval-on-change recorded per checkout, the gate runs at config resolution and an unapproved layer is not loaded, an unapproved change refuses to start non-interactively, the prompt shows the resolved old → new effect and its source, and every later read of a project file is verified against the approved bytes (verify on read).
  - Read If: Working on the project config trust gate, the approve-on-change gate, config resolution in the root command's `PersistentPreRunE`, project-layer provenance, or `config harden`; or diagnosing a run that refuses to start, or a command that ignores the project config, because `.agents-sandbox` changed.

#### `specs/conventions/` - Repo-wide conventions

- `specs/conventions/deprecation-policy.md`
  - Description: Durable deprecation policy for agents-sandbox (0.x, high UX bar): how a flag/config key/env var/command is deprecated, how long it survives, how it is surfaced, and the current deprecation register with replacement and removal targets.
  - Read If: Deprecating or removing a CLI flag, config key, env var, or command; deciding whether a change needs a shim or a clean break; or enforcing the deprecation register in tests.

#### `specs/identity/` - Project, artifact, and sandbox identity and naming

- `specs/identity/artifact-naming.md`
  - Description: Contract for the prefix-free artifact naming strategy: sandboxes, home volumes and microsandbox image references drop the `agents-sandbox-` prefix while Docker images keep it, new artifacts follow the new scheme and existing ones stay discoverable (write-new, read-both), and discovery relies on the private runtime home plus labels rather than a name prefix.
  - Read If: Changing `internal/sandbox/naming`, the VM/home-volume/runner-image name or reference constructors or parsers, artifact discovery (list/remove/prune/reference scans), the sandbox or volume labels, or the Docker-vs-msb image reference split (`ImageInfo`); or diagnosing a legacy artifact that stopped being found or rebuilt.
- `specs/identity/checkout-identity.md`
  - Description: Contract for splitting project identity in two: a repo-scoped slug (origin-derived) driving the per-slug user config dir and the sandbox grouping label, and a checkout-scoped slug (resolved git common dir) driving state, VM name, home volume, locks/claims, and the runner image tag. Includes the automatic, noticed migration that adopts the legacy origin-keyed identity, the prune home-volume reference guard, the side-effect-free dry-run, and the outside-worktree identity.
  - Read If: Changing the repo-vs-checkout slug derivation, the runner-image tag identity, the `config paths` command, the checkout-identity migration and its notice, the prune reference guard, or the identity of a worktree outside its checkout.
- `specs/identity/sandbox-identity.md`
  - Description: Contract distinguishing a sandbox's mutable *name* from its stable *identity* - operations that act on a specific sandbox key off the SDK's identity-aware lifecycle methods and fail closed on `ErrSandboxReplaced` (`msb.IsReplaced`); the per-path handling for prune, throwaway task sandboxes, reconfig replace, the reaper, project-VM create, and `ConnectOrStart`; and no persisted identity.
  - Read If: Working on the msb seam's sandbox create/removal/attach paths, `EnsureProjectVM`, prune, the reaper, or reconfig-replace; or diagnosing `ErrSandboxReplaced`/`ErrSandboxAlreadyExists`.

#### `specs/network-and-secrets/` - Network profiles and secret scoping

- `specs/network-and-secrets/network-profiles.md`
  - Description: Contract for the launcher's composable network profiles: public/private/host compose into one microsandbox policy, none is terminal and allowlist-only, the config/env/flag surfaces accept a scalar, comma string, or list, and the fingerprint keeps one `profile=` line per profile so single-profile digests do not change.
  - Read If: Changing `network.Policy`, profile parsing or validation, the `network.profile`/`--network`/`AGENTS_SANDBOX_NETWORK_PROFILE` surface, or network fingerprinting and its VM-reconfiguration trigger.
- `specs/network-and-secrets/secret-header-scoping.md`
  - Description: Contract for scoping a secret's placeholder substitution to named request-header fields via `env.secret.yaml` `header_fields`: the lowercase/dedupe/sort normalization shared by the built entry and the fingerprint, the all-headers default for an absent or empty list, and the `HeaderFields` fold in `SecretsContentHash`.
  - Read If: Changing per-secret header substitution, `envsecret.SecretSpec`/`BuildSecretsFromSpecs`, the `header_fields` docs, or `state.SecretsContentHash`'s field set.

#### `specs/onboarding/` - First-run guidance and config migration

- `specs/onboarding/onboarding.md`
  - Description: Contract for guiding a fresh install to a working sandbox - safe config migration (static-vs-login credential kinds, host determination, the derived-state machine, the `Guide` outcomes), the tiered `config wizard`, `config harden`, and the doctor-remediation + Get started docs flow.
  - Read If: Working on `config migrate`/`config wizard`/`config harden`, first-run guidance, credential/auth handling, network egress derivation, the `Guide` outcomes and their warning/`HasChanges` gating, the doctor output and its troubleshooting anchors, or the Get started docs.

#### `specs/release/` - Release branching, assets, self-upgrade, darwin cross-build

- `specs/release/darwin-cross-compile.md`
  - Description: Contract for the darwin cross-build's zig prerequisite - beyond zig on PATH, the macOS linker stubs (`.github/stubs/*.tbd`) must be baked into `<zig-root>/lib/libc/darwin`; the three consumers (CI release job, dev VM image, `make bootstrap`); `make bootstrap`'s classify-then-act guarantee; the symlink-following `ZIG_ROOT` resolution; and the accepted stub duplication.
  - Read If: Changing the macOS/darwin cross-compile path, `make bootstrap` or its zig classification, the release job's stub step, `.github/stubs/*.tbd`, the dev VM Dockerfile's stub step, or the Makefile's `ZIG_ROOT`/`ZIG_MANAGED_ROOT` resolution; or diagnosing a darwin cross-build that cannot find CoreFoundation, Security, or resolv.
- `specs/release/release-asset-integrity.md`
  - Description: Contract for verifying a downloaded release asset before the self-update installs it: the release publishes one `checksums.txt`, the updater verifies SHA-256 against it, and every metadata failure is fail-closed.
  - Read If: Changing the self-update download/verify/replace path, the release job's asset or checksums publishing, or `internal/upgrade`'s checksum handling.
- `specs/release/release-branching.md`
  - Description: Contract for the per-minor release branch `release/<major>.<minor>` - cut at the release commit, the branch the line's patch releases are tagged from, and where fixes for a released line land while `main` keeps new work.
  - Read If: Deciding where a release tag or a fix lands, cutting a patch release for a released line, backporting a fix, or changing the release branch name or flow.
- `specs/release/self-upgrade-channels.md`
  - Description: Design for the launcher's self-upgrade release channels - an `upgrade.channel` setting (`stable` default, `canary`) where canary means the newest eligible release, a tag-addressed download replacing the `/latest` URL, a switch back to stable only via an explicit `--channel` (a configured channel never downgrades), an undocumented dev-build lever, and the release-CI prerelease classification.
  - Read If: Changing `upgrade.channel`, canary resolution/eligibility, the tag-pinned release download, the `--channel` switch, the undocumented dev-build upgrade lever, or the release CI's prerelease classification and the docs workflow's prerelease guard.

#### `specs/resources/` - Resource-limit detection and proposal

- `specs/resources/resource-limit-proposal.md`
  - Description: Contract for detecting a project VM resource-limit hit (memory OOM and full disks) and prompting the user to raise the persisted setting, with the host terminal recovered automatically - the evidence classes (guest OOM kernel-log line, metrics headroom, in-guest probe), the threshold and re-arm, the anti-nag marker, the persistence-layer selection and env-shadow rule, and which limits apply live vs recreate.
  - Read If: Working on the OOM/limit-hit or disk-limit detector or raise proposal, the `pressure`/`resourceproposal` packages, the `msb.Sandbox.Metrics`/`Logs` seam, persistence of `memory`/`disk-size`/`workspace-quota`/`tmp-size`, the live root-disk grow, or the automatic terminal reset after an attach.

#### `specs/runner-image/` - Runner image build, composition, PATH, CA trust

- `specs/runner-image/ca-trust-helper.md`
  - Description: Contract for the CA-trust helper the runner image installs as `/usr/local/bin/msb-trust` - which application trust stores need it and which do not, the verbatim-vendoring pin, why it is not run automatically, and where it is documented.
  - Read If: Working on TLS interception, secret-substitution failures in a specific application (Java/Gradle, Chrome), the runner image's build-context files, or the `msb-trust` helper.
- `specs/runner-image/image-build.md`
  - Description: Contract for the runner-image build and its microsandbox cache - which agent version is baked, when the image is (re)built and when Docker's layer cache is bypassed (`BuildOptions.Force`), the lazy msb load decision over a content-addressed reference (`<slug>:<agent>.<contentid>`, with the Docker tag kept stable), `ParseImageTag`/ownership round-trip, and prune retention.
  - Read If: Changing image build/caching, `buildDecision`/`buildReasons`/`BuildOptions.Force`/`--rebuild`, `image.EnsureImage`/`Build`/`EnsureLoaded`/`ImageInfo`, `prepare.ResolveAgentVersionForBuild`/`resolveBuildVersion`/`recoverAgentVersion`, `naming.RunnerImageRef`/`ImageIdentity`/`ParseImageTag`, `state.ImageReferences`, `pruning.PruneImages`/`keepImage`, or the volume subcommands' image handling; or diagnosing an unexpected `loading image … into microsandbox`.
- `specs/runner-image/image-composition.md`
  - Description: Contract for how the per-project runner Dockerfile is composed into per-tool multistage stages so tool-owned layers cache independently of the user body, the base, and each other; includes the node stage, the native vs npm agent install methods, and the `dind` → `docker` rename with deprecated aliases.
  - Read If: Changing `RenderDockerfile`/stage composition, image layer caching, which agents need node, agent install methods, tool-tree ownership (`COPY --chown=dev:dev`), the dind/docker switch or marker, custom-base handling, tool install paths, login-shell PATH handling, or the dockerd storage driver.
- `specs/runner-image/login-shell-path.md`
  - Description: Contract for keeping the runner image's composed PATH (`ENV PATH`, `/opt/agents-sandbox/bin`, project toolchains) effective in login-shell contexts by re-merging dropped entries from `AGENTS_SANDBOX_IMAGE_PATH` through a `/etc/profile.d` drop-in.
  - Read If: Working on runner-image PATH handling, login-shell (`-l`) sessions, `run`/`shell` attach, custom Dockerfile `ENV PATH`, or the `/etc/profile.d` merge helper.

#### `specs/state/` - Persisted state model and upgrade/provenance state

- `specs/state/state-model.md`
  - Description: Contract for how the launcher persists state - launcher-global state is one root `state.yaml` document (with the `updates:` namespace and silent legacy folds), every read-modify-write goes through one locked transaction per document (`WithHomeState`/`RecoverHomeState`/`WithRootState`/`WithKeyLocks`/`RemoveHomeStateIf`), locks live under `<root>/locks/` and are never deleted, and `internal/atomicwrite` makes each write crash-durable with an installed-write error marker.
  - Read If: Adding or changing launcher-global persisted state or the root `state.yaml` shape; changing any project/launcher state read-modify-write; the state locking primitives or recovery; `internal/upgrade`'s state seam; the `upgrade.json`/`updater.yaml` folds; or `internal/atomicwrite` and the callers that roll back a resource after a failed state write.
- `specs/state/upgrade-state.md`
  - Description: Contract for the agent upgrade-check and provenance state - machine-global network facts (`RootState.updates.agents`: cadence + cached `latest_version`) split from per-(slug, agent) image facts (`HomeState`), with the runner image's `agent-version` label the authority and `agent_version` a cache the image-ensuring paths reconcile, so a stale cache never rebuilds to an older release.
  - Read If: Changing the agent upgrade prompt, `pendingUpgrade`/`resolveBuildVersion`/`recoverAgentVersion`/`recordImageProvenance`, `PrepareSandbox`'s version handling, the release resolution, the `agent-version` label, `--agent-version`, or the legacy `updater.yaml` migration; or diagnosing a baseline that stays empty after deletion or an accepted upgrade that reverts.

#### `specs/testing/` - Test coverage, isolation, and harness smoke tests

- `specs/testing/harness-smoke-tests.md`
  - Description: Contract for the `msb`-tagged harness smoke tests that start every registered harness to its TUI in a real microsandbox VM (readiness by alternate-screen marker, startup-line marker, or a settled wait; optional seeded home files), the docker-storage smoke as a sibling subtest, the per-harness isolated home tree (`prepareHome`) with a shared microsandbox image cache, the parallel subtests sized to the host's free memory (1.5 GiB working-set VMs, 2 GiB `--memory` ceiling, `AGENTS_SANDBOX_MSB_PARALLEL` override) and bounded image prebuild, the reusable-root escape hatch (`AGENTS_SANDBOX_SMOKE_ROOT` / `make test-msb-reuse`, held under an exclusive lock so concurrent reuse runs serialize), and the isolation that keeps them off the developer's existing agents-sandbox state.
  - Read If: Adding or changing the harness smoke tests or the docker-storage sibling subtest, the `msb` build tag, `make test-msb`/`make test-msb-reuse`/`MSB_PARALLEL` or `AGENTS_SANDBOX_SMOKE_ROOT`, the harness quit-press table, the readiness signals or seeded home files, the memory-derived parallelism or per-VM memory cap, or when a harness's TUI startup or quit behavior changes.
- `specs/testing/test-coverage.md`
  - Description: Contract for the coverage bar on added and changed lines - every added line must be executed by a test (error/transport/status branches included), a branch unreachable by construction must be removed, records where the numeric thresholds live (`.codecov.yml`), and that codecov is not a required status check.
  - Read If: Adding or changing production code and deciding what to test, reviewing a diff for coverage gaps, or triaging a failing `codecov/patch` or `codecov/project` check on a PR.
- `specs/testing/test-isolation.md`
  - Description: Contract for keeping tests off real host state: the test doubles and fail-fast installers living in a `<pkg>/<name>test` subpackage (so the command's dep graph never links `testing`), the fail-fast seam mocks installed by every package's `TestMain`, the `realMsbClient` SDK seams that keep unit tests off the native library, the msb-tagged tests' isolated SDK-pinned runtime home (with the `AGENTS_SANDBOX_MSB_TEST_HOME` reuse opt-in, held under an exclusive lock so concurrent reuse runs serialize) and its stop-before-remove / post-run sweep / idle-timeout-backstop teardown, the `WithMockConfigPaths` home redirect, the image integration tests' deliberate cached-release read, the enforcement tests that derive each package's required set from the import graph, the ambient-environment gate (the reason-bearing `//testenv:allow` marker over net/env/device/signal/absolute-exec/host-probe calls), the injected-client/in-package-resolver shift (see `specs/architecture/client-injection.md`), the rule that seam overrides must be restored so the suite stays idempotent under `-count>1`, the absolute-write-root invariant that guards every state/config write funnel at the consumer so an unresolved root cannot write relative to the process CWD (the mutation-sweep residue class), and the real-git artifact tests living behind the `integration` tag in `<unit>_integration_test.go` with a hermetic git environment.
  - Read If: Adding a test package or a `TestMain`, deciding where a test double or fail-fast installer belongs, adding a real-SDK test, debugging a test that reads or writes real host state (config, home, docker, msb, doctor, notify), adding a default-suite test that touches the process environment, a socket, a device, a signal, an absolute-path binary, or a host figure like installed RAM (the `//testenv:allow` gate), an msb-tagged test that leaks a sandbox/VM, the `AGENTS_SANDBOX_MSB_TEST_HOME` reuse opt-in, a test that fails only under repeated runs (`-count>1`), or a state/config/lock write that must reject a path that did not resolve to an absolute root.

#### `specs/vm/` - VM lifecycle and per-VM behaviours

- `specs/vm/detached-jobs.md`
  - Description: Contract for running dockerd and the agent serve daemon as microsandbox runtime-owned detached jobs (`ExecDetached`/`Job`) instead of shell-backgrounded `nohup` processes; health-probe readiness with `Job.Inspect` crash detection and `Job.Logs` on failure, no shell fallback, marker identity for kill rediscovery, and the running-container idle-stop warning.
  - Read If: Working on the VM's dockerd/agent-serve startup, readiness, or restart in `internal/sandbox/vm`, the msb detached-job seam, the agent `DaemonProvider` daemon command, or the VM idle timeout.
- `specs/vm/idle-timeout.md`
  - Description: Contract for the VM idle timeout default: it lives only in `options.DefaultVMIdleTimeout`; the config layer carries `auto-stop-timeout` as-is and never substitutes its own default.
  - Read If: Changing the idle timeout default, `Config.AutoStopTimeout`/`Config.IdleTimeout`, `RunOptions.IdleTimeout`, `runoptions.Build`, or the VM lifecycle's `WithIdleTimeout` wiring.
- `specs/vm/lifecycle.md`
  - Description: Canonical model of the `run`/`shell` lifecycle: the ordered stage ids the code constants (`internal/sandbox/lifecycle`) and the trace tests share, what each stage reads, writes and guarantees, the single authority for every cross-stage value, the liveness principle and its per-artifact rules, the mode model, and the invariants a stage change must not break.
  - Read If: Making any change to a run/shell/session lifecycle stage or a cross-stage value (`cmd.runFunc`, `pruning.AutoPrune`/`keepImage`/`PruneVolumes`, `prepare.PrepareSandbox`, `session.Run`/`session.Shell`, the `--dry-run`/`--dry-run-vm`/`--serve-only` modes); or diagnosing two subsystems that disagree about what is live or current.
- `specs/vm/live-mount-readback.md`
  - Description: Contract for reading a live project VM's mounts and published ports. The microsandbox Go SDK's `SandboxConfig.UnmarshalJSON` drops the persisted `mounts` and `network.ports`, so the launcher decodes the persisted config JSON through `msb.SandboxHandle.ConfigMounts()`/`ConfigPorts()` instead.
  - Read If: Reading or changing how the launcher learns a live VM's mounts or published ports, or diagnosing an empty `cfg.Volumes` / `mounts (0)` / ignored `tmp_size`, `workspace-quota`, or `ports:` change.
- `specs/vm/reconfig-decision.md`
  - Description: Contract for the per-startup project-VM reconfiguration decision: how changed settings are detected via persisted fingerprints, which changes recreate the VM vs restart the daemon, how a change is shown (the alone-apply warning and the value-free fingerprint entries), the fingerprint-preservation invariant, the #183 seeding fix, and the per-project creation lock that also covers the recreate destroy.
  - Read If: Changing `readLiveProjectVM`/`resolveReconfigDecision`/`applyReconfigHomeAction`/`PlanReconfig`, the env/secret/network/mount fingerprints or their display entries, the reconfig prompt or alone-apply warning, or the `ensure-vm.lock` scope; or diagnosing a spurious VM rebuild or "network policy changed" prompt.
- `specs/vm/session-reaper.md`
  - Description: Contract for what a session in retry does to the detach reaper's VM hold, and where the retry cap applies.
  - Read If: Changing `waitQuiescent`/`quiescenceOf`/`sessionPoll`, `options.ReapPolicy`, or `auto-stop-max-session-retries`; or diagnosing a VM that idles out while a session is still retrying, or one held past the retry cap.
- `specs/vm/workspace-bind-permissions.md`
  - Description: Contract for the `/workspace` bind mount's host-permission policy - guest rwx bits mirror to the real host inode (`host-permissions=mirror`) so host git trees stay consistent, while ownership/setuid are never propagated and configured mounts keep the `private` default.
  - Read If: Changing the `/workspace` bind mount options in `buildMounts`, diagnosing host-vs-VM permission (exec-bit) divergence or dirty host git trees, or reasoning about whether a VM recreate is needed to apply a mount-permission change.

#### `specs/volumes/` - Home volume recovery/decoupling and volume commands

- `specs/volumes/home-volume-recovery.md`
  - Description: Contract for recovering a project's home volume when its `state.yaml` is lost - reuse the live VM's mounted volume instead of minting a duplicate and orphaning the original.
  - Read If: Changing home-volume resolution (`volume.resolveHomeVolumePlan`/`Manager.ResolveHomeVolume`/`commitHomeVolumePlan`/`PreviewHomeVolume`), `liveHomeVolume` in `internal/sandbox/prepare` (delegating to `vm.ProjectVMHomeVolume`), or diagnosing a duplicate/orphaned home volume after state loss.
- `specs/volumes/runtime-home-decoupling.md`
  - Description: Design for decoupling agents-sandbox's microsandbox runtime home from any user-installed microsandbox: a private XDG data home, no user-facing msb, retired version-matching recovery, and an opt-in copy of legacy home volumes tracked in the root `state.yaml` `migrations` registry, plus a one-time post-migration legacy-runtime notice and the `volume remove` cleanup command.
  - Read If: Implementing or reviewing runtime-home decoupling, the private `MSB_HOME`, removal of the runtime mismatch recovery menu, the legacy home-volume copy prompt and its persisted migration state, the ambiguous-state offer, the post-migration notice, or `volume remove`.
- `specs/volumes/runtime-management-commands.md`
  - Description: Contract for agents-sandbox's microsandbox management surface - volume/image/sandbox inspection, image and sandbox removal, sandbox disk compaction, a top-level `df` plus a `storage` group alias, and uniform `-q`/`--format json`/`--yes`/`--older-than` conventions.
  - Read If: Adding or reviewing volume/image/sandbox inspection, removal, and disk-compaction commands, the `df`/`storage` accounting commands, prune confirmation/format flags, or command-surface consistency conventions.

#### `specs/worktrees/` - The `/workspace` bind rule

- `specs/worktrees/worktree-layout.md`
  - Description: Contract for the `/workspace` bind-source rule - bind the smallest enclosing checkout containing the CWD, so a session in a nested or bare-layout worktree always exposes its worktree, with guest-CWD derivation for subdirectories and the outside-worktree case (bind its own root, run it as its own checkout with its shared git dir mounted). The bare-checkout recipe is the tracked `bare-worktrees` skill; per-checkout identity is `specs/identity/checkout-identity.md`.
  - Read If: Changing the `/workspace` bind-source resolution, guest-CWD derivation, the worktree mount rule, or the outside-worktree shared-git-dir mount; or diagnosing a session that hides the worktree it was launched from or a `git` command that cannot resolve inside the VM.

### `plans/` - TDD implementation plans

- `2026-11-07-hermetic-test-suite-design.md`
  - Description: Design for making the default `go test` suite hermetic: the acceptance envelope (module copy outside any checkout + scrubbed env + network namespace), the host-figure sentinel pass on injected seams, the wall-clock rule, and the release-to-default cadence.
  - Read If: Working on test decoupling from the execution environment, the hermetic envelope targets, the host-figure sentinel pass, or the hermetic suite's cadence.
- `2026-11-07-hermetic-test-envelope.md`
  - Description: Plan 1 of the hermetic design: `scripts/test-envelope.sh` (module copy + `env -i` scrub, `--netns` strict wrapper), the `test-scrubbed`/`test-hermetic`/`envelope-test` targets, and the task that records the failing inventory and runtime budget.
  - Read If: Implementing the hermetic envelope targets, the scrubbed/strict runners, or the inventory step that sequences the identity, host-figure, and wall-clock fixes.
- `2026-11-07-hermetic-inventory.md`
  - Description: First-run envelope record: the runtime budget (envelope ~1.5× an uncached `go test`, ~4.5× a cached `make test`), the green scrubbed run, and the one failing surface (6 uid/permission `chmod`-based tests the strict run runs as root).
  - Read If: Picking up a hermetic surface fix, choosing the next hermetic plan's tasks, or checking the envelope's measured runtime against `make test`.
- `2026-11-07-hermetic-uid-permission-surface.md`
  - Description: Plan 2 of the hermetic design: remove the effective-uid dependence from the six tests the strict envelope runs as uid 0 (via `configmerge.readFile`, `runtimemigration.readDir`, and a `state.OverrideStateWriteForTest` hook), pin `GOTOOLCHAIN=local`, and verify `make test-hermetic` is green.
  - Read If: Implementing the uid/permission surface fix that makes the strict hermetic envelope green, or adding a read/write failure seam to `configmerge`, `runtimemigration`, or `state`.
