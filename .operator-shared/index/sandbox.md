---
description: The internal/sandbox package tree - microsandbox VM, docker, image, session, state, volume, and related subsystems.
read_if: Working in internal/sandbox (VM lifecycle, image building, docker/msb clients, volumes, pruning, state locking, sessions).
---

# Sandbox

## Coverage

- `internal/sandbox/` — all subpackages

## Architecture

- `internal/sandbox/` is the largest area of the codebase; it drives VM lifecycle, runner-image builds, docker & microsandbox (msb) clients, volumes, pruning, sessions, and state locking.
- A run is layered `session/` → `prepare/` → `vm/` (`specs/architecture/package-boundaries.md`): `prepare/` runs the stages that ready the project VM and makes the version/reconfig/home decisions; `vm/` holds the capabilities that act on a project VM (create/ensure, setup with the dockerd sidecar and agent daemon, worktree handling, adoption, VM env/control).
- Cross-cutting dependencies: `msb/` (microsandbox SDK client) and `docker/` are the external system clients; `naming/`, `options/`, `state/`, `network/`, `mounts/` provide shared configuration/locking primitives used across sandbox subpackages.
- Heavily test-driven (many `*_test.go` / `coverage_*.go` files per package); most integration seams are mockable via `testmock.go` / `main_test.go` per package.

## `internal/sandbox` Index

### `docker/` — Docker client wrapper and guards

- `docker.go`, `dockercontext.go` — Docker client/context
- `testmock.go`, `*_test.go` — mocks and tests

### `doctor/` — Environment diagnostics and prerequisite fixes

- `doctor.go`, `install.go` — health checks and prerequisite fixes
- `doctor_darwin.go`, `doctor_linux.go` — platform variants

### `envsecret/` — Host env-var files and secret specs

- `env.go`, `secrets.go` — loads host environment variable files and secret specs

### `image/` — Runner image build, Dockerfile, fetch, inspect, tags

- `build.go` (options, `ImageInfo`, build plan, `EnsureImage`/`Build`), `from_stages.go` (FROM/stage parsing), `docker_build.go` (Docker build, build context, failure classification), `load.go` (`EnsureLoaded` into msb), `base_digest.go` (base-image digest/pull), `fetch.go`, `image.go`, `format.go`, `inspect.go`, `inspect_detail.go` (`InspectImage`), `list.go`, `remove.go`, `references.go` (`InUseImageReferences`: the fail-closed in-use guard), `tags.go`, `embed.go` — image build/inspect/list/remove pipeline
- `dockerfile.go`, `dockerfile_lines.go`, `instructions.go`, `project_dockerfile.go`, `warn.go` — `RenderDockerfile` stage composition and the project-Dockerfile parse/docker-decision (`specs/runner-image/image-composition.md`)
- `preview.go` — `PreviewImage`: the `--dry-run` image plan (`specs/architecture/dry-run-preview.md`)
- `data/` — build-context assets embedded into the image: the managed-base `Dockerfile`, `agents-sandbox-path.sh` (profile.d `PATH` merge, 0644), `msb-trust.sh` (the vendored CA-trust helper, installed 0755; `msb-trust.LICENSE` beside it records provenance and is neither embedded nor shipped; see `specs/runner-image/ca-trust-helper.md`), and `xdg-open.sh` (the in-VM opener shim, installed 0755; see `guides/agent-link-opening.md`)
- `render_integration_test.go` — `//go:build integration` docker-build tests for `RenderDockerfile` composition (no Dockerfile / managed base / custom Fedora base, each ± dind). Run via `make test-integration`; wired into CI as a selective job — main and release tags, manual dispatch, and a PR labeled `run-integration` (the gating contract is `specs/ci/ci-pipeline.md`). **In the agents-sandbox VM, microsandbox's egress-inspection CA makes in-image downloads (apt/dnf/node) fail** unless each base image is replaced by a CA-trusting rebuild first (the `AGENTS.md` workaround covers `debian:trixie-slim`, so the managed-base cases run; the custom-base cases need the same for `fedora:latest`).
- Custom-base requirements (shadow-utils, POSIX shell bash→zsh→sh, curl+tar, dind prereqs) and unsupported bases (Alpine/distroless/scratch) are documented in `docs/runner-image.md`.

### `lifecycle/` — Run lifecycle vocabulary and trace

