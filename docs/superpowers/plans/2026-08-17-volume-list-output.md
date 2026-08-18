# Richer `volume list` Output Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace `opencode-sandbox volume list`'s two-column `NAME PATH` output with a columnar `NAME KIND SIZE CREATED` table matching `msb volume list`.

**Architecture:** Add the missing metadata accessors to the `msb.VolumeHandle` interface and `realVolumeHandle` adapter (mirroring `CreatedAt()`), add a shared bytes→human formatter in a new `internal/sandbox/humanize` package, extend `volume.VolumeInfo` and `ListVolumes` to populate the new fields, and update the CLI `list` subcommand to render the new columns via a shared `volumeListFormat` constant.

**Tech Stack:** Go, spf13/cobra, the microsandbox SDK (sdk/go@v0.6.9).

**Spec:** `docs/superpowers/specs/volume-list-output.md`

## Global Constraints

- Columns, left-aligned, in this order: `NAME`, `KIND`, `SIZE`, `CREATED`. Drop the `PATH` column.
- `SIZE` = quota (`QuotaMiB`) if non-nil, else capacity (`CapacityBytes`) if non-nil, else `-`. Quota/capacity are bytes → human-readable via `humanize.FormatBytes`.
- `CREATED` = `YYYY-MM-DD HH:MM:SS` in the time's own location; zero time renders `-`.
- Absent/nil metadata (unlimited quota, no capacity, no creation time) renders as `-`.
- The format string must be shared by the command and its tests (`volumeListFormat`, like `sandboxListFormat`).
- Target platforms: Linux (KVM) and macOS (Apple Silicon). No platform-specific code.
- Go style: run `make fmt` / `golangci-lint`; run `make check` when finalizing. No inline comments unless the code is not self-explanatory.

---

### Task 1: Shared bytes→human size formatter

**Files:**
- Create: `internal/sandbox/humanize/humanize.go`
- Create: `internal/sandbox/humanize/humanize_test.go`

**Interfaces:**
- Consumes: nothing (standard library only).
- Produces: `humanize.FormatBytes(uint64) string` — a reusable bytes→human-readable formatter (e.g. `1234567` → `1.2G`). Used by Task 3 (`volume list` SIZE) and later by `image list` (Chunk C).

- [ ] **Step 1: Write the failing test**

Create `internal/sandbox/humanize/humanize_test.go`:

```go
package humanize

import "testing"

func TestFormatBytes(t *testing.T) {
	cases := []struct {
		in   uint64
		want string
	}{
		{0, "0"},
		{1023, "1023"},
		{1024, "1.0K"},
		{1536, "1.5K"},
		{1024 * 1024, "1.0M"},
		{1234567, "1.2M"},
		{1024 * 1024 * 1024, "1.0G"},
		{3 * 1024 * 1024 * 1024, "3.0G"},
	}
	for _, tc := range cases {
		if got := FormatBytes(tc.in); got != tc.want {
			t.Errorf("FormatBytes(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/sandbox/humanize/... -v`
Expected: FAIL to compile — `FormatBytes` undefined.

- [ ] **Step 3: Write minimal implementation**

Create `internal/sandbox/humanize/humanize.go`:

```go
package humanize

import "fmt"

// FormatBytes renders a byte count in a human-readable form (e.g. 1.2G),
// using 1024-based units. Values below 1 KiB render as a plain byte count.
func FormatBytes(bytes uint64) string {
	const (
		kib = 1024
		mib = 1024 * kib
		gib = 1024 * mib
		tib = 1024 * gib
	)
	switch {
	case bytes >= tib:
		return format(bytes, tib, "T")
	case bytes >= gib:
		return format(bytes, gib, "G")
	case bytes >= mib:
		return format(bytes, mib, "M")
	case bytes >= kib:
		return format(bytes, kib, "K")
	default:
		return fmt.Sprintf("%d", bytes)
	}
}

func format(bytes, unit uint64, suffix string) string {
	return fmt.Sprintf("%.1f%s", float64(bytes)/float64(unit), suffix)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/sandbox/humanize/... -v`
