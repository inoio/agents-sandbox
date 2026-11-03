# fuse-overlayfs as the in-VM dockerd storage driver — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Switch the in-VM `dockerd` off the forced `vfs` storage driver onto `fuse-overlayfs` when the guest provides it, keeping `vfs` as the fallback (#489).

**Architecture:** Two production changes. The runner image's `agents-sandbox-docker` stage installs `fuse-overlayfs` + its libfuse3 runtime from the tool base's apt. `vm/dockerd.go` gains a minimal driver-selection change: when the image's `daemon.json` sets no driver it probes the guest (`fuse-overlayfs` on `PATH` and a usable `/dev/fuse`) and requests `fuse-overlayfs`, else `vfs`; an explicit `daemon.json` driver is accepted for `vfs` or `fuse-overlayfs` and otherwise hard-errors, preserving the #287 "never pass the flag when the file sets it" rule.

**Tech Stack:** Go, Docker/Dockerfile stage composition, microsandbox msb SDK (`msb.Sandbox.Shell`), the `curl`/`ca-certificates`/`tar` tool base.

**Spec:** `.operator-shared/specs/image-composition.md` (§ "vfs storage driver"); issue [inoio/agents-sandbox#489](https://github.com/inoio/agents-sandbox/issues/489); why-overlay2-fails background in `.operator-shared/guides/docker-storage-driver-in-vm.md`.

## Global Constraints

- Target platforms: Linux (KVM) and macOS (Apple Silicon).
- `fuse-overlayfs` and `libfuse3-4` are the Debian trixie package names (verified: `fuse-overlayfs` depends on `libfuse3-4`, `fuse3`); the docker stage always builds on the pinned `debian:trixie-slim` tool base (`distroBaseRef`).
- Keep the `#287` rule: pass `--storage-driver=` **only** when the image's `/etc/docker/daemon.json` sets no `storage-driver`.
- `golangci-lint` is the linter (`make lint`); `make fmt`; never `go vet`.
- Each task ends with the package's tests green: `go test -tags microsandbox_ffi_path ./internal/sandbox/vm/... ./internal/sandbox/image/...`.

## Review Focus

- `docker` enabled on a base whose image carries no `fuse-overlayfs` (a cached older runner image): selection must fall back to `vfs`, never request a driver the binary lacks. Covered in Task 2.
- `daemon.json` sets `fuse-overlayfs` but the guest lacks `/dev/fuse`: the explicit value is honored (no probe, no flag), and dockerd reports its own failure. Covered in Task 2.
- Probe transport error (`Shell` returns an error): must not fail the VM start; fall back to `vfs`. Covered in Task 2.
- A user `daemon.json` naming a driver other than `vfs`/`fuse-overlayfs` (`overlay2`): hard error naming both accepted drivers, no dockerd started. Covered in Task 2.
- The runner image failing to build because apt cannot reach the Debian mirror: the new apt layer runs on the tool base which already runs `apt-get update` successfully, so it adds no new requirement. Covered by the existing integration build suite (Task 5 runs `make compile-all`); real host verification is the user's.

---

### Task 1: Install fuse-overlayfs in the docker stage

**Files:**
- Modify: `internal/sandbox/image/dockerfile.go` (`dockerStage`, ~601-609)
- Test: `internal/sandbox/image/render_test.go` (`TestRenderDockerfileDockerStage`, ~277-296)

**Interfaces:**
- Consumes: `toolbaseStageAlias`, `dockerStageAlias`, `dockerVersion`, `toolBin` (existing).
- Produces: a `dockerStage()` whose rendered stage installs `fuse-overlayfs` and `libfuse3-4` before downloading the docker tarball.

- [ ] **Step 1: Extend the render test**

Add to the `want` list in `TestRenderDockerfileDockerStage`:

```go
"apt-get install -y --no-install-recommends fuse-overlayfs libfuse3-4",
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test -tags microsandbox_ffi_path ./internal/sandbox/image/ -run TestRenderDockerfileDockerStage -v`
Expected: FAIL — rendered Dockerfile missing the apt line.

- [ ] **Step 3: Add the apt install to `dockerStage()`**

Render an apt install of `fuse-overlayfs libfuse3-4` (the tool base is the pinned trixie image) before the tarball download, mirroring `toolbaseStage`'s `apt-get update` + `--no-install-recommends` + `rm -rf /var/lib/apt/lists/*` shape:

```go
func dockerStage() string {
	return fmt.Sprintf(`FROM %s AS %s
USER root
ARG DOCKER_VERSION=%s
RUN apt-get update && \
    apt-get install -y --no-install-recommends fuse-overlayfs libfuse3-4 && \
    rm -rf /var/lib/apt/lists/*
RUN set -e; mkdir -p %s && \
    curl -fsSL "https://download.docker.com/linux/static/stable/$(uname -m)/docker-${DOCKER_VERSION}.tgz" \
      | tar -xz -C %s --strip-components=1
`, toolbaseStageAlias, dockerStageAlias, dockerVersion, toolBin, toolBin)
}
```

Update the function doc comment to name the new capability (the copy-on-write driver binary) alongside the static docker install.

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test -tags microsandbox_ffi_path ./internal/sandbox/image/ -run TestRenderDockerfileDockerStage -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/image/dockerfile.go internal/sandbox/image/render_test.go
git commit -m "feat(image): install fuse-overlayfs in the docker stage (#489)"
```

---

### Task 2: Select fuse-overlayfs when available, vfs otherwise

**Files:**
- Modify: `internal/sandbox/vm/dockerd.go` (consts ~31-40, `dockerdArgs` ~54-79, `startDockerdIfPresent` ~137-142)
- Test: `internal/sandbox/vm/dockerd_daemonconfig_test.go`, `internal/sandbox/vm/dockerd_test.go`

**Interfaces:**
- Consumes: `msb.Sandbox.Shell(ctx, cmd, msbSdk.WithExecUser(RootUser)) (msb.ShellResult, error)`, `ShellResult.Success()`, `RootUser`, `msb.NewTestResult`.
- Produces: `fuseOverlayfsStorageDriver` const, `fuseOverlayfsProbeCmd` const, `defaultGuestStorageDriver(ctx, sb) string`, `guestSupportsFuseOverlayfs(ctx, sb) bool`, and `dockerdArgs` returning `([]string, string, error)` (args, resolved driver, error).

- [ ] **Step 1: Update the daemon.json test helper and add the fuse cases**

In `dockerd_daemonconfig_test.go`, keep `daemonJSONSandbox(content)` as the "fuse unavailable" case and add a fuse-available variant:

```go
// daemonJSONSandboxWithFuse returns a MockSandbox with the given daemon.json
// contents (omitted when empty) whose guest probe reports the fuse-overlayfs
// binary and /dev/fuse as available or not.
func daemonJSONSandboxWithFuse(content string, fuseAvailable bool) *msb.MockSandbox {
	files := map[string][]byte{}
	if content != "" {
		files[dockerdDaemonConfigPath] = []byte(content)
	}
	return &msb.MockSandbox{
		FSValue_: msb.NewTestFS(files, nil),
		ShellOut: map[string]msb.ShellResult{
			fuseOverlayfsProbeCmd: msb.NewTestResult(fuseAvailable, 0, "", "", nil),
		},
	}
}
```

Make `daemonJSONSandbox(content)` delegate to `daemonJSONSandboxWithFuse(content, false)` so the existing vfs expectations keep holding, and add:

```go
func TestDockerdArgsUsesFuseOverlayfsWhenAvailable(t *testing.T) {
	args, driver, err := dockerdArgs(context.Background(), daemonJSONSandboxWithFuse("", true))
	// expect nil err, driver == fuseOverlayfsStorageDriver,
	// args contains "--storage-driver="+fuseOverlayfsStorageDriver
}

func TestDockerdArgsAcceptsFuseOverlayfsFromDaemonJSON(t *testing.T) {
	args, driver, err := dockerdArgs(context.Background(), daemonJSONSandboxWithFuse(`{"storage-driver":"fuse-overlayfs"}`, false))
	// expect nil err, driver == fuseOverlayfsStorageDriver, no "--storage-driver" arg
}

func TestDockerdArgsFallsBackToVFSAOnProbeError(t *testing.T) {
	fs := msb.NewTestFS(nil, nil)
	sb := &msb.MockSandbox{FSValue_: fs, ShellErr: errors.New("probe boom")}
	args, driver, err := dockerdArgs(context.Background(), sb)
	// expect nil err, driver == vfsStorageDriver, "--storage-driver="+vfsStorageDriver in args
}
```

Update every existing `dockerdArgs(...)` call to the three-value form (`args, _, err :=` or `args, driver, err :=`).

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test -tags microsandbox_ffi_path ./internal/sandbox/vm/ -run 'TestDockerdArgs' -v`
Expected: FAIL — `dockerdArgs` returns 2 values; new tests undefined.

- [ ] **Step 3: Implement the selection in `dockerd.go`**

Add the consts:

```go
fuseOverlayfsStorageDriver = "fuse-overlayfs"

// fuseOverlayfsProbeCmd reports whether the runner image provides the
// fuse-overlayfs binary and a usable /dev/fuse, the two things a rootful
// fuse-overlayfs dockerd needs. It runs as root, the user dockerd runs as.
fuseOverlayfsProbeCmd = "command -v fuse-overlayfs >/dev/null 2>&1 && " +
	"[ -c /dev/fuse ] && [ -r /dev/fuse ] && [ -w /dev/fuse ]"
```

Rewrite `dockerdArgs` to return the driver and delegate the default to `defaultGuestStorageDriver`:

```go
func dockerdArgs(ctx context.Context, sb msb.Sandbox) (args []string, driver string, err error) {
	driver, err = guestStorageDriver(ctx, sb)
	if err != nil {
		return nil, "", err
	}
	requested := driver == "" // the flag is dropped when daemon.json sets it (#287)
	if requested {
		driver = defaultGuestStorageDriver(ctx, sb)
	}
	switch driver {
	case fuseOverlayfsStorageDriver, vfsStorageDriver:
	default:
		return nil, "", fmt.Errorf(
			"%s sets storage-driver=%q, but the microsandbox filesystem supports only %q or %q; "+
				"remove the storage-driver entry or set it to one of them",
			dockerdDaemonConfigPath, driver, fuseOverlayfsStorageDriver, vfsStorageDriver,
		)
	}
	args = []string{dockerdSocketFlag, dockerdSocket}
	if requested {
		args = append(args, "--storage-driver="+driver)
	}
	return args, driver, nil
}
```

Add the two helpers:

```go
// defaultGuestStorageDriver returns the driver to request when the image's
// daemon.json sets none: fuse-overlayfs, whose copy-on-write layers cut both
// build time and data-root disk use, when the guest provides the binary and a
// usable /dev/fuse, and vfs otherwise.
func defaultGuestStorageDriver(ctx context.Context, sb msb.Sandbox) string {
	if guestSupportsFuseOverlayfs(ctx, sb) {
		return fuseOverlayfsStorageDriver
	}
	return vfsStorageDriver
}

// guestSupportsFuseOverlayfs probes the guest for the fuse-overlayfs binary and
// a usable /dev/fuse. A probe failure is treated as unsupported so a transport
// hiccup falls back to vfs rather than failing the VM start.
func guestSupportsFuseOverlayfs(ctx context.Context, sb msb.Sandbox) bool {
	out, err := sb.Shell(ctx, fuseOverlayfsProbeCmd, msbSdk.WithExecUser(RootUser))
	if err != nil {
		return false
	}
	return out.Success()
}
```

In `startDockerdIfPresent`, use the returned driver for the verbose note:

```go
	args, driver, err := dockerdArgs(ctx, sb)
	if err != nil {
		return err
	}
	ui.Verbosef("starting dockerd with %s storage driver", driver)
```

- [ ] **Step 4: Update the start-path tests**

`countingSandbox.Shell` returns success for any unlisted command, so the probe reports fuse-overlayfs. In `dockerd_test.go`:
- change the expected note in `TestStartDockerdIfPresentStartsAndBecomesReady` to `"starting dockerd with fuse-overlayfs storage driver"`;
- add `TestStartDockerdIfPresentUsesFuseOverlayfsDefault` asserting the detached job's args contain `--storage-driver=fuse-overlayfs`.

- [ ] **Step 5: Run the vm tests to verify they pass**

Run: `go test -tags microsandbox_ffi_path ./internal/sandbox/vm/... -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/sandbox/vm/dockerd.go internal/sandbox/vm/dockerd_daemonconfig_test.go internal/sandbox/vm/dockerd_test.go
git commit -m "feat(vm): run dockerd with fuse-overlayfs when available (#489)"
```

---

### Task 3: msb-tagged integration test

**Files:**
- Create: `test/harness/docker_storage_msb_test.go`
- Modify (if needed): `test/harness/smoke_msb_test.go` (add an image-build helper used by both tests)

**Interfaces:**
- Consumes: `preflight`, `prepare`, `preflightDocker`, `cleanupSandboxes`, `projectDir`, `isolatedEnv`, `mustStartUnderPTY`, `ptySession` (`write`, `output`, `waitForMarker`, `waitExit`, `close`), `prepareTimeout`, `probeTimeout`.
- Produces: `TestDockerStorageDriverIsFuseOverlayfs`.

- [ ] **Step 1: Write the integration test**

Add a new `//go:build msb` file. It:
1. runs `preflight(t)`, `prepare`, `preflightDocker`, `cleanupSandboxes`;
2. builds the docker-enabled runner image: `smokeBinary build --agent opencode --docker` in `projectDir(root, "docker-storage")`;
3. starts `smokeBinary shell --agent opencode --docker --network none` under a PTY in that directory and waits settled;
4. writes a command that prints the driver behind a sentinel and waits for it:
   `docker info --format '{{.Driver}}'; echo __DRIVER_DONE__`;
5. writes an offline layered build + run behind a second sentinel and waits for it:
   ```
   mkdir -p /tmp/ctx && printf 'FROM scratch\nCOPY dockerd /dockerd\nCOPY extra /extra\n' > /tmp/ctx/Dockerfile && \
   cp "$(command -v dockerd)" /tmp/ctx/dockerd && touch /tmp/ctx/extra && \
   docker build -t fuse-overlayfs-probe /tmp/ctx && docker run --rm fuse-overlayfs-probe /dockerd --version; echo __BUILD_DONE__
   ```
6. asserts the driver output is `fuse-overlayfs` and the build/run output contains a version string with no error;
7. writes `exit\n` and asserts exit code 0.

A short doc comment records that the disk-drop figure is measured manually (it needs a second daemon; see the guide), and that the test is host-only.

- [ ] **Step 2: Compile it**

Run: `make compile-all`
Expected: PASS (the msb-tagged test compiles).

- [ ] **Step 3: Commit**

```bash
git add test/harness/docker_storage_msb_test.go test/harness/smoke_msb_test.go
git commit -m "test(harness): assert the in-VM dockerd uses fuse-overlayfs (#489)"
```

---

### Task 4: Docs, spec, changelog

**Files:**
- Modify: `docs/runner-image.md` (~306-314)
- Modify: `.operator-shared/specs/image-composition.md` (§ "vfs storage driver", ~239-257)
- Modify: `.operator-shared/guides/docker-storage-driver-in-vm.md` (add the measured figures)
- Create: `changelog.d/<pr>.changed.md`
- Modify: `.operator-shared/catalog.md` (add the plan entry; update the guide Description if its scope grew)

**Interfaces:** none (prose).

- [ ] **Step 1: Update the user docs**

Rewrite the storage-driver paragraph in `docs/runner-image.md` to say the launcher requests `fuse-overlayfs` when the runner image provides the binary and a usable `/dev/fuse`, falling back to `vfs`, and that a `daemon.json` naming `vfs` or `fuse-overlayfs` is left alone (no flag, #287) while any other driver fails start-up naming both accepted drivers.

- [ ] **Step 2: Update the spec**

Rewrite the "vfs storage driver" section of `.operator-shared/specs/image-composition.md` to state the current contract: the docker stage installs `fuse-overlayfs` + `libfuse3-4`; the launcher probes the guest when `daemon.json` sets no driver and requests `fuse-overlayfs` or falls back to `vfs`; `daemon.json` may set either; anything else is a hard error. Keep the #287 rule and the "never writes daemon.json" invariant.

- [ ] **Step 3: Update the guide**

In `.operator-shared/guides/docker-storage-driver-in-vm.md`, record the issue's measured vfs-vs-fuse-overlayfs figures (load, run, build timings; ~6.2× data-root disk ratio) as the reason the default flipped, replacing the "Performance against vfs was not measured" line.

- [ ] **Step 4: Add the changelog fragment**

Create `changelog.d/<pr>.changed.md` with one bullet (see `CONTRIBUTING.md`); use the PR number once opened, `489` until then.

- [ ] **Step 5: Catalog the plan**

Add the plan to `.operator-shared/catalog.md` with a one-line Description and a Read If.

- [ ] **Step 6: Run the docs gate**

Run: `make check` (or `ci/check-docs.sh`)
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add docs/runner-image.md .operator-shared/specs/image-composition.md .operator-shared/guides/docker-storage-driver-in-vm.md changelog.d .operator-shared/catalog.md
git commit -m "docs: document the fuse-overlayfs in-VM storage driver (#489)"
```

---

### Task 5: Full verification

- [ ] **Step 1: Run the full check**

Run: `make check`
Expected: PASS.

- [ ] **Step 2: Compile every test binary**

Run: `make compile-all`
Expected: PASS (including the `msb`-tagged integration test).

- [ ] **Step 3: Hand the real-VM verification to the user**

Note in the handoff that `make test-msb` (or `-run TestDockerStorageDriverIsFuseOverlayfs`) must run host-side: `/dev/kvm` is not functional in the dev VM.
