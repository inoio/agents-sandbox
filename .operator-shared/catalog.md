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
  - Description: Contract for composing the per-project runner Dockerfile into per-tool multistage stages (base/node/agent/docker) merged into the runner via `COPY`, so tool-owned layers cache independently of the user body and each other; when the node stage is emitted (only for agents that require node, i.e. pi), native-installer vs npm agent install methods, the `/opt/agents-sandbox` artifact contract, adoption-time user-provided detection, the docker marker as a copy/adopt point, forcing vfs via the dockerd flag instead of `daemon.json`, the `dind` → `docker` rename with deprecated aliases, and login-shell PATH handling (`AGENTS_SANDBOX_IMAGE_PATH` re-merged via the `/etc/profile.d` script, whose content is folded into the dockerfile-id).
  - Read If: Changing `RenderDockerfile`/stage composition, which agents need node, agent install methods, tool install paths, layer caching, the docker/dind switch or marker, custom-base handling, login-shell PATH handling (`AGENTS_SANDBOX_IMAGE_PATH`, the `/etc/profile.d` merge script, dockerfile-id identity), or the dockerd storage driver.
- `login-shell-path.md`
  - Description: Contract for keeping the runner image's composed PATH (custom Dockerfile `ENV PATH`, `/opt/agents-sandbox/bin`, project toolchains) effective in login-shell contexts by re-merging dropped entries from `AGENTS_SANDBOX_IMAGE_PATH` through a `/etc/profile.d` drop-in (devcontainers-style mergePaths); supersedes the `/usr/local/bin` symlink stopgap. Acceptance target is resolution equivalence, not byte-identical PATH.
  - Read If: Working on runner-image PATH handling, login-shell (`-l`) sessions, `run`/`shell` attach, custom Dockerfile `ENV PATH`, or the `/etc/profile.d` merge helper.
