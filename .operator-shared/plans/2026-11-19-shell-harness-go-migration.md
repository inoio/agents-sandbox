# Shell test harnesses → Go implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Move the git-fixture shell test harnesses to a Go test package that drives the standalone `scripts/`/`ci/` tools, sharing one git-isolation helper and one fixture builder instead of hand-copying an isolation head into every harness.

**Architecture:** The standalone tools stay bash. Their harnesses move to a new test-only package `test/scripts/` (beside `test/harness/`) that execs `bash <script>` through a shared `testutil.RunScript` helper (which isolates the host git config internally) and builds fixtures with a new go-git-based `internal/testutil/gittest` builder. External CLIs the scripts call (`gh`, `docker`, `msb`) are faked by re-execing the test binary through a `PATH` symlink.

**Tech Stack:** Go 1.27, `github.com/go-git/go-git/v5`, the module's `internal/testutil`, `bash`, `git`/`gh` only inside the scripts under test.

**Spec:** `.operator-shared/specs/testing/shell-script-harnesses.md`

## Global Constraints

- Every test package's `TestMain` calls `mocks.InitFailFastMocks(m)` (`internal/testutil/mocks`).
- A Go harness must not exec an ambient tool (`docker`, `gh`, `git`, `gpg`, `msb`, `ssh`) or call `testutil.RunGit`/`ConfigureRepo` in a default-suite file without a reason-bearing `//testenv:allow <reason>` marker (see `specs/testing/test-isolation.md`). Execing `bash` is allowed.
- Fixtures are go-git; the ambient `git` binary is never exec'd except under an explicit marker.
- The standalone tools (`scripts/*.sh`, `ci/*.sh`) stay bash and keep their `set -euo pipefail` contract.
- `make test` builds with `-tags microsandbox_ffi_path`; the new package must build and pass under it.
- A change to `scripts/x.sh` or its harness must keep `scripts/changed-targets.sh` selecting the right target, pinned by `scripts/test-changed-targets.sh`.
- `make shellcheck` lints every tracked `*.sh`; deleting a harness drops it from the gate automatically.
- No feature work beyond the migration: no change to the standalone tools' behavior.

## Review Focus

The failure modes the spec implies that no single task's tests own, most likely to bite first:

1. **A harness that runs a script whose git picks up the host config.** The assertion is the poisoned-`GIT_CONFIG_GLOBAL` run (Task 3), applied to every migrated harness, not just the pilot.
2. **A fixture whose commit reproduces a patch-id by accident rather than by identical diff.** `backport-audit`/`open-backport-pr` depend on it; Task 3's builder documents the rule and Task 9/8 assert the ported/unported outcomes.
3. **A fake command whose argv handling diverges from the real CLI.** `gh ... --jq`/`-q` matters to `backport-audit` (`serve_doc`) and `enqueue-pr`; the fake must strip and honor it.
4. **A harness that reads or writes ambient paths.** `HOME`/`TMPDIR`/`XDG_*` must be redirected into `t.TempDir()` by the runner; `surface-sweep` and `envelope` additionally own socket/`XDG_DATA_HOME` paths.
5. **A harness migrated onto go-git that actually needed a real-git porcelain.** `rebase-status` (in-progress rebase) and `cleanup-merged-branches` (`worktree`, `clone`, `push --force-with-lease`) stay bash; migrating them is out of scope and a task that touches them is a red flag.

## File Structure

- Create `internal/testutil/runscript.go` — `ScriptResult`, `RunScript`.
- Modify `internal/testutil/testgit.go` — export `GitIsolationEnvironment`.
- Create `internal/testutil/gittest/gittest.go` — fixtures via go-git.
- Create `internal/testutil/fakecmd/fakecmd.go` — re-exec faked commands.
- Create `test/scripts/main_test.go`, `test/scripts/helpers_test.go`, and one `<tool>_test.go` per migrated harness.
- Modify `Makefile`, `scripts/changed-targets.sh`, `scripts/test-changed-targets.sh`.
- Delete `scripts/test-<tool>.sh` and `ci/test-check-<tool>.sh` as each is migrated.
- Modify `.operator-shared/specs/testing/shell-script-harnesses.md` (`Current state`).

