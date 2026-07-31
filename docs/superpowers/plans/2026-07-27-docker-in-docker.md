# Docker-in-Docker Support Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add Docker-in-Docker support to the microsandbox runner so agents can build and run Docker containers inside the VM, controlled entirely by the project Dockerfile's `FROM` line.

**Architecture:** Two published base images — `runner-base:latest` (unchanged) and `runner-base-dind:latest` (extends base, adds Docker CE with `vfs` storage driver). A project Dockerfile's `FROM` line is the single toggle: extend `runner-base-dind` to get Docker, extend `runner-base` (or use no Dockerfile) to skip it. At VM boot, the launcher checks for the `dockerd` binary and starts it as root if present, then polls for socket readiness before launching opencode.

**Tech Stack:** Go 1.26, Docker SDK (`github.com/moby/moby/client`), microsandbox SDK (`github.com/superradcompany/microsandbox/sdk/go`), Debian trixie-slim base, Docker CE apt packages.

## Global Constraints

- `vfs` is the only supported storage driver — `overlay2` fails inside the microsandbox VM (nested overlay mounts rejected by kernel 6.12.91).
- No `sudo` in the dind image — the agent must never gain root. Dockerd is started via `msb.WithExecUser("root")` from the launcher only.
- No CLI flag or launcher-config setting for Docker — the `FROM` line is the only toggle.
- The base image (`Dockerfile`) is unchanged.
- Target platforms: Linux (KVM) and macOS (Apple Silicon).
- Code style: self-explanatory code, minimal abstractions, no inline comments unless code is not self-explanatory, small focused files.
- `force` (`--rebuild`) means "rebuild what's needed with NoCache", not "build everything possible". Dind base is only built when the project Dockerfile references it.

---

## File Structure

| File | Responsibility | Action |
|------|---------------|--------|
| `internal/sandbox/data/Dockerfile.dind` | Dockerfile for `runner-base-dind` image (extends base, installs Docker CE, configures `vfs`) | Create |
| `internal/sandbox/data.go` | Embeds `Dockerfile` and `Dockerfile.dind` as `[]byte` | Modify |
| `internal/sandbox/image.go` | `BaseTag`, `DindBaseTag`, `ReferencesBase`, `ReferencesDindBase`, `EnsureImage`, `buildDockerImage` | Modify |
| `internal/sandbox/image_test.go` | Tests for `ReferencesDindBase`, `EnsureImage` build decisions | Modify |
| `internal/sandbox/docker.go` | `startDockerdIfPresent` — checks for dockerd binary, starts it as root, polls for readiness | Create |
| `internal/sandbox/docker_test.go` | Unit tests for command constants and polling config | Create |
| `internal/sandbox/runner.go` | Call `startDockerdIfPresent` from `prepareSandbox` after provisioning | Modify |

---

### Task 1: Create `Dockerfile.dind` and embed it

**Files:**
- Create: `internal/sandbox/data/Dockerfile.dind`
- Modify: `internal/sandbox/data.go`

**Interfaces:**
- Consumes: nothing (foundational)
- Produces: `EmbeddedDindDockerfile` (`[]byte`) — the dind Dockerfile content, embedded at compile time via `//go:embed`. Used by Task 3 in `EnsureImage`.

**Background:** The existing `internal/sandbox/data.go` embeds `data/Dockerfile` as `EmbeddedDockerfile`. We add a parallel embed for `data/Dockerfile.dind`. The dind Dockerfile extends `runner-base:latest`, switches to `USER root` (because the base ends with `USER dev`), installs Docker CE from Docker's official apt repo for Debian trixie, writes `/etc/docker/daemon.json` with `{"storage-driver":"vfs"}`, adds `dev` to the `docker` group, and switches back to `USER dev`.

- [ ] **Step 1: Create the dind Dockerfile**

Create `internal/sandbox/data/Dockerfile.dind`:

```dockerfile
FROM opencode-msb/runner-base:latest

USER root

RUN apt-get update && \
    apt-get install -y --no-install-recommends \
        ca-certificates gnupg && \
    install -m 0755 -d /etc/apt/keyrings && \
    curl -fsSL https://download.docker.com/linux/debian/gpg -o /etc/apt/keyrings/docker.asc && \
    chmod a+r /etc/apt/keyrings/docker.asc && \
    echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/debian trixie stable" > /etc/apt/sources.list.d/docker.list && \
    apt-get update && \
    apt-get install -y --no-install-recommends docker-ce docker-ce-cli && \
    rm -rf /var/lib/apt/lists/*

RUN mkdir -p /etc/docker && \
    echo '{"storage-driver":"vfs"}' > /etc/docker/daemon.json

RUN usermod -aG docker dev

USER dev
WORKDIR /workspace
```

- [ ] **Step 2: Add the embed to `data.go`**

Modify `internal/sandbox/data.go`. The current content is:

```go
package sandbox

import _ "embed"

//go:embed data/Dockerfile
var EmbeddedDockerfile []byte
```

Add the dind embed after the existing one:

```go
package sandbox

import _ "embed"

//go:embed data/Dockerfile
var EmbeddedDockerfile []byte

//go:embed data/Dockerfile.dind
var EmbeddedDindDockerfile []byte
```

- [ ] **Step 3: Verify the build compiles**

Run: `go build ./internal/sandbox/`
Expected: no errors

- [ ] **Step 4: Add a test verifying the embed is non-empty**

Add to `internal/sandbox/image_test.go`:

```go
func TestEmbeddedDindDockerfileIsNonEmpty(t *testing.T) {
	if len(EmbeddedDindDockerfile) == 0 {
		t.Error("expected EmbeddedDindDockerfile to be non-empty")
	}
}
```

- [ ] **Step 5: Run the test**

Run: `go test ./internal/sandbox/ -run TestEmbeddedDindDockerfileIsNonEmpty -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/sandbox/data/Dockerfile.dind internal/sandbox/data.go internal/sandbox/image_test.go
git commit -m "feat: add Dockerfile.dind and embed it as EmbeddedDindDockerfile"
```

---

### Task 2: Add `ReferencesDindBase` and `DindBaseTag`

**Files:**
- Modify: `internal/sandbox/image.go`
- Modify: `internal/sandbox/image_test.go`

**Interfaces:**
- Consumes: nothing
- Produces:
  - `DindBaseTag` (`string` constant, value `"opencode-msb/runner-base-dind:latest"`) — used by Task 3 in `EnsureImage` as the build tag for the dind base image.
  - `ReferencesDindBase(dockerfile []byte) bool` — scans a Dockerfile's `FROM` lines for `opencode-msb/runner-base-dind:latest`. Used by Task 3 in `EnsureImage` to decide whether to build the dind base.

**Background:** The existing `ReferencesBase` function (in `internal/sandbox/image.go:48-57`) scans `FROM` lines for `BaseTag` (`"opencode-msb/runner-base:latest"`) using `strings.Contains`. Because `strings.Contains("FROM opencode-msb/runner-base-dind:latest", "opencode-msb/runner-base:latest")` is `false` (the substring `runner-base:latest` does not appear in `runner-base-dind:latest`), `ReferencesBase` correctly returns false for dind Dockerfiles. `ReferencesDindBase` follows the same pattern for the dind tag.

- [ ] **Step 1: Write the failing tests**

Add to `internal/sandbox/image_test.go`:

```go
func TestReferencesDindBaseDetectsDindImage(t *testing.T) {
	dockerfile := []byte("FROM opencode-msb/runner-base-dind:latest\nRUN echo hi\n")
	if !ReferencesDindBase(dockerfile) {
		t.Error("expected ReferencesDindBase=true for Dockerfile with dind FROM")
	}
}

func TestReferencesDindBaseReturnsFalseForPlainBase(t *testing.T) {
	dockerfile := []byte("FROM opencode-msb/runner-base:latest\nRUN echo hi\n")
	if ReferencesDindBase(dockerfile) {
		t.Error("expected ReferencesDindBase=false for plain base Dockerfile")
	}
}

func TestReferencesDindBaseReturnsFalseForOtherImage(t *testing.T) {
	dockerfile := []byte("FROM debian:trixie-slim\nRUN echo hi\n")
	if ReferencesDindBase(dockerfile) {
		t.Error("expected ReferencesDindBase=false for non-base Dockerfile")
	}
}

func TestReferencesDindBaseIgnoresComments(t *testing.T) {
	dockerfile := []byte("# FROM opencode-msb/runner-base-dind:latest\nFROM debian:trixie-slim\n")
	if ReferencesDindBase(dockerfile) {
		t.Error("expected ReferencesDindBase=false for commented FROM")
	}
}

func TestReferencesBaseReturnsFalseForDindImage(t *testing.T) {
	dockerfile := []byte("FROM opencode-msb/runner-base-dind:latest\nRUN echo hi\n")
	if ReferencesBase(dockerfile) {
		t.Error("expected ReferencesBase=false for dind Dockerfile (no false positive)")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/sandbox/ -run TestReferencesDindBase -v`
