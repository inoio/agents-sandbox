# Code Review Fixes Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement tasks sequentially. Steps follow TDD where applicable.

**Goal:** Fix all 7 active lint issues and improve code quality identified in the opencode-msb review, organized from lowest to highest risk.

**Architecture:** This is a refactoring-only plan — no new features, no behavioral changes. Each task is independent and produces a clean build + passing tests before moving on.

**Tech Stack:** Go 1.26, cobra, golangci-lint, moby/moby, microsandbox SDK

## Global Constraints

- Go 1.26 target (no compatibility constraints)
- All changes must pass `go test ./...` and `golangci-lint run` clean
- No behavioral changes, no new dependencies
- Every commit must be independently compilable and testable

---

### Task 1: Remove dead code `buildCmd` function

**Files:**
- Modify: `cmd/opencode-msb/cli_test.go:24-30`

**Interfaces:**
- Consumes: nothing
- Produces: no changes to public API

**Context:** `buildCmd` in `cli_test.go:26` is defined but never called anywhere. The linter reports it as `unused`. A grep for `buildCmd` in test files confirms no callers.

- [ ] **Step 1: Verify no callers exist**

Run: `grep -rn "buildCmd(" cmd/opencode-msb/`

Expected: only the definition at line 26, no other usages.

- [ ] **Step 2: Remove the function and its doc comment**

Search for the block to remove:

```go
// buildCmd finds and returns the named subcommand under root.
// Returns nil if the command is not found.
func buildCmd(t *testing.T, root *cobra.Command, name string) *cobra.Command {
	t.Helper()
	cmd, _, _ := root.Find(strings.Split(name, " "))
	return cmd
}
```

Delete lines 24-30 (the comment and the function body, plus blank line after).

- [ ] **Step 3: Run tests to confirm no breakage**

Run: `go test ./cmd/opencode-msb/... -count=1`

Expected: all tests pass.

- [ ] **Step 4: Run linter for this file**

Run: `golangci-lint run cmd/opencode-msb/cli_test.go`

Expected: `unused` issue for `buildCmd` gone. No new issues introduced.

- [ ] **Step 5: Commit**

```bash
git add cmd/opencode-msb/cli_test.go
git commit -m "refactor(test): remove unused buildCmd helper"
```

---

### Task 2: Fix placeholder `time.Duration(10)` values in prune.go

**Files:**
- Modify: `internal/sandbox/prune.go`

**Interfaces:**
- Consumes: nothing
- Produces: `StaleEntry.StaleFor` set to meaningful values

**Context:** In `prune.go` there are multiple places where `StaleFor: time.Duration(10)` appears — this is 10 nanoseconds, which is clearly a placeholder. There are 6 occurrences spread across prune helper functions. They represent "how long this artifact has been stale" and should be set to 0 (unknown) rather than a misleading small duration, since the caller already records the actual `StaleFor` value.

- [ ] **Step 1: Verify all occurrences**

Run: `grep -n "time.Duration(10)" internal/sandbox/prune.go`

Expected output lines in these functions:
- `pruneClones` or `pruneCloneVolumes` (line ~514)
- `pruneActiveVMHomeVolumes` (line ~594)
- `pruneActiveVMMSBImages` (line ~626)
- `removeHomeVolumes` (line ~751)
- `removeMSBImages` (line ~780)
- `removeDockerImages` (line ~799)

- [ ] **Step 2: Replace `time.Duration(10)` with `0`**

In each prune helper function, replace:

```go
StaleFor: time.Duration(10),
```

with:

```go
StaleFor: 0,
```

This means "duration not tracked for this artifact type." Since `StaleFor` is a `time.Duration` zero value is meaningful (unknown/placeholder).

- [ ] **Step 3: Run tests to confirm no breakage**

Run: `go test ./internal/sandbox/... -count=1 -v -run "Prune|prune|Stale"`

Expected: all prune tests pass. Note: existing tests use `0` already or compare count fields.

- [ ] **Step 4: Commit**

```bash
git add internal/sandbox/prune.go
git commit -m "fix(sandbox): replace placeholder time.Duration(10) with zero value in prune"
```

---