---

## Phase 1 — shared helpers

### Task 1: Export the git-isolation environment

**Files:**
- Modify: `internal/testutil/testgit.go`
- Test: `internal/testutil/testutil_test.go` (or a new `testgit_test.go`)

**Interfaces:**
- Produces: `func GitIsolationEnvironment(environ []string) []string`

- [ ] **Step 1: Write the failing test**

```go
func TestGitIsolationEnvironmentStripsAmbientSettings(t *testing.T) {
	in := []string{"PATH=/bin", "GIT_CONFIG_GLOBAL=/home/me/.gitconfig", "GIT_ASKPASS=x", "GIT_CONFIG_KEY_0=commit.gpgsign", "HOME=/home/me"}
	got := GitIsolationEnvironment(in)
	// PATH and HOME survive; the four ambient settings do not.
	// GIT_CONFIG_NOSYSTEM=1, GIT_CONFIG_GLOBAL=/dev/null, GIT_TERMINAL_PROMPT=0 are appended.
}
```

- [ ] **Step 2: Run it and see it fail to compile** (`GitIsolationEnvironment` undefined).

Run: `go test ./internal/testutil/ -run TestGitIsolationEnvironment -v`

- [ ] **Step 3: Rename `gitIsolationEnvironment` to `GitIsolationEnvironment`**

Update its doc comment to state the contract (the exported entry point; `isAmbientGitSetting` stays unexported). Update the two internal callers (`RunGit`) accordingly.

- [ ] **Step 4: Run the tests** — `go test ./internal/testutil/ -v` — PASS.

- [ ] **Step 5: Commit.** `git commit -m "test: export GitIsolationEnvironment from testutil"`

### Task 2: Add `RunScript`

**Files:**
- Create: `internal/testutil/runscript.go`
- Test: `internal/testutil/runscript_test.go`

**Interfaces:**
- Consumes: `GitIsolationEnvironment` (Task 1).
- Produces:
```go
type ScriptResult struct { Stdout, Stderr string; Status int }
func RunScript(tb testing.TB, dir string, extraEnv map[string]string, script string, args ...string) ScriptResult
```

- [ ] **Step 1: Write the failing test**

Assert: a script that echoes `$GIT_CONFIG_GLOBAL`/`$HOME` observes the isolated values; the exit status is captured (a script doing `exit 7` yields `Status == 7`); a missing `bash`-fatal (non-ExitError) calls `tb.Fatalf`. Point `HOME` at a temp dir via `extraEnv` and assert it is set.

```go
func TestRunScriptIsolatesAndCaptures(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "s.sh")
	os.WriteFile(script, []byte("#!/usr/bin/env bash\necho \"glob=$GIT_CONFIG_GLOBAL\"\necho err >&2\nexit 7\n"), 0o755)
	got := RunScript(t, dir, map[string]string{"HOME": dir}, script)
	// got.Status == 7; got.Stderr == "err\n"; got.Stdout contains "glob=/dev/null"
}
```

- [ ] **Step 2: Run it** — FAIL (undefined).

- [ ] **Step 3: Implement** `RunScript`: `exec.Command("bash", append([]string{script}, args...)...)`, `cmd.Dir = dir`, `cmd.Env` = `overlay(GitIsolationEnvironment(os.Environ()), extraEnv)` (drop then set, so no duplicate keys), capture both streams, map `*exec.ExitError` to `Status`, any other error to `tb.Fatalf`.

- [ ] **Step 4: Run** — PASS.

- [ ] **Step 5: Commit.**

### Task 3: Add the go-git fixture builder

**Files:**
- Create: `internal/testutil/gittest/gittest.go`
- Test: `internal/testutil/gittest/gittest_test.go`

