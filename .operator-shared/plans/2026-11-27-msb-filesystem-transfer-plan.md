---
description: TDD implementation plan for #920 - a cwd-scoped, running-VM `sandbox cp`/`copy` command for host↔project-VM files, the `SandboxFS.CopyFromHost`/`CopyToHost` seam methods it consumes via a `realSandboxFS` adapter, and the spec/guide/docs/changelog updates.
read_if: `sandbox cp`/`sandbox copy`, `vm.CopyProjectFile`, `msb.SandboxFS.CopyFromHost`/`CopyToHost`, or `realSandboxFS`; a `vm:` operand, a host↔project-VM file transfer that is refused or silent, or the `sandbox copy`/`cp` "dropped" note in `specs/volumes/runtime-management-commands.md`.
---

# msb filesystem/transfer Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a bounded `sandbox cp`/`copy <src> <dst>` command that transfers a single file between the host and the current checkout's project VM, by adopting the SDK's `SandboxFS.CopyFromHost`/`CopyToHost` behind a launcher adapter.

**Architecture:** Widen the `msb.SandboxFS` port with the two transfer methods and implement them in a launcher `realSandboxFS` that wraps the SDK `*SandboxFSOps` (the SDK type stays inside the adapter). A new `vm.CopyProjectFile` resolves the cwd checkout identity the way `stop`/`kill` do, requires the VM to be running (never boots), `Mkdir`s a nested guest destination's parent, and calls the right transfer method. A `copy`/`cp` CLI command parses the `vm:`-marked operand, infers direction, and renders one line or JSON; the spec's "dropped" decision is reversed.

**Tech Stack:** Go 1.27, module `github.com/inoio/agents-sandbox`, microsandbox SDK v0.8.3, Cobra, golangci-lint.

**Spec:** `.operator-shared/plans/2026-11-27-msb-filesystem-transfer-design.md`

## Global Constraints

- Base is `main` after #923 (PR #928) merges; the adapter's `LocalContext` destination and the #924 observation aliases are present. This plan does not touch those - it only widens `SandboxFS`.
- `phase4_boundary_test.go` must stay green: no production (non-`_test.go`) package outside `internal/sandbox/msb` may import `msbSdk`. `roles_boundary_test.go` must stay green; do not touch the role/composite interfaces.
- Unit tests for `internal/sandbox/msb` must stay FFI-free: swap the package-level seams with `swapSeam` (or use `msbtest` doubles), never resolve a real SDK resource.
- A unit's tests live in the file named after it (`fs.go` → `fs_test.go`, `vm_transfer.go` → `vm_transfer_test.go`); CLI tests go in `cmd/agents-sandbox/cli_*_test.go`. A different build tag may use a separate unit-named file (`fs_msb_test.go`).
- Guest paths are POSIX paths inside the VM: use `path`/`path.IsAbs`/`path.Dir`, never `path/filepath`.
- The command is not destructive: no `--yes`, no `--force`, no reference/in-use guard, and `--dry-run` is not added. The VM must already be running; `cp` never boots one.
- Exactly one operand carries the `vm:` marker and its guest path must be absolute; two `vm:` operands (VM↔VM) and zero `vm:` operands are errors. Directory and stream transfer are out of scope.
- Command surface conventions (`specs/volumes/runtime-management-commands.md`): alias `cp`, the `also: sandbox copy` tree annotation (register the command at both the top level and under `sandbox`, as `stop`/`kill` do), `-q`/`--quiet`, `--format json`.
- Format with `make fmt` (never `go fmt`); lint with `make lint` (never `go vet`); run `make check` before finalizing.
- The spec change to `specs/volumes/runtime-management-commands.md` (replace the `sandbox copy`/`cp` "dropped" line with the contract) is part of the work, as is `changelog.d/<PR-number>.added.md`.

## Review Focus

