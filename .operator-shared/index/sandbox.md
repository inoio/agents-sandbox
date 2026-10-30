---
description: The internal/sandbox package tree - microsandbox VM, docker, image, session, state, volume, and related subsystems.
read_if: Working in internal/sandbox (VM lifecycle, image building, docker/msb clients, volumes, pruning, state locking, sessions).
---

# Sandbox

## Coverage

- `internal/sandbox/` — all subpackages

## Architecture

- `internal/sandbox/` is the largest area of the codebase; it drives VM lifecycle, runner-image builds, docker & microsandbox (msb) clients, volumes, pruning, sessions, and state locking.
- The `vm/` package is the biggest: it orchestrates project-VM creation/ensure, the dockerd sidecar, run execution, upgrades, worktree handling, and VM env/control.
- Cross-cutting dependencies: `msb/` (microsandbox SDK client) and `docker/` are the external system clients; `naming/`, `options/`, `state/`, `network/`, `mounts/` provide shared configuration/locking primitives used across sandbox subpackages.
- Heavily test-driven (many `*_test.go` / `coverage_*.go` files per package); most integration seams are mockable via `testmock.go` / `main_test.go` per package.

## `internal/sandbox` Index

### `docker/` — Docker client wrapper and guards

- `docker.go`, `dockercontext.go` — Docker client/context
- `testmock.go`, `*_test.go` — mocks and tests

### `doctor/` — Environment diagnostics and shell rc

- `doctor.go`, `install.go`, `shell_rc.go` — health checks and shell setup
- `doctor_darwin.go`, `doctor_linux.go` — platform variants

### `envsecret/` — Host env-var files and secret specs

- `env.go`, `secrets.go` — loads host environment variable files and secret specs

### `image/` — Runner image build, Dockerfile, fetch, inspect, tags

- `build.go`, `dockerfile.go`, `fetch.go`, `format.go`, `image.go`, `inspect.go`, `inspect_detail.go`, `list.go`, `tags.go`, `embed.go` — image pipeline (`inspect_detail.go` is `InspectImage`: the image-inspect detail seam)
- `data/` — build-context assets embedded into the image: the managed-base `Dockerfile`, `agents-sandbox-path.sh` (profile.d `PATH` merge, 0644), and `msb-trust.sh` (the vendored CA-trust helper, installed 0755; `msb-trust.LICENSE` beside it records provenance and is neither embedded nor shipped; see `specs/ca-trust-helper.md`)
- `references.go` — `InUseImageReferences`: the fail-closed in-use runner-image guard, unioning the project-state scan with kept sandbox image refs (Slice 1 of the runtime-management epic)
- `render_integration_test.go` — `//go:build integration` docker-build tests for `RenderDockerfile` composition (no Dockerfile / managed base / custom Fedora base, each ± dind). Run via `make test-integration`; wired into CI as a selective job — main and release tags, manual dispatch, and a PR labeled `run-integration` (the gating contract is `specs/ci-job-gating.md`). **In the agents-sandbox VM, microsandbox's egress-inspection CA makes in-image downloads (apt/dnf/node) fail** unless each base image is replaced by a CA-trusting rebuild first (the `AGENTS.md` workaround covers `debian:trixie-slim`, so the managed-base cases run; the custom-base cases need the same for `fedora:latest`).
- Custom-base requirements (shadow-utils, POSIX shell bash→zsh→sh, curl+tar, dind prereqs) and unsupported bases (Alpine/distroless/scratch) are documented in `docs/runner-image.md`.

### `mounts/` — Filesystem mount handling

- `mounts.go`
- `fuzz_test.go` — `FuzzResolveMountTarget`: a configured guest target must never equal or shadow a managed mount (`/workspace`, `/tmp`, `/home/dev`)

### `msb/` — Microsandbox SDK client

- `msb.go`, `stream.go` — msb client and stream handling; also storage observation (`StorageUsage`) and the `IsUnsupportedOperation` classifier

### `naming/` — Naming, labels, artifacts

- `artifact.go`, `labels.go`, `naming.go`

### `network/` — Network policy/config

- `network.go`

### `options/` — Sandbox options, sizes, timeouts, worktree spec

- `options.go`, `sizes.go`, `timeouts.go`, `worktree_spec.go`
- `fuzz_test.go` — `FuzzParseOptions`: size-spec parsing against an independent overflow oracle, and `--worktree` slug validation

