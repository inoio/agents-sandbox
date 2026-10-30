# Agent-Version Baseline Sync Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Keep `HomeState.agent_version` equal to the release actually baked into the runner image, so an accepted upgrade never reverts and a `--agent-version` pin persists.

**Architecture:** The image's `org.agents-sandbox.identity.agent-version` label becomes the authority for an existing image and `HomeState.agent_version` its cache. The image-ensuring paths record the label value after home resolution; `resolveBuildVersion` reconciles a disagreeing (stale) cache from the label before the upgrade check; `recoverAgentVersion` widens from "empty only" to "empty or disagreeing".

**Tech Stack:** Go 1.26, Docker SDK (`github.com/moby/moby/client`), `go.yaml.in/yaml/v3`, testify-free standard `testing`, the repo's `docker.MockDockerClient` / `msb.MockMsbClient` fixtures.

**Spec:** `.operator-shared/specs/agent-version-sync.md`

## Global Constraints

- Label key is `org.agents-sandbox.identity.agent-version` (`image.agentVersionLabelKey`); non-release sentinels are `none` (`image.identityNone`) and `user-provided` (`image.userProvidedImageIdentity`).
- Every `HomeState` mutation goes through the locked transaction API (`state.WithHomeState` / `state.RecoverHomeState`); never write the file directly — see `.operator-shared/specs/project-state-transactions.md`.
- A write must never create a fingerprint-less partial state before home resolution (#183); recording skips when no state file exists.
- Run `make fmt` and `make lint` after edits; tests via `make test` (or `go test -tags microsandbox_ffi_path ./...`).
- Do not document behavior already evident in code; keep brain docs for contracts only.

## Review Focus

- **Explicit `--agent-version` pin vs. a present image carrying another release:** the pin must win and must not be reconciled away by the image label (Task 3).
- **User-provided agent (`agent_source=user`, `user-provided` label):** reconciliation must not overwrite the recorded version with the sentinel and must record nothing (Task 3).
- **No Docker image present with a recorded baseline (pruned/cleaned host):** the recorded value must be kept for offline reuse, not cleared (Task 3).
- **First run with no state file:** recording must not create a partial state (Task 1).
- **State write fails / lock contended during recording:** non-fatal, warn and continue (Task 3, extending `TestResolveOpenCodeVersionRecoveryPersistFailureStillUsesVersion`).

---

### Task 0: Land the design docs

**Files:**
- Add: `.operator-shared/specs/agent-version-sync.md` (already written)
- Modify: `.operator-shared/catalog.md` (entry already added)
- Add: `.operator-shared/plans/2026-10-28-agent-version-sync.md` (this file)

- [ ] **Step 1: Commit the spec, catalog entry and plan**

```bash
git add .operator-shared/specs/agent-version-sync.md .operator-shared/catalog.md .operator-shared/plans/2026-10-28-agent-version-sync.md
git commit -m "docs: spec agent-version baseline synchronization"
```

---

### Task 1: `state.RecordAgentVersion` helper

**Files:**
- Add: `internal/sandbox/state/agent_version.go`
- Test: `internal/sandbox/state/agent_version_test.go`

**Interfaces:**
- Consumes: `WithHomeState(k Key, fn func(*HomeState, bool) (bool, error)) error`, `ReadState(k) (*HomeState, error)`, `ErrStateNotFound` (all existing).
- Produces: `func RecordAgentVersion(k Key, version string) error` — writes `HomeState.AgentVersion` only when a state file exists and the value changes; no-op for an empty version or a missing state.

- [ ] **Step 1: Write the failing tests**

```go
package state

import (
	"errors"
	"testing"
)

func TestRecordAgentVersionWritesExistingState(t *testing.T) {
	WithMockConfigPaths(t)
	k := Key{Slug: "proj", Agent: "opencode"}
	if err := writeState(k, HomeState{HomeVolume: "vol"}); err != nil {
		t.Fatalf("writeState: %v", err)
	}

	if err := RecordAgentVersion(k, "2.0.0"); err != nil {
		t.Fatalf("RecordAgentVersion: %v", err)
	}

	got, err := ReadState(k)
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if got.AgentVersion != "2.0.0" {
		t.Errorf("AgentVersion = %q, want 2.0.0", got.AgentVersion)
	}
	if got.HomeVolume != "vol" {
		t.Errorf("HomeVolume = %q, want preserved vol", got.HomeVolume)
	}
}

func TestRecordAgentVersionSkipsMissingState(t *testing.T) {
	WithMockConfigPaths(t)
	k := Key{Slug: "proj", Agent: "opencode"}

	if err := RecordAgentVersion(k, "2.0.0"); err != nil {
		t.Fatalf("RecordAgentVersion: %v", err)
	}

	if _, err := ReadState(k); !errors.Is(err, ErrStateNotFound) {
		t.Fatalf("ReadState error = %v, want ErrStateNotFound (no state created)", err)
	}
}

func TestRecordAgentVersionSkipsEmptyVersion(t *testing.T) {
	WithMockConfigPaths(t)
	k := Key{Slug: "proj", Agent: "opencode"}

	if err := RecordAgentVersion(k, ""); err != nil {
		t.Fatalf("RecordAgentVersion: %v", err)
	}
	if _, err := ReadState(k); !errors.Is(err, ErrStateNotFound) {
		t.Fatalf("ReadState error = %v, want ErrStateNotFound", err)
	}
}
```

Use the existing `configpaths.WithMockConfigPaths(t)` import (as other `state` tests do); replace `WithMockConfigPaths(t)` with the fully qualified call if that is the established form in the package.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test -tags microsandbox_ffi_path ./internal/sandbox/state/ -run TestRecordAgentVersion -v`
Expected: FAIL — `undefined: RecordAgentVersion`

- [ ] **Step 3: Implement `RecordAgentVersion` in `internal/sandbox/state/agent_version.go`**

```go
// RecordAgentVersion records the agent release baked into the project's runner
// image as its upgrade baseline. It is a no-op for an empty version (a
// non-release sentinel image) and when no state file exists, so it never creates
// a fingerprint-less partial state before home resolution (#183). Routing it
// through WithHomeState preserves the fields the caller does not own.
func RecordAgentVersion(k Key, version string) error {
	if version == "" {
		return nil
	}
	return WithHomeState(k, func(hs *HomeState, exists bool) (bool, error) {
		if !exists || hs.AgentVersion == version {
			return false, nil
		}
		hs.AgentVersion = version
		return true, nil
	})
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test -tags microsandbox_ffi_path ./internal/sandbox/state/ -run TestRecordAgentVersion -v`
Expected: PASS (3 tests)

- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/state/agent_version.go internal/sandbox/state/agent_version_test.go
git commit -m "feat(state): add RecordAgentVersion baseline writer"
```

---

### Task 2: Surface the baked agent version on `image.ImageInfo`

**Files:**
- Modify: `internal/sandbox/image/build.go` (`ImageInfo` struct ~line 55, `EnsureImageWithClient` return ~line 475, `Build` signature ~line 717)
- Modify: `internal/sandbox/image/inspect.go` (`ExistingAgentVersion`, to share the sentinel filter)
- Test: `internal/sandbox/image/agent_version_test.go`
- Test (call-site fixes): `internal/sandbox/image/coverage_test.go:79,94,118`

**Interfaces:**
- Consumes: `agentVersionLabelKey`, `identityNone`, `userProvidedImageIdentity`, `docker.Get().ImageInspect` (existing).
- Produces: `ImageInfo.AgentVersion string` — the release from the image's `agent-version` label, empty for a missing/sentinel label; `func Build(ctx, a, projectSlug, buildOpts, ui) (ImageInfo, error)` (was `error`).

- [ ] **Step 1: Write the failing test**

```go
package image

import (
	"context"
	"testing"

	dockerspec "github.com/moby/docker-image-spec/specs-go/v1"
	"github.com/moby/moby/client"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/docker"
	"github.com/inoio/agents-sandbox/internal/termio"
)

func TestEnsureImageReportsBakedAgentVersion(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	WithMockAgentVersion(t, "1.2.3")
	a := agentOpencode(t)
	rendered, err := RenderDockerfile(a, "1.2.3", nil, false)
	if err != nil {
		t.Fatalf("render Dockerfile: %v", err)
	}
	labels := map[string]string{
		dockerfileIDLabelKey: computeDockerfileID(rendered, "1.2.3"),
		agentVersionLabelKey: "1.2.3",
	}
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageInspectFn: func(_ context.Context, ref string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			if ref == "debian:trixie-slim" {
				return client.ImageInspectResult{ID: "sha256:base"}, nil
			}
			return client.ImageInspectResult{
				ID:     "sha256:existing",
				Config: &dockerspec.DockerOCIImageConfig{Labels: labels},
			}, nil
		},
	})

	info, err := EnsureImage(context.Background(), a, "proj", BuildOptions{}, &termio.Mock{})
	if err != nil {
		t.Fatalf("EnsureImage: %v", err)
	}
	if info.AgentVersion != "1.2.3" {
		t.Errorf("AgentVersion = %q, want 1.2.3", info.AgentVersion)
	}
}

func TestEnsureImageReportsEmptyForSentinelLabel(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	WithMockAgentVersion(t, "1.2.3")
	a := agentOpencode(t)
	rendered, err := RenderDockerfile(a, "1.2.3", nil, false)
	if err != nil {
		t.Fatalf("render Dockerfile: %v", err)
	}
	labels := map[string]string{
		dockerfileIDLabelKey: computeDockerfileID(rendered, "1.2.3"),
		agentVersionLabelKey: userProvidedImageIdentity,
	}
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageInspectFn: func(_ context.Context, ref string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			if ref == "debian:trixie-slim" {
				return client.ImageInspectResult{ID: "sha256:base"}, nil
			}
			return client.ImageInspectResult{
				ID:     "sha256:existing",
				Config: &dockerspec.DockerOCIImageConfig{Labels: labels},
			}, nil
		},
	})

	info, err := EnsureImage(context.Background(), a, "proj", BuildOptions{}, &termio.Mock{})
	if err != nil {
		t.Fatalf("EnsureImage: %v", err)
	}
	if info.AgentVersion != "" {
		t.Errorf("AgentVersion = %q, want empty for a user-provided label", info.AgentVersion)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test -tags microsandbox_ffi_path ./internal/sandbox/image/ -run 'TestEnsureImageReports' -v`
Expected: FAIL — `info.AgentVersion undefined` (and `EnsureImage` returns one value today)

- [ ] **Step 3: Implement**

In `internal/sandbox/image/inspect.go`, add the shared filter and use it in `ExistingAgentVersion`:

```go
// agentReleaseFromLabels returns the release the image labels report, or "" for
// a missing or non-release sentinel.
func agentReleaseFromLabels(labels map[string]string) string {
	version := labels[agentVersionLabelKey]
	if version == identityNone || version == userProvidedImageIdentity {
		return ""
	}
	return version
}
```

Replace the body of `ExistingAgentVersion` after the `Present` check with `return agentReleaseFromLabels(state.Labels), agentReleaseFromLabels(state.Labels) != ""` (read once into a variable).

In `internal/sandbox/image/build.go`:
- Add `AgentVersion string` to `ImageInfo` (with a doc line: the release the image reports via the `agent-version` label).
- In `EnsureImageWithClient`, after `inspect, err := docker.Get().ImageInspect(ctx, rTag)`, read the label: `var bakedVersion string; if inspect.Config != nil { bakedVersion = agentReleaseFromLabels(inspect.Config.Labels) }`, and set `AgentVersion: bakedVersion` in the returned `ImageInfo`.
- Change `func Build(...) error` to `func Build(...) (ImageInfo, error)`; return `info, nil` after `EnsureLoaded`, and `ImageInfo{}, err` on error.

- [ ] **Step 4: Fix the `Build` call sites and run the package tests**

Update the three `coverage_test.go` call sites to `_, err := Build(...)` (lines 79, 94, 118).

Run: `go test -tags microsandbox_ffi_path ./internal/sandbox/image/ -run 'TestEnsureImageReports|TestExistingAgentVersion|TestBuild' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/image
git commit -m "feat(image): surface the baked agent version and return ImageInfo from Build"
```

---

### Task 3: Reconcile the baseline in `resolveBuildVersion`

**Files:**
- Modify: `internal/sandbox/vm/upgrade.go` (`resolveBuildVersion` ~line 96, `recoverAgentVersion` ~line 326)
- Test: `internal/sandbox/vm/upgrade_test.go`

**Interfaces:**
- Consumes: `state.RecordAgentVersion` (Task 1), `existingAgentVersion` seam (existing), `currentAgentVersion` (existing).
- Produces: `func recoverAgentVersion(ctx context.Context, k state.Key, recorded string, ui termio.UI) string` — returns the image label when it disagrees with `recorded` (and records it), otherwise `recorded`.

- [ ] **Step 1: Write the failing tests**

```go
func TestResolveBuildVersionReconcilesStaleBaselineWithImage(t *testing.T) {
	origLatest := agentLatestVersion
	defer func() { agentLatestVersion = origLatest }()
	agentLatestVersion = func(_ context.Context, _ agent.Agent) (string, error) { return "1.0.0", nil }

	configpaths.WithMockConfigPaths(t)
	k := state.Key{Slug: "proj", Agent: "opencode"}
	seedAgentState(t, k, "0.9.0", agentSourceTool) // stale: the image holds 1.0.0
	withExistingAgentVersion(t)                    // seam returns "1.0.0", true

	got, upgraded, _, err := resolveBuildVersion(
		context.Background(), k, opencodeAgent(t), &termio.Mock{}, options.RunOptions{Agent: "opencode"},
	)
	if err != nil {
		t.Fatalf("resolveBuildVersion: %v", err)
	}
	if got != "1.0.0" {
		t.Errorf("resolveBuildVersion = %q, want the image's 1.0.0, not the stale 0.9.0", got)
	}
	if upgraded {
		t.Error("expected upgraded=false: 1.0.0 is not newer than the image's 1.0.0")
	}
	hs, err := state.ReadState(k)
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if hs.AgentVersion != "1.0.0" {
		t.Errorf("recorded agent_version = %q, want the image's 1.0.0", hs.AgentVersion)
	}
}

func TestResolveBuildVersionPinWinsOverImageLabel(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := state.Key{Slug: "proj", Agent: "opencode"}
	seedAgentState(t, k, "0.9.0", agentSourceTool)
	withExistingAgentVersion(t) // image reports 1.0.0

	got, _, _, err := resolveBuildVersion(
		context.Background(), k, opencodeAgent(t), &termio.Mock{},
		options.RunOptions{Agent: "opencode", AgentVersion: "3.0.0"},
	)
	if err != nil {
		t.Fatalf("resolveBuildVersion: %v", err)
	}
	if got != "3.0.0" {
		t.Errorf("resolveBuildVersion = %q, want the explicit pin 3.0.0", got)
	}
}

func TestResolveBuildVersionKeepsBaselineWhenImageAbsent(t *testing.T) {
	origLatest := agentLatestVersion
	defer func() { agentLatestVersion = origLatest }()
	agentLatestVersion = func(_ context.Context, _ agent.Agent) (string, error) { return "2.0.0", nil }

	configpaths.WithMockConfigPaths(t)
	k := state.Key{Slug: "proj", Agent: "opencode"}
	seedAgentState(t, k, "1.0.0", agentSourceTool) // no withExistingAgentVersion: seam defaults to absent

	ui := &termio.Mock{SelectFn: func(string, []termio.Choice, string) (string, error) { return "k", nil }}
	got, upgraded, _, err := resolveBuildVersion(
		context.Background(), k, opencodeAgent(t), ui, options.RunOptions{Agent: "opencode"},
	)
	if err != nil {
		t.Fatalf("resolveBuildVersion: %v", err)
	}
	if got != "1.0.0" {
		t.Errorf("resolveBuildVersion = %q, want the recorded 1.0.0 kept for offline reuse", got)
	}
	if upgraded {
		t.Error("expected upgraded=false for a keep choice")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test -tags microsandbox_ffi_path ./internal/sandbox/vm/ -run 'TestResolveBuildVersion(Reconciles|PinWins|KeepsBaseline)' -v`
Expected: `TestResolveBuildVersionReconcilesStaleBaselineWithImage` FAILS (`got 0.9.0`); the other two PASS (they pin the desired behavior).

- [ ] **Step 3: Implement**

In `resolveBuildVersion`, replace the empty-only recovery block:

```go
current := currentAgentVersion(k)
if current == "" {
	current = recoverAgentVersion(ctx, k, ui)
}
if current == "" {
	return "", false, "", nil
}
```

with:

```go
current := recoverAgentVersion(ctx, k, currentAgentVersion(k), ui)
if current == "" {
	return "", false, "", nil
}
```

Replace `recoverAgentVersion` with:

```go
// recoverAgentVersion reconciles a project's upgrade baseline with the release
// its existing runner image reports. A missing image (or a non-release sentinel)
// leaves the recorded value as the authoritative offline fallback; a label that
// disagrees with the recorded value wins, because the image is what is actually
// installed, and the cache is rewritten. It returns the version to build.
func recoverAgentVersion(ctx context.Context, k state.Key, recorded string, ui termio.UI) string {
	version, ok := existingAgentVersion(ctx, k.Slug, k.Agent)
	if !ok || version == recorded {
		return recorded
	}
	if err := state.RecordAgentVersion(k, version); err != nil {
		ui.Verbosef("could not record recovered agent version: %v", err)
	}
	return version
}
```

- [ ] **Step 4: Run the vm package and fix any fallout**

Run: `go test -tags microsandbox_ffi_path ./internal/sandbox/vm/ -v`
Expected: PASS. The default seam (`main_test.go` returns `"", false`) keeps recorded versions, so existing recorded-baseline tests are unaffected; the empty-baseline recovery tests still recover because the label is now used at the same point.

- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/vm/upgrade.go internal/sandbox/vm/upgrade_test.go
git commit -m "fix(vm): reconcile a stale agent-version baseline from the image label"
```

---

### Task 4: Record the baked version in `PrepareSandbox`

**Files:**
- Modify: `internal/sandbox/vm/run_orchestrate.go` (`PrepareSandbox`, next to `persistOfferedVersion` ~line 166; add `persistAgentVersion`)
- Test: `internal/sandbox/vm/prepare_sandbox_test.go`

**Interfaces:**
- Consumes: `state.RecordAgentVersion` (Task 1), `image.ImageInfo.AgentVersion` (Task 2).
- Produces: `func persistAgentVersion(k state.Key, version string, ui termio.UI)` — warns, never fails, on a state write error.

- [ ] **Step 1: Write the failing test**

Add to `prepare_sandbox_test.go`, modeled on `TestPrepareSandboxPersistsMountFingerprintOnVMCreation` (its mocks: `WithMockAgentVersionResolver`, `docker.WithDockerMock`, `msb.WithMsbMock`, `seedHomeState`, `SetDaemonHealthFunc`, a `termio.NewTestMock`):

```go
func TestPrepareSandboxRecordsBakedAgentVersion(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	image.WithMockAgentVersionResolver(t, func(_ context.Context, _ agent.Agent, req string) (string, error) {
		return req, nil
	})
	origLatest := agentLatestVersion
	agentLatestVersion = func(_ context.Context, _ agent.Agent) (string, error) { return "", nil }
	t.Cleanup(func() { agentLatestVersion = origLatest })

	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageBuildFn: func(_ context.Context, _ io.Reader, _ client.ImageBuildOptions) (client.ImageBuildResult, error) {
			return client.ImageBuildResult{Body: io.NopCloser(strings.NewReader(""))}, nil
		},
		ImageInspectFn: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{
				ID: "sha256:abc123",
				Config: &dockerspec.DockerOCIImageConfig{
					Labels: map[string]string{"org.agents-sandbox.identity.agent-version": "2.0.0"},
				},
			}, nil
		},
	})

	slug := git.CheckoutSlug()
	vmFS := msb.NewTestFS(nil, nil)
	createdSb := &msb.MockSandbox{
		Name_:    projectVMName(state.Key{Slug: slug, Agent: "opencode"}),
		FSValue_: vmFS,
		ShellOut: map[string]msb.ShellResult{
			dockerdBinaryCheckCmd: msb.NewTestResult(false, 1, "", "", nil),
		},
	}
	mock := &msb.MockMsbClient{
		ImageGetFn:     func(_ context.Context, _ string) error { return nil },
		CreatedSandbox: createdSb,
		Volumes:        []msb.VolumeHandle{&msb.MockVolumeHandle{Name_: "home-vol"}},
	}
	msb.WithMsbMock(t, mock)

	k := state.Key{Slug: slug, Agent: "opencode"}
	// No agent_version recorded: recording must fill it from the baked image.
	if err := seedHomeState(k, state.HomeState{HomeVolume: "home-vol", ImageDigest: "sha256:abc123"}); err != nil {
		t.Fatalf("seedHomeState: %v", err)
	}

	origDaemon := SetDaemonHealthFunc(func(_ context.Context, _ msb.Sandbox, command string) (string, int, error) {
		if command == opencodeProvider(t).DaemonHealthCmd() {
			return `{"healthy":true,"version":"test"}`, 0, nil
		}
		return "", 0, nil
	})
	t.Cleanup(func() { SetDaemonHealthFunc(origDaemon) })

	ui := termio.NewTestMock(t)
	sess, err := PrepareSandbox(context.Background(), options.RunOptions{Agent: "opencode"}, &ui)
	if err != nil {
		t.Fatalf("PrepareSandbox: %v", err)
	}
	defer sess.Cleanup()

	st, err := state.ReadState(k)
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if st.AgentVersion != "2.0.0" {
		t.Errorf("AgentVersion = %q, want the baked 2.0.0 recorded", st.AgentVersion)
	}
}
```

Mirror the imports/helpers from the model test (the model test's package and imports already exist in `prepare_sandbox_test.go`).

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -tags microsandbox_ffi_path ./internal/sandbox/vm/ -run TestPrepareSandboxRecordsBakedAgentVersion -v`
Expected: FAIL — `AgentVersion = ""` (recording not wired); note the boot-time `recordImageProvenance` cannot fill it because the test file system has no `/etc/agents-sandbox/agent-source`.