Expected: PASS.

- [ ] **Step 5: Run linter and format**

Run: `make fmt && make lint`
Expected: no errors.

- [ ] **Step 6: Commit**

```bash
git add internal/sandbox/humanize/humanize.go internal/sandbox/humanize/humanize_test.go
git commit -m "feat(sandbox): add shared human-readable size formatter"
```

---

### Task 2: Extend the msb `VolumeHandle` interface and adapter

**Files:**
- Modify: `internal/sandbox/msb/msb.go` (interface at lines 82-87, `realVolumeHandle` at lines 284-320)
- Modify: `internal/sandbox/msb/testmock.go` (`MockVolumeHandle` at lines 610-625)

**Interfaces:**
- Consumes: `internal/sandbox/humanize` (not yet; only via volume package).
- Produces: `msb.VolumeHandle` gains `IsDefault() bool`, `QuotaMiB() *uint32`, `UsedBytes() uint64`, `CapacityBytes() *uint64`, `DiskFormat() *string`, `DiskFstype() *string`, `Labels() map[string]string`. `MockVolumeHandle` gains matching underscore fields so tests (Task 3/4) can set them.

- [ ] **Step 1: Write the failing test**

Append to `internal/sandbox/msb/testmock.go` (or a small new `msb_test.go` in the same package) a test that the mock satisfies the extended interface:

```go
func TestMockVolumeHandleImplementsInterface(t *testing.T) {
	var _ VolumeHandle = (*MockVolumeHandle)(nil)
	v := &MockVolumeHandle{}
	_ = v.IsDefault()
	_ = v.QuotaMiB()
	_ = v.UsedBytes()
	_ = v.CapacityBytes()
	_ = v.DiskFormat()
	_ = v.DiskFstype()
	_ = v.Labels()
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/sandbox/msb/... -run TestMockVolumeHandleImplementsInterface -v`
Expected: FAIL to compile — the methods do not exist yet.

- [ ] **Step 3: Extend the interface**

In `internal/sandbox/msb/msb.go`, replace the `VolumeHandle` interface (lines 82-87):

```go
// VolumeHandle is the subset of *msb.VolumeHandle that the launcher needs.
type VolumeHandle interface {
	Name() string
	Path() string
	Kind() msbSdk.VolumeKind
	CreatedAt() time.Time
	IsDefault() bool
	QuotaMiB() *uint32
	UsedBytes() uint64
	CapacityBytes() *uint64
	DiskFormat() *string
	DiskFstype() *string
	Labels() map[string]string
}
```

- [ ] **Step 4: Implement the adapter methods**

In `internal/sandbox/msb/msb.go`, append these methods to `realVolumeHandle` (after `CreatedAt`, line 320). The new accessors only exist on `*msbSdk.VolumeHandle`; for the `*msbSdk.Volume` case return zero values:

```go
func (v realVolumeHandle) IsDefault() bool {
	if h, ok := v.val.(*msbSdk.VolumeHandle); ok {
		return h.IsDefault()
	}
	return false
}

func (v realVolumeHandle) QuotaMiB() *uint32 {
	if h, ok := v.val.(*msbSdk.VolumeHandle); ok {
		return h.QuotaMiB()
	}
	return nil
}

func (v realVolumeHandle) UsedBytes() uint64 {
	if h, ok := v.val.(*msbSdk.VolumeHandle); ok {
		return h.UsedBytes()
	}
	return 0
}

func (v realVolumeHandle) CapacityBytes() *uint64 {
	if h, ok := v.val.(*msbSdk.VolumeHandle); ok {
		return h.CapacityBytes()
	}
	return nil
}

func (v realVolumeHandle) DiskFormat() *string {
	if h, ok := v.val.(*msbSdk.VolumeHandle); ok {
		return h.DiskFormat()
	}
	return nil
}

func (v realVolumeHandle) DiskFstype() *string {
	if h, ok := v.val.(*msbSdk.VolumeHandle); ok {
		return h.DiskFstype()
	}
	return nil
}

func (v realVolumeHandle) Labels() map[string]string {
	if h, ok := v.val.(*msbSdk.VolumeHandle); ok {
		return h.Labels()
	}
	return nil
}
```

