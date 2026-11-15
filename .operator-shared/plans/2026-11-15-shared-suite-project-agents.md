# Shared suite project/agent list Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make one list — `suiteProjectAgents()` — the single source for the runner images the `msb` harness smoke suite builds, consumed by both the image prebuild and the runner-image cleanup, so the two cannot drift.

**Architecture:** `test/harness/runner_images.go` already derives the tags the suite built for cleanup, but from its own `harnessProjectAgents()` copy of "what the suite builds". The prebuild in `test/harness/smoke_msb_test.go` (`buildHarnessImages`) and `test/harness/docker_storage_msb_test.go` (`prebuildDockerStorageImage`) hold the other copies. Collapse them: `projectAgent` gains a `Docker bool`, `suiteProjectAgents()` becomes the list, one prebuild loop consumes it, and cleanup reads the same list. A second image added to the list then updates the prebuild and the cleanup together.

**Tech Stack:** Go 1.27, `go test`, golangci-lint, the `msb` build tag (host-only suite).

## Context (read this first)

This is a follow-up to PR #694 (`fix/runner-image-retention`, branch `fix/runner-image-retention`). That PR stopped tagged `agents-sandbox/runner-*` Docker images accumulating from the test tiers. Its whole-branch review raised one deferred Minor, which this plan closes:

> `harnessProjectAgents` is a third copy of "what the suite builds" — it mirrors `TestHarnessSmoke`'s loop and `runDockerStorageDriverSmoke`, and a future second image per agent would silently leave tags behind with every test still green.

**The three copies today:**

1. `buildHarnessImages` (`smoke_msb_test.go`) — loops `agent.Names()`, runs `agents-sandbox build --agent <name>` with `Dir = projectDir(root, <name>)`.
2. `prebuildDockerStorageImage` (`docker_storage_msb_test.go`) — runs `agents-sandbox build --agent opencode --docker` with `Dir = projectDir(root, dockerStorageProject)`, behind its own `prebuildDockerOnce`/`errPrebuildDocker`.
3. `harnessProjectAgents` (`runner_images.go`) — returns `[]projectAgent{{Project, Agent}}` for every `agent.Names()` plus `{dockerStorageProject, "opencode"}`, consumed only by `harnessRunnerImageTags` (cleanup).

**The decision already made (do not re-litigate):** `suiteProjectAgents()` carries a `Docker bool` and both prebuilds collapse into one loop over it. This removes `prebuildDockerStorageImage` entirely rather than leaving it as a second consumer whose agreement with the list is by convention. The alternative — keep two prebuild loops, rename the list — was rejected: it leaves the drift the finding names.

**Out of scope:** the harness *run* loop in `TestHarnessSmoke` stays keyed on `agent.Names()` and `harnessSpecs` (network, seeded files, quit keys — a different concern). Its completeness is already pinned by `test/harness/harness_test.go` ("table key set == `agent.Names()`"). Do not fold the run loop into `suiteProjectAgents()`.

