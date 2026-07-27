# One VM Per Project — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the per-invocation ephemeral VM model with one long-lived VM per project that hosts a persistent `opencode serve` daemon and supports parallel branch isolation via opencode's experimental workspaces feature.

**Architecture:** One detached microsandbox VM per project (`opencode-msb-vm-<slug>`) is created on first run and reused on subsequent runs. An `opencode serve` daemon inside the VM owns the SQLite db, MCP servers, and workspaces control-plane. Each invocation attaches a TUI client via `opencode attach`. Branch isolation uses opencode's native linked worktrees (same `project_id` → shared history), all within one kernel so `fcntl()` locks on SQLite work correctly.

**Tech Stack:** Go 1.26, microsandbox SDK v0.6.6, opencode 1.18.5+, cobra CLI, Docker (for image builds).

## Global Constraints

- Target platforms: Linux (KVM) and macOS (Apple Silicon).
- msb SDK: `github.com/superradcompany/microsandbox/sdk/go` v0.6.6 — exact signatures documented inline per task.
- opencode version: 1.18.5+ (workspaces flag recognized behind `OPENCODE_EXPERIMENTAL_WORKSPACES=true`).
- Secrets are only passed to VMs via msb's secret mechanism.
- Sandbox names limited to 128 UTF-8 bytes (SDK constraint).
- Code style: self-explanatory code, minimal abstractions, no comments unless non-obvious. SOLID, DRY, KISS, YAGNI. Small focused files. Composition over inheritance.
- Testing: prefer unit tests on pure functions with mocks over integration tests.
- Verification commands: `go test ./...`, `golangci-lint fmt`, `golangci-lint run`, `go run ./cmd/opencode-msb --dry-run`.
- New VM naming infix: `opencode-msb-vm-<slug>` (replaces `opencode-msb-sb-<slug>-<branch>`).
- The spike (Section 1 of the spec) is already complete — findings are in `docs/superpowers/notes/2026-07-27-workspaces-spike-findings.md`. This plan implements Sections 2–7.

## File Structure

| File | Responsibility | Status |
|---|---|---|
| `internal/sandbox/projectvm.go` | Project VM naming + lifecycle + env provisioning (`projectVMName`, `EnsureProjectVM`, `buildProjectVMEnv`, first-boot flock, `StopProjectVM`, `KillProjectVM`) | **Create** |
| `internal/sandbox/flock_linux.go`, `flock_darwin.go` | Platform-specific flock helper | **Create** |
| `internal/sandbox/daemon.go` | Daemon supervisor (`EnsureDaemon` — healthcheck, start, poll) | **Create** |
| `internal/sandbox/worktree.go` | Branch→worktree mapping (`ResolveTarget` via HTTP API) | **Create** |
| `internal/sandbox/runner.go` | Invocation flow (`Run`, `Shell`, `prepareSandbox`, `sandboxSession` cleanup) | **Modify** — rewrite invocation flow, remove managed-repo/clone machinery |
| `internal/sandbox/query.go` | List/filter sandboxes + volumes | **Modify** — filter prefix `sb-` → `vm-` |
| `internal/sandbox/doctor.go` | Orphan detection | **Modify** — orphan check for `vm-` prefix, obsolete `clone-` volumes |
| `internal/sandbox/volumes.go` | Volume management | **Modify** — remove `CloneVolume`, `cloneVolumeName`, `sameHomeVolumeInUse`, `extractNamedVolumes` |
| `internal/git/git.go` | Git operations | **Modify** — remove managed-clone/merge functions, add `PruneWorktrees` |
| `cmd/opencode-msb/cli.go` | CLI commands | **Modify** — add `stop`/`kill` commands, update `-b` help |
| `cmd/opencode-msb/cli_test.go` | CLI tests | **Modify** — update tree/flag tests for new commands |

---

## Task 1: Project VM naming, lifecycle, and env provisioning

**Files:**
- Create: `internal/sandbox/projectvm.go`
- Create: `internal/sandbox/projectvm_test.go`
- Create: `internal/sandbox/flock_linux.go`, `internal/sandbox/flock_darwin.go`
- Modify: `internal/sandbox/query.go` (filter prefix)

**Background — SDK signatures (from `sandbox.go`):**
- `msb.GetSandbox(ctx, name) (*SandboxHandle, error)` — returns `ErrSandboxNotFound` if missing.
- `(*SandboxHandle).Status()` → `msb.SandboxStatus` (`"running"`, `"stopped"`, `"crashed"`, `"draining"`, `"paused"`).
- `(*SandboxHandle).Connect(ctx) (*Sandbox, error)` — reattaches to a running VM.
- `(*SandboxHandle).Start(ctx) (*Sandbox, error)` — boots a stopped VM.
- `(*SandboxHandle).Refresh(ctx) (*SandboxHandle, error)` — returns a fresh handle for the same name.
- `msb.CreateSandbox(ctx, name, opts...) (*Sandbox, error)` — creates + boots a new VM.
- `(*Sandbox).Detach(ctx)` — releases handle without stopping the VM (use with `WithDetached`).
- `(*Sandbox).Close()` — releases handle (stops VM if not detached).
- `msb.IsKind(err, msb.ErrSandboxNotFound)` — checks not-found error.
- Options: `msb.WithDetached()`, `msb.WithIdleTimeout(d)`, `msb.WithReplace()`, `msb.WithImage(ref)`, `msb.WithMounts(m)`, `msb.WithEnv(e)`, `msb.WithUser(u)`, `msb.WithWorkdir(p)`, `msb.WithCPUs(n)`, `msb.WithMaxCPUs(n)`, `msb.WithMemory(mib)`, `msb.WithMaxMemory(mib)`, `msb.WithSecrets(s...)`.

**Design:** This task creates the project VM naming, lifecycle, and env provisioning in one file. `EnsureProjectVM` returns a live `*msb.Sandbox` and a boolean `created` (true if this invocation created+provisioned the VM fresh, false if it reused an existing one). The decision tree:
1. `GetSandbox` → NotFound → `CreateSandbox(WithDetached, WithIdleTimeout, mounts, env, ...)`. Return `sb, true`.
2. `GetSandbox` → Running → `handle.Connect()`. Return `sb, false`.
3. `GetSandbox` → Stopped/Crashed → `handle.Start()`. Return `sb, false` (VM already provisioned from prior creation).

The caller (Task 4) uses `created` to decide whether to provision config + start daemon. The first-boot race (two invocations both see NotFound) is guarded by a per-project host-side flock around the ensure-create step. The env map includes `OPENCODE_EXPERIMENTAL_WORKSPACES=true` via a `buildProjectVMEnv` helper.

**Interfaces:**
- Produces: `projectVMName(slug string) string`, `projectVMPrefix` constant, `EnsureProjectVM(...)`, `buildProjectVMEnv(...)`, `decideVMAction(...)`.
- Consumes: `RunOptions`, `Config`, `buildMounts`, `parseMemory`, `resolveTmpSizeMiB`, `isSandboxActive` (existing in `runner.go`).

- [ ] **Step 1: Write the failing tests**

```go
// internal/sandbox/projectvm_test.go
package sandbox

import (
	"testing"

	msb "github.com/superradcompany/microsandbox/sdk/go"
)

func TestProjectVMName(t *testing.T) {
	got := projectVMName("myproj-aBc1234D")
	want := "opencode-msb-vm-myproj-aBc1234D"
	if got != want {
		t.Errorf("projectVMName(%q) = %q, want %q", "myproj-aBc1234D", got, want)
	}
}

func TestProjectVMNameTruncation(t *testing.T) {
	longSlug := "p-abcdef-very-long-slug-that-exceeds-the-128-byte-limit-and-then-some-more-padding"
	got := projectVMName(longSlug)
	if len(got) > maxSandboxNameLen {
		t.Errorf("expected name <= %d bytes, got %d", maxSandboxNameLen, len(got))
	}
	if len(got) < len(projectVMPrefix) {
		t.Errorf("name too short: %q", got)
	}
}

func TestProjectVMPrefixConstant(t *testing.T) {
	if projectVMPrefix != "opencode-msb-vm-" {
		t.Errorf("expected prefix %q, got %q", "opencode-msb-vm-", projectVMPrefix)
	}
}

func TestBuildProjectVMEnvIncludesWorkspaces(t *testing.T) {
	envMap := map[string]string{
		"FOO": "bar",
	}
	buildProjectVMEnv(envMap)
	if envMap["OPENCODE_EXPERIMENTAL_WORKSPACES"] != "true" {
		t.Errorf("expected OPENCODE_EXPERIMENTAL_WORKSPACES=true, got %q", envMap["OPENCODE_EXPERIMENTAL_WORKSPACES"])
	}
}

func TestEnsureProjectVMCreatesWhenNotFound(t *testing.T) {
	decision, err := decideVMAction(msb.ErrSandboxNotFound, msb.SandboxStatus(""))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if decision != vmActionCreate {
		t.Errorf("expected vmActionCreate, got %v", decision)
	}
}

func TestEnsureProjectVMConnectsWhenRunning(t *testing.T) {
	decision, err := decideVMAction(nil, msb.SandboxStatusRunning)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if decision != vmActionConnect {
		t.Errorf("expected vmActionConnect, got %v", decision)
	}
}

func TestEnsureProjectVMStartsWhenStopped(t *testing.T) {
	decision, err := decideVMAction(nil, msb.SandboxStatusStopped)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if decision != vmActionStart {
		t.Errorf("expected vmActionStart, got %v", decision)
	}
}

func TestEnsureProjectVMStartsWhenCrashed(t *testing.T) {
	decision, err := decideVMAction(nil, msb.SandboxStatusCrashed)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if decision != vmActionStart {
		t.Errorf("expected vmActionStart for crashed, got %v", decision)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/sandbox/ -run "TestProjectVM|TestBuildProjectVMEnv|TestEnsureProjectVM" -v`
