---
description: TDD implementation plan for #923 - adopt an explicit LocalContext and its Sandboxes()/Volumes() collections in the msb adapter, plus the contract tests and spec clauses that record the captured-observation, error-class, enum, and unavailable-inspection decisions.
read_if: Executing #923 after its design lands; touching internal/sandbox/msb/client.go's destination resolution, the captured-observation or unavailable-inspection contract tests, or the #923 clauses in specs/architecture/msb-seam.md.
---

# msb adapter decisions Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the `internal/sandbox/msb` adapter's microsandbox destination explicit with a `LocalContext` and its `Sandboxes()`/`Volumes()` collections, and record the captured-observation, error-class, enum, and unknown-value decisions as seam contracts with tests.

**Architecture:** Only item 1 is code: a `localContext` seam builds `msbSdk.LocalContext{Home: configpaths.MSBHome()}` per call, and the sandbox/volume function seams are rebound to closures over `Sandboxes()`/`Volumes()`. Items 2-5 are recorded decisions: short `msb-seam.md` clauses plus contract tests that pin snapshot status and unavailable-inspection zero projection. No interface, signature, or consumer changes.

**Tech Stack:** Go 1.27, module `github.com/inoio/agents-sandbox`, microsandbox SDK v0.8.3, golangci-lint.

**Spec:** `.operator-shared/plans/2026-11-27-msb-adapter-decisions-design.md`

## Global Constraints

- Base is `main` after #925 merged; #923 sits on top of the #924 projection reduction, which is already on `main`.
- `phase4_boundary_test.go` must stay green: no production (non-`_test.go`) package outside `internal/sandbox/msb` may import `msbSdk`.
- `roles_boundary_test.go` must stay green; do not touch the role/composite interfaces.
- No consumer call-site or signature churn: the `Client`/`SandboxHandle` interfaces and every launcher value type are unchanged.
- Destination resolution is **per call**, not memoized; the SDK resolves its configured context per operation.
- The destination is **local by design**: `NewLocalContext(Home)` ignores `ConfigPath` and never selects a remote provider.
- Unit tests must stay FFI-free: swap `localContext`/`newLocalContext` (or an existing seam) rather than resolving a real context.
- The sandbox/volume seam **signatures do not move**; only their default bodies change, so `swapSeam` call sites are untouched.
- Format with `make fmt` (never `go fmt`); lint with `make lint`; run `make check` before finalizing.
- The `Image.*` and `StorageUsage`/`PruneStorage` seams stay ambient: the SDK exposes no context collection for them.

## Review Focus

- **A unit test evaluates `configpaths.MSBHome()` without mock config paths** (the fail-fast `TestMain` panics). Every test that reaches `localContext()` must install `configpathstest.WithMockConfigPaths(t)` or swap `localContext`. Pinned by Task 1's destination test and the adjusted `TestRealMsbClientCreateVolumeError`.
- **A seam returns the destination-resolution error instead of panicking or nil-derefing** when `localContext()` fails. Pinned by Task 1's error-path assertion.
- **A captured accessor is documented as a snapshot but silently starts reading live** (or vice versa). Pinned by Task 2's capture test.
- **An inspection field marked `Unavailable`/`Redacted` projects as a misleading non-zero value.** Pinned by Task 4's zero-projection test.
- **The explicit-local change alters which destination the launcher uses** (remote selection, a different home). Pinned by Task 1's home assertion; verified on a KVM host in Task 5.

---

### Task 1: Adopt the explicit local destination (item 1)

**Files:**
- Modify: `internal/sandbox/msb/client.go` (the `getSandbox`/`createSandbox`/`listSandboxesWith`/`getVolume`/`createVolume`/`listVolumes`/`removeVolume` seam block; add the `configpaths` import)
- Create: `internal/sandbox/msb/client_test.go`
- Modify: `internal/sandbox/msb/msb_test.go` (`TestRealMsbClientCreateVolumeError`)

**Interfaces:**
- Produces: `var newLocalContext func(msbSdk.LocalOptions) (*msbSdk.LocalContext, error)`; `var localContext func() (*msbSdk.LocalContext, error)`; helpers `sandboxes() (*msbSdk.Sandboxes, error)` and `volumes() (*msbSdk.Volumes, error)`.
- Produces (unchanged signatures): `getSandbox func(context.Context, string) (*msbSdk.Sandbox, error)`, `createSandbox func(context.Context, string, ...msbSdk.SandboxOption) (*msbSdk.Sandbox, error)`, `listSandboxesWith func(context.Context, ...msbSdk.SandboxListOption) (*msbSdk.SandboxPage, error)`, `getVolume func(context.Context, string) (*msbSdk.Volume, error)`, `createVolume func(context.Context, string, ...msbSdk.VolumeOption) (*msbSdk.Volume, error)`, `listVolumes func(context.Context) ([]*msbSdk.Volume, error)`, `removeVolume func(context.Context, string) error`.

