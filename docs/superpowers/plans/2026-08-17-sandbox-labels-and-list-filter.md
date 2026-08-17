# Sandbox Labels & List Filter Flags Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Tag the project sandbox VM with project+image labels at create and add `--label`, `--limit`, `--running`, `--stopped`, `-q`/names-only, and `--format json` filter/format flags to `sandbox list`, renaming the global `-q/--quiet` flag to `--error` to free `-q`.

**Architecture:** Labels are attached to the project VM as `msbSdk.SandboxOption`s at the single create site in `ensureProjectVM`. `msb.Client.ListSandboxes` gains a label filter passed to the SDK's `WithListLabels`; `session.ListSandboxes` layers the VmPrefix name filter, running/stopped status filter, and a local `--limit` truncation on top. The CLI wires the new flags and a JSON renderer. The global `-q/--quiet` flag is renamed to `--error` (same "only errors" behavior) so `-q` means names-only on `list`.

**Tech Stack:** Go, cobra, microsandbox sdk/go@v0.6.9, testmock (MockMsbClient).

**Spec:** `docs/superpowers/specs/sandbox-labels-and-list-filter.md`

## Global Constraints

- Label key prefix is `org.opencode-sandbox.` (matches `image.OpenCodeVersionLabel`).
- Labels set on the **project VM only**; the transient volume prefill/copy/edit helper sandboxes are **not** labeled. There is **no worktree label**.
- `--running` + `--stopped` together → `--running` wins (no error).
- `--limit` applies to the **final** filtered result (local truncation). `WithListLimit` is not used in the msb wrapper.
- `--format json` emits a top-level JSON array; timestamps marshal as Go `time.Time` (RFC3339 w/ nanos). `-q` and `--format json` are mutually exclusive (error if both); unknown `--format` values error.
- `--label` values must parse as `KEY=VALUE`; a missing `=` is a usage error. Repeatable, AND-matched.
- Global flag rename: `-q/--quiet` → `--error` (no shorthand), same "only show errors" behavior. Config key `quiet` → `error`; env `OPENCODE_SANDBOX_QUIET` → `OPENCODE_SANDBOX_ERROR`.
- TDD: write the failing test first, verify it fails, implement, verify it passes, commit. Use `make fmt`/`golangci-lint fmt`, `make lint`/`golangci-lint run`, `make test`/`go test ./...`. Run the linter after every major edit.

---

### Task 1: Rename global `-q/--quiet` → `--error`

**Files:**
- Modify: `cmd/opencode-sandbox/constants.go:8`
- Modify: `cmd/opencode-sandbox/commands.go:117`
- Modify: `internal/viperconfig/viperconfig.go` (`Config` struct, `configFlagKeys`, `configEnvKeys`, `flagTypedDefault`, `Resolver.Quiet`)
- Modify: `cmd/opencode-sandbox/cli.go:50`
- Test: `internal/viperconfig/viperconfig_test.go:27`

**Interfaces:**
- Consumes: nothing (first task).
- Produces: `Resolver.Error() bool` (replaces `Resolver.Quiet()`); root persistent flag `--error` (no shorthand). Later tasks rely on `-q` being free for `list`.

- [ ] **Step 1: Write the failing tests**

Update the existing assertions that reference `--quiet`/`-q` on the root command so they expect the renamed flag:

- `internal/viperconfig/viperconfig_test.go:27` — replace `r.Quiet()` with `r.Error()`:

```go
if !r.Yes() || !r.Verbose() || r.Error() {
    t.Error("UI getters mismatch")
}
```

