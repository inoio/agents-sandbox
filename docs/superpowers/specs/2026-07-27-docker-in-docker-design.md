# Docker-in-Docker support

## Problem

Agents running inside the microsandbox VM occasionally need to build Docker
images or run Docker containers — for instance to test a service, run a
containerized toolchain, or verify a Dockerfile. The current runner image
ships no Docker daemon, so these workflows are impossible without leaving the
sandbox.

Research confirmed that Docker-in-Docker works inside a microsandbox VM when
the `vfs` storage driver is used. The default `overlay2` driver fails because
the sandbox root filesystem is already an overlay and nested overlay mounts are
rejected by the microVM kernel. `vfs` avoids this by copying layers instead of
mounting them; the trade-off is slower I/O, which is acceptable for
development workflows.

## Goals

- Agents can build and run Docker containers inside the microsandbox VM.
- Docker availability is a single, unambiguous toggle controlled by the
  project Dockerfile's `FROM` line — no separate CLI flag or config option.
- Projects that do not need Docker pay no cost (no larger image, no daemon
  startup).
- Existing projects that do not use a custom Dockerfile continue to work
  unchanged with the plain base image.

## Non-goals

- No CLI flag or launcher-config setting to enable Docker at runtime. The
  `FROM` line is the only toggle.
- No support for storage drivers other than `vfs`. `overlay2` does not work
  inside the microsandbox VM; `devicemapper` and `fuse-overlayfs` are out of
  scope for the MVP.
- No privilege escalation or custom kernel modules. Docker runs as a regular
  daemon inside the VM using its default capabilities.

## Architecture

### Single toggle: the Dockerfile `FROM` line

Docker availability is determined entirely by which base image a project
Dockerfile extends:

| Scenario | Docker? | How |
|----------|---------|-----|
| No project Dockerfile | No | Launcher uses embedded `Dockerfile` (plain base) |
| Project extends `runner-base:latest` | No | Plain base only |
| Project extends `runner-base-dind:latest` | Yes | Dind base ships Docker CE; daemon auto-starts |

There is no `--docker` flag and no `docker` config key. This avoids a
confusing two-mechanism toggle where a flag only applies in some contexts.

### Two published base images

1. **`opencode-msb/runner-base:latest`** — current Debian trixie-slim image
   with standard tools (curl, git, nodejs, opencode, etc.). Unchanged.

2. **`opencode-msb/runner-base-dind:latest`** — extends `runner-base`,
   installs Docker CE (daemon + CLI), writes `/etc/docker/daemon.json` with
   `{"storage-driver":"vfs"}`, and adds the `dev` user to the `docker`
   group.

### Two embedded Dockerfiles

- `internal/sandbox/data/Dockerfile` — current, unchanged.
- `internal/sandbox/data/Dockerfile.dind` — `FROM opencode-msb/runner-base:latest`,
  installs Docker CE, configures `vfs` storage driver, adds `dev` to `docker`
  group.

### Base image detection

The launcher already parses project Dockerfiles to detect `FROM` references
to `runner-base` (see `ReferencesBase` in `internal/sandbox/image.go`).
This pattern is extended:

- `ReferencesBase` — detects `FROM opencode-msb/runner-base:latest` (existing).
- `ReferencesDindBase` — detects `FROM opencode-msb/runner-base-dind:latest` (new).

`EnsureImage` uses these to rebuild the correct base image on `--rebuild`:
if the project Dockerfile references `runner-base-dind`, the dind base is
(re)built first, then the project image.

### Dockerd startup

After the VM starts, the launcher checks whether the `dockerd` binary is
present in the image. If present, it starts `dockerd` with `vfs` storage
driver before attaching to opencode. If absent, no Docker daemon is started.

The startup is **sequential**: `dockerd` is started, the launcher waits for
the Docker socket to be ready, and only then attaches to opencode. This
ensures that any `dockerd` startup failure is surfaced immediately with a
clear error before the agent session begins. Parallelizing `dockerd` and
opencode startup was considered and rejected: the ~3-5 seconds saved is
minor, while the failure modes (agents hitting a not-yet-ready daemon, or
silent `dockerd` failures discovered only when a `docker` command runs) are
meaningfully worse.