- **A `vm:` operand with a relative or empty guest path is rejected, not read as a host path** (the `vm:` marker is the only signal of side). Pinned by Task 3's operand-rejection table.
- **Two `vm:` operands (a VM→VM transfer) are refused, not silently handled or host-staged.** Pinned by Task 3's operand-rejection table.
- **A stopped/created/crashed/starting VM errors with how to start it and copies nothing** (the design's "no boot" non-goal). Pinned by Task 2's running-gate test.
- **A failed copy surfaces its cause instead of reporting success** (a missing host destination parent, a permission error). Pinned by Task 2's error propagation test and Task 3's copy-error test.
- **`--format json` together with `-q` is rejected before any transfer** (the shared mode resolver, not a produced-but-suppressed document). Pinned by Task 3's operand-rejection table.

---

### Task 1: Widen `SandboxFS` with the transfer methods and wrap the SDK filesystem

**Files:**
- Modify: `internal/sandbox/msb/fs.go` (interface + `realSandboxFS` + seams)
- Modify: `internal/sandbox/msb/sandbox.go` (add the `realSandboxFilesystem` seam; `realSandbox.FS()`)
- Create: `internal/sandbox/msb/fs_test.go`
- Modify: `internal/sandbox/msb/msb_test.go` (move the existing `TestRealSandboxFS` into `fs_test.go`)
- Modify: `internal/sandbox/msb/msbtest/testmock.go` (`TestFS` copy support)
- Modify: `internal/sandbox/msb/msbtest/testmock_test.go` (`TestTestFSRecordsCopies`)
- Create: `internal/sandbox/msb/fs_msb_test.go` (`//go:build msb`, host-only round trip)

**Interfaces:**
- Produces: `SandboxFS.CopyFromHost(ctx context.Context, hostPath, guestPath string) error` and `SandboxFS.CopyToHost(ctx context.Context, guestPath, hostPath string) error`.
- Produces: `type realSandboxFS struct { *msbSdk.SandboxFSOps }`; seams `var realSandboxFSCopyFromHost = (*msbSdk.SandboxFSOps).CopyFromHost`, `var realSandboxFSCopyToHost = (*msbSdk.SandboxFSOps).CopyToHost`; and `var realSandboxFilesystem = (*msbSdk.Sandbox).FS`.
- Produces: `msbtest.MockCopyCall{Source, Destination string}`; `TestFS.CopiedFromHost []MockCopyCall`, `TestFS.CopiedToHost []MockCopyCall`, `TestFS.CopyErr error`.

- [ ] **Step 1: Write the failing adapter tests (and move the existing FS test)**

Create `internal/sandbox/msb/fs_test.go`:

```go
package msb

import (
	"context"
	"errors"
	"testing"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"
)

// TestRealSandboxFS pins that realSandbox.FS returns the launcher adapter over
// the SDK filesystem, not the SDK type itself. It moves here (from msb_test.go)
// so the unit's tests live in the file named after the unit.
func TestRealSandboxFS(t *testing.T) {
	swapSeam(t, &realSandboxFilesystem,
		func(*msbSdk.Sandbox) *msbSdk.SandboxFSOps { return &msbSdk.SandboxFSOps{} })

	got := realSandbox{sandbox: &msbSdk.Sandbox{}}.FS()
	if _, ok := got.(realSandboxFS); !ok {
		t.Fatalf("FS() = %T, want realSandboxFS", got)
	}
}

// TestRealSandboxFSDelegatesTransfers pins that the launcher FS adapter routes
// the two transfer methods to the SDK filesystem with the argument order each
// method promises (hostPath, guestPath) and (guestPath, hostPath).
func TestRealSandboxFSDelegatesTransfers(t *testing.T) {
	ctx := context.Background()
	fs := realSandboxFS{SandboxFSOps: &msbSdk.SandboxFSOps{}}

	var fromHost [2]string
	swapSeam(t, &realSandboxFSCopyFromHost,
		func(_ *msbSdk.SandboxFSOps, _ context.Context, hostPath, guestPath string) error {
			fromHost = [2]string{hostPath, guestPath}
			return nil
		})
	if err := fs.CopyFromHost(ctx, "host.txt", "/guest.txt"); err != nil {
		t.Fatalf("CopyFromHost() error = %v", err)
	}
	if fromHost != [2]string{"host.txt", "/guest.txt"} {
		t.Errorf("CopyFromHost args = %v, want host then guest", fromHost)
	}

	var toHost [2]string
	swapSeam(t, &realSandboxFSCopyToHost,
		func(_ *msbSdk.SandboxFSOps, _ context.Context, guestPath, hostPath string) error {
			toHost = [2]string{guestPath, hostPath}
			return nil
		})
	if err := fs.CopyToHost(ctx, "/guest.txt", "host.txt"); err != nil {
		t.Fatalf("CopyToHost() error = %v", err)
	}
	if toHost != [2]string{"/guest.txt", "host.txt"} {
		t.Errorf("CopyToHost args = %v, want guest then host", toHost)
	}
}

// TestRealSandboxFSPropagatesTransferErrors pins that a failed SDK transfer is
// returned to the caller rather than swallowed, so sandbox cp reports it.
func TestRealSandboxFSPropagatesTransferErrors(t *testing.T) {
	cause := errors.New("transfer boom")
	fs := realSandboxFS{SandboxFSOps: &msbSdk.SandboxFSOps{}}
	swapSeam(t, &realSandboxFSCopyFromHost,
		func(*msbSdk.SandboxFSOps, context.Context, string, string) error { return cause })

	if err := fs.CopyFromHost(context.Background(), "host.txt", "/guest.txt"); !errors.Is(err, cause) {
		t.Fatalf("CopyFromHost() error = %v, want %v", err, cause)
	}
}
```

Delete the existing `TestRealSandboxFS` from `internal/sandbox/msb/msb_test.go` (lines ~578-584): its coverage moves to `fs_test.go` above, so the unit's tests live in the file named after the unit and the symbol is not declared twice.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/sandbox/msb/ -run 'TestRealSandboxFS'`
Expected: build failure, `undefined: realSandboxFS`, `undefined: realSandboxFSCopyFromHost`, `undefined: realSandboxFilesystem`.

- [ ] **Step 3: Implement the adapter**

Replace `internal/sandbox/msb/fs.go` with:

```go
package msb

import (
	"context"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"
)

// SandboxFS is the subset of the sandbox filesystem operations used by the launcher.
type SandboxFS interface {
	Exists(ctx context.Context, path string) (bool, error)
	ReadString(ctx context.Context, path string) (string, error)
	Mkdir(ctx context.Context, path string) error
	Write(ctx context.Context, path string, data []byte) error
	Read(ctx context.Context, path string) ([]byte, error)
	Remove(ctx context.Context, path string) error
	// CopyFromHost copies a single host file into the sandbox. It does not create
	// a missing parent directory, so a caller writing to a nested guest path
	// Mkdirs that parent first.
	CopyFromHost(ctx context.Context, hostPath, guestPath string) error
	// CopyToHost copies a single sandbox file to the host.
	CopyToHost(ctx context.Context, guestPath, hostPath string) error
}

// realSandboxFS adapts *msbSdk.SandboxFSOps to SandboxFS so the seam's
// filesystem surface is implemented by a launcher type, not the SDK type. The
// six read/write operations are promoted from the embedded SDK filesystem; the
// two transfer methods are explicit so their delegation is unit-testable
// without the native library.
type realSandboxFS struct {
	*msbSdk.SandboxFSOps
}

// The transfer delegation targets are package-level seams (method expressions)
// so the thin adapter is unit-testable without a live VM.
var (
	realSandboxFSCopyFromHost = (*msbSdk.SandboxFSOps).CopyFromHost
	realSandboxFSCopyToHost   = (*msbSdk.SandboxFSOps).CopyToHost
)

func (f realSandboxFS) CopyFromHost(ctx context.Context, hostPath, guestPath string) error {
	return realSandboxFSCopyFromHost(f.SandboxFSOps, ctx, hostPath, guestPath)
}

func (f realSandboxFS) CopyToHost(ctx context.Context, guestPath, hostPath string) error {
	return realSandboxFSCopyToHost(f.SandboxFSOps, ctx, guestPath, hostPath)
}
```

In `internal/sandbox/msb/sandbox.go`, add `realSandboxFilesystem` to the `realSandbox` delegation seams and wrap it in `FS()`:

```go
// realSandbox adapts *msbSdk.Sandbox to Sandbox.
type realSandbox struct {
	sandbox *msbSdk.Sandbox
}

var realSandboxFilesystem = (*msbSdk.Sandbox).FS

func (s realSandbox) FS() SandboxFS {
	return realSandboxFS{SandboxFSOps: realSandboxFilesystem(s.sandbox)}
}
```

(Replace the existing `func (s realSandbox) FS() SandboxFS { return s.sandbox.FS() }`.)

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/sandbox/msb/ -run 'TestRealSandboxFS'`
Expected: PASS.

- [ ] **Step 5: Write the failing mock test**

Append to `internal/sandbox/msb/msbtest/testmock_test.go`:

```go
// TestTestFSRecordsCopies pins that the filesystem double records each transfer
// with the side-correct paths, so a consumer's copy can be asserted.
func TestTestFSRecordsCopies(t *testing.T) {
	ctx := context.Background()
	fs := NewTestFS(nil)

	if err := fs.CopyFromHost(ctx, "host.txt", "/guest.txt"); err != nil {
		t.Fatalf("CopyFromHost() error = %v", err)
	}
	if err := fs.CopyToHost(ctx, "/guest.txt", "out.txt"); err != nil {
		t.Fatalf("CopyToHost() error = %v", err)
	}
	if got := fs.CopiedFromHost; len(got) != 1 || got[0] != (MockCopyCall{Source: "host.txt", Destination: "/guest.txt"}) {
		t.Errorf("CopiedFromHost = %+v", got)
	}
	if got := fs.CopiedToHost; len(got) != 1 || got[0] != (MockCopyCall{Source: "/guest.txt", Destination: "out.txt"}) {
		t.Errorf("CopiedToHost = %+v", got)
	}

	cause := errors.New("copy boom")
	failing := &TestFS{CopyErr: cause}
	if err := failing.CopyFromHost(ctx, "a", "/b"); !errors.Is(err, cause) {
		t.Errorf("CopyFromHost() error = %v, want %v", err, cause)
	}
	if err := failing.CopyToHost(ctx, "/b", "a"); !errors.Is(err, cause) {
		t.Errorf("CopyToHost() error = %v, want %v", err, cause)
	}
}
```

- [ ] **Step 6: Run the mock test to verify it fails**

Run: `go test ./internal/sandbox/msb/msbtest/ -run TestTestFSRecordsCopies`
Expected: build failure, `undefined: MockCopyCall`, `unknown field CopiedFromHost`.

- [ ] **Step 7: Implement the mock support**

In `internal/sandbox/msb/msbtest/testmock.go`, add to `TestFS`:

```go
// MockCopyCall records one transfer: Source is the from-side path and
// Destination the to-side path, whichever direction the copy ran.
type MockCopyCall struct {
	Source      string
	Destination string
}

// TestFS is a test double for SandboxFS that supports configurable file contents
// and error injection.
type TestFS struct {
	Contents       map[string][]byte
	ReadErr        error
	WriteErr       error
	MkdirErr       error
	CopyErr        error
	Writes         map[string][]byte
	Mkdirs         []string
	Removed        []string
	CopiedFromHost []MockCopyCall
	CopiedToHost   []MockCopyCall
}

func (t *TestFS) CopyFromHost(_ context.Context, hostPath, guestPath string) error {
	if t.CopyErr != nil {
		return t.CopyErr
	}
	t.CopiedFromHost = append(t.CopiedFromHost, MockCopyCall{Source: hostPath, Destination: guestPath})
	return nil
}

func (t *TestFS) CopyToHost(_ context.Context, guestPath, hostPath string) error {
	if t.CopyErr != nil {
		return t.CopyErr
	}
	t.CopiedToHost = append(t.CopiedToHost, MockCopyCall{Source: guestPath, Destination: hostPath})
	return nil
}
```

(Keep the existing `TestFS` fields and methods; this adds the two methods and fields.)

- [ ] **Step 8: Run the mock test to verify it passes**

Run: `go test ./internal/sandbox/msb/msbtest/ -run TestTestFSRecordsCopies`
Expected: PASS.

- [ ] **Step 9: Write the host-only real-VM round trip**

Create `internal/sandbox/msb/fs_msb_test.go` (host-only; it cannot run in the dev VM):

```go
//go:build msb

package msb

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/inoio/agents-sandbox/internal/sandbox/msb/msbtest/runtime"
)