- `cmd/opencode-sandbox/cli_tree_test.go:132` — in `TestPrintTreeBoolFlagsHaveNoValuePlaceholders`, replace `"--quiet <QUIET>"` with `"--error <ERROR>"`.
- `cmd/opencode-sandbox/cli_tree_test.go:152` — in `TestPrintTreeFlagShortcuts`, replace `"-q, --quiet"` with `"--error"` (the flag now has no shorthand).
- `cmd/opencode-sandbox/cli_help_test.go:51` — in `TestRootHelpListsGlobalFlags`, replace `"--quiet"` with `"--error"`.
- `cmd/opencode-sandbox/cli_test.go:21` — in `TestRootHasGlobalFlags`, replace `"quiet"` with `"error"` in the `flags` slice.
- `cmd/opencode-sandbox/cli_test.go:54` — in `TestRunCommandFlagShortcuts`, **remove** the `"q": "quiet"` entry from the `shortcuts` map (after the rename, `-q` no longer exists on the root/run commands; it becomes a local flag on `list`, tested in Task 6).

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/viperconfig/ -run TestResolverGettersReturnConfig -count=1`
Expected: FAIL — `r.Error` undefined.

Run: `go test ./cmd/opencode-sandbox/ -run 'TestPrintTree|TestRootHelpListsGlobalFlags' -count=1`
Expected: FAIL — `--error` not present in tree/help output (still `--quiet`).

- [ ] **Step 3: Rename the config field and resolver accessor**

In `internal/viperconfig/viperconfig.go`:

```go
// in Config struct:
Error bool `mapstructure:"error"`
```

Change `configFlagKeys` and `configEnvKeys`: replace `"quiet"` with `"error"`. Change `flagTypedDefault` case `"quiet"` → `"error"`. Rename the accessor:

```go
func (r *Resolver) Error() bool { return r.cfg.Error }
```

- [ ] **Step 4: Update `cli.go` and root flags**

In `cmd/opencode-sandbox/constants.go`, replace `pFlagQuiet = "quiet"` with `pFlagError = "error"`.

In `cmd/opencode-sandbox/commands.go` (`buildMinimalRootFlagsCmd`), replace:

```go
rootFlagsCmd.PersistentFlags().BoolP(pFlagQuiet, pFlagQuiet[:1], false, "Suppress non-error output")
```

with:

```go
rootFlagsCmd.PersistentFlags().BoolP(pFlagError, "", false, "Only show error output")
```

In `cmd/opencode-sandbox/cli.go`, replace `quiet := r.Quiet()` with `quiet := r.Error()` (the rest of `applyCLISettings`/`levelFrom` is unchanged). Update the comment on `cli.go:41` from `--verbose/--quiet/--yes` to `--verbose/--error/--yes`.

- [ ] **Step 5: Run tests and linter**

Run: `go test ./internal/viperconfig/ ./cmd/opencode-sandbox/ -count=1`
Expected: PASS. Then `golangci-lint run ./internal/viperconfig/ ./cmd/opencode-sandbox/` — clean.

- [ ] **Step 6: Commit**

```bash
git add cmd/opencode-sandbox/constants.go cmd/opencode-sandbox/commands.go cmd/opencode-sandbox/cli.go cmd/opencode-sandbox/cli_tree_test.go cmd/opencode-sandbox/cli_help_test.go cmd/opencode-sandbox/cli_test.go internal/viperconfig/viperconfig.go internal/viperconfig/viperconfig_test.go
git commit -m "feat(cli): rename global -q/--quiet to --error, freeing -q"
```

---

### Task 2: Define label constants

**Files:**
- Create: `internal/sandbox/naming/labels.go`
- Test: `internal/sandbox/naming/labels_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `naming.LabelProject = "org.opencode-sandbox.project"` and `naming.LabelImage = "org.opencode-sandbox.image"`. Used by Task 3 (create) and the label-filter tests.

- [ ] **Step 1: Write the failing test**

Create `internal/sandbox/naming/labels_test.go`:

```go
package naming

import "testing"

func TestLabelConstants(t *testing.T) {
	if LabelProject != "org.opencode-sandbox.project" {
		t.Errorf("LabelProject = %q, want org.opencode-sandbox.project", LabelProject)
	}
	if LabelImage != "org.opencode-sandbox.image" {
		t.Errorf("LabelImage = %q, want org.opencode-sandbox.image", LabelImage)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/sandbox/naming/ -run TestLabelConstants -count=1`
Expected: FAIL — `LabelProject` undefined.

- [ ] **Step 3: Create `labels.go`**

Create `internal/sandbox/naming/labels.go`:

```go
package naming

// Labels the launcher attaches to the project sandbox VM at creation. The
// org.opencode-sandbox. prefix matches image.OpenCodeVersionLabel.
const (
	// LabelProject identifies the project the sandbox belongs to.
	LabelProject = "org.opencode-sandbox.project"
	// LabelImage records the runner image reference the sandbox was created with.
	LabelImage = "org.opencode-sandbox.image"
)
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/sandbox/naming/ -run TestLabelConstants -count=1`
Expected: PASS.

- [ ] **Step 5: Lint and commit**

Run: `golangci-lint run ./internal/sandbox/naming/` — clean.

