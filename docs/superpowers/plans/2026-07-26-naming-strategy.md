# Unified Artifact Naming Strategy Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the inconsistent artifact naming (hex hashes, substring-matching volume filters, leaky prefixes) with a unified base62-based naming scheme using one prefix stem (`opencode-msb`), typed infixes, and clean prefix-based filtering.

**Architecture:** A single `HashID(input string) string` function in the `git` package replaces all hex hashing, producing 8-char base62 tokens (47.6 bits of entropy). `ProjectSlug` returns `<sanitized-folder-name>-<8b62>` instead of `p-<8hex>`. Image, sandbox, and volume naming functions are updated to embed the project slug and use typed infixes (`-sb-`, `-task-`, `-home-`, `-clone-`). All list/prune filters become prefix matches. No automatic migration — old artifacts become invisible to new filters.

**Tech Stack:** Go 1.26, `math/big.Int` for base62 encoding, Docker SDK (`github.com/moby/moby/client`), microsandbox SDK (`github.com/superradcompany/microsandbox/sdk/go`).

## Global Constraints

- Base62 alphabet must be ordered `0-9a-zA-Z` for deterministic encoding.
- `HashID` takes an arbitrary string, computes `sha256.Sum256`, converts to `big.Int`, encodes in base62, takes first 8 chars.
- Sandbox names capped at 128 chars by msb; `sandboxName` truncates if needed.
- No type-marker letters on hash tokens — positional parsing suffices.
- Branch slug escaping (`-` → `--`, `/` → `---`) is unchanged.
- The base image is Docker-only — never loaded into the msb image store.
- Old-prefixed artifacts are not deleted; they become invisible to new prefix filters.
- Target platforms: Linux (KVM) and macOS (Apple Silicon).

---

## File Structure

| File | Responsibility | Action |
|------|---------------|--------|
| `internal/git/git.go` | `HashID` base62 function, `sanitizeFolderName`, `ProjectSlug` rewrite | Modify |
| `internal/git/git_test.go` | Tests for `HashID`, `sanitizeFolderName`, `ProjectSlug` | Modify |
| `internal/sandbox/image.go` | `BaseTag`, `ImageTag`, `runnerTag`, `ReferencesBase`, `EnsureImage` | Modify |
| `internal/sandbox/image_test.go` | Test expectations for new image tags | Modify |
| `internal/sandbox/runner.go` | `sandboxName`, caller wiring for `EnsureImage`/`ensureNoSameHomeSession`/`BuildImage` | Modify |
| `internal/sandbox/runner_test.go` | Test expectations for new sandbox names | Modify |
| `internal/sandbox/volumes.go` | `HomeVolumeName`, `cloneVolumeName`, `prefillVolume`, `CloneVolume` | Modify |
| `internal/sandbox/volumes_test.go` | Test expectations for new volume names | Modify |
| `internal/sandbox/query.go` | All filters → prefix matches | Modify |
| `internal/sandbox/query_test.go` | Test expectations for new filter prefixes | Modify |
| `internal/sandbox/integration_test.go` | Update `ensureNoSameHomeSession` call signature | Modify |
| `internal/sandbox/doctor.go` | Optional: warn about orphaned old-prefixed artifacts | Modify |

---

### Task 1: `HashID` — base62 hash function

**Files:**
- Modify: `internal/git/git.go`
- Modify: `internal/git/git_test.go`

**Interfaces:**
- Consumes: nothing (foundational)
- Produces: `HashID(input string) string` — takes arbitrary string, returns first 8 chars of base62-encoded SHA-256 digest. Exported so `internal/sandbox` can use it for image digest and volume naming.

**Background:** The current code uses `hex.EncodeToString(h[:])[:8]` (8 hex chars = 32 bits) for project slugs and `hex[:12]` (48 bits) for image digests. `HashID` replaces both with 8 base62 chars (47.6 bits). The algorithm: `sha256.Sum256` → `big.Int.SetBytes` → repeated `DivMod(62)` collecting remainders → reverse to most-significant-first → pad to 8 with `0` → truncate to 8.

- [ ] **Step 1: Write the failing tests**

Add to `internal/git/git_test.go`:

```go
func TestHashIDReturns8Chars(t *testing.T) {
	got := HashID("test-input")
	if len(got) != 8 {
		t.Errorf("expected 8 chars, got %d (%q)", len(got), got)
	}
}

func TestHashIDDeterministic(t *testing.T) {
	a := HashID("sha256:abc123def456")
	b := HashID("sha256:abc123def456")
	if a != b {
		t.Errorf("expected deterministic output, got %q and %q", a, b)
	}
}

func TestHashIDDifferentInputs(t *testing.T) {
	a := HashID("hello")
	b := HashID("world")
	if a == b {
		t.Errorf("expected different hashes for different inputs, got %q for both", a)
	}
}

func TestHashIDBase62AlphabetOnly(t *testing.T) {
	got := HashID("sha256:fce5c4a3b2d1e0f9a8b7c6d5e4f3a2b1c0d9e8f7a6b5c4d3e2f1a0b9c8d7e6f5")
	for _, r := range got {
		isAlnum := (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
		if !isAlnum {
			t.Errorf("expected base62 alphabet only, found %q in %q", r, got)
		}
	}
}

func TestHashIDKnownValues(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"", "RZwTDmWj"},
		{"sha256:abc123def456", "xRX898Gl"},
		{"hello", "aEO7hBt3"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			if got := HashID(tt.input); got != tt.want {
				t.Errorf("HashID(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/git/ -run TestHashID -v`
Expected: FAIL — `HashID` undefined.

- [ ] **Step 3: Implement `HashID`**

Add to `internal/git/git.go`. First update the imports to include `math/big`:

```go
import (
	"crypto/sha256"
	"errors"
	"fmt"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"gitlab.inoio.de/inoio/opencode-msb/internal/output"
)
```

Remove the `"encoding/hex"` import (no longer needed after `ProjectSlug` is rewritten in Task 2; if the build fails because `hex` is still referenced, leave it for now and remove it in Task 2).