The `vfs` storage driver is pre-configured via `/etc/docker/daemon.json`
baked into the dind image, so the launcher only needs to start `dockerd`
and poll for socket readiness.

This keeps Docker startup automatic and transparent: if the image supports
Docker, it just works; if not, nothing happens.

### Image build flow

`EnsureImage` in `internal/sandbox/image.go` builds base images before the
project image. The current logic rebuilds `runner-base` when `--rebuild` is
set or the project Dockerfile references it. This is extended to handle the
dind base.

Build decisions by project Dockerfile:

| Project Dockerfile | Build `runner-base`? | Build `runner-base-dind`? |
|---|---|---|
| `FROM runner-base:latest` | yes (existing) | no |
| `FROM runner-base-dind:latest` | yes (dind extends it) | yes |
| (no project Dockerfile) | no | no |

The dind image's `Dockerfile.dind` uses `FROM opencode-msb/runner-base:latest`,
so `runner-base` is always built first when dind is needed. `runner-base-dind`
is built from the embedded `Dockerfile.dind` and tagged
`opencode-msb/runner-base-dind:latest`.

Detection reuses the existing `ReferencesBase` pattern:

- `ReferencesBase` — scans `FROM` lines for `opencode-msb/runner-base:latest`.
  Uses `strings.Contains`, so it returns false for `runner-base-dind:latest`
  (the substring `runner-base:latest` does not appear).
- `ReferencesDindBase` — scans `FROM` lines for
  `opencode-msb/runner-base-dind:latest` (new).

### Dockerd startup at VM boot

After the sandbox VM is created and provisioned, but before opencode is
launched, the launcher performs a single `sb.Shell` call that conditionally
starts `dockerd`:

```bash
test -x /usr/bin/dockerd && \
  dockerd -H unix:///var/run/docker.sock > /var/log/dockerd.log 2>&1 &
```

If `dockerd` is not present (plain base image), the `test` fails and nothing
happens. If it is present (dind base image), `dockerd` starts in the
background with the `vfs` storage driver (pre-configured in
`/etc/docker/daemon.json` baked into the image).

The launcher then polls for socket readiness (e.g. `docker info` succeeding)
before proceeding to attach opencode. This sequential approach ensures that
`dockerd` startup failures are surfaced immediately with a clear error.

This approach is preferred over image-label detection because:

- **No label convention to maintain.** The image is self-describing: if
  `dockerd` is installed, Docker works; if not, it doesn't.
- **No drift.** A label is a parallel source of truth that can fall out of
  sync with the actual image contents (e.g. someone removes Docker packages
  in a downstream Dockerfile). Checking the binary is always accurate.
- **No changes to `EnsureImage`.** The image build pipeline is unaffected;
  detection happens at runtime in the VM where Docker actually runs.

### User privileges and dockerd startup

`dockerd` requires root to start, but the agent session must never have root
access. The microsandbox SDK supports per-command user overrides via
`WithExecUser`, independent of the sandbox-level `WithUser`:

1. Sandbox is created with `WithUser("dev")` — unchanged.
2. Launcher starts `dockerd` as root:
   `sb.Shell(ctx, "test -x /usr/bin/dockerd && dockerd ...", msb.WithExecUser("root"))`
3. Launcher polls for socket readiness as `dev`:
   `sb.Shell(ctx, "docker info", msb.WithExecUser("dev"))`
4. Launcher attaches opencode as `dev` — unchanged.

The agent never runs as root and has no escalation path. No `sudo` is
installed in the image. Only the launcher's one-shot startup command uses
`WithExecUser("root")`.

For the `dev` user to access `/var/run/docker.sock` at runtime, the
`Dockerfile.dind` adds the `dev` user to the `docker` group. This is an
image-level concern, not a launcher concern.

### Embedded `Dockerfile.dind`

