# Login-shell PATH preservation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the runner image's composed `PATH` (custom Dockerfile `ENV PATH`, `/opt/agents-sandbox/bin`, project toolchains) survive the login shell's `/etc/profile` reset, by re-merging missing entries from `AGENTS_SANDBOX_IMAGE_PATH` in a `/etc/profile.d` drop-in.

**Architecture:** Image-only change. The final stage records its composed `PATH` in `ENV AGENTS_SANDBOX_IMAGE_PATH="${PATH}"` and installs a POSIX-sh merge script via `COPY` into `/etc/profile.d/`, which `/etc/profile` sources after its `PATH` reset. No launcher/session changes. This replaces the `/usr/local/bin` symlink stopgap.

**Tech Stack:** Go (module `github.com/inoio/agents-sandbox`), Docker (classic builder API via `internal/sandbox/docker`), POSIX `sh`, `go:embed`.

**Spec:** `.operator-shared/specs/login-shell-path.md`

## Global Constraints

- Idiomatic Go; self-documenting, non-abbreviated identifiers; no comments unless they add durable decision value.
- Format/lint with `make fmt` / `make lint` (`golangci-lint`); **never** `go vet`. Run `make check` when finalizing.
- `buildImage` uses the **classic** Docker builder (no BuildKit): the rendered Dockerfile must be classic-builder compatible — **no heredocs in `RUN`**. Write shipped files by adding them to the synthetic build context and using `COPY`.
- Keep `docs/` (`docs/runner-image.md`) and `.operator-shared/specs/image-composition.md` in sync; add a `[Unreleased]` line to `CHANGELOG.md`.
- Target platforms: Linux (KVM) and macOS (Apple Silicon); the change is platform-independent.
- `make test-integration` runs all `integration`-tagged tests (`./...`) and needs a Docker daemon + network.
- Do **not** commit/push on the `/workspace` bind mount — leave finalization to the user.

## Review Focus

The five inputs/conditions the spec implies but no task's own happy-path test exercises; each has a test attached to its owning task:

1. A `PATH` entry that is a prefix of another (`/opt` vs `/opt/agents-sandbox/bin`) must not be treated as present → colon-delimited match (Task 1).
2. Empty/unset `AGENTS_SANDBOX_IMAGE_PATH` must be a no-op, adding no entries and no trailing colon (Task 1).
3. Sourcing the merge script twice (nested login shells) must not duplicate entries (Task 1).
4. A custom base without an existing `/etc/profile.d` must still build; `COPY` creates the parent dir (Task 2 tests the render; Task 3 builds a custom base).
5. Login-shell additions (`/.msb/scripts`, `~/.local/bin`) must keep their position; the merge appends, never rebuilds `PATH` (Task 1).

---

### Task 1: Merge script and its POSIX-sh behaviour tests