**Interfaces:**
- Produces:
```go
type Repo struct{ /* unexported */ }
func New(tb testing.TB) *Repo
func (r *Repo) Dir() string
func (r *Repo) Write(name, content string)
func (r *Repo) Commit(message string) plumbing.Hash      // stages every change
func (r *Repo) SetBranch(name string, at plumbing.Hash)
func (r *Repo) Checkout(name string)
func (r *Repo) MergeCommit(message string, into, from plumbing.Hash) plumbing.Hash // parents [into, from], tree of `from`
func (r *Repo) AddBareOrigin() string                    // creates bare origin, remote "origin", returns its path
func (r *Repo) Push(tb testing.TB)                       // pushes all local branches to origin
func OriginRefs(tb testing.TB, origin string) []string
func OriginHash(tb testing.TB, origin, branch string) (plumbing.Hash, bool)
```

- [ ] **Step 1: Write the failing tests** covering: a `Commit` exposes the file content; `SetBranch`+`Checkout` moves the working tree; `MergeCommit` with parents `[base, second]` makes `git log --no-merges <merge>^1..<merge>` (run via a `RunScript`-style `git` call under isolation, or go-git parent walk) yield both source commits; `Push` then `OriginRefs` contains the pushed branch.

- [ ] **Step 2: Run** — FAIL (undefined).

- [ ] **Step 3: Implement** on go-git (`PlainInitWithOptions` for the worktree, bare `PlainInitWithOptions` for origin, `CreateRemote` + `Push` with a `+refs/heads/*:refs/heads/*` refspec). `MergeCommit` stores a raw `object.Commit` (go-git has no merge command) with two `ParentHashes` and `from`'s `TreeHash`. Document in the package comment the rule a fixture follows to reproduce a patch-id match: commit the same diff, never cherry-pick.

- [ ] **Step 4: Run** — PASS.

- [ ] **Step 5: Commit.**

### Task 4: Add the re-exec fake-command helper

**Files:**
- Create: `internal/testutil/fakecmd/fakecmd.go`
- Test: `internal/testutil/fakecmd/fakecmd_test.go`

**Interfaces:**
- Produces:
```go
type Handler func(args []string) (stdout, stderr string, status int)
func Register(name string, h Handler)
func BinDir(tb testing.TB, name string) string   // holds `name` -> symlink to os.Executable()
func Dispatch() int                              // runs the registered handler when launched as a fake; -1 otherwise
```

- [ ] **Step 1: Write the failing tests**: `BinDir` creates an executable `name`; a child process started with `fakecmd`'s marker env and `Dispatch()` used as the entry point returns the handler's stdout/status; a call with no registered handler returns status 1.

- [ ] **Step 2: Run** — FAIL.

- [ ] **Step 3: Implement**: the marker is an env var set by `BinDir`'s child (e.g. `AGENTS_SANDBOX_FAKE_COMMAND=<name>`); `Dispatch` reads it and the command args, looks up the handler, prints, and returns the status. A test package's `TestMain` calls `Dispatch()` first and `os.Exit` with its status when it returns ≥ 0.

- [ ] **Step 4: Run** — PASS.

- [ ] **Step 5: Commit.**

---

## Phase 2 — package + pilot

### Task 5: Create `test/scripts` and migrate `open-backport-pr`

**Files:**
- Create: `test/scripts/main_test.go`, `test/scripts/helpers_test.go`, `test/scripts/open_backport_pr_test.go`
- Read for cases: `scripts/test-open-backport-pr.sh`

**Interfaces:**
- Consumes: `testutil.RunScript`, `gittest`, `fakecmd`.

- [ ] **Step 1: Write `main_test.go`** — `func TestMain(m *testing.M) { if status := fakecmd.Dispatch(); status >= 0 { os.Exit(status) }; mocks.InitFailFastMocks(m) }`.

- [ ] **Step 2: Write `helpers_test.go`** — a `newScriptEnv(tb)` returning `(*gittest.Repo, map[string]string)` with `binDir` on `PATH`, `BACKPORT_REPO`, and the fake-`gh` vars; a `gh` `fakecmd.Handler` that logs argv and honors `--json`/`-q` the way `scripts/test-open-backport-pr.sh`'s shell fake does.