- [ ] **Step 5: Extend `MockVolumeHandle`**

In `internal/sandbox/msb/testmock.go`, update `MockVolumeHandle` (lines 610-625) with underscore fields and methods. The `//nolint:revive // underscore names avoid conflicts with interface methods` comment on the struct stays. Replace the struct and method block:

```go
//nolint:revive // underscore names avoid conflicts with interface methods
type MockVolumeHandle struct {
	Name_          string
	Path_          string
	Kind_          msbSdk.VolumeKind
	CreatedAt_     time.Time
	IsDefault_     bool
	QuotaMiB_      *uint32
	UsedBytes_     uint64
	CapacityBytes_ *uint64
	DiskFormat_    *string
	DiskFstype_    *string
	Labels_        map[string]string
}

func (m MockVolumeHandle) Name() string { return m.Name_ }
func (m MockVolumeHandle) Path() string { return m.Path_ }
func (m MockVolumeHandle) Kind() msbSdk.VolumeKind {
	if m.Kind_ == "" {
		return msbSdk.VolumeKindDir
	}
	return m.Kind_
}
func (m MockVolumeHandle) CreatedAt() time.Time               { return m.CreatedAt_ }
func (m MockVolumeHandle) IsDefault() bool                     { return m.IsDefault_ }
func (m MockVolumeHandle) QuotaMiB() *uint32                   { return m.QuotaMiB_ }
func (m MockVolumeHandle) UsedBytes() uint64                   { return m.UsedBytes_ }
func (m MockVolumeHandle) CapacityBytes() *uint64              { return m.CapacityBytes_ }
func (m MockVolumeHandle) DiskFormat() *string                 { return m.DiskFormat_ }
func (m MockVolumeHandle) DiskFstype() *string                 { return m.DiskFstype_ }
func (m MockVolumeHandle) Labels() map[string]string           { return m.Labels_ }
```

- [ ] **Step 6: Run tests to verify they pass**

Run: `go test ./internal/sandbox/msb/... -v`
Expected: PASS (including `TestMockVolumeHandleImplementsInterface`).

- [ ] **Step 7: Run linter and format**

Run: `make fmt && make lint`
Expected: no errors.

- [ ] **Step 8: Commit**

```bash
git add internal/sandbox/msb/msb.go internal/sandbox/msb/testmock.go
git commit -m "feat(msb): expose volume metadata accessors on wrapper and mock"
```

---

### Task 3: Extend `VolumeInfo` and `ListVolumes`

**Files:**
- Modify: `internal/sandbox/volume/list.go`
- Modify: `internal/sandbox/volume/list_test.go`

**Interfaces:**
- Consumes: `msb.VolumeHandle` (Task 2) accessors `QuotaMiB()`, `CapacityBytes()`, `CreatedAt()`; `humanize.FormatBytes` (Task 1).
- Produces: `volume.VolumeInfo` gains `QuotaMiB *uint32`, `CapacityBytes *uint64`, `CreatedAt string`. `volume.FormatVolumeTime(time.Time) string` renders `YYYY-MM-DD HH:MM:SS` (or `-` for zero). Used by Task 4 (CLI columns).

- [ ] **Step 1: Write the failing test**

Add to `internal/sandbox/volume/list_test.go`:

```go
func TestListVolumes_PopulatesMetadata(t *testing.T) {
	quota := uint32(1024)
	var capacity uint64 = 2 * 1024 * 1024 * 1024
	created := time.Date(2026, 8, 17, 10, 42, 36, 0, time.UTC)
	mockClient := &msb.MockMsbClient{
		Volumes: []msb.VolumeHandle{
			&msb.MockVolumeHandle{
				Name_:          "opencode-sandbox-home-proj",
				Kind_:          "disk",
				QuotaMiB_:      &quota,
				CapacityBytes_: &capacity,
				CreatedAt_:     created,
			},
		},
	}
	msb.WithMsbMock(t, mockClient)

	result, err := ListVolumes(context.Background())
	if err != nil {
		t.Fatalf("ListVolumes returned error: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 volume, got %d", len(result))
	}
	v := result[0]
	if v.QuotaMiB == nil || *v.QuotaMiB != quota {
		t.Errorf("QuotaMiB = %v, want %d", v.QuotaMiB, quota)
	}
	if v.CapacityBytes == nil || *v.CapacityBytes != capacity {
		t.Errorf("CapacityBytes = %v, want %d", v.CapacityBytes, capacity)
	}
	if v.CreatedAt != "2026-08-17 10:42:36" {
		t.Errorf("CreatedAt = %q, want %q", v.CreatedAt, "2026-08-17 10:42:36")
	}
}

func TestFormatVolumeTime(t *testing.T) {
	utc := time.Date(2026, 8, 17, 10, 42, 36, 0, time.UTC)
	if got := FormatVolumeTime(utc); got != "2026-08-17 10:42:36" {
		t.Errorf("FormatVolumeTime(nonzero) = %q, want %q", got, "2026-08-17 10:42:36")
	}
	if got := FormatVolumeTime(time.Time{}); got != "-" {
		t.Errorf("FormatVolumeTime(zero) = %q, want %q", got, "-")
	}
}
```

Add `"time"` to the imports in `list_test.go`.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/sandbox/volume/... -run 'TestListVolumes_PopulatesMetadata|TestFormatVolumeTime' -v`
Expected: FAIL — `QuotaMiB`/`CapacityBytes`/`CreatedAt` fields and `FormatVolumeTime` do not exist.

- [ ] **Step 3: Write minimal implementation**

Rewrite `internal/sandbox/volume/list.go`:

```go
package volume

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/inoio/opencode-sandbox/internal/sandbox/msb"
	"github.com/inoio/opencode-sandbox/internal/sandbox/naming"
)

//nolint:revive // VolumeInfo is the established name from query.go
type VolumeInfo struct {
	Name          string
	Path          string
	Kind          string
	QuotaMiB      *uint32
	CapacityBytes *uint64
	CreatedAt     string
}

// FormatVolumeTime renders a timestamp as YYYY-MM-DD HH:MM:SS in the time's own
// location, or "-" for the zero time. This matches msb volume list output and is
// intentionally distinct from session.FormatTime (which omits seconds).
func FormatVolumeTime(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.Format("2006-01-02 15:04:05")
}

// ListVolumes returns a list of home volumes managed by opencode-sandbox.
func ListVolumes(ctx context.Context) ([]VolumeInfo, error) {
	handles, err := msb.Get().ListVolumes(ctx)
	if err != nil {
		return nil, fmt.Errorf("list volumes: %w", err)
	}
	var result []VolumeInfo
	for _, h := range handles {
		name := h.Name()
		if !strings.HasPrefix(name, naming.HomePrefix) {
			continue
		}
		result = append(result, VolumeInfo{
			Name:          name,
			Path:          h.Path(),
			Kind:          string(h.Kind()),
			QuotaMiB:      h.QuotaMiB(),
			CapacityBytes: h.CapacityBytes(),
			CreatedAt:     FormatVolumeTime(h.CreatedAt()),
		})
	}
	return result, nil
}
```

Note: `Path` is kept populated for now (the CLI still references it until Task 4). Task 4 removes the `Path` field from `VolumeInfo` when it drops the CLI `v.Path` reference, so the module stays compiling at every commit.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/sandbox/volume/... -v`
Expected: PASS (both existing and new tests). Note the existing `TestListVolumes_ReturnsOnlyHomeVolumes` references `Path_` and `Kind_` on the mock (still valid) but no longer checks `result[0].Path` — confirm no compile error. If the existing test asserts `Path`, it does not (it only checks `Name` and `Kind`), so it is unaffected.