```bash
git add internal/sandbox/naming/labels.go internal/sandbox/naming/labels_test.go
git commit -m "feat(naming): define sandbox project and image label constants"
```

---

### Task 3: Attach labels at project VM creation

**Files:**
- Modify: `internal/sandbox/session/vm_lifecycle.go` (`ensureProjectVM`, optsList near line 273)
- Test: `internal/sandbox/session/vm_test.go`

**Interfaces:**
- Consumes: `naming.LabelProject`, `naming.LabelImage` (Task 2); `slug` and `imageRef` already in scope in `ensureProjectVM`; `msbSdk.WithLabels` from sdk.
- Produces: project VMs created with `org.opencode-sandbox.project` and `org.opencode-sandbox.image` labels. Consumed by Task 4's label filter.

- [ ] **Step 1: Write the failing test**

In `internal/sandbox/session/vm_test.go`, add a test modeled on the existing `TestEnsureProjectVM_CreatePath` (vm_test.go:167), which uses `msb.WithMsbMock`, `configpaths.WithMockConfigPaths`, `testutil.InitRepo` + `t.Chdir`, and drives `ensureProjectVM` directly. Assert the labels by applying the recorded opts to a `SandboxConfig`:

```go
func TestEnsureProjectVMAppliesLabels(t *testing.T) {
	testUI := termio.NewTestMock(t)
	ui := &testUI

	client := &msb.MockMsbClient{}
	client.GetSandboxFn = func(_ context.Context, _ string) (msb.SandboxHandle, error) {
		return nil, &msbSdk.Error{Kind: msbSdk.ErrSandboxNotFound, Message: "not found"}
	}
	client.CreateSandboxFn = func(_ context.Context, _ string, opts ...msbSdk.SandboxOption) (msb.Sandbox, error) {
		cfg := msbSdk.SandboxConfig{}
		for _, opt := range opts {
			opt(&cfg)
		}
		if cfg.Labels[naming.LabelProject] == "" {
			t.Errorf("expected project label set, got %v", cfg.Labels)
		}
		if cfg.Labels[naming.LabelImage] == "" {
			t.Errorf("expected image label set, got %v", cfg.Labels)
		}
		return msb.NewMockSandbox(msb.SandboxOpts{}), nil
	}
	msb.WithMsbMock(t, client)
	configpaths.WithMockConfigPaths(t)

	tmpRepo := testutil.InitRepo(t)
	t.Chdir(tmpRepo)

	sb, created, err := ensureProjectVM(
		context.Background(),
		options.RunOptions{Memory: "1G", TmpSize: "512M"},
		"opencode-sandbox/runner-test:latest",
		"test-home-vol",
		tmpRepo,
		nil,
		ui,
	)
	if err != nil {
		t.Fatalf("ensureProjectVM (create): %v", err)
	}
	if !created || sb == nil {
		t.Fatal("expected created sandbox")
	}
}
```

Imports needed in `vm_test.go`: `naming`, and `msbSdk`/`msb`/`testutil`/`configpaths`/`termio`/`options` are already imported by the neighboring tests.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/sandbox/session/ -run TestEnsureProjectVMAppliesLabels -count=1`
Expected: FAIL — labels not set (cfg.Labels empty).

- [ ] **Step 3: Add the labels to `optsList`**

In `internal/sandbox/session/vm_lifecycle.go`, in `ensureProjectVM`, add to `optsList`:

```go
msbSdk.WithLabels(map[string]string{
    naming.LabelProject: slug,
    naming.LabelImage:   imageRef,
}),
```

Add `"gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/naming"` to the imports if not already present.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/sandbox/session/ -run TestEnsureProjectVMAppliesLabels -count=1`
Expected: PASS. Then run the full session package: `go test ./internal/sandbox/session/ -count=1` — PASS.

- [ ] **Step 5: Lint and commit**

Run: `golangci-lint run ./internal/sandbox/session/` — clean.

```bash
git add internal/sandbox/session/vm_lifecycle.go internal/sandbox/session/vm_test.go
git commit -m "feat(session): attach project and image labels at VM creation"
```

---

### Task 4: Extend `msb.ListSandboxes` with a label filter

**Files:**
- Modify: `internal/sandbox/msb/msb.go:29` (interface), `:177-200` (real impl)
- Modify: `internal/sandbox/msb/testmock.go:37`, `:142-151`
- Modify: `internal/sandbox/session/list.go:44`
- Modify: `internal/sandbox/pruning/catalog.go:51`
- Modify: `internal/sandbox/volume/operations.go:23`
- Test: `internal/sandbox/msb/msb_test.go` (new test)

