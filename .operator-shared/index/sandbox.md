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

### `image/` — Runner image build, Dockerfile, fetch, inspect, tags

- `build.go`, `dockerfile.go`, `fetch.go`, `format.go`, `image.go`, `inspect.go`, `list.go`, `tags.go`, `embed.go` — image pipeline
- `render_integration_test.go` — `//go:build integration` docker-build tests for `RenderDockerfile` composition (no Dockerfile / managed base / custom Fedora base, each ± dind). Run via `make test-integration`; wired into CI as a selective job (main/release/manual). **Cannot run inside the agents-sandbox VM** — microsandbox's egress-inspection CA makes in-image downloads (apt/dnf/node) fail; must run on a host with a normal docker daemon.
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