// TestRealClientFileRoundTrip drives realSandbox.FS's transfer methods against a
// real sandbox: a host file copied in reads back, a nested guest destination's
// parent is created by the caller (CopyFromHost itself does not), and a file
// copied out matches the original.
func TestRealClientFileRoundTrip(t *testing.T) {
	runtime.EnsureIsolatedRuntime(t)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	client := realMsbClient{}
	name := "agents-sandbox-msb-cp-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), runtime.DestroyTimeout)
		defer cleanupCancel()
		if handle, err := client.GetSandbox(cleanupCtx, name); err == nil {
			_ = handle.Destroy(cleanupCtx, LifecycleOptions{})
		}
	})

	sb, err := client.CreateSandbox(ctx, name, SandboxSpec{
		Image:       "alpine:3.20",
		Detached:    true,
		IdleTimeout: runtime.TestIdleTimeout,
	})
	if err != nil {
		t.Fatalf("CreateSandbox: %v", err)
	}
	fs := sb.FS()

	hostIn := filepath.Join(t.TempDir(), "payload.txt")
	want := []byte("round-trip payload\n")
	if err := os.WriteFile(hostIn, want, 0o644); err != nil {
		t.Fatalf("write host input: %v", err)
	}
	if err := fs.Mkdir(ctx, "/tmp/cp-test/nested"); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	if err := fs.CopyFromHost(ctx, hostIn, "/tmp/cp-test/nested/payload.txt"); err != nil {
		t.Fatalf("CopyFromHost: %v", err)
	}
	got, err := fs.ReadString(ctx, "/tmp/cp-test/nested/payload.txt")
	if err != nil || got != string(want) {
		t.Fatalf("ReadString after CopyFromHost = (%q, %v), want %q", got, err, want)
	}

	hostOut := filepath.Join(t.TempDir(), "out.txt")
	if err := fs.CopyToHost(ctx, "/tmp/cp-test/nested/payload.txt", hostOut); err != nil {
		t.Fatalf("CopyToHost: %v", err)
	}
	roundTripped, err := os.ReadFile(hostOut)
	if err != nil || string(roundTripped) != string(want) {
		t.Fatalf("round-tripped host file = (%q, %v), want %q", roundTripped, err, want)
	}
}
```

- [ ] **Step 10: Run the package tests and lint**

Run: `go test ./internal/sandbox/msb/ ./internal/sandbox/msb/msbtest/` → PASS.
Run: `make lint` → clean.
(This does not compile the host-only test on this VM; Task 5 compiles/runs it on a KVM host.)

- [ ] **Step 11: Commit**

```bash
git add internal/sandbox/msb/fs.go internal/sandbox/msb/sandbox.go internal/sandbox/msb/fs_test.go \
  internal/sandbox/msb/msb_test.go internal/sandbox/msb/fs_msb_test.go \
  internal/sandbox/msb/msbtest/testmock.go internal/sandbox/msb/msbtest/testmock_test.go
git commit -m "feat(msb): expose SandboxFS transfer methods behind a realSandboxFS adapter"
```

---

### Task 2: Add `vm.CopyProjectFile`

**Files:**
- Create: `internal/sandbox/vm/vm_transfer.go`
- Create: `internal/sandbox/vm/vm_transfer_test.go`

**Interfaces:**
- Consumes: `msb.SandboxFS.CopyFromHost`/`CopyToHost`/`Mkdir` (Task 1); `FindProjectVM`, `AdoptLegacyIdentity` (`vm_name.go`, `adopt.go`); `agent.Lookup`, `git.ResolveIdentity`, `state.Key`.
- Fixture: the direction/guard tests chdir to a non-repo temp dir, so repo and checkout slugs coincide and `AdoptLegacyIdentity` finds no legacy source; `TestCopyProjectFileAppliesAPendingLegacyIdentity` uses an origin clone with a name-scoped `GetSandboxFn` to assert the `apply=true` adoption deliberately.
- Produces: `type CopyDirection int` with `CopyHostToVM` and `CopyVMToHost`, and `func (d CopyDirection) String() string` returning `"host-to-vm"`/`"vm-to-host"`.
- Produces: `type CopyResult struct { VMName string; Direction CopyDirection; HostPath, GuestPath string }`.
- Produces: `func CopyProjectFile(ctx context.Context, paths configpaths.ConfigPaths, mclient msb.Client, direction CopyDirection, hostPath, guestPath string, ui termio.UI, agentName string) (CopyResult, error)`.

- [ ] **Step 1: Write the failing tests**

Create `internal/sandbox/vm/vm_transfer_test.go`:

```go
package vm

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/configpaths/configpathstest"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb/msbtest"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio/termiotest"
)