Expected: FAIL — `projectVMName`, `projectVMPrefix`, `buildProjectVMEnv`, `decideVMAction`, `vmActionCreate` etc. undefined.

- [ ] **Step 3: Write minimal implementation**

```go
// internal/sandbox/projectvm.go
package sandbox

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/superradcompany/microsandbox/sdk/go"
	"gitlab.inoio.de/inoio/opencode-msb/internal/git"
	"gitlab.inoio.de/inoio/opencode-msb/internal/output"
	"gitlab.inoio.de/inoio/opencode-msb/internal/sysinfo"
)

const projectVMPrefix = "opencode-msb-vm-"

func projectVMName(slug string) string {
	name := projectVMPrefix + slug
	if len(name) > maxSandboxNameLen {
		name = name[:maxSandboxNameLen]
	}
	return name
}

type vmAction int

const (
	vmActionCreate vmAction = iota
	vmActionConnect
	vmActionStart
)

const defaultVMIdleTimeout = 30 * time.Minute

func buildProjectVMEnv(envMap map[string]string) {
	envMap["OPENCODE_EXPERIMENTAL_WORKSPACES"] = "true"
}

// decideVMAction maps a GetSandbox result to the lifecycle action.
// notFound=true means the sandbox doesn't exist → create.
// Otherwise the status determines connect (running) vs start (stopped/crashed).
func decideVMAction(notFoundErr error, status msb.SandboxStatus) (vmAction, error) {
	if notFoundErr != nil {
		return vmActionCreate, nil
	}
	switch status {
	case msb.SandboxStatusRunning, msb.SandboxStatusDraining, msb.SandboxStatusPaused:
		return vmActionConnect, nil
	case msb.SandboxStatusStopped, msb.SandboxStatusCrashed:
		return vmActionStart, nil
	}
	return vmActionCreate, fmt.Errorf("unexpected sandbox status: %s", status)
}

// EnsureProjectVM returns a live *Sandbox for the project VM. The boolean
// return is true when the VM was created fresh (first boot); false when an
// existing VM was reused (connect or start). A per-project host-side flock
// guards the first-boot race between concurrent invocations.
func EnsureProjectVM(
	ctx context.Context,
	opts RunOptions,
	cfg Config,
	imageRef, homeVol, repoPath string,
	logger *output.Printer,
) (*msb.Sandbox, bool, error) {
	slug := git.ProjectSlug(logger)
	name := projectVMName(slug)

	flockPath := filepath.Join(cfg.StateDir, "vm-ensure", slug+".lock")
	if err := os.MkdirAll(filepath.Dir(flockPath), 0o750); err != nil {
		return nil, false, fmt.Errorf("create flock dir: %w", err)
	}

	spin := output.NewSpinner(logger)
	spin.Start("Checking project VM")

	handle, err := msb.GetSandbox(ctx, name)
	notFound := err != nil && msb.IsKind(err, msb.ErrSandboxNotFound)
	if err != nil && !notFound {
		spin.StopError(err)
		return nil, false, fmt.Errorf("check sandbox %q: %w", name, err)
	}

	// Fast path: VM is already running → connect without flock.
	if !notFound {
		action, err := decideVMAction(nil, handle.Status())
		if err != nil {
			spin.StopError(err)
			return nil, false, err
		}
		if action == vmActionConnect {
			sb, connErr := handle.Connect(ctx)
			if connErr != nil {
				// Idle-timeout race: the VM may have auto-stopped between
				// GetSandbox and Connect. Retry once via Start.
				logger.Debugf("connect failed (%v), retrying via Start", connErr)
				handle2, refreshErr := handle.Refresh(ctx)
				if refreshErr != nil {
					return nil, false, fmt.Errorf("connect sandbox %q (refresh after connect failure): %w", name, refreshErr)
				}
				if isSandboxActive(handle2.Status()) {
					sb, connErr2 := handle2.Connect(ctx)
					if connErr2 != nil {
						return nil, false, fmt.Errorf("connect sandbox %q: %w", name, connErr2)
					}
					spin.Stop()
					return sb, false, nil
				}
				sb, startErr := handle2.Start(ctx)
				if startErr != nil {
					return nil, false, fmt.Errorf("start sandbox %q: %w", name, startErr)
				}
				spin.Stop()
				return sb, false, nil
			}
			spin.Stop()
			logger.Debugf("connected to existing project VM: %s", name)
			return sb, false, nil
		}
		spin.Stop()
		// Stopped/crashed → start (no flock needed, Start is idempotent enough).
		sb, startErr := handle.Start(ctx)
		if startErr != nil {
			return nil, false, fmt.Errorf("start sandbox %q: %w", name, startErr)
		}
		logger.Debugf("started existing project VM: %s", name)
		return sb, false, nil
	}

	spin.Stop()

	// Slow path: VM doesn't exist → create. Hold a flock so concurrent
	// invocations don't both create (and clobber via WithReplace).
	release, lockErr := acquireProjectFlock(flockPath)
	if lockErr != nil {
		return nil, false, fmt.Errorf("acquire project flock: %w", lockErr)
	}
	defer release()

	// Re-check after acquiring the flock — another invocation may have created it.
	handle, err = msb.GetSandbox(ctx, name)
	if err == nil {
		// Someone else created it while we waited for the lock.
		action, err := decideVMAction(nil, handle.Status())
		if err != nil {
			return nil, false, err
		}
		if action == vmActionConnect {
			sb, _ := handle.Connect(ctx)
			if sb != nil {
				return sb, false, nil
			}
		}
		sb, startErr := handle.Start(ctx)
		if startErr != nil {
			return nil, false, fmt.Errorf("start sandbox %q: %w", name, startErr)
		}
		return sb, false, nil
	}
	if !msb.IsKind(err, msb.ErrSandboxNotFound) {
		return nil, false, fmt.Errorf("re-check sandbox %q: %w", name, err)
	}

	sb, created, err := createProjectVM(ctx, name, imageRef, homeVol, repoPath, opts, cfg, logger)
	if err != nil {
		return nil, false, err
	}
	return sb, created, nil
}

func createProjectVM(
	ctx context.Context,
	name, imageRef, homeVol, repoPath string,
	opts RunOptions,
	cfg Config,
	logger *output.Printer,
) (*msb.Sandbox, bool, error) {
	user := opts.User
	if user == "" {
		user = "dev"
	}
	cpus := opts.CPUs
	if cpus == 0 {
		cpus = sysinfo.NumCPUs()
	}
	maxMemoryGiB := sysinfo.TotalMemoryGiB()

	envMap := mergeEnvMaps(
		buildEnvMap(filepath.Join(cfg.UserLauncherDir, "env")),
		buildEnvMap(".opencode-msb/env"),
	)
	buildProjectVMEnv(envMap)

	secrets := BuildSecrets(mergeEnvMaps(
		buildEnvMap(filepath.Join(cfg.UserLauncherDir, "env.secret")),
		buildEnvMap(".opencode-msb/env.secret"),
	), logger)

	mounts := buildMounts(homeVol, repoPath, resolveTmpSizeMiB(opts.TmpSize))

	spin := output.NewSpinner(logger)
	spin.Start("Checking microsandbox runtime")
	if err := msb.EnsureInstalled(ctx); err != nil {
		spin.StopError(err)
		return nil, false, fmt.Errorf("microsandbox runtime: %w", err)
	}
	spin.Stop()

	spin = output.NewSpinner(logger)
	spin.Start("Starting project VM")
	sb, err := msb.CreateSandbox(ctx, name,
		msb.WithImage(imageRef),
		msb.WithMounts(mounts),
		msb.WithSecrets(secrets...),
		msb.WithEnv(envMap),
		msb.WithUser(user),
		msb.WithWorkdir("/workspace"),
		msb.WithCPUs(cpus),
		msb.WithMaxCPUs(sysinfo.NumCPUs()),
		msb.WithMemory(parseMemory(opts.Memory)),
		//nolint:gosec // G115: maxMemoryGiB is physical RAM in GiB, cannot overflow uint32
		msb.WithMaxMemory(uint32(maxMemoryGiB)*mibPerGib),
		msb.WithDetached(),
		msb.WithIdleTimeout(defaultVMIdleTimeout),
		msb.WithReplace(),
	)
	if err != nil {
		spin.StopError(err)
		return nil, false, fmt.Errorf("create sandbox: %w", err)
	}
	spin.Stop()
	logger.Debugf("created new project VM: %s", name)
	return sb, true, nil
}

// acquireProjectFlock takes an exclusive flock on the given path. It returns a
// release function. The flock prevents two concurrent invocations from both
// creating a project VM (which would clobber via WithReplace).
func acquireProjectFlock(path string) (func(), error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open flock %s: %w", path, err)
	}
	if err := flockExclusive(f); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("flock %s: %w", path, err)
	}
	return func() {
		_ = f.Close()
	}, nil
}
```

- [ ] **Step 4: Add flock helper (platform-specific)**

The `flockExclusive` function uses `syscall.Flock` on Linux and Darwin. Create a small helper file:

```go
// internal/sandbox/flock_linux.go
//go:build linux

package sandbox

import (
	"os"
	"syscall"
)

func flockExclusive(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_EX)
}
```