- `lifecycle.go` — the `StageID` constants and `Order()` of `specs/vm/lifecycle.md` (`TestOrderMatchesSpec` parses the spec's stage table), the nil-safe `Trace` recorder, and `Run`, the loop that runs an ordered `Stage` list and records each stage. No stage logic: `cmd`, `prepare` and `session` own their stages.

### `mounts/` — Filesystem mount handling

- `mounts.go`
- `fuzz_test.go` — `FuzzResolveMountTarget`: a configured guest target must never equal or shadow a managed mount (`/workspace`, `/tmp`, `/home/dev`)

### `msb/` — Microsandbox SDK client

- The launcher's port over the microsandbox SDK (`specs/architecture/msb-seam.md`, #196): provider-owned role interfaces (`RuntimeInstaller`, `SandboxStore`, `VolumeStore`, `ImageStore`, `StorageStore`, `SandboxIdentity`, `SandboxLifecycleHandle`, `SandboxConfigReader`, `SandboxResourceControl`, `SandboxExecutor`, `SandboxStreamer`, `SandboxAttacher`, `SandboxJobs`, `SandboxObserver`, `SandboxLifecycle`) composed into the `Client`/`SandboxHandle`/`Sandbox` composites; consumers narrow to the role they use. `roles_boundary_test.go` pins `composite method set == union of roles`.
- `msb.go` (package doc, `Get` factory, runtime seams), `client.go` (`Client` + `realMsbClient` + option seams), `sandbox.go` (`Sandbox`/`SandboxHandle` + reals), `job.go` (`Job`/`ShellResult`/`Stream*`), `volume.go`, `image.go` (`ImageHandle`/`ImageDetail`), `fs.go`, `errors.go` (the `Is*` classifiers), `status.go` (`VMStatusKind`, `GetVMStatus`) — the split introduced by #196 Phase 1.
- `requests.go` — launcher-owned request types (`ExecRequest`, `AttachRequest`, `JobSpec`, `JobQuery`, `LifecycleOptions`, `LogQuery`+`LogSource`, `CompactionRequest`); `translate.go` — one translator per option family plus the reflection projection registers. #196 Phase 2 makes the option-taking methods (`Shell`/`Exec`/`ShellStream`, `AttachWith`, `ExecDetached`, `ListJobs`, `Stop`/`Kill`/`Destroy`, `Logs`, `Compact`) accept these instead of `...msbSdk.*Option`.
- `storage_values.go` / `observation_values.go` / `image_values.go` / `enums.go` — the launcher-owned return types and enums (#196 Phase 3): `StorageReport`/`PruneRequest`/`PruneReport`, `JobInfo`/`JobLogEntry`/`JobLogQuery`/`LogEntry`/`Metrics`, `ImageConfig`/`ImageLayer`/`ImagePruneReport`, and string-backed `SandboxStatus`/`BackendKind`/`VolumeKind`. `phase3_boundary_test.go` pins that `storage`/`pressure`/`resourceproposal`/`image`/`session` do not import the SDK; `Modify`/`SandboxConfig`-family/compaction result stay SDK until Phase 4 (`specs/architecture/msb-seam.md`).
- `configmounts.go` — `ParseConfigMounts` / `SandboxHandle.ConfigMounts()`: the live-mount read-back the Go SDK drops (`specs/vm/live-mount-readback.md`)
- `testmock.go` — `MockMsbClient` (the wide composite double; fields stay on the outer struct), `testmock_roles.go` — standalone role doubles (`MockImageStore`, `MockSandboxStore`, …) for narrower tests; `*_msb_test.go` — `//go:build msb` real-runtime tests

### `naming/` — Naming, labels, artifacts

- `artifact.go`, `labels.go`, `naming.go`

### `network/` — Network policy/config

- `network.go`

### `options/` — Sandbox options, sizes, timeouts, worktree spec, run mode

- `mode.go`, `options.go`, `sizes.go`, `timeouts.go`, `worktree_spec.go`
- `fuzz_test.go` — `FuzzParseOptions`: size-spec parsing against an independent overflow oracle, and `--worktree` slug validation

### `prepare/` — Run preparation: stages resolve-identity through setup-sandbox

- `prepare.go` — `PrepareSandbox`, the ordered `lifecycle.Stage` list of the prepare-owned stages with one `…Stage` step per stage (`resolveIdentityStage` is the single place that resolves the identity, adopts a legacy one and builds the state key), and the returned `Session`
- `preview.go` — `PreviewSandbox`/`Preview`: the read-only `--dry-run` variant of the same stages
- `upgrade.go`, `upgrade_state.go`, `upgrade_migration.go` — the resolve-version decision (`resolveBuildVersion`, the upgrade prompt, `ResolveAgentVersionForBuild` shared with `build`) and the legacy `updater.yaml` fold
- `reconfig.go` — the plan-reconfig decision: `readLiveProjectVM`, `resolveReconfigDecision`, `applyReconfigHomeAction`
- `run_envstate.go`, `shared_git_dir_mount.go` — fingerprint persistence and the shared-git-dir mount for an outside-worktree checkout
- `fuzz_test.go` — `FuzzParseLegacyUpdaterState`: the retired `updater.yaml` reader is panic-free and folds a flat `last_checked` into the opencode agent entry

### `pruning/` — Cleanup and prune of images, sandboxes, volumes

- `cleanup.go`, `compact.go`, `helpers.go`, `images.go`, `liveness.go`, `prune.go`, `report.go`, `sandboxes.go`, `storage.go`, `volumes.go` — `liveness.go` holds `PruneState`, its builder and the liveness rules (`prunable`, `retires`, `keepImage`, `allowsNativeImagePrune`) of `specs/vm/lifecycle.md`; `compact.go` backs `sandbox compact`; `storage.go` is the age-gated runtime-RAM phase (`PruneStorage`) that enriches the top-level `prune`

### `reconfig/` — Reconfiguration planning

- `plan.go`, `prompts.go` — plans and resolves a project VM's reconfiguration (fingerprint diff → recreate vs restart) and its prompt

### `reprovision/` — Reconfig and reprovision planning

- `config_files.go`, `dependency_warning.go`, `reprovision.go` — materializes the agent config-merge subject and provisions the worktree's config files

### `runtime/` — Runtime selection and private runtime home

- `home.go`, `runtime.go` — runtime selection plus the private runtime home (`PrivateHome`, home migration) of `specs/volumes/runtime-home-decoupling.md`

### `runtimemigration/` — Legacy runtime-home copy campaign

- `copy.go`, `legacy.go`, `notice.go`, `offer.go` — the opt-in legacy home-volume copy campaign (host-owned `migrations` registry) and the post-migration legacy-runtime notice

### `session/` — Session run and reaper

- `run.go`, `reaper.go`, `host_opener.go` — the run/shell session loop, the idle reaper, and the host-opener channel wiring (`internal/opener`)

### `state/` — State transactions, locking, and project state

- `state.go`, `root.go`, `schema.go` — the `HomeState`/`RootState` documents (`state.go`/`root.go` hold the pure `parseHomeState`/`parseRootState` unmarshal steps the readers delegate to)
- `home_txn.go`, `root_txn.go` — the locked transaction API (`specs/state/state-model.md`): `WithHomeState`/`RecoverHomeState`/`WithRootState`/`RemoveHomeStateIf` plus the lockless reads
- `homelock.go`, `rootlock.go`, `lockkeys.go`, `lockregistry.go`, `projectvmlock.go` — the per-document lock files and the per-project VM lock (`ProjectVMLockDir` → `ensure-vm.lock`)
- `claim.go`, `clientlock.go` — client leases and claims
- `fingerprint.go`, `agent_version.go`, `root_repair.go` — the config fingerprint, the `agent_version` baseline writer (`specs/state/upgrade-state.md`), and root-state repair
- `move.go` — `SlugDir` and `MoveSlugDir`: a project's state slug directory, and the recursive merge that migrates a legacy origin-keyed state onto its checkout slug, returning the source-relative paths it could not move (#144).
- `references.go` — fail-closed project-state reference scan: `HomeVolumeReferences` and `ImageReferences` (the image analogue), both built on the shared `projectStateReferences` walk
- `testmock.go`, `fuzz_test.go` — mock state and `FuzzParseState`: both state documents must never panic, classify malformed bytes as `ErrStateUnparsable`, and serialize stably (a re-read of the writer's output yields the same bytes)

### `storage/` — Private-home storage accounting seam (backs `df`)

- `storage.go` — `Observe`: the six microsandbox storage categories (snake_case names, msb order, with `InUse`) plus the runtime host category; nil counters stay unknown, `ErrUnsupportedOperation` degrades to unknown with a report-level note.
- `host.go` — walks the private home's `bin`/`lib` runtime/FFI artifacts, summing regular files.

### `vm/` — Project-VM capabilities (largest)

- `daemon.go`, `dockerd.go`, `detached.go`, `detached_service.go`, `exit.go`, `inspect.go`, `list.go`, `remove.go`, `provenance.go`, `setup_sandbox.go`, `vm_control.go`, `vm_env.go`, `vm_lifecycle.go`, `vm_name.go`, `vm_resources.go`, `worktree.go` (`inspect.go` is `InspectSandbox`: the sandbox-inspect detail seam; `vm_lifecycle.go` holds `EnsureProjectVM`, `vm_name.go` `FindProjectVM`/`ProjectVMName`/`ProjectVMHomeVolume`, `setup_sandbox.go` `SetUpSandbox` (config files, startup hooks, dockerd, agent daemon, attach target); `remove.go` holds `RemoveSandbox`; `daemon.go`/`dockerd.go` launch the serve daemon and dockerd as runtime-owned detached jobs, with `detached.go` the shared readiness/kill helpers and `detached_service.go` the ensure-detached-service recipe)
- `adopt.go` — `AdoptLegacyIdentity`/`IdentityAdoption`: the #144 first-run migration (destroy the legacy repo-slug VM, then merge the state onto the checkout slug) and the notice block, including the dry-run variant that stays on the legacy slug; the lost-state VM-only arm records the destroyed VM's home volume in the checkout state (`recovered_home_volume`) before the destroy, and keeps the trigger live until the legacy VM is gone.
- `create_project_vm_test.go`, `ensure_project_vm_test.go`, `setupsandbox_test.go` — lifecycle setup tests

### `volume/` — Volume management

- `commit.go`, `inspect.go`, `list.go`, `operations.go`, `preview.go`, `prompt.go`, `remove.go`, `state.go`, `throwaway.go`, `volume.go` (`inspect.go` is `InspectVolume`: the volume-inspect detail seam; `state.go` holds `ResolveHomeVolume`/`EnsureNewHome`, including recovery of the live VM's mounted volume when the state file is lost)