### Task 3: Fix `nilnil` linter issues in prune_client_test.go

**Files:**
- Modify: `internal/sandbox/prune_client_test.go`

**Interfaces:**
- Consumes: `sandboxFS` interface from `internal/sandbox`

**Context:** `prune_client_test.go` has two `nil, nil` returns that violate the `nilnil` linter rule (line 349 and 357). These are in `mockFs` methods for testing.

- [ ] **Step 1: Inspect the current code**

Read lines 340-360 of `internal/sandbox/prune_client_test.go` around the `mockFs` type.

The two offenders are:
- Line 349: `func (f *mockFs) Stat(_ context.Context, _ string) (*msb.FsStat, error) { return nil, nil }`
- Line ~357: `func (f *mockFs) ReadStream(_ context.Context, _ string) (*msb.FsReadStream, error) { return nil, nil }`

- [ ] **Step 2: Fix Stat — return a sentinel error instead of nil,nil**

Replace line 349:

```go
func (f *mockFs) Stat(_ context.Context, _ string) (*msb.FsStat, error) {
	return &msb.FsStat{}, nil
}
```

Note: `FsStat{}` is a zero-value struct that satisfies the return type. This is the idiomatic fix.

- [ ] **Step 3: Fix ReadStream — return a sentinel error instead of nil,nil**

Replace line ~357:

```go
func (f *mockFs) ReadStream(_ context.Context, _ string) (*msb.FsReadStream, error) {
	return &msb.FsReadStream{}, nil
}
```

Note: `FsStreamReader{}` or empty reader might be needed. Actually, the return type is `*msb.FsReadStream` so `&msb.FsReadStream{}` works.

- [ ] **Step 4: Run tests to confirm no breakage**

Run: `go test ./internal/sandbox/... -count=1`

Expected: all tests pass. Verify no code path checks `err == nil` on `Stat` or `ReadStream` from the mock expecting the fields to be nil.

- [ ] **Step 5: Run linter for this file**

Run: `golangci-lint run internal/sandbox/prune_client_test.go`

Expected: `nilnil` issues gone.

- [ ] **Step 6: Commit**

```bash
git add internal/sandbox/prune_client_test.go
git commit -m "fix(test): replace nil,nil returns with zero-value structs in mockFs"
```

---

### Task 4: Fix shadowed `err` declarations in docker.go

**Files:**
- Modify: `internal/sandbox/docker.go`

**Interfaces:**
- Consumes: nothing
- Produces: no API changes, only variable rename

**Context:** `docker.go` has 3 instances of shadowed `err` inside `startDockerdIfPresent`:

```
line 23: out, err := sb.Shell(ctx, dockerdBinaryCheckCmd, ...)    // first declaration
line 33: if infoOut, err := sb.Shell(ctx, dockerdReadyCmd, ...)   // shadows line 23
line 39: if _, err := sb.Shell(ctx, dockerdRestartCmd, ...)       // shadows line 23
line 45: out, err := sb.Shell(ctx, dockerdReadyCmd, ...)          // shadows line 23
```

The outer `err` (line 23) is never reassigned after its declaration, so the inner shadows are harmless but confusing and flagged by `govet shadow`.

**Approach:** Rename each inner declaration to a distinct name. The outer `err` should be renamed too since it's the "parent" that gets shadowed.

- [ ] **Step 1: Verify the current code**

Read `internal/sandbox/docker.go` lines 22-68 to see the full function.

- [ ] **Step 2: Rename outer `err` to `checkErr`**

Line 23:

```go
out, checkErr := sb.Shell(ctx, dockerdBinaryCheckCmd, msb.WithExecUser("root"))
if checkErr != nil {
    return fmt.Errorf("while checking dockerd binary: %w", checkErr)
}
```

- [ ] **Step 3: Rename inner `err` on line 33 to `readyErr`**

```go
if infoOut, readyErr := sb.Shell(ctx, dockerdReadyCmd, msb.WithExecUser("dev")); readyErr == nil && infoOut.Success() {
```

- [ ] **Step 4: Rename inner `err` on line 39 to `restartErr`**