```go
// internal/sandbox/flock_darwin.go
//go:build darwin

package sandbox

import (
	"os"
	"syscall"
)

func flockExclusive(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_EX)
}
```

- [ ] **Step 5: Update query.go filter prefix**

In `internal/sandbox/query.go`, change `filterSandboxes` and `ListSandboxes` to use the `projectVMPrefix` instead of `"opencode-msb-sb-"`. The old `sandboxName` function in `runner.go` will be removed in Task 7, so update references now.

```go
// internal/sandbox/query.go — replace "opencode-msb-sb-" with projectVMPrefix
func filterSandboxes(handles []sandboxHandle) []string {
	var result []string
	for _, h := range handles {
		if strings.HasPrefix(h.name, projectVMPrefix) {
			result = append(result, h.name)
		}
	}
	return result
}
```

Apply the same change in `ListSandboxes`:
```go
if !strings.HasPrefix(name, projectVMPrefix) {
    continue
}
```

- [ ] **Step 6: Update query_test.go for new prefix**

In `internal/sandbox/query_test.go`, update `TestFilterSandboxesByPrefix` to use `opencode-msb-vm-` prefix:

```go
func TestFilterSandboxesByPrefix(t *testing.T) {
	handles := []sandboxHandle{
		{name: "opencode-msb-vm-proj-aBc1234D"},
		{name: "opencode-msb-vm-other-feat"},
		{name: "opencode-msb-task-prefill-proj-1719432000"},
		{name: "someone-elses-sandbox"},
		{name: "random"},
	}
	got := filterSandboxes(handles)
	if len(got) != 2 {
		t.Fatalf("expected 2 project VMs, got %d", len(got))
	}
	if got[0] != "opencode-msb-vm-proj-aBc1234D" {
		t.Errorf("expected first match, got %q", got[0])
	}
	if got[1] != "opencode-msb-vm-other-feat" {
		t.Errorf("expected second match, got %q", got[1])
	}
}
```

- [ ] **Step 7: Run all tests + format**

Run: `go test ./internal/sandbox/ -run "TestProjectVM|TestBuildProjectVMEnv|TestEnsureProjectVM|TestFilter|TestListSandboxes" -v && golangci-lint fmt`
Expected: PASS, no lint errors

- [ ] **Step 8: Commit**

```bash
git add internal/sandbox/projectvm.go internal/sandbox/projectvm_test.go internal/sandbox/flock_linux.go internal/sandbox/flock_darwin.go internal/sandbox/query.go internal/sandbox/query_test.go
git commit -m "feat: add project VM naming, lifecycle, env provisioning, and query filters"
```

---

## Task 2: Daemon supervisor (`EnsureDaemon`)

**Files:**
- Create: `internal/sandbox/daemon.go`
- Create: `internal/sandbox/daemon_test.go`

**Background — SDK signatures (from `exec.go` / `sandbox.go`):**
- `(*Sandbox).Shell(ctx, command string, opts...) (*ExecOutput, error)` — runs `/bin/sh -c command`, returns captured output.
- `(*ExecOutput).Success()` — true if exit code 0.
- `(*ExecOutput).Stdout()` — captured stdout as string.
- `(*ExecOutput).ExitCode()` — process exit code.

**Design:** The daemon supervisor checks if `opencode serve` is healthy by running `curl` **inside** the VM against `http://127.0.0.1:4096/global/health`. If unhealthy, it starts the daemon detached (via `nohup`) and polls until healthy or timeout. The healthcheck runs inside the VM — no host↔VM networking. If the daemon is unresponsive, kill the stale process (via `pkill`) before restarting.

**Interfaces:**
- Consumes: `*msb.Sandbox` (the project VM handle from Task 1).
- Produces: `EnsureDaemon(ctx, sb, logger) error` — guarantees the daemon is healthy when it returns nil.

- [ ] **Step 1: Write the failing test**

The healthcheck response parsing is pure and testable. Extract a `parseHealthResponse` function. The poll loop is tested via the `daemonShellFunc` test seam (matching the `ensureInstalled` pattern in `doctor.go`).

```go
// internal/sandbox/daemon_test.go
package sandbox

import (
	"context"
	"strings"
	"testing"
)

func TestParseHealthResponseHealthy(t *testing.T) {
	healthy, err := parseHealthResponse(`{"healthy":true,"version":"1.18.5"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !healthy {
		t.Error("expected healthy=true")
	}
}