// runningCopyHandle returns a running project VM whose sandbox exposes fs.
func runningCopyHandle(fs *msbtest.TestFS) *msbtest.MockSandboxHandle {
	return &msbtest.MockSandboxHandle{
		Name_:     "vm",
		Status_:   msb.SandboxStatusRunning,
		ConnectSb: &msbtest.MockSandbox{FSValue_: fs},
	}
}

// The isolated tests below chdir to a non-repo temp dir, so the repo and
// checkout slugs coincide and AdoptLegacyIdentity has no legacy source to adopt
// as a side effect. The explicit TestCopyProjectFileAppliesAPendingLegacyIdentity
// uses an origin clone to assert the adoption deliberately.

func TestCopyProjectFileHostToVMCreatesNestedGuestParent(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	t.Chdir(t.TempDir())
	fs := msbtest.NewTestFS(nil)
	client := &msbtest.MockMsbClient{}
	client.SetGotSandbox(runningCopyHandle(fs))
	ui := termiotest.NewTestMock(t)

	result, err := CopyProjectFile(context.Background(), configpaths.Get(), client,
		CopyHostToVM, "/host/patch.diff", "/workspace/sub/patch.diff", &ui, "opencode")
	if err != nil {
		t.Fatalf("CopyProjectFile() error = %v", err)
	}
	want := msbtest.MockCopyCall{Source: "/host/patch.diff", Destination: "/workspace/sub/patch.diff"}
	if got := fs.CopiedFromHost; len(got) != 1 || got[0] != want {
		t.Errorf("CopiedFromHost = %+v, want [%+v]", got, want)
	}
	if len(fs.Mkdirs) != 1 || fs.Mkdirs[0] != "/workspace/sub" {
		t.Errorf("Mkdirs = %v, want the guest parent /workspace/sub", fs.Mkdirs)
	}
	if result.Direction != CopyHostToVM || result.HostPath != "/host/patch.diff" || result.GuestPath != "/workspace/sub/patch.diff" {
		t.Errorf("result = %+v", result)
	}
}

func TestCopyProjectFileVMToHostCopiesWithoutMkdir(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	t.Chdir(t.TempDir())
	fs := msbtest.NewTestFS(nil)
	client := &msbtest.MockMsbClient{}
	client.SetGotSandbox(runningCopyHandle(fs))
	ui := termiotest.NewTestMock(t)

	if _, err := CopyProjectFile(context.Background(), configpaths.Get(), client,
		CopyVMToHost, "/host/out.diff", "/workspace/out.diff", &ui, "opencode"); err != nil {
		t.Fatalf("CopyProjectFile() error = %v", err)
	}
	want := msbtest.MockCopyCall{Source: "/workspace/out.diff", Destination: "/host/out.diff"}
	if got := fs.CopiedToHost; len(got) != 1 || got[0] != want {
		t.Errorf("CopiedToHost = %+v, want [%+v]", got, want)
	}
	if len(fs.Mkdirs) != 0 {
		t.Errorf("Mkdirs = %v, want none for a VM-to-host copy", fs.Mkdirs)
	}
}

func TestCopyProjectFileRequiresARunningVM(t *testing.T) {
	for _, status := range []msb.SandboxStatus{
		msb.SandboxStatusCreated, msb.SandboxStatusStarting, msb.SandboxStatusStopped,
		msb.SandboxStatusCrashed, msb.SandboxStatusPaused,
	} {
		t.Run(string(status), func(t *testing.T) {
			configpathstest.WithMockConfigPaths(t)
			t.Chdir(t.TempDir())
			fs := msbtest.NewTestFS(nil)
			handle := runningCopyHandle(fs)
			handle.Status_ = status
			client := &msbtest.MockMsbClient{}
			client.SetGotSandbox(handle)
			ui := termiotest.NewTestMock(t)

			_, err := CopyProjectFile(context.Background(), configpaths.Get(), client,
				CopyHostToVM, "/host/patch.diff", "/workspace/patch.diff", &ui, "opencode")
			if err == nil || !strings.Contains(err.Error(), "not running") {
				t.Fatalf("error = %v, want a not-running refusal naming how to start", err)
			}
			if len(fs.CopiedFromHost) != 0 {
				t.Error("a non-running VM must not copy anything")
			}
		})
	}
}

func TestCopyProjectFileNoProjectVM(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	t.Chdir(t.TempDir())
	client := &msbtest.MockMsbClient{}
	client.SetGetSandboxErr(&msbSdk.Error{Kind: msbSdk.ErrSandboxNotFound, Message: "gone"})
	ui := termiotest.NewTestMock(t)

	_, err := CopyProjectFile(context.Background(), configpaths.Get(), client,
		CopyHostToVM, "/host/patch.diff", "/workspace/patch.diff", &ui, "opencode")
	if err == nil || !strings.Contains(err.Error(), "no project VM") {
		t.Fatalf("error = %v, want a no-project-VM refusal", err)
	}
}

func TestCopyProjectFilePropagatesCopyError(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	t.Chdir(t.TempDir())
	cause := errors.New("copy boom")
	fs := msbtest.NewTestFS(nil)
	fs.CopyErr = cause
	client := &msbtest.MockMsbClient{}
	client.SetGotSandbox(runningCopyHandle(fs))
	ui := termiotest.NewTestMock(t)

	_, err := CopyProjectFile(context.Background(), configpaths.Get(), client,
		CopyHostToVM, "/host/patch.diff", "/workspace/patch.diff", &ui, "opencode")
	if !errors.Is(err, cause) {
		t.Fatalf("error = %v, want the copy failure %v", err, cause)
	}
}

func TestCopyDirectionString(t *testing.T) {
	if got := CopyHostToVM.String(); got != "host-to-vm" {
		t.Errorf("CopyHostToVM = %q, want host-to-vm", got)
	}
	if got := CopyVMToHost.String(); got != "vm-to-host" {
		t.Errorf("CopyVMToHost = %q, want vm-to-host", got)
	}
}