Add the function and alphabet constant:

```go
const base62Alphabet = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"

func HashID(input string) string {
	sum := sha256.Sum256([]byte(input))
	num := new(big.Int).SetBytes(sum[:])
	if num.Sign() == 0 {
		return "00000000"
	}
	var encoded []byte
	base := big.NewInt(62)
	mod := new(big.Int)
	for num.Sign() > 0 {
		num.DivMod(num, base, mod)
		encoded = append(encoded, base62Alphabet[mod.Int64()])
	}
	for len(encoded) < 8 {
		encoded = append(encoded, '0')
	}
	for i, j := 0, len(encoded)-1; i < j; i, j = i+1, j-1 {
		encoded[i], encoded[j] = encoded[j], encoded[i]
	}
	if len(encoded) > 8 {
		encoded = encoded[:8]
	}
	return string(encoded)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/git/ -run TestHashID -v`
Expected: PASS

- [ ] **Step 5: Run full test suite and linter**

Run: `go test ./internal/git/ -v && golangci-lint run ./internal/git/`
Expected: PASS (existing tests still pass; `hex` import may now be unused — if so, remove it now to avoid a compile error).

- [ ] **Step 6: Commit**

```bash
git add internal/git/git.go internal/git/git_test.go
git commit -m "feat: add HashID base62 hash function"
```

---

### Task 2: `sanitizeFolderName` and `ProjectSlug` rewrite

**Files:**
- Modify: `internal/git/git.go`
- Modify: `internal/git/git_test.go`

**Interfaces:**
- Consumes: `HashID(input string) string` from Task 1, `gitCommonDir(cwd string) (string, error)` (existing)
- Produces: `ProjectSlug(logger *output.Printer) string` — returns `<sanitized-folder-name>-<8b62>` (was `p-<8hex>`). The folder name comes from the repo root directory (`filepath.Base(filepath.Dir(commonDir))`), or CWD basename if not a repo. The hash is `HashID(absCommonDir)`.

**Background:** The current `ProjectSlug` returns `p-<8hex>` — a bare hash with no project name. The new format embeds a human-readable folder name for readability, with the hash disambiguating projects that share a folder name. `sanitizeFolderName` lowercases, replaces non-alphanumeric with `-`, collapses consecutive `-`, trims leading/trailing `-`, and caps at 20 chars.

- [ ] **Step 1: Write the failing tests**

Add to `internal/git/git_test.go`:

```go
func TestSanitizeFolderNameLowercases(t *testing.T) {
	got := sanitizeFolderName("MyApp")
	if got != "myapp" {
		t.Errorf("expected %q, got %q", "myapp", got)
	}
}

func TestSanitizeFolderNameReplacesNonAlnum(t *testing.T) {
	got := sanitizeFolderName("My App")
	if got != "my-app" {
		t.Errorf("expected %q, got %q", "my-app", got)
	}
}

func TestSanitizeFolderNameCollapsesDashes(t *testing.T) {
	got := sanitizeFolderName("My---App!!!")
	if got != "my-app" {
		t.Errorf("expected %q, got %q", "my-app", got)
	}
}

func TestSanitizeFolderNameTrimsLeadingTrailingDashes(t *testing.T) {
	got := sanitizeFolderName("---leading-and-trailing---")
	if got != "leading-and-trailing" {
		t.Errorf("expected %q, got %q", "leading-and-trailing", got)
	}
}

func TestSanitizeFolderNameCapsAt20(t *testing.T) {
	got := sanitizeFolderName("abcdefghijklmnopqrstuvwxyz")
	if len(got) > 20 {
		t.Errorf("expected <= 20 chars, got %d (%q)", len(got), got)
	}
}

func TestSanitizeFolderNameEmptyInput(t *testing.T) {
	got := sanitizeFolderName("")
	if got != "" {
		t.Errorf("expected empty string, got %q", got)
	}
}
```

Also update the existing `TestWorktreePathConstruction` and `TestWorktreePathWithBranchSlug` tests — they use `"p-abc123"` and `"p-abc"` as slug examples. These still work because `WorktreePath` just joins path components, so the slug format doesn't matter. No change needed to those tests.

Add a structural test for `ProjectSlug` that doesn't depend on a specific temp dir path:

```go
func TestProjectSlugFormat(t *testing.T) {
	repo := initRepo(t)
	// ProjectSlug uses the working directory, so chdir into the repo.
	oldWd, _ := os.Getwd()
	defer os.Chdir(oldWd)
	if err := os.Chdir(repo); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	l := output.NewPrinter(os.Stderr, false)
	got := ProjectSlug(l)
	// Expected format: <sanitized-folder>-<8 base62 chars>.
	// The folder name is filepath.Base(repo), sanitized.
	folderName := sanitizeFolderName(filepath.Base(repo))
	if !strings.HasPrefix(got, folderName+"-") {
		t.Errorf("expected slug to start with %q, got %q", folderName+"-", got)
	}
	hashPart := got[len(folderName)+1:]
	if len(hashPart) != 8 {
		t.Errorf("expected 8-char hash suffix, got %d chars (%q)", len(hashPart), hashPart)
	}
	for _, r := range hashPart {
		isAlnum := (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
		if !isAlnum {
			t.Errorf("expected base62 hash, found %q in %q", r, hashPart)
		}
	}
}

func TestProjectSlugDeterministic(t *testing.T) {
	repo := initRepo(t)
	oldWd, _ := os.Getwd()
	defer os.Chdir(oldWd)
	if err := os.Chdir(repo); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	l := output.NewPrinter(os.Stderr, false)
	a := ProjectSlug(l)
	b := ProjectSlug(l)
	if a != b {
		t.Errorf("expected deterministic slug, got %q and %q", a, b)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/git/ -run "TestSanitizeFolderName|TestProjectSlug" -v`
Expected: FAIL — `sanitizeFolderName` undefined; `ProjectSlug` still returns `p-<8hex>` format.

- [ ] **Step 3: Implement `sanitizeFolderName` and rewrite `ProjectSlug`**

