---
description: Contract for the shell scripts' test harnesses - the host-git-config isolation every git fixture harness must apply, the shared `internal/testutil` helpers (`GitIsolationEnvironment`, `RunScript`, the `gittest` fixture builder), the re-exec fake-command pattern, the `test/scripts` Go home for harnesses that need no worktree, and the boundary where a harness stays in bash.
read_if: Writing or fixing a `scripts/test-*.sh` or `ci/test-check-*.sh` harness that creates git fixtures; a harness that prompts for an ssh passphrase or fails with `failed to write commit object` under commit signing; exporting `internal/testutil`'s git-isolation helper or adding `RunScript` or a go-git fixture builder; moving a shell harness into a Go test package; or the ambient-command contract for a test that execs git.
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
two newest harnesses, `scripts/test-backport-audit.sh` and `scripts/test-open-backport-pr.sh`, which fail under
commit signing. Hand-copying cannot fail closed: nothing makes a new harness include the head.

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

`internal/testutil/testgit.go` owns git-fixture isolation for Go tests: `gitIsolationEnvironment` (over
`isAmbientGitSetting`) neutralizes the set above, and `RunGit`, `ConfigureRepo`, and `InitRepo` build and drive
fixtures. A Go harness reuses these instead of re-deriving the environment. Three things complete the pattern:

- `GitIsolationEnvironment(environ []string) []string` — the exported form of `gitIsolationEnvironment`, so a
  caller outside `testutil` can build a child process environment. `isAmbientGitSetting` stays unexported: the
  exported function is the only entry point, so a caller cannot filter by hand and miss a channel.
- `RunScript(tb testing.TB, dir string, extraEnv map[string]string, script string, args ...string) ScriptResult`
  — runs `bash <script> <args...>` in `dir` with an environment rooted in
  `GitIsolationEnvironment(os.Environ())` and `extraEnv` overlaid, returning captured stdout, stderr, and exit
  status. Isolation is internal, so a harness that drives a script cannot forget it.
- A fixture builder in `internal/testutil/gittest`, a leaf package so a test-support package can import it
  without a cycle. It returns a repo handle with commit, branch, checkout, a two-parent merge commit, a bare
  origin, and push, all through go-git — `InitRepo` is too minimal (one commit, a path, no handle), and a
  go-git builder never execs the ambient `git`. A fixture that must reproduce a patch-id match does so by
  committing the same diff, not by cherry-picking.

### Where harnesses live

The standalone tools (`scripts/*.sh`, `ci/*.sh`) stay bash: a Makefile target, a CI workflow, or a human runs one
file from a checkout with no build step.

A harness moves to Go — the test-only package `test/scripts`, beside `test/harness`, whose `TestMain` installs
the fail-fast mocks — when it drives a script and can build its fixtures with go-git. The porcelain-shaped
harnesses (`open-backport-pr`, `backport-audit`, `upstream-drift`, `rebase-status`, `enqueue-pr`) fit. A harness
whose fixtures need `git worktree` or `git clone`, which go-git does not implement (`cleanup-merged-branches`),
stays in bash.

### Fake commands

A harness fakes an external CLI the script calls (`gh`, `docker`, `msb`) without a shell stub: it puts a symlink
to the test binary on `PATH`, and `TestMain` serves the canned response (argv-logged) when a marker environment
variable is set. This removes the fake-`gh` shell script the bash harnesses each carry.

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

Several harnesses have landed. `scripts/test-open-backport-pr.sh`,
`scripts/test-backport-audit.sh`, `scripts/test-changed-files.sh`, and
`scripts/test-changed-targets.sh` are gone; their cases live in the Go `test/scripts` package,
driving `scripts/open-backport-pr.sh`, `scripts/backport-audit.sh`, `scripts/changed-files.sh`, and
`scripts/changed-targets.sh` through `testutil.RunScript` with `gittest` go-git fixtures and a re-exec
fake `gh`. The helpers exist — `GitIsolationEnvironment`, `RunScript`, `internal/testutil/gittest`,
and `internal/testutil/fakecmd` — and each migrated Make target runs
`go test ./test/scripts/ -run <Tool>`.

The remaining bash fixture harnesses still hand-copy the isolation head; a new one must include it
until it migrates.