**Interfaces:**
- Consumes: `msbSdk.WithListLabels`, `msbSdk.ListSandboxesWith`.
- Produces: `msb.Client.ListSandboxes(ctx context.Context, labels map[string]string) ([]SandboxHandle, error)` and `MockMsbClient.ListSandboxes(ctx, labels)`. Consumed by Task 5.

- [ ] **Step 1: Write the failing test**

Add `internal/sandbox/msb/msb_test.go` with a test that `MockMsbClient.ListSandboxes` records the labels passed and returns them for assertion. Since `realMsbClient` can't run against the SDK in tests, test the contract via the mock's recording:

```go
package msb

import (
	"context"
	"testing"
)

func TestMockListSandboxesRecordsLabels(t *testing.T) {
	m := &MockMsbClient{}
	m.ListSandboxesFn = func(ctx context.Context, labels map[string]string) ([]SandboxHandle, error) {
		if labels["k"] != "v" {
			t.Errorf("expected label k=v, got %v", labels)
		}
		return nil, nil
	}
	_, err := m.ListSandboxes(context.Background(), map[string]string{"k": "v"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/sandbox/msb/ -run TestMockListSandboxesRecordsLabels -count=1`
Expected: FAIL — `ListSandboxes` signature mismatch (`ListSandboxesFn` type error).

- [ ] **Step 3: Update the interface and real impl**

In `internal/sandbox/msb/msb.go`:

```go
// interface:
ListSandboxes(ctx context.Context, labels map[string]string) ([]SandboxHandle, error)
```

```go
func (realMsbClient) ListSandboxes(ctx context.Context, labels map[string]string) ([]SandboxHandle, error) {
	var result []SandboxHandle
	var cursor *string
	for {
		var page *msbSdk.SandboxPage
		var err error
		opts := []msbSdk.SandboxListOption{}
		if len(labels) > 0 {
			opts = append(opts, msbSdk.WithListLabels(labels))
		}
		if cursor != nil {
			opts = append(opts, msbSdk.WithListCursor(*cursor))
		}
		page, err = msbSdk.ListSandboxesWith(ctx, opts...)
		if err != nil {
			return nil, err
		}
		for _, h := range page.Sandboxes {
			result = append(result, &realSandboxHandle{handle: h})
		}
		cursor = page.NextCursor
		if cursor == nil {
			break
		}
	}
	return result, nil
}
```

Note this changes the cursor logic to always call `ListSandboxesWith` (the original used `ListSandboxes` for the first page). This is fine — `ListSandboxesWith` with no options is equivalent.

- [ ] **Step 4: Update the mock**

In `internal/sandbox/msb/testmock.go`, change the field:

```go
ListSandboxesFn   func(ctx context.Context, labels map[string]string) ([]SandboxHandle, error)
```

and the method:

```go
func (m *MockMsbClient) ListSandboxes(ctx context.Context, labels map[string]string) ([]SandboxHandle, error) {
	if m.ListSandboxesFn != nil {
		return m.ListSandboxesFn(ctx, labels)
	}
	if m.ListSandboxesErr != nil {
		return nil, m.ListSandboxesErr
	}
	return m.Sandboxes, nil
}
```

- [ ] **Step 5: Update the three callers to pass nil**

In `internal/sandbox/session/list.go:44`:

```go
handles, err := msb.Get().ListSandboxes(ctx, nil)
```

In `internal/sandbox/pruning/catalog.go:51`:

```go
sandboxHandles, err := client.ListSandboxes(ctx, nil)
```

In `internal/sandbox/volume/operations.go:23`:

```go
sandboxes, err := client.ListSandboxes(ctx, nil)
```

- [ ] **Step 6: Fix the CLI list test mock setup**

In `cmd/opencode-sandbox/cli_list_subcommand_test.go`, the `MockMsbClient` uses the `Sandboxes` collection (not `ListSandboxesFn`), so it works without change. Verify `go build ./...` compiles.

- [ ] **Step 7: Run tests and linter**

Run: `go test ./... -count=1`
Expected: PASS. Then `golangci-lint run ./...` — clean.

- [ ] **Step 8: Commit**

