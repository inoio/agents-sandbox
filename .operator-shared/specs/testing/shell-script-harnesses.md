---
description: Contract for the shell scripts' test harnesses - the host-git-config isolation every git fixture harness must apply, the shared helpers (`GitIsolationEnvironment` in `internal/testutil`; `RunScript`, the `gittest` fixture builder, and `fakecmd` as `*_helpers_test.go` in `test/scripts`), the re-exec fake-command pattern, the `test/scripts` Go home for harnesses that need no worktree, and the boundary where a harness stays in bash.
read_if: Writing or fixing the two remaining bash git-fixture harnesses `scripts/test-rebase-status.sh` and `scripts/test-cleanup-merged-branches.sh`; a harness that prompts for an ssh passphrase or fails with `failed to write commit object` under commit signing; exporting `internal/testutil`'s git-isolation helper, or adding or re-homing a shared `test/scripts` helper (`RunScript`, the go-git `gittest` builder, `fakecmd`); moving a shell harness into a Go test package; or the ambient-command contract for a test that execs git.
---

# Shell script test harnesses

## Problem

The repo's non-Go tooling is shell: the maintenance tools under `scripts/` and the CI gates under `ci/`, each
with a `test-*.sh` harness. A harness that exercises a git-dependent tool builds throwaway repositories with the
real `git` binary, and those fixtures inherit the developer's global git configuration. Commit signing —
`commit.gpgsign` with an `ssh` signing key — then makes every fixture commit prompt for the key's passphrase
through the askpass program; declined or with no tty, the commit fails (`fatal: failed to write commit object`)
and the harness aborts under `set -euo pipefail`. The same inheritance reaches a credential helper or an askpass
program.

The isolation that prevents this is a head written out by hand in each fixture harness. It was omitted from the
two newest harnesses, which then failed under commit signing; those two are now migrated (see Current state).
Hand-copying cannot fail closed: nothing makes a new bash harness include the head.

## Contract

### Fixture isolation (bash harnesses)

Every shell harness that creates git fixtures neutralizes the host git and ssh configuration before it runs any
`git` command, once per harness: clear `GIT_CONFIG_GLOBAL`, `GIT_CONFIG_SYSTEM`, `GIT_CONFIG_PARAMETERS`, the
`GIT_CONFIG_COUNT`/`GIT_CONFIG_KEY_n`/`GIT_CONFIG_VALUE_n` channels, and the askpass programs (`GIT_ASKPASS`,
`SSH_ASKPASS`, `SSH_ASKPASS_REQUIRE`); then pin `GIT_CONFIG_NOSYSTEM=1`, `GIT_CONFIG_GLOBAL=/dev/null`,
`GIT_TERMINAL_PROMPT=0`. The per-invocation channels are cleared too — they stay active even with the config
files disabled, so a poisoned `commit.gpgsign` would still sign. A fixture pins any git setting it depends on
itself, rather than reading the host's.

### The shared Go helpers

`internal/testutil/testgit.go` owns git-fixture isolation for Go tests: `GitIsolationEnvironment` (over
`isAmbientGitSetting`) neutralizes the set above, and `RunGit`, `ConfigureRepo`, and `InitRepo` build and drive
fixtures. A Go harness reuses these instead of re-deriving the environment. The rest of the pattern lives in
`test/scripts` — its only consumer — as test-only files:

- `GitIsolationEnvironment(environ []string) []string` — the exported form of the isolation filter, so a caller
  outside `testutil` can build a child process environment. `isAmbientGitSetting` stays unexported: the exported
  function is the only entry point, so a caller cannot filter by hand and miss a channel.
- `RunScript(tb testing.TB, dir string, extraEnv map[string]string, script string, args ...string) ScriptResult`
  (`runscript_helpers_test.go`) — runs `bash <script> <args...>` in `dir` with an environment rooted in
  `GitIsolationEnvironment(os.Environ())` and `extraEnv` overlaid, returning captured stdout, stderr, and exit
  status. Isolation is internal, so a harness that drives a script cannot forget it. It also reads the script
  file itself, so `cmd/go` records it as a test-cache input: the child `bash` reads the script, which the test
  process cannot see, and without this a changed script would replay a stale cached test result. Finally it
  disables the race runtime's exit delay for the child (`GORACE=atexit_sleep_ms=0`, preserving any `GORACE`
  options the environment already sets), because a fake command re-execs the race-instrumented test binary and
  every clean exit of one would otherwise sleep a second (see Current state).
- The fixture builder (`gittest_helpers_test.go`) returns a repo handle with commit, branch, checkout, a
  two-parent merge commit, a bare origin, and push, all through go-git — `InitRepo` is too minimal (one commit,
  a path, no handle), and a go-git builder never execs the ambient `git`. A fixture that must reproduce a
  patch-id match does so by committing the same diff, not by cherry-picking.
- The re-exec fake-command helper (`fakecmd_helpers_test.go`, see Fake commands).