- [ ] **Step 1: Write the failing destination test**

Create `internal/sandbox/msb/client_test.go`:

```go
package msb

import (
	"context"
	"errors"
	"testing"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/configpaths/configpathstest"
)

// TestLocalContextNamesThePinnedHome pins that the adapter's destination is an
// explicit LocalContext carrying the launcher's private home, not the ambient
// MSB_HOME. The zero context the fake returns has no native handle, so the
// collection call fails with ErrInvalidConfig, which is also the proof that the
// default seam entered the context's collection rather than a package-level
// function.
func TestLocalContextNamesThePinnedHome(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	var got msbSdk.LocalOptions
	swapSeam(t, &newLocalContext, func(options msbSdk.LocalOptions) (*msbSdk.LocalContext, error) {
		got = options
		return &msbSdk.LocalContext{}, nil
	})

	if _, err := getSandbox(context.Background(), "example"); !msbSdk.IsKind(err, msbSdk.ErrInvalidConfig) {
		t.Fatalf("getSandbox() error = %v, want ErrInvalidConfig from the zero context", err)
	}
	if _, err := getVolume(context.Background(), "example"); !msbSdk.IsKind(err, msbSdk.ErrInvalidConfig) {
		t.Fatalf("getVolume() error = %v, want ErrInvalidConfig from the zero context", err)
	}
	if got.Home != configpaths.MSBHome() {
		t.Errorf("newLocalContext() Home = %q, want %q", got.Home, configpaths.MSBHome())
	}
}

// TestSandboxSeamsPropagateDestinationError pins that a failed destination
// resolution surfaces as the seam's error rather than panicking or dereferencing
// a nil context.
func TestSandboxSeamsPropagateDestinationError(t *testing.T) {
	cause := errors.New("no destination")
	swapSeam(t, &localContext, func() (*msbSdk.LocalContext, error) {
		return nil, cause
	})

	if _, err := getSandbox(context.Background(), "example"); !errors.Is(err, cause) {
		t.Errorf("getSandbox() error = %v, want %v", err, cause)
	}
	if _, err := getVolume(context.Background(), "example"); !errors.Is(err, cause) {
		t.Errorf("getVolume() error = %v, want %v", err, cause)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/sandbox/msb/ -run TestLocalContextNamesThePinnedHome`
Expected: build failure, `undefined: newLocalContext` (the seam does not exist yet).

- [ ] **Step 3: Implement the destination and rebind the seams**

In `internal/sandbox/msb/client.go`, add `"github.com/inoio/agents-sandbox/internal/configpaths"` to the imports, then replace the block of sandbox/volume SDK function vars with:

```go
// newLocalContext is the SDK's local-context constructor as a seam, so a test
// can assert the destination options without the native library.
var newLocalContext = msbSdk.NewLocalContext

// localContext resolves agents-sandbox's microsandbox destination, naming the
// private home explicitly rather than relying on the process-global MSB_HOME.
// The SDK resolves its configured context per operation, so this resolves per
// call too; it is a seam so a test can substitute a destination.
var localContext = func() (*msbSdk.LocalContext, error) {
	return newLocalContext(msbSdk.LocalOptions{Home: configpaths.MSBHome()})
}

func sandboxes() (*msbSdk.Sandboxes, error) {
	c, err := localContext()
	if err != nil {
		return nil, err
	}
	return c.Sandboxes(), nil
}

func volumes() (*msbSdk.Volumes, error) {
	c, err := localContext()
	if err != nil {
		return nil, err
	}
	return c.Volumes(), nil
}

// The sandbox and volume seams route through the explicit destination. They stay
// package-level function values so unit tests inject fakes without the native
// library.
var (
	getSandbox = func(ctx context.Context, name string) (*msbSdk.Sandbox, error) {
		s, err := sandboxes()
		if err != nil {
			return nil, err
		}
		return s.Get(ctx, name)
	}
	createSandbox = func(ctx context.Context, name string, opts ...msbSdk.SandboxOption) (*msbSdk.Sandbox, error) {
		s, err := sandboxes()
		if err != nil {
			return nil, err
		}
		return s.Create(ctx, name, opts...)
	}
	listSandboxesWith = func(ctx context.Context, opts ...msbSdk.SandboxListOption) (*msbSdk.SandboxPage, error) {
		s, err := sandboxes()
		if err != nil {
			return nil, err
		}
		return s.List(ctx, opts...)
	}
	getVolume = func(ctx context.Context, name string) (*msbSdk.Volume, error) {
		v, err := volumes()
		if err != nil {
			return nil, err
		}
		return v.Get(ctx, name)
	}
	createVolume = func(ctx context.Context, name string, opts ...msbSdk.VolumeOption) (*msbSdk.Volume, error) {
		v, err := volumes()
		if err != nil {
			return nil, err
		}
		return v.Create(ctx, name, opts...)
	}
	listVolumes = func(ctx context.Context) ([]*msbSdk.Volume, error) {
		v, err := volumes()
		if err != nil {
			return nil, err
		}
		return v.List(ctx)
	}
	removeVolume = func(ctx context.Context, name string) error {
		v, err := volumes()
		if err != nil {
			return err
		}
		return v.Remove(ctx, name)
	}
)
```