**Files:**
- Create: `internal/sandbox/image/data/agents-sandbox-path.sh`
- Test: `internal/sandbox/image/path_merge_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: the on-disk script `internal/sandbox/image/data/agents-sandbox-path.sh`, a POSIX-sh snippet safe to `.`-source; reads env var `AGENTS_SANDBOX_IMAGE_PATH`; appends its not-yet-present colon-separated entries to `PATH`; exports `PATH`; no-op when `AGENTS_SANDBOX_IMAGE_PATH` is unset/empty; idempotent. Later tasks embed this exact file.

- [ ] **Step 1: Write the failing test**

Create `internal/sandbox/image/path_merge_test.go`:

````go
package image

import (
	"os/exec"
	"path/filepath"
	"testing"
)

// runMergeScript sources data/agents-sandbox-path.sh in a fresh POSIX shell
// with the given environment and returns the resulting PATH. A source count > 1
// exercises re-sourcing (nested login shells).
func runMergeScript(t *testing.T, path, imagePath string, setImage bool, sources int) string {
	t.Helper()
	script, err := filepath.Abs("data/agents-sandbox-path.sh")
	if err != nil {
		t.Fatalf("abs script path: %v", err)
	}
	shell := ". " + script
	if sources > 1 {
		shell += "; . " + script
	}
	shell += `; printf '%s' "$PATH"`

	cmd := exec.Command("sh", "-c", shell)
	cmd.Env = []string{"PATH=" + path}
	if setImage {
		cmd.Env = append(cmd.Env, "AGENTS_SANDBOX_IMAGE_PATH="+imagePath)
	}
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("sh merge script: %v", err)
	}
	return string(out)
}

func TestAgentsSandboxPathMergeScript(t *testing.T) {
	cases := []struct {
		name       string
		path       string
		imagePath  string
		setImage   bool
		sources    int
		want       string
	}{
		{
			name:      "appends missing image entries in image order",
			path:      "/usr/local/bin:/usr/bin",
			imagePath: "/usr/local/bin:/opt/agents-sandbox/bin:/usr/local/go/bin",
			setImage:  true,
			sources:   1,
			want:      "/usr/local/bin:/usr/bin:/opt/agents-sandbox/bin:/usr/local/go/bin",
		},
		{
			name:      "preserves login additions and their order",
			path:      "/.msb/scripts:/usr/local/bin:/usr/bin",
			imagePath: "/usr/local/go/bin:/usr/local/bin",
			setImage:  true,
			sources:   1,
			want:      "/.msb/scripts:/usr/local/bin:/usr/bin:/usr/local/go/bin",
		},
		{
			name:      "adds nothing when all entries present",
			path:      "/usr/local/bin:/opt/agents-sandbox/bin",
			imagePath: "/usr/local/bin:/opt/agents-sandbox/bin",
			setImage:  true,
			sources:   1,
			want:      "/usr/local/bin:/opt/agents-sandbox/bin",
		},
		{
			name:      "prefix of an existing entry is still appended",
			path:      "/opt/agents-sandbox/bin",
			imagePath: "/opt",
			setImage:  true,
			sources:   1,
			want:      "/opt/agents-sandbox/bin:/opt",
		},
		{
			name:      "no-op when image path is unset",
			path:      "/usr/local/bin:/usr/bin",
			setImage:  false,
			sources:   1,
			want:      "/usr/local/bin:/usr/bin",
		},
		{
			name:      "no-op when image path is empty",
			path:      "/usr/local/bin",
			imagePath: "",
			setImage:  true,
			sources:   1,
			want:      "/usr/local/bin",
		},
		{
			name:      "idempotent when sourced twice",
			path:      "/usr/local/bin:/usr/bin",
			imagePath: "/usr/local/bin:/opt/agents-sandbox/bin",
			setImage:  true,
			sources:   2,
			want:      "/usr/local/bin:/usr/bin:/opt/agents-sandbox/bin",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := runMergeScript(t, tc.path, tc.imagePath, tc.setImage, tc.sources)
			if got != tc.want {
				t.Errorf("PATH = %q, want %q", got, tc.want)
			}
		})
	}
}
````

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/sandbox/image/ -run TestAgentsSandboxPathMergeScript -count=1`
Expected: FAIL — `. data/agents-sandbox-path.sh: No such file or directory` (script not created yet).

- [ ] **Step 3: Write the script**

Create `internal/sandbox/image/data/agents-sandbox-path.sh` with exactly:

```sh
# Restore the runner image's composed PATH after a login shell resets it.
#
# /etc/profile on Debian and most distributions replaces PATH with a hard-coded
# default, discarding the entries the image set via ENV (the appended
# /opt/agents-sandbox/bin, project Dockerfile PATH entries, sbin dirs).
# /etc/profile sources /etc/profile.d/*.sh after that reset, so this re-appends
# any AGENTS_SANDBOX_IMAGE_PATH entry missing from PATH, keeping the login
# shell's own entries and their order untouched.
if [ -n "${AGENTS_SANDBOX_IMAGE_PATH:-}" ]; then
  _as_merge_old_ifs=$IFS
  IFS=:
  for _as_merge_dir in $AGENTS_SANDBOX_IMAGE_PATH; do
    case ":$PATH:" in
      *":$_as_merge_dir:"*) ;;
      *) PATH="${PATH:+$PATH:}$_as_merge_dir" ;;
    esac
  done
  IFS=$_as_merge_old_ifs
  export PATH
  unset _as_merge_old_ifs _as_merge_dir
fi
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/sandbox/image/ -run TestAgentsSandboxPathMergeScript -count=1`
Expected: PASS (all 7 subtests).

- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/image/data/agents-sandbox-path.sh internal/sandbox/image/path_merge_test.go
git commit -m "feat(image): add profile.d merge script for login-shell PATH"
```

---

### Task 2: Ship the script and the authoritative PATH env var; drop the symlink stopgap

**Files:**
- Modify: `internal/sandbox/image/embed.go`
- Modify: `internal/sandbox/image/build.go` (`dockerfileTar`, around `build.go:255`)
- Modify: `internal/sandbox/image/dockerfile.go` (finalizationBlock ~`dockerfile.go:433`; remove `toolBinLinksBlock` ~`dockerfile.go:277` and its call ~`dockerfile.go:271`)
- Modify: `internal/sandbox/image/image_test.go` (`TestDockerfileTarContainsDockerfile`, ~`image_test.go:192`)
- Modify: `internal/sandbox/image/render_test.go` (remove `TestRenderDockerfileToolBinLinkedIntoLoginPath`, ~`render_test.go:128`; add render assertions)

**Interfaces:**
- Consumes: `data/agents-sandbox-path.sh` from Task 1.
- Produces:
  - `pathMergeScript() []byte` — the embedded script contents (package-private).
  - const `pathMergeAsset = "agents-sandbox-path.sh"` and `pathMergeDest = "/etc/profile.d/agents-sandbox-path.sh"`.
  - `dockerfileTar(dockerfile []byte) (*bytes.Buffer, error)` now emits a second tar entry named `pathMergeAsset` with `pathMergeScript()` as content (signature unchanged).
  - Rendered final stage contains `COPY agents-sandbox-path.sh /etc/profile.d/agents-sandbox-path.sh` and `ENV AGENTS_SANDBOX_IMAGE_PATH="${PATH}"` after the final `ENV PATH`.

- [ ] **Step 1: Write the failing tests**

In `internal/sandbox/image/render_test.go`, delete `TestRenderDockerfileToolBinLinkedIntoLoginPath` (its behaviour is replaced), and add:

```go
// TestRenderDockerfileSetsAuthoritativeImagePath guards that the composed PATH
// is recorded for the profile.d merge, after the final PATH directive so it
// captures every ENV PATH contribution.
func TestRenderDockerfileSetsAuthoritativeImagePath(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	final := finalStage(t, mustRender(t, a, nil, true))
	pathIdx := strings.Index(final, `ENV PATH="/home/dev/.local/bin:${PATH}"`)
	imgIdx := strings.Index(final, `ENV AGENTS_SANDBOX_IMAGE_PATH="${PATH}"`)
	if imgIdx < 0 {
		t.Fatalf("final stage must record AGENTS_SANDBOX_IMAGE_PATH; got:\n%s", final)
	}
	if pathIdx < 0 || imgIdx < pathIdx {
		t.Errorf("AGENTS_SANDBOX_IMAGE_PATH must be set after the final PATH directive; got:\n%s", final)
	}
}