// TestCopyProjectFileAppliesAPendingLegacyIdentity pins the deliberate adoption:
// with a distinct repo slug carrying legacy state, cp applies the migration
// (apply=true) before resolving the checkout project VM, rather than copying
// from a half-understood identity.
func TestCopyProjectFileAppliesAPendingLegacyIdentity(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	t.Chdir(newOriginClone(t))
	identity := mustIdentity(t, ".")
	if identity.RepoSlug == identity.CheckoutSlug {
		t.Fatalf("fixture must have distinct repo and checkout slugs")
	}
	legacyKey := state.Key{Slug: identity.RepoSlug, Agent: "opencode"}
	if err := seedHomeState(legacyKey, state.HomeState{HomeVolume: "home-legacy"}); err != nil {
		t.Fatalf("seed legacy state: %v", err)
	}

	fs := msbtest.NewTestFS(nil)
	checkoutKey := state.Key{Slug: identity.CheckoutSlug, Agent: "opencode"}
	client := &msbtest.MockMsbClient{}
	client.GetSandboxFn = func(_ context.Context, name string) (msb.SandboxHandle, error) {
		// Only the checkout VM exists; the legacy name must look absent so the
		// pending trigger comes from state, not from a name-agnostic stub.
		if name == ProjectVMName(checkoutKey) {
			return runningCopyHandle(fs), nil
		}
		return nil, &msbSdk.Error{Kind: msbSdk.ErrSandboxNotFound, Message: "gone"}
	}
	ui := termiotest.NewTestMock(t)

	if _, err := CopyProjectFile(context.Background(), configpaths.Get(), client,
		CopyHostToVM, "/host/patch.diff", "/workspace/patch.diff", &ui, "opencode"); err != nil {
		t.Fatalf("CopyProjectFile() error = %v", err)
	}
	if len(fs.CopiedFromHost) != 1 {
		t.Errorf("CopiedFromHost = %+v, want the copy to run under the checkout identity", fs.CopiedFromHost)
	}
	if !statePresent(t, checkoutKey) {
		t.Error("the pending legacy state must be adopted onto the checkout")
	}
	if statePresent(t, legacyKey) {
		t.Error("the legacy state must be gone after the applied adoption")
	}
	if !slices.ContainsFunc(ui.WarnCalls, func(c string) bool {
		return strings.Contains(c, "Migrating this checkout")
	}) {
		t.Errorf("WarnCalls = %v, want the applied-adoption notice", ui.WarnCalls)
	}
}
```

`ProjectVMName`, `seedHomeState`, `statePresent`, `mustIdentity`, and `newOriginClone` are in the same `vm` test package (`vm_name.go`, `helpers_test.go`, `vm_control_test.go`). The `state`-seeded trigger means the legacy VM is not required; if the adoption path ever consults it, mirror `adopt_test.go`'s name-scoped handle fixture.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/sandbox/vm/ -run 'TestCopyProjectFile|TestCopyDirectionString'`
Expected: build failure, `undefined: CopyProjectFile`, `undefined: CopyHostToVM`.

- [ ] **Step 3: Implement `internal/sandbox/vm/vm_transfer.go`**

Write the direction enum, the result, and the copy function. The exact signatures and pinned values:

```go
// CopyDirection is the direction of a host↔project-VM file transfer.
type CopyDirection int

const (
	// CopyHostToVM copies a host file into the project VM.
	CopyHostToVM CopyDirection = iota
	// CopyVMToHost copies a project-VM file out to the host.
	CopyVMToHost
)

func (d CopyDirection) String() string {
	if d == CopyVMToHost {
		return "vm-to-host"
	}
	return "host-to-vm"
}

// CopyResult is a completed host↔project-VM transfer. HostPath and GuestPath
// name the two sides, not the source/destination order.
type CopyResult struct {
	VMName    string
	Direction CopyDirection
	HostPath  string
	GuestPath string
}
```

`CopyProjectFile` resolves identity as `StopProjectVM` does, requires a running VM, then copies:

```go
func CopyProjectFile(
	ctx context.Context,
	paths configpaths.ConfigPaths,
	mclient msb.Client,
	direction CopyDirection,
	hostPath, guestPath string,
	ui termio.UI,
	agentName string,
) (CopyResult, error) {
	a, ok := agent.Lookup(agentName)
	if !ok {
		return CopyResult{}, fmt.Errorf("unknown agent %q", agentName)
	}
	identity, err := git.ResolveIdentity(".")
	if err != nil {
		return CopyResult{}, err
	}
	// cp has no dry run, so a pending legacy identity is applied as run does.
	adoption, err := AdoptLegacyIdentity(ctx, paths, mclient, identity, a.Name(), true, ui)
	if err != nil {
		return CopyResult{}, err
	}
	k := state.Key{Slug: adoption.Slug, Agent: a.Name()}

	handle, found, err := FindProjectVM(ctx, mclient, k)
	if err != nil {
		return CopyResult{}, err
	}
	if !found {
		return CopyResult{}, errors.New(
			"no project VM for this checkout; start it with `agents-sandbox run` or `agents-sandbox shell`")
	}
	if handle.Status() != msb.SandboxStatusRunning {
		return CopyResult{}, fmt.Errorf(
			"project VM %q is %s, not running; start it with `agents-sandbox run` or `agents-sandbox shell`",
			handle.Name(), handle.Status())
	}
	// Connect is status-gated and never boots the VM.
	sandbox, err := handle.Connect(ctx)
	if err != nil {
		return CopyResult{}, fmt.Errorf("connect to sandbox %q: %w", handle.Name(), err)
	}
	if err := copyFile(ctx, sandbox.FS(), direction, hostPath, guestPath); err != nil {
		return CopyResult{}, err
	}
	return CopyResult{VMName: handle.Name(), Direction: direction, HostPath: hostPath, GuestPath: guestPath}, nil
}

// copyFile runs one transfer. CopyFromHost does not create the guest
// destination's parent, so a nested guest destination is Mkdir'd first (the SDK
// Mkdir creates every missing parent); CopyToHost needs no such step.
func copyFile(
	ctx context.Context,
	fs msb.SandboxFS,
	direction CopyDirection,
	hostPath, guestPath string,
) error {
	if direction == CopyVMToHost {
		if err := fs.CopyToHost(ctx, guestPath, hostPath); err != nil {
			return fmt.Errorf("copy %s -> %s: %w", guestPath, hostPath, err)
		}
		return nil
	}
	if parent := path.Dir(guestPath); parent != "/" {
		if err := fs.Mkdir(ctx, parent); err != nil {
			return fmt.Errorf("create guest destination directory %s: %w", parent, err)
		}
	}
	if err := fs.CopyFromHost(ctx, hostPath, guestPath); err != nil {
		return fmt.Errorf("copy %s -> %s: %w", hostPath, guestPath, err)
	}
	return nil
}
```

Imports: `context`, `errors`, `fmt`, `path`, `agent`, `configpaths`, `git`, `msb`, `state`, `termio`.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/sandbox/vm/ -run 'TestCopyProjectFile|TestCopyDirectionString'`
Expected: PASS.

- [ ] **Step 5: Run the package tests and lint**

Run: `go test ./internal/sandbox/vm/` → PASS.
Run: `make lint` → clean.

- [ ] **Step 6: Commit**

```bash
git add internal/sandbox/vm/vm_transfer.go internal/sandbox/vm/vm_transfer_test.go
git commit -m "feat(vm): copy a file between the host and the running project VM"
```

---

### Task 3: Add the `sandbox copy`/`cp` command

**Files:**
- Create: `cmd/agents-sandbox/copy_cli.go`
- Create: `cmd/agents-sandbox/cli_sandbox_copy_test.go`
- Modify: `cmd/agents-sandbox/constants.go` (`cmdCopy`, `cmdCopyAliases`)
- Modify: `cmd/agents-sandbox/commands_system.go` (`buildSandboxCmd` adds it)
- Modify: `cmd/agents-sandbox/commands.go` (`buildRootCmd` adds it)

**Interfaces:**
- Consumes: `vm.CopyProjectFile`, `vm.CopyDirection`/`CopyHostToVM`/`CopyVMToHost`, `vm.CopyResult` (Task 2); `cliout.ResolveMode`, `cliout.ModeJSON`, `cliout.ModeIdentifiers`, `cliout.WriteJSON`; `resolveAgentFlag`, `bindAgentFlag`.
- Produces: `func buildCopyCmd(ui termio.UI) *cobra.Command` (annotation `also: sandbox copy`, alias `cp`, `--format`, `--agent`).
- Produces: `const copyVMOperandPrefix = "vm:"`; `func parseCopyOperands(source, destination string) (vm.CopyDirection, hostPath, guestPath string, err error)`; `type copyJSON struct { VM, Direction, Source, Destination string }`; `func renderCopy(ui termio.UI, mode cliout.Mode, result vm.CopyResult, source, destination string) error`.

- [ ] **Step 1: Write the failing CLI tests**

Create `cmd/agents-sandbox/cli_sandbox_copy_test.go`:

```go
package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb/msbtest"
	"github.com/inoio/agents-sandbox/internal/termio/termiotest"
)