func TestParseHealthResponseUnhealthy(t *testing.T) {
	healthy, err := parseHealthResponse(`{"healthy":false}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if healthy {
		t.Error("expected healthy=false")
	}
}

func TestParseHealthResponseInvalidJSON(t *testing.T) {
	_, err := parseHealthResponse("not json")
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

// daemonShellFunc is the test seam for sb.Shell, matching the ensureInstalled
// pattern in doctor.go. Tests override this; production code leaves the default
// (which calls the real sb.Shell).
var daemonShellFunc = func(ctx context.Context, sb *msb.Sandbox, command string) (string, int, error) {
	out, err := sb.Shell(ctx, command)
	if err != nil {
		return "", -1, err
	}
	return out.Stdout(), out.ExitCode(), nil
}

// mockDaemonShell overrides daemonShellFunc for testing. It returns queued
// (stdout, exitCode) pairs.
type mockDaemonShell struct {
	responses []mockShellResp
	calls     int
}

type mockShellResp struct {
	stdout   string
	exitCode int
	err      error
}

func (m *mockDaemonShell) run(_ context.Context, _ *msb.Sandbox, _ string) (string, int, error) {
	if m.calls >= len(m.responses) {
		return "", 0, nil
	}
	r := m.responses[m.calls]
	m.calls++
	return r.stdout, r.exitCode, r.err
}

func TestEnsureDaemonStartsWhenUnhealthy(t *testing.T) {
	logger := newTestLogger(t)
	mock := &mockDaemonShell{
		responses: []mockShellResp{
			// First healthcheck: unhealthy (daemon not running).
			{stdout: "", exitCode: 1},
			// Start command (response ignored).
			{stdout: "", exitCode: 0},
			// Poll: still starting.
			{stdout: "", exitCode: 1},
			// Poll again: healthy.
			{stdout: `{"healthy":true}`, exitCode: 0},
		},
	}
	prev := daemonShellFunc
	t.Cleanup(func() { daemonShellFunc = prev })
	daemonShellFunc = mock.run

	err := EnsureDaemon(context.Background(), nil, logger)
	if err != nil {
		t.Fatalf("EnsureDaemon failed: %v", err)
	}
}

func TestEnsureDaemonFailsAfterTimeout(t *testing.T) {
	logger := newTestLogger(t)
	mock := &mockDaemonShell{
		responses: []mockShellResp{
			// Always unhealthy.
			{stdout: "", exitCode: 1},
			{stdout: "", exitCode: 0},
			{stdout: "", exitCode: 1},
			{stdout: "", exitCode: 1},
			{stdout: "", exitCode: 1},
			{stdout: "", exitCode: 1},
			{stdout: "", exitCode: 1},
		},
	}
	prev := daemonShellFunc
	t.Cleanup(func() { daemonShellFunc = prev })
	daemonShellFunc = mock.run

	err := EnsureDaemon(context.Background(), nil, logger)
	if err == nil {
		t.Fatal("expected error after timeout")
	}
	if !strings.Contains(err.Error(), "did not become healthy") {
		t.Errorf("expected timeout error, got: %v", err)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/sandbox/ -run "TestParseHealth|TestEnsureDaemon" -v`
Expected: FAIL — `parseHealthResponse`, `EnsureDaemon`, `daemonShellFunc` undefined.

- [ ] **Step 3: Write minimal implementation**

```go
// internal/sandbox/daemon.go
package sandbox

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"gitlab.inoio.de/inoio/opencode-msb/internal/output"

	msb "github.com/superradcompany/microsandbox/sdk/go"
)

const (
	daemonHealthURL    = "http://127.0.0.1:4096/global/health"
	daemonStartCmd     = "nohup opencode serve --hostname 127.0.0.1 --port 4096 > /tmp/opencode-serve.log 2>&1 &"
	daemonKillCmd      = "pkill -f 'opencode serve' || true"
	daemonReadyTimeout = 60 * time.Second
	daemonPollInterval = 2 * time.Second
)

// daemonShellFunc is the test seam for sb.Shell, matching the ensureInstalled
// pattern in doctor.go. Tests override this; production code leaves the default.
var daemonShellFunc = func(ctx context.Context, sb *msb.Sandbox, command string) (string, int, error) {
	out, err := sb.Shell(ctx, command)
	if err != nil {
		return "", -1, err
	}
	return out.Stdout(), out.ExitCode(), nil
}

type healthResponse struct {
	Healthy bool   `json:"healthy"`
	Version string `json:"version"`
}

func parseHealthResponse(stdout string) (bool, error) {
	var resp healthResponse
	if err := json.Unmarshal([]byte(stdout), &resp); err != nil {
		return false, fmt.Errorf("parse health response: %w", err)
	}
	return resp.Healthy, nil
}

// EnsureDaemon guarantees the opencode serve daemon is healthy inside the VM.
// It healthchecks via curl inside the VM; if unhealthy, it kills any stale
// daemon process, starts a fresh one, and polls until healthy or timeout.
func EnsureDaemon(ctx context.Context, sb *msb.Sandbox, logger *output.Printer) error {
	if healthy := checkDaemonHealth(ctx, sb); healthy {
		logger.Debugf("opencode daemon already healthy")
		return nil
	}

	logger.Debugf("starting opencode serve daemon")
	if _, _, err := daemonShellFunc(ctx, sb, daemonKillCmd); err != nil {
		logger.Warnf("kill stale daemon failed (continuing): %v", err)
	}
	if _, _, err := daemonShellFunc(ctx, sb, daemonStartCmd); err != nil {
		return fmt.Errorf("start opencode serve: %w", err)
	}

	deadline := time.Now().Add(daemonReadyTimeout)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(daemonPollInterval):
		}
		if healthy := checkDaemonHealth(ctx, sb); healthy {
			logger.Debugf("opencode daemon is healthy")
			return nil
		}
	}
	return fmt.Errorf("opencode daemon did not become healthy within %s", daemonReadyTimeout)
}

func checkDaemonHealth(ctx context.Context, sb *msb.Sandbox) bool {
	stdout, exitCode, err := daemonShellFunc(ctx, sb, "curl -sf "+daemonHealthURL)
	if err != nil || exitCode != 0 {
		return false
	}
	healthy, err := parseHealthResponse(stdout)
	return err == nil && healthy
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/sandbox/ -run "TestParseHealth|TestEnsureDaemon" -v`
Expected: PASS

- [ ] **Step 5: Run all tests + format**

Run: `go test ./internal/sandbox/ -v && golangci-lint fmt`
Expected: PASS (existing tests still pass), no lint errors

- [ ] **Step 6: Commit**

```bash
git add internal/sandbox/daemon.go internal/sandbox/daemon_test.go
git commit -m "feat: add EnsureDaemon supervisor with healthcheck and retry"
```

---

## Task 3: Branch→worktree mapping (`ResolveTarget`)

**Files:**
- Create: `internal/sandbox/worktree.go`
- Create: `internal/sandbox/worktree_test.go`

**Background — spike findings (from `docs/superpowers/notes/2026-07-27-workspaces-spike-findings.md`):**
- Worktree creation is API-only: `POST /experimental/worktree` (no CLI subcommand).
- The API creates branches in the `opencode/<name>` namespace, not by checking out existing host branches.
- Worktree directories live under `/home/dev/.local/share/opencode/worktree/<project-id>/<name>`, not under `/workspace`.
- Attach a TUI via `opencode attach http://127.0.0.1:4096 --dir <worktree-path>`.
- The HTTP API is called via `curl` inside the VM (the daemon listens on `127.0.0.1:4096`).

**Design:** `ResolveTarget` takes the sandbox handle, a branch name (empty = no branch), and returns the `--dir` target path for `opencode attach`.
- No branch → return `/workspace`.
- `-b <branch>` → call `POST /experimental/worktree` via `curl` inside the VM. If the worktree already exists (API returns it), reuse it. If not, create it. Return the worktree directory path.

The worktree API request/response shape (from spike):
- `POST /experimental/worktree` with JSON body `{"name": "<branch>"}`.
- Response includes the worktree directory path.

**Interfaces:**
- Consumes: `*msb.Sandbox` (for `Shell`), branch string from `RunOptions`.
- Produces: `ResolveTarget(ctx, sb, branch, logger) (string, error)`.

- [ ] **Step 1: Write the failing test**

The no-branch case is pure logic. The branch case requires the HTTP API (integration). Test the no-branch path and the response parsing.

```go
// internal/sandbox/worktree_test.go
package sandbox

import (
	"strings"
	"testing"
)

func TestResolveTargetNoBranchReturnsWorkspace(t *testing.T) {
	got := resolveTargetNoBranch()
	if got != "/workspace" {
		t.Errorf("expected /workspace, got %q", got)
	}
}

func TestParseWorktreeResponse(t *testing.T) {
	resp := `{"directory": "/home/dev/.local/share/opencode/worktree/abc123/feat-x"}`
	got, err := parseWorktreeResponse(resp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "/home/dev/.local/share/opencode/worktree/abc123/feat-x"
	if got != want {
		t.Errorf("expected %q, got %q", want, got)
	}
}

func TestParseWorktreeResponseInvalidJSON(t *testing.T) {
	_, err := parseWorktreeResponse("not json")
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestBuildWorktreeCreateBody(t *testing.T) {
	got := buildWorktreeCreateBody("feat-x")
	if !strings.Contains(got, `"name"`) {
		t.Errorf("expected body to contain 'name', got %q", got)
	}
	if !strings.Contains(got, "feat-x") {
		t.Errorf("expected body to contain branch name, got %q", got)
	}
}

func TestWorktreeCurlCommand(t *testing.T) {
	cmd := buildWorktreeCreateCmd("feat-x")
	if !strings.Contains(cmd, "POST") {
		t.Errorf("expected POST in command, got %q", cmd)
	}
	if !strings.Contains(cmd, "/experimental/worktree") {
		t.Errorf("expected API path in command, got %q", cmd)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/sandbox/ -run "TestResolveTargetNoBranch|TestParseWorktree|TestBuildWorktree" -v`
Expected: FAIL — functions undefined.

- [ ] **Step 3: Write minimal implementation**

```go
// internal/sandbox/worktree.go
package sandbox

import (
	"context"
	"encoding/json"
	"fmt"

	"gitlab.inoio.de/inoio/opencode-msb/internal/output"

	msb "github.com/superradcompany/microsandbox/sdk/go"
)

const defaultTargetDir = "/workspace"

type worktreeResponse struct {
	Directory string `json:"directory"`
}

func resolveTargetNoBranch() string {
	return defaultTargetDir
}

func parseWorktreeResponse(stdout string) (string, error) {
	var resp worktreeResponse
	if err := json.Unmarshal([]byte(stdout), &resp); err != nil {
		return "", fmt.Errorf("parse worktree response: %w", err)
	}
	if resp.Directory == "" {
		return "", fmt.Errorf("worktree response missing directory field: %s", stdout)
	}
	return resp.Directory, nil
}

func buildWorktreeCreateBody(name string) string {
	return fmt.Sprintf(`{"name":%q}`, name)
}

func buildWorktreeCreateCmd(name string) string {
	return fmt.Sprintf(
		`curl -sf -X POST http://127.0.0.1:4096/experimental/worktree -H 'Content-Type: application/json' -d '%s'`,
		buildWorktreeCreateBody(name),
	)
}

func buildWorktreeListCmd() string {
	return "curl -sf http://127.0.0.1:4096/experimental/worktree"
}

