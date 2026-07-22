# Idiomatic Go Project Structure Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Restructure opencode-msb from a single flat package into idiomatic Go sub-packages, remove build-tag duplication, eliminate global state, clean up Python remnants, and add tooling.

**Architecture:** Bottom-up package creation — leaf packages (log, config, sysinfo) first, then git (depends on log), then sandbox (depends on all). During Tasks 2-6, new packages coexist with the old `internal/opencodemsb/` package (duplicate code). Task 7 switches `cmd/` to use the new packages and deletes the old one.

**Tech Stack:** Go 1.26, cobra, microsandbox SDK (CGO required), moby Docker client, json5

## Global Constraints

- Module path: `gitlab.inoio.de/inoio/opencode-msb` (changed from `github.com/inoio/opencode-msb`)
- CGO is always enabled — no build tags, no `_nosdk.go` shims
- SDK alias: `msb` (was `m`)
- Logger is injected, never global — functions that log accept `*log.Logger`
- Config (StateDir, UserConfigDir) is passed explicitly, never via `init()`
- Target: Linux (KVM) and macOS (Apple Silicon) — but CI builds Linux only for now
- No new features, no user-facing behavior changes
- Tests co-located with source in each package

---

### Task 1: Change module path

**Files:**
- Modify: `go.mod`
- Modify: `cmd/opencode-msb/main.go`

**Interfaces:**
- Consumes: nothing
- Produces: module path `gitlab.inoio.de/inoio/opencode-msb` for all subsequent tasks

- [ ] **Step 1: Update go.mod module path**

Change line 1 of `go.mod` to `module gitlab.inoio.de/inoio/opencode-msb`

- [ ] **Step 2: Update the import in main.go**

In `cmd/opencode-msb/main.go`, change the import to `gitlab.inoio.de/inoio/opencode-msb/internal/opencodemsb`

- [ ] **Step 3: Verify it builds**

Run: `go build ./cmd/opencode-msb`
Expected: succeeds (only one internal import path to update)

- [ ] **Step 4: Run go mod tidy**

Run: `go mod tidy`

- [ ] **Step 5: Commit**

```bash
git add go.mod go.sum cmd/opencode-msb/main.go
git commit -m "refactor: change module path to gitlab.inoio.de"
```

---

### Task 2: Create `internal/log/` package

**Files:**
- Create: `internal/log/log.go` — exported `Logger`, `New(w, color) *Logger`
- Create: `internal/log/spinner.go` — exported `Spinner`, `NewSpinner(l) *Spinner`, `Start/Stop/StopError`
- Create: `internal/log/timing.go` — exported `NewTiming(l, enabled) (tick, summary)`
- Create: `internal/log/log_test.go`, `internal/log/spinner_test.go`, `internal/log/timing_test.go`

**Interfaces:**
- Consumes: nothing (leaf package)
- Produces: `log.Logger`, `log.New()`, `log.NewSpinner()`, `log.NewTiming()`

**Key changes from original:**
- Package name: `log` (was `opencodemsb`)
- `logger` struct → `Logger` (exported)
- `newLogger` → `New` (exported)
- `spinner` struct → `Spinner` (exported); `startSpinner(msg)` → `NewSpinner(l).Start(msg)`
- Remove all `logMu.Lock()`/`logOut` global references — spinner holds its own writer/color from the logger
- `newTiming(enabled)` → `NewTiming(l, enabled)` — takes `*Logger` instead of using globals

- [ ] **Step 1: Create `internal/log/log.go`**

Copy `internal/opencodemsb/log.go`, change package to `log`, export `Logger` and `New`. No other changes needed — the struct fields and methods stay the same, just capitalized.

- [ ] **Step 2: Create `internal/log/spinner.go`**

Copy `internal/opencodemsb/spinner.go`, change package to `log`, export `Spinner`/`NewSpinner`/`Start`/`Stop`/`StopError`. Remove all `logMu.Lock()`/`logMu.Unlock()` calls and `logOut` references — the spinner already holds its own `w io.Writer` and `color bool` fields, set from the logger in `NewSpinner`.

- [ ] **Step 3: Create `internal/log/timing.go`**