These are `*_helpers_test.go` files, not importable packages: a `_test.go` file is compiled only into its own
package's test binary, so coverage never counts it and `deadcode` skips it, and no `.codecov.yml` `ignore` entry
or `//deadcode:ignore` marker is needed. A test-support helper a *different* package's tests need is re-homed
into that consumer's `*_helpers_test.go` the same way, rather than kept importable and excluded from coverage
(the earlier `internal/testutil/gittest`, `fakecmd`, and `runscript.go` were exactly that, with a file-level
`ignore` broader than its "transitively exercised" reason — each also had direct tests the profile credited).

### Where harnesses live

The standalone tools (`scripts/*.sh`, `ci/*.sh`) stay bash: a Makefile target, a CI workflow, or a human runs one
file from a checkout with no build step.

A harness moves to Go — the test-only package `test/scripts`, beside `test/harness`, whose `TestMain` installs
the fail-fast mocks — when it drives a script and can build its fixtures with go-git. The porcelain-shaped
harnesses (`open-backport-pr`, `backport-audit`, `upstream-drift`, `enqueue-pr`) fit. A harness whose fixtures
need a real-git porcelain go-git does not implement stays in bash: `rebase-status` (an in-progress rebase) and
`cleanup-merged-branches` (`git worktree`, `clone`, `push --force-with-lease`).

### Fake commands

A harness fakes an external CLI the script calls (`gh`, `docker`, `msb`) without a shell stub: it puts a symlink
to the test binary on `PATH`, and `TestMain` registers the canned response. When the test binary is invoked under
the fake's name, `Dispatch` — keyed on `filepath.Base(os.Args[0])` — serves it, argv-logged. This removes
the fake-`gh` shell script the bash harnesses each carry. A served fake exits through `syscall.Exit`, not
`os.Exit`: `test/scripts` has only `_test.go` files, so under `go test -cover` (the codecov and mutation runs) the
coverage runtime's exit hook would print `program not built with -cover` onto the fake's stderr, which the
caller reads as the command's output. The raw exit skips the hook.

### Ambient-command contract

`TestUntaggedTestsExecuteNoAmbientTool` and `TestUntaggedTestsReachNoUnsanctionedEnvironment`
(`internal/testutil`) apply to a Go harness like any default-suite test: execing `bash` is allowed; execing an
ambient tool (`docker`, `gh`, `git`, `gpg`, `msb`, `ssh`) or calling `testutil.RunGit`/`ConfigureRepo` is not
unless the statement carries a reason-bearing `//testenv:allow <reason>` marker. The fixture builder is go-git,
so a porcelain harness normally needs no marker.

See [test isolation](test-isolation.md) for the Go-side seam contract this sits beside.

## Non-goals

- Rewriting the standalone tools in Go. They are bash so that a workflow or a human can run one file with no
  build.
- Adopting a bash test framework. The shared Go harness is the answer to the duplicated boilerplate.

## Current state

The migration is complete. Fifteen harnesses live in the Go `test/scripts` package —
`open-backport-pr`, `backport-audit`, `changed-files`, `changed-targets`, `upstream-drift`,
`enqueue-pr`, `event-base`, `brain-check`, `check`, `changelog`, `ci-guards`,
`classify-changes`, `deadcode`, `surface-sweep`, and `envelope` — each driving its standalone
`scripts/*.sh` or `ci/*.sh` tool through `RunScript` with go-git `gittest` fixtures and re-exec
fakes. The helpers are `GitIsolationEnvironment` in `internal/testutil`, and `RunScript`,
`gittest`, and `fakecmd` as `*_helpers_test.go` in `test/scripts`; each migrated Make target runs
`go test ./test/scripts/ -run <Tool>`, and `scripts/changed-targets.sh` selects it from both the
tool path and the `test/scripts/<tool>_test.go` path.

Two harnesses stay bash, both because their porcelain is not implementable on go-git:
`scripts/test-rebase-status.sh` (an in-progress rebase) and
`scripts/test-cleanup-merged-branches.sh` (`git worktree`, `clone`, `push --force-with-lease`).
Each still carries the isolation head by hand, and a new bash harness that creates git fixtures
must include it. `ci/test-check-docs.sh` was never in scope — it is fixture-only, creates no git
fixtures, and stays bash.

A fake command re-execs the race-instrumented test binary, and a race-instrumented process sleeps
`atexit_sleep_ms` (1000 ms by default) on a clean exit — to give other threads time to report a
race. Since a successful fake invocation exits 0, every one of them slept a second under `-race`:
`backport-audit` alone re-execs `gh` ~85 times, so the migrated harnesses added roughly two minutes to
the required `test-race` job. `RunScript` now disables that exit delay for the child
(`GORACE=atexit_sleep_ms=0`, merged with any `GORACE` the environment already sets); race detection
stays on, only the exit delay goes. The `test/scripts` package's `-race` time fell from ~115 s to
~13 s, close to its plain ~9 s. A `//go:build !race` exclusion was the earlier candidate and was not
taken: it would have removed the re-execs (and the fixture coverage) wholesale rather than the delay.