**Branch:** implement as a follow-up commit on `fix/runner-image-retention` (PR #694) — the worktree is at `/workspace/fix--runner-image-retention`, the change is review-adjacent, and CI/PR already exist. If #694 must land exactly as reviewed, take a fresh branch off `main` instead; nothing else changes.

## Global Constraints

- Go style per `AGENTS.md`: self-documenting, non-abbreviated names; comments state the contract.
- Use `make fmt` and `make lint`; never `go fmt` or `go vet`.
- `test/harness/runner_images.go` must keep its `//deadcode:ignore` file-scope marker (the required `deadcode` gate, not covered by `make check`).
- Do not change the cleanup behaviour, the tag derivation, or the sweep.
- `make check` does not run `deadcode`; run `make deadcode` too before finalizing.

## Review Focus

- **A pair added to the list but not built** (or vice versa): the prebuild and cleanup now read one slice, so this reduces to editing `suiteProjectAgents()`; `TestSuiteProjectAgentsCoverEveryBuiltImage` pins the list's shape and the exactly-one `Docker` pair.
- **The docker-storage build losing `--docker`**: the docker-enabled image must still be built with `--docker`; pinned by the list test (`Docker:true` on that pair) and by the prebuild's `if pair.Docker` branch.
- **The two `sync.Once` prebuilds racing or double-running**: collapsing to one `prebuildOnce` removes the second `Once`; the serial prebuild phase still runs once.

---

### Task 1: One list for the built images

**Files:**
- Modify: `test/harness/runner_images.go`
- Modify: `test/harness/smoke_msb_test.go`
- Modify: `test/harness/docker_storage_msb_test.go`
- Modify: `test/harness/runner_images_test.go`
- Modify: `.operator-shared/catalog.md` and this plan's catalog entry (retire this plan when the work lands)

**Interfaces:**
- Produces: `type projectAgent struct { Project, Agent string; Docker bool }`; `func suiteProjectAgents() []projectAgent`.
- Renames: `harnessProjectAgents` → `suiteProjectAgents` (update its callers: `harnessRunnerImageTags`, `seedProjectDirs` in the test).
- Unchanged: `harnessRunnerImageTags(root string) ([]string, error)`, `cleanupRunnerImages(t *testing.T, root string)`, `projectDir(root, name string) string`, `const dockerStorageProject`.

- [ ] **Step 1: Write the failing test**

Add to `test/harness/runner_images_test.go`:

```go
// TestSuiteProjectAgentsCoverEveryBuiltImage pins the one list the prebuild and
// cleanup share: every harness in its own project dir, plus the docker-enabled
// docker-storage pair.
func TestSuiteProjectAgentsCoverEveryBuiltImage(t *testing.T) {
	pairs := suiteProjectAgents()
	if len(pairs) != len(agent.Names())+1 {
		t.Fatalf("pairs = %d, want one per harness plus docker-storage (%d)", len(pairs), len(agent.Names())+1)
	}
	for _, name := range agent.Names() {
		if !slices.Contains(pairs, projectAgent{Project: name, Agent: name}) {
			t.Errorf("missing pair for harness %s; got %v", name, pairs)
		}
	}
	if !slices.Contains(pairs, projectAgent{Project: dockerStorageProject, Agent: "opencode", Docker: true}) {
		t.Errorf("missing the docker-enabled docker-storage pair; got %v", pairs)
	}
	docker := 0
	for _, pair := range pairs {
		if pair.Docker {
			docker++
		}
	}
	if docker != 1 {
		t.Errorf("docker-enabled pairs = %d, want exactly 1", docker)
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./test/harness/ -run TestSuiteProjectAgentsCoverEveryBuiltImage`
Expected: FAIL / compile error — `suiteProjectAgents` and `projectAgent.Docker` undefined.

- [ ] **Step 3: Implement the list in `runner_images.go`**

- Add `Docker bool` to `projectAgent`.
- Rename `harnessProjectAgents` → `suiteProjectAgents`; set `Docker: true` on the docker-storage pair:

```go
// suiteProjectAgents lists the runner images the suite builds: every registered
// harness in its own project dir, plus the docker-storage project's
// docker-enabled opencode image. It is the single source the prebuild and the
// cleanup both read, so they cannot drift.
func suiteProjectAgents() []projectAgent {
	names := agent.Names()
	pairs := make([]projectAgent, 0, len(names)+1)
	for _, name := range names {
		pairs = append(pairs, projectAgent{Project: name, Agent: name})
	}
	return append(pairs, projectAgent{Project: dockerStorageProject, Agent: "opencode", Docker: true})
}
```

- Change `harnessRunnerImageTags` to call `suiteProjectAgents()`.
- In `runner_images_test.go`, change `seedProjectDirs` to call `suiteProjectAgents()`.

- [ ] **Step 4: Collapse the prebuild in `smoke_msb_test.go`**

Replace the body of `buildHarnessImages` so it loops `suiteProjectAgents()` and passes `--docker` when the pair asks for it. Delete the `prebuildDockerStorageImage(t, root)` call in `TestHarnessSmoke` (keep `prebuildImages(t, root)`), and delete `prebuildDockerStorageImage`, `prebuildDockerOnce`, `errPrebuildDocker` from `docker_storage_msb_test.go`.

```go
func buildHarnessImages(t *testing.T, root string) error {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), prepareTimeout)
	defer cancel()

	env := isolatedEnv(root, root, os.Environ())
	pairs := suiteProjectAgents()
	for _, pair := range pairs {
		if err := os.MkdirAll(projectDir(root, pair.Project), 0o755); err != nil {
			return fmt.Errorf("create project dir for %s: %w", pair.Project, err)
		}
	}

	buildErrors := make([]error, len(pairs))
	semaphore := make(chan struct{}, prebuildConcurrency)
	var builds sync.WaitGroup
	for index, pair := range pairs {
		builds.Go(func() {
			semaphore <- struct{}{}
			defer func() { <-semaphore }()

			args := []string{"build", "--agent", pair.Agent}
			if pair.Docker {
				args = append(args, "--docker")
			}
			build := exec.CommandContext(ctx, smokeBinary, args...)
			build.Dir = projectDir(root, pair.Project)
			build.Env = env
			started := time.Now()
			out, err := build.CombinedOutput()
			t.Logf("prebuilt %s image in %s", pair.Agent, time.Since(started).Round(time.Millisecond))
			if err != nil {
				buildErrors[index] = fmt.Errorf("prebuild image for %s: %w\n%s", pair.Agent, err, out)
			}
		})
	}
	builds.Wait()
	return errors.Join(buildErrors...)
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./test/harness/`
Expected: `ok github.com/inoio/agents-sandbox/test/harness`.

Then compile the tagged files (the run suite is `msb`-tagged):
`go test -tags msb -run '^$' ./test/harness/`
Expected: `ok ... [no tests to run]` (compiles; `-run='^$'` runs nothing).

- [ ] **Step 6: Lint, dead-code, and the full gate**

Run: `make fmt && make lint && make deadcode && make check`
Expected: lint `0 issues`; `make deadcode` exit 0 (the file keeps `//deadcode:ignore`); `make check` exit 0.

- [ ] **Step 7: Commit**

```bash
git add test/harness/runner_images.go test/harness/runner_images_test.go \
  test/harness/smoke_msb_test.go test/harness/docker_storage_msb_test.go
git commit -m "test: share one suite project/agent list between prebuild and cleanup"
```

- [ ] **Step 8: Retire this plan**

Once the commit is green, delete this file and its `.operator-shared/catalog.md` entry (the plan is done; git history is the record).

---

### Final verification

- [ ] `make check` and `make deadcode` both exit 0.
- [ ] `grep -rn harnessProjectAgents test/` returns nothing (the rename is complete).
- [ ] Host-only (KVM + docker), if available: `make test-msb-cold` builds and then removes the eight runner images; `make test-msb` reuses them. Not runnable in the agents-sandbox VM.