// setupCopyConfig installs a mock msb client for a sandbox copy command.
func setupCopyConfig(
	t *testing.T,
	args []string,
	mockSetup func(*msbtest.MockMsbClient),
) (*cobra.Command, *termiotest.Mock) {
	t.Helper()
	cmd, ui := setupCommandFixtures(t, args...)
	mock := &msbtest.MockMsbClient{}
	if mockSetup != nil {
		mockSetup(mock)
	}
	msbtest.WithMsbMock(t, mock)
	return cmd, ui
}

// runningCopyMock sets a running project VM whose sandbox exposes fs.
func runningCopyMock(fs *msbtest.TestFS) func(*msbtest.MockMsbClient) {
	return func(m *msbtest.MockMsbClient) {
		m.SetGotSandbox(&msbtest.MockSandboxHandle{
			Name_: "vm", Status_: msb.SandboxStatusRunning,
			ConnectSb: &msbtest.MockSandbox{FSValue_: fs},
		})
	}
}

func TestSandboxCopyHostToVM(t *testing.T) {
	initTestProjectDir(t)
	fs := msbtest.NewTestFS(nil)
	cmd, ui := setupCopyConfig(t, []string{"sandbox", "cp", "./patch.diff", "vm:/workspace/sub/patch.diff"}, runningCopyMock(fs))
	if err := cmd.Execute(); err != nil {
		t.Fatalf("sandbox cp: %v", err)
	}
	if got := fs.CopiedFromHost; len(got) != 1 || got[0].Destination != "/workspace/sub/patch.diff" {
		t.Fatalf("CopiedFromHost = %+v", got)
	}
	if len(fs.Mkdirs) != 1 || fs.Mkdirs[0] != "/workspace/sub" {
		t.Fatalf("Mkdirs = %v, want the nested guest parent", fs.Mkdirs)
	}
	if !containsNormalized(ui.InfoCalls, "Copied ./patch.diff -> vm:/workspace/sub/patch.diff") {
		t.Fatalf("InfoCalls = %v", ui.InfoCalls)
	}
}

func TestSandboxCopyVMToHost(t *testing.T) {
	initTestProjectDir(t)
	fs := msbtest.NewTestFS(nil)
	cmd, _ := setupCopyConfig(t, []string{"sandbox", "copy", "vm:/workspace/out.diff", "./out.diff"}, runningCopyMock(fs))
	if err := cmd.Execute(); err != nil {
		t.Fatalf("sandbox copy: %v", err)
	}
	if got := fs.CopiedToHost; len(got) != 1 || got[0].Destination != "./out.diff" {
		t.Fatalf("CopiedToHost = %+v", got)
	}
}

func TestSandboxCopyRejectsOperands(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{"two vm operands", []string{"vm:/a", "vm:/b"}, "exactly one"},
		{"no vm operand", []string{"./a", "./b"}, "exactly one"},
		{"relative guest destination", []string{"./a", "vm:rel/b"}, "must be absolute"},
		{"relative guest source", []string{"vm:rel/a", "./b"}, "must be absolute"},
		{"empty guest destination", []string{"./a", "vm:"}, "must be absolute"},
		{"empty guest source", []string{"vm:", "./b"}, "must be absolute"},
		{"json and quiet", []string{"--format", "json", "-q", "./a", "vm:/b"}, "mutually exclusive"},
		{"unsupported format", []string{"--format", "yaml", "./a", "vm:/b"}, "yaml"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			initTestProjectDir(t)
			cmd, _ := setupCopyConfig(t, append([]string{"sandbox", "copy"}, tt.args...), nil)
			if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestSandboxCopyRequiresRunningVM(t *testing.T) {
	initTestProjectDir(t)
	fs := msbtest.NewTestFS(nil)
	cmd, _ := setupCopyConfig(t, []string{"sandbox", "cp", "./a", "vm:/b"},
		func(m *msbtest.MockMsbClient) {
			m.SetGotSandbox(&msbtest.MockSandboxHandle{
				Name_: "vm", Status_: msb.SandboxStatusStopped,
				ConnectSb: &msbtest.MockSandbox{FSValue_: fs},
			})
		})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "not running") {
		t.Fatalf("error = %v, want a not-running refusal", err)
	}
	if len(fs.CopiedFromHost) != 0 {
		t.Error("a stopped VM must not copy anything")
	}
}

func TestSandboxCopyQuietSuppressesOutput(t *testing.T) {
	initTestProjectDir(t)
	cmd, ui := setupCopyConfig(t, []string{"sandbox", "cp", "-q", "./a", "vm:/b"}, runningCopyMock(msbtest.NewTestFS(nil)))
	if err := cmd.Execute(); err != nil {
		t.Fatalf("sandbox cp -q: %v", err)
	}
	if len(ui.InfoCalls) != 0 || len(ui.OutCalls) != 0 {
		t.Errorf("quiet copy wrote output: info=%v out=%v", ui.InfoCalls, ui.OutCalls)
	}
}

func TestSandboxCopyJSON(t *testing.T) {
	initTestProjectDir(t)
	cmd, ui := setupCopyConfig(t, []string{"sandbox", "cp", "--format", "json", "./patch.diff", "vm:/workspace/patch.diff"},
		runningCopyMock(msbtest.NewTestFS(nil)))
	if err := cmd.Execute(); err != nil {
		t.Fatalf("sandbox cp --format json: %v", err)
	}
	if len(ui.OutCalls) != 1 {
		t.Fatalf("OutCalls = %v, want the one JSON document", ui.OutCalls)
	}
	var got copyJSON
	if err := json.Unmarshal([]byte(ui.OutCalls[0]), &got); err != nil {
		t.Fatalf("unmarshal %q: %v", ui.OutCalls[0], err)
	}
	if got.VM != "vm" || got.Direction != "host-to-vm" || got.Source != "./patch.diff" || got.Destination != "vm:/workspace/patch.diff" {
		t.Fatalf("copyJSON = %+v", got)
	}
}

func TestSandboxCopySurfacesCopyError(t *testing.T) {
	initTestProjectDir(t)
	fs := msbtest.NewTestFS(nil)
	fs.CopyErr = errBoom
	cmd, _ := setupCopyConfig(t, []string{"sandbox", "cp", "./a", "vm:/b"}, runningCopyMock(fs))
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "copy ./a -> /b") {
		t.Fatalf("error = %v, want the copy failure", err)
	}
}
```

`containsNormalized` and `errBoom` are existing helpers in the `cmd/agents-sandbox` test package (see `cli_stop_kill_test.go`, `cli_inspect_test.go`).

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./cmd/agents-sandbox/ -run 'TestSandboxCopy'`
Expected: build failure, `unknown command "copy"`, `undefined: copyJSON`.

