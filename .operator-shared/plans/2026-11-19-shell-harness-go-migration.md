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
5. **A harness migrated onto go-git that actually needed a real-git porcelain.** `rebase-status` (in-progress rebase) and `cleanup-merged-branches` (`worktree`, `clone --bare`, `push --force-with-lease`) could not be built on go-git. Superseded: both were ported to Go and drive real git through the shared exec-git porcelain helper `test/scripts/gitporcelain_helpers_test.go` (the single sanctioned ambient-git exec in `test/scripts`), so the host-git-config isolation is applied at one chokepoint and cannot drift.

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

## Follow-up (bundled onto this PR)

Both tasks pick up `Final rulings (deferred minors)` from the review ledger; they land on this branch (PR #742), not a new one.

### Task 23: Enforce that the audit reads a PR's commits from the clone, not the API

**Files:**
- Modify: `test/scripts/helpers_test.go` (the fake `gh`)
- Modify: `test/scripts/backport_audit_test.go`

**Interfaces:**
- Produces: `func requestsJSONField(args []string, name string) bool`

- [ ] **Step 1: Fail the fake `gh` on a `commits` field** — in `fakeGhHandler`, after the argv record, return `"", "fake gh: the audit must not request commits: "+strings.Join(args, " ")+"\n", 1` when `requestsJSONField(args, "commits")` is true.

- [ ] **Step 2: Add `requestsJSONField`** — scan `args` for `--json`, split its value on `,`, trim each element, and report whether one equals `name`.

- [ ] **Step 3: Delete the weak assertion** — remove `assertNotContains(t, "argv", h.argv(), "--json commits")` from the `truncated expected set` subtest; it inspects the argv of a run that aborts before the commit read, for a token no code path emits.

- [ ] **Step 4: Pin the debt to the clone's commits** — in the `issue label, unlabeled PR` subtest (which reaches the per-PR read), add `assertContains(t, "stdout", result.Stdout, "missing:1")`.

- [ ] **Step 5: Run** — `CGO_ENABLED=1 go test -tags microsandbox_ffi_path -count=1 ./test/scripts/ -run BackportAudit` — PASS.

- [ ] **Step 6: Verify the probe bites** — in a scratch copy, append `,commits` to a `--json` list in `scripts/backport-audit.sh`; the suite fails. Revert.

- [ ] **Step 7: Commit.**

### Task 24: Re-home the Go harness helpers as test-only files

**Files:**
- Create: `test/scripts/gittest_helpers_test.go`, `test/scripts/runscript_helpers_test.go`, `test/scripts/fakecmd_helpers_test.go` (the moved production files, `package scripts`)
- Create: `test/scripts/gittest_test.go`, `test/scripts/runscript_test.go`, `test/scripts/fakecmd_test.go` (the moved unit tests)
- Modify: `test/scripts/main_test.go`, `test/scripts/helpers_test.go`, every `test/scripts/*_test.go` call site
- Delete: `internal/testutil/gittest/`, `internal/testutil/fakecmd/`, `internal/testutil/runscript.go`, `internal/testutil/runscript_test.go`
- Modify: `.codecov.yml`

**Interfaces:**
- Consumes: `testutil.GitIsolationEnvironment` (Task 1), `testutil.WritePath`, `internal/testutil/mocks`.
- Produces: the same symbols as `internal/testutil/gittest`, `internal/testutil/fakecmd`, `internal/testutil.RunScript`, now unqualified in `package scripts`.

- [ ] **Step 1: Move `gittest`** — `git mv internal/testutil/gittest/gittest.go test/scripts/gittest_helpers_test.go`, change its `package gittest` to `package scripts`, drop the file-scope `//deadcode:ignore`; move `gittest_test.go` to `test/scripts/gittest_test.go` (`package scripts`); delete `internal/testutil/gittest/main_test.go` (the folded `TestMain` is Step 6). Drop the `gittest.` qualifier at every call site in `test/scripts/*_test.go`.

- [ ] **Step 2: Move `fakecmd`** — same move for `fakecmd.go` → `test/scripts/fakecmd_helpers_test.go` and `fakecmd_test.go` → `test/scripts/fakecmd_test.go`; delete its `main_test.go`; drop the `fakecmd.` qualifier at every call site.

- [ ] **Step 3: Move `RunScript`** — `internal/testutil/runscript.go` → `test/scripts/runscript_helpers_test.go` (`package scripts`), `internal/testutil/runscript_test.go` → `test/scripts/runscript_test.go`; drop the file-scope marker; qualify `GitIsolationEnvironment` as `testutil.GitIsolationEnvironment`; drop the `testutil.` qualifier from `RunScript`/`ScriptResult` at every call site (keep `testutil.WritePath`).

- [ ] **Step 4: Fold the `TestMain`** — `test/scripts/main_test.go` registers `"faketool"` (the handler from the deleted `internal/testutil/fakecmd/main_test.go`) before `Dispatch()`, and calls `Register`/`Dispatch` unqualified; keep `mocks.InitFailFastMocks(m)`.

- [ ] **Step 5: Resolve collisions** — the moved `gittest.go` names (`New`, `Repo`, `Write`, `Commit`, …) must not clash with the existing `test/scripts/helpers_test.go` names; rename within the moved file where they do.

- [ ] **Step 6: Remove the coverage exceptions** — in `.codecov.yml` delete `"**/testutil/runscript.go"`, `"**/testutil/gittest/*.go"`, `"**/testutil/fakecmd/*.go"`; rewrite the comment to keep only `testgit.go`'s reason and state that a test-support helper living in another package is re-homed into its consumer's `_test.go` rather than ignored.

- [ ] **Step 7: Run** — `go test -tags microsandbox_ffi_path -count=1 ./test/scripts/ ./internal/testutil/...` PASS; `make deadcode` clean (three fewer markers); `make shellcheck`; `make changed-targets-test`.

- [ ] **Step 8: Commit.**

### Task 25: Sync the brain and changelog

**Files:** Modify `specs/testing/shell-script-harnesses.md`, `specs/testing/test-coverage.md`, `index/index.md`, `catalog.md`, `changelog.d/742.changed.md`.

- [ ] **Step 1: `shell-script-harnesses.md`** — the shared helpers now live in `test/scripts` as `*_helpers_test.go`; `GitIsolationEnvironment` stays in `internal/testutil/testgit.go` as the exported entry point. Update the frontmatter `description` and the `The shared Go helpers`/`Where harnesses live` sections.
- [ ] **Step 2: `test-coverage.md`** — drop the harness-helpers clause; keep `testgit.go`'s reason.
- [ ] **Step 3: Index/catalog** — `internal/testutil` no longer owns `gittest`/`RunScript`/`fakecmd`; `test/scripts` does.
- [ ] **Step 4: `changelog.d/742.changed.md`** — record the re-homing and the stronger audit probe.
- [ ] **Step 5: Run** — `make brain-check` — PASS; `bash ci/check-changelog.sh . --changed <(printf 'test/scripts/gittest_helpers_test.go\n')` — PASS.
- [ ] **Step 6: Commit.**

---

## Self-review notes
- **Spec coverage:** isolation contract → Tasks 1–3; shared helpers → Tasks 1–4; `test/scripts` home → Task 5; fake commands → Task 4/5; ambient contract → Global Constraints + Task 5 Step 5; "where bash stays" → Tasks 14–21 and Task 22 Step 1.
- **Type consistency:** `GitIsolationEnvironment(environ []string) []string`, `RunScript(tb, dir, extraEnv, script, args...) ScriptResult`, and the `gittest.Repo` methods are consumed unchanged in every later task.
- **Proportion:** infrastructure and the pilot are specified with tests; the repeated harnesses reference the pilot's template rather than re-transcribing it.

---

# Phase 6 — the two porcelain harnesses (approved extension)

> **For agentic workers:** This phase supersedes **Review Focus 5** above. The two harnesses it
> named (`rebase-status`, `cleanup-merged-branches`) are ported to Go after all: the benefit is not
> go-git fixtures (they need real-git porcelain) but that their fixture `git` runs through the same
> shared isolation chokepoint as the other thirteen, so the head can never drift.

**Goal:** Move `scripts/test-rebase-status.sh` and `scripts/test-cleanup-merged-branches.sh` into
`test/scripts`, driving each tool through `RunScript` and building each fixture's git state through
one exec-git helper that applies `GitIsolationEnvironment`.

**Architecture:** New `test/scripts/gitporcelain_helpers_test.go` funnels every fixture git command
through a single function that calls `testutil.RunGit` (which applies `GitIsolationEnvironment`),
carrying one reason-bearing `//testenv:allow` marker. Thin wrappers (`bareClone`, `worktreeAdd`,
`updateRef`, `revParse`, …) call it. Harness code carries no markers; the tools stay bash and
unchanged; `ci/test-check-docs.sh` stays bash (git-free, out of scope).

**Tech Stack:** Go 1.27, `internal/testutil.RunGit`, the existing `test/scripts` helpers
(`RunScript`, `fakecmd`, `assertStatus`/`assertContains`/`assertNotContains`).

**Spec:** `.operator-shared/specs/testing/shell-script-harnesses.md`

**Execution order:** Tasks 26 → 27 → 28 (helper, then the two ports), then 30 → 31 → 32 (rename,
macOS fix, optional check-docs port), and **Task 29 last** (the brain/gate close-out).

## Global Constraints (Phase 6)

- Every test package's `TestMain` calls `mocks.InitFailFastMocks(m)` — already done.
- Fixture `git` runs only through the shared exec helper; the sole `//testenv:allow` marker is on
  that helper's `testutil.RunGit` call. No other default-suite file execs `git`.
- Driving a tool under test uses `RunScript` (already isolated). Execing `bash` is allowed.
- The standalone tools (`rebase-status.sh`, `cleanup-merged-branches.sh`) stay bash, behavior
  unchanged.
- `make test` builds with `-tags microsandbox_ffi_path`; the package must build and pass under it.
- A change to a harness must keep `scripts/changed-targets.sh` selecting the right target, pinned by
  `test/scripts/changed_targets_test.go`.
- Adding a fake requires `Register` in `test/scripts/main_test.go` before `Dispatch`; use `fakecmd`
  for `gh` (no shell stub).

## Review Focus (Phase 6)

1. **A ported harness that still execs ambient `git` outside the helper.** The scanner
   (`internal/testutil/real_command_contract_test.go`) flags any `exec.Command("git", …)` or
   `testutil.RunGit` without a marker; only `gitporcelain_helpers_test.go` may appear in its output.
2. **A fixture that does not reproduce the bash repo shape.** `cleanup-merged-branches` depends on
   exact refs/worktree topology (config mirror, `refs/remotes/origin/*`, detached worktrees) and on
   real porcelain (`clone --bare`, `worktree`, `cherry-pick`, `merge`, `--force-with-lease`);
   `rebase-status` needs a genuinely in-progress conflicting rebase. A fixture that only approximates
   it makes the tool's output assertions vacuous.
3. **A `gh` fake whose argv handling diverges from the stub.** `cleanup-merged-branches` calls
   `gh repo view`, `gh pr view`, `gh api`; the fake must serve the same shape the bash stub did.
4. **A harness that reads or writes ambient paths.** `HOME`/`TMPDIR`/`XDG_*` stay redirected into
   `t.TempDir()`; the fixtures live under it.

---

### Task 26: Add the exec-git fixture helper

**Files:**
- Create: `test/scripts/gitporcelain_helpers_test.go`
- Test: `test/scripts/gitporcelain_test.go`

**Interfaces:**
- Consumes: `testutil.RunGit(tb, dir, args...) string`.
- Produces (all in `package scripts`, tool-named to avoid collisions):
  - `porcelainGit(tb testing.TB, dir string, args ...string) string` — the one marked call.
  - `gitInit(tb, dir string)`, `gitConfig(tb, dir, key, value string)`, `gitCommitAll(tb, dir, msg string)`,
    `gitBranch(tb, dir, name, at string)`, `gitCheckout(tb, dir, name string)`,
    `gitSymbolicRefHead(tb, dir, ref string)`, `gitBareClone(tb, seed, into string)`,
    `gitWorktreeAdd(tb, dir, path, rev string)`, `gitWorktreeAddDetached(tb, dir, path, rev string)`,
    `gitUpdateRef(tb, dir, ref, rev string)`, `gitSymbolicRef(tb, dir, ref, target string)`,
    `gitCherryPick(tb, dir, rev string)`, `gitMerge(tb, dir, rev string)`,
    `gitRevParse(tb, dir, rev string) string`, `gitRebase(tb, dir, onto string) error` (captures failure),
    `gitRebaseAbort(tb, dir string)`, `gitCloneBareConfig(...)`.

- [ ] **Step 1: Write the failing test** — a repo built with the helper yields the expected
  `rev-parse` values, a `worktree add` creates the tree, and `porcelainGit` returns trimmed stdout:

```go
func TestPorcelainGitHelpers(t *testing.T) {
	dir := t.TempDir()
	gitInit(t, dir)
	gitConfig(t, dir, "user.email", "test@example.com")
	gitConfig(t, dir, "user.name", "test")
	os.WriteFile(filepath.Join(dir, "f"), []byte("a\n"), 0o600)
	gitCommitAll(t, dir, "base") // stages every change
	if got := gitRevParse(t, dir, "HEAD"); len(got) != 40 { t.Fatalf("HEAD = %q", got) }
}
```

- [ ] **Step 2: Run** `CGO_ENABLED=1 go test -tags microsandbox_ffi_path -count=1 ./test/scripts/ -run TestPorcelainGitHelpers` — FAIL (undefined).

- [ ] **Step 3: Implement** the helper. `porcelainGit` calls `testutil.RunGit` with a
  `//testenv:allow the two porcelain fixtures drive the real git the tool needs` marker; every other
  function wraps it. `gitRevParse` returns the first line trimmed.

- [ ] **Step 4: Run** — PASS. Also `CGO_ENABLED=1 go test -tags microsandbox_ffi_path -count=1 ./internal/testutil/ -run TestUntaggedTestsExecuteNoAmbientTool` — PASS (the scanner sees only the sanctioned helper).

- [ ] **Step 5: Commit.**

### Task 27: Port `rebase-status`

**Files:**
- Create: `test/scripts/rebase_status_test.go`
- Delete: `scripts/test-rebase-status.sh`
- Read for cases: `scripts/test-rebase-status.sh`
- Modify (wiring): `Makefile` (`rebase-status-test`), `scripts/changed-targets.sh`, `test/scripts/changed_targets_test.go`

**Interfaces:**
- Consumes: Task 26 helpers; `RunScript`, `assertStatus`/`assertContains`/`assertNotContains`.
- Produces: `TestRebaseStatus`; `Makefile`'s `rebase-status-test` runs `go test ./test/scripts/ -run RebaseStatus`.

- [ ] **Step 1: Write the failing test** — port every case: clean repo (`no rebase in progress`,
  `unmerged paths: 0`, `files with conflict markers: 0`); a conflicting rebase (built with
  `gitInit`/`gitConfig`/`gitCommitAll`/`gitBranch`/`gitCheckout`/`gitRebase` as the bash harness does)
  asserting `rebase in progress`, `step 2/2`, `onto <main-tip>`, `unmerged paths: 1`, `conflict.txt`,
  `files with conflict markers: 1`; `gitRebaseAbort` then clean; missing repo → non-zero; `--help` → 0.
  Run the tool with `RunScript(t, repoDir, nil, repositoryScript(t, "rebase-status.sh"), "--repo", repoDir)`.
- [ ] **Step 2: Run** `-run RebaseStatus` — FAIL (undefined).
- [ ] **Step 3: Implement** and delete `scripts/test-rebase-status.sh`; repoint the Make target,
  `scripts/changed-targets.sh` (`scripts/rebase-status.sh|test/scripts/rebase_status_test.go` →
  `rebase-status-test`), and the pinned cases in `test/scripts/changed_targets_test.go`.
- [ ] **Step 4: Run** `-run RebaseStatus` and `-run ChangedTargets` — PASS.
- [ ] **Step 5: Commit.**

### Task 28: Port `cleanup-merged-branches`

**Files:**
- Create: `test/scripts/cleanup_merged_branches_test.go`
- Delete: `scripts/test-cleanup-merged-branches.sh`
- Read for cases: `scripts/test-cleanup-merged-branches.sh`
- Modify: `Makefile` (`cleanup-branches-test`), `scripts/changed-targets.sh`,
  `test/scripts/changed_targets_test.go`, `test/scripts/main_test.go` (register the `gh` fake)

**Interfaces:**
- Consumes: Task 26 helpers; `RunScript`, `fakecmd`; a local `cleanupSection(output, header) string`
  helper (the bash `section` awk: the lines under a header up to the next blank line).
- Produces: `TestCleanupMergedBranches`; a `cleanupMergedBranchesFakeGh` handler registered as `gh`;
  `Makefile`'s `cleanup-branches-test` runs `go test ./test/scripts/ -run CleanupMergedBranches`.

- [ ] **Step 1: Write the failing test** — build `newCleanupFixture(tb, configDir, configBranch, symlink, dirty, containment)`
  with the helpers: seed repo (init, `.agents-sandbox/Dockerfile`, commit, `branch -M main`, optional
  containment history via cherry-pick/merge/amend), `gitBareClone(seed, proj+"/.git")`, config,
  `worktree.useRelativePaths true`, `worktree add` for `main` and the config branch, the `.agents-sandbox`
  symlink (relative/absolute/none), advance `main`, `update-ref` the `origin/*` refs, `symbolic-ref`
  `origin/HEAD`, `branch stale-branch`, `worktree add -b old-work`, the containment detached worktrees,
  and an optional untracked dirt file. Port every assertion block from the bash harness (cases 1–4,
  branch-loop protection, `--quiet`, non-TTY abort, zero-planned-actions, content containment, detached
  worktrees, dirty detached, remote branches, kept-entry notes, fresh worktree, and the two `gh` cases).
  Run the tool via `RunScript` with `cmd.Dir = proj+"/main"` and the bash flags
  `--dry-run --no-fetch [--no-gh] --fresh 0 --color never`; use the `gh` fake for the two gh cases.
- [ ] **Step 2: Run** `-run CleanupMergedBranches` — FAIL (undefined).
- [ ] **Step 3: Implement** and delete the bash harness; register the fake in `main_test.go`; repoint
  `Makefile`, `scripts/changed-targets.sh`
  (`scripts/cleanup-merged-branches.sh|test/scripts/cleanup_merged_branches_test.go` → `cleanup-branches-test`),
  and `test/scripts/changed_targets_test.go`.
- [ ] **Step 4: Run** `-run CleanupMergedBranches` and `-run ChangedTargets` — PASS.
- [ ] **Step 5: Commit.**

### Task 29: Sync the brain and close the gate
**Files:**
- Modify: `.operator-shared/specs/testing/shell-script-harnesses.md` (frontmatter, "Where harnesses
  live", `Current state`), `.operator-shared/index/index.md`, `.operator-shared/catalog.md`,
  `changelog.d/<n>.changed.md`
- Modify: the plan's **Review Focus 5** note above (superseded — already done in this phase)

- [ ] **Step 1: Update `shell-script-harnesses.md`** — the boundary is now "a harness whose fixture
  `git` runs through the shared exec helper"; the `Current state` lists all fifteen Go harnesses and
  states no bash git-fixture harness remains (`ci/test-check-docs.sh` stays bash, git-free).
- [ ] **Step 2: `index.md`/`catalog.md`** — `scripts/` no longer has `test-*.sh` harnesses; name the
  Go `test/scripts` home; keep `ci/test-check-docs.sh`.
- [ ] **Step 3: changelog** — record the two additional ports.
- [ ] **Step 4: Run** `make check` (green), `make shellcheck`, `make brain-check`.
- [ ] **Step 5: Commit.**

### Task 30: Rename the hermetic-envelope tool off the `test-` prefix

**Rationale:** `scripts/test-envelope.sh` is a tool (the hermetic-envelope runner behind
`make test-scrubbed`/`test-hermetic`), not a harness; its harness was `scripts/test-envelope_test.sh`,
already migrated to `test/scripts/envelope_test.go`. Rename it so no tool carries a harness-looking
name. Ask the user to confirm the target name; default `scripts/hermetic-envelope.sh`.

**Files:**
- Rename: `scripts/test-envelope.sh` → `scripts/hermetic-envelope.sh`
- Modify: `Makefile` (`test-scrubbed`, `test-hermetic`), `scripts/changed-targets.sh` (the
  `envelope-test` alternative), `test/scripts/envelope_test.go` (`repositoryScript(t, "test-envelope.sh")`),
  `test/scripts/changed_targets_test.go` (the `"modified envelope test"` case path),
  `.operator-shared/specs/testing/test-isolation.md` and `shell-script-harnesses.md` references.

- [ ] **Step 1: `git mv scripts/test-envelope.sh scripts/hermetic-envelope.sh`.**
- [ ] **Step 2: Update every reference** — run
  `grep -rn 'test-envelope\.sh' Makefile scripts test .operator-shared/specs` and repoint each (the
  `.sh` stays bash; only the filename changes).
- [ ] **Step 3: Run** `CGO_ENABLED=1 go test -tags microsandbox_ffi_path -count=1 ./test/scripts/ -run 'Envelope|ChangedTargets'`
  and `make shellcheck` — PASS.
- [ ] **Step 4: Commit.**

### Task 31: Fix the surface-sweep relative-root case for macOS

**Rationale (Greptile P2):** the harness stages its root directly under `/tmp` because the sandbox
socket path must fit `sun_path` (104). The relative-root subtest composes its name from Go's
`os.MkdirTemp` suffix (9–10 digits); on macOS `/tmp` resolves to `/private/tmp`, so the derived
socket path reaches `105 >= 104` and `scripts/surface-sweep.sh init` exits 1, failing the
`assertStatus(..., 0)`. The deleted bash harness used `asb-rel-$$` (a short PID), which fits.

**Files:**
- Modify: `test/scripts/surface_sweep_test.go:59-78`

- [ ] **Step 1: Confirm the failure mechanism** by arithmetic on this host: the subtest's `name` is
  `"asb-rel-" + <MkdirTemp suffix>`; the script's `check_root_fits_sandbox_socket` uses
  `len(socketPath) + SOCKET_SUFFIX(50) >= 104`. A 9–10 digit suffix plus the resolved base is over
  budget on macOS.
- [ ] **Step 2: Change the name to mirror the bash harness** — replace the suffix derivation with
  `name := "asb-rel-" + strconv.Itoa(os.Getpid())` (add the `strconv` import if absent). Keep the
  `expected`/cleanup logic unchanged.
- [ ] **Step 3: Run** `CGO_ENABLED=1 go test -tags microsandbox_ffi_path -count=1 ./test/scripts/ -run TestSurfaceSweep` — PASS.
- [ ] **Step 4: Commit.**

### Task 32: Port `ci/test-check-docs.sh` to Go (optional, for a single harness home)

**Rationale:** the last bash harness. No isolation rationale (git-free fixtures), so this is a
consistency call: after it, `test/scripts` is the only harness home and no `test-*.sh` harness
remains. The tool `ci/check-docs.sh` stays bash.

**Files:**
- Create: `test/scripts/check_docs_test.go`
- Delete: `ci/test-check-docs.sh`
- Read for cases: `ci/test-check-docs.sh` (600 lines)
- Modify: `Makefile` (`docs-linkcheck-test`), `scripts/changed-targets.sh` (line 122),
  `test/scripts/changed_targets_test.go`

**Interfaces:**
- Consumes: `RunScript`, `assertStatus`/`assertContains`/`assertNotContains`, `repositoryScript`.
- Produces: `TestCheckDocs`; `Makefile`'s `docs-linkcheck-test` runs
  `go test ./test/scripts/ -run CheckDocs`.

- [ ] **Step 1: Write the failing test** — port every case. Build the bash `new_fixture` tree in
  `t.TempDir()` (README with rendered-HTML links, `docs/` pages with `{% link %}` tags, a `.puml`, a
  Go file calling `docslink.SectionURL`, a troubleshooting page); then port each mutation+assertion:
  the pass/fail matrix for each link source, the slug edge cases (backticked/punctuated, duplicate →
  `-1`/`-2`, declared `{#id}`, dash-led, `#` in front matter), explicit `<a id="…">` anchors, fenced
  code blocks, the `_site/`/`vendor/` ignores, and the terminology cases. Run via
  `RunScript(t, root, nil, repositoryScript(t, "check-docs.sh"), root)` (the checker takes ROOT as
  `$1`).
- [ ] **Step 2: Run** `-run CheckDocs` — FAIL (undefined).
- [ ] **Step 3: Implement**; delete `ci/test-check-docs.sh`; repoint `Makefile`'s
  `docs-linkcheck-test`, `scripts/changed-targets.sh`
  (`ci/check-docs.sh|test/scripts/check_docs_test.go` → `docs-linkcheck docs-linkcheck-test`), and add
  the `test/scripts/check_docs_test.go` pinned case to `test/scripts/changed_targets_test.go`.
- [ ] **Step 4: Run** `-run CheckDocs`, `-run ChangedTargets`, and `make check` — PASS.
- [ ] **Step 5: Commit.**

## Self-review notes (Phase 6)

- **Spec coverage:** the isolation chokepoint → Task 26; the two ports → Tasks 27–28; the boundary
  rewrite and gate → Task 29.
- **Type consistency:** every fixture helper is `test/scripts`-local and tool-named; `TestRebaseStatus`
  and `TestCleanupMergedBranches` are the `-run` values.
- **Proportion:** the ports reference the bash harnesses as the case source rather than re-transcribing
  ~40 assertions; the helper is specified by its wrapper list.