The dind Dockerfile extends `runner-base` and installs Docker CE. It switches
to `root` for installation (the base image ends with `USER dev`), then back
to `dev` at the end:

```dockerfile
FROM opencode-msb/runner-base:latest

USER root

RUN apt-get update && \
    apt-get install -y --no-install-recommends \
        ca-certificates gnupg && \
    install -m 0755 -d /etc/apt/keyrings && \
    curl -fsSL https://download.docker.com/linux/debian/gpg -o /etc/apt/keyrings/docker.asc && \
    chmod a+r /etc/apt/keyrings/docker.asc && \
    echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/debian trixie stable" > /etc/apt/sources.list.d/docker.list && \
    apt-get update && \
    apt-get install -y --no-install-recommends docker-ce docker-ce-cli && \
    rm -rf /var/lib/apt/lists/*

RUN mkdir -p /etc/docker && \
    echo '{"storage-driver":"vfs"}' > /etc/docker/daemon.json

RUN usermod -aG docker dev

USER dev
WORKDIR /workspace
```

The dind image is tagged `opencode-msb/runner-base-dind:latest` and embedded
as `internal/sandbox/data/Dockerfile.dind` (via `//go:embed`).

### Testing

Tests follow existing patterns in `internal/sandbox/image_test.go`:

**Unit tests (no Docker/msb required):**

- `ReferencesDindBase` detects `FROM opencode-msb/runner-base-dind:latest` in
  a project Dockerfile (positive, negative, commented-out cases — mirroring
  the existing `ReferencesBase` tests).
- `ReferencesBase` returns false for a Dockerfile referencing
  `runner-base-dind:latest` (the `strings.Contains` check must not produce a
  false positive).
- `EnsureImage` builds the correct base images:
  - Project Dockerfile referencing `runner-base-dind` triggers builds of both
    `runner-base` and `runner-base-dind` before the project image.
  - Project Dockerfile referencing `runner-base` does not trigger a dind base
    build.
  - No project Dockerfile triggers no base builds.
- Dockerd startup logic: when `dockerd` binary is present in the VM, the
  startup command runs as root; when absent, the command is a no-op. This is
  tested via the shell command construction, not a live VM.

**Integration tests (require msb, skipped in CI without it):**

- Build `runner-base-dind` image, start a sandbox, verify `dockerd` starts
  and `docker run --rm hello-world` succeeds.
- Verify plain base image does not start `dockerd` (command exits as no-op).

## Changes summary

### New files

- `internal/sandbox/data/Dockerfile.dind` — embedded dind Dockerfile (extends
  `runner-base`, installs Docker CE, configures `vfs`, adds `dev` to `docker`
  group).

### Modified files

- `internal/sandbox/data.go` — add `//go:embed data/Dockerfile.dind` and
  `EmbeddedDindDockerfile` variable.
- `internal/sandbox/image.go`:
  - Add `DindBaseTag` constant (`opencode-msb/runner-base-dind:latest`).
  - Add `ReferencesDindBase` function.
  - Extend `EnsureImage` to build `runner-base-dind` when the project
    Dockerfile references it (or on `--rebuild`).
- `internal/sandbox/runner.go` — add dockerd startup step in
  `prepareSandbox` (or a new helper called after sandbox creation, before
  opencode attach):
  - `sb.Shell(ctx, "test -x /usr/bin/dockerd && dockerd -H unix:///var/run/docker.sock > /var/log/dockerd.log 2>&1 &", msb.WithExecUser("root"))`
  - Poll for socket readiness via `sb.Shell(ctx, "docker info", ...)` with a
    timeout.
  - On failure, return an error with clear context (dockerd failed to start).
- `internal/sandbox/image_test.go` — add tests for `ReferencesDindBase` and
  the extended `EnsureImage` build-decision logic.

### Unchanged

- `internal/sandbox/data/Dockerfile` — plain base image, no changes.
- `cmd/opencode-msb/cli.go` — no new CLI flags or config options.
- `internal/launcherconfig/config.go` — no new config keys.
