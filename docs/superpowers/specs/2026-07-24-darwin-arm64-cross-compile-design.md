# darwin/arm64 cross-compilation via Zig

## Problem

The release pipeline (`.gitlab-ci.yml`) only builds `opencode-msb-linux-amd64`.
macOS Apple-Silicon users have no released binary and must build from source, which
requires a working Go + CGO toolchain locally. We want a prebuilt
`opencode-msb-darwin-arm64` artifact in releases.

CGO is genuinely required: the microsandbox SDK's `internal/ffi` is a cgo bridge
(`#cgo linux LDFLAGS: -ldl`, `#cgo darwin LDFLAGS:`) excluded under
`CGO_ENABLED=0`. The C is trivial (only `dlfcn.h`/`stdlib.h`/`string.h`),
and the Rust library it loads is **embedded per-platform and dlopen'd at
runtime** — not linked at build time. The SDK ships embedded libraries for
exactly three targets (`bundle_unsupported.go` covers the rest):

- `darwin/arm64` — `libmicrosandbox_go_ffi-darwin-arm64.dylib` (real 16 MB Mach-O, pre-signed)
- `linux/amd64`  — `libmicrosandbox_go_ffi-linux-amd64.so`
- `linux/arm64`  — `libmicrosandbox_go_ffi-linux-arm64.so`

So the cross-built binary is self-contained: build-tag selection embeds the
matching native library for the target.

## Approach

Use **Zig as a drop-in cross C compiler** (`zig cc -target <triple>`) for the
cgo step, inside a **custom Go+Zig builder image** in CI. Go invokes `$CC` for
cgo; Zig compiles the C for the foreign target using its bundled cross-libc (no
macOS SDK on the Linux builder). Produce two release artifacts; leave
linux/arm64 as a documented-but-disabled job (SDK supports it; trivially
enableable later).

### Why Zig

- Cross-compiles CGO with no per-target sysroot/SDK on the host.
- `zig cc` is a drop-in `$CC` — no Go-toolchain changes.
- Stable, single-binary distribution; pinned to 0.16.0.

### Why a builder image (Approach C)

- Go+Zig preinstalled → no per-job download, fast, reproducible.
- GitLab Container Registry on `gitlab.inoio.de/inoio/opencode-msb` is available.
- CI job token (`$CI_REGISTRY_USER` / `$CI_REGISTRY_PASSWORD`) pushes to the
  project's own registry — **no extra auth key**.
- A privileged runner / kaniko executor is available.

### Ad-hoc code signing is automatic (no secrets)

On Apple Silicon, arm64 Mach-O code requires at least an ad-hoc signature to
execute (unsigned arm64 code is `Killed: 9`). This is handled with **zero extra
tooling**:

- The main binary: Go's internal linker auto ad-hoc-signs any `darwin/arm64`
  binary via `cmd/internal/codesign`. `NeedCodeSign()` returns true when
  `ctxt.IsDarwin() && ctxt.IsARM64()` — keyed off the **target**, not the host
  OS — so cross-builds on Linux are signed in-process. No `codesign` binary
  needed.
- The embedded Rust dylib already carries a 128 KB embedded code signature
  (verified: `CSMAGIC_EMBEDDED_SIGNATURE` + `LC_CODE_SIGNATURE` present in the
  shipped `.dylib`).

Gatekeeper will still warn end users ("developer cannot be verified") since the
signature is ad-hoc, not notarized. Users bypass via right-click > Open or
`xattr -d com.apple.quarantine`. This matches the MVP / curl-install path in the
backlog. Notarization is explicitly out of scope.

## Design

### Components

- **`ci/builder/Dockerfile`** — `FROM golang:1.26`, download + extract pinned
  Zig 0.16.0 (`zig-x86_64-linux-0.16.0.tar.xz`) to `/usr/local/zig`, prepend
  to `PATH`. No code beyond the install.
- **`build-builder-image` job** (`prepare` stage) — kaniko
  (`gcr.io/kaniko-project/executor:debug`) builds and pushes the image to
  `$CI_REGISTRY_IMAGE/ci-builder:latest` using the CI job token. Runs on
  changes to `ci/builder/Dockerfile` and via manual trigger — **does not run on
  the release branch**. Release-branch pipelines pull the prebuilt `:latest`
  image from the registry.
- **`version` job** (`build` stage) — computes
  `VERSION="0.$(date -u +%Y.%-m.%-d)+${CI_COMMIT_SHORT_SHA}"`, emits
  `version.env` as a dotenv artifact report so both targets get the identical
  version. (Currently inlined in `build-release`; hoisted out so the version is
  single-sourced.)