- [ ] **Step 3: Port every case** from `scripts/test-open-backport-pr.sh` as subtests: clean port; fragment rename; tracker reference; rename push failure; conflict aborts without pushing; partially ported; empty commit; already-present; fully ported; no release branch; unknown PR; usage. Assert the same exit codes (`0/1/2/3/4`), stdout/stderr substrings, `gh` argv, and origin refs.

- [ ] **Step 4: Run** — `go test ./test/scripts/ -run OpenBackport -v` — PASS.

- [ ] **Step 5: Run under the poison** — with a `commit.gpgsign`+ssh `GIT_CONFIG_GLOBAL` set, the suite still passes (isolation proof).

- [ ] **Step 6: Commit.**

### Task 6: Delete the pilot's bash harness and rewire the map

**Files:**
- Delete: `scripts/test-open-backport-pr.sh`
- Modify: `scripts/changed-targets.sh` (the `open-backport-pr` case), `scripts/test-changed-targets.sh` (the two pinned lines), `Makefile` (`open-backport-pr-test`)

**Interfaces:**
- Produces: `make open-backport-pr-test` runs `go test ./test/scripts/ -run OpenBackport -v`.

- [ ] **Step 1: Update the failing map test** — change the expected target mapping for `scripts/open-backport-pr.sh` to the same `open-backport-pr-test` (now a Go test); add a case for a changed `test/scripts/open_backport_pr_test.go` selecting it.
- [ ] **Step 2: Run** `./scripts/test-changed-targets.sh` — FAIL on the not-yet-updated map.
- [ ] **Step 3: Update `changed-targets.sh`** to map both `scripts/open-backport-pr.sh` and `test/scripts/open_backport_pr_test.go` to `open-backport-pr-test`, and `Makefile` to run the Go test. Delete the bash harness.
- [ ] **Step 4: Run** `./scripts/test-changed-targets.sh` and `make open-backport-pr-test` — PASS.
- [ ] **Step 5: Commit.**

### Task 7: Fold the pilot's outcome into the spec

**Files:** Modify: `.operator-shared/specs/testing/shell-script-harnesses.md`

- [ ] **Step 1: Update `Current state`** to record the pilot migrated and the helpers exported.
- [ ] **Step 2: Run** `make brain-check` — PASS.
- [ ] **Step 3: Commit.**

---

## Phase 3 — the remaining go-git harnesses

Each task follows the Task 5 template: create `test/scripts/<tool>_test.go`, port every case from the named bash harness, run `go test ./test/scripts/ -run <Tool> -v`, run under the poison, then (Task-6 style) delete the bash harness and rewire `changed-targets.sh` + `test-changed-targets.sh` + `Makefile`. Do not re-specify the shared helpers; consume them.

### Task 8: `backport-audit` (`scripts/test-backport-audit.sh`)

Cases: no release branch; unported; truncated expected set; one row below the cap; merge commit not in the clone; empty commit; ported; merged within grace; open backport PR; merged backport PR clears an adapted port; two open PRs; line-start reference; truncated merged backports; unlabeled merged PR; duplicate patch-id; adapted port; issue label (unported, ported); issue+PR label; issue references an unmerged PR; usage. The fake `gh` must honor `--jq` and the `GH_MERGED_ROWS`/`GH_MERGED_BACKPORTS_ROWS` page-truncation knobs (Review Focus 3).

### Task 9: `changed-files` (`scripts/test-changed-files.sh`)

Cases: clean tree; modified tracked; modified+untracked; deleted; rename endpoints; branch mode merge-base; branch mode untracked; missing base; usage.

### Task 10: `changed-targets` (`scripts/test-changed-targets.sh`)

Cases: no changes; deleted/untracked Go; rename source class; branch merge-base; missing base selects all; the shim dir; each per-script mapping (including the new `test/scripts` paths from Tasks 6+).

### Task 11: `upstream-drift` (`scripts/test-upstream-drift.sh`)

Cases: counts; overlap; names; quiet mode; usage.

### Task 12: `enqueue-pr` (`scripts/test-enqueue-pr.sh`)