Extract the timing helper from `internal/opencodemsb/cmd.go:39-75` into `internal/log/timing.go`. Change signature from `newTiming(enabled bool)` to `NewTiming(l *Logger, enabled bool)`. Replace `logMu.Lock()`/`logOut.Timing(...)`/`logMu.Unlock()` with `l.Timing(...)`.

- [ ] **Step 4: Create `internal/log/log_test.go`**

Copy `internal/opencodemsb/log_test.go`, change package to `log`, replace `newLogger(&buf, ...)` with `New(&buf, ...)`.

- [ ] **Step 5: Create `internal/log/spinner_test.go`**

Copy `internal/opencodemsb/spinner_test.go`, change package to `log`. Replace `&spinner{w: &buf, color: false, msg: "..."}` with `s := NewSpinner(New(&buf, false)); s.Start("...")`.

- [ ] **Step 6: Create `internal/log/timing_test.go`**

Copy `internal/opencodemsb/cmd_test.go`, change package to `log`. Replace `setLogOutput(&buf)` + `newTiming(...)` with `NewTiming(New(&buf, false), ...)`.

- [ ] **Step 7: Run tests**

Run: `go test ./internal/log/... -v`
Expected: all tests pass

- [ ] **Step 8: Commit**

```bash
git add internal/log/
git commit -m "refactor: create internal/log package with Logger, Spinner, Timing"
```

---

### Task 3: Create `internal/config/` package

**Files:**
- Create: `internal/config/config.go` — `LoadProviderConfig`, `DeepMerge`, `BuildMergedConfig`
- Create: `internal/config/data.go` — `//go:embed data/provider-config.json`
- Create: `internal/config/data/provider-config.json`
- Create: `internal/config/config_test.go`

**Interfaces:**
- Consumes: nothing (leaf package)
- Produces: `config.LoadProviderConfig`, `config.DeepMerge`, `config.BuildMergedConfig`, `config.EmbeddedProviderConfig`

- [ ] **Step 1: Create directory and copy data file**

```bash
mkdir -p internal/config/data
cp internal/opencodemsb/data/provider-config.json internal/config/data/provider-config.json
```

- [ ] **Step 2: Create `internal/config/data.go`**

New file with `//go:embed data/provider-config.json` and `var EmbeddedProviderConfig []byte`.

- [ ] **Step 3: Create `internal/config/config.go`**

Copy `internal/opencodemsb/config.go`, change package to `config`. All functions already exported. No other changes.

- [ ] **Step 4: Create `internal/config/config_test.go`**

Copy `internal/opencodemsb/config_test.go`, change package to `config`. All references stay the same (same package).

- [ ] **Step 5: Run tests**

Run: `go test ./internal/config/... -v`
Expected: all tests pass

- [ ] **Step 6: Commit**

```bash
git add internal/config/
git commit -m "refactor: create internal/config package with config merge logic"
```

---

### Task 4: Create `internal/sysinfo/` package

**Files:**
- Create: `internal/sysinfo/sysinfo.go` — `NumCPUs`, `TotalMemoryGiB`
- Create: `internal/sysinfo/sysinfo_test.go`

**Interfaces:**
- Consumes: nothing (leaf package)
- Produces: `sysinfo.NumCPUs()`, `sysinfo.TotalMemoryGiB()`

- [ ] **Step 1: Create `internal/sysinfo/sysinfo.go`**

Copy `internal/opencodemsb/sysinfo.go`, change package to `sysinfo`. All functions already exported. No other changes.

- [ ] **Step 2: Create `internal/sysinfo/sysinfo_test.go`**

Copy `internal/opencodemsb/sysinfo_test.go`, change package to `sysinfo`. No other changes.

- [ ] **Step 3: Run tests**

Run: `go test ./internal/sysinfo/... -v`
Expected: all tests pass

- [ ] **Step 4: Commit**

```bash
git add internal/sysinfo/
git commit -m "refactor: create internal/sysinfo package"
```

---

### Task 5: Create `internal/git/` package

**Files:**
- Create: `internal/git/git.go` — `ProjectSlug`, `BranchSlug`, `BranchName`, `CurrentWorktreePath`, `EnsureWorktree`, `WorktreePath`
- Create: `internal/git/git_test.go`

**Interfaces:**
- Consumes: `log.Logger` from `internal/log`
- Produces: `git.ProjectSlug(logger)`, `git.BranchSlug(branch)`, `git.BranchName(cwd)`, `git.CurrentWorktreePath(cwd)`, `git.EnsureWorktree(repoRoot, stateDir, projectSlug, branch)`, `git.WorktreePath(stateDir, projectSlug, branch)`