- [ ] **Step 5: Run linter and format**

Run: `make fmt && make lint`
Expected: no errors.

- [ ] **Step 6: Commit**

```bash
git add internal/sandbox/volume/list.go internal/sandbox/volume/list_test.go
git commit -m "feat(volume): populate quota, capacity, and created time in list info"
```

---

### Task 4: Update the CLI `volume list` command

**Files:**
- Modify: `cmd/opencode-sandbox/commands_system.go` (`buildVolumeCmd` at lines 237-304; add `volumeListFormat` const near `sandboxListFormat` at line 29)
- Modify: `cmd/opencode-sandbox/cli_volume_test.go` (or create `cmd/opencode-sandbox/cli_volume_list_test.go`)

**Interfaces:**
- Consumes: `volume.VolumeInfo` (Task 3) with `Name`, `Kind`, `QuotaMiB`, `CapacityBytes`, `CreatedAt`; `humanize.FormatBytes` (Task 1); `msb.MockVolumeHandle` (Task 2) for test setup.
- Produces: `volumeListFormat` constant shared by the command and its tests.

- [ ] **Step 1: Write the failing test**

Create `cmd/opencode-sandbox/cli_volume_list_test.go`. It reuses `setupCommandFixtures`, `containsNormalized`, `errBoom`, and the `sandboxmsb` / `msb` imports from the sibling `cli_list_subcommand_test.go` (all in package `main`). Reference the SDK alias as `msb "github.com/superradcompany/microsandbox/sdk/go"`.

```go
package main

import (
	"testing"
	"time"

	msb "github.com/superradcompany/microsandbox/sdk/go"

	sandboxmsb "github.com/inoio/opencode-sandbox/internal/sandbox/msb"
)

func TestVolumeList(t *testing.T) {
	quota := uint32(2048)
	var capacity uint64 = 1024 * 1024 * 1024

	tests := []struct {
		name            string
		mockSetup       func(m *sandboxmsb.MockMsbClient)
		wantOut         []string
		wantInfo        []string
		wantErr         bool
		wantErrContains string
	}{
		{
			name:      "empty",
			mockSetup: func(_ *sandboxmsb.MockMsbClient) {},
			wantInfo:  []string{"No volumes found."},
		},
		{
			name: "dir volume renders dash size",
			mockSetup: func(m *sandboxmsb.MockMsbClient) {
				m.Volumes = []sandboxmsb.VolumeHandle{
					&sandboxmsb.MockVolumeHandle{
						Name_:      "opencode-sandbox-home-proj",
						Kind_:      msb.VolumeKindDir,
						CreatedAt_: time.Date(2026, 8, 17, 10, 42, 36, 0, time.UTC),
					},
				}
			},
			wantOut: []string{
				"opencode-sandbox-home-proj dir - 2026-08-17 10:42:36",
			},
		},
		{
			name: "disk volume uses quota for size",
			mockSetup: func(m *sandboxmsb.MockMsbClient) {
				m.Volumes = []sandboxmsb.VolumeHandle{
					&sandboxmsb.MockVolumeHandle{
						Name_:      "opencode-sandbox-home-proj",
						Kind_:      msb.VolumeKindDisk,
						QuotaMiB_:  &quota,
						CreatedAt_: time.Date(2026, 8, 17, 10, 42, 36, 0, time.UTC),
					},
				}
			},
			wantOut: []string{
				"opencode-sandbox-home-proj disk 2.0G 2026-08-17 10:42:36",
			},
		},
		{
			name: "disk volume uses capacity when no quota",
			mockSetup: func(m *sandboxmsb.MockMsbClient) {
				m.Volumes = []sandboxmsb.VolumeHandle{
					&sandboxmsb.MockVolumeHandle{
						Name_:          "opencode-sandbox-home-proj",
						Kind_:          msb.VolumeKindDisk,
						CapacityBytes_: &capacity,
						CreatedAt_:     time.Date(2026, 8, 17, 10, 42, 36, 0, time.UTC),
					},
				}
			},
			wantOut: []string{
				"opencode-sandbox-home-proj disk 1.0G 2026-08-17 10:42:36",
			},
		},
		{
			name: "non-project volume filtered",
			mockSetup: func(m *sandboxmsb.MockMsbClient) {
				m.Volumes = []sandboxmsb.VolumeHandle{
					&sandboxmsb.MockVolumeHandle{Name_: "other-volume", Kind_: msb.VolumeKindDir},
				}
			},
			wantOut: []string{},
		},
		{
			name:            "list error",
			mockSetup:       func(m *sandboxmsb.MockMsbClient) { m.ListVolumesErr = errBoom },
			wantErr:         true,
			wantErrContains: "list volumes",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mock := &sandboxmsb.MockMsbClient{}
			tc.mockSetup(mock)
			cmd, ui := setupCommandFixtures(t, cmdVolume, cmdList)
			sandboxmsb.WithMsbMock(t, mock)
			if err := cmd.Execute(); err != nil {
				if !tc.wantErr {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if tc.wantErr {
				t.Fatal("expected error, got none")
			}
			for _, want := range tc.wantOut {
				if !containsNormalized(ui.OutCalls, want) {
					t.Errorf("OutCalls missing %q; got: %v", want, ui.OutCalls)
				}
			}
			for _, want := range tc.wantInfo {
				if !containsNormalized(ui.InfoCalls, want) {
					t.Errorf("InfoCalls missing %q; got: %v", want, ui.InfoCalls)
				}
			}
		})
	}
}

func TestVolumeListFormatShared(t *testing.T) {
	if volumeListFormat == "" {
		t.Fatal("volumeListFormat must be non-empty so command and tests share it")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/opencode-sandbox/... -run TestVolumeList -v`