In `internal/git/git.go`, add `sanitizeFolderName` and update `ProjectSlug`. Remove the `"encoding/hex"` import if not already removed in Task 1:

```go
func sanitizeFolderName(name string) string {
	name = strings.ToLower(name)
	fields := strings.FieldsFunc(name, func(r rune) bool {
		return !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'))
	})
	s := strings.Join(fields, "-")
	if len(s) > 20 {
		s = s[:20]
	}
	return strings.Trim(s, "-")
}
```

Replace the existing `ProjectSlug` function:

```go
func ProjectSlug(logger *output.Printer) string {
	commonDir, err := gitCommonDir(".")
	if err != nil || commonDir == "" {
		cwd, _ := filepath.Abs(".")
		logger.Warnf("not inside a git repo; using CWD hash as project slug.")
		return sanitizeFolderName(filepath.Base(cwd)) + "-" + HashID(cwd)
	}
	abs, _ := filepath.Abs(commonDir)
	folderName := sanitizeFolderName(filepath.Base(filepath.Dir(abs)))
	return folderName + "-" + HashID(abs)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/git/ -run "TestSanitizeFolderName|TestProjectSlug" -v`
Expected: PASS

- [ ] **Step 5: Run full test suite and linter**

Run: `go test ./internal/git/ -v && golangci-lint run ./internal/git/`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/git/git.go internal/git/git_test.go
git commit -m "feat: rewrite ProjectSlug with folder name and base62 hash"
```

---

### Task 3: Image naming — `BaseTag`, `ImageTag`, `runnerTag`, `ReferencesBase`

**Files:**
- Modify: `internal/sandbox/image.go`
- Modify: `internal/sandbox/image_test.go`
- Modify: `internal/sandbox/runner.go` (caller updates: `prepareSandbox`, `BuildImage`)

**Interfaces:**
- Consumes: `git.HashID(input string) string` from Task 1, `projectSlug string` (produced by `git.ProjectSlug`)
- Produces:
  - `BaseTag` constant → `"opencode-msb/runner-base:latest"` (was `"opencode-msb/runner:base"`)
  - `ImageTag(projectSlug, imageDigest string) string` → `"opencode-msb/runner-<slug>:<8b62>"` (was `ImageTag(digest string) string` → `"opencode-msb/runner:sha256-<12hex>"`)
  - `runnerTag(projectSlug string) string` → `"opencode-msb/runner-<slug>:latest"` (was `const runnerTag = "opencode-msb/runner:latest"`)
  - `ReferencesBase(dockerfile []byte) bool` — checks for `"opencode-msb/runner-base"` instead of `"opencode-msb/runner:base"`
  - `EnsureImage(ctx, cli, dockerfile, projectSlug, force, logger)` — new `projectSlug` parameter

**Background:** The base image tag changes from `opencode-msb/runner:base` to `opencode-msb/runner-base:latest` so it doesn't collide with the `runner-<slug>` namespace. `runnerTag` becomes a per-project function (not a constant) so each project gets its own Docker image. `ImageTag` uses `git.HashID` on the image digest instead of truncating hex, producing 8 base62 chars. The msb reference (`ImageTag` output) is identical to the Docker digest tag.

- [ ] **Step 1: Write the failing tests**

Update `internal/sandbox/image_test.go`. Replace the existing `TestReferencesBaseDetectsBaseImage`, `TestReferencesBaseReturnsFalseForOtherImage`, `TestReferencesBaseIgnoresComments`, and `TestImageTag` tests:

```go
func TestReferencesBaseDetectsBaseImage(t *testing.T) {
	dockerfile := []byte("FROM opencode-msb/runner-base:latest\nRUN echo hi\n")
	if !ReferencesBase(dockerfile) {
		t.Error("expected ReferencesBase=true for Dockerfile with base FROM")
	}
}

func TestReferencesBaseReturnsFalseForOtherImage(t *testing.T) {
	dockerfile := []byte("FROM debian:trixie-slim\nRUN echo hi\n")
	if ReferencesBase(dockerfile) {
		t.Error("expected ReferencesBase=false for non-base Dockerfile")
	}
}

func TestReferencesBaseIgnoresComments(t *testing.T) {
	dockerfile := []byte("# FROM opencode-msb/runner-base:latest\nFROM debian:trixie-slim\n")
	if ReferencesBase(dockerfile) {
		t.Error("expected ReferencesBase=false for commented FROM")
	}
}