Cases: merged; watch+enqueue; linked issue needs-backport (copy, refused copy); no backport marker; closed/pending/conflict; enqueue failure; usage. Fake `gh` honors `--jq` and the canned view/exit-code knobs.

### Task 13: `event-base` (`ci/test-check-event-base.sh`)

Cases: each event (push, pull_request) and the undecidable forms. Fixture origin is a local bare repo (`gittest`).

---

## Phase 4 — the non-git and near-git harnesses

Same template unless noted. These are mostly fixture trees and stub binaries, which go-git does not enter.

### Task 14: `brain-check` (`scripts/test-brain-check.sh`) — build Markdown brain fixtures in `t.TempDir()`; port each structural rule's case.

### Task 15: `check` (`scripts/test-check.sh`) — a stub `make` written by the test; assert the `PASS`/`FAIL` summary, `JOBS`/`SERIAL`/`VERBOSE`, and ordering.

### Task 16: `changelog` (`ci/test-check-changelog.sh`) — `changelog.d/` fixtures and the shape/slug/presence cases.

### Task 17: `ci-guards` (`ci/test-check-ci-guards.sh`) — workflow fixtures; port the canonical-block, exempt-path, event-name, and job-gate cases.

### Task 18: `classify-changes` (`ci/test-check-classify-changes.sh`) — changed-file lists; the `code=…`/`exec=…` cases.

### Task 19: `deadcode` (`ci/test-check-deadcode.sh`) — fixture files plus the `DEADCODE_*` env and the `DEADCODE_CHECKER` reuse; build the checker once per test binary.

### Task 20: `surface-sweep` (`scripts/test-surface-sweep.sh`) — fake `msb`/`asb`/docker binaries via `fakecmd`; port the socket-budget, env/lock, sweep, runner-image, and failure cases. `XDG_DATA_HOME`/`HOME` under the root are set by the runner.

### Task 21: `envelope` (`scripts/test-envelope_test.sh`) — drives `scripts/test-envelope.sh`; port the env-scrub, netns, source-dir, and spaced-path cases.

---

## Phase 5 — finish the gate

### Task 22: Retire the bash harnesses and close the gate

**Files:**
- Delete: any remaining `scripts/test-*.sh`/`ci/test-check-*.sh` migrated above (keep the two Task-Review-Focus-5 harnesses) and `scripts/test-envelope.sh` if unused.
- Modify: `Makefile` (`CHECK_LIGHT_TARGETS`, each `*-test` target), `scripts/changed-targets.sh`, `scripts/test-changed-targets.sh`, `CONTRIBUTING.md`/`docs` if they describe `make check` layout, `specs/testing/shell-script-harnesses.md`.

- [ ] **Step 1: Confirm `rebase-status` and `cleanup-merged-branches` stay bash**; add the isolation head to any bash harness that creates git fixtures and lacks it (the spec's `Current state` names the two offenders — they may already be migrated, in which case drop this).
- [ ] **Step 2: Repoint every migrated `*-test` Make target** at `go test ./test/scripts/ -run <Tool>`; delete the dead bash harnesses.
- [ ] **Step 3: Run the full gate** — `make check` — PASS, `make shellcheck` clean, `make test` green under `-tags microsandbox_ffi_path`.
- [ ] **Step 4: Update the spec** (`Current state` → the migrated set and the two that stay bash), run `make brain-check`.
- [ ] **Step 5: Commit.** Then delete this plan: its work has landed and the spec now carries the durable clauses.

---

## Self-review notes

- **Spec coverage:** isolation contract → Tasks 1–3; shared helpers → Tasks 1–4; `test/scripts` home → Task 5; fake commands → Task 4/5; ambient contract → Global Constraints + Task 5 Step 5; "where bash stays" → Tasks 14–21 and Task 22 Step 1.
- **Type consistency:** `GitIsolationEnvironment(environ []string) []string`, `RunScript(tb, dir, extraEnv, script, args...) ScriptResult`, and the `gittest.Repo` methods are consumed unchanged in every later task.
- **Proportion:** infrastructure and the pilot are specified with tests; the repeated harnesses reference the pilot's template rather than re-transcribing it.