Leave the `imageGet`/`imageList`/… and `storageUsage`/`storagePrune` vars as the SDK functions (`msbSdk.Image.Get`, `msbSdk.StorageUsage`, …).

- [ ] **Step 4: Adjust the adapter test that reached the ambient path**

In `internal/sandbox/msb/msb_test.go`, `TestRealMsbClientCreateVolumeError` currently relies on `msbSdk.CreateVolume` failing at `ffi.ConfiguredContext()`. The default `createVolume` now resolves `localContext()` first, so swap it (FFI-free, no config-path mock needed):

```go
func TestRealMsbClientCreateVolumeError(t *testing.T) {
	swapSeam(t, &localContext, func() (*msbSdk.LocalContext, error) {
		return &msbSdk.LocalContext{}, nil
	})
	c := &realMsbClient{}
	if _, err := c.CreateVolume(context.Background(), "", VolumeSpec{}); err == nil {
		t.Error("CreateVolume() with empty name = nil error, want an error from the uninitialized destination")
	}
}
```

Audit the other `realMsbClient` tests that call a sandbox/volume method without swapping its seam and rely on the ambient call failing; give each the same `localContext` swap (or mock config paths).

- [ ] **Step 5: Run the tests and the lint**

Run: `go test ./internal/sandbox/msb/` → PASS.
Run: `make lint` → clean.

- [ ] **Step 6: Commit**

```bash
git add internal/sandbox/msb/client.go internal/sandbox/msb/client_test.go internal/sandbox/msb/msb_test.go
git commit -m "refactor(msb): resolve an explicit local destination for sandbox and volume operations"
```

---

### Task 2: Record the captured-observation contract (item 2)

**Files:**
- Modify: `internal/sandbox/msb/sandbox_test.go`
- Modify: `.operator-shared/specs/architecture/msb-seam.md` (after the "Launcher-owned types" list)
- Create: `changelog.d/<PR-number>.changed.md`

**Interfaces:**
- Consumes: `sandboxHandleFrom`, `realSandboxHandleInfo`, `realSandboxHandleConfigJSON`, `realSandboxHandle.Status()` from Task 1's package and the existing adapter.
- Produces: no code symbol; the seam contract.

- [ ] **Step 1: Write the failing capture test**

Append to `internal/sandbox/msb/sandbox_test.go`:

```go
// TestSandboxHandleStatusIsCaptured pins the captured-observation contract:
// Status() reports the observation taken when the handle was resolved, not a
// fresh lookup. A consumer that needs live state re-resolves the handle or uses
// the status-gated Connect.
func TestSandboxHandleStatusIsCaptured(t *testing.T) {
	status := msbSdk.SandboxStatusRunning
	swapSeam(t, &realSandboxHandleInfo, func(*msbSdk.Sandbox, context.Context) (*msbSdk.SandboxInfo, error) {
		return &msbSdk.SandboxInfo{Status: status}, nil
	})
	swapSeam(t, &realSandboxHandleConfigJSON, func(*msbSdk.Sandbox, context.Context) (string, error) {
		return `{"kind":"local"}`, nil
	})

	handle, err := sandboxHandleFrom(context.Background(), &msbSdk.Sandbox{})
	if err != nil {
		t.Fatalf("sandboxHandleFrom() error = %v", err)
	}
	status = msbSdk.SandboxStatusStopped
	if got := handle.Status(); got != SandboxStatusRunning {
		t.Errorf("Status() after the live status changed = %q, want the captured %q", got, SandboxStatusRunning)
	}
}
```