- [ ] **Step 3: Implement**

Add `persistAgentVersion(k, imageInfo.AgentVersion, ui)` immediately after `persistOfferedVersion(k, offeredVersion, ui)` in `PrepareSandbox`, and:

```go
// persistAgentVersion records the release baked into the runner image as this
// project's upgrade baseline, once the home volume is resolved so a fresh state
// exists. It is the writer of record: unlike boot-time provenance it does not
// depend on the profile implementing a VersionProvider. A failure only warns;
// the next run reconciles from the image label.
func persistAgentVersion(k state.Key, version string, ui termio.UI) {
	if err := state.RecordAgentVersion(k, version); err != nil {
		ui.Warnf("could not persist the agent version: %v (continuing)", err)
	}
}
```

- [ ] **Step 4: Run the vm package tests**

Run: `go test -tags microsandbox_ffi_path ./internal/sandbox/vm/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/vm/run_orchestrate.go internal/sandbox/vm/prepare_sandbox_test.go
git commit -m "fix(vm): record the baked agent version on run/shell"
```

---

### Task 5: Record the baked version from the `build` command

**Files:**
- Modify: `cmd/agents-sandbox/commands_system.go` (`buildBuildCmd` ~line 266)
- Test: `cmd/agents-sandbox/cli_build_test.go`

