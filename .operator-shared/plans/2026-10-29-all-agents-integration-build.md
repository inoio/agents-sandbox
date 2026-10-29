# All-Agents Integration Build Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give CI a real docker build *and run* of every built-in agent profile, so an agent whose install or runtime is broken (hermes) fails a required-or-labelled CI job instead of only the host-only msb smoke.

**Architecture:** One new `integration`-tagged test, `TestRenderDockerfileBuildsEveryAgent`, iterates `agent.Names()` and builds then runs each profile on the managed base and a custom non-Debian (Fedora) base. It reuses the existing `buildDockerfileTag`/`imageAgentVersion` helpers. `opencode2` gains a `VersionProvider` so the run probe is uniform across all six profiles. Integration tests that the new test subsumes are deleted.

**Tech Stack:** Go, `testing`, docker (moby client), the repo's `internal/agent` and `internal/sandbox/image` packages.

**Spec:** `.operator-shared/specs/agent-version-sync.md` (opencode2 `VersionProvider`), `.operator-shared/specs/hermes-agent.md` (install/runtime risks), `.operator-shared/specs/image-composition.md` (stage composition). Work happens in the existing worktree `/workspace/feat--hermes-agent` (branch `feat/hermes-agent`, PR #290).

## Global Constraints

- Run in `/workspace/feat--hermes-agent`; commit to `feat/hermes-agent` (PR #290). **Another agent is committing to this branch in parallel.** Before starting each task, and again before committing, run `git fetch origin && git log --oneline HEAD..origin/feat/hermes-agent`; rebase onto any new commits and re-check that the symbols this plan names still exist (they may have been renamed or their helper replaced).
- The integration image tests resolve a bakeable release through `integrationAgentVersion(t, a) string` (`render_integration_test.go`), which reads the launcher's real upgrade cache and only falls back to `agent.LatestVersion`. Use it; do not reintroduce `resolveAgentVersion` in that file.
- TDD: write the failing test, watch it fail, implement, watch it pass, commit.
- Format and lint with `make fmt` / `make lint` (never `go vet`). `make check` before finishing.
- Unit/compile runs use `make test` / `make compile-all` (they pass `-tags microsandbox_ffi_path`; a bare `go test ./...` links the SDK's ~36 MB native library).
- `make test-integration` requires a working docker daemon and network, and runs host-side only (the dev VM's docker cannot complete these builds). The person running it reports the result back.
- Every added line must be executed by a test; a branch unreachable by construction is removed, not left uncovered.
- Keep `CHANGELOG.md` (`## [Unreleased]` → `### Added`/`### Changed`) and user docs in sync; docs link style per `STYLEGUIDE.md`.
- Reference code by symbol/package in the brain, not `file:line`.

## Review Focus

Inputs/failure modes the spec implies but the existing tests do not exercise; each is pinned by a test in the task named.

- `opencode2 --version` output shape differs from `extractSemverFromOutput`'s first-semver match (Task 1 unit test with the real format; Task 2 run probe).
- Running a daemon agent's `--version` starts the server or hangs instead of printing and exiting (Task 2 run probe, bounded by the docker-run step).
- The custom Fedora base is missing something the final-stage check requires, so the build fails for a reason unrelated to the agent — here a missing `curl` for the daemon runtime check (Task 2; the Dockerfile installs it).
- hermes's provisioned runtime needs `libatomic.so.1` on the managed base, whose `libatomic1` was installed only in the discarded agent stage (Task 2 managed run; Task 4 fix).
- hermes's `--version` needs provider network access before it prints, so it fails in a network-less container (Task 2 runs with the container's default network).

---

### Task 1: Give `opencode2` a `VersionProvider`

**Files:**
- Modify: `internal/agent/opencode2.go`
- Test: `internal/agent/version_provider_test.go`
- Modify: `.operator-shared/specs/agent-version-sync.md`
- Modify: `CHANGELOG.md`

**Interfaces:**
- Consumes: `extractSemverFromOutput(stdout string) (string, error)` (package `agent`).
- Produces: `opencode2Profile.VersionCmd() string` and `opencode2Profile.ParseVersion(string) (string, error)`, making `agent.AsVersionProvider(opencode2Profile) == true`.

- [ ] **Step 1: Write the failing test**

In `internal/agent/version_provider_test.go`, add `"opencode2": "opencode2 --version"` to the map in `TestVersionProviderForBuiltIns`, and add `"opencode2"` to the slice in `TestVersionProvidersParseVersion` (which parses `"1.2.3\n"` and expects `"1.2.3"`).

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/agent/ -run 'TestVersionProvider' -v`
Expected: FAIL — `agent "opencode2" must be a VersionProvider`.

- [ ] **Step 3: Implement the provider in `internal/agent/opencode2.go`**

Add beside `LatestVersion`/`NewerThan`:

```go
func (opencode2Profile) VersionCmd() string { return "opencode2 --version" }
func (opencode2Profile) ParseVersion(stdout string) (string, error) {
	return extractSemverFromOutput(stdout)
}
```

`opencode2` is the binary name the image publishes (`cp … /opt/agents-sandbox/bin/opencode2`) and the name `AttachCommand` invokes, so the probe resolves on the runner PATH.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/agent/ -run 'TestVersionProvider' -v`
Expected: PASS.

- [ ] **Step 5: Update the spec and changelog**

In `.operator-shared/specs/agent-version-sync.md`, replace the present-tense claims that `opencode2` lacks a `VersionProvider` (the "Problem" paragraph naming opencode2, and the "Boot provenance" bullet saying opencode2 needs a source) with the new truth: **every built-in profile implements `agent.VersionProvider`**, so `detectToolAgentVersion` and `recordImageProvenance` write an `opencode2` baseline like any other profile. Keep the acceptance bullet about accepting an upgrade. Add a `CHANGELOG.md` `## [Unreleased]` → `### Fixed` line: opencode2 now reports its installed version, so its `agent_version` baseline is recorded on boot instead of staying empty.

- [ ] **Step 6: Run the package suite and commit**

Run: `make lint && go test ./internal/agent/ ./internal/sandbox/vm/`
Expected: PASS (the vm provenance tests use `opencode`, not `opencode2`, so they are unaffected; fix any that assume opencode2 has no provider).

```bash
git add internal/agent/opencode2.go internal/agent/version_provider_test.go .operator-shared/specs/agent-version-sync.md CHANGELOG.md
git commit -m "feat(agent): opencode2 reports its installed version"
```

---

### Task 2: Build and run every agent profile

**Files:**
- Test: `internal/sandbox/image/render_integration_test.go`

**Interfaces:**
- Consumes: `agent.Names() []string`, `agent.Lookup(string) (agent.Agent, bool)`, `agent.AsVersionProvider`, `integrationAgentVersion(t, a) string`, `renderBytesVersion(t, a, version, project []byte, docker) []byte`, `dockerOff`, `buildDockerfileTag(t, a, dockerfile, agentVersion, dockerEnabled, tag string)`, `imageAgentVersion(t, tag, a) string`, `sanitizeTag(string) string`, `removeTestImage(tag string)`.
- Produces: `TestRenderDockerfileBuildsEveryAgent` (integration).

- [ ] **Step 1: Add the test**

In `internal/sandbox/image/render_integration_test.go`:

```go
// TestRenderDockerfileBuildsEveryAgent builds and runs every registered agent
// profile on the managed base and on a custom non-Debian base. The agent stage
// always builds on the Debian tool base, so the custom base exercises the
// final-stage merge and the agent's *runtime*, not its install: running the
// version probe in the built image is what catches a runtime library the
// discarded agent stage installed (hermes's libatomic1) or a broken binary.
func TestRenderDockerfileBuildsEveryAgent(t *testing.T) {
	origGet := docker.Get
	docker.Get = docker.RealClient
	t.Cleanup(func() { docker.Get = origGet })

	bases := []struct {
		name    string
		project []byte
	}{
		{"managed", nil},
		{"custom-fedora", []byte(
			"FROM fedora:latest\n" +
				"RUN dnf install -y curl libatomic && dnf clean all\n")},
	}

	for _, name := range agent.Names() {
		a, ok := agent.Lookup(name)
		if !ok {
			t.Fatalf("agent %q not registered", name)
		}
		version := integrationAgentVersion(t, a)
		for _, base := range bases {
			t.Run(name+"/"+base.name, func(t *testing.T) {
				tag := "agents-sandbox/it-all-" + sanitizeTag(name) + "-" + base.name
				t.Cleanup(func() { removeTestImage(tag) })

				buildDockerfileTag(t, a, renderBytesVersion(t, a, version, base.project, dockerOff), version, false, tag)

				if got := imageAgentVersion(t, tag, a); got != version {
					t.Errorf("%s on %s: built image reports agent version %q, want %q", name, base.name, got, version)
				}
			})
		}
	}
}
```

- [ ] **Step 2: Verify the integration suite compiles**

Run: `make compile-all`
Expected: PASS (`[no tests to run]` per package; this only checks that the tagged file compiles).

- [ ] **Step 3: Run it on the host and record the result**

On a host with docker and network:
Run: `make test-integration`
Expected: PASS for all twelve subtests. If any fails, record the exact failure — a `hermes/managed` failure naming `libatomic.so.1` selects Task 4; any other failure is a real defect in that profile's install or runtime and must be fixed before this task is done.

- [ ] **Step 4: Commit**

```bash
git add internal/sandbox/image/render_integration_test.go
git commit -m "test(image): build and run every agent profile in CI"
```

---

### Task 3: Remove the integration coverage Task 2 subsumes

**Files:**
- Modify: `internal/sandbox/image/render_integration_test.go`
- Modify: `.operator-shared/specs/image-composition.md`

**Interfaces:**
- Consumes: `TestRenderDockerfileBuildsEveryAgent` from Task 2.
- Produces: no new symbols; removes duplicates only.

- [ ] **Step 1: Delete the duplicate cases**

In `TestRenderDockerfileBuilds`, remove the `no-dockerfile` case (`{"no-dockerfile", nil, dockerOff}`) — Task 2's `opencode/managed` covers managed base, docker off, build and run — and the `custom-base-fedora` case — Task 2's `opencode/custom-fedora` covers the Fedora base, build and run. Keep every docker/marker/implied case and `custom-base-slim-no-tar` (#143).

- [ ] **Step 2: Delete the node regression test**

Delete `TestRenderDockerfileNodeAgentNodeRuns` entirely. Task 2 builds and runs `pi` on the managed base; `pi --version` executes its node bundle, which is the #132 regression the old test caught with `node --version`, and pi's `npm install` build step already proves npm.

- [ ] **Step 3: Verify no orphaned identifiers remain**

Run: `go build ./... && make compile-all`
Expected: PASS — no unused imports or helpers after removing the test.

- [ ] **Step 4: Update the spec reference**

In `.operator-shared/specs/image-composition.md`, replace the `TestRenderDockerfileNodeAgentNodeRuns` regression-guard sentence with the new guard: `TestRenderDockerfileBuildsEveryAgent` (integration) builds and runs every profile, including the npm agent `pi`, on the managed base.

- [ ] **Step 5: Run the suite and commit**

Run: `make lint && make compile-all`
Expected: PASS.

```bash
git add internal/sandbox/image/render_integration_test.go .operator-shared/specs/image-composition.md
git commit -m "test(image): drop integration cases the all-agents build subsumes"
```

---

### Task 4: Hand hermes's runtime library to the runner (only if Task 2 Step 3 failed hermes/managed)

**Trigger:** Task 2's `hermes/managed` subtest fails with `libatomic.so.1` / `cannot open shared object file`. If it passed, skip this task and note in Task 5 that the risk is cleared.

**Files:**
- Modify: `internal/sandbox/image/data/Dockerfile`
- Test: `internal/sandbox/image/render_test.go`
- Modify: `.operator-shared/specs/hermes-agent.md`

**Interfaces:**
- Produces: the managed base image carries `libatomic1`.

- [ ] **Step 1: Write the failing test**

In `internal/sandbox/image/render_test.go`, add `libatomic1` to the assertion set of the test that pins the embedded managed-base packages (the test that renders the managed base and asserts its `apt-get install` package list; extend its `want` list).

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/sandbox/image/ -run <that test name> -v`
Expected: FAIL — the rendered managed base does not install `libatomic1`.

- [ ] **Step 3: Add the package**

In `internal/sandbox/image/data/Dockerfile`, add `libatomic1` to the `apt-get install` package list (keep the list alphabetically sorted as it is now).

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/sandbox/image/ -v`
Expected: PASS.

- [ ] **Step 5: Re-run the run probe on the host and commit**

Run: `make test-integration`
Expected: PASS, `hermes/managed` now prints a version.

Update `.operator-shared/specs/hermes-agent.md`: move the "Runtime availability of the installer's apt packages" open risk to resolved, stating that `libatomic1` now lives in the runner's managed base because the agent-stage copy is discarded.

```bash
git add internal/sandbox/image/data/Dockerfile internal/sandbox/image/render_test.go .operator-shared/specs/hermes-agent.md
git commit -m "fix(image): install libatomic1 in the managed base for hermes"
```

---

### Task 5: Record the coverage posture

**Files:**
- Modify: `.operator-shared/specs/hermes-agent.md`

**Interfaces:**
- Consumes: the Task 2 result (and Task 4, if run).

- [ ] **Step 1: Update the hermes spec**

In `.operator-shared/specs/hermes-agent.md`, state the coverage posture in the "Open risks" or "Harness smoke entry" section: hermes's install and runtime are exercised in CI by `TestRenderDockerfileBuildsEveryAgent` on the managed and a custom non-Debian base; the classic-CLI REPL is exercised end-to-end by the `msb` harness smoke, which is host-only and already gives hermes the heavyweight `ReadyTimeout` budget (`harness-smoke-tests.md`). Note whether the libatomic risk is cleared (Task 4) or was not reproduced. Keep the offline-boot risk's "manual and documented" wording.

- [ ] **Step 2: Verify docs and commit**

Run: `make check`
Expected: PASS (docs-link gate included).

```bash
git add .operator-shared/specs/hermes-agent.md
git commit -m "docs(brain): hermes records its CI coverage posture"
```

---

## Self-Review

- **Spec coverage:** `agent-version-sync.md` → Task 1. `hermes-agent.md` risks + harness entry → Tasks 2/4/5. `image-composition.md` regression-guard reference → Task 3. All covered.
- **Type consistency:** `imageAgentVersion`, `buildDockerfileTag`, `renderBytesVersion`, `sanitizeTag`, `removeTestImage`, `resolveAgentVersion` all referenced with their existing signatures; `VersionCmd`/`ParseVersion` match the `VersionProvider` interface.
- **Review Focus:** each line is pinned by the task named in the section.
- **Proportion:** the plan carries decisions (which cases to delete, where the libatomic fix goes), not the diff.