// TestRenderDockerfileInstallsProfilePathScript guards that the merge script is
// installed into /etc/profile.d so login shells (which source it after the PATH
// reset) pick up the image's entries.
func TestRenderDockerfileInstallsProfilePathScript(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	final := finalStage(t, mustRender(t, a, nil, true))
	if !strings.Contains(final, "COPY "+pathMergeAsset+" "+pathMergeDest) {
		t.Errorf("final stage must COPY %s to %s; got:\n%s", pathMergeAsset, pathMergeDest, final)
	}
}
```

In `internal/sandbox/image/image_test.go`, extend the tar test (rename to `TestDockerfileTarContents`) or add a second test asserting the tar also contains the script:

```go
func TestDockerfileTarContainsPathMergeScript(t *testing.T) {
	tarBuf, err := dockerfileTar([]byte("FROM debian:trixie-slim\n"))
	if err != nil {
		t.Fatalf("dockerfileTar failed: %v", err)
	}
	entries := map[string]string{}
	tr := tar.NewReader(tarBuf)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("read tar: %v", err)
		}
		content, err := io.ReadAll(tr)
		if err != nil {
			t.Fatalf("read tar entry: %v", err)
		}
		entries[header.Name] = string(content)
	}
	if got := entries[pathMergeAsset]; got != string(pathMergeScript()) {
		t.Errorf("tar entry %q = %q, want embedded script", pathMergeAsset, got)
	}
	if _, ok := entries["Dockerfile"]; !ok {
		t.Errorf("tar must still contain the Dockerfile entry")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/sandbox/image/ -run 'TestRenderDockerfileSetsAuthoritativeImagePath|TestRenderDockerfileInstallsProfilePathScript|TestDockerfileTarContainsPathMergeScript' -count=1`
Expected: FAIL — `undefined: pathMergeAsset` / missing `ENV AGENTS_SANDBOX_IMAGE_PATH`.

- [ ] **Step 3: Implement embed, context, and renderer**

In `internal/sandbox/image/embed.go`, add:

```go
//go:embed data/agents-sandbox-path.sh
var embeddedPathMergeScript []byte

// pathMergeScript returns the profile.d merge script shipped in the build
// context and installed into the image.
func pathMergeScript() []byte { return embeddedPathMergeScript }
```

In `internal/sandbox/image/dockerfile.go`, add constants near the other asset constants:

```go
const (
	pathMergeAsset = "agents-sandbox-path.sh"
	pathMergeDest  = "/etc/profile.d/agents-sandbox-path.sh"
)
```

Remove `toolBinLinksBlock` and its entry in `runnerStage`'s `parts` list. In `finalizationBlock`, add the COPY after `USER root` and the ENV after the `ENV PATH` line:

```
USER root
COPY agents-sandbox-path.sh /etc/profile.d/agents-sandbox-path.sh
ARG BASE_IMAGE
ARG DOCKERFILE_ID

USER dev
# extend PATH with ~/.local/bin
ENV PATH="/home/dev/.local/bin:${PATH}"
ENV AGENTS_SANDBOX_IMAGE_PATH="${PATH}"
WORKDIR /workspace
LABEL org.agents-sandbox.managed=true
...
```

Keep the `finalizationBlock` string using the `pathMergeAsset`/`pathMergeDest` constants via `fmt.Sprintf` so the render and the build context cannot drift apart.

In `internal/sandbox/image/build.go`, extend `dockerfileTar` to write the script entry after the Dockerfile entry:

```go
	for _, entry := range []struct {
		name    string
		content []byte
	}{
		{"Dockerfile", dockerfile},
		{pathMergeAsset, pathMergeScript()},
	} {
		if err := tw.WriteHeader(&tar.Header{
			Name: entry.name,
			Mode: dockerfileMode,
			Size: int64(len(entry.content)),
		}); err != nil {
			_ = tw.Close()
			return nil, fmt.Errorf("tar write header: %w", err)
		}
		if _, err := io.Copy(tw, bytes.NewReader(entry.content)); err != nil {
			_ = tw.Close()
			return nil, fmt.Errorf("tar write %s: %w", entry.name, err)
		}
	}
```

Keep `TestDockerfileTarContainsDockerfile` valid (the `Dockerfile` entry is still first).

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/sandbox/image/ -count=1`
Expected: PASS (whole package).

- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/image/embed.go internal/sandbox/image/build.go internal/sandbox/image/dockerfile.go internal/sandbox/image/image_test.go internal/sandbox/image/render_test.go
git commit -m "feat(image): record AGENTS_SANDBOX_IMAGE_PATH and install merge script; drop /usr/local/bin symlinks"
```

---

### Task 3: End-to-end integration test for the login shell

**Files:**
- Modify: `internal/sandbox/image/render_integration_test.go`

**Interfaces:**
- Consumes: the rendered image from Task 2 (records `AGENTS_SANDBOX_IMAGE_PATH`; installs `/etc/profile.d/agents-sandbox-path.sh`); existing `buildDockerfile` / `buildDockerfileTag` helpers and the `docker.RealClient` opt-in.
- Produces: nothing other tasks use.

- [ ] **Step 1: Write the failing test**

Add to `internal/sandbox/image/render_integration_test.go` (imports `os/exec`, `strings`):

```go
// TestRenderDockerfileLoginShellHasImagePath proves the profile.d merge fires
// in a real login shell: /etc/profile resets PATH, and the installed script
// re-adds the image's entries. Covers the managed base (its /opt/agents-sandbox
// is the entry the reset drops) and a project ENV PATH entry.
func TestRenderDockerfileLoginShellHasImagePath(t *testing.T) {
	origGet := docker.Get
	docker.Get = docker.RealClient
	t.Cleanup(func() { docker.Get = origGet })

	a, _ := agent.Lookup("opencode")
	version, err := resolveAgentVersion(context.Background(), a, "")
	if err != nil {
		t.Fatalf("resolve agent version: %v", err)
	}
	project := []byte("ENV PATH=\"/opt/my-tools:${PATH}\"\n")

	tag := "agents-sandbox/it-login-path"
	t.Cleanup(func() {
		_, _ = docker.Get().
			ImageRemove(context.Background(), tag, client.ImageRemoveOptions{Force: true, PruneChildren: true})
	})
	buildDockerfileTag(t, a, renderBytes(t, a, project, false), version, false, tag)

	out, err := exec.Command("docker", "run", "--rm", tag, "bash", "-lc", `printf '%s' "$PATH"`).Output()
	if err != nil {
		t.Fatalf("docker run: %v", err)
	}
	loginPath := string(out)
	for _, want := range []string{"/opt/agents-sandbox/bin", "/opt/my-tools"} {
		if !strings.Contains(loginPath, want) {
			t.Errorf("login PATH %q missing %q", loginPath, want)
		}
	}
}
```

`os/exec` must be added to the test file's imports (`context`, `docker`, `client`, `agent`, `strings` are already imported). `renderBytes`, `resolveAgentVersion`, and `buildDockerfileTag` already exist in this file. If building the managed base needs the microsandbox CA workaround in the local sandbox, rely on the same environment the existing cases use (do not add CA handling — the existing suite assumes it).

- [ ] **Step 2: Run the test to verify it fails**

Run: `CGO_ENABLED=1 go test -tags integration -count=1 -timeout 40m -run TestRenderDockerfileLoginShellHasImagePath ./internal/sandbox/image/`
Expected: on the pre-Task-2 image, FAIL — `/opt/agents-sandbox/bin` absent from the login PATH. (With Task 2 already applied this test may pass immediately; if so, temporarily stub the script removal to confirm it fails, then restore. Verify it fails against `git stash` of Task 2 when practical.)

- [ ] **Step 3: Confirm the fix**

No production code should be needed; if Step 2 fails with the Task 2 code in place, debug the wiring (COPY path, profile.d sourcing) before proceeding.

- [ ] **Step 4: Run the test to verify it passes**

Run: `CGO_ENABLED=1 go test -tags integration -count=1 -timeout 40m -run TestRenderDockerfileLoginShellHasImagePath ./internal/sandbox/image/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/image/render_integration_test.go
git commit -m "test(image): integration coverage for login-shell PATH merge"
```

---

### Task 4: Docs, spec, CHANGELOG, and the stale session comment

**Files:**
- Modify: `docs/runner-image.md`
- Modify: `.operator-shared/specs/image-composition.md`
- Modify: `CHANGELOG.md`
- Modify: `internal/sandbox/session/run.go` (comment at ~`run.go:153-155`)

**Interfaces:**
- Consumes: the behaviour from Tasks 1-3.
- Produces: nothing.

- [ ] **Step 1: Update `docs/runner-image.md`**

Replace the `/usr/local/bin` symlink paragraph (the paragraph beginning "Every tool lands under `/opt/agents-sandbox`; ...") with the merge description: the image records its composed `PATH` in `AGENTS_SANDBOX_IMAGE_PATH` and installs `/etc/profile.d/agents-sandbox-path.sh`, which re-appends entries lost when a login shell's `/etc/profile` resets `PATH`. In "Base starting point", add to the custom-base requirements: the base's login profile must source `/etc/profile.d/*.sh`. In the "ENV configuration" section, state that image `ENV PATH` entries are restored into login shells by the merge.

- [ ] **Step 2: Update `.operator-shared/specs/image-composition.md`**

In "Tool artifact contract", replace the `/usr/local/bin` symlink bullet with a bullet describing the `AGENTS_SANDBOX_IMAGE_PATH` env var and the `/etc/profile.d` merge; state that it supersedes the symlink approach. Cross-link `.operator-shared/specs/login-shell-path.md`.

- [ ] **Step 3: Update `CHANGELOG.md`**

Replace the `[Unreleased]` Fixed entry about `/usr/local/bin` symlinks with one describing the login-shell PATH merge (`AGENTS_SANDBOX_IMAGE_PATH` + `/etc/profile.d` drop-in), preserving the note that a custom Dockerfile's `ENV PATH` (and the toolchain) now stays effective in `shell`/`!`.

- [ ] **Step 4: Fix the stale session comment**

In `internal/sandbox/session/run.go`, drop `~/.microsandbox/bin` from the login-shell rationale comment (it is a host install path under `internal/sandbox/doctor/install.go`, not a VM concern) and note that the image's composed `PATH` is restored by the `/etc/profile.d` merge.

- [ ] **Step 5: Run the full checks**

Run: `make check`
Expected: fmt/lint (0 issues)/unit tests/docs linkcheck all pass.

- [ ] **Step 6: Commit**

```bash
git add docs/runner-image.md .operator-shared/specs/image-composition.md CHANGELOG.md internal/sandbox/session/run.go
git commit -m "docs: document login-shell PATH merge; refresh stale session comment"
```

---

## Execution handoff

Run the whole plan with `superpowers:subagent-driven-development`. The final whole-branch review runs on the most capable available model.

## Rulings expected from the controller

- Task 1 vs `sh` availability: the behaviour seam shells out to the system `sh`; acceptable on Linux/macOS CI and dev hosts.
- Task 3 uses the host `docker` CLI to run the container (the build uses the moby client); acceptable because integration tests already require a Docker daemon and CLI. If CI lacks the CLI, switch to the moby client — a ruling to record.
- If removing the symlink stopgap (Task 2) conflicts with any other unmerged change still relying on it, stop and surface to the user (the spec chose one mechanism).