- [ ] **Step 3: Add the constants and wire the command**

In `cmd/agents-sandbox/constants.go`, add to the command block:

```go
	cmdCopy       = "copy"
```

and to the aliases block:

```go
	cmdCopyAliases = []string{"cp"}
```

In `cmd/agents-sandbox/commands_system.go`, add `cmd.AddCommand(buildCopyCmd(ui))` to `buildSandboxCmd` (beside `buildSandboxCompactCmd`). In `cmd/agents-sandbox/commands.go`, add `rootCmd.AddCommand(buildCopyCmd(ui))` beside `buildStopCmd`/`buildKillCmd` so the top-level `copy` carries the `also: sandbox copy` annotation.

- [ ] **Step 4: Implement `cmd/agents-sandbox/copy_cli.go`**

Write the command, the operand parser, and the renderer. Pinned behaviour:

```go
const copyVMOperandPrefix = "vm:"

// copyJSON is the --format json shape of a sandbox copy.
type copyJSON struct {
	VM          string `json:"vm"`
	Direction   string `json:"direction"`
	Source      string `json:"source"`
	Destination string `json:"destination"`
}

func buildCopyCmd(ui termio.UI) *cobra.Command {
	var format string
	cmd := &cobra.Command{
		Use:     cmdCopy + " <src> <dst>",
		Aliases: cmdCopyAliases,
		Args:    cobra.ExactArgs(2),
		Short:   "Copy a file between the host and the project VM",
		Long: "Copy a single file between the host and the current checkout's project VM, either " +
			"direction. Exactly one operand is a host path and the other a vm:<guest-path> (an absolute " +
			"path inside the VM). The VM must be running; cp never starts a stopped VM.",
		Annotations: map[string]string{
			annotationAlsoAs: cmdSandbox + " " + cmdCopy,
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			mode, err := cliout.ResolveMode(format, ui.Quiet())
			if err != nil {
				return err
			}
			direction, hostPath, guestPath, err := parseCopyOperands(args[0], args[1])
			if err != nil {
				return err
			}
			a, err := resolveAgentFlag(cmd)
			if err != nil {
				return err
			}
			result, err := sandbox.CopyProjectFile(cmd.Context(), configpaths.Get(), msb.Get(),
				direction, hostPath, guestPath, ui, a.Name())
			if err != nil {
				return err
			}
			return renderCopy(ui, mode, result, args[0], args[1])
		},
	}
	cmd.Flags().StringVar(&format, flagFormat, "", "Output format (json)")
	bindAgentFlag(cmd)
	return cmd
}
```

`parseCopyOperands` (pure, POSIX paths): exactly one `vm:` prefix; strip it; reject a non-absolute guest path.

```go
func parseCopyOperands(source, destination string) (vm.CopyDirection, string, string, error) {
	sourceIsVM := strings.HasPrefix(source, copyVMOperandPrefix)
	destinationIsVM := strings.HasPrefix(destination, copyVMOperandPrefix)
	switch {
	case sourceIsVM && destinationIsVM:
		return 0, "", "", errors.New("exactly one of <src> and <dst> may be a vm: path; VM-to-VM copies are not supported")
	case !sourceIsVM && !destinationIsVM:
		return 0, "", "", errors.New("exactly one of <src> and <dst> must be a vm: path; prefix the guest file with vm:")
	case sourceIsVM:
		guestPath := strings.TrimPrefix(source, copyVMOperandPrefix)
		if !path.IsAbs(guestPath) {
			return 0, "", "", fmt.Errorf("the vm: guest path %q must be absolute", guestPath)
		}
		return vm.CopyVMToHost, destination, guestPath, nil
	default:
		guestPath := strings.TrimPrefix(destination, copyVMOperandPrefix)
		if !path.IsAbs(guestPath) {
			return 0, "", "", fmt.Errorf("the vm: guest path %q must be absolute", guestPath)
		}
		return vm.CopyHostToVM, source, guestPath, nil
	}
}
```

`renderCopy`: one line by default, JSON under `--format json`, nothing under `-q` (the design suppresses the line rather than printing an identifier).

```go
func renderCopy(ui termio.UI, mode cliout.Mode, result vm.CopyResult, source, destination string) error {
	switch mode {
	case cliout.ModeJSON:
		return cliout.WriteJSON(ui, copyJSON{
			VM:          result.VMName,
			Direction:   result.Direction.String(),
			Source:      source,
			Destination: destination,
		})
	case cliout.ModeIdentifiers:
		return nil
	default:
		ui.Infof("Copied %s -> %s", source, destination)
		return nil
	}
}
```

Imports: `errors`, `fmt`, `path`, `strings`, `cobra`, `cliout`, `configpaths`, `msb`, `sandbox "github.com/inoio/agents-sandbox/internal/sandbox/vm"`, `termio`.

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./cmd/agents-sandbox/ -run 'TestSandboxCopy'`
Expected: PASS.

- [ ] **Step 6: Run the package tests and lint**

Run: `go test ./cmd/agents-sandbox/` → PASS.
Run: `make lint` → clean.

- [ ] **Step 7: Commit**

```bash
git add cmd/agents-sandbox/copy_cli.go cmd/agents-sandbox/cli_sandbox_copy_test.go \
  cmd/agents-sandbox/constants.go cmd/agents-sandbox/commands_system.go cmd/agents-sandbox/commands.go
git commit -m "feat(cli): add sandbox copy/cp for host<->project-VM files"
```

---

### Task 4: Reverse the spec decision and update the seam spec, guide, docs, and changelog

**Files:**
- Modify: `.operator-shared/specs/volumes/runtime-management-commands.md`
- Modify: `.operator-shared/specs/architecture/msb-seam.md`
- Modify: `.operator-shared/guides/msb-sdk-integration.md` (§1.6)
- Modify: `docs/commands.md`
- Modify: `docs/sandboxes.md`
- Create: `changelog.d/<PR-number>.added.md`

**Interfaces:**
- Consumes: the shipped command and seam from Tasks 1-3.
- Produces: no code symbol; the contracts.

- [ ] **Step 1: Reverse the dropped decision and fold transfer into the spec's scope**

In `.operator-shared/specs/volumes/runtime-management-commands.md`, make all four edits, so the spec's own scope summary matches the new command (leaving the `image pull`/`load`/`save` exclusion intact):

1. Replace the `sandbox copy`/`cp` bullet (the "dropped: host↔VM transfer is the excluded data-portability surface" lines) with:

```markdown
- `sandbox copy`/`cp <src> <dst>` — transfer a single file between the host and
  the current checkout's project VM, either direction. Exactly one operand is a host
  path and the other a `vm:<guest-path>` (an absolute path inside the VM); both `vm:`
  or neither is an error. The VM is the cwd checkout's project VM (resolved like
  `sandbox stop`/`kill`, including a legacy adopted slug) and must be **running** —
  cp never boots a stopped VM, naming `run`/`shell` in the error. A nested guest
  destination's parent directory is created before the copy. The transfer is not
  destructive, so there is no confirmation, no reference guard, and `--dry-run` is
  not meaningful. `-q/--quiet` suppresses the transfer line; `--format json` reports
  the copied item (vm, direction, source, destination). VM↔VM transfer, directory
  transfer, and addressing a named/arbitrary VM are out of scope.
