---
description: The internal/sandbox package tree - microsandbox VM, BuildKit builder, image, session, state, volume, and related subsystems.
read_if: Working in internal/sandbox (VM lifecycle, image building, BuildKit builder/msb clients, volumes, pruning, state locking, sessions).
---

# Sandbox

## Coverage

- `internal/sandbox/` — all subpackages

## Architecture

- `internal/sandbox/` is the largest area of the codebase; it drives VM lifecycle, runner-image builds, microsandbox (msb) runtime and BuildKit `builder/` clients, volumes, pruning, sessions, and state locking.
- The `vm/` package is the biggest: it orchestrates project-VM creation/ensure, the dockerd sidecar, run execution, upgrades, worktree handling, and VM env/control.
- Cross-cutting dependencies: `msb/` (microsandbox SDK client) is the external runtime client and `builder/` drives the BuildKit builder VM for image builds; `naming/`, `options/`, `state/`, `network/`, `mounts/` provide shared configuration/locking primitives used across sandbox subpackages.
- Heavily test-driven (many `*_test.go` / `coverage_*.go` files per package); most integration seams are mockable via `testmock.go` / `main_test.go` per package.

## `internal/sandbox` Index

### `builder/` — runner-image builder backends (msb default, moby opt-in)

- `builder.go`, `vm.go`, `vmstate.go` — shared builder-VM lifecycle (ensure/start `buildkitd`, readiness probe, teardown, keep-alive); worker storage is a disk-backed owned volume, the layer cache lives in `cacheregistry/`; `vmstate.go` persists the builder-VM host port so a running VM is rediscovered and reused
- `client.go`, `build.go`, `auth.go` — host-side BuildKit client: `Solve` the rendered Dockerfile, OCI export, gateway base-digest resolution, `type=registry` cache import/export, registry auth (host docker config + embedded cache-registry token)
- `selection.go`, `backend.go` — `Get()`/`SelectedName()`: resolve `msb` (default) or `moby` from viperconfig `builder`/`AGENTS_SANDBOX_BUILDER`; the buildkit-only `backend` interface (`msbBackend`) stays internal
- `moby.go`, `mobycontext.go` — opt-in Docker Engine API backend (`ImageBuild` → `ImageSave` archive loaded via the shared `EnsureLoaded`); no `msb.Client`, no buildkit
- `cacheregistry/` — embedded per-user OCI cache registry (`distribution/v3`): `Ensure` a daemon endpoint, deterministic `CacheRef`, token auth, prune
- `testmock.go`, `*_test.go` — mocks and tests

### `doctor/` — Environment diagnostics and shell rc

- `doctor.go`, `install.go`, `shell_rc.go` — health checks (platform virtualization + msb runtime, plus a Docker check gated on the selected `moby` backend) and shell setup
- `doctor_darwin.go`, `doctor_linux.go` — platform variants

### `image/` — Runner image build, Dockerfile, fetch, inspect, tags

- `build.go`, `dockerfile.go`, `fetch.go`, `format.go`, `image.go`, `inspect.go`, `list.go`, `tags.go`, `embed.go` — image pipeline (rendering, identity/cache decision; delegates the actual build to `builder/`)
- `integration_build_test.go` — `//go:build integration` end-to-end tests for the real build pipeline and cache reuse. Two tiers: `make test-integration` (moby backend against a Docker daemon, `AGENTS_SANDBOX_BUILDER=moby`, no `/dev/kvm`; CI `integration` job on every PR) and `make test-integration-msb` (msb backend, registry cache + shared builder VM, needs a KVM host with msb and network access; gated CI `integration-msb` job, main/release/manual). **The moby tier cannot run inside the agents-sandbox VM** (microsandbox egress TLS interception breaks in-build registry pulls); the msb tier needs `/dev/kvm`.

- Custom-base requirements (shadow-utils, POSIX shell bash→zsh→sh, curl+tar, dind prereqs) and unsupported bases (Alpine/distroless/scratch) are documented in `docs/runner-image.md`.

### `mounts/` — Filesystem mount handling

- `mounts.go`

### `msb/` — Microsandbox SDK client

- `msb.go`, `stream.go` — msb client and stream handling

### `naming/` — Naming, labels, artifacts

- `artifact.go`, `labels.go`, `naming.go`

### `network/` — Network policy/config

- `network.go`

### `options/` — Sandbox options, sizes, timeouts, worktree spec

- `options.go`, `sizes.go`, `timeouts.go`, `worktree_spec.go`

### `pruning/` — Cleanup and prune of images, sandboxes, volumes

- `cleanup.go`, `helpers.go`, `images.go`, `prune.go`, `report.go`, `sandboxes.go`, `state.go`, `volumes.go`

### `reprovision/` — Reconfig and reprovision planning

- `config_files.go`, `plan.go`, `prompts.go`, `reprovision.go`, `secrets.go`

### `runtime/` — Runtime selection

- `runtime.go`

### `session/` — Session run and reaper

- `run.go`, `reaper.go`

### `state/` — State locking (flock, clientlock, claims)

- `claim.go`, `clientlock.go`, `flock.go`, `state.go`

### `vm/` — VM lifecycle and orchestration (largest)

- `daemon.go`, `dockerd.go`, `exit.go`, `list.go`, `provenance.go`, `reconfig.go`, `run_envstate.go`, `run_orchestrate.go`, `upgrade.go`, `upgrade_state.go`, `vm_control.go`, `vm_env.go`, `vm_lifecycle.go`, `vm_name.go`, `vm_resources.go`, `worktree.go`
- `create_project_vm_test.go`, `ensure_project_vm_test.go`, `prepare_sandbox_test.go`, `setupsandbox_test.go` — lifecycle setup tests

### `volume/` — Volume management

- `list.go`, `operations.go`, `prompt.go`, `state.go`, `volume.go`