Expected: FAIL with "undefined: ReferencesDindBase"

Run: `go test ./internal/sandbox/ -run TestReferencesBaseReturnsFalseForDindImage -v`
Expected: PASS (existing `ReferencesBase` already handles this correctly)

- [ ] **Step 3: Add `DindBaseTag` constant and `ReferencesDindBase` function**

Modify `internal/sandbox/image.go`. Add `DindBaseTag` next to the existing `BaseTag` constant (around line 25):

```go
const (
	BaseTag        = "opencode-msb/runner-base:latest"
	DindBaseTag    = "opencode-msb/runner-base-dind:latest"
	dockerfileMode = 0o644
)
```

Add `ReferencesDindBase` right after the existing `ReferencesBase` function (after line 57):

```go
func ReferencesDindBase(dockerfile []byte) bool {
	scanner := bufio.NewScanner(bytes.NewReader(dockerfile))
	for scanner.Scan() {
		line := strings.TrimLeft(scanner.Text(), " \t")
		if strings.HasPrefix(line, "FROM") && strings.Contains(line, DindBaseTag) {
			return true
		}
	}
	return false
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/sandbox/ -run "TestReferencesDindBase|TestReferencesBaseReturnsFalseForDindImage" -v`
Expected: all PASS

- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/image.go internal/sandbox/image_test.go
git commit -m "feat: add ReferencesDindBase and DindBaseTag constant"
```

---

### Task 3: Extend `EnsureImage` to build dind base

**Files:**
- Modify: `internal/sandbox/image.go`
- Modify: `internal/sandbox/image_test.go`

**Interfaces:**
- Consumes: `ReferencesDindBase` (Task 2), `EmbeddedDindDockerfile` (Task 1), `DindBaseTag` (Task 2)
- Produces: modified `EnsureImage` that builds `runner-base-dind` when the project Dockerfile references it.

**Background:** The existing `EnsureImage` (in `internal/sandbox/image.go:100-157`) builds the plain base image when `force || ReferencesBase(dockerfile)`, then builds the project image. We extend this to also build the dind base when `ReferencesDindBase(dockerfile)` is true. The dind Dockerfile (`Dockerfile.dind`) uses `FROM opencode-msb/runner-base:latest`, so the plain base must be built first. The condition for building plain base is extended to include `ReferencesDindBase` (because dind extends it). Dind base is only built when the project Dockerfile references it — `force` does not unconditionally build dind (unlike plain base, which `force` always builds because the embedded Dockerfile IS the plain base).

The existing test mocks in `image_test.go` are `failingDockerClient` (fails on all operations) and `recordingDockerClient` (succeeds on `ImageBuild`, fails on `ImageInspect`/`ImageSave`). We add a `tagTrackingDockerClient` that succeeds on `ImageBuild` (recording which tags were built) and fails on `ImageInspect` (so the test never reaches `ImageSave`/`msb load`). This lets us verify which images were built before the inspect failure.

- [ ] **Step 1: Write the failing tests**

Add to `internal/sandbox/image_test.go`:

```go
type tagTrackingDockerClient struct {
	builtTags []string
}

func (t *tagTrackingDockerClient) ImageBuild(
	_ context.Context,
	_ ui.Reader,
	opts client.ImageBuildOptions,
) (client.ImageBuildResult, error) {
	t.builtTags = append(t.builtTags, opts.Tags...)
	return client.ImageBuildResult{Body: ui.NopCloser(bytes.NewReader(nil))}, nil
}