func TestImageTag(t *testing.T) {
	got := ImageTag("myproj-aBc1234D", "sha256:abc123def456")
	expected := "opencode-msb/runner-myproj-aBc1234D:xRX898Gl"
	if got != expected {
		t.Errorf("expected %q, got %q", expected, got)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/sandbox/ -run "TestReferencesBase|TestImageTag" -v`
Expected: FAIL — old expectations don't match new format; `ImageTag` signature mismatch.

- [ ] **Step 3: Implement the changes in `image.go`**

Update `internal/sandbox/image.go`:

Change the `BaseTag` constant and remove `shortDigestLen`:

```go
const (
	BaseTag        = "opencode-msb/runner-base:latest"
	dockerfileMode = 0o644
)
```

Update `ReferencesBase` to check for the new base tag:

```go
func ReferencesBase(dockerfile []byte) bool {
	scanner := bufio.NewScanner(bytes.NewReader(dockerfile))
	for scanner.Scan() {
		line := strings.TrimLeft(scanner.Text(), " \t")
		if strings.HasPrefix(line, "FROM") && strings.Contains(line, BaseTag) {
			return true
		}
	}
	return false
}
```

(This is unchanged in logic — `BaseTag` now contains `runner-base` instead of `runner:base`, so the `Contains` check naturally matches the new string.)

Replace `ImageTag`:

```go
func ImageTag(projectSlug, imageDigest string) string {
	return "opencode-msb/runner-" + projectSlug + ":" + git.HashID(imageDigest)
}
```

Replace the `runnerTag` constant with a function:

```go
func runnerTag(projectSlug string) string {
	return "opencode-msb/runner-" + projectSlug + ":latest"
}
```

Update `EnsureImage` to accept `projectSlug` and use the new `runnerTag` function:

```go
func EnsureImage(
	ctx context.Context,
	cli dockerClient,
	dockerfile []byte,
	projectSlug string,
	force bool,
	logger *output.Printer,
) (string, string, error) {
	if force || ReferencesBase(dockerfile) {
		if err := buildDockerImage(
			ctx,
			cli,
			EmbeddedDockerfile,
			BaseTag,
			"Building base runner image",
			force,
			logger,
		); err != nil {
			return "", "", fmt.Errorf("building base image: %w", err)
		}
	}

	rTag := runnerTag(projectSlug)
	if err := buildDockerImage(ctx, cli, dockerfile, rTag, "Building runner image", force, logger); err != nil {
		return "", "", err
	}

	inspect, err := cli.ImageInspect(ctx, rTag)
	if err != nil {
		return "", "", fmt.Errorf("cannot inspect built image: %w", err)
	}
	imageDigest := inspect.ID
	imageRef := ImageTag(projectSlug, imageDigest)

	_, cacheErr := msb.Image.Get(ctx, imageRef)
	if cacheErr == nil && !force {
		return imageRef, imageDigest, nil
	}

	spin := output.NewSpinner(logger)
	spin.Start("Loading image into microsandbox")
	saveResult, err := cli.ImageSave(ctx, []string{rTag})
	if err != nil {
		spin.StopError(err)
		return "", "", fmt.Errorf("cannot export Docker image: %w", err)
	}
	defer saveResult.Close()

	cmd := exec.CommandContext(ctx, "msb", "load", "--tag", imageRef)
	cmd.Stdin = saveResult
	if out, err := cmd.CombinedOutput(); err != nil {
		spin.StopError(err)
		return "", "", fmt.Errorf("loading image into microsandbox failed: %w: %s", err, out)
	}
	spin.Stop()

	return imageRef, imageDigest, nil
}
```

Add the `git` import to `image.go`:

```go
import (
	"archive/tar"
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/moby/moby/client"

	"gitlab.inoio.de/inoio/opencode-msb/internal/git"
	"gitlab.inoio.de/inoio/opencode-msb/internal/output"

	msb "github.com/superradcompany/microsandbox/sdk/go"
)
```

- [ ] **Step 4: Update callers in `runner.go`**

In `internal/sandbox/runner.go`, update the `EnsureImage` call in `prepareSandbox` (around line 493):

```go
imageRef, imageDigest, err := EnsureImage(ctx, dockerCli, dockerfile, projectSlug, opts.Rebuild, logger)
```

Update `BuildImage` to compute `projectSlug` and pass it:

```go
func BuildImage(ctx context.Context, force bool, logger *output.Printer) error {
	if !CheckDocker(logger) {
		return errors.New("docker not available")
	}
	projectSlug := git.ProjectSlug(logger)
	dockerfile := resolveDockerfile()
	dockerCli, err := client.New(client.FromEnv)
	if err != nil {
		return fmt.Errorf("cannot connect to Docker daemon (is dockerd running?): %w", err)
	}
	defer dockerCli.Close()

	_, _, err = EnsureImage(ctx, dockerCli, dockerfile, projectSlug, force, logger)
	return err
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/sandbox/ -run "TestReferencesBase|TestImageTag" -v`
Expected: PASS

- [ ] **Step 6: Run full test suite and linter**

Run: `go test ./internal/sandbox/ -v && golangci-lint run ./internal/sandbox/`
Expected: PASS (some tests in `runner_test.go` may fail if they reference old image tag format — those are updated in Task 4).

- [ ] **Step 7: Commit**

```bash
git add internal/sandbox/image.go internal/sandbox/image_test.go internal/sandbox/runner.go
git commit -m "feat: update image naming to per-project runner-base and base62 digest"
```

---

### Task 4: Sandbox naming — `sandboxName` with `-sb-` infix

**Files:**
- Modify: `internal/sandbox/runner.go`
- Modify: `internal/sandbox/runner_test.go`

**Interfaces:**
- Consumes: `projectSlug string` (produced by `git.ProjectSlug`)
- Produces: `sandboxName(projectSlug, branchSlug string) string` → `"opencode-msb-sb-<slug>-<branchSlug>"` (was `"opencode-msb-<slug>-<branchSlug>"`)

**Background:** Session sandboxes get a `-sb-` infix so they are cleanly separable from ephemeral task sandboxes (which get `-task-` in Task 6) by prefix matching. The `opencode-msb-sb-` prefix allows `ListSandboxes` to show only real sessions, not transient provisioning sandboxes.

- [ ] **Step 1: Write the failing test**

Update `internal/sandbox/runner_test.go`. Replace the existing `TestSandboxNameTruncation`:

```go
func TestSandboxNameFormat(t *testing.T) {
	got := sandboxName("myproj-aBc1234D", "main")
	expected := "opencode-msb-sb-myproj-aBc1234D-main"
	if got != expected {
		t.Errorf("expected %q, got %q", expected, got)
	}
}

func TestSandboxNameTruncation(t *testing.T) {
	got := sandboxName("p-abcdef", "feat-very-long-branch-name-that-exceeds-the-limit-and-more")
	if len(got) > 128 {
		t.Errorf("expected name <= 128 bytes, got %d", len(got))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/sandbox/ -run "TestSandboxName" -v`
Expected: FAIL — `sandboxName` still produces `opencode-msb-<slug>-<branch>` without `-sb-`.

- [ ] **Step 3: Implement the change**

In `internal/sandbox/runner.go`, update `sandboxName`:

```go
func sandboxName(projectSlug, branchSlug string) string {
	name := "opencode-msb-sb-" + projectSlug + "-" + branchSlug
	if len(name) > maxSandboxNameLen {
		name = name[:maxSandboxNameLen]
	}
	return name
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/sandbox/ -run "TestSandboxName" -v`
Expected: PASS

- [ ] **Step 5: Run full test suite and linter**

Run: `go test ./internal/sandbox/ -v && golangci-lint run ./internal/sandbox/`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/sandbox/runner.go internal/sandbox/runner_test.go
git commit -m "feat: add -sb- infix to session sandbox names"
```

---

### Task 5: Volume naming — `HomeVolumeName` and `cloneVolumeName`

**Files:**
- Modify: `internal/sandbox/volumes.go`
- Modify: `internal/sandbox/volumes_test.go`

**Interfaces:**
- Consumes: `git.HashID(input string) string` from Task 1
- Produces:
  - `HomeVolumeName(projectSlug, imageDigest string) string` → `"opencode-msb-home-<slug>-<8b62>"` (was `"<slug>-opencode-home-<sanitized-digest>"`)
  - `cloneVolumeName(sourceVol string) string` → `"opencode-msb-clone-<slug>-<8b62>-<ts>"` (was `"<sourceVol>-clone-<ts>"`)

**Background:** The home volume name changes from a mid-string substring (`-opencode-home-`) to a clean prefix (`opencode-msb-home-`). The image digest in the volume name is now `git.HashID(imageDigest)` — the same 8-char base62 token used in the msb image tag, making image ↔ volume correlation greppable. The clone volume name strips the `opencode-msb-home-` prefix from the source and replaces it with `opencode-msb-clone-`, preserving the slug and digest, plus a timestamp.

- [ ] **Step 1: Write the failing tests**

Update `internal/sandbox/volumes_test.go`. Replace the existing `TestHomeVolumeName`, `TestHomeVolumeNameSanitizesColon`, and `TestCloneVolumeName`:

```go
func TestHomeVolumeName(t *testing.T) {
	got := HomeVolumeName("myproj-aBc1234D", "sha256:abc123def456")
	expected := "opencode-msb-home-myproj-aBc1234D-xRX898Gl"
	if got != expected {
		t.Errorf("expected %q, got %q", expected, got)
	}
}

func TestHomeVolumeNameIgnoresColonPrefix(t *testing.T) {
	got := HomeVolumeName("myproj-aBc1234D", "sha256:abc123def456")
	got2 := HomeVolumeName("myproj-aBc1234D", "abc123def456")
	if got != got2 {
		t.Errorf("expected same hash regardless of sha256: prefix, got %q and %q", got, got2)
	}
}
```

Note: `HashID` hashes the full input string, so `"sha256:abc123def456"` and `"abc123def456"` produce different hashes. The `sha256:` prefix is part of the Docker image ID and is included in the hash input. The old `TestHomeVolumeNameSanitizesColon` test is removed since `HashID` handles the full digest string uniformly — there is no colon sanitization step anymore. The known-value test above verifies the correct hash token (`xRX898Gl`), which is the same token that `TestImageTag` in Task 3 produces for the same digest — this verifies the image ↔ volume correlation property.

Replace `TestCloneVolumeName`:

```go
func TestCloneVolumeName(t *testing.T) {
	source := "opencode-msb-home-myproj-aBc1234D-xRX898Gl"
	got := cloneVolumeName(source)
	if !strings.HasPrefix(got, "opencode-msb-clone-myproj-aBc1234D-xRX898Gl-") {
		t.Errorf("expected clone name to start with 'opencode-msb-clone-myproj-aBc1234D-xRX898Gl-', got %q", got)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/sandbox/ -run "TestHomeVolumeName|TestCloneVolumeName" -v`
Expected: FAIL — old format doesn't match.

- [ ] **Step 3: Implement the changes**

In `internal/sandbox/volumes.go`, add the `git` import and replace `sanitizeDigest`, `HomeVolumeName`, and `cloneVolumeName`:

```go
import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"gitlab.inoio.de/inoio/opencode-msb/internal/git"
	"gitlab.inoio.de/inoio/opencode-msb/internal/output"

	msb "github.com/superradcompany/microsandbox/sdk/go"
)
```

Remove the `sanitizeDigest` function entirely (no longer needed — `HashID` handles the full digest string).

Replace `HomeVolumeName`:

```go
func HomeVolumeName(projectSlug, imageDigest string) string {
	return "opencode-msb-home-" + projectSlug + "-" + git.HashID(imageDigest)
}
```

Replace `cloneVolumeName`:

```go
func cloneVolumeName(sourceVol string) string {
	stripped := strings.TrimPrefix(sourceVol, "opencode-msb-home-")
	return fmt.Sprintf("opencode-msb-clone-%s-%d", stripped, time.Now().UnixNano())
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/sandbox/ -run "TestHomeVolumeName|TestCloneVolumeName" -v`
Expected: PASS

- [ ] **Step 5: Run full test suite and linter**

Run: `go test ./internal/sandbox/ -v && golangci-lint run ./internal/sandbox/`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/sandbox/volumes.go internal/sandbox/volumes_test.go
git commit -m "feat: update volume naming to opencode-msb-home prefix and base62 digest"
```

---

### Task 6: Task sandbox naming and `CloneVolume` caller wiring

**Files:**
- Modify: `internal/sandbox/volumes.go`
- Modify: `internal/sandbox/runner.go`
- Modify: `internal/sandbox/integration_test.go`

**Interfaces:**
- Consumes: `projectSlug string` (from `git.ProjectSlug`)
- Produces:
  - `prefillVolume(ctx, vm, projectSlug, volumeName, imageTag)` — sandbox name becomes `"opencode-msb-task-prefill-<slug>-<ts>"` (was `"opencode-msb-prefill-<ts>"`)
  - `CloneVolume(ctx, vm, projectSlug, sourceVol, imageTag)` — sandbox name becomes `"opencode-msb-task-clone-<slug>-<ts>"` (was `"opencode-msb-clone-<ts>"`)
  - `ensureNoSameHomeSession(ctx, vm, projectSlug, homeVol, excludeSandbox, imageRef, logger)` — new `projectSlug` parameter, threaded through to `CloneVolume`

**Background:** Ephemeral task sandboxes (prefill, clone) get the `-task-` infix so they are excluded from `ListSandboxes` (which filters on `opencode-msb-sb-`). The `projectSlug` is threaded from `prepareSandbox` → `ensureNoSameHomeSession` → `CloneVolume` and from `EnsureHome` → `prefillVolume`.

- [ ] **Step 1: Write the failing test**

No direct unit test for `prefillVolume`/`CloneVolume` sandbox names (they require msb). The integration test in `integration_test.go` calls `ensureNoSameHomeSession` which needs its signature updated.

Update `internal/sandbox/integration_test.go` — the `TestEnsureNoSameHomeSessionNoConflict` call:

```go
func TestEnsureNoSameHomeSessionNoConflict(t *testing.T) {
	vm := NewVolumeManager(newTestLogger(t))
	got, err := ensureNoSameHomeSession(t.Context(), vm, "test-project", "nonexistent-vol", "my-sandbox", "my-image", newTestLogger(t))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "nonexistent-vol" {
		t.Errorf("expected original volume name, got %q", got)
	}
}
```

And the `TestSameHomeVolumeInUseConflict` call to `ensureNoSameHomeSession`:

```go
gotVol, err := ensureNoSameHomeSession(ctx, vm, "test-project", volName, "new-sandbox", "alpine:latest", logger)
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/sandbox/ -run "TestEnsureNoSameHomeSession" -v` (without integration tag)
Expected: FAIL — `ensureNoSameHomeSession` signature doesn't include `projectSlug`.

Run: `go test -tags integration ./internal/sandbox/ -run "TestSameHomeVolumeInUseConflict" -v`
Expected: FAIL — same signature mismatch.

- [ ] **Step 3: Update `prefillVolume` signature and sandbox name**

In `internal/sandbox/volumes.go`, update `prefillVolume`:

```go
func (vm *VolumeManager) prefillVolume(ctx context.Context, projectSlug, volumeName, imageTag string) error {
	prefillName := fmt.Sprintf("opencode-msb-task-prefill-%s-%d", projectSlug, time.Now().UnixNano())
```

Update `EnsureHome` to pass `projectSlug` to `prefillVolume`:

```go
func (vm *VolumeManager) EnsureHome(
	ctx context.Context,
	projectSlug, imageDigest, imageTag string,
) (string, error) {
	name := HomeVolumeName(projectSlug, imageDigest)

	_, err := msb.GetVolume(ctx, name)
	if err == nil {
		return name, nil
	}

	vol, err := msb.CreateVolume(ctx, name,
		msb.WithVolumeKind(msb.VolumeKindDir),
	)
	if err != nil {
		return "", fmt.Errorf("create volume %s: %w", name, err)
	}

	if err := vm.prefillVolume(ctx, projectSlug, vol.Name(), imageTag); err != nil {
		return "", fmt.Errorf("prefill volume %s: %w", name, err)
	}

	return name, nil
}
```

- [ ] **Step 4: Update `CloneVolume` signature and sandbox name**

In `internal/sandbox/volumes.go`, update `CloneVolume`:

```go
func (vm *VolumeManager) CloneVolume(
	ctx context.Context,
	projectSlug, sourceVol, imageTag string,
) (string, error) {
	cloneName := cloneVolumeName(sourceVol)

	vol, err := msb.CreateVolume(ctx, cloneName,
		msb.WithVolumeKind(msb.VolumeKindDir),
	)
	if err != nil {
		return "", fmt.Errorf("create clone volume %s: %w", cloneName, err)
	}

	defer func() {
		if err != nil {
			_ = msb.RemoveVolume(context.Background(), cloneName)
		}
	}()

	taskName := fmt.Sprintf("opencode-msb-task-clone-%s-%d", projectSlug, time.Now().UnixNano())

	mounts := map[string]msb.MountConfig{
		"/mnt/src": msb.Mount.Named(sourceVol, msb.MountOptions{Readonly: true}),
		"/mnt/dst": msb.Mount.Named(vol.Name(), msb.MountOptions{}),
	}

	spin := output.NewSpinner(vm.logger)
	spin.Start("Cloning home volume")
	sb, err := msb.CreateSandbox(ctx, taskName,
		msb.WithImage(imageTag),
		msb.WithMounts(mounts),
		msb.WithReplace(),
	)
	if err != nil {
		spin.StopError(err)
		return "", fmt.Errorf("create clone sandbox: %w", err)
	}
	defer func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), sandboxStopTimeout)
		defer cancel()
		_ = sb.Stop(stopCtx)
		_ = sb.Close()
		_ = msb.RemoveSandbox(context.Background(), taskName)
	}()

	out, err := sb.Exec(ctx, "sh", []string{"-c",
		"cp -a /mnt/src/. /mnt/dst/ && chown -R dev:dev /mnt/dst && find /mnt/dst -name '*.shm' -delete",
	})
	if err != nil {
		spin.StopError(err)
		return "", fmt.Errorf("clone cp: %w", err)
	}
	if !out.Success() {
		err = fmt.Errorf("clone cp failed (exit %d): %s", out.ExitCode(), out.Stderr())
		spin.StopError(err)
		return "", err
	}
	spin.Stop()
	return cloneName, nil
}
```

- [ ] **Step 5: Update `ensureNoSameHomeSession` in `runner.go`**

In `internal/sandbox/runner.go`, add `projectSlug` to `ensureNoSameHomeSession`:

```go
func ensureNoSameHomeSession(
	ctx context.Context,
	vm *VolumeManager,
	projectSlug, homeVol, excludeSandbox, imageRef string,
	logger *output.Printer,
) (string, error) {
	inUseBy, inUse, err := sameHomeVolumeInUse(ctx, homeVol, excludeSandbox)
	if err != nil {
		return "", err
	}
	if !inUse {
		return homeVol, nil
	}

	logger.Warnf(
		"Another opencode session (%q) is using the same project state.\n"+
			"Starting with a snapshot copy of the current home directory.\n"+
			"Opencode sessions and history from this run will NOT be persisted.",
		inUseBy,
	)

	if !prompt.AssumeYes {
		confirmed, confirmErr := prompt.ConfirmDefault("Proceed with snapshot copy?", false, logger)
		if confirmErr != nil {
			return "", fmt.Errorf("prompt for clone: %w", confirmErr)
		}
		if !confirmed {
			return "", fmt.Errorf("aborted: another session (%q) is using the project state", inUseBy)
		}
	}

	cloneVol, err := vm.CloneVolume(ctx, projectSlug, homeVol, imageRef)
	if err != nil {
		return "", err
	}
	logger.Infof("Cloned home volume: %s", cloneVol)
	return cloneVol, nil
}
```

Update the caller in `prepareSandbox` (around line 517):

```go
homeVol, err = ensureNoSameHomeSession(ctx, vm, projectSlug, homeVol, name, imageRef, logger)
```

- [ ] **Step 6: Run tests to verify they pass**

Run: `go test ./internal/sandbox/ -v && go test -tags integration ./internal/sandbox/ -run TestEnsureNoSameHomeSessionNoConflict -v`
Expected: PASS

- [ ] **Step 7: Run linter**

Run: `golangci-lint run ./internal/sandbox/`
Expected: PASS

- [ ] **Step 8: Commit**

```bash
git add internal/sandbox/volumes.go internal/sandbox/runner.go internal/sandbox/integration_test.go
git commit -m "feat: add -task- infix to ephemeral sandboxes and thread projectSlug to CloneVolume"
```

---

### Task 7: Filter strategy — prefix matches in `query.go`

**Files:**
- Modify: `internal/sandbox/query.go`
- Modify: `internal/sandbox/query_test.go`

**Interfaces:**
- Consumes: new naming prefixes from Tasks 4, 5, 6
- Produces: updated filter prefixes for `filterSandboxes`, `filterVolumes`, `filterImages`, `ListSandboxes`, `ListVolumes`, `ListImages`

**Background:** All filters change from mixed prefix/substring matching to clean prefix matches:

| Filter | Old match | New prefix |
|--------|-----------|------------|
| `filterSandboxes` / `ListSandboxes` | prefix `opencode-msb-` | prefix `opencode-msb-sb-` |
| `filterVolumes` / `ListVolumes` | substring `-opencode-home-` | prefix `opencode-msb-home-` |
| `filterImages` / `ListImages` | prefix `opencode-msb/runner` | prefix `opencode-msb/runner-` |
| Clone volume cleanup | (none) | prefix `opencode-msb-clone-` |
| Task sandbox cleanup | (none) | prefix `opencode-msb-task-` |

The `opencode-msb/runner-` prefix matches `runner-<slug>` (project images) but also `runner-base` (base image). The base image is Docker-only and never loaded into msb, so it can't appear in `ListImages` results.

- [ ] **Step 1: Write the failing tests**

Update `internal/sandbox/query_test.go`:

```go
func TestFilterSandboxesByPrefix(t *testing.T) {
	handles := []sandboxHandle{
		{name: "opencode-msb-sb-proj-main"},
		{name: "opencode-msb-sb-other-feat"},
		{name: "opencode-msb-task-prefill-proj-1719432000"},
		{name: "opencode-msb-task-clone-proj-1719432000"},
		{name: "someone-elses-sandbox"},
		{name: "random"},
	}
	got := filterSandboxes(handles)
	if len(got) != 2 {
		t.Fatalf("expected 2 session sandboxes, got %d", len(got))
	}
	if got[0] != "opencode-msb-sb-proj-main" {
		t.Errorf("expected first match, got %q", got[0])
	}
	if got[1] != "opencode-msb-sb-other-feat" {
		t.Errorf("expected second match, got %q", got[1])
	}
}

func TestFilterVolumesByPrefix(t *testing.T) {
	handles := []volumeHandle{
		{name: "opencode-msb-home-proj-aBc1234D"},
		{name: "opencode-msb-clone-proj-aBc1234D-1719432000"},
		{name: "old-style-proj-opencode-home-sha256-abc"},
		{name: "random-volume"},
	}
	got := filterVolumes(handles)
	if len(got) != 1 {
		t.Fatalf("expected 1 home volume, got %d", len(got))
	}
	if got[0] != "opencode-msb-home-proj-aBc1234D" {
		t.Errorf("expected home volume, got %q", got[0])
	}
}

func TestFilterImagesByPrefix(t *testing.T) {
	handles := []imageHandle{
		{reference: "opencode-msb/runner-proj-aBc1234D:xRX898Gl"},
		{reference: "opencode-msb/runner-otherproj-eFg5678I:abc12345"},
		{reference: "python:3.12"},
	}
	got := filterImages(handles)
	if len(got) != 2 {
		t.Fatalf("expected 2 project images, got %d", len(got))
	}
	if got[0] != "opencode-msb/runner-proj-aBc1234D:xRX898Gl" {
		t.Errorf("expected first match, got %q", got[0])
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/sandbox/ -run "TestFilter" -v`
Expected: FAIL — old prefixes don't match new test data.

- [ ] **Step 3: Implement the changes**

In `internal/sandbox/query.go`, update all filter functions:

```go
func filterSandboxes(handles []sandboxHandle) []string {
	var result []string
	for _, h := range handles {
		if strings.HasPrefix(h.name, "opencode-msb-sb-") {
			result = append(result, h.name)
		}
	}
	return result
}

func filterVolumes(handles []volumeHandle) []string {
	var result []string
	for _, h := range handles {
		if strings.HasPrefix(h.name, "opencode-msb-home-") {
			result = append(result, h.name)
		}
	}
	return result
}

func filterImages(handles []imageHandle) []string {
	var result []string
	for _, h := range handles {
		if strings.HasPrefix(h.reference, "opencode-msb/runner-") {
			result = append(result, h.reference)
		}
	}
	return result
}
```

Update the `ListSandboxes` function:

```go
func ListSandboxes(ctx context.Context) ([]Info, error) {
	handles, err := msb.ListSandboxes(ctx)
	if err != nil {
		return nil, fmt.Errorf("list sandboxes: %w", err)
	}
	var result []Info
	for _, h := range handles {
		name := h.Name()
		if !strings.HasPrefix(name, "opencode-msb-sb-") {
			continue
		}
		result = append(result, Info{
			Name:   name,
			Status: string(h.Status()),
		})
	}
	return result, nil
}
```

Update the `ListVolumes` function:

```go
func ListVolumes(ctx context.Context) ([]VolumeInfo, error) {
	handles, err := msb.ListVolumes(ctx)
	if err != nil {
		return nil, fmt.Errorf("list volumes: %w", err)
	}
	var result []VolumeInfo
	for _, h := range handles {
		name := h.Name()
		if !strings.HasPrefix(name, "opencode-msb-home-") {
			continue
		}
		result = append(result, VolumeInfo{
			Name: name,
			Path: h.Path(),
			Kind: string(h.Kind()),
		})
	}
	return result, nil
}
```

Update the `ListImages` function:

```go
func ListImages(ctx context.Context) ([]ImageInfo, error) {
	handles, err := msb.Image.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("list images: %w", err)
	}
	var result []ImageInfo
	for _, h := range handles {
		ref := h.Reference()
		if !strings.HasPrefix(ref, "opencode-msb/runner-") {
			continue
		}
		result = append(result, ImageInfo{
			Reference: ref,
			Digest:    h.ManifestDigest(),
		})
	}
	return result, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/sandbox/ -run "TestFilter" -v`
Expected: PASS

- [ ] **Step 5: Run full test suite and linter**

Run: `go test ./... && golangci-lint run`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/sandbox/query.go internal/sandbox/query_test.go
git commit -m "feat: update all filters to clean prefix matches"
```

---

### Task 8: Doctor orphan warning (optional)

**Files:**
- Modify: `internal/sandbox/doctor.go`

**Interfaces:**
- Consumes: `msb.ListSandboxes`, `msb.ListVolumes`, `msb.Image.List` (existing)
- Produces: warning output in `CheckAll` when orphaned old-prefixed artifacts are detected

**Background:** Old-prefixed artifacts won't match any new prefix filter. The `doctor` command can optionally warn about them so users know to clean up once. This is a best-effort scan — it lists artifacts matching old patterns:

- Sandboxes: prefix `opencode-msb-` but NOT `opencode-msb-sb-` or `opencode-msb-task-`
- Volumes: substring `-opencode-home-` (old pattern) but NOT prefix `opencode-msb-home-` or `opencode-msb-clone-`
- Images: `opencode-msb/runner:base`, `opencode-msb/runner:latest`, `opencode-msb/runner:sha256-*`

- [ ] **Step 1: Write the failing test**

Add to `internal/sandbox/doctor_test.go` (or a new `internal/sandbox/doctor_orphan_test.go`):

```go
func TestIsOrphanedSandboxOldPrefix(t *testing.T) {
	if !isOrphanedSandbox("opencode-msb-proj-main") {
		t.Error("expected old-prefix sandbox to be orphaned")
	}
	if isOrphanedSandbox("opencode-msb-sb-proj-main") {
		t.Error("expected new-prefix sandbox to not be orphaned")
	}
	if isOrphanedSandbox("opencode-msb-task-prefill-proj-123") {
		t.Error("expected task sandbox to not be orphaned")
	}
	if isOrphanedSandbox("someone-elses-sandbox") {
		t.Error("expected foreign sandbox to not be orphaned")
	}
}

func TestIsOrphanedVolumeOldPattern(t *testing.T) {
	if !isOrphanedVolume("proj-opencode-home-sha256-abc") {
		t.Error("expected old-pattern volume to be orphaned")
	}
	if isOrphanedVolume("opencode-msb-home-proj-aBc1234D") {
		t.Error("expected new-prefix volume to not be orphaned")
	}
	if isOrphanedVolume("opencode-msb-clone-proj-aBc1234D-123") {
		t.Error("expected clone volume to not be orphaned")
	}
	if isOrphanedVolume("random-volume") {
		t.Error("expected foreign volume to not be orphaned")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/sandbox/ -run "TestIsOrphaned" -v`
Expected: FAIL — functions undefined.

- [ ] **Step 3: Implement orphan detection helpers**

In `internal/sandbox/doctor.go`, add:

```go
func isOrphanedSandbox(name string) bool {
	if !strings.HasPrefix(name, "opencode-msb-") {
		return false
	}
	return !strings.HasPrefix(name, "opencode-msb-sb-") &&
		!strings.HasPrefix(name, "opencode-msb-task-")
}

func isOrphanedVolume(name string) bool {
	if strings.HasPrefix(name, "opencode-msb-home-") || strings.HasPrefix(name, "opencode-msb-clone-") {
		return false
	}
	return strings.Contains(name, "-opencode-home-")
}
```

Add `"strings"` to the imports in `doctor.go` if not already present.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/sandbox/ -run "TestIsOrphaned" -v`
Expected: PASS

- [ ] **Step 5: Run full test suite and linter**

Run: `go test ./... && golangci-lint run`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/sandbox/doctor.go internal/sandbox/doctor_test.go
git commit -m "feat: add orphaned old-prefixed artifact detection to doctor"
```

---

## Self-Review Checklist

After all tasks are complete, verify:

- [ ] `go test ./...` passes
- [ ] `golangci-lint run` passes
- [ ] `golangci-lint fmt` produces no diff
- [ ] `go run ./cmd/opencode-msb --dry-run` works from inside the repo (produces a slug like `workspace-<8b62>`)
- [ ] The naming table from the spec matches the implementation:

| Entity | Pattern | Implemented? |
|--------|---------|-------------|
| Project slug | `<folder>-<8b62>` | |
| Base image | `opencode-msb/runner-base:latest` | |
| Project runner image | `opencode-msb/runner-<slug>:<8b62>` | |
| Session sandbox | `opencode-msb-sb-<slug>-<branchSlug>` | |
| Task sandbox (prefill) | `opencode-msb-task-prefill-<slug>-<ts>` | |
| Task sandbox (clone) | `opencode-msb-task-clone-<slug>-<ts>` | |
| Main home volume | `opencode-msb-home-<slug>-<8b62>` | |
| Clone home volume | `opencode-msb-clone-<slug>-<8b62>-<ts>` | |

---
