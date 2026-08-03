# CLI Integration Testability Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `Execute(args []string, ui stdio.UI)` testable end-to-end by removing `os.Exit()` calls from cobra handlers and injecting the Docker client via a factory variable.

**Architecture:** Two changes to production code, zero new production dependencies. 1) Export `DockerClient` from sandbox package so CLI can use it. 2) Add `newDockerClient` factory variable in `commands.go`. 3) Remove `os.Exit` from cobra handlers so they return errors instead of terminating the process. 4) Handle `*sandbox.ExitError` at the `main.go` entry point. Tests then override factory variables to mock dependencies.

**Tech Stack:** Go, cobra, moby/client via interface.

## Global Constraints

- Go idiomatic patterns: use temp dirs/fixtures, not mocks for filesystem
- Follow existing factory variable pattern: `var newXyz = func() Xyz { return real }` at package level, tests override via `t.Cleanup`
- Keep abstractions minimal; do not introduce new layers
- Apply: SOLID, DRY, KISS, YAGNI, Convention over Configuration
- Every task must compile and pass existing tests before moving to next
- No `os.Exit` calls from Go test functions

---

### Task 1: Export `DockerClient` interface in sandbox package

**Files:**
- Modify: `internal/sandbox/image.go`

**Interfaces:**
- Introduces: `sandbox.DockerClient` exported interface with two methods: `ImageRemove` and `Close`
- Consumes: `context.Context`, `github.com/moby/moby/client.ImageRemoveOptions`, `client.ImageRemoveResult`

**Step 1: Add the exported interface type**

In `internal/sandbox/image.go`, add this after the existing import block (after line 20), before the `const` block:

```go
// DockerClient is the exported interface for Docker API operations needed by
// the prune command. It lets CLI code create and pass a Docker client for
// pruning without depending directly on the moby client package.
type DockerClient interface {
	ImageRemove(ctx context.Context, imageID string, opts client.ImageRemoveOptions) (client.ImageRemoveResult, error)
	Close() error
}
```

Note: The existing unexported `dockerClient` interface on lines 28-54 of `image.go` is used by build operations internally. The new exported `DockerClient` is a minimal subset specifically for prune.

**Step 2: Verify internal code still compiles**

The `dockerClient` internal type (lowercase) in `prune.go` is used by `pruneActiveVMDockerImages`, `pruneStaleCascade`, `pruneActiveVMCleanup`, `pruneOrphanSlug`, `removeDockerImages`, and `Prune`. All these functions accept `dockerClient` (unexported). The new `DockerClient` (exported) is a separate type alias. No internal code is affected.

Run: `go build ./internal/sandbox/...`
Expected: COMPILATION SUCCESS

**Step 3: Verify CLI package can import the new type**

Run: `go build ./cmd/opencode-msb/...`
Expected: COMPILATION SUCCESS

**Step 4: Commit**

```bash
git add internal/sandbox/image.go
git commit -m "feat(sandbox): export DockerClient interface for prune command"
```

---

### Task 2: Add `newDockerClient` factory variable in commands.go

**Files:**
- Modify: `cmd/opencode-msb/commands.go`

**Interfaces:**
- Consumes: `sandbox.DockerClient` from `internal/sandbox`, `github.com/moby/moby/client` (for creation)
- Produces: Factory variable `newDockerClient` for CLI tests to override

**Step 1: Add factory variable at package level**

In `cmd/opencode-msb/commands.go`, between the import block and the first function definition, add:

```go
// newDockerClient creates a new Docker client for the prune command.
// Tests override this to inject stub clients.
var newDockerClient = func() (sandbox.DockerClient, error) {
    cli, err := client.New(client.FromEnv)
    if err != nil {
        return nil, err
    }
    return cli, nil
}
```

This uses the existing `client` import from moby (line 10). The `*client.Client` type from moby satisfies the `sandbox.DockerClient` interface because it has `ImageRemove` and `Close` methods with compatible signatures.

**Step 2: Update buildPruneCmd to use factory instead of direct call**

Replace lines 374-377:

```go
// Before:
dockerCli, err := client.New(client.FromEnv)
if err != nil {
    return fmt.Errorf("cannot connect to Docker daemon (is dockerd running?): %w", err)
}

// After:
dockerCli, err := newDockerClient()
if err != nil {
    return fmt.Errorf("cannot connect to Docker daemon (is dockerd running?): %w", err)
}
```

The rest of the function (lines 378-393) stays the same. `dockerCli.Close()` is called correctly on line 379 since `sandbox.DockerClient` has `Close() error`.

**Step 3: Verify compilation**

Run: `go build ./cmd/opencode-msb/...`
Expected: COMPILATION SUCCESS

**Step 4: Commit**

```bash
git add cmd/opencode-msb/commands.go
git commit -m "feat(cli): abstract Docker client creation with factory variable"
```

---

### Task 3: Remove `os.Exit()` from `runFunc`

**Files:**
- Modify: `cmd/opencode-msb/commands.go`

**Interfaces:**
- Removes: `os.Exit()` call from handler function (line 353)
- No new interfaces needed

**Step 1: Simplify runFunc error handling**

Replace lines 350-355:

```go
// Change from:
err := sandbox.Run(cmd.Context(), opts, cfg, ui)
var exitErr *sandbox.ExitError
if errors.As(err, &exitErr) {
    os.Exit(exitErr.Code)
}
return err

// To:
return sandbox.Run(cmd.Context(), opts, cfg, ui)
```

**Step 2: Remove unused os import (if applicable)**

The `os` package is still used on line 171 (`os.Stat`), so the import must stay.

**Step 3: Verify compilation**

Run: `go build ./cmd/opencode-msb/...`
Expected: COMPILATION SUCCESS

**Step 4: Verify existing tests pass**

Run: `go test ./cmd/opencode-msb/... -v`
Expected: ALL 19 existing tests PASS

**Step 5: Commit**

```bash
git add cmd/opencode-msb/commands.go
git commit -m "refactor(cli): remove os.Exit from runFunc, return error instead"
```

---

### Task 4: Remove `os.Exit()` from `buildShellCmd`

**Files:**
- Modify: `cmd/opencode-msb/commands.go`

**Interfaces:**
- Removes: `os.Exit()` call from command handler (line 142)
- No new interfaces needed

**Step 1: Simplify buildShellCmd error handling**

Replace lines 139-144:

```go
// Change from:
err := sandbox.Shell(cmd.Context(), opts, cfg, ui)
var exitErr *sandbox.ExitError
if errors.As(err, &exitErr) {
    os.Exit(exitErr.Code)
}
return err

// To:
return sandbox.Shell(cmd.Context(), opts, cfg, ui)
```

**Step 2: Verify compilation**

Run: `go build ./cmd/opencode-msb/...`
Expected: COMPILATION SUCCESS

**Step 3: Verify tests pass**

Run: `go test ./cmd/opencode-msb/... -v`
Expected: ALL tests PASS

**Step 4: Commit**

```bash
git add cmd/opencode-msb/commands.go
git commit -m "refactor(cli): remove os.Exit from buildShellCmd, return error instead"
```

---

### Task 5: Handle `*sandbox.ExitError` at the entry point

**Files:**
- Modify: `cmd/opencode-msb/main.go`

**Interfaces:**
- Consumes: `errors` from stdlib, `sandbox.ExitError` from `internal/sandbox`
- Produces: Proper exit code propagation (sandbox exit code vs default 1)

**Step 1: Rewrite main.go**

Replace the entire `cmd/opencode-msb/main.go` file content with:

```go
package main

import (
	"errors"
	"os"

	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox"
)

func main() {
	args := os.Args[1:]
	ui := newUI(args)
	ui.Verbose("Initialized terminal output")

	if err := Execute(args, ui); err != nil {
		var exitErr *sandbox.ExitError
		if errors.As(err, &exitErr) {
			os.Exit(exitErr.Code)
		}
		ui.Error("Failed: ", err)
		os.Exit(1)
	}
}
```

Changes from original:
- Removed `fmt` import → replaced with `errors` (for `errors.As`)
- Added `internal/sandbox` import (for `*sandbox.ExitError`)
- Added `ExitError` type check before default `os.Exit(1)`
- Kept `ui.Error` for all non-ExitError errors