func (t *tagTrackingDockerClient) ImageInspect(
	context.Context,
	string,
	...client.ImageInspectOption,
) (client.ImageInspectResult, error) {
	return client.ImageInspectResult{}, errors.New("not implemented")
}

func (t *tagTrackingDockerClient) ImageSave(
	context.Context,
	[]string,
	...client.ImageSaveOption,
) (client.ImageSaveResult, error) {
	return nil, errors.New("not implemented")
}

func (t *tagTrackingDockerClient) Close() error {
	return nil
}

func TestEnsureImageBuildsDindBaseWhenDockerfileReferencesDind(t *testing.T) {
	cli := &tagTrackingDockerClient{}
	dockerfile := []byte("FROM opencode-msb/runner-base-dind:latest\nRUN echo hi\n")
	_, _, err := EnsureImage(context.Background(), cli, dockerfile, "test-project", false, newTestLogger(t))
	if err == nil {
		t.Fatal("expected error from ImageInspect, got nil")
	}
	wantTags := []string{BaseTag, DindBaseTag, "opencode-msb/runner-test-project:latest"}
	if !reflect.DeepEqual(cli.builtTags, wantTags) {
		t.Errorf("built tags:\n  got:  %v\n  want: %v", cli.builtTags, wantTags)
	}
}

func TestEnsureImageDoesNotBuildDindForPlainBase(t *testing.T) {
	cli := &tagTrackingDockerClient{}
	dockerfile := []byte("FROM opencode-msb/runner-base:latest\nRUN echo hi\n")
	_, _, err := EnsureImage(context.Background(), cli, dockerfile, "test-project", false, newTestLogger(t))
	if err == nil {
		t.Fatal("expected error from ImageInspect, got nil")
	}
	wantTags := []string{BaseTag, "opencode-msb/runner-test-project:latest"}
	if !reflect.DeepEqual(cli.builtTags, wantTags) {
		t.Errorf("built tags:\n  got:  %v\n  want: %v", cli.builtTags, wantTags)
	}
}

