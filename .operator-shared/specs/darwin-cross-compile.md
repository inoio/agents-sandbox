---
description: Contract for the darwin cross-build's zig prerequisite — beyond zig on PATH, the macOS linker stubs (.github/stubs/*.tbd) must be baked into <zig-root>/lib/libc/darwin; the three consumers (CI release job, dev VM image, make bootstrap); make bootstrap's classify-then-act guarantee (managed vs outside zig, on-disk idempotency, warnings); the symlink-following ZIG_ROOT resolution; and the accepted duplication between .github/stubs and the dev Dockerfile's inline printf (relocating it into the build context does not help).
read_if: Changing the macOS/darwin cross-compile path, make bootstrap or its zig classification, the release job's stub step, .github/stubs/*.tbd, the dev VM Dockerfile's stub step, or the Makefile's ZIG_ROOT/ZIG_MANAGED_ROOT resolution; or diagnosing a darwin cross-build that cannot find CoreFoundation, Security, or resolv.
---

# Darwin cross-compile prerequisites

## Contract

`make build-release GOOS=darwin` (and `build-release-all`) links through `zig cc`. Two things must hold:

- `zig` is on `PATH` (checked; the failure message points at `make bootstrap`).
- The three macOS linker stubs are present under `<zig-root>/lib/libc/darwin`:
  `System/Library/Frameworks/CoreFoundation.framework/CoreFoundation.tbd`,
  `System/Library/Frameworks/Security.framework/Security.tbd`, and `usr/lib/libresolv.tbd`.

zig ships the macOS headers and `libc/darwin/libSystem.tbd`, but **not** those three. Go's linker asks for
`-framework CoreFoundation -framework Security -lresolv`, so without the stubs the build dies with
`unable to find system framework "CoreFoundation"` / `unable to find dynamic system library "resolv"`. The stubs are
empty `!tapi-tbd` files; symbols resolve at runtime on macOS.

## ZIG_ROOT resolution

The `Makefile` resolves zig's install root (`ZIG_ROOT`) from `command -v zig`, following **one** symlink level: the
link's target is made absolute against the link's own directory (`dirname "$f"/$(readlink "$f")`) before `dirname` +
`pwd -P`. Making the target absolute matters — a relative target (Homebrew's `bin/zig -> ../Cellar/...`, for instance)
left `ZIG_ROOT` empty, which made an outside zig compare equal to the managed root and silently install a managed zig
instead of warning. `ZIG_ROOT` is empty when zig is absent, and it is a lazy (`=`) variable, so non-darwin targets do
not invoke `command -v zig` or `readlink` at all.

Two limits are structural, not bugs to discover later: only one link level is followed, and the layout is assumed to be
a flat tarball install, where the binary's directory is the libc root. A package manager's layout (Homebrew installs the
binary to `<prefix>/bin/zig` with libs under `<prefix>/lib/zig/`) therefore does not resolve, and the stub probe looks
under the wrong directory. The probe errs toward reporting "no stubs found" rather than toward claiming compliance, and
its message says what it looked for, not that the zig is broken.

## The three consumers

- `.github/workflows/ci.yml` (release job) — copies `.github/stubs/*.tbd` into
  `$(dirname "$(command -v zig)")/lib/libc/darwin` before building. Release binaries are cross-built on `ubuntu-latest`;
  no macOS runner is involved.
- `.agents-sandbox/Dockerfile` (dev VM image) — bakes the same three stubs. The bytes are inlined with `printf` because
  the image build context is synthetic, so `.github/stubs` is not in it (see `specs/image-composition.md`).
- `make bootstrap` — the host route. It ensures the zig it manages *and* the stubs in one classify-then-act step (below).

## `make bootstrap`'s guarantee

Bootstrap classifies on the install root of the zig that `PATH` resolves to (compared against the realpath of
`$HOME/.local/zig`, both via `ZIG_ROOT`/`ZIG_MANAGED_ROOT`):

| `command -v zig` resolves to | action |
|---|---|
| nothing | install the managed zig into `~/.local` (versioned dir + `~/.local/zig` + `~/.local/bin/zig`), then ensure the stubs |
| the managed install | ensure idempotently: reinstall only when the on-disk version differs from `ZIG_VERSION`, then ensure the `~/.local/bin/zig` link and the stubs |
| anywhere else | warn only, mutate nothing — once that it resolves outside the managed install (naming the path and the version that zig reports), plus once more if the stubs are not found under the resolved root |

What to install is decided from the managed zig **on disk**, not from `PATH`, so a managed install that is not on
`PATH` is not re-downloaded; in that case bootstrap prints the `~/.local/bin` PATH hint, since without it the
`build-release` guard's "run `make bootstrap`" is a closed loop. Managed-path failures (download, extraction) exit
non-zero; everything about an outside zig is a warning and the run still exits 0. The binary link and the stubs are
re-ensured on every managed run rather than only when missing — idempotent in effect, and it self-heals a stale stub or
a pre-fix install that never created `~/.local/bin/zig` (the link the `~/.local/bin` PATH hint depends on).

**Accepted risk:** the packaging decision (make-only, no new files) means there is no committed test. The
classification is verified by a throwaway harness (`/tmp/opencode/zbz.sh`-style: a fake zig tree plus a stub `goenv`
and a throwaway `HOME`), so a future edit that breaks a branch is caught only by a `darwin` link failure, not by CI.

## Duplication with the dev Dockerfile — accepted

`.github/stubs/*.tbd` is canonical: the CI release job and `make bootstrap` copy those files. The dev
`.agents-sandbox/Dockerfile` carries a second copy, inlined with `printf`.

Relocating the stubs under `internal/sandbox/image` so `//go:embed` and `buildContextFiles()` can carry them does
**not** remove the second step. `COPY` cannot expand `$(which zig)`, so the launcher can only ship the files to a
neutral path — the project Dockerfile would still need a zig-dependent `RUN` to place them into `lib/libc/darwin`,
and that step misbehaves on any base image that does not ship zig (an empty `which zig` makes `$(dirname …)` resolve
to `.`). It would also put the stubs in every user's runner-image context and fold them into `computeDockerfileID`,
invalidating all cached runner images once and rebuilding every image on any future stub edit. A CI drift guard
(a `ci/check-stubs.sh` + test pair, like `check-docs.sh`) was considered and judged not worth it for three stable
files. The duplication is accepted; keep the two copies in sync by hand when a stub changes.

Note the launcher's own build-context assets (`agents-sandbox-path.sh`, `msb-trust.sh`, `msb-trust.LICENSE`) *do*
reach custom bases: `finalizationBlock`'s `COPY` lines are appended to the runner stage for managed and custom
bases alike (`runnerStage`, `dockerfile.go`). Shipping bytes to a custom base is fine; placing them inside a
toolchain that base may not have is not.