```go
if _, restartErr := sb.Shell(ctx, dockerdRestartCmd, msb.WithExecUser("root")); restartErr != nil {
    return fmt.Errorf("start dockerd: %w", restartErr)
}
```

- [ ] **Step 5: Rename inner `err` on line 45 to `pollErr`**

```go
out, pollErr := sb.Shell(ctx, dockerdReadyCmd, msb.WithExecUser("dev"))
if pollErr == nil && out.Success() {
```

(No other uses of `err` in this for-loop body after this change.)

- [ ] **Step 6: Run tests**

Run: `go test ./internal/sandbox/... -count=1`

Expected: all tests pass.

- [ ] **Step 7: Run linter for this file**

Run: `golangci-lint run internal/sandbox/docker.go`

Expected: 0 `govet shadow` issues.

- [ ] **Step 8: Commit**

```bash
git add internal/sandbox/docker.go
git commit -m "fix(sandbox): remove shadowed err declarations in startDockerdIfPresent"
```

---

### Task 5: Remove dead `bool` return from prune phase functions

**Files:**
- Modify: `internal/sandbox/prune.go`

**Interfaces:**
- Consumes: nothing
- Produces: prune functions return `(*StaleReport, error)` instead of `(*StaleReport, bool)`

**Context:** The four prune phase functions return `(*StaleReport, bool)` but the bool is always `true` and never checked by callers:

```go
// In Prune() at lines ~406-409:
report, _ = pruneStaleVMs(...)        // bool discarded
report, _ = pruneActiveVMArtifacts(...) // bool discarded
report, _ = pruneOrphanArtifacts(...)   // bool discarded
report, _ = pruneCloneVolumes(...)      // bool discarded
```

Every function ends with `return report, true`. The bool parameter serves no purpose.

**Affected signatures:**
- `pruneStaleVMs(...) (*StaleReport, bool)` → `(*StaleReport, error)`
- `pruneActiveVMArtifacts(...) (*StaleReport, bool)` → `(*StaleReport, error)`
- `pruneOrphanArtifacts(...) (*StaleReport, bool)` → `(*StaleReport, error)`
- `pruneCloneVolumes(...) (*StaleReport, bool)` → `(*StaleReport, error)`

All callers in `Prune()` discard the second return value (`_`).

- [ ] **Step 1: Write a test that the PR function works correctly**

Run: `go test ./internal/sandbox/... -count=1 -v -run "TestPrune"`

Capture baseline to verify behavior hasn't changed.

- [ ] **Step 2: Update `Prune()` callers in `Prune()` function**

In `internal/sandbox/prune.go`, the `Prune` function (lines ~406-409):

Replace:
```go
report, _ = pruneStaleVMs(ctx, client, cli, catalog, dryRun, ui, report)
report, _ = pruneActiveVMArtifacts(ctx, client, cli, catalog, dryRun, ui, report)
report, _ = pruneOrphanArtifacts(ctx, client, cli, catalog, dryRun, ui, report)
report, _ = pruneCloneVolumes(ctx, client, catalog, dryRun, ui, report)
```

With:
```go
var err error
report, err = pruneStaleVMs(ctx, client, cli, catalog, dryRun, ui, report)
if err != nil {
    return report, err
}
report, err = pruneActiveVMArtifacts(ctx, client, cli, catalog, dryRun, ui, report)
if err != nil {
    return report, err
}
report, err = pruneOrphanArtifacts(ctx, client, cli, catalog, dryRun, ui, report)
if err != nil {
    return report, err
}
report, err = pruneCloneVolumes(ctx, client, catalog, dryRun, ui, report)
if err != nil {
    return report, err
}
```

This changes `_` to capturing actual errors — a small improvement.

- [ ] **Step 3: Update `pruneStaleVMs` signature and body**

Change:
```go
func pruneStaleVMs(...) (*StaleReport, bool) {
```
to:
```go
func pruneStaleVMs(...) (*StaleReport, error) {
```

And change the return at the end from:
```go
return report, true
```
to:
```go
return report, nil
```

- [ ] **Step 4: Update `pruneActiveVMArtifacts` signature and body**

Same pattern — `*StaleReport, bool` → `*StaleReport, error`, `true` → `nil`.