```

2. Frontmatter `description`: acknowledge the transfer category between "sandbox disk compaction" and "a top-level `df`", keeping the `data-portability` exclusion in the "not a mirror of msb's … surface" tail:
   - Before: "… sandbox disk compaction, a top-level `df` plus a `storage` group alias, and uniform …"
   - After: "… sandbox disk compaction, a bounded `sandbox copy` host↔project-VM file transfer, a top-level `df` plus a `storage` group alias, and uniform …"

3. Intent: extend the management-surface enumeration to include transfer:
   - Before: "… without an `msb` binary: object inspection, object removal, storage accounting, and uniform prune controls."
   - After: "… without an `msb` binary: object inspection, object removal, storage accounting, uniform prune controls, and a bounded host↔project-VM file transfer."
   Leave the next sentence ("not a front-end to msb, and not a mirror of its lifecycle or portability features") unchanged; the Non-goals `image pull`/`load`/`save` exclusion stays.

4. Goals: add a bullet after "Provide opt-in compaction of project VM disks.":
   - "Provide a bounded, cwd-scoped transfer of a single file between the host and the current checkout's project VM."

- [ ] **Step 2: Record the seam clause**

In `.operator-shared/specs/architecture/msb-seam.md`, add a bullet under "Launcher-owned types" (after the "Observation aliases" bullet):

```markdown
- **Filesystem.** `SandboxFS` is a launcher-owned interface; its `CopyFromHost`/`CopyToHost`
  transfer methods are implemented by the launcher `realSandboxFS` adapter over the SDK
  `*SandboxFSOps`, so the SDK type does not itself satisfy the interface. Both take plain
  paths and return an error; no SDK type crosses the seam. `CopyFromHost` does not create a
  missing parent directory, so the consumer `sandbox cp` Mkdirs a nested guest parent first.
```

and a Status entry:

```markdown
- **Landed (#920):** `SandboxFS` gained `CopyFromHost`/`CopyToHost`, implemented by the new
  `realSandboxFS` adapter wrapping `*msbSdk.SandboxFSOps` (returned by `realSandbox.FS`).
  Their consumer is the cwd-scoped `sandbox cp`/`copy` command
  (specs/volumes/runtime-management-commands.md).
```

- [ ] **Step 3: Fold the adoption into the SDK integration guide**

In `.operator-shared/guides/msb-sdk-integration.md` §1.6, replace the "six methods… Copying a patch out… is not expressible today" paragraph with:

```markdown
### 1.6 Filesystem and transfer surface

The seam's `SandboxFS` exposes `Exists`, `ReadString`, `Mkdir`, `Write`, `Read`, `Remove`,
`CopyFromHost`, and `CopyToHost`. The first six served the read-back (config/provenance)
path; `CopyFromHost`/`CopyToHost` were adopted by `sandbox cp` (#920), the bounded
host↔current-project-VM file transfer, so the SDK's file copy is now reachable through the
launcher. `List`/`Stat` (`Copy`/`Rename`/`RemoveDir`) and `ReadStream`/`WriteStream` stay
unused: directory transfer and stdout piping are out of scope, and the filesystem ticket
owns the surface (SFTP is not a second path to it; see §1.8).
```

- [ ] **Step 4: Update the user docs**

In `docs/commands.md`:
- In the `sandbox` group intro, add `copy` to the list of subcommands **also available at the top level** (`run`, `shell`, `stop`, `kill`, `list`) — Task 3 registers it at both levels, so it does not belong in the "live only under this group" list.
- Add a `#### sandbox copy` section after `#### sandbox compact`, documenting the two-operand `vm:` form, the running-only requirement, `-q`/`--quiet`, `--format json`, and a `console` example (`agents-sandbox sandbox cp ./patch.diff vm:/workspace/patch.diff`, the reverse, and `--format json`).

In `docs/sandboxes.md`, add a Quick reference row:

```markdown
| Copy a file in or out of the project sandbox | `agents-sandbox sandbox cp <src> <dst>` |
```

- [ ] **Step 5: Add the changelog fragment**

Create `changelog.d/<PR-number>.added.md` (use the implementation PR's number):

```markdown
Add `sandbox copy`/`cp <src> <dst>` to transfer a single file between the host and the current checkout's running project VM, in either direction (`vm:` marks the guest operand).
```

- [ ] **Step 6: Run the docs and brain gates**

Run: `make docs-linkcheck brain-check`
Expected: both report ok.

- [ ] **Step 7: Commit**

```bash
git add .operator-shared/specs/volumes/runtime-management-commands.md \
  .operator-shared/specs/architecture/msb-seam.md .operator-shared/guides/msb-sdk-integration.md \
  docs/commands.md docs/sandboxes.md changelog.d/
git commit -m "docs: record the sandbox copy contract and adopt the transfer seam"
```

---

### Task 5: Run the full gate and verify on a KVM host

**Files:**
- None (verification only).

**Interfaces:**
- Consumes: all prior tasks.

- [ ] **Step 1: Run the full gate**

Run: `make check`
Expected: every target reports PASS. If `validate-fmt` fails, run `make fmt`. If the tree/help tests fail, update the affected `cmd/agents-sandbox/cli_*_test.go` expectations.

- [ ] **Step 2: Compile the tagged tests**

Run: `CGO_ENABLED=1 go test -tags msb -run '^$' ./internal/sandbox/msb/`
Expected: compiles (`[no tests to run]` is compile-only), catching a tagged-only build error in `fs_msb_test.go`.

- [ ] **Step 3: Verify the real-VM round trip on a KVM host**

On a host with KVM and a Docker daemon, run one of:
- `make test-msb` (the whole host-only suite), or
- `CGO_ENABLED=1 go test -tags msb -run TestRealClientFileRoundTrip ./internal/sandbox/msb/`.

Expected: PASS — a host file copied in reads back from the guest, a nested guest destination's parent is created, and a file copied out matches the original. This cannot run in the development VM (no functional `/dev/kvm`).

- [ ] **Step 4: Confirm the command by hand (optional, host)**

With a running project VM, run `agents-sandbox sandbox cp ./patch.diff vm:/workspace/patch.diff` and the reverse, plus `--format json` and `-q`, and confirm the output shapes.

---

## Out of scope

- VM↔VM transfer (would need a host-staging design), directory transfer, and `ReadStream`/`WriteStream` piping.
- Addressing an arbitrary/named project VM, or booting a stopped VM from `cp`.
- A `run --export` or any transfer smuggled into the run lifecycle.
- `List`/`Stat`, `Copy`/`Rename`/`RemoveDir`, or the SSH/SFTP and agent-client surfaces (declined in `guides/msb-sdk-integration.md` §1.8).
- Any other SDK capability work (#916, #918, #919, #921-#924).