- **`.build-release-tmpl:`** hidden template — `image:
  $CI_REGISTRY_IMAGE/ci-builder:latest`, `needs: [version]`, GOPATH cache,
  release-branch rules. Script sets `CC`/`CXX` to `zig cc/c++ -target
  $ZIG_TARGET`, runs `go build`, smoke-checks the artifact, emits a per-target
  `*_JOB_ID` dotenv. No hard dependency on `build-builder-image` — the image
  must already exist in the registry (one-time bootstrap prerequisite).
- **`build-release:linux-amd64`** and **`build-release:darwin-arm64`** —
  concrete jobs extending the template, each setting `GOOS`/`GOARCH`/
  `ZIG_TARGET`/`ARTIFACT_NAME`. Replaces the current monolithic `build-release`.
- **Commented-out `build-release:linux-arm64`** with a note that it is
  SDK-supported and ready to enable by uncommenting.
- **`release`** job — `needs` the two builds + `version`; wires 2 asset links
  via the per-target job IDs (mirrors the existing `${CI_PROJECT_URL}/-/jobs/${JOB_ID}/artifacts/file/<binary>` pattern).

### Out of scope (local builds)

The `Makefile` is unchanged. Cross-compilation is CI-only; local development
stays native (`make build` with the host's gcc). No `build-darwin-arm64` /
`build-linux-arm64` Makefile targets.

### Cross-compile invocation (per target)

```
CC="zig cc -target $ZIG_TARGET"
CXX="zig c++ -target $ZIG_TARGET"
CGO_ENABLED=1 GOOS=$GOOS GOARCH=$GOARCH \
  go build -ldflags "-X main.version=$VERSION" -o $ARTIFACT_NAME ./cmd/opencode-msb
```

| target             | GOOS   | GOARCH | ZIG_TARGET        | artifact                  |
|--------------------|--------|--------|-------------------|---------------------------|
| linux/amd64        | linux  | amd64  | x86_64-linux-gnu  | opencode-msb-linux-amd64  |
| darwin/arm64       | darwin | arm64  | aarch64-macos-gnu | opencode-msb-darwin-arm64 |
| linux/arm64 (off)  | linux  | arm64  | aarch64-linux-gnu | opencode-msb-linux-arm64  |

Build-tag selection auto-embeds the correct pre-signed Rust dylib per target.

### Data flow

```
prepare: build-builder-image (on Dockerfile change / manual; NOT on release)
   |
   v  (image exists in registry as :latest)
build:  version ──┬── build-release:linux-amd64  ──┐
                  └── build-release:darwin-arm64 ──┤
                                                   v
release:  2 asset links (linux-amd64, darwin-arm64)
```

Version flows via the `version.env` dotenv; each build's `CI_JOB_ID` flows via
its own dotenv; `release` builds the asset URLs from per-target job IDs.

## Testing & verification

- **linux/amd64**: `./opencode-msb-linux-amd64 --version` (runs on the linux
  runner).
- **darwin/arm64**: cannot execute a Mach-O on the linux runner. Smoke test =
  `file opencode-msb-darwin-arm64` asserts `Mach-O 64-bit ... arm64`, a Python
  assertion that `LC_CODE_SIGNATURE` (load command `0x1d`) is present, and
  `go version -m opencode-msb-darwin-arm64` to confirm the version ldflag
  landed. CI build success (`go build` exits 0) is the primary automated
  signal.
- **Manual macOS runtime check** is the final acceptance gate — run the
  released binary on an Apple-Silicon Mac. Automated CI cannot execute it.

## Error handling

- Builder-image job failing blocks image updates; release-branch builds fail at
  `pull` time if `:latest` is missing (one-time bootstrap: push the image once
  via a manual pipeline run before the first release that uses it).
- Any `go build` failure or smoke-assertion failure fails its job; `release`
  only runs when all builds pass.
- Zig version is pinned (0.16.0) for reproducibility; 0.15.2 is the fallback
  if a cgo regression appears.

## Out of scope

- linux/arm64 as a release artifact (documented only).
- Notarization / hardened runtime (ad-hoc auto-sign satisfies the MVP; revisit
  if Gatekeeper friction grows).
- Windows, FreeBSD, darwin/amd64 (the SDK ships no native libraries for these).
- Local cross-compilation via Makefile.
