---
description: Contract for the dev-VM image's multistage layout in `.agents-sandbox/Dockerfile` — one stage per component (`system`, `fetch`, `go`, `zig`, `extras`) merged into the final stage, which components can and cannot cross a `COPY` boundary, and per-stage version pins.
read_if: Editing `.agents-sandbox/Dockerfile` or the dev-VM toolchain; bumping a pinned dev tool or adding/removing an apt package; diagnosing a dev-image rebuild that re-ran more than the changed component.
---

# Dev-VM image composition

This repo's dev VM runs an image built from `.agents-sandbox/Dockerfile`. The launcher reads that file as a **project Dockerfile** and renders it with `RenderDockerfile` (see [image-composition.md](../runner-image/image-composition.md)): everything before the **last** `FROM` is preserved as earlier stages, and everything after it becomes the runner stage's body. Only the final stage ships, so a component installed in an earlier stage reaches the image only if the final stage `COPY`s it.

## Stages

One stage per component, ordered by dependency:

- `system` — the apt package set and the `fd` symlink. The final stage is `FROM system`, so these are **inherited**, not copied.
- `fetch` — `debian:forky-slim` plus `ca-certificates curl tar xz-utils jq unzip`: the minimal base the tool stages download onto. Kept separate from `system` so a package change does not re-run a tool download.
- `go` — the Go toolchain (`/usr/local/go`) and the tools it installs (`/usr/local/bin`).
- `zig` — zig at the stable `/usr/local/zig`, plus the inlined macOS linker stubs.
- `extras` — gh, ast-grep, plantuml, operator-helper.
- the final stage — `FROM system`, then one `COPY --from=<stage>` per tool tree.

## What crosses a `COPY` boundary

Only files under a self-contained prefix can be isolated in a stage and copied into the final stage:

- **Isolatable** (a stage plus `COPY`): `/usr/local/go`, `/usr/local/zig`, a tool stage's own `/usr/local/bin` binaries, `/usr/local/plantuml`, `/opt/operator-helper`.
- **Not isolatable** (kept in `system`): apt packages and anything a `.deb` installs system-wide. Their files are spread across `/usr`, `/etc`, and `/var/lib/dpkg`, so they are inherited only through `FROM system`. This is why the `plantuml` and `operator-helper` runtimes (`default-jre`, `nodejs`) stay in `system`, and why gh's `.deb` is extracted with `dpkg-deb -x` into `/opt/gh` and merged with `COPY --from=extras /opt/gh/ /` rather than installed.

## Pinned versions

Each `ARG *_VERSION` sits in the **stage that installs its tool**, so a bump invalidates only that stage and the final stage's `COPY`s. A pin hoisted above every install would invalidate the whole image on a one-line change.

## Invariants

- No stage alias may collide with the launcher's reserved aliases (`agents-sandbox-base`, `-toolbase`, `-node`, `-agent`, `-docker`, `-runner`); the renderer fails on a collision.
- `ENV` set in an earlier stage is dropped — Docker keeps config only from the final stage — so the runtime `PATH` (`/usr/local/go/bin`, `/usr/local/zig`, `/home/dev/go/bin`) is set in the final-stage body.
- The `# agents-sandbox:docker` marker stays in the final-stage body; the launcher injects the docker `COPY`/adopt block at it.
- The final stage builds on `system`, so the image provides what the launcher requires of a custom base: `/bin/bash`, `curl`, and `tar`.