Expected: FAIL — `volumeListFormat` undefined and the current output has `NAME PATH`, not the new columns.

- [ ] **Step 3: Write minimal implementation**

In `cmd/opencode-sandbox/commands_system.go`, add a `volumeListFormat` constant next to `sandboxListFormat` (line 29):

```go
// volumeListFormat is shared by buildVolumeCmd and its tests so the column
// layout stays in sync. Matches msb volume list: NAME KIND SIZE CREATED.
const volumeListFormat = "%-60s %-6s %-8s %-19s"
```

Add a size-rendering helper near `truncateImage` (line 39), or directly in the command. Prefer a small helper so the SIZE fallback logic is testable at the CLI boundary:

```go
// volumeSize renders the SIZE column: quota, else capacity, else "-" for
// dir/unlimited volumes. Quota/capacity are bytes rendered human-readable.
func volumeSize(q *uint32, c *uint64) string {
	if q != nil {
		return humanize.FormatBytes(uint64(*q) * 1024 * 1024)
	}
	if c != nil {
		return humanize.FormatBytes(*c)
	}
	return "-"
}
```

Add the import `"github.com/inoio/opencode-sandbox/internal/sandbox/humanize"` to `commands_system.go`.

In `internal/sandbox/volume/list.go`, remove the now-unused `Path` field from `VolumeInfo` (and the `Path: h.Path(),` line from `ListVolumes`), since the CLI no longer references it:

```go
//nolint:revive // VolumeInfo is the established name from query.go
type VolumeInfo struct {
	Name          string
	Kind          string
	QuotaMiB      *uint32
	CapacityBytes *uint64
	CreatedAt     string
}
```

Then update the `list` subcommand inside `buildVolumeCmd` (lines 243-259) to use the new format and columns:

```go
	cmd.AddCommand(&cobra.Command{
		Use:     cmdList,
		Aliases: cmdListAliases,
		Args:    cobra.NoArgs,
		Short:   "List managed volumes",
		RunE: func(cmd *cobra.Command, _ []string) error {
			volumes, err := volume.ListVolumes(cmd.Context())
			if err != nil {
				return err
			}
			printItems(volumes, "No volumes found.", volumeListFormat, ui,
				func(v volume.VolumeInfo) string { return v.Name },
				func(v volume.VolumeInfo) string { return v.Kind },
				func(v volume.VolumeInfo) string { return volumeSize(v.QuotaMiB, v.CapacityBytes) },
				func(v volume.VolumeInfo) string { return v.CreatedAt },
			)
			return nil
		},
	})
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./cmd/opencode-sandbox/... -run TestVolumeList -v`
Expected: PASS. The `TestVolumeList` cases assert the new columns (including `-` size for dir volumes).

- [ ] **Step 5: Run the full test suite**

Run: `go test ./...`
Expected: PASS.

- [ ] **Step 6: Run linter and format**

Run: `make fmt && make lint`
Expected: no errors.

- [ ] **Step 7: Commit**

```bash
git add cmd/opencode-sandbox/commands_system.go cmd/opencode-sandbox/cli_volume_list_test.go
git commit -m "feat(cli): render volume list with NAME KIND SIZE CREATED columns"
```

---

### Task 5: Docs and final check

**Files:**
- Modify: `README.md` (and any `docs/` listing `volume list` output, if present)

**Interfaces:**
- Consumes: the final CLI output shape from Task 4.
- Produces: updated documentation describing the new `volume list` columns.

- [ ] **Step 1: Check current docs**

Search `README.md` and `docs/` for references to `volume list` output or the `%-50s` volume format:

Run: `rg -n "volume list|volume ls|VolumeInfo|PATH" README.md docs/`
Expected: identify any text to update to the new `NAME KIND SIZE CREATED` columns. If none exists, this task is a no-op.

- [ ] **Step 2: Update documentation**

Update any `README.md`/docs references describing `volume list` output to state the new columns (`NAME`, `KIND`, `SIZE`, `CREATED`), noting `SIZE` shows `-` for dir/unlimited volumes. Keep it accurate and concise.

- [ ] **Step 3: Run full checks**

Run: `make check`
Expected: fmt, lint, and all tests pass.

- [ ] **Step 4: Commit**

Note: `docs/superpowers` is in `.gitignore`. Use `git add -f` for any path under it (e.g. README/docs in other locations add normally).

```bash
git add README.md docs/
git commit -m "docs: document volume list columns"
```

---

## Self-Review

**Spec coverage:**
- Columns `NAME KIND SIZE CREATED`, drop PATH → Task 4 (format) + Task 3 (remove `Path`).
- SIZE = quota → capacity → `-`, bytes→human → Task 1 (`humanize.FormatBytes`) + Task 4 (`volumeSize`).
- CREATED `YYYY-MM-DD HH:MM:SS`, zero → `-` → Task 3 (`FormatVolumeTime`).
- Nil/absent → `-` → Task 4 (`volumeSize` returns `-`) + Task 3 (zero time → `-`).
- Shared format string with tests → Task 4 (`volumeListFormat`, `TestVolumeListFormatShared`).
- Wrapper accessors + zero-value for `*Volume` case → Task 2.
- Shared size helper in `internal/sandbox/humanize` reused → Task 1; image list will reuse later (out of scope for this plan).
- Docs in sync → Task 5.

**Placeholder scan:** All steps contain concrete code and commands; no TBD/TODO. Verified the `humanize` package does not exist yet and no byte-formatting helper exists.

**Type consistency:** `humanize.FormatBytes(uint64) string` used in Task 1 and Task 4. `volume.VolumeInfo` fields `QuotaMiB *uint32`, `CapacityBytes *uint64`, `CreatedAt string` match between Task 3 and Task 4. `msb.MockVolumeHandle` underscore fields match the interface in Task 2 and test usage in Tasks 3/4. `volumeSize(*uint32, *uint64) string` matches Task 4 test expectations (`2.0G` for 2048 MiB quota, `1.0G` for 1 GiB capacity).