**Interfaces:**
- Consumes: `image.Build(...) (ImageInfo, error)` (Task 2), `state.RecordAgentVersion` (Task 1).
- Produces: no new exported symbol; the `build` command records the built image's `AgentVersion` when a project state exists.

- [ ] **Step 1: Write the failing test**

Add a test next to `TestBuildAgentVersionPrecedenceMatchesRun`, reusing its `setupCommandFixtures` / docker-mock scaffolding, that seeds a project state, runs `build --agent-version 9.9.9`, and asserts the recorded baseline:

```go
func TestBuildCommandRecordsPinnedAgentVersion(t *testing.T) {
	// Use the same fixture setup as TestBuildAgentVersionPrecedenceMatchesRun:
	// setupCommandFixtures(t, cmdBuild, "--agent-version", "9.9.9") plus the
	// docker mock whose ImageInspectFn returns Labels with
	// agentVersionLabelKey "9.9.9" and a matching dockerfileIDLabelKey so no
	// rebuild is forced.
	//
	// Seed the state for the resolved (slug, "opencode") key with a HomeVolume
	// but no agent_version, using the state package's test writer.
	//
	// Execute the build command, then:
	st, err := state.ReadState(state.Key{Slug: slug, Agent: "opencode"})
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if st.AgentVersion != "9.9.9" {
		t.Errorf("AgentVersion = %q, want the pinned 9.9.9 recorded", st.AgentVersion)
	}
}
```