```bash
git add internal/sandbox/msb/msb.go internal/sandbox/msb/testmock.go internal/sandbox/msb/msb_test.go internal/sandbox/session/list.go internal/sandbox/pruning/catalog.go internal/sandbox/volume/operations.go
git commit -m "feat(msb): add label filter to ListSandboxes"
```

---

### Task 5: Extend `session.ListSandboxes` with `ListOption` filters

**Files:**
- Modify: `internal/sandbox/session/list.go`
- Test: `internal/sandbox/session/list_test.go`

**Interfaces:**
- Consumes: `msb.Get().ListSandboxes(ctx, labels)` (Task 4), `msb.GetVMStatus`, `msb.IsSandboxActive`, `naming.VmPrefix`.
- Produces: `session.ListSandboxes(ctx context.Context, opts ...session.ListOption) ([]Info, error)` where

  ```go
  type ListOption struct {
      Labels      map[string]string
      Limit       *uint32
      RunningOnly bool
      StoppedOnly bool
  }
  ```

  and `Info` gains `Labels map[string]string`, `CreatedAtRaw time.Time`, `UpdatedAtRaw time.Time`. Consumed by Task 6.

- [ ] **Step 1: Write the failing test**

In `internal/sandbox/session/list_test.go`, add tests modeled on the existing `TestListSandboxesPopulatesInfo` (uses `msb.WithMsbMock`, a `Sandboxes` collection of `MockSandboxHandle`s). The mock's `ListSandboxes` returns `m.Sandboxes` regardless of labels, so label-forwarding is asserted via `ListSandboxesFn` capturing the argument. Example:

```go
func TestListSandboxesRunningOnly(t *testing.T) {
	mock := &msb.MockMsbClient{}
	mock.Sandboxes = []msb.SandboxHandle{
		&msb.MockSandboxHandle{Name_: "opencode-sandbox-vm-a", Status_: msbSdk.SandboxStatusRunning},
		&msb.MockSandboxHandle{Name_: "opencode-sandbox-vm-b", Status_: msbSdk.SandboxStatusStopped},
		&msb.MockSandboxHandle{Name_: "other-vm-c", Status_: msbSdk.SandboxStatusRunning},
	}
	msb.WithMsbMock(t, mock)

	infos, err := ListSandboxes(context.Background(), ListOption{RunningOnly: true})
	if err != nil {
		t.Fatalf("ListSandboxes: %v", err)
	}
	if len(infos) != 1 || infos[0].Name != "opencode-sandbox-vm-a" {
		t.Errorf("expected only running project VM, got %+v", infos)
	}
}
```

Add analogous tests:
- `TestListSandboxesStoppedOnly` — only stopped project VMs.
- `TestListSandboxesRunningWinsWhenBoth` — `ListOption{RunningOnly: true, StoppedOnly: true}` → running only.
- `TestListSandboxesLimit` — set the mock's `Sandboxes` with 2 running project VMs, `ListOption{Limit: intPtr(1)}` → 1 result. Add a small helper `func intPtr(n uint32) *uint32 { return &n }` in `list_test.go`.
- `TestListSandboxesLabelsForwarded` — set `mock.ListSandboxesFn = func(_ context.Context, labels map[string]string) ([]msb.SandboxHandle, error) { captured = labels; return mock.Sandboxes, nil }`, call with `ListOption{Labels: map[string]string{"k": "v"}}`, assert `captured["k"] == "v"`.
- `TestListSandboxesPopulatesInfoFields` — one running project VM handle with `Cfg: &msbSdk.SandboxConfig{Image: "...", Labels: map[string]string{naming.LabelProject: "proj"}}` and `CreatedAt_`/`UpdatedAt_` set; assert `info.Labels[naming.LabelProject]`, `info.CreatedAtRaw`, `info.UpdatedAtRaw` are populated.