// ResolveTarget returns the --dir target for opencode attach. No branch →
// /workspace. With a branch → create or reuse an opencode worktree via the
// daemon's HTTP API and return its directory path.
func ResolveTarget(
	ctx context.Context,
	sb *msb.Sandbox,
	branch string,
	logger *output.Printer,
) (string, error) {
	if branch == "" {
		return resolveTargetNoBranch(), nil
	}

	// Try to create the worktree. The API is idempotent enough: if it already
	// exists, the response returns the existing directory.
	logger.Debugf("creating/reusing worktree for branch %q", branch)
	out, err := sb.Shell(ctx, buildWorktreeCreateCmd(branch))
	if err != nil {
		return "", fmt.Errorf("create worktree %q: %w", branch, err)
	}
	if !out.Success() {
		return "", fmt.Errorf("create worktree %q failed (exit %d): %s", branch, out.ExitCode(), out.Stderr())
	}

	dir, err := parseWorktreeResponse(out.Stdout())
	if err != nil {
		return "", fmt.Errorf("parse worktree response for %q: %w", branch, err)
	}
	logger.Debugf("worktree for %q: %s", branch, dir)
	return dir, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/sandbox/ -run "TestResolveTargetNoBranch|TestParseWorktree|TestBuildWorktree|TestWorktreeCurl" -v`
Expected: PASS

- [ ] **Step 5: Run all tests + format**

Run: `go test ./internal/sandbox/ -v && golangci-lint fmt`
Expected: PASS, no lint errors

- [ ] **Step 6: Commit**

```bash
git add internal/sandbox/worktree.go internal/sandbox/worktree_test.go
git commit -m "feat: add ResolveTarget worktree mapping via opencode HTTP API"
```

---

## Task 4: Rewrite Run/Shell invocation flow + host worktree prune

**Files:**
- Modify: `internal/sandbox/runner.go` (`Run`, `Shell`, `prepareSandbox`, `sandboxSession`)
- Modify: `internal/sandbox/runner_test.go` (update/remove tests for removed functions)
- Modify: `internal/git/git.go` (add `PruneWorktrees`)
- Modify: `internal/git/git_test.go` (add test for `PruneWorktrees`)

**Background — spike findings:** opencode's linked worktrees created from the bind-mounted `/workspace` write metadata into the host repo's `.git/worktrees/<name>/` directory. These reference VM-internal paths that don't exist on the host, so `git worktree list` on the host shows them as `prunable`. The launcher must run `git worktree prune` on the host repo after session/VM cleanup.

**Design:** The new invocation flow:
1. `prepareSandbox`: `CheckAll` → `ProjectSlug` → `EnsureImage` → `EnsureHome` → `loadConfigFiles` → `EnsureProjectVM` (returns `sb, created`) → if `created`: `provisionSandbox` + `startDockerdIfPresent` → `EnsureDaemon` → return session.
2. `Run`: `prepareSandbox` → `ResolveTarget(branch)` → `Attach(bash -lc 'opencode attach http://127.0.0.1:4096 --dir <target>')` → on exit: `Detach`/`Close` (VM persists) + `git worktree prune` on host.
3. `Shell`: same but attaches `bash -l` directly (debug).
4. `--dry-run`: ensure VM + daemon, skip attach.

The `sandboxSession` struct changes: `repoPath`/`cwdBranch`/`created`/`cloneVol` fields are replaced by `target` (the `--dir` path), `sb`, `name`. Cleanup no longer stops the VM — only `Detach`/`Close` + `PruneWorktrees`.

**Interfaces:**
- Consumes: `EnsureProjectVM` (Task 1), `EnsureDaemon` (Task 2), `ResolveTarget` (Task 3).
- Produces: rewritten `Run`, `Shell`, `prepareSandbox`, `sandboxSession`; `git.PruneWorktrees`.

- [ ] **Step 1: Write the failing test**

Test the new `sandboxSession` struct shape and the attach command builder.

```go
// internal/sandbox/runner_test.go — replace the old session/resolveWorkspace tests

func TestBuildAttachCommand(t *testing.T) {
	got := buildAttachCommand("/workspace", true, []string{"foo"})
	// Should be: opencode attach http://127.0.0.1:4096 --dir /workspace --auto foo
	if !strings.Contains(got, "opencode attach") {
		t.Errorf("expected 'opencode attach' in command, got %q", got)
	}
	if !strings.Contains(got, "http://127.0.0.1:4096") {
		t.Errorf("expected daemon URL in command, got %q", got)
	}
	if !strings.Contains(got, "--dir /workspace") {
		t.Errorf("expected --dir /workspace in command, got %q", got)
	}
	if !strings.Contains(got, "--auto") {
		t.Errorf("expected --auto flag, got %q", got)
	}
}

func TestBuildAttachCommandNoAuto(t *testing.T) {
	got := buildAttachCommand("/workspace", false, nil)
	if strings.Contains(got, "--auto") {
		t.Errorf("did not expect --auto flag, got %q", got)
	}
}

func TestBuildAttachCommandWorktreeTarget(t *testing.T) {
	got := buildAttachCommand("/home/dev/.local/share/opencode/worktree/abc/feat", true, nil)
	if !strings.Contains(got, "--dir /home/dev/.local/share/opencode/worktree/abc/feat") {
		t.Errorf("expected worktree dir in command, got %q", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/sandbox/ -run TestBuildAttachCommand -v`
Expected: FAIL — `buildAttachCommand` undefined.

- [ ] **Step 3: Add PruneWorktrees to git.go**

The `sandboxSession.cleanup()` (written in Step 5 below) calls `git.PruneWorktrees`. Add it first so the code compiles.

```go
// internal/git/git.go — append

func PruneWorktrees(cwd string) error {
	cmd := exec.Command("git", "worktree", "prune")
	cmd.Dir = cwd
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git worktree prune failed in %s: %w: %s", cwd, err, out)
	}
	return nil
}
```

- [ ] **Step 4: Add PruneWorktrees test**

```go
// internal/git/git_test.go — append

func TestPruneWorktreesCleansStaleEntries(t *testing.T) {
	repo := initRepo(t)
	wtDir := filepath.Join(t.TempDir(), "stale-wt")
	runGit(t, repo, "worktree", "add", "--detach", wtDir)
	if err := os.RemoveAll(wtDir); err != nil {
		t.Fatalf("remove worktree dir: %v", err)
	}
	out := runGit(t, repo, "worktree", "list")
	if !strings.Contains(out, "prunable") {
		t.Fatalf("expected prunable entry, got: %s", out)
	}
	if err := PruneWorktrees(repo); err != nil {
		t.Fatalf("PruneWorktrees: %v", err)
	}
	out = runGit(t, repo, "worktree", "list")
	if strings.Contains(out, "prunable") {
		t.Errorf("expected no prunable entries after prune, got: %s", out)
	}
}

func TestPruneWorktreesNoRepo(t *testing.T) {
	dir := t.TempDir()
	err := PruneWorktrees(dir)
	if err == nil {
		t.Error("expected error when not in a git repo")
	}
}
```

Run: `go test ./internal/git/ -run TestPruneWorktrees -v`
Expected: PASS

- [ ] **Step 5: Write minimal implementation — invocation flow**

Rewrite the invocation flow in `runner.go`. Replace the old `prepareSandbox`, `Run`, `Shell`, `sandboxSession`, and remove `resolveWorkspace`/`cleanupManagedRepo`/`handleUncommittedChanges`/`handleRepoCleanup`/`ensureNoSameBranchSession`/`ensureNoSameHomeSession`/`sameBranchSessionExists`/`promptExistingSession`/`promptBranchCreation` (these are removed in Task 7, but we remove their call sites now).

```go
// internal/sandbox/runner.go — replace Run, Shell, prepareSandbox, sandboxSession

func buildAttachCommand(target string, auto bool, args []string) string {
	parts := []string{"opencode", "attach", "http://127.0.0.1:4096", "--dir", target}
	if auto {
		parts = append(parts, autoFlag)
	}
	parts = append(parts, args...)
	return strings.Join(parts, " ")
}

type sandboxSession struct {
	sb     *msb.Sandbox
	name   string
	target string
	cwd    string
}

func (s *sandboxSession) cleanup() {
	if s.sb != nil {
		_ = s.sb.Detach(context.Background())
	}
	// Run git worktree prune on the host repo to clean up stale entries.
	if s.cwd != "" {
		_ = git.PruneWorktrees(s.cwd)
	}
}

func prepareSandbox(
	ctx context.Context,
	opts RunOptions,
	cfg Config,
	logger *output.Printer,
) (*sandboxSession, error) {
	if !CheckAll(ctx, logger) {
		return nil, errors.New("preflight failed")
	}

	projectSlug := git.ProjectSlug(logger)

	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("get current directory: %w", err)
	}

	dockerfile := resolveDockerfile()
	dockerCli, err := client.New(client.FromEnv)
	if err != nil {
		return nil, fmt.Errorf("cannot connect to Docker daemon (is dockerd running?): %w", err)
	}
	defer dockerCli.Close()

	imageRef, imageDigest, err := EnsureImage(ctx, dockerCli, dockerfile, projectSlug, opts.Rebuild, logger)
	if err != nil {
		return nil, fmt.Errorf("image setup failed: %w", err)
	}
	logger.Debugf("image: %s (digest=%s)", imageRef, imageDigest)

	vm := NewVolumeManager(logger)
	homeVol, err := vm.EnsureHome(ctx, projectSlug, imageDigest, imageRef)
	if err != nil {
		return nil, fmt.Errorf("volume setup failed: %w", err)
	}
	logger.Debugf("home volume: %s", homeVol)

	sb, created, err := EnsureProjectVM(ctx, opts, cfg, imageRef, homeVol, cwd, logger)
	if err != nil {
		return nil, err
	}
	name := projectVMName(projectSlug)

	if created {
		configFiles, err := loadConfigFiles(cfg.UserConfigDir)
		if err != nil {
			return nil, err
		}
		fs := sb.FS()
		if err = provisionSandbox(ctx, fs, configFiles, cwd, logger); err != nil {
			return nil, err
		}
		if err := startDockerdIfPresent(ctx, sb, logger); err != nil {
			return nil, fmt.Errorf("docker startup: %w", err)
		}
	}

	if err := EnsureDaemon(ctx, sb, logger); err != nil {
		return nil, err
	}

	target, err := ResolveTarget(ctx, sb, opts.Branch, logger)
	if err != nil {
		return nil, err
	}
	logger.Debugf("attach target: %s", target)

	return &sandboxSession{
		sb:     sb,
		name:   name,
		target: target,
		cwd:    cwd,
	}, nil
}

func Run(ctx context.Context, opts RunOptions, cfg Config, logger *output.Printer) error {
	session, err := prepareSandbox(ctx, opts, cfg, logger)
	if err != nil {
		return err
	}
	defer session.cleanup()

	var exitCode int
	var attachErr error
	if opts.DryRun {
		logger.Infof("dry run: VM and daemon validated, skipping opencode execution")
	} else {
		setup := buildAttachCommand(session.target, opts.Auto, opts.Args)
		// Run as a login shell so /etc/profile and ~/.profile are sourced,
		// putting tools installed under /usr/local/go/bin, ~/go/bin and
		// ~/.microsandbox/bin on PATH for opencode and its child shells.
		exitCode, attachErr = session.sb.Attach(ctx, "/bin/bash", "-l", "-c", setup)
	}

	return finalizeRun(attachErr, nil, exitCode)
}

func Shell(ctx context.Context, opts RunOptions, cfg Config, logger *output.Printer) error {
	session, err := prepareSandbox(ctx, opts, cfg, logger)
	if err != nil {
		return err
	}
	defer session.cleanup()

	// Login shell so the interactive shell inherits PATH from /etc/profile and ~/.profile.
	exitCode, attachErr := session.sb.Attach(ctx, "/bin/bash", "-l")
	return finalizeRun(attachErr, nil, exitCode)
}
```

- [ ] **Step 6: Update runner_test.go — remove tests for removed functions**

Remove or replace tests that test `resolveWorkspace`, `cleanupManagedRepo`, `handleUncommittedChanges`, `handleRepoCleanup`, `sandboxSession.cloneVol`, `promptExistingSession`, `ensureNoSameBranchSession`, `sameBranchSessionExists`, `promptBranchCreation`. These functions are being removed (Task 7). Keep the tests for `parseMemory`, `resolveTmpSizeMiB`, `buildMounts`, `buildEnvMap`, `mergeEnvMaps`, `buildOpencodeArgs`, `isSandboxActive` (still used by doctor).

The tests to remove from `runner_test.go`:
- `TestResolveWorkspaceNoBranch`
- `TestResolveWorkspaceBranchMatchesCurrentBranch`
- `TestResolveWorkspaceBranchCreatesManagedRepo`
- `TestResolveWorkspaceBranchOutsideRepo`
- `TestCleanupManagedRepoRemovesCleanRepo`
- `TestCleanupManagedRepoKeepsUncommittedChanges`
- `TestCleanupManagedRepoDiscardsAndRemoves`
- `TestCleanupManagedRepoMergeSuccess`
- `TestCleanupManagedRepoMergeConflict`
- `TestPromptExistingSessionTerminate`
- `TestPromptExistingSessionExitDefault`
- `TestPromptExistingSessionNonInteractiveExits`
- `TestSandboxSessionHasCloneVolumeField`
- `TestSandboxNameFormat`
- `TestSandboxNameTruncation`

The `TestSandboxName*` tests reference `sandboxName` which is removed; `projectVMName` tests (Task 1) replace them.

- [ ] **Step 7: Run tests to verify they pass**

Run: `go test ./internal/sandbox/ -run "TestBuildAttachCommand|TestParseMemory|TestBuildMounts|TestBuildEnvMap|TestMergeEnvMaps|TestBuildOpencodeArgs|TestIsSandboxActive" -v`
Expected: PASS

- [ ] **Step 8: Run all tests + format**

Run: `go test ./... 2>&1 | head -30` (expect compile errors from removed functions still referenced — these are cleaned up in Task 7. For now, just verify the new tests pass in isolation.)
Run: `go test ./internal/sandbox/ -run "TestBuildAttachCommand" -v && golangci-lint fmt`
Expected: New tests PASS.

- [ ] **Step 9: Commit**

```bash
git add internal/sandbox/runner.go internal/sandbox/runner_test.go internal/git/git.go internal/git/git_test.go
git commit -m "feat: rewrite Run/Shell flow for one-VM-per-project, add PruneWorktrees"
```

---

## Task 5: Update doctor orphan detection + query filters

**Files:**
- Modify: `internal/sandbox/doctor.go` (`isOrphanedSandbox`, `isOrphanedVolume`)
- Modify: `internal/sandbox/doctor_test.go` (`TestIsOrphanedSandbox*`, `TestIsOrphanedVolume*`)

**Design:** The orphan detection needs updating for the new naming:
- `isOrphanedSandbox`: the old `sb-` prefix is now an orphan (replaced by `vm-`). The `task-` prefix (prefill/clone task sandboxes) is still recognized. Clone task sandboxes (`task-clone`) are now obsolete but `task-prefill` still exists (for `EnsureHome`).
- `isOrphanedVolume`: `clone-` volumes are now orphans (clone-on-use is removed). `home-` volumes are still valid.

**Interfaces:**
- Consumes: `projectVMPrefix` (Task 1).
- Produces: updated orphan detection.

- [ ] **Step 1: Write the failing test**

Update the doctor tests to reflect the new orphan rules:

```go
// internal/sandbox/doctor_test.go — replace TestIsOrphanedSandboxOldPrefix

func TestIsOrphanedSandboxVM(t *testing.T) {
	if isOrphanedSandbox("opencode-msb-vm-proj-main") {
		t.Error("expected vm- sandbox to NOT be orphaned")
	}
}

func TestIsOrphanedSandboxOldSBPrefix(t *testing.T) {
	if !isOrphanedSandbox("opencode-msb-sb-proj-main") {
		t.Error("expected old sb- sandbox to be orphaned")
	}
}

func TestIsOrphanedSandboxTaskPrefix(t *testing.T) {
	if isOrphanedSandbox("opencode-msb-task-prefill-proj-123") {
		t.Error("expected task sandbox to NOT be orphaned")
	}
}

func TestIsOrphanedSandboxForeign(t *testing.T) {
	if isOrphanedSandbox("someone-elses-sandbox") {
		t.Error("expected foreign sandbox to NOT be orphaned")
	}
}
```

```go
// internal/sandbox/doctor_test.go — replace TestIsOrphanedVolumeOldPattern

func TestIsOrphanedVolumeHome(t *testing.T) {
	if isOrphanedVolume("opencode-msb-home-proj-aBc1234D") {
		t.Error("expected home volume to NOT be orphaned")
	}
}

func TestIsOrphanedVolumeClone(t *testing.T) {
	if !isOrphanedVolume("opencode-msb-clone-proj-aBc1234D-123") {
		t.Error("expected clone volume to be orphaned (clone-on-use removed)")
	}
}

func TestIsOrphanedVolumeOldPattern(t *testing.T) {
	if !isOrphanedVolume("proj-opencode-home-sha256-abc") {
		t.Error("expected old-pattern volume to be orphaned")
	}
}

func TestIsOrphanedVolumeForeign(t *testing.T) {
	if isOrphanedVolume("random-volume") {
		t.Error("expected foreign volume to NOT be orphaned")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/sandbox/ -run "TestIsOrphaned" -v`
Expected: FAIL — the current `isOrphanedSandbox` doesn't treat `sb-` as orphaned, and `isOrphanedVolume` doesn't treat `clone-` as orphaned.

- [ ] **Step 3: Write minimal implementation**

```go
// internal/sandbox/doctor.go — replace isOrphanedSandbox

func isOrphanedSandbox(name string) bool {
	if !strings.HasPrefix(name, "opencode-msb-") {
		return false
	}
	// vm- sandboxes are the current model; task- sandboxes are operational (prefill).
	return !strings.HasPrefix(name, projectVMPrefix) &&
		!strings.HasPrefix(name, "opencode-msb-task-")
}
```

```go
// internal/sandbox/doctor.go — replace isOrphanedVolume

func isOrphanedVolume(name string) bool {
	if strings.HasPrefix(name, "opencode-msb-home-") {
		return false
	}
	// clone- volumes are obsolete (clone-on-use removed) → orphaned.
	if strings.HasPrefix(name, "opencode-msb-clone-") {
		return true
	}
	return strings.Contains(name, "-opencode-home-")
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/sandbox/ -run "TestIsOrphaned" -v`
Expected: PASS

- [ ] **Step 5: Run all tests + format**

Run: `go test ./internal/sandbox/ -v && golangci-lint fmt`
Expected: PASS, no lint errors

- [ ] **Step 6: Commit**

```bash
git add internal/sandbox/doctor.go internal/sandbox/doctor_test.go
git commit -m "feat: update orphan detection for vm- prefix, obsolete clone volumes"
```

---

## Task 6: CLI `stop` / `kill` commands + `-b` help update

**Files:**
- Modify: `cmd/opencode-msb/cli.go` (add `stop`/`kill` commands, update `-b` flag help)
- Modify: `cmd/opencode-msb/cli_test.go` (add tests for new commands, update tree tests)

**Background — SDK signatures:**
- `msb.GetSandbox(ctx, name) (*SandboxHandle, error)` — lookup by name.
- `(*SandboxHandle).Stop(ctx, opts...)` — graceful stop, waits for stopped state.
- `(*SandboxHandle).Kill(ctx, opts...)` — force kill, waits for stopped state.
- `(*SandboxHandle).Remove(ctx)` — removes persisted state (must be stopped first).

**Design:** Add `stop` and `kill` subcommands. Both look up the project VM by name (using `git.ProjectSlug` from the current directory), stop/kill it, and optionally remove its persisted state. `stop` is graceful (SIGTERM); `kill` is forceful (SIGKILL). The `-b` flag help text is updated to reflect that it now maps to an opencode worktree, not a host branch checkout.

**Interfaces:**
- Consumes: `projectVMName`, `git.ProjectSlug`.
- Produces: `stop` and `kill` CLI commands.

- [ ] **Step 1: Write the failing test**

```go
// cmd/opencode-msb/cli_test.go — append

func TestStopCommandExists(t *testing.T) {
	root := buildRootCmd()
	stopCmd, _, _ := root.Find([]string{"stop"})
	if stopCmd == nil {
		t.Fatal("expected stop command")
	}
}

func TestKillCommandExists(t *testing.T) {
	root := buildRootCmd()
	killCmd, _, _ := root.Find([]string{"kill"})
	if killCmd == nil {
		t.Fatal("expected kill command")
	}
}

func TestStopCommandHasForceFlag(t *testing.T) {
	root := buildRootCmd()
	stopCmd, _, _ := root.Find([]string{"stop"})
	if stopCmd == nil {
		t.Fatal("expected stop command")
	}
	if stopCmd.Flags().Lookup("force") == nil {
		t.Error("expected --force flag on stop command")
	}
}
```

Also update `TestPrintTreeContainsAllCommands` to include `"stop"` and `"kill"` in the expected list:

```go
// cmd/opencode-msb/cli_test.go — modify TestPrintTreeContainsAllCommands

expected := []string{"run", "doctor", "build", "list", "shell", "config", "image", "volume", "stop", "kill"}
```

And update `TestPrintTreeContainsCommandDescriptions` to add the new descriptions:

```go
descs := []string{
	// ... existing ...
	"Stop the project VM",
	"Force-kill the project VM",
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./cmd/opencode-msb/ -run "TestStopCommand|TestKillCommand" -v`
Expected: FAIL — commands don't exist yet.

- [ ] **Step 3: Write minimal implementation**

Add new commands to `cli.go`:

```go
// cmd/opencode-msb/cli.go — add constants

const (
	cmdStop = "stop"
	cmdKill = "kill"
	// ... existing ...
)
```

```go
// cmd/opencode-msb/cli.go — add to buildRootCmd()

	root.AddCommand(buildStopCmd())
	root.AddCommand(buildKillCmd())
```

```go
// cmd/opencode-msb/cli.go — add command builders

func buildStopCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   cmdStop,
		Short: "Stop the project VM",
		Annotations: map[string]string{
			annotationAlsoAs: "sandbox stop",
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			force, _ := cmd.Flags().GetBool("force")
			logger := newLogger(cmd)
			return sandbox.StopProjectVM(cmd.Context(), force, logger)
		},
	}
	cmd.Flags().BoolP("force", "f", false, "Remove the VM's persisted state after stopping")
	return cmd
}

func buildKillCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   cmdKill,
		Short: "Force-kill the project VM",
		Annotations: map[string]string{
			annotationAlsoAs: "sandbox kill",
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			force, _ := cmd.Flags().GetBool("force")
			logger := newLogger(cmd)
			return sandbox.KillProjectVM(cmd.Context(), force, logger)
		},
	}
	cmd.Flags().BoolP("force", "f", false, "Remove the VM's persisted state after killing")
	return cmd
}
```

Add `stop`/`kill` to the sandbox subcommand group in `buildSandboxCmd`:

```go
func buildSandboxCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sandbox",
		Short: "Manage sandboxes",
	}
	cmd.AddCommand(buildListCmd())
	cmd.AddCommand(buildShellCmd())
	cmd.AddCommand(buildRunCmd())
	cmd.AddCommand(buildStopCmd())
	cmd.AddCommand(buildKillCmd())
	return cmd
}
```

Update the `-b` flag help text on `run` and `shell` commands:

```go
// cmd/opencode-msb/cli.go — in buildRunCmd and buildShellCmd
cmd.Flags().StringP("branch", "b", "", "Run in an opencode worktree for the given branch name")
```

Add `StopProjectVM` and `KillProjectVM` to the sandbox package:

```go
// internal/sandbox/projectvm.go — append