**Key changes from original `worktree.go`:**
- Package name: `git` (was `opencodemsb`)
- `ProjectSlug()` takes `*log.Logger` parameter (for the "not in git repo" warning)
- `warn(...)` → `logger.Warn(...)`
- `RemoveWorktree()` deleted (dead code — never called)

- [ ] **Step 1: Create `internal/git/git.go`**

Copy `internal/opencodemsb/worktree.go`, change package to `git`. Delete `RemoveWorktree()` function (lines 93-96). Change `ProjectSlug()` signature to `func ProjectSlug(logger *log.Logger) string` and replace `warn(...)` with `logger.Warn(...)`. Add import `"gitlab.inoio.de/inoio/opencode-msb/internal/log"`.

- [ ] **Step 2: Create `internal/git/git_test.go`**

Copy `internal/opencodemsb/worktree_test.go`, change package to `git`. No changes to existing tests needed (they test `BranchSlug` and `WorktreePath` which don't take logger). Add `"os"` and `"strings"` to imports if adding a `TestProjectSlugNotInGitRepo` test (optional — the existing 4 tests are sufficient).

- [ ] **Step 3: Run tests**

Run: `go test ./internal/git/... -v`
Expected: all tests pass

- [ ] **Step 4: Commit**

```bash
git add internal/git/
git commit -m "refactor: create internal/git package, remove dead RemoveWorktree()"
```

---

### Task 6: Create `internal/sandbox/` package (merge + build-tag cleanup + code fixes)

This is the largest task. It creates the `internal/sandbox/` package by merging `_sdk.go` files with their base files, deleting `_nosdk.go` files (by not copying them), removing build tags, changing the SDK alias from `m` to `msb`, and applying all code fixes.

**Files:**
- Create: `internal/sandbox/data.go` — `//go:embed data/Dockerfile`
- Create: `internal/sandbox/data/Dockerfile`
- Create: `internal/sandbox/secrets.go` (merged `secrets.go` + `secrets_sdk.go`)
- Create: `internal/sandbox/doctor.go` (merged `doctor.go` + `doctor_sdk.go`)
- Create: `internal/sandbox/image.go` (merged `image.go` + `image_sdk.go`)
- Create: `internal/sandbox/volumes.go` (merged `volumes.go` + `volumes_sdk.go`)
- Create: `internal/sandbox/runner.go` (merged `runner.go` + `runner_sdk.go`)
- Create: `internal/sandbox/secrets_test.go`, `image_test.go`, `volumes_test.go`, `runner_test.go`

**NOT created (deleted):**
- `doctor_test.go` — tautological tests (Finding 6)
- `secrets_sdk_test.go` — redundant SDK smoke test (Finding 5)

**Interfaces:**
- Consumes: `log.Logger`, `git.*`, `config.*`, `sysinfo.*`, `msb` SDK, moby Docker client
- Produces: `sandbox.Run(ctx, opts, cfg, logger)`, `sandbox.EnsureImage(ctx, dockerfile, force, logger)`, `sandbox.VolumeManager`, `sandbox.NewVolumeManager(fallback, stateDir, logger)`, `sandbox.BuildSecrets(logger)`, `sandbox.CheckAll(ctx, logger)`, `sandbox.EmbeddedDockerfile`, `sandbox.RunOptions`, `sandbox.Config`, `sandbox.ExitError`

**Key changes applied across all files:**
- Package name: `sandbox` (was `opencodemsb`)
- SDK alias: `m` → `msb`
- Remove `//go:build cgo` build tags
- `startSpinner(msg)` → `spin := log.NewSpinner(logger); spin.Start(msg)`; `spin.stop()` → `spin.Stop()`; `spin.stopError(err)` → `spin.StopError(err)`
- `warn(...)` → `logger.Warn(...)`
- `errorMsg(...)` → `logger.Error(...)`
- `exitError` → `ExitError` (exported, with `Code` field exported)
- `runCommand(opts)` → `Run(ctx, opts, cfg, logger)` (exported)
- `NODE_ENV=development` removed from `vmEnv` (Finding 4)
- `scanBuildOutput` returns decode errors instead of `nil` (Finding 7)
- `SecretMap` → `secretMap` (unexported)
- `BuildSecrets()` → `BuildSecrets(logger)`
- `CheckAll(ctx)` → `CheckAll(ctx, logger)`; all `Check*` functions take `logger`
- `NewVolumeManager(fallback, stateDir)` → `NewVolumeManager(fallback, stateDir, logger)`; `VolumeManager` has `logger` field
- `EnsureImage(ctx, dockerfile, force)` → `EnsureImage(ctx, dockerfile, force, logger)`
- Cross-package calls: `ProjectSlug()` → `git.ProjectSlug(logger)`, `NumCPUs()` → `sysinfo.NumCPUs()`, etc.

- [ ] **Step 1: Create directory and copy data files**

```bash
mkdir -p internal/sandbox/data
cp internal/opencodemsb/data/Dockerfile internal/sandbox/data/Dockerfile
```

- [ ] **Step 2: Create `internal/sandbox/data.go`**

```go
package sandbox

import _ "embed"

//go:embed data/Dockerfile
var EmbeddedDockerfile []byte
```

- [ ] **Step 3: Create `internal/sandbox/secrets.go`**

Merge `secrets.go` + `secrets_sdk.go`. Package `sandbox`, SDK alias `msb`, `SecretMap` → `secretMap`, `warn(...)` → `logger.Warn(...)`, `BuildSecrets` takes `*log.Logger`. No build tag. Remove the `logMu`/`logOut` globals and `warn()`/`errorMsg()` helper functions (they move to the logger).

- [ ] **Step 4: Create `internal/sandbox/doctor.go`**

Merge `doctor.go` + `doctor_sdk.go`. Package `sandbox`, SDK alias `msb`, `errorMsg(...)` → `logger.Error(...)`. `CheckMsb(ctx, logger)`, `CheckDocker(logger)`, `CheckKvm(logger)`, `CheckGit(logger)`, `CheckAll(ctx, logger)` all take `*log.Logger`. No build tag.

- [ ] **Step 5: Create `internal/sandbox/image.go`**

Merge `image.go` + `image_sdk.go`. Package `sandbox`, SDK alias `msb`. `EnsureImage` takes `*log.Logger`. `startSpinner(...)` → `log.NewSpinner(logger)` + spin methods. Fix `scanBuildOutput`: change line `return nil` (on non-EOF decode error) to `return fmt.Errorf("unexpected Docker build output: %w", err)`. No build tag.

- [ ] **Step 6: Create `internal/sandbox/volumes.go`**

Merge `volumes.go` + `volumes_sdk.go`. Package `sandbox`, SDK alias `msb`. `VolumeManager` has `logger *log.Logger` field. `NewVolumeManager(fallback, stateDir, logger)`. `warn(...)` → `vm.logger.Warn(...)`. `startSpinner(...)` → `log.NewSpinner(vm.logger)` + spin methods. No build tag.

- [ ] **Step 7: Create `internal/sandbox/runner.go`**

Merge `runner.go` + `runner_sdk.go`. This is the largest file. Key changes:
- Package `sandbox`, SDK alias `msb`
- Define `RunOptions`, `Config`, `ExitError` structs (all exported)
- `runCommand(opts)` → `Run(ctx context.Context, opts RunOptions, cfg Config, logger *log.Logger) error`
- Remove `NODE_ENV=development` from `vmEnv`
- `newTiming(opts.Timing)` → `log.NewTiming(logger, opts.Timing)`
- `CheckAll(ctx)` → `CheckAll(ctx, logger)`
- `ProjectSlug()` → `git.ProjectSlug(logger)`
- `BranchName(".")` → `git.BranchName(".")`
- `CurrentWorktreePath(cwd)` → `git.CurrentWorktreePath(cwd)`
- `EnsureWorktree(cwd, stateDir, ...)` → `git.EnsureWorktree(cwd, cfg.StateDir, ...)`
- `BranchSlug(branch)` → `git.BranchSlug(branch)`
- `EnsureImage(ctx, dockerfile, opts.ImageRebuild)` → `EnsureImage(ctx, dockerfile, opts.ImageRebuild, logger)`
- `NewVolumeManager(opts.VolumeFallback, stateDir)` → `NewVolumeManager(opts.VolumeFallback, cfg.StateDir, logger)`
- `LoadProviderConfig(EmbeddedProviderConfig)` → `config.LoadProviderConfig(config.EmbeddedProviderConfig)`
- `BuildMergedConfig(userConfigDir, ...)` → `config.BuildMergedConfig(cfg.UserConfigDir, ...)`
- `BuildSecrets()` → `BuildSecrets(logger)`
- `NumCPUs()` → `sysinfo.NumCPUs()`
- `TotalMemoryGiB()` → `sysinfo.TotalMemoryGiB()`
- `startSpinner(...)` → `log.NewSpinner(logger)` + spin methods
- `warn(...)` → `logger.Warn(...)`
- `m.` → `msb.` everywhere (CreateSandbox, Mount, WithImage, etc.)
- No build tag

- [ ] **Step 8: Create `internal/sandbox/runner_test.go`**

Copy `internal/opencodemsb/runner_test.go`, change package to `sandbox`. Remove `NODE_ENV` assertion from `TestBuildEnvMap` (the two lines checking `got["NODE_ENV"] != "development"`).

- [ ] **Step 9: Create `internal/sandbox/image_test.go`**

Copy `internal/opencodemsb/image_test.go`, change package to `sandbox`. Update `TestEnsureImageReturnsErrorWithoutDocker` to pass a real logger: `l := log.New(io.Discard, false)` and add imports for `"io"` and `"gitlab.inoio.de/inoio/opencode-msb/internal/log"`.

- [ ] **Step 10: Create `internal/sandbox/volumes_test.go`**

Copy `internal/opencodemsb/volumes_test.go`, change package to `sandbox`. Update `NewVolumeManager` calls to pass a logger: `l := log.New(nil, false)` then `NewVolumeManager(true, "/tmp/state", l)`. Add import for `internal/log`.

- [ ] **Step 11: Create `internal/sandbox/secrets_test.go`**

Copy `internal/opencodemsb/secrets_test.go`, change package to `sandbox`. `SecretMap` → `secretMap`. `BuildSecrets()` → `BuildSecrets(l)` where `l := log.New(io.Discard, false)`. Add imports for `"io"` and `internal/log`. Do NOT copy `secrets_sdk_test.go`.

- [ ] **Step 12: Run tests**

Run: `go test ./internal/sandbox/... -v`
Expected: all tests pass

- [ ] **Step 13: Commit**

```bash
git add internal/sandbox/
git commit -m "refactor: create internal/sandbox package, merge SDK files, remove build tags"
```

---

### Task 7: Update `cmd/opencode-msb/` and delete old package

**Files:**
- Create: `cmd/opencode-msb/cli.go` — cobra commands, Config, Execute()
- Modify: `cmd/opencode-msb/main.go` — update imports
- Delete: `internal/opencodemsb/` (entire directory)

**Interfaces:**
- Consumes: `sandbox.Run`, `sandbox.CheckAll`, `sandbox.ExitError`, `sandbox.RunOptions`, `sandbox.Config`, `log.New`, `log.NewTiming`
- Produces: working `opencode-msb` binary using new packages

**Key changes:**
- `setLogOutput()` deleted (Finding 1)
- `parseRunFlags()` replaced with "prepend run" pattern (Finding 3)
- `init()` replaced with explicit Config construction in `Execute()`
- `version` var stays for `-ldflags`

- [ ] **Step 1: Create `cmd/opencode-msb/cli.go`**

New file. Contains `Execute()`, cobra root + doctor subcommands, `RunOptions` (re-exported from sandbox), Config construction, and the "prepend run" default-command pattern. The pattern: if `os.Args[1]` is not a known subcommand (`doctor`, `run`, `help`, `--help`, `-h`, `--version`, `-v`) and doesn't start with `-`, prepend `"run"` to args so cobra routes to the run subcommand. Also handles bare `opencode-msb` (no args) by prepending `"run"`.

```go
package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"gitlab.inoio.de/inoio/opencode-msb/internal/log"
	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox"
)

var version = "dev"

func Execute() error {
	root := &cobra.Command{
		Use:     "opencode-msb",
		Short:   "Run opencode inside an ephemeral microsandbox VM",
		Version: version,
	}

	root.AddCommand(buildDoctorCmd())
	root.AddCommand(buildRunCmd())

	args := os.Args[1:]
	if len(args) == 0 || !isKnownSubcommand(args[0]) {
		os.Args = append([]string{os.Args[0], "run"}, args...)
	}

	return root.Execute()
}

func isKnownSubcommand(arg string) bool {
	switch arg {
	case "doctor", "run", "help", "--help", "-h", "--version", "-v":
		return true
	default:
		return false
	}
}

func buildDoctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check prerequisites",
		RunE: func(cmd *cobra.Command, args []string) error {
			logger := newLogger()
			if !sandbox.CheckAll(cmd.Context(), logger) {
				return fmt.Errorf("preflight failed")
			}
			fmt.Fprintln(os.Stderr, "doctor: all checks passed")
			return nil
		},
	}
}

func buildRunCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run [flags] [ARGS...]",
		Short: "Run opencode in a microsandbox VM",
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := sandbox.RunOptions{Args: args}
			opts.Worktree, _ = cmd.Flags().GetString("worktree")
			opts.ImageRebuild, _ = cmd.Flags().GetBool("image-rebuild")
			opts.VolumeFallback, _ = cmd.Flags().GetBool("volume-fallback")
			opts.ResetHome, _ = cmd.Flags().GetBool("reset-home")
			opts.CPUs, _ = cmd.Flags().GetUint8("cpus")
			opts.Memory, _ = cmd.Flags().GetString("memory")
			opts.Timing, _ = cmd.Flags().GetBool("timing")

			cfg := newConfig()
			logger := newLogger()

			err := sandbox.Run(cmd.Context(), opts, cfg, logger)
			var exitErr *sandbox.ExitError
			if errors.As(err, &exitErr) {
				os.Exit(exitErr.Code)
			}
			return err
		},
	}

	cmd.Flags().String("worktree", "", "Worktree name")
	cmd.Flags().Bool("image-rebuild", false, "Force image rebuild")
	cmd.Flags().Bool("volume-fallback", false, "Use host directories instead of msb volumes")
	cmd.Flags().Bool("reset-home", false, "Recreate the project home volume")
	cmd.Flags().Uint8("cpus", 0, "Number of CPUs (default: all)")
	cmd.Flags().String("memory", "4G", "Memory limit (default: 4G)")
	cmd.Flags().Bool("timing", false, "Print per-phase launcher timing to stderr")

	return cmd
}

func newConfig() sandbox.Config {
	home, _ := os.UserHomeDir()
	return sandbox.Config{
		StateDir:      filepath.Join(home, ".local", "share", "opencode-msb"),
		UserConfigDir: filepath.Join(home, ".config", "inoio-sandbox", "opencode"),
	}
}

func newLogger() *log.Logger {
	return log.New(os.Stderr, term.IsTerminal(int(os.Stderr.Fd())))
}
```

- [ ] **Step 2: Update `cmd/opencode-msb/main.go`**

```go
package main

import (
	"fmt"
	"os"
)

func main() {
	if err := Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
```

- [ ] **Step 3: Delete old package**

```bash
git rm -r internal/opencodemsb/
```

- [ ] **Step 4: Verify build**

Run: `go build ./cmd/opencode-msb`
Expected: succeeds

- [ ] **Step 5: Run all tests**

Run: `go test ./...`
Expected: all tests pass (old package deleted, new packages have their tests)

- [ ] **Step 6: Commit**

```bash
git add cmd/opencode-msb/ internal/opencodemsb/
git commit -m "refactor: switch cmd/ to new packages, delete old opencodemsb package"
```

---

### Task 8: Add tooling (Makefile + .golangci.yml)

**Files:**
- Create: `Makefile`
- Create: `.golangci.yml`

- [ ] **Step 1: Create Makefile**

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

- [ ] **Step 2: Create `.golangci.yml`**

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

- [ ] **Step 3: Verify**

Run: `make build && make test && make vet`
Expected: all succeed

- [ ] **Step 4: Commit**

```bash
git add Makefile .golangci.yml
git commit -m "chore: add Makefile and .golangci.yml"
```

---

### Task 9: Update CI, .gitignore, README

**Files:**
- Modify: `.gitlab-ci.yml`
- Modify: `.gitignore`
- Modify: `README.md`

- [ ] **Step 1: Update `.gitlab-ci.yml`**

Remove Python `lint` and `unit-tests` stages. Rename `go-lint` → `lint`, `go-test` → `test`, `go-build` → `build`. Remove `PIP_DISABLE_PIP_VERSION_CHECK`. Add `release` stage. Use `make` commands.

```yaml
stages:
  - lint
  - test
  - build
  - release

variables:
  CGO_ENABLED: "1"

lint:
  stage: lint
  image: golang:1.26
  script:
    - go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
    - golangci-lint run ./...

test:
  stage: test
  image: golang:1.26
  script:
    - go mod download
    - go test ./... -v

build:
  stage: build
  image: golang:1.26
  script:
    - go mod download
    - go build -o opencode-msb ./cmd/opencode-msb
    - ./opencode-msb --version
  artifacts:
    paths:
      - opencode-msb

release:
  stage: release
  image: golang:1.26
  rules:
    - if: $CI_COMMIT_TAG
  script:
    - go build -o opencode-msb-linux-amd64 ./cmd/opencode-msb
  release:
    name: "opencode-msb $CI_COMMIT_TAG"
    description: "Release $CI_COMMIT_TAG"
    tag_name: "$CI_COMMIT_TAG"
    assets:
      links:
        - name: "opencode-msb-linux-amd64"
          url: "${CI_PROJECT_URL}/-/jobs/${CI_JOB_ID}/artifacts/file/opencode-msb-linux-amd64"
```

- [ ] **Step 2: Update `.gitignore`**

```
/opencode-msb
/.idea/
/.gitlab-ci-local/
/.envrc
```

- [ ] **Step 3: Rewrite `README.md`**

```markdown
# opencode-msb

Run opencode inside an ephemeral microsandbox VM.

## Install

Download the latest Linux binary from [GitLab Releases](https://gitlab.inoio.de/inoio/opencode-msb/-/releases):

```bash
curl -fsSL -o opencode-msb "https://gitlab.inoio.de/inoio/opencode-msb/-/releases/latest/download/opencode-msb-linux-amd64"
chmod +x opencode-msb && sudo mv opencode-msb /usr/local/bin/
```

Or install from source (requires Go + CGO toolchain):

```bash
export GOPRIVATE=gitlab.inoio.de
go install gitlab.inoio.de/inoio/opencode-msb/cmd/opencode-msb@latest
```

## Usage

```bash
opencode-msb                    # run opencode in a sandbox
opencode-msb --worktree my-feature  # run in a named git worktree
opencode-msb doctor             # check prerequisites
opencode-msb run --worktree my-feature  # explicit run subcommand
```

## Flags

| Flag | Default | Purpose |
|---|---|---|
| `--worktree` | `""` | create/use a git worktree by that name |
| `--image-rebuild` | `false` | force rebuild of the runner image |
| `--volume-fallback` | `false` | use project-local dirs instead of msb volumes |
| `--reset-home` | `false` | wipe the opencode home volume before run |
| `--cpus` | host CPU count | vCPUs for the sandbox |
| `--memory` | `4G` | memory limit (e.g. `4G`, `512M`) |
| `--timing` | `false` | print per-phase launcher timing to stderr |

## Project overrides

Create `.sandbox/Dockerfile` to override the runner image.
Create `.sandbox/env` to add environment variables.
```

- [ ] **Step 4: Verify build**

Run: `make build && ./opencode-msb --version`
Expected: prints version

- [ ] **Step 5: Commit**

```bash
git add .gitlab-ci.yml .gitignore README.md
git commit -m "chore: update CI, gitignore, README for Go-only project"
```

---

### Task 10: Final verification

- [ ] **Step 1: Run go mod tidy**

Run: `go mod tidy`

- [ ] **Step 2: Run go vet**

Run: `go vet ./...`
Expected: no issues

- [ ] **Step 3: Run all tests**

Run: `go test ./... -v`
Expected: all pass

- [ ] **Step 4: Run linter**

Run: `golangci-lint run ./...`
Expected: no issues (fix any that come up)

- [ ] **Step 5: Build binary**

Run: `make build`
Expected: produces `opencode-msb` binary

- [ ] **Step 6: Verify binary**

Run: `./opencode-msb --version`
Expected: prints version

- [ ] **Step 7: Final commit if any fixes were needed**

```bash
git add -A
git commit -m "fix: resolve lint and vet issues from restructuring"
```