Imports: `naming` must be added to `list_test.go`.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/sandbox/session/ -run 'TestListSandboxes' -count=1`
Expected: FAIL — `ListOption` undefined, new `Info` fields absent, and the existing `ListSandboxes(ctx)` signature mismatch.

- [ ] **Step 3: Add `ListOption` and update `Info`**

In `internal/sandbox/session/list.go`:

```go
// ListOption carries optional filters/format controls for ListSandboxes.
type ListOption struct {
	Labels      map[string]string
	Limit       *uint32
	RunningOnly bool
	StoppedOnly bool
}
```

Extend `Info`:

```go
type Info struct {
	Name          string
	Status        string
	Image         string
	CreatedAt     string
	Labels        map[string]string
	CreatedAtRaw  time.Time
	UpdatedAtRaw  time.Time
}
```

- [ ] **Step 4: Rewrite `ListSandboxes` with the filter pipeline**

```go
func ListSandboxes(ctx context.Context, opts ...ListOption) ([]Info, error) {
	opt := ListOption{}
	for _, o := range opts {
		if o.Labels != nil {
			opt.Labels = o.Labels
		}
		if o.Limit != nil {
			opt.Limit = o.Limit
		}
		opt.RunningOnly = opt.RunningOnly || o.RunningOnly
		opt.StoppedOnly = opt.StoppedOnly || o.StoppedOnly
	}

	handles, err := msb.Get().ListSandboxes(ctx, opt.Labels)
	if err != nil {
		return nil, fmt.Errorf("list sandboxes: %w", err)
	}

	var result []Info
	for _, h := range handles {
		name := h.Name()
		if !strings.HasPrefix(name, naming.VmPrefix) {
			continue
		}
		status := h.Status()
		if opt.RunningOnly || opt.StoppedOnly {
			active := msb.IsSandboxActive(status)
			if opt.RunningOnly && !active {
				continue
			}
			if opt.StoppedOnly && active {
				continue
			}
		}
		cfg, _ := h.Config()
		var labels map[string]string
		if cfg != nil {
			labels = cfg.Labels
		}
		result = append(result, Info{
			Name:         name,
			Status:       string(status),
			Image:        h.Image(),
			CreatedAt:    FormatTime(h.CreatedAt()),
			Labels:       labels,
			CreatedAtRaw: h.CreatedAt(),
			UpdatedAtRaw: h.UpdatedAt(),
		})
	}
	if opt.Limit != nil && uint32(len(result)) > *opt.Limit {
		result = result[:*opt.Limit]
	}
	return result, nil
}
```

Note: `--running` wins over `--stopped` because when `RunningOnly` is true the `StoppedOnly` branch never filters (only the `RunningOnly` branch runs). This matches "running wins".

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/sandbox/session/ -run 'TestListSandboxes' -count=1`
Expected: PASS. Then the full session package: `go test ./internal/sandbox/session/ -count=1` — PASS.

- [ ] **Step 6: Lint and commit**

Run: `golangci-lint run ./internal/sandbox/session/` — clean.

```bash
git add internal/sandbox/session/list.go internal/sandbox/session/list_test.go
git commit -m "feat(session): support label, running/stopped, and limit filters in ListSandboxes"
```

---

### Task 6: CLI flags on `sandbox list`

**Files:**
- Modify: `cmd/opencode-sandbox/commands_system.go` (`buildListCmd`)
- Modify: `cmd/opencode-sandbox/constants.go` (add flag names)
- Test: `cmd/opencode-sandbox/cli_list_subcommand_test.go`

**Interfaces:**
- Consumes: `session.ListSandboxes(ctx, session.ListOption{...})` (Task 5), `session.Info` (Labels, CreatedAtRaw, UpdatedAtRaw), `termio.UI`.
- Produces: the `sandbox list` subcommand with `-q/--quiet`, `--label`, `--limit`, `--running`, `--stopped`, `--format` flags. Terminal task.

- [ ] **Step 1: Write the failing CLI tests**

In `cmd/opencode-sandbox/cli_list_subcommand_test.go`, add test cases via the existing `runListCmdTest` helper, plus **new dedicated test functions** where the helper cannot express the assertion (it asserts presence of strings, not absence or JSON shape):