Fill in the fixture lines by copying `TestBuildAgentVersionPrecedenceMatchesRun`; the only new assertions are the `ReadState` / `AgentVersion` pair above.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -tags microsandbox_ffi_path ./cmd/agents-sandbox/ -run TestBuildCommandRecordsPinnedAgentVersion -v`
Expected: FAIL — `AgentVersion = ""`

- [ ] **Step 3: Implement**

In `buildBuildCmd`, capture the returned info and record it:

```go
info, err := image.Build(cmd.Context(), a, adoption.Slug, image.BuildOptions{
	Force: force, AgentVersion: agentVersion, UserProvided: userProvided, Docker: resolveBuildDocker(cmd),
}, ui)
if err != nil {
	return err
}
if err := state.RecordAgentVersion(state.Key{Slug: adoption.Slug, Agent: a.Name()}, info.AgentVersion); err != nil {
	ui.Warnf("could not persist the agent version: %v (continuing)", err)
}
return nil
```

- [ ] **Step 4: Run the CLI package tests**

Run: `go test -tags microsandbox_ffi_path ./cmd/agents-sandbox/ -run 'TestBuild' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add cmd/agents-sandbox/commands_system.go cmd/agents-sandbox/cli_build_test.go
git commit -m "fix(build): record the baked agent version so a pin persists"
```

---

### Task 6: Docs and changelog

**Files:**
- Modify: `docs/runner-image.md` (~lines 333-361, the "Upgrades" section)
- Modify: `CHANGELOG.md` (`[Unreleased]`)

- [ ] **Step 1: Update `docs/runner-image.md`**

Replace the sentence at line 333-335 ("Instead of reading the version from an image label, the version is detected on first boot and recorded for upgrade checks.") with the new model: the version baked into the image is read from the image's `agent-version` label and recorded as the project's upgrade baseline on every run and build, so a pin persists and an accepted upgrade sticks; a label is never read as a network probe. Update the line 349-351 sentence ("When no version has been recorded ... reads it once from the project's existing image ...") to state that the baseline is reconciled with the image label whenever the image is present, so a stale baseline cannot rebuild the image backwards. Keep the `--agent-version`, `--rebuild`, and `agent-source=user` paragraphs accurate.

- [ ] **Step 2: Add the changelog line**

Under `## [Unreleased]` → `### Fixed`, add:

```markdown
- The recorded agent-version baseline is kept in sync with the runner image, so an accepted agent upgrade no longer
  reverts to the previous release on a later run, a `--agent-version` pin persists across unpinned runs, and a
  deleted baseline is re-recorded. Fixes opencode2, whose image has no in-VM version probe.
```

- [ ] **Step 3: Run the docs/lint checks**

Run: `ci/check-docs.sh && make lint`
Expected: PASS (docs links resolve; lint clean)

- [ ] **Step 4: Commit**

```bash
git add docs/runner-image.md CHANGELOG.md
git commit -m "docs: describe the agent-version baseline sync"
```

---

### Task 7: Final verification

- [ ] **Step 1: Run the full check**

Run: `make check`
Expected: PASS (fmt, lint, test)

- [ ] **Step 2: Run the compile-only pass for tagged files**

Run: `make compile-all`
Expected: every package compiles; `[no tests to run]` for `-run='^$'` is expected, not a failure

- [ ] **Step 3: Commit any formatting fallout**

```bash
git add -A
git commit -m "chore: make check fallout"
```

Only if `make check` changed files.