func StopProjectVM(ctx context.Context, remove bool, logger *output.Printer) error {
	slug := git.ProjectSlug(logger)
	name := projectVMName(slug)

	handle, err := msb.GetSandbox(ctx, name)
	if err != nil {
		if msb.IsKind(err, msb.ErrSandboxNotFound) {
			logger.Infof("no project VM found: %s", name)
			return nil
		}
		return fmt.Errorf("get sandbox %q: %w", name, err)
	}

	spin := output.NewSpinner(logger)
	spin.Start("Stopping project VM")
	if err := handle.Stop(ctx); err != nil {
		spin.StopError(err)
		return fmt.Errorf("stop sandbox %q: %w", name, err)
	}
	spin.Stop()
	logger.Infof("stopped project VM: %s", name)

	if remove {
		if err := handle.Remove(ctx); err != nil {
			logger.Warnf("failed to remove sandbox state: %v", err)
		}
	}
	return nil
}

func KillProjectVM(ctx context.Context, remove bool, logger *output.Printer) error {
	slug := git.ProjectSlug(logger)
	name := projectVMName(slug)

	handle, err := msb.GetSandbox(ctx, name)
	if err != nil {
		if msb.IsKind(err, msb.ErrSandboxNotFound) {
			logger.Infof("no project VM found: %s", name)
			return nil
		}
		return fmt.Errorf("get sandbox %q: %w", name, err)
	}

	spin := output.NewSpinner(logger)
	spin.Start("Force-killing project VM")
	if err := handle.Kill(ctx); err != nil {
		spin.StopError(err)
		return fmt.Errorf("kill sandbox %q: %w", name, err)
	}
	spin.Stop()
	logger.Infof("killed project VM: %s", name)

	if remove {
		if err := handle.Remove(ctx); err != nil {
			logger.Warnf("failed to remove sandbox state: %v", err)
		}
	}
	return nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./cmd/opencode-msb/ -run "TestStopCommand|TestKillCommand" -v && go test ./cmd/opencode-msb/ -run "TestPrintTreeContainsAllCommands|TestPrintTreeContainsCommandDescriptions" -v`
Expected: PASS

- [ ] **Step 5: Run all tests + format**

Run: `go test ./... && golangci-lint fmt`
Expected: PASS (may still have compile errors from Task 7's pending removals — see note in Task 4 Step 8).

- [ ] **Step 6: Commit**

```bash
git add cmd/opencode-msb/cli.go cmd/opencode-msb/cli_test.go internal/sandbox/projectvm.go
git commit -m "feat: add stop/kill CLI commands, update -b help text"
```

---

## Task 7: Remove obsolete code (clone-on-use + managed-clone/merge machinery)

**Files:**
- Modify: `internal/sandbox/volumes.go` (remove `CloneVolume`, `cloneVolumeName`, `sameHomeVolumeInUse`, `extractNamedVolumes`)
- Modify: `internal/sandbox/volumes_test.go` (remove tests for removed functions)
- Modify: `internal/sandbox/runner.go` (remove `sandboxName`, `sameBranchSessionExists`, `ensureNoSameBranchSession`, `ensureNoSameHomeSession`, `resolveWorkspace`, `cleanupManagedRepo`, `handleUncommittedChanges`, `handleRepoCleanup`, `promptBranchCreation`, `promptExistingSession`, `createSandbox`)
- Modify: `internal/git/git.go` (remove `WorktreePath`, `FindManagedRepo`, `EnsureManagedRepoFromRef`, `EnsureManagedRepo`, `RemoveManagedRepo`, `MergeBranchInto`, `AbortMerge`, `HasUncommittedChanges`, `CommitAll`, `DiscardAll`, `IsRepoForBranch`, `isGitRepo`, `checkoutNewBranch`, `checkoutBranch`)
- Modify: `internal/git/git_test.go` (remove tests for removed functions)
- Modify: `internal/sandbox/integration_test.go` (remove tests for removed functions)

**Design:** After Tasks 1–6 are in place, the old clone-on-use and managed-clone/merge machinery is dead code. Remove it wholesale. The `createSandbox` function is replaced by `createProjectVM` (Task 1). The `sandboxName` function is replaced by `projectVMName` (Task 1). The `resolveWorkspace`/`cleanupManagedRepo` flow is replaced by `ResolveTarget` (Task 3). The `ensureNoSameHomeSession`/`sameHomeVolumeInUse`/`CloneVolume` clone-on-use flow is entirely removed.

**Interfaces:**
- Consumes: all prior tasks (these functions are no longer referenced).
- Produces: clean codebase with no dead code.

- [ ] **Step 1: Remove obsolete functions from volumes.go**

Remove from `internal/sandbox/volumes.go`:
- `cloneVolumeName`
- `(*VolumeManager).CloneVolume`
- `sameHomeVolumeInUse`
- `extractNamedVolumes`
- `rawMountSpec`, `rawSandboxConfig` types (only used by `extractNamedVolumes`)

Keep: `HomeVolumeName`, `VolumeManager`, `NewVolumeManager`, `EnsureHome`, `prefillVolume`.

- [ ] **Step 2: Remove obsolete tests from volumes_test.go**

Remove from `internal/sandbox/volumes_test.go`:
- `TestExtractNamedVolumes`
- `TestExtractNamedVolumesEmpty`
- `TestExtractNamedVolumesInvalidJSON`
- `TestCloneVolumeName`

Keep: `TestHomeVolumeName`, `TestHomeVolumeNameDifferentInputs`, `TestNewVolumeManager`.

- [ ] **Step 3: Remove obsolete functions from runner.go**

Remove from `internal/sandbox/runner.go`:
- `sandboxName` (replaced by `projectVMName`)
- `sameBranchSessionExists`
- `ensureNoSameBranchSession`
- `promptExistingSession`
- `ensureNoSameHomeSession`
- `resolveWorkspace`
- `cleanupManagedRepo`
- `handleUncommittedChanges`
- `handleRepoCleanup`
- `promptBranchCreation`
- `createSandbox` (replaced by `createProjectVM` in Task 1)

Keep: `parseMemory`, `resolveTmpSizeMiB`, `buildEnvMap`, `mergeEnvMaps`, `buildOpencodeArgs`, `resolveDockerfile`, `envrcFiles`, `buildMounts`, `provisionSandbox`, `loadConfigFiles`, `finalizeRun`, `BuildImage`, `ExitError`, `RunOptions`, `Config`, `sandboxFS`, `buildAttachCommand`, `sandboxSession`, `prepareSandbox`, `Run`, `Shell`, constants.

Note: `isSandboxActive` is still used by `doctor.go` (`CheckOrphans` doesn't use it, but the integration tests do). Check references before removing — keep it if `doctor.go` or tests reference it. Looking at `doctor.go`, it does NOT use `isSandboxActive`. The only references are in `runner.go` (removed functions) and `integration_test.go`. Remove `isSandboxActive` too, and update integration tests.

- [ ] **Step 4: Remove obsolete functions from git.go**

Remove from `internal/git/git.go`:
- `WorktreePath`
- `FindManagedRepo`
- `EnsureManagedRepoFromRef`
- `EnsureManagedRepo`
- `RemoveManagedRepo`
- `MergeBranchInto`
- `AbortMerge`
- `HasUncommittedChanges`
- `CommitAll`
- `DiscardAll`
- `IsRepoForBranch`
- `isGitRepo`
- `checkoutNewBranch`
- `checkoutBranch`
- `ErrNothingToCommit`

Keep: `HashID`, `ProjectSlug`, `BranchSlug`, `BranchAt`, `BranchName`, `gitCommonDir`, `sanitizeFolderName`, `PruneWorktrees` (Task 4), and the constants.

- [ ] **Step 5: Remove obsolete tests from git_test.go**

Remove from `internal/git/git_test.go`:
- `TestWorktreePathConstruction`
- `TestWorktreePathWithBranchSlug`
- `TestIsRepoForBranchRootCheckout`
- `TestIsRepoForBranchClonedRepo`
- `TestIsRepoForBranchOutsideRepo`
- `TestFindManagedRepoMissing`
- `TestFindManagedRepoValid`
- `TestFindManagedRepoInvalidRemoved`
- `TestFindManagedRepoWrongBranchRemoved`
- `TestEnsureManagedRepoCreatesFromExistingBranch`
- `TestEnsureManagedRepoCreatesNewBranchFromHEAD`
- `TestEnsureManagedRepoReusesExisting`
- `TestEnsureManagedRepoRecreatesOnWrongBranch`
- `TestHasUncommittedChangesClean`
- `TestHasUncommittedChangesUnstaged`
- `TestHasUncommittedChangesStaged`
- `TestCommitAllCommitsChanges`
- `TestCommitAllFailsWhenNothingToCommit`
- `TestDiscardAllRevertsUnstagedChanges`
- `TestRemoveManagedRepoRemovesClonedRepo`
- `TestRemoveManagedRepoRemovesWithUncommittedChanges`
- `TestMergeBranchIntoFastForward`
- `TestMergeBranchIntoConflict`
- `TestAbortMergeResetsMergeState`

Keep: `TestBranchSlug*`, `TestBranchAt*`, `TestHashID*`, `TestSanitizeFolderName*`, `TestProjectSlug*`, `TestPruneWorktrees*` (Task 4).

- [ ] **Step 6: Remove obsolete integration tests**

Remove from `internal/sandbox/integration_test.go`:
- `TestSameHomeVolumeInUseNoSandboxes`
- `TestSameBranchSessionExistsNoSandbox`
- `TestEnsureNoSameHomeSessionNoConflict`
- `TestSameHomeVolumeInUseConflict`

Keep: `TestStartDockerdIfPresentWithDindImage`, `TestStartDockerdIfPresentWithPlainBaseImage` (these test `startDockerdIfPresent` which is still used).

- [ ] **Step 7: Verify the codebase compiles**

Run: `go build ./...`
Expected: no compile errors.

Run: `go test ./...`
Expected: all remaining tests pass.

- [ ] **Step 8: Run linter**

Run: `golangci-lint fmt && golangci-lint run`
Expected: no lint errors. Watch for unused import warnings — remove imports for `prompt`, `slices` (from `volumes.go`), `errors` (from `git.go`) that are no longer needed.

- [ ] **Step 9: Commit**

```bash
git add internal/sandbox/volumes.go internal/sandbox/volumes_test.go internal/sandbox/runner.go internal/sandbox/runner_test.go internal/git/git.go internal/git/git_test.go internal/sandbox/integration_test.go
git commit -m "refactor: remove clone-on-use and managed-clone/merge machinery"
```

---

## Task 8: Integration test (one-VM-per-project lifecycle)

**Files:**
- Modify: `internal/sandbox/integration_test.go` (add `TestProjectVMLifecycle`)

**Design:** Add an integration test (behind `//go:build integration`) that exercises the full lifecycle: create project VM → ensure daemon health → attach a trivial command → detach → reconnect → stop/remove. This doubles as the core integration validation (the spike already confirmed worktrees share `project_id`; this test validates the launcher's VM lifecycle). Follow the existing integration test pattern in `integration_test.go` (skip if msb/docker unavailable).

**Interfaces:**
- Consumes: `EnsureProjectVM`, `EnsureDaemon`, `ResolveTarget`, `StopProjectVM`.
- Produces: `TestProjectVMLifecycle`.

- [ ] **Step 1: Write the integration test**

```go
// internal/sandbox/integration_test.go — append

func TestProjectVMLifecycle(t *testing.T) {
	ctx := t.Context()
	logger := newTestLogger(t)

	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	// Ensure msb runtime is available.
	if err := msb.EnsureInstalled(ctx); err != nil {
		t.Skipf("msb runtime not available: %v", err)
	}

	// Build the base image (same pattern as existing integration tests).
	dockerCli, err := client.New(client.FromEnv)
	if err != nil {
		t.Skipf("docker not available: %v", err)
	}
	defer dockerCli.Close()

	if err := buildDockerImage(ctx, dockerCli, EmbeddedDockerfile, BaseTag, "Building base", false, logger); err != nil {
		t.Skipf("cannot build base image: %v", err)
	}

	// Use a unique project slug derived from the test temp dir.
	tmpRepo := t.TempDir()
	t.Chdir(tmpRepo)
	runGitInTest(t, tmpRepo, "init", "-b", "main")
	runGitInTest(t, tmpRepo, "config", "user.email", "test@example.com")
	runGitInTest(t, tmpRepo, "config", "user.name", "Test User")
	writeFileInTest(t, tmpRepo, "README.md", "hello")
	runGitInTest(t, tmpRepo, "add", "README.md")
	runGitInTest(t, tmpRepo, "commit", "-m", "initial")

	projectSlug := git.ProjectSlug(logger)
	imageRef := BaseTag
	homeVolName := HomeVolumeName(projectSlug, "sha256:integration-test")
	// Ensure the home volume exists.
	if _, err := msb.GetVolume(ctx, homeVolName); err != nil {
		vol, volErr := msb.CreateVolume(ctx, homeVolName, msb.WithVolumeKind(msb.VolumeKindDir))
		if volErr != nil {
			t.Skipf("cannot create volume: %v", volErr)
		}
		defer func() { _ = msb.RemoveVolume(context.Background(), vol.Name()) }()
	}

	opts := RunOptions{Memory: "1G", TmpSize: "512M"}
	cfg := Config{
		StateDir:        filepath.Join(t.TempDir(), "state"),
		UserConfigDir:   t.TempDir(),
		UserLauncherDir: t.TempDir(),
	}

	// Step 1: EnsureProjectVM creates the VM.
	sb, created, err := EnsureProjectVM(ctx, opts, cfg, imageRef, homeVolName, tmpRepo, logger)
	if err != nil {
		t.Fatalf("EnsureProjectVM (create): %v", err)
	}
	if !created {
		t.Fatal("expected created=true on first call")
	}
	vmName := projectVMName(projectSlug)
	defer func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = sb.Detach(stopCtx)
		_ = StopProjectVM(context.Background(), true, logger)
	}()

	// Step 2: EnsureDaemon is healthy.
	if err := EnsureDaemon(ctx, sb, logger); err != nil {
		t.Fatalf("EnsureDaemon: %v", err)
	}

	// Step 3: ResolveTarget with no branch returns /workspace.
	target, err := ResolveTarget(ctx, sb, "", logger)
	if err != nil {
		t.Fatalf("ResolveTarget (no branch): %v", err)
	}
	if target != "/workspace" {
		t.Errorf("expected /workspace, got %q", target)
	}

	// Step 4: Attach a trivial command and verify it runs.
	exitCode, attachErr := sb.Attach(ctx, "/bin/bash", "-l", "-c", "echo hello")
	if attachErr != nil {
		t.Fatalf("attach failed: %v", attachErr)
	}
	if exitCode != 0 {
		t.Errorf("expected exit code 0, got %d", exitCode)
	}

	// Step 5: Detach and reconnect (simulates a second invocation).
	if err := sb.Detach(ctx); err != nil {
		t.Fatalf("detach failed: %v", err)
	}

	sb2, created2, err := EnsureProjectVM(ctx, opts, cfg, imageRef, homeVolName, tmpRepo, logger)
	if err != nil {
		t.Fatalf("EnsureProjectVM (reconnect): %v", err)
	}
	if created2 {
		t.Error("expected created=false on second call (VM should exist)")
	}
	_ = sb2.Detach(ctx)
}
```

Add the helper functions `runGitInTest` and `writeFileInTest` if they don't already exist in the integration test file (they do in `runner_test.go` but that's a different build tag). Duplicate them in the integration test file with the same signatures:

```go
// internal/sandbox/integration_test.go — add helpers if not present

func runGitInTest(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v in %s failed: %v: %s", args, dir, err, out)
	}
}

func writeFileInTest(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}
```

- [ ] **Step 2: Verify the test compiles**

Run: `go test -tags integration ./internal/sandbox/ -run TestProjectVMLifecycle -v -count=1`
Expected: The test runs (skips if msb/docker not available, passes if they are).

- [ ] **Step 3: Run all unit tests + linter (final verification)**

Run: `go test ./... && golangci-lint fmt && golangci-lint run`
Expected: All tests pass, no lint errors.

Run: `go run ./cmd/opencode-msb --dry-run`
Expected: Validates the setup without starting opencode. Note: `--dry-run` now means "ensure VM + daemon, skip attach". It may fail if msb/docker aren't available — that's expected in CI without the runtime.

- [ ] **Step 4: Commit**

```bash
git add internal/sandbox/integration_test.go
git commit -m "test: add integration test for project VM lifecycle"
```

---

## Summary

This plan implements the one-VM-per-project design from `docs/superpowers/specs/2026-07-27-one-vm-per-project-design.md`:

| Task | Component | Spec Section |
|------|-----------|--------------|
| 1 | Project VM naming, lifecycle, env provisioning + query filters | §3-A, §3-E, §3-F, §4 |
| 2 | Daemon supervisor | §3-B |
| 3 | Branch→worktree mapping | §3-D |
| 4 | Rewritten Run/Shell flow + host worktree prune | §3-C, §3-H, §4, §5 |
| 5 | Doctor orphan detection | §7 |
| 6 | CLI stop/kill commands | §3-G, §7 |
| 7 | Remove obsolete code | §3-Removed, §7 |
| 8 | Integration test | §6 |