- `TestListSandboxesLabelFilter` — dedicated function (not `runListCmdTest`): the mock's default `ListSandboxes` ignores labels (returns `m.Sandboxes`), so to simulate the server-side `WithListLabels` filter, set `mock.ListSandboxesFn` to filter `m.Sandboxes` by the requested labels. Two running project-VM handles, one with `Cfg.Labels[naming.LabelProject]` matching and one not; run `list --label org.opencode-sandbox.project=<slug>`; assert only the matching handle's row appears.
- `TestListSandboxesLabelInvalid` — a dedicated function: run `list --label missing-equals`, expect an error.
- `TestListSandboxesLimit` — mock with 2 running project VMs; run `list --limit 1`; assert only one row (use `runListCmdTest`).
- `TestListSandboxesRunningOnly` / `TestListSandboxesStoppedOnly` / `TestListSandboxesBothFlagsRunningWins` — dedicated functions: mock with one running and one stopped project VM; assert the kept row is present and the filtered-out row is absent (`!containsNormalized(ui.OutCalls, <filtered-out-row>)`). Both flags → running only (stopped row absent).
- `TestListSandboxesQuietNames` — dedicated function: run `list -q` with running project VMs; assert each `ui.OutCalls` entry is exactly a sandbox name and that the `NAME IMAGE STATUS CREATED` header is absent (check `!containsNormalized(ui.OutCalls, "NAME IMAGE STATUS CREATED")`).
- `TestListSandboxesFormatJSON` — dedicated function: run `list --format json`; join `ui.OutCalls` into one string, `json.Unmarshal` into `[]struct{ Name, Status, Image string; Labels map[string]string; Created, Updated time.Time }`; assert the documented fields and that `Created`/`Updated` unmarshal as Go times.
- `TestListSandboxesFormatJSONAndQuietConflict` — run `list -q --format json`, expect an error.
- `TestListSandboxesFormatUnknown` — run `list --format yaml`, expect an error.
- `TestListSandboxesDefaultUnchanged` — existing default tests pass (no flags → column table).

The mock handles must set `Cfg: &msbSdk.SandboxConfig{Image: ..., Labels: ...}` for the label and image columns, and `CreatedAt_`/`UpdatedAt_` for JSON times. Import `naming` and `encoding/json` in the test file.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/opencode-sandbox/ -run 'TestListSandboxes' -count=1`
Expected: FAIL — new flags not defined (cobra "unknown flag"), JSON not produced.

- [ ] **Step 3: Add flag name constants**

In `cmd/opencode-sandbox/constants.go`, add to the `const` block:

```go
flagLabel     = "label"
flagLimit     = "limit"
flagRunning   = "running"
flagStopped   = "stopped"
flagFormat    = "format"
```

`-q/--quiet` reuses the existing `pFlagQuiet = "quiet"` string constant name — but `pFlagQuiet` no longer exists (renamed to `pFlagError` in Task 1). Re-add `pFlagQuiet = "quiet"` as a new constant (it is now the local `list` flag, not the global one). Do **not** change its value `"quiet"`.

- [ ] **Step 4: Rewrite `buildListCmd`**

In `cmd/opencode-sandbox/commands_system.go`, replace `buildListCmd`:

```go
func buildListCmd(ui termio.UI) *cobra.Command {
	var (
		labelsStr  []string
		limit      uint32
		running    bool
		stopped    bool
		namesOnly  bool
		format     string
	)
	cmd := &cobra.Command{
		Use:     cmdList,
		Aliases: cmdListAliases,
		Args:    cobra.NoArgs,
		Short:   "List sandboxes for this host",
		Annotations: map[string]string{
			annotationAlsoAs: "sandbox list",
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			if namesOnly && format != "" {
				return fmt.Errorf("--quiet and --format are mutually exclusive")
			}
			if format != "" && format != "json" {
				return fmt.Errorf("unsupported format %q: only \"json\" is supported", format)
			}
			labels, err := parseLabelFlags(labelsStr)
			if err != nil {
				return err
			}
			var lim *uint32
			if cmd.Flags().Changed(flagLimit) {
				lim = &limit
			}
			opt := session.ListOption{
				Labels:      labels,
				Limit:       lim,
				RunningOnly: running,
				StoppedOnly: stopped,
			}
			sandboxes, err := session.ListSandboxes(cmd.Context(), opt)
			if err != nil {
				return err
			}
			if namesOnly {
				for _, s := range sandboxes {
					ui.Out(s.Name)
				}
				return nil
			}
			if format == "json" {
				return printSandboxesJSON(ui, sandboxes)
			}
			printItems(sandboxes, "No sandboxes found.", sandboxListHeaders(), ui,
				func(s session.Info) string { return s.Name },
				func(s session.Info) string { return s.Image },
				func(s session.Info) string { return termio.StyleStatus(s.Status) },
				func(s session.Info) string { return s.CreatedAt },
			)
			return nil
		},
	}
	cmd.Flags().BoolP(pFlagQuiet, pFlagQuiet[:1], false, "Print only sandbox names")
	cmd.Flags().StringArray(flagLabel, nil, "Only show sandboxes carrying this label KEY=VALUE (repeatable, all must match)")
	cmd.Flags().Uint32(flagLimit, 0, "Limit the number of sandboxes shown")
	cmd.Flags().Bool(flagRunning, false, "Show only running sandboxes")
	cmd.Flags().Bool(flagStopped, false, "Show only stopped sandboxes")
	cmd.Flags().String(flagFormat, "", "Output format (json)")
	return cmd
}
```

Add two helpers in `commands_system.go` (or `commands.go`):

```go
func parseLabelFlags(values []string) (map[string]string, error) {
	labels := make(map[string]string, len(values))
	for _, v := range values {
		key, val, ok := strings.Cut(v, "=")
		if !ok || key == "" {
			return nil, fmt.Errorf("invalid label %q: must be KEY=VALUE", v)
		}
		labels[key] = val
	}
	return labels, nil
}