func TestEnsureImageDoesNotBuildDindOnForceWithoutReference(t *testing.T) {
	cli := &tagTrackingDockerClient{}
	dockerfile := []byte("FROM debian:trixie-slim\nRUN echo hi\n")
	_, _, err := EnsureImage(context.Background(), cli, dockerfile, "test-project", true, newTestLogger(t))
	if err == nil {
		t.Fatal("expected error from ImageInspect, got nil")
	}
	wantTags := []string{BaseTag, "opencode-msb/runner-test-project:latest"}
	if !reflect.DeepEqual(cli.builtTags, wantTags) {
		t.Errorf("built tags:\n  got:  %v\n  want: %v", cli.builtTags, wantTags)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/sandbox/ -run "TestEnsureImageBuildsDind|TestEnsureImageDoesNotBuildDind" -v`
Expected: FAIL — `TestEnsureImageBuildsDindBaseWhenDockerfileReferencesDind` fails because dind base is not built (only `BaseTag` and runner tag are in `builtTags`).

- [ ] **Step 3: Extend `EnsureImage` to build dind base**

Modify `internal/sandbox/image.go`. The current `EnsureImage` function (lines 100-157) starts with:

```go
func EnsureImage(
	ctx context.Context,
	cli dockerClient,
	dockerfile []byte,
	projectSlug string,
	force bool,
	ui *stdio.IO,
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
```

Replace the base-build condition and add a dind-build block after it:

```go
func EnsureImage(
	ctx context.Context,
	cli dockerClient,
	dockerfile []byte,
	projectSlug string,
	force bool,
	ui *stdio.IO,
) (string, string, error) {
	if force || ReferencesBase(dockerfile) || ReferencesDindBase(dockerfile) {
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

	if ReferencesDindBase(dockerfile) {
		if err := buildDockerImage(
			ctx,
			cli,
			EmbeddedDindDockerfile,
			DindBaseTag,
			"Building Docker-in-Docker base image",
			force,
			logger,
		); err != nil {
			return "", "", fmt.Errorf("building dind base image: %w", err)
		}
	}

	rTag := runnerTag(projectSlug)
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/sandbox/ -run "TestEnsureImageBuildsDind|TestEnsureImageDoesNotBuildDind|TestEnsureImageReturnsErrorWhenBuildFails" -v`
Expected: all PASS

- [ ] **Step 5: Run the full test suite to check for regressions**

Run: `go test ./internal/sandbox/ -v`
Expected: all PASS

- [ ] **Step 6: Commit**

```bash
git add internal/sandbox/image.go internal/sandbox/image_test.go
git commit -m "feat: extend EnsureImage to build dind base when project references it"
```

---

### Task 4: Add dockerd startup logic

**Files:**
- Create: `internal/sandbox/docker.go`
- Create: `internal/sandbox/docker_test.go`

**Interfaces:**
- Consumes: `*msb.Sandbox` (for `Shell` with `WithExecUser`), `*output.Printer` (for logging)
- Produces: `startDockerdIfPresent(ctx context.Context, sb *msb.Sandbox, ui *stdio.IO) error` — checks for `dockerd` binary, starts it as root with `vfs` config, polls for socket readiness. Called by Task 5 from `prepareSandbox`.

**Background:** After the VM starts, the launcher needs to start `dockerd` if the image has Docker installed. The `vfs` storage driver is pre-configured in `/etc/docker/daemon.json` baked into the dind image, so the launcher only starts `dockerd` and polls. The check uses `test -x /usr/bin/dockerd` as root. If the binary is absent (plain base image), the function returns nil (no-op). If present, dockerd starts in the background, and the launcher polls `docker info` as `dev` until it succeeds or times out.

The `dev` user is in the `docker` group (added in `Dockerfile.dind`), so `docker info` as `dev` verifies both daemon readiness and user permissions.

`*msb.Sandbox` is a concrete struct (not an interface), so unit tests cover the command constants and configuration values. The actual execution is tested via integration tests (Task 6).

- [ ] **Step 1: Write the failing tests**

Create `internal/sandbox/docker_test.go`:

```go
package sandbox

import (
	"testing"
	"time"
)

func TestDockerdCheckCmdChecksBinaryPresence(t *testing.T) {
	want := "test -x /usr/bin/dockerd"
	if dockerdCheckCmd != want {
		t.Errorf("dockerdCheckCmd:\n  got:  %q\n  want: %q", dockerdCheckCmd, want)
	}
}

func TestDockerdStartCmdUsesUnixSocketAndVfsConfig(t *testing.T) {
	want := "dockerd -H unix:///var/run/docker.sock > /var/log/dockerd.log 2>&1 &"
	if dockerdStartCmd != want {
		t.Errorf("dockerdStartCmd:\n  got:  %q\n  want: %q", dockerdStartCmd, want)
	}
}

func TestDockerdReadyCmdRunsDockerInfo(t *testing.T) {
	want := "docker info"
	if dockerdReadyCmd != want {
		t.Errorf("dockerdReadyCmd:\n  got:  %q\n  want: %q", dockerdReadyCmd, want)
	}
}

func TestDockerdReadyTimeoutIs30Seconds(t *testing.T) {
	if dockerdReadyTimeout != 30*time.Second {
		t.Errorf("dockerdReadyTimeout:\n  got:  %v\n  want: 30s", dockerdReadyTimeout)
	}
}

func TestDockerdPollIntervalIsOneSecond(t *testing.T) {
	if dockerdPollInterval != time.Second {
		t.Errorf("dockerdPollInterval:\n  got:  %v\n  want: 1s", dockerdPollInterval)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/sandbox/ -run TestDockerd -v`
Expected: FAIL with "undefined: dockerdCheckCmd" etc.

- [ ] **Step 3: Create the implementation**

Create `internal/sandbox/docker.go`:

```go
package sandbox

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gitlab.inoio.de/inoio/opencode-msb/internal/output"

	msb "github.com/superradcompany/microsandbox/sdk/go"
)

const (
	dockerdCheckCmd      = "test -x /usr/bin/dockerd"
	dockerdStartCmd      = "dockerd -H unix:///var/run/docker.sock > /var/log/dockerd.log 2>&1 &"
	dockerdReadyCmd      = "docker info"
	dockerdReadyTimeout  = 30 * time.Second
	dockerdPollInterval  = time.Second
)

func startDockerdIfPresent(ctx context.Context, sb *msb.Sandbox, ui *stdio.IO) error {
	out, err := sb.Shell(ctx, dockerdCheckCmd, msb.WithExecUser("root"))
	if err != nil {
		return fmt.Errorf("check dockerd binary: %w", err)
	}
	if !out.Success() {
		logger.Debugf("dockerd not present, skipping Docker startup")
		return nil
	}

	logger.Debugf("starting dockerd with vfs storage driver")
	if _, err := sb.Shell(ctx, dockerdStartCmd, msb.WithExecUser("root")); err != nil {
		return fmt.Errorf("start dockerd: %w", err)
	}

	deadline := time.Now().Add(dockerdReadyTimeout)
	for time.Now().Before(deadline) {
		out, err := sb.Shell(ctx, dockerdReadyCmd, msb.WithExecUser("dev"))
		if err == nil && out.Success() {
			logger.Debugf("dockerd is ready")
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(dockerdPollInterval):
		}
	}
	return errors.New("dockerd did not become ready within " + dockerdReadyTimeout.String())
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/sandbox/ -run TestDockerd -v`
Expected: all PASS

- [ ] **Step 5: Verify the build compiles**

Run: `go build ./internal/sandbox/`
Expected: no errors

- [ ] **Step 6: Commit**

```bash
git add internal/sandbox/docker.go internal/sandbox/docker_test.go
git commit -m "feat: add startDockerdIfPresent for conditional dockerd startup"
```

---

### Task 5: Wire dockerd startup into `prepareSandbox`

**Files:**
- Modify: `internal/sandbox/runner.go`

**Interfaces:**
- Consumes: `startDockerdIfPresent` (Task 4)
- Produces: modified `prepareSandbox` that starts dockerd after provisioning, before returning the session.

**Background:** `prepareSandbox` (in `internal/sandbox/runner.go:463-564`) creates the sandbox VM, provisions config files, and returns a `sandboxSession`. Both `Run` and `Shell` call `prepareSandbox`, then attach to the sandbox. The dockerd startup is inserted after `provisionSandbox` returns (line 549-552) and before the session is returned (line 554). This ensures Docker is ready before either `Run` or `Shell` attaches.

The call is placed after the defer that cleans up the sandbox on error (lines 538-546), so if dockerd startup fails, the sandbox is properly cleaned up.

- [ ] **Step 1: Read the current `prepareSandbox` to identify the insertion point**

Read `internal/sandbox/runner.go` around lines 548-564. The current code after provisioning:

```go
	fs := sb.FS()
	err = provisionSandbox(ctx, fs, configFiles, repoPath, logger)
	if err != nil {
		return nil, err
	}

	return &sandboxSession{
		sb:        sb,
		name:      name,
		repoPath:  repoPath,
		cwd:       cwd,
		cwdBranch: cwdBranch,
		created:   created,
		branch:    branch,
		cloneVol:  cloneVol,
	}, nil
```

- [ ] **Step 2: Add the dockerd startup call**

Modify `internal/sandbox/runner.go`. Insert the dockerd startup between `provisionSandbox` and the return statement:

```go
	fs := sb.FS()
	err = provisionSandbox(ctx, fs, configFiles, repoPath, logger)
	if err != nil {
		return nil, err
	}

	if err := startDockerdIfPresent(ctx, sb, logger); err != nil {
		return nil, fmt.Errorf("docker startup: %w", err)
	}

	return &sandboxSession{
		sb:        sb,
		name:      name,
		repoPath:  repoPath,
		cwd:       cwd,
		cwdBranch: cwdBranch,
		created:   created,
		branch:    branch,
		cloneVol:  cloneVol,
	}, nil
```

- [ ] **Step 3: Verify the build compiles**

Run: `go build ./...`
Expected: no errors

- [ ] **Step 4: Run the full test suite**

Run: `go test ./... -short`
Expected: all PASS (integration tests are skipped with `-short` or `//go:build integration`)

- [ ] **Step 5: Run linter**

Run: `golangci-lint run ./internal/sandbox/...`
Expected: no errors

- [ ] **Step 6: Commit**

```bash
git add internal/sandbox/runner.go
git commit -m "feat: wire startDockerdIfPresent into prepareSandbox"
```

---

### Task 6: Integration test for dockerd startup

**Files:**
- Modify: `internal/sandbox/integration_test.go`

**Interfaces:**
- Consumes: `startDockerdIfPresent` (Task 4), msb runtime
- Produces: integration test verifying dockerd starts and `docker run --rm hello-world` succeeds in a dind image.

**Background:** The existing integration tests in `internal/sandbox/integration_test.go` use `//go:build integration` build tag and require a running msb runtime. They create real sandboxes with `msb.CreateSandbox`. We add a test that builds the dind base image, starts a sandbox, runs `startDockerdIfPresent`, and verifies Docker works.

This test is skipped in normal CI (requires `//go:build integration` tag and a running msb runtime with Docker available on the host).

- [ ] **Step 1: Write the integration test**

Add to `internal/sandbox/integration_test.go`:

```go
func TestStartDockerdIfPresentWithDindImage(t *testing.T) {
	ctx := t.Context()
	logger := newTestLogger(t)

	// Build the dind base image requires Docker on the host.
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dockerCli, err := client.New(client.FromEnv)
	if err != nil {
		t.Skipf("docker not available: %v", err)
	}
	defer dockerCli.Close()

	// Build the plain base image first (dind extends it).
	if err := buildDockerImage(ctx, dockerCli, EmbeddedDockerfile, BaseTag, "Building base", false, logger); err != nil {
		t.Skipf("cannot build base image: %v", err)
	}

	// Build the dind base image.
	if err := buildDockerImage(ctx, dockerCli, EmbeddedDindDockerfile, DindBaseTag, "Building dind base", false, logger); err != nil {
		t.Skipf("cannot build dind image: %v", err)
	}

	// Load into msb.
	imageRef := DindBaseTag
	if _, err := msb.Image.Get(ctx, imageRef); err != nil {
		saveResult, err := dockerCli.ImageSave(ctx, []string{DindBaseTag})
		if err != nil {
			t.Skipf("cannot export dind image: %v", err)
		}
		defer saveResult.Close()
		cmd := exec.CommandContext(ctx, "msb", "load", "--tag", imageRef)
		cmd.Stdin = saveResult
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("cannot load dind image into msb: %v: %s", err, out)
		}
	}

	sandboxName := fmt.Sprintf("test-dind-%d", time.Now().UnixNano())
	sb, err := msb.CreateSandbox(ctx, sandboxName,
		msb.WithImage(imageRef),
		msb.WithUser("dev"),
		msb.WithWorkdir("/workspace"),
		msb.WithReplace(),
	)
	if err != nil {
		t.Skipf("cannot create sandbox: %v", err)
	}
	defer func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = sb.Stop(stopCtx)
		_ = sb.Close()
		_ = msb.RemoveSandbox(context.Background(), sandboxName)
	}()

	if err := startDockerdIfPresent(ctx, sb, logger); err != nil {
		t.Fatalf("startDockerdIfPresent failed: %v", err)
	}

	out, err := sb.Shell(ctx, "docker run --rm hello-world", msb.WithExecUser("dev"))
	if err != nil {
		t.Fatalf("docker run hello-world failed: %v", err)
	}
	if !out.Success() {
		t.Fatalf("docker run hello-world exited non-zero:\n%s\n%s", out.Stdout(), out.Stderr())
	}
	if !strings.Contains(out.Stdout(), "Hello from Docker!") {
		t.Errorf("expected hello-world output, got:\n%s", out.Stdout())
	}
}

func TestStartDockerdIfPresentWithPlainBaseImage(t *testing.T) {
	ctx := t.Context()
	logger := newTestLogger(t)

	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dockerCli, err := client.New(client.FromEnv)
	if err != nil {
		t.Skipf("docker not available: %v", err)
	}
	defer dockerCli.Close()

	if err := buildDockerImage(ctx, dockerCli, EmbeddedDockerfile, BaseTag, "Building base", false, logger); err != nil {
		t.Skipf("cannot build base image: %v", err)
	}

	imageRef := BaseTag
	if _, err := msb.Image.Get(ctx, imageRef); err != nil {
		saveResult, err := dockerCli.ImageSave(ctx, []string{BaseTag})
		if err != nil {
			t.Skipf("cannot export base image: %v", err)
		}
		defer saveResult.Close()
		cmd := exec.CommandContext(ctx, "msb", "load", "--tag", imageRef)
		cmd.Stdin = saveResult
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("cannot load base image into msb: %v: %s", err, out)
		}
	}

	sandboxName := fmt.Sprintf("test-plain-%d", time.Now().UnixNano())
	sb, err := msb.CreateSandbox(ctx, sandboxName,
		msb.WithImage(imageRef),
		msb.WithUser("dev"),
		msb.WithWorkdir("/workspace"),
		msb.WithReplace(),
	)
	if err != nil {
		t.Skipf("cannot create sandbox: %v", err)
	}
	defer func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = sb.Stop(stopCtx)
		_ = sb.Close()
		_ = msb.RemoveSandbox(context.Background(), sandboxName)
	}()

	// Should be a no-op on plain base (no dockerd binary).
	if err := startDockerdIfPresent(ctx, sb, logger); err != nil {
		t.Fatalf("startDockerdIfPresent should be no-op on plain base, got: %v", err)
	}
}
```

- [ ] **Step 2: Add missing imports to integration_test.go**

The existing integration_test.go imports `context`, `fmt`, `testing`, `time`, and `msb`. Add:

```go
import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/client"

	msb "github.com/superradcompany/microsandbox/sdk/go"
)
```

- [ ] **Step 3: Verify the build compiles with integration tag**

Run: `go build -tags integration ./internal/sandbox/`
Expected: no errors

- [ ] **Step 4: Run unit tests (without integration tag) to verify no regressions**

Run: `go test ./internal/sandbox/ -v`
Expected: all PASS (integration tests are not compiled)

- [ ] **Step 5: Run linter**

Run: `golangci-lint run ./internal/sandbox/...`
Expected: no errors

- [ ] **Step 6: Commit**

```bash
git add internal/sandbox/integration_test.go
git commit -m "test: add integration tests for dockerd startup with dind and plain base"
```

---

## Self-Review

**1. Spec coverage:**

| Spec Section | Task(s) |
|---|---|
| Two published base images | Task 1 (Dockerfile.dind) |
| Two embedded Dockerfiles | Task 1 (embed in data.go) |
| Base image detection (`ReferencesDindBase`) | Task 2 |
| Image build flow (dind extends base) | Task 3 |
| Dockerd startup at VM boot (binary check, sequential) | Task 4 |
| User privileges (`WithExecUser("root")` for dockerd, `dev` for readiness) | Task 4 |
| `dev` in `docker` group | Task 1 (in Dockerfile.dind) |
| Testing: unit tests for `ReferencesDindBase`, `ReferencesBase` false positive | Task 2 |
| Testing: `EnsureImage` build decisions | Task 3 |
| Testing: dockerd startup command construction | Task 4 |
| Testing: integration tests for dind and plain base | Task 6 |
| Wired into `prepareSandbox` | Task 5 |
| No CLI flag / config changes | Confirmed: no changes to `cli.go` or `launcherconfig` |

**2. Placeholder scan:** No TBD, TODO, or vague steps. All code blocks contain actual implementation.

**3. Type consistency:**
- `EmbeddedDindDockerfile` ([]byte) — defined Task 1, used Task 3 ✓
- `DindBaseTag` (string const) — defined Task 2, used Task 3 ✓
- `ReferencesDindBase(dockerfile []byte) bool` — defined Task 2, used Task 3 ✓
- `startDockerdIfPresent(ctx context.Context, sb *msb.Sandbox, ui *stdio.IO) error` — defined Task 4, used Task 5 ✓
- `dockerdCheckCmd`, `dockerdStartCmd`, `dockerdReadyCmd`, `dockerdReadyTimeout`, `dockerdPollInterval` — defined Task 4, tested Task 4 ✓