- [ ] **Step 2: Run it to verify it passes today**

Run: `go test ./internal/sandbox/msb/ -run TestSandboxHandleStatusIsCaptured`
Expected: PASS. The test pins existing behavior; it fails only if a later change makes the accessor live. (A pass here is the evidence the contract holds; proceed.)

- [ ] **Step 3: Record the clause in the seam spec**

In `.operator-shared/specs/architecture/msb-seam.md`, add after the "Launcher-owned types" list:

```markdown
**Captured observations.** A `SandboxHandle`'s `Status`, `Image`, and
`Config`/`ConfigMounts`/`ConfigPorts` are a point-in-time observation taken when the handle is
resolved and never refresh. A consumer that needs the live VM state re-resolves the handle (as
`vm.FindProjectVM` does) or uses the status-gated `Connect`, which reads live. A failed config
observation is retained on the handle, so a run degrades to "config unreadable" rather than failing
the lookup.
```

- [ ] **Step 4: Add the changelog fragment**

Create `changelog.d/<PR-number>.changed.md` (use the implementation PR's number) describing the internal destination change and the recorded seam contracts; it is not user-visible.

- [ ] **Step 5: Run the docs and brain gates**

Run: `make docs-linkcheck brain-check`
Expected: both report ok.

- [ ] **Step 6: Commit**

```bash
git add internal/sandbox/msb/sandbox_test.go .operator-shared/specs/architecture/msb-seam.md changelog.d/
git commit -m "docs(msb): state the captured-observation contract and pin it"
```

---

### Task 3: Record the error-class and enum decisions (items 3 and 4)

**Files:**
- Modify: `.operator-shared/specs/architecture/msb-seam.md`

**Interfaces:**
- Consumes: nothing.
- Produces: no code symbol; the seam contract.

- [ ] **Step 1: Add the error-class clause**

In `.operator-shared/specs/architecture/msb-seam.md`, add a subsection after "Captured observations":

```markdown
**Error classes.** The adapter's `Is*` predicates (`IsNotFound`, `IsVolumeNotFound`,
`IsVolumeAlreadyExists`, `IsSandboxAlreadyExists`, `IsReplaced`, `IsUnsupportedOperation`,
`IsJobNotRunning`) are the seam's named error classes over the SDK's typed `*Error{Kind}`; consumers
branch on them rather than on SDK kinds. `IsJobNotRunning` is the one class the SDK does not type: it
matches `*msbJobError.Code == "job_not_running"`, an upstream gap to revisit if the SDK gains a kind.
```

- [ ] **Step 2: Add the enum clause**

In "Launcher-owned types → Values out", append to the bullet:

```markdown
The lifecycle enums stay string-backed launcher types (`SandboxStatus` mirrors the SDK's string
values so `string(x)` and `==` keep working); `JobInfo.State` and `JobLogEntry.Source` surface the SDK
enum as a plain `string` because the launcher renames it, so no SDK enum crosses the seam.
```

- [ ] **Step 3: Run the brain gate**

Run: `make brain-check`
Expected: ok.

- [ ] **Step 4: Commit**

```bash
git add .operator-shared/specs/architecture/msb-seam.md
git commit -m "docs(msb): record the seam error-class and enum decisions"
```

---

### Task 4: Record the unavailable-inspection convention (item 5)

**Files:**
- Modify: `internal/sandbox/msb/translate_test.go` (`TestSandboxConfigFromUnavailableResourcesAreZero`)
- Modify: `.operator-shared/specs/architecture/msb-seam.md`

**Interfaces:**
- Consumes: `sandboxConfigFrom` (existing), `msbSdk.SandboxConfigInspection.Unavailable`/`Redacted` (SDK v0.8.3).
- Produces: no code symbol; the seam contract.

- [ ] **Step 1: Extend the zero-projection test with the unavailable/redacted lists**

Replace `TestSandboxConfigFromUnavailableResourcesAreZero` in `internal/sandbox/msb/translate_test.go` with:

```go
func TestSandboxConfigFromUnavailableResourcesAreZero(t *testing.T) {
	// The provider's Unavailable/Redacted path lists are informational: the
	// adapter projects the fields themselves, so an unavailable resource reads
	// as its zero value and consumers treat zero/nil as unknown.
	config := sandboxConfigFrom(&msbSdk.SandboxConfigInspection{
		Unavailable: []string{"spec.resources.cpus", "spec.resources.max_memory_mib"},
		Redacted:    []string{"spec.env"},
		Spec: msbSdk.SandboxInspectionSpec{
			Image: json.RawMessage(`{"Oci":{"reference":"img:1"}}`),
		},
	})
	if config == nil {
		t.Fatal("sandboxConfigFrom() = nil, want a config")
	}
	if config.CPUs != 0 || config.MaxCPUs != 0 || config.MaxMemoryMiB != 0 {
		t.Errorf("sandboxConfigFrom() = %+v, want zero for unavailable resources", config)
	}
	if config.RootDisk != nil {
		t.Errorf("RootDisk = %+v, want nil when the source records none", config.RootDisk)
	}
}
```

- [ ] **Step 2: Run it to verify it passes today**

Run: `go test ./internal/sandbox/msb/ -run TestSandboxConfigFromUnavailableResourcesAreZero`
Expected: PASS (the adapter already ignores the lists; the test records that the ignored lists still yield zero).

- [ ] **Step 3: Add the unknown-value clause**

In `.operator-shared/specs/architecture/msb-seam.md`, add after the error-class clause:

```markdown
**Unknown values.** An inspection field the provider marks `Unavailable`/`Redacted`, or that the
adapter cannot decode, projects as the launcher zero value / nil; consumers treat that as "unknown"
(`cfg.Image != ""`, `cfg.MaxMemoryMiB > 0`, `cfg.RootDisk != nil`). The `Unavailable`/`Redacted`
lists themselves are not surfaced.
```

- [ ] **Step 4: Run the tests and the brain gate**

Run: `go test ./internal/sandbox/msb/` → PASS.
Run: `make brain-check` → ok.

- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/msb/translate_test.go .operator-shared/specs/architecture/msb-seam.md
git commit -m "docs(msb): record the unknown-value seam convention and pin it"
```

---

### Task 5: Fold the decisions into the integration guide and run the full gate

**Files:**
- Modify: `.operator-shared/guides/msb-sdk-integration.md` (§2.1, §2.2, §2.4, §2.5, §4)

**Interfaces:**
- Consumes: the decisions recorded in Tasks 1-4.
- Produces: nothing; the guide's assessment.

- [ ] **Step 1: Update the guide's §2.1, §2.2, §2.4, §2.5**

In `.operator-shared/guides/msb-sdk-integration.md`:
- §2.1 "Explicit contexts and collections are still bypassed" → state that the adapter now resolves a `LocalContext{Home}` per operation and routes sandbox/volume operations through `Sandboxes()`/`Volumes()`; the package-level functions remain only for the context-less image and storage surfaces.
- §2.2 "The captured-observation trade-off" → state the decision: the snapshot stays and the contract is documented in `msb-seam.md` (live read via the status-gated `Connect`); no `LiveStatus`/`LiveConfig` seam is added.
- §2.4 "Typed errors are still not folded" → state that the predicates stay as the seam's error vocabulary; `IsJobNotRunning`'s string match is recorded as an upstream gap.
- §2.5 "New-line-only items not taken" → state the enum decision (launcher string enums and plain-string job state/source) and the inspection decision (unavailable/redacted read as zero/nil; the lists are not surfaced).
- §4 "State and next steps" → drop the #923 items now decided.

- [ ] **Step 2: Run the full gate**

Run: `make check`
Expected: every target reports PASS.

- [ ] **Step 3: Verify destination equivalence on a host (KVM)**

Run the `msb`-tagged suite on a KVM host: `make test-msb` (or one test via `CGO_ENABLED=1 go test -tags msb ./internal/sandbox/msb/`).
Expected: PASS — the explicit-local destination resolves the same private home the ambient path did, and sandbox/volume success paths route through `Sandboxes()`/`Volumes()`.

- [ ] **Step 4: Commit**

```bash
git add .operator-shared/guides/msb-sdk-integration.md
git commit -m "docs(msb): fold the #923 decisions into the SDK integration guide"
```

---

## Out of scope

- Any SDK capability work (warm start, pause/resume, metrics streaming, filesystem widening, create knobs, SSH/agent) — issues #915-#922.
- A live `Status`/`Config` accessor, launcher error sentinels, new launcher enum types, or surfacing the inspection's `Unavailable`/`Redacted` lists — the design rejected or deferred each.
- Removing `MSB_HOME`, or adopting a context collection for images/storage (the SDK exposes none).