type jsonSandbox struct {
	Name    string            `json:"name"`
	Status  string            `json:"status"`
	Image   string            `json:"image"`
	Created time.Time         `json:"created"`
	Updated time.Time         `json:"updated"`
	Labels  map[string]string `json:"labels"`
}

func printSandboxesJSON(ui termio.UI, infos []session.Info) error {
	out := make([]jsonSandbox, 0, len(infos))
	for _, s := range infos {
		out = append(out, jsonSandbox{
			Name:    s.Name,
			Status:  s.Status,
			Image:   s.Image,
			Created: s.CreatedAtRaw,
			Updated: s.UpdatedAtRaw,
			Labels:  s.Labels,
		})
	}
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	ui.Out(string(data))
	return nil
}
```

Add the import `encoding/json` to `commands_system.go`. (`fmt`, `strings`, `time`, and `session` are already imported by that file.)

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./cmd/opencode-sandbox/ -run 'TestListSandboxes' -count=1`
Expected: PASS. Then `go test ./cmd/opencode-sandbox/ -count=1` — PASS (existing list tests unchanged).

- [ ] **Step 6: Lint and commit**

Run: `golangci-lint run ./cmd/opencode-sandbox/` — clean.

```bash
git add cmd/opencode-sandbox/commands_system.go cmd/opencode-sandbox/constants.go cmd/opencode-sandbox/cli_list_subcommand_test.go
git commit -m "feat(cli): add filter and format flags to sandbox list"
```

---

### Task 7: Update documentation

**Files:**
- Modify: `docs/commands.md`
- Modify: `docs/configuration.md`
- Modify: `README.md`

**Interfaces:**
- Consumes: all prior tasks' CLI and config behavior.
- Produces: docs consistent with the new flags and config keys.

- [ ] **Step 1: Update `docs/commands.md`**

Replace the global flag row for `--quiet`/`-q`:

```markdown
| `--quiet`    | `-q`  | `false` | Suppress non-error output      |
```

with:

```markdown
| `--error`    |       | `false` | Only show error output         |
```

Add a `sandbox list` flags subsection documenting: `-q, --quiet` (names only), `--label KEY=VALUE` (repeatable, AND), `--limit N`, `--running`, `--stopped` (running wins if both), `--format json` (array of `{name,status,image,created,updated,labels}`).

- [ ] **Step 2: Update `docs/configuration.md`**

In the config-key table, rename the `quiet` row's flag column from `--quiet / -q` to `--error`, and rename the key from `quiet` to `error`:

```markdown
| `error`                          | `--error`                | Only show error output                                                                                                                                                                 |
```

In the environment table, rename:

```markdown
| `error`                          | `OPENCODE_SANDBOX_ERROR`                                          |
```

- [ ] **Step 3: Update `README.md`**

Add a note in the CLI/flags section: the global `-q/--quiet` flag was renamed to `--error` (breaking change); `sandbox list` now supports `--label`, `--limit`, `--running`, `--stopped`, `-q/--quiet` (names only), and `--format json`.

- [ ] **Step 4: Verify no stale references**

Run: `grep -rn -- '--quiet\|OPENCODE_SANDBOX_QUIET\|| quiet ' README.md docs/commands.md docs/configuration.md`
Expected: no matches. Update any remaining occurrences.

- [ ] **Step 5: Commit**

```bash
git add docs/commands.md docs/configuration.md README.md
git commit -m "docs: document --error rename and sandbox list filter flags"
```

---

## Final verification

Run `make check` (fmt, lint, test) from the module root. Ensure `make build` succeeds. Verify `go run ./cmd/opencode-sandbox --dry-run` still behaves (skips launch). Confirm the CLI manually if the user is able to run VMs (msb note: /dev/kvm is not functional in this VM, so live testing must be done by the user).