- [ ] **Step 5: Update `pruneOrphanArtifacts` signature and body**

Same pattern.

- [ ] **Step 6: Update `pruneCloneVolumes` signature and body**

Same pattern.

- [ ] **Step 7: Run tests**

Run: `go test ./internal/sandbox/... -count=1 -v -run "Prune"`

Expected: all tests pass.

- [ ] **Step 8: Run linter**

Run: `golangci-lint run internal/sandbox/prune.go`

Expected: no new issues.

- [ ] **Step 9: Commit**

```bash
git add internal/sandbox/prune.go
git commit -m "refactor(sandbox): remove dead bool return from prune phase functions"
```

---

### Task 6: Extract magic string constants from prune.go

**Files:**
- Modify: `internal/sandbox/prune.go`

**Interfaces:**
- Consumes: nothing
- Produces: constants that replace 15+ inline string literals

**Context:** `prune.go` has many magic string prefixes for artifact names scattered throughout. These are duplicated between naming code (in `volumes.go`, `image.go`, `projectvm.go`) and parsing code (in `prune.go`). The parsing constants should be extracted here for maintainability.

The prefixes that need constants:
- `"opencode-msb-vm-"` — used 3 times in `extractProjectSlugAndDigest` and `buildCatalog`
- `"opencode-msb-home-"` — used 3 times
- `"opencode-msb-clone-"` — used 3 times
- `"opencode-msb-task-"` — used 2 times
- `"opencode-msb/runner-"` — used 2 times
- `"opencode-msb-"` (generic prefix) — used 2 times

**Existing constant:** `projectVMPrefix = "opencode-msb-vm-"` already exists in `projectvm.go:19`.

- [ ] **Step 1: Define constants in prune.go**

At the top of `internal/sandbox/prune.go`, after the existing const block:

Add:
```go
const (
	sbPrefix         = "opencode-msb-"
	vmPrefix         = "opencode-msb-vm-"
	homePrefix       = "opencode-msb-home-"
	clonePrefix      = "opencode-msb-clone-"
	taskPrefix       = "opencode-msb-task-"
	imagePrefix      = "opencode-msb/runner-"
	baseImagePrefix  = "opencode-msb/runner-base"
)
```

- [ ] **Step 2: Replace `extractProjectSlugAndDigest` prefix checks**

In the function starting at line 118, replace:

Line 120: `if strings.HasPrefix(name, "opencode-msb/runner-")` → `if strings.HasPrefix(name, imagePrefix)`
Line 121: `afterPrefix := name[len("opencode-msb/runner-"):]` → `afterPrefix := name[len(imagePrefix):]`

In the switch starting at line 138:
Line 138: `case strings.HasPrefix(name, "opencode-msb-vm-"):` → `vmPrefix`
Line 139: `prefixLen = len("opencode-msb-vm-")` → `len(vmPrefix)`
Line 141: `case strings.HasPrefix(name, "opencode-msb-home-"):` → `homePrefix`
Line 142: `prefixLen = len("opencode-msb-home-")` → `len(homePrefix)`
Line 144: `case strings.HasPrefix(name, "opencode-msb-clone-"):` → `clonePrefix`
Line 145: `prefixLen = len("opencode-msb-clone-")` → `len(clonePrefix)`
Line 147: `case strings.HasPrefix(name, "opencode-msb-task-"):` → `taskPrefix`
Line 148: `prefixLen = len("opencode-msb-task-")` → `len(taskPrefix)`

- [ ] **Step 3: Replace `buildCatalog` prefix checks**

Line 271: `if !strings.HasPrefix(name, "opencode-msb-")` → `sbPrefix`
Line 275: `if strings.HasPrefix(name, projectVMPrefix)` → `vmPrefix` (note: `projectVMPrefix` from projectvm.go already equals this)
Line 295: `if strings.HasPrefix(name, "opencode-msb-task-")` → `taskPrefix`
Line 318: `if !strings.HasPrefix(name, "opencode-msb-")` → `sbPrefix`
Line 322: `if strings.HasPrefix(name, "opencode-msb-home-")` → `homePrefix`
Line 330: `if strings.HasPrefix(name, "opencode-msb-clone-")` → `clonePrefix`
Line 339: `if !strings.HasPrefix(ref, "opencode-msb/runner-")` → `imagePrefix`