- `buildkit-builder-vm.md`
  - Description: Design for issue #15, implemented: build the runner image without host Docker by running `buildkitd` inside a microsandbox VM, driven by a host-side BuildKit Go client over a published TCP port; free/unique host port invariant, `native` snapshotter, `type=oci` export, `ImageLoad` delivery, identity/reuse via the msb cache, gateway `ResolveImageConfig` base-digest route, removal of `internal/sandbox/docker` + `doctor.CheckDocker`, and registry auth via a `~/.docker/config.json` BuildKit session. Two clauses superseded by `builder-backends.md`: the cache is now the embedded OCI registry (not a named volume) and the Docker-container test harness is removed (moby replaces it); the original `spike/` validation tree is also removed.
  - Read If: Implementing or reviewing the Docker-free BuildKit builder (issue #15), the `internal/sandbox/builder` package, or changes to the image build/inspect/load pipeline and doctor prerequisites.
- `builder-backends.md` (implemented)
  - Description: Builder-backend contract — default msb (buildkitd in a shared, reused microsandbox VM) with an embedded host OCI registry (`distribution/v3`) for `type=registry` cache, and an opt-in moby backend (Docker Engine API build). Backend selection is config + env (no CLI flag), env overrides config, default msb. Registry is a per-user singleton daemon with a 0600 token; builder-VM reuse via a persisted port lets parallel builds share one VM. Includes the Keep vs Remove inventory stripping the spike benchmarks/`AGENTS_SANDBOX_BUILDER_*` knobs and the Docker-container buildkit backend (harness + `docker` value removed; `docker-test-harness` spec retired), and the prune rewire. Implemented; one clause (registry bind reachability on a KVM host) is unvalidated.
  - Read If: Working on builder backend selection, the embedded cache registry, builder-VM reuse/parallel builds, the moby/engine-API build path, builder doctor checks, builder docs, or cleaning up the build-performance spike.
- `registry-pull-delivery.md` (implemented)
  - Description: Design contract for delivering the runner image to microsandbox by pushing it to the embedded registry from the single solve and pulling it by reference at VM creation (msb backend), retiring the OCI-archive/`ImageLoad` round-trip for msb while moby keeps the archive. Decisions: registry image exporter (guest-authority push name), host-dial pull ref with `WithRegistryInsecure`, refresh-after-build via `PullPolicy(Always)` (measured: msb `IfMissing` short-circuits a same-tag re-push), registry keep-alive held across `CreateSandbox`, anonymous reads / token'd writes, prune clearing cache + runner-image repos together (global blob store), and a test-hermeticity invariant (offline delivery seam + hidden-command dispatch in every test binary reaching `image.EnsureImage`). O1/O2 resolved: anonymous loopback read approved; msb-only for now. Implemented on `opencode/buildkit-builder-vm`; host KVM validation in progress and it falsified the build-time hypothesis — the registry push's per-layer materialization is invariant to changed-layer count (1m14s cold vs 1m22s one-layer), so the export vertex does not shrink; the win is architectural (no host archive, no `Image.Load`, pull-side layer dedupe).
  - Read If: Changing runner-image delivery, the registry push/pull path, `ImageInfo`/`WithImage` handling, `EnsureAvailable`/delivery, registry auth, runner-image prune scope, or test-binary registry hermeticity.

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
  - Description: Plan for `specs/login-shell-path.md`: the POSIX-sh profile.d merge script (+ sh behaviour tests), image wiring (`AGENTS_SANDBOX_IMAGE_PATH`, build-context `COPY`), removing the `/usr/local/bin` symlink stopgap, an end-to-end login-shell integration test, and docs/spec/CHANGELOG updates — implemented on `opencode/speed-up-image-builds` (PR #126).
  - Read If: Reviewing the login-shell PATH (mergePaths) work; the plan is complete.
- `2026-10-12-docker-test-harness.md`
  - Description: TDD plan for the (now retired) `specs/docker-test-harness.md`: extract a `backend` seam in `internal/sandbox/builder` (msb vs docker), a `dockerCLI` seam, a `dockerBackend` (`docker run moby/buildkit`), env-based selection, harness lifecycle/TestMain hook, porting the node/npm and login-shell tests to `docker run`, gating the msb-only tests, and Makefile/CI/docs. Implemented on `opencode/buildkit-builder-vm`, then superseded by `2026-10-13-builder-backends.md` (the harness and `docker` value were removed; moby is the KVM-free tier).
  - Read If: Reviewing the Docker test harness implementation or how the builder `backend` seam came to be.
- `2026-10-10-buildkit-builder-vm.md`
  - Description: TDD plan for `specs/buildkit-builder-vm.md` (issue #15): new `internal/sandbox/builder` package (shared builder VM lifecycle, host-side BuildKit client seam/readiness, solve+OCI export+metadata, gateway `ResolveImageConfig` base-digest route, registry auth), add the `moby/buildkit`/`docker/cli` deps, rewire `image` onto the builder, thread the archive through VM creation, BuildKit-cache pruning, remove `internal/sandbox/docker` + `doctor.CheckDocker`, integration + docs. Implemented on `opencode/buildkit-builder-vm` (all ten tasks). Resolved the spec's open decisions (OCI export, `~/.docker/config.json` creds, global cache) and overrode the spec's buildinfo base-digest route with the gateway resolver.
  - Read If: Reviewing or resuming the Docker-free BuildKit builder (issue #15 / `internal/sandbox/builder`).
- `2026-10-13-builder-backends.md`
  - Description: TDD plan for `specs/builder-backends.md`: persist the builder-VM host port for reuse, add the embedded per-user OCI cache registry (`distribution/v3`, token/0600, hidden `__builder-registry` serve mode, prune), wire `type=registry` cache + drop spike knobs/docker harness, restore the `moby` Engine-API `Builder`, add config+env backend selection at `builder.Get()`, gate `doctor`/prune per backend, retarget the moby integration tier, and update docs/spec/brain — implemented on `opencode/buildkit-builder-vm` (all ten tasks). Resolves the spec's open mechanics (selection surface, registry lifecycle, bind address, moby delivery).
  - Read If: Implementing or resuming the two-backend builder work (`specs/builder-backends.md`); also the source of the follow-up prune-scope and registry-bind validation items.
- `2026-10-14-registry-pull-delivery.md`
  - Description: TDD plan for `specs/registry-pull-delivery.md`: registry read auth + `PullRef`/`PushRef`, msb `Export` as a registry push with no archive, the `ImagePullRef`/`HoldRegistry` seam, the `image` delivery model (`ImageRef`/`SandboxImageOptions`/`EnsureAvailable`), threading `ImageInfo` through VM/volume creation, registry-qualified refs in naming/list/prune, and docs. Implemented on `opencode/buildkit-builder-vm` (all tasks); live msb/KVM validation is host-run.
  - Read If: Implementing or resuming registry-pull delivery (`specs/registry-pull-delivery.md`).
