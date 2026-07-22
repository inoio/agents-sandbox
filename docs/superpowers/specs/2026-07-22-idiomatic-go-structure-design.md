# Idiomatic Go Project Structure: opencode-msb

**Date:** 2026-07-22
**Status:** Approved
**Author:** brainstorming session

## Overview

Restructure the opencode-msb Go project to follow idiomatic Go conventions
from the [golang-standards/project-layout](https://github.com/golang-standards/project-layout)
and [Organizing a Go module](https://go.dev/doc/modules/layout) guides. The
project was recently rewritten from Python to Go; several Python remnants
remain in CI, `.gitignore`, and README. The current single flat package
(`internal/opencodemsb` with 35 files) and CGO build-tag duplication need
to be addressed for moderate expected growth.

**Goals:**

- Split the single flat package into focused sub-packages by SDK dependency
  boundary.
- Remove all `_nosdk.go` build-tag shims and require CGO unconditionally.
- Refactor `init()` globals and package-level logger state into explicit
  configuration passed via function parameters.
- Remove Python remnants from CI, `.gitignore`, and README.
- Add Makefile and `.golangci.yml` tooling.
- Update module path from `github.com/inoio/opencode-msb` to
  `gitlab.inoio.de/inoio/opencode-msb` (private GitLab hosting).
- Set up GitLab CI release jobs for Linux binary distribution.

**Non-goals:**

- Changing user-facing behavior, CLI flags, or config file locations.
- Adding new features.
- Refactoring the core orchestration logic (only moving and re-packaging it).

## Package Structure

Approach: minimal split by SDK dependency boundary. Pure-logic packages
separate from the SDK-dependent `sandbox` package.

```
cmd/opencode-msb/
  main.go                 # func main() -> calls Execute()
  cli.go                  # cobra root + doctor/run subcommands, RunOptions, Execute()
                          # (parseRunFlags replaced by cobra default-command pattern)
  cli_test.go             # timing helper tests (moved from cmd_test.go)

internal/
  config/
    config.go             # BuildMergedConfig, DeepMerge, LoadProviderConfig
    config_test.go
    data/
      provider-config.json   # embedded via //go:embed

  git/
    git.go                # ProjectSlug, BranchName, EnsureWorktree, RemoveWorktree, etc.
    git_test.go

  sysinfo/
    sysinfo.go            # NumCPUs, TotalMemoryGiB
    sysinfo_test.go

  log/
    log.go                # Logger struct (Info/Warn/Error/Timing)
    spinner.go            # Spinner struct
    timing.go             # NewTiming() — tick/summary closure
    log_test.go
    spinner_test.go
    timing_test.go

  sandbox/
    runner.go             # Run() — orchestrates full flow, CreateSandbox, Attach
    image.go              # EnsureImage() — docker build/inspect + SDK image cache
    volumes.go            # VolumeManager — SDK volumes + host-dir fallback
    secrets.go            # BuildSecrets() — env-var -> SDK SecretEntry
    doctor.go             # CheckAll(), CheckDocker(), CheckKvm(), CheckGit(), CheckMsb()
    data/
      Dockerfile           # embedded via //go:embed
    runner_test.go
    image_test.go
    volumes_test.go
    secrets_test.go
    doctor_test.go
```

### Dependency graph (one-directional, no cycles)

```
cmd/opencode-msb
  -> internal/sandbox   (runner, image, volumes, secrets, doctor)
  -> internal/config     (config merge)
  -> internal/log        (logger, spinner, timing)

internal/sandbox
  -> internal/git        (worktree ops)
  -> internal/config     (BuildMergedConfig)
  -> internal/sysinfo    (CPU/mem)
  -> internal/log        (logger + spinner)
  -> msb SDK + moby docker client

internal/git
  -> internal/log        (warn on non-git-repo)

internal/config, sysinfo, log
  -> no internal deps
```

### Data file placement

- `provider-config.json` moves to `internal/config/data/` (consumed by the
  config package via `//go:embed`).
- `Dockerfile` moves to `internal/sandbox/data/` (consumed by the image
  builder via `//go:embed`).

### Alignment with Go standards

- `/cmd/opencode-msb/` — directory name matches executable name. Thin layer:
  only cobra setup and flag parsing. All logic lives in `internal/`.
  (project-layout: "Don't put a lot of code in the application directory.")
- `/internal/` with sub-packages — private code enforced by Go compiler.
  (go.dev: "it's recommended to keep packages in internal as much as possible.")
- No `/pkg` — not needed for a CLI tool that isn't a library.
- No `/src` — neither reference recommends it.
- `/docs/` — already exists, matches both references.

## CGO / Build Tag Cleanup

**Current state:** 5 pairs of `_sdk.go` / `_nosdk.go` files with build tags
`//go:build cgo` / `//go:build !cgo`. The `secrets_nosdk.go` alone re-defines
`SecretEntry`, `SecretEnvOptions`, and `secretFactory` types (53 lines of
boilerplate) just to compile without CGO.

**Decision:** Drop non-CGO support entirely. CGO is always enabled.

**Changes:**

1. Delete all `_nosdk.go` files:
   - `doctor_nosdk.go` (12 lines)
   - `image_nosdk.go` (12 lines)
   - `runner_nosdk.go` (9 lines)
   - `secrets_nosdk.go` (53 lines — eliminates all duplicated type definitions)
   - `volumes_nosdk.go` (28 lines)

2. Merge `_sdk.go` files into their base files by dropping the `_sdk` suffix:
   - `doctor_sdk.go` + `doctor.go` -> `doctor.go`
   - `image_sdk.go` + `image.go` -> `image.go`
   - `runner_sdk.go` + `runner.go` -> `runner.go`
   - `secrets_sdk.go` + `secrets.go` -> `secrets.go`
   - `volumes_sdk.go` + `volumes.go` -> `volumes.go`

3. Remove `//go:build cgo` build tag comments from all remaining files.

4. Merge `secrets_sdk_test.go` into `secrets_test.go`.

5. Set `CGO_ENABLED=1` in CI and Makefile explicitly.

**Result:** ~114 lines of boilerplate removed. No duplicated types. Single
source of truth per concern.

## Global State Refactoring

**Current problem:** `init()` in `cmd.go` sets package-level globals
(`stateDir`, `userConfigDir`). The logger uses package-level `logMu` mutex
and `logOut` global. These make testing harder and are an anti-pattern in Go.

**Changes:**

1. **Config struct replaces `init()` globals:**

```go
type Config struct {
    StateDir      string  // ~/.local/share/opencode-msb
    UserConfigDir string  // ~/.config/inoio-sandbox/opencode
}
```

Constructed explicitly in `Execute()`, passed to `sandbox.Run()`.

2. **Logger as instance, not global:** Remove the package-level `logOut`
   global and `logMu` mutex from the `log` package. Packages that need
   logging accept a `*log.Logger`:
   - `sandbox.Run()` receives a `*log.Logger` in its options
   - `sandbox.EnsureImage()`, `sandbox.VolumeManager`, `sandbox.BuildSecrets()`,
     `sandbox.CheckAll()` accept a logger
   - `git.ProjectSlug()` accepts a logger (for the "not in git repo" warning)

3. **Spinner takes a logger:** `startSpinner()` currently accesses
   `logOut`/`logMu` directly. Refactor to `log.NewSpinner(logger)` — the
   spinner reads the writer and color settings from the logger instance.

4. **Timing helper:** `newTiming()` moves to `internal/log/timing.go` and
   accepts a `*log.Logger`:

```go
func NewTiming(logger *Logger, enabled bool) (tick func(string), summary func())
```

5. **`SecretMap` stays as a package-level `var`** but becomes unexported
   (`var secretMap`). It's a static lookup table, not mutable state —
   acceptable as a package-level var.

6. **`version` var** stays in `cmd/opencode-msb/` for `-ldflags` injection —
   standard Go pattern.

## Python Remnants Cleanup

### .gitlab-ci.yml

Remove Python stages, keep Go-only:

- Remove `lint` stage (Python ruff check)
- Remove `unit-tests` stage (Python pytest)
- Rename `go-lint` -> `lint`, `go-test` -> `test`, `go-build` -> `build`
- Remove `PIP_DISABLE_PIP_VERSION_CHECK` variable
- Keep `CGO_ENABLED: "1"`
- Add `release` stage for binary distribution (see Distribution section)
- CI calls `make lint`, `make test`, `make build` — single source of truth

### .gitignore

Replace with Go-only entries:

```
/opencode-msb
/.idea/
/.gitlab-ci-local/
/.envrc
```

### README.md

Rewrite to match actual CLI:
- Title: `opencode-msb`
- Document binary download from GitLab releases for Linux
- `go install` as developer alternative (requires Go + CGO + `GOPRIVATE=gitlab.inoio.de`)
- Usage examples matching actual commands and flags
- Project overrides: `.sandbox/Dockerfile` and `.sandbox/env`

## Distribution

**Approach:** GitLab CI release jobs producing Linux binaries.

**CI release stage:**
- Build `linux/amd64` binary in a `golang:1.26` Docker image with `CGO_ENABLED=1`
- Attach binary to GitLab releases via the `release` keyword
- Tagged releases trigger the release job

**macOS:** Deferred until macOS CI runners are available. The CI structure
supports adding a `darwin/arm64` job later.

**README documents:**
```bash
# Download the latest Linux binary from GitLab Releases
curl -fsSL -o opencode-msb "https://gitlab.inoio.de/inoio/opencode-msb/-/releases/latest/download/opencode-msb-linux-amd64"
chmod +x opencode-msb && sudo mv opencode-msb /usr/local/bin/
```

**Developer alternative:**
```bash
export GOPRIVATE=gitlab.inoio.de
go install gitlab.inoio.de/inoio/opencode-msb/cmd/opencode-msb@latest
```

## Tooling

### Makefile

```makefile
.PHONY: build test lint vet fmt clean

build:
	go build -o opencode-msb ./cmd/opencode-msb

test:
	go test ./...

lint:
	golangci-lint run ./...

vet:
	go vet ./...

fmt:
	gofmt -w .

clean:
	rm -f opencode-msb
```

### .golangci.yml

```yaml
linters:
  enable:
    - errcheck
    - govet
    - ineffassign
    - staticcheck
    - unused
    - gofmt
    - goimports
    - misspell
    - revive

linters-settings:
  goimports:
    local-prefixes: gitlab.inoio.de/inoio/opencode-msb
```

No complexity limiters (`gocyclo`, `gocognit`) — they would flag
legitimately complex code like `parseRunFlags`.

`revive` replaces deprecated `golint`.

### Module path

- `go.mod`: `module gitlab.inoio.de/inoio/opencode-msb`
- All import paths change from `github.com/inoio/opencode-msb/...` to
  `gitlab.inoio.de/inoio/opencode-msb/...`
- `local-prefixes` in `.golangci.yml` matches module path

## Import Conventions & SDK Alias

**SDK alias:** Use `msb` for the microsandbox SDK:

```go
import (
    msb "github.com/superradcompany/microsandbox/sdk/go"
)
// msb.CreateSandbox(), msb.Image.Get(), msb.Mount.Named(), etc.
```

**Internal package name:** The SDK-dependent package is named `sandbox`
(not `msb`) to avoid collision with the SDK alias:

```go
import (
    "gitlab.inoio.de/inoio/opencode-msb/internal/sandbox"
    msb "github.com/superradcompany/microsandbox/sdk/go"
)
// sandbox.Run(), sandbox.EnsureImage(), msb.CreateSandbox()
```

## Code Cleanup: Remove Debugging/Dead Code

Audit of all source files identified the following items to remove or fix:

### 1. Remove `setLogOutput()` — test-only helper in production code

**Location:** `cmd.go:29-37`
**What:** Swaps the global logger output writer. Only called from tests
(`cmd_test.go:12,24`) to redirect output to a `bytes.Buffer`.
**Action:** Remove. With the global state refactoring (Section 3), tests
construct their own `*log.Logger` directly. Dead code after refactoring.

### 2. Remove `RemoveWorktree()` — dead code

**Location:** `worktree.go:93-96`
**What:** Removes a git worktree. Never called anywhere in the codebase.
The runner creates worktrees but never cleans them up (they persist for
reuse across sessions).
**Action:** Remove. YAGNI.

### 3. Replace `parseRunFlags()` with cobra default command

**Location:** `cmd.go:99-107, 154-191`
**What:** A 37-line manual flag parser that duplicates what cobra already
does. It exists to support "bare invocation" (`opencode-msb --worktree x`
instead of `opencode-msb run --worktree x`). The `Execute()` function has
a complex condition (line 99) to decide whether to use cobra or fall back
to this parser.
**Why remove:** Every new flag must be added in two places. The condition on
line 99 is fragile. Cobra supports implicit subcommands via
`TraverseChildren` or by making `run` the default command.
**Action:** Replace with cobra's native "default command" pattern so bare
`opencode-msb [flags] [args]` works without a custom parser. Removes ~37
lines of duplicated logic and the fragile condition. Also remove
`flags.go` (which would have been the new home for `parseRunFlags` in the
restructured layout).

### 4. Remove `NODE_ENV=development` from VM environment

**Location:** `runner.go:20` (in `vmEnv` slice)
**What:** Sets `NODE_ENV=development` inside the VM, inherited from the
Python implementation. This affects Node.js behavior (verbose logging,
disabled minification, etc.).
**Action:** Remove `NODE_ENV` entirely. Let opencode/Node.js use its
default. Also remove the corresponding test assertion in `runner_test.go`
(line 48-49).

### 5. Remove `secrets_sdk_test.go` — redundant SDK smoke test

**Location:** `secrets_sdk_test.go:11-16`
**What:** `TestSecretEntryMatchesSDKFactory` calls `m.Secret.Env()` and
checks the `EnvVar` field. This tests the SDK's own factory, not our code.
**Action:** Remove. We shouldn't test third-party libraries. After the
build-tag merge, `secrets_test.go` already tests our `BuildSecrets()`.

### 6. Remove `doctor_test.go` — tautological tests

**Location:** `doctor_test.go:8-25`
**What:** All four tests assert nothing meaningful:
- `TestCheckGitReturnsBool` — asserts a bool returns a bool (always passes)
- `TestCheckKvmReturnsBool` — calls `CheckKvm()`, asserts nothing
- `TestCheckDockerReturnsBool` — calls `CheckDocker()`, asserts nothing
- `TestCheckAllReturnsBool` — calls `CheckAll()`, asserts nothing
**Action:** Remove all four. They can't fail. If meaningful doctor tests
are needed later, they should mock `exec.LookPath` and verify specific
error messages.

### 7. Fix `scanBuildOutput` silently swallowing decode errors

**Location:** `image_sdk.go:108-125`
**What:** When `dec.Decode(&msg)` returns an error other than `io.EOF`,
the function returns `nil` (line 116). This silently swallows malformed
Docker build output. If Docker emits a non-JSON line, any subsequent real
error messages are missed.
**Action:** Return the decode error instead of `nil`. Low risk, prevents
silent failures.

## Testing

- Tests stay co-located with source in each package (idiomatic Go).
- `internal/log/timing_test.go` — timing helper tests (from `cmd_test.go`)
- `internal/config/config_test.go` — config merge tests (unchanged)
- `internal/git/git_test.go` — project slug, branch slug, worktree path tests
- `internal/log/*_test.go` — logger, spinner tests
- `internal/sysinfo/sysinfo_test.go` — meminfo parsing tests
- `internal/sandbox/*_test.go` — runner, image, volumes, secrets tests
  (secrets_sdk_test.go removed — tested SDK's own factory, not our code)
  (doctor_test.go removed — all four tests were tautological/no-op)
- `parseRunFlags` currently has no tests; `cmd/opencode-msb/cli_test.go`
  may be added during implementation if needed

Integration tests (not yet implemented but planned):
- Tag with `//go:build integration`
- Live in `internal/sandbox/` alongside unit tests
- Skipped in CI without the build tag

## Migration Steps

1. Change module path in `go.mod` + update all import paths (mechanical
   find/replace + `go mod tidy`)
2. Create new package directories and move files (preserving git history
   via `git mv`)
3. Merge `_sdk.go` files into base files, delete `_nosdk.go` files
4. Refactor global state (init -> Config struct, logger injection)
5. Remove dead/debugging code:
   - Delete `setLogOutput()`
   - Delete `RemoveWorktree()`
   - Replace `parseRunFlags()` with cobra default command pattern
   - Remove `NODE_ENV=development` from `vmEnv` + test assertions
   - Delete `secrets_sdk_test.go`
   - Delete `doctor_test.go`
   - Fix `scanBuildOutput` to return decode errors
6. Add Makefile + `.golangci.yml`
7. Update `.gitlab-ci.yml` (remove Python stages, add release stage)
8. Clean `.gitignore`
9. Rewrite `README.md`
10. Verify: `go mod tidy`, `go vet ./...`, `go test ./...`,
    `golangci-lint run`, `make build`