**Step 2: Verify compilation**

Run: `go build ./cmd/opencode-msb/...`
Expected: COMPILATION SUCCESS

**Step 3: Verify all tests pass**

Run: `go test ./cmd/opencode-msb/... -v`
Expected: ALL tests PASS

**Step 4: Commit**

```bash
git add cmd/opencode-msb/main.go
git commit -m "refactor(main): handle sandbox.ExitError at entry point"
```

---

### Task 6: Full verification

**Files:**
- No files changed

**Step 1: Run full test suite**

Run: `go test ./...`
Expected: ALL tests PASS

**Step 2: Run linter**

Run: `golangci-lint run`
Expected: PASS (no new issues, existing issues unchanged)

**Step 3: Run formatter**

Run: `golangci-lint fmt`
Expected: Formats any files that need it

**Step 4: Verify the binary builds**

Run: `go build -o /tmp/opencode-msb ./cmd/opencode-msb`
Expected: Binary built successfully

**Step 5: Smoke test the binary**

Run: `/tmp/opencode-msb --help`
Expected: Show help text successfully

**Step 6: Verify dry-run works**

Run: `go run ./cmd/opencode-msb --dry-run`
Expected: Builds and exits (skips opencode)

---

### Task 7: Add testability documentation

**Files:**
- Modify: `cmd/opencode-msb/cli.go`

**Step 1: Add comment block above Execute**

Add this doc comment above the existing `Execute` function in `cli.go` (replace line 49 with the commented version):

```go
// Execute runs the CLI with the given arguments and UI.
//
// For integration testing, override factory variables:
//   - sandbox packages: sandbox.NewMsbClient (same pattern as prune_client_test.go)
//   - this package: newDockerClient (for prune command mock injection)
//
// Example:
//
//	func TestListSandboxCommand(t *testing.T) {
//	    old := sandbox.NewMsbClient
//	    sandbox.NewMsbClient = func() sandbox.MsbClient { return mock }
//	    t.Cleanup(func() { sandbox.NewMsbClient = old })
//
//	    ui := stdio.NewMock(t)
//	    err := Execute([]string{"list"}, ui)
//	    // ...assert...
//	}
func Execute(args []string, ui stdio.UI) error {
```

**Step 2: Verify compilation and commit**

Run: `go build ./cmd/opencode-msb/...`
Then:

```bash
git add cmd/opencode-msb/cli.go
git commit -m "docs(cli): add testability comment to Execute function"
```

---

## Execution Summary

| # | Task | Files Modified | Why |
|---|------|---------------|-----|
| 1 | Export `DockerClient` | `internal/sandbox/image.go` | CLI needs exported interface to use it |
| 2 | Docker factory variable | `cmd/opencode-msb/commands.go` | Prune command can be mocked |
| 3 | Remove `os.Exit` from `runFunc` | `cmd/opencode-msb/commands.go` | `run` command is testable |
| 4 | Remove `os.Exit` from `buildShellCmd` | `cmd/opencode-msb/commands.go` | `shell` command is testable |
| 5 | Handle `ExitError` in main | `cmd/opencode-msb/main.go` | Entry point handles returned errors |
| 6 | Full verification | No files | Ensure nothing regressed |
| 7 | Document testability | `cmd/opencode-msb/cli.go` | Future devs know how to test |

### Key Notes for Implementers

- **No interface splitting needed**: `*client.Client` from moby already has `ImageRemove` and `Close`, so it satisfies `sandbox.DockerClient` implicitly
- **`os` import stays in commands.go**: `os.Stat` is used in `buildConfigCmd` line 171
- **`errors` import stays in commands.go**: `errors.New` is used in `buildDoctorCmd` line 281
- **sandbox's `newMsbClient` is lowercase (package-private)**: CLI tests can't access it directly, but CLI-level tests don't need it — they test command structure, flag parsing, and error propagation. Sandbox-level tests (inside `internal/sandbox/`) still mock everything
- **After this, CLI tests can call `Execute([]string{"tree"}, ui)` and get structured output without external dependencies**