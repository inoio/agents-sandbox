# CLI Integration Testability Design

## Goal

Make `Execute(args []string, ui stdio.UI) error` in `cmd/opencode-msb/cli.go` testable end-to-end without requiring Docker, KVM, msb, or any external daemon.

## Current State

The sandbox package already provides robust test seams via factory variables (`newMsbClient`) and interfaces (`dockerClient`) that allow overriding real implementations with mocks (see `prune_client_test.go`, `projectvm_test.go`). The CLI package (`cli.go` + `commands.go`) uses `stdio.UI` for output (fully abstracted with `stdio.Mock`).

Two things prevent comprehensive integration testing from the CLI layer:

1. `os.Exit(exitErr.Code)` called inside `runFunc` (commands.go:353) and `buildShellCmd` (commands.go:142) — prevents testing error-exit paths from Go tests.
2. `client.New(client.FromEnv)` (moby) called inline in `buildPruneCmd` (commands.go:374) — no way to inject a mock Docker client from CLI tests.

## Detailed Design

### 1. Remove `os.Exit()` from command handlers

In `commands.go`, remove `os.Exit()` calls in `runFunc` (line 353) and `buildShellCmd` (line 142). Replace with plain return of the `sandbox.ExitError`:

```go
// Before (runFunc, commands.go:350-355):
err := sandbox.Run(cmd.Context(), opts, cfg, ui)
var exitErr *sandbox.ExitError
if errors.As(err, &exitErr) {
    os.Exit(exitErr.Code)
}
return err

// After:
return sandbox.Run(cmd.Context(), opts, cfg, ui)
```

Same for `buildShellCmd`. The `errors` import in `commands.go` is used elsewhere (`buildDoctorCmd: errors.New`), so it stays. Remove `os` import from `commands.go` if it becomes unused.

**Impact**: Command handlers return `*sandbox.ExitError` instead of exiting. The entry point (`main`) handles `ExitError` and calls `os.Exit` at the top level.

### 2. Abstract Docker client creation in `buildPruneCmd`

**Add to `internal/sandbox/` (e.g., `cleanup.go`)**:
An exported `DockerClient` interface covering methods `Prune` uses internally:

```go
// DockerClient defines the Docker API surface needed for pruning.
// It is a subset of github.com/moby/moby/client.Client.
type DockerClient interface {
    ImageRemove(ctx context.Context, image string, opts client.ImageRemoveOptions) (client.ImageRemoveResult, error)
    Close() error
}
```

**Add to `cmd/opencode-msb/commands.go`**:
A factory variable following the existing pattern in the sandbox package:

```go
import sandbox "gitlab.inoio.de/inoio/opencode-msb/internal/sandbox"

var newDockerClient = func() (sandbox.DockerClient, error) {
    cli, err := client.New(client.FromEnv)
    if err != nil {
        return nil, err
    }
    return cli, nil // *client.Client satisfies sandbox.DockerClient
}
```

Update `buildPruneCmd` (line 374):

```go
// Before:
dockerCli, err := client.New(client.FromEnv)

// After:
dockerCli, err := newDockerClient()
```

Remove the `github.com/moby/moby/client` import from `commands.go` (only used for the Docker client creation).

**Tests override via** `t.Cleanup(func() { oldFn := newDockerClient; newDockerClient = mockFn })`.

### 3. Handle `*sandbox.ExitError` at the entry point

In `main.go`, after calling `Execute()`:

```go
// Before:
if err := Execute(args, ui); err != nil {
    ui.Error("Failed: ", err)
    os.Exit(1)
}

// After:
if err := Execute(args, ui); err != nil {
    var exitErr *sandbox.ExitError
    if errors.As(err, &exitErr) {
        os.Exit(exitErr.Code)
    }
    ui.Error("Failed: ", err)
    os.Exit(1)
}
```

## What Stays Unchanged (Tested via temp files)

| Dependency | Current State | Test Approach |
|------------|---------------|---------------|
| `os.UserHomeDir()` in `newConfig()` | Direct call | `t.Setenv("HOME", tempDir)` via `os.UserHomeDir` hook |
| `os.Stat()` in `buildConfigCmd` | Direct call | Create known directory structure in temp dir |
| `launcherconfig.Load()` | Uses viper | Create temp `.opencode-msb/` dirs with JSON configs |
| `os.Args[0]` in CLI entry | Hardcoded in `main` | Not part of `Execute()` |

## Test Coverage Targets

| Commands | Testing Strategy |
|----------|-----------------|
| `tree`, `version`, `--help`, `doctor` | `Execute(args, mockUI)` → assert `mockUI` calls |
| `list`, `sandbox list`, `image list`, `volume list` | Override `sandbox.newMsbClient` to return mock handles |
| `build` | Override `sandbox.newMsbClient` (sandbox already abstracts dockerClient via interface) |
| `prune` | Override `newDockerClient` in CLI + `sandbox.newMsbClient` |
| `run`, `shell` | Override `sandbox.newMsbClient` and `sandbox.ensureInstalled` |
| `stop`, `kill` | Override `sandbox.newMsbClient` |
| `config show` | Temp dirs with known config files |
| Error paths (unknown flags, bad args) | Cobra handles — assert error strings |

## Files Modified

| File | Changes |
|------|---------|
| `internal/sandbox/cleanup.go` | Add exported `DockerClient` interface |
| `cmd/opencode-msb/commands.go` | Remove 2 `os.Exit`, add `newDockerClient` factory var, update `buildPruneCmd`, remove moby import |
| `cmd/opencode-msb/main.go` | Handle `*sandbox.ExitError` at top level (use exit code from sandbox vs always 1) |
| `cmd/opencode-msb/commands_test.go` (new) | Integration tests for all commands |

## Open Questions

N/A — design approved by user.