### `pruning/` — Cleanup and prune of images, sandboxes, volumes

- `cleanup.go`, `helpers.go`, `images.go`, `prune.go`, `report.go`, `sandboxes.go`, `state.go`, `storage.go`, `volumes.go` — `storage.go` is the age-gated runtime-RAM phase (`PruneStorage`) that enriches the top-level `prune` (#156)

### `reconfig/` — Reconfiguration planning

- `plan.go`, `prompts.go` — plans and resolves a project VM's reconfiguration (fingerprint diff → recreate vs restart) and its prompt

### `reprovision/` — Reconfig and reprovision planning

- `config_files.go`, `plan.go`, `prompts.go`, `reprovision.go`, `secrets.go`

### `runtime/` — Runtime selection

- `runtime.go`

### `runtimemigration/` — Legacy runtime-home copy campaign

- `copy.go`, `legacy.go`, `notice.go`, `offer.go` — the opt-in legacy home-volume copy campaign (host-owned `migrations` registry) and the post-migration legacy-runtime notice

### `session/` — Session run and reaper

- `run.go`, `reaper.go`

### `state/` — State locking (flock, clientlock, claims) and project state

- `claim.go`, `clientlock.go`, `flock.go`, `state.go`, `root.go`, `fingerprint.go` (`state.go`/`root.go` hold the pure `parseHomeState`/`parseRootState` unmarshal steps the readers delegate to)
- `move.go` — `SlugDir` and `MoveSlugDir`: a project's state slug directory, and the recursive merge that migrates a legacy origin-keyed state onto its checkout slug, returning the source-relative paths it could not move (#144).
- `references.go` — fail-closed project-state reference scan: `HomeVolumeReferences` and `ImageReferences` (the image analogue), both built on the shared `projectStateReferences` walk
- `fuzz_test.go` — `FuzzParseState`: both state documents must never panic, classify malformed bytes as `ErrStateUnparsable`, and serialize stably (a re-read of the writer's output yields the same bytes)

### `storage/` — Private-home storage accounting seam (Slice 2; backs `df` in Slice 5)

- `storage.go` — `Observe`: the six microsandbox storage categories (snake_case names, msb order, with `InUse`) plus the runtime host category; nil counters stay unknown, `ErrUnsupportedOperation` degrades to unknown with a report-level note.
- `host.go` — walks the private home's `bin`/`lib` runtime/FFI artifacts, summing regular files.

### `vm/` — VM lifecycle and orchestration (largest)

- `daemon.go`, `dockerd.go`, `detached.go`, `exit.go`, `inspect.go`, `list.go`, `provenance.go`, `reconfig.go`, `run_envstate.go`, `run_orchestrate.go`, `upgrade.go`, `upgrade_state.go`, `vm_control.go`, `vm_env.go`, `vm_lifecycle.go`, `vm_name.go`, `vm_resources.go`, `worktree.go` (`inspect.go` is `InspectSandbox`: the sandbox-inspect detail seam; `run_orchestrate.go` holds `resolveRunTarget`, the single place that resolves the identity, adopts a legacy one and builds the state key; `daemon.go`/`dockerd.go` launch the serve daemon and dockerd as runtime-owned detached jobs, with `detached.go` holding the shared readiness/kill helpers)
- `adopt.go` — `AdoptLegacyIdentity`/`IdentityAdoption`: the #144 first-run migration (destroy the legacy repo-slug VM, then merge the state onto the checkout slug) and the notice block, including the dry-run variant that stays on the legacy slug; the lost-state VM-only arm records the destroyed VM's home volume in the checkout state (`recovered_home_volume`) before the destroy, and keeps the trigger live until the legacy VM is gone.
- `create_project_vm_test.go`, `ensure_project_vm_test.go`, `prepare_sandbox_test.go`, `setupsandbox_test.go` — lifecycle setup tests
- `fuzz_test.go` — `FuzzParseLegacyUpdaterState`: the retired `updater.yaml` reader is panic-free and folds a flat `last_checked` into the opencode agent entry

### `volume/` — Volume management

- `list.go`, `inspect.go`, `operations.go`, `prompt.go`, `remove.go`, `state.go`, `volume.go` (`inspect.go` is `InspectVolume`: the volume-inspect detail seam; `state.go` holds `ResolveHomeVolume`/`EnsureNewHome`, including recovery of the live VM's mounted volume when the state file is lost)