Verify that every replacement produces correct behavior by checking that the constant values match the original strings exactly.

- [ ] **Step 4: Run tests**

Run: `go test ./internal/sandbox/... -count=1 -v -run "Prune|Extract|Slug"`

Expected: all tests pass. The table-driven tests in `prune_test.go` exercise the parsers extensively.

- [ ] **Step 5: Run linter**

Run: `golangci-lint run internal/sandbox/prune.go`

Expected: no new issues.

- [ ] **Step 6: Commit**

```bash
git add internal/sandbox/prune.go
git commit -m "refactor(sandbox): extract magic string prefixes to named constants in prune"
```

---

### Task 7: Split `extractProjectSlugAndDigest` into type-specific parsers

**Files:**
- Modify: `internal/sandbox/prune.go`

**Interfaces:**
- Consumes: nothing new
- Produces: cleaner parsing functions per artifact type

**Context:** `extractProjectSlugAndDigest` (118-193, ~75 lines) handles 4 different artifact name formats with embedded logic for images, VMs, volumes, and clone/task sandboxes. It should be split.

The function currently has this structure:
1. Image reference case (`opencode-msb/runner-{slug}:{tag}`) — lines 119-132
2. Prefix-based dispatch — lines 138-151
3. VM parsing with hash suffix detection — lines 162-184
4. Home volume parsing — lines 186-189
5. Fallback for clone/task — line 192

`findHashSuffix` (lines 88-106) is only used by the VM parsing path.

- [ ] **Step 1: Verify test coverage**

Run: `go test ./internal/sandbox/... -count=1 -v -run "Extract"`

All table-driven tests in `prune_test.go` should cover all paths. Record the list of test names.

- [ ] **Step 2: Extract `parseImageTag`**

Create a new function in `internal/sandbox/prune.go`:

```go
// parseImageTag extracts the slug and digest from a Docker image reference.
// Examples: "opencode-msb/runner-myproject:xYz1234AbCdEfGh"
//           → slug="myproject", digest="xYz1234AbCdEfGh"
//
//           "opencode-msb/runner-myproject:latest"
//           → slug="myproject", digest=""
//
//           "opencode-msb/runner-myproject"
//           → slug="myproject", digest=""
func parseImageTag(name string) (slug, digest string) {
	if !strings.HasPrefix(name, imagePrefix) {
		return "", ""
	}
	afterPrefix := name[len(imagePrefix):]
	lastColon := strings.LastIndex(afterPrefix, ":")
	if lastColon == -1 {
		return afterPrefix, ""
	}
	tag := afterPrefix[lastColon+1:]
	slug = afterPrefix[:lastColon]
	if tag != "" && tag != "latest" {
		digest = tag
	}
	return slug, digest
}
```

- [ ] **Step 3: Extract `parseVMName`, `parseHomeVolumeName`, `parseCloneVolumeName`**

```go
// parseVMName extracts the slug and optional branch (digest) from a sandbox name.
// Examples: "opencode-msb-vm-projectname-aB3cDe4fGhIjKl"
//           → slug="projectname-aB3cDe4fGhIjKl", digest=""
//
//           "opencode-msb-vm-projectname-aB3cDe4fGhIjKl-feature"
//           → slug="projectname-aB3cDe4fGhIjKl", digest="feature"
func parseVMName(name string) (slug, digest string) {
	if !strings.HasPrefix(name, vmPrefix) {
		return "", ""
	}
	remainder := name[len(vmPrefix):]
	hashStart := findHashSuffix(remainder)
	if hashStart == -1 {
		return remainder, ""
	}
	folderName := remainder[:hashStart-1]
	hash := remainder[hashStart : hashStart+14]
	slug = folderName + "-" + hash
	if hashStart+14 < len(remainder) {
		rest := remainder[hashStart+14:]
		if len(rest) > 1 && rest[0] == '-' {
			digest = rest[1:]
		}
	}
	return slug, digest
}

// parseHomeVolumeName extracts the slug and digest from a home volume name.
// Examples: "opencode-msb-home-myproject-aB3cDe4fGhIjKl-xYz1234AbCdEfGh"
//           → slug="myproject-aB3cDe4fGhIjKl", digest="xYz1234AbCdEfGh"
func parseHomeVolumeName(name string) (slug, digest string) {
	if !strings.HasPrefix(name, homePrefix) {
		return "", ""
	}
	remainder := name[len(homePrefix):]
	parts := strings.Split(remainder, "-")
	if len(parts) < 2 {
		return remainder, ""
	}
	digest = parts[len(parts)-1]
	slug = strings.Join(parts[:len(parts)-1], "-")
	return slug, digest
}

// parseCloneVolumeName extracts the slug from a clone volume name.
// Clone volumes have no digest component.
func parseCloneVolumeName(name string) (slug string) {
	if !strings.HasPrefix(name, clonePrefix) {
		return ""
	}
	remainder := name[len(clonePrefix):]
	parts := strings.Split(remainder, "-")
	if len(parts) < 2 {
		return remainder
	}
	return strings.Join(parts[:len(parts)-1], "-")
}
```

- [ ] **Step 4: Update `extractProjectSlugAndDigest` to delegate**

Replace the body of `extractProjectSlugAndDigest` (lines 118-193). Keep the existing doc comment but update the examples section to reference the new functions.

New body:

```go
func extractProjectSlugAndDigest(name string) (slug, digest string) {
	switch {
	case strings.HasPrefix(name, imagePrefix):
		return parseImageTag(name)
	case strings.HasPrefix(name, taskPrefix):
		// Task sandboxes have no digest, only slug.
		remainder := name[len(taskPrefix):]
		parts := strings.Split(remainder, "-")
		if len(parts) < 2 {
			return remainder, ""
		}
		return strings.Join(parts[:len(parts)-1], "-"), ""
	case strings.HasPrefix(name, vmPrefix):
		return parseVMName(name)
	case strings.HasPrefix(name, homePrefix):
		return parseHomeVolumeName(name)
	case strings.HasPrefix(name, clonePrefix):
		return parseCloneVolumeName(name), ""
	}
	return "", ""
}
```

- [ ] **Step 5: Add targeted tests for each parser**

Add table-driven tests in `prune_test.go`:

```go
func TestParseImageTag(t *testing.T) {
	tests := []struct {
		input string; wantSlug string; wantDigest string
	}{
		{"opencode-msb/runner-myproject:xYz1234AbCdEfGh", "myproject", "xYz1234AbCdEfGh"},
		{"opencode-msb/runner-myproject:latest", "myproject", ""},
		{"opencode-msb/runner-myproject:", "myproject", ""},
		{"opencode-msb/runner-myproject", "myproject", ""},
		{"opencode-msb/runner-my-project-name:xYz1234AbCdEfGh", "my-project-name", "xYz1234AbCdEfGh"},
		{"opencode-msb/runner-myproject:sha256:abc123", "myproject:sha256", "abc123"},
		{"other-image/myproject:tag", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			slug, digest := parseImageTag(tt.input)
			if slug != tt.wantSlug {
				t.Errorf("parseImageTag(%q) slug = %q, want %q", tt.input, slug, tt.wantSlug)
			}
			if digest != tt.wantDigest {
				t.Errorf("parseImageTag(%q) digest = %q, want %q", tt.input, digest, tt.wantDigest)
			}
		})
	}
}

func TestParseVMName(t *testing.T) {
	tests := []struct {
		input string; wantSlug string; wantDigest string
	}{
		{"opencode-msb-vm-projectname-aB3cDe4fGhIjKl", "projectname-aB3cDe4fGhIjKl", ""},
		{"opencode-msb-vm-projectname-aB3cDe4fGhIjKl-feature", "projectname-aB3cDe4fGhIjKl", "feature"},
		{"opencode-msb-vm-projectname-aB3cDe4fGhIjKl-feature-and-more", "projectname-aB3cDe4fGhIjKl", "feature-and-more"},
		{"opencode-msb-vm-projectname-main", "projectname-main", ""},
		{"opencode-msb-vm-myproject-abc1234567890", "myproject-abc1234567890", ""},
		{"opencode-msb-vm-noHash", "noHash", ""}, // no 14-char base36 suffix
		{"opencode-msb-home-test", "", ""}, // wrong prefix
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			slug, digest := parseVMName(tt.input)
			if slug != tt.wantSlug {
				t.Errorf("parseVMName(%q) slug = %q, want %q", tt.input, slug, tt.wantSlug)
			}
			if digest != tt.wantDigest {
				t.Errorf("parseVMName(%q) digest = %q, want %q", tt.input, digest, tt.wantDigest)
			}
		})
	}
}

func TestParseHomeVolumeName(t *testing.T) {
	tests := []struct {
		input string; wantSlug string; wantDigest string
	}{
		{"opencode-msb-home-myproject-aB3cDe4fGhIjKl-xYz1234AbCdEfGh", "myproject-aB3cDe4fGhIjKl", "xYz1234AbCdEfGh"},
		{"opencode-msb-home-myproject-abc1234567890", "myproject", "abc1234567890"},
		{"opencode-msb-home-single", "", ""}, // only one part after prefix
		{"opencode-msb-vm-something", "", ""}, // wrong prefix
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			slug, digest := parseHomeVolumeName(tt.input)
			if slug != tt.wantSlug {
				t.Errorf("parseHomeVolumeName(%q) slug = %q, want %q", tt.input, slug, tt.wantSlug)
			}
			if digest != tt.wantDigest {
				t.Errorf("parseHomeVolumeName(%q) digest = %q, want %q", tt.input, digest, tt.wantDigest)
			}
		})
	}
}

func TestParseCloneVolumeName(t *testing.T) {
	tests := []struct {
		input string; wantSlug string
	}{
		{"opencode-msb-clone-myproject--abc123", "myproject--abc123"},
		{"opencode-msb-clone-my-project-something", "my-project"},
		{"opencode-msb-home-foo", ""}, // wrong prefix
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			slug := parseCloneVolumeName(tt.input)
			if slug != tt.wantSlug {
				t.Errorf("parseCloneVolumeName(%q) = %q, want %q", tt.input, slug, tt.wantSlug)
			}
		})
	}
}
```

- [ ] **Step 6: Verify existing tests still pass**

Run: `go test ./internal/sandbox/... -count=1 -v -run "Prune|Extract|Slug|Image|Clone"`

Expected: same results as before the refactor.

- [ ] **Step 7: Run linter**

Run: `golangci-lint run internal/sandbox/prune.go`

Expected: no new issues. Check that `nonamedreturns` lint is still satisfied.

- [ ] **Step 8: Commit**

```bash
git add internal/sandbox/prune.go internal/sandbox/prune_test.go
git commit -m "refactor(sandbox): split extractProjectSlugAndDigest into type-specific parsers"
```

---

## Execution Order Summary

| # | Task | Type | Risk | Dependencies |
|---|------|------|------|--------------|
| 1 | Remove dead `buildCmd` | Dead code removal | None | — |
| 2 | Fix `time.Duration(10)` placeholders | Bugfix | Low | — |
| 3 | Fix `nilnil` test issues | Linter fix | Low | — |
| 4 | Fix shadowed `err` in docker.go | Bugfix | Low | — |
| 5 | Remove dead `bool` from prune functions | Dead code removal | Low | — |
| 6 | Extract magic string constants | Refactor | Medium | Tasks 1-5 |
| 7 | Split extractProjectSlugAndDigest | Refactor | Medium | Task 6 |

Tasks 1-5 are fully independent and can be done in any order. They each fix one lint issue from the original report. Tasks 6-7 are refactors that improve code quality and should follow the simple fixes so the linter stays clean during refactoring.

**Recommended parallelization:** Tasks 1-4 touch different files and can be done in parallel. Task 5 and 6 both touch `prune.go` so they must be sequential. Task 7 depends on Task 6 completing since it uses the extracted constants.

---

## Verification After All Tasks

Run `golangci-lint run` and confirm 0 issues, then `go test ./...` confirm all green.