# Contributing to agents-sandbox

Thanks for considering a contribution. This guide explains how to build, test, and submit changes.

## Project overview

The layout is a standard Go module:

- `cmd/agents-sandbox` — the CLI entry point
- `internal/` — packages for sandbox lifecycle, VM control, image building, pruning, config, and more
- `docs/` — user documentation

## Development environment

- **Go** — version pinned in `.go-version`, managed via goenv
- **golangci-lint** — used by `make lint` and `make fmt`
- **Docker** — for building the runner image (`agents-sandbox build`)
- **zig** — only needed for cross-compiling release binaries (`make build-release`)

Run `make bootstrap` once to install the pinned Go, golangci-lint, and Zig. `gotestsum` is fetched on demand via
`go run` for `make coverage-junit`, so no separate install is needed.

### Bootstrap

`make bootstrap` requires [goenv](https://github.com/go-nv/goenv) (install via
[Homebrew](https://github.com/go-nv/goenv#installing) or [manually](https://github.com/go-nv/goenv/blob/master/INSTALL.md))
and installs:

- **Go**, at the version pinned in [`.go-version`](.go-version) — via goenv.
- **golangci-lint**, at the version pinned in the [`Makefile`](Makefile) — via goenv tools.
- **zig**, at the version pinned in the [`Makefile`](Makefile) — for cross-compiling release binaries
  (`make build-release-all`).

`make bootstrap` is idempotent: it skips a tool that is already installed at the pinned version. The `Makefile` is the
source of truth for the golangci-lint, Zig, and gotestsum versions; the Go version lives in `.go-version`. The
`.agents-sandbox/Dockerfile` carries its own matching versions for the runtime runner image, which is built separately.

#### Zig and `darwin` cross-compilation

Zig is installed under `~/.local` (`~/.local/zig-<arch>-<os>-<version>`) and `make bootstrap` symlinks it into
`~/.local/bin`, so no `sudo` is needed. It also keeps a stable `~/.local/zig` install-root symlink, and copies the macOS
linker stubs (`.github/stubs/*.tbd`) into that zig's `lib/libc/darwin`. zig ships the macOS headers but not these
framework/library stubs, so without them `make build-release` for `darwin/arm64` fails to link `CoreFoundation`,
`Security`, and `resolv`.

`~/.local/bin` is on `PATH` by default on most Linux distributions but **not** on macOS. If `zig` is not found after
bootstrapping, `make bootstrap` says so; the fix is to add `~/.local/bin` to your `PATH` and open a new shell.

`make bootstrap` only manages that zig. If `PATH` resolves `zig` to an installation elsewhere — a Homebrew or system
zig — bootstrap leaves it untouched and warns: once that it resolves outside the managed install, and again if it cannot
find the macOS cross-compile stubs under that zig. To let the managed install win instead, put `~/.local/bin` ahead of
the other zig on `PATH`. Bootstrap decides what to install from the managed zig on disk, so a managed install that is
not on your `PATH` is not re-downloaded.

## Common commands

Run these from the module root:

| Command              | Purpose                                                    |
|----------------------|------------------------------------------------------------|
| `make test`          | Run the test suite                                         |
| `make test-race`     | Run the test suite under the race detector                 |
| `make fuzz`          | Fuzz the parsers for `FUZZTIME` per target (default `30s`)  |
| `make vulncheck`     | Scan for reachable vulnerabilities (`govulncheck`)         |
| `make compile-all`   | Compile all code incl. `integration`-tagged test files      |
| `make lint`          | Run the linter                                             |
| `make fmt`           | Format all source files                                    |
| `make check`         | Format, lint, and test — run before finalizing any change  |
| `make verify`        | Read-only CI-parity check: format-check, lint, test, race, vulncheck |
| `make build`         | Build the `agents-sandbox` binary                        |
| `make coverage`      | Run tests and print the coverage total                     |
| `make coverage-junit`| Run tests, write coverage + JUnit XML (`junit.xml`)        |
| `go mod tidy`        | Sync `go.mod` / `go.sum` after changing imports            |

## Writing code

- Prefer self-explanatory code to comments.
- Keep abstractions minimal. Prefer small, focused files with one clear responsibility.
- Follow Go conventions: idiomatic error handling, no globals where avoidable.

### Formatting

Use `make fmt` (which runs `golangci-lint fmt`). Do not hand-format or use plain `go fmt`.

### Linting

Use `make lint` (`golangci-lint run ./...`), which includes `vet`.

## Testing

This project defaults to **test-driven development**:

1. Write a failing test first.
2. Confirm it compiles and fails for the expected reason.
3. Implement the change.
4. Confirm the test passes.

- Every new or changed CLI command or flag should be covered in `cmd/agents-sandbox/cli_*_test.go`.
- Add focused unit tests for internal functionality with each implementation.
- Do manual tests and run the full suite with `make check` before submitting.
- `make verify` is the CI-parity check and additionally runs `make test-race` (race detector) and `make vulncheck`
  (`govulncheck` against the Go vulnerability database; needs network on first run). CI runs each as its own job, so run
  `make verify` when a change touches concurrency or dependencies.
- `make deadcode` fails when production code cannot be reached from any `main` package — code only tests call. It
  analyses the default, `integration`, `msb`, and `integration msb` configurations and unions the findings, so a function
  only a tagged build reaches still fails. When unreachable code is deliberate (a test seam, an internal extension
  point), record the reason where it lives with `//deadcode:ignore reason="..."`: in the declaration's doc comment (or as
  a trailing comment on it), or in the comment block above its `package` clause to excuse a whole file. The reason is
  required, and a marker that excuses nothing fails the gate, so markers cannot rot. `PKG=./cmd/...` narrows the analysis
  for inspection and skips the stale-marker pass; the analysis needs cgo. It is a required merge check, but deliberately
  not a release prerequisite, and `make check` runs only the gate's own script test (`make deadcode-test`).
- `make fuzz` drives the fuzz targets for a bounded `FUZZTIME` per target (`FUZZPKG` narrows it to one package,
  `FUZZTARGET` to one target). The seed corpus
  and any committed crasher run under `make test`, so a crasher is a regression test first; the `fuzz` workflow explores
  the targets nightly and on merges that touch them, and nothing is gated on it, so a red run reports a crasher instead of
  blocking a merge. A crasher it finds comes back as a build artifact to commit as a seed.
- `make mutation` runs [gomutants](https://github.com/gomutants/gomutants) on demand: it mutates the code and reports the
  mutants the tests do not notice. A surviving ("LIVED") mutant is either a missing assertion or a mutation that cannot
  change behaviour. By default it covers the whole module but only lines changed since the last release tag
  (`PKG=./internal/foo` narrows it to one package; `FULL=1` runs every line). It is **never** a gate — not in
  `make check`, `make verify`, or as a required CI check — and a package takes minutes. Close every survivor: strengthen
  the test so the mutant now fails, or record the reason in the source next to the mutant with `// gomutants:disable
  <TYPE> reason="..."`. The `mutation` workflow is an advisory signal, not a check: it sweeps every PR that
  touches code automatically (docs-only PRs are filtered out; the sweep covers just that PR's changeset — lines changed
  since the merge base with its base branch), posting each survivor as an inline `::warning::` on the diff (GitHub shows
  at most ten), a sticky PR comment with the totals and the complete survivor list, and a `mutation-report` artifact.
  Because gomutants has no threshold, the job stays green on survivors; a red run means the sweep failed, and nothing is
  ever gated on it.
- The `integration`-tagged docker-build suite is excluded from `make test` and from PR CI by default (~10 minutes). Run
  it locally with `make test-integration`, or ask a maintainer to add the `run-integration` label to a PR to run it
  there; it always runs on `main` (when the merge changed code) and on releases. The same suite also runs against rootless podman under the
  `run-integration-podman` label, which keeps podman an engine this project tests rather than only documents.

## Documentation

Keep user documentation current with any behavior change:

- `README.md` for anything visible at a glance (quick start, installation, badges).
- `docs/` for commands, configuration, sandbox lifecycle, and troubleshooting.

User-facing docs follow [`STYLEGUIDE.md`](STYLEGUIDE.md).

If you change a CLI command or flag, update the relevant docs in the same PR.

## Submitting a PR

1. Open an issue first if the change is non-trivial, so the approach can be discussed.
2. Create a focused branch with a descriptive name.
3. Make your changes, following the testing and formatting rules above.
4. Add a `changelog.d/` fragment for any change that touches code (see [Changelog & versioning](#changelog--versioning)).
5. Run `make check` and make sure it passes.
6. Open a pull request and fill out the template.
7. Address review feedback; keep the commit history clean.

### Merging

`main` merges through a **merge queue**. Once a PR is approved and its required checks (`lint`, `test`, `race`,
`deadcode`, `changelog`) pass, add it to the queue (a maintainer can, and anyone with write access can in the PR's merge box). The
queue builds your branch on top of the current `main` **plus the PRs ahead of you**, re-runs the required checks on that
combined tree, and merges it only if they pass. This is what stops two independently-green PRs from breaking `main`
together, without anyone hand-rebasing before merging.

If a required check fails on that combined build, the queue drops your PR and leaves the reason on the timeline; fix it
and re-add the PR. `race`, `deadcode`, and `changelog` are required and always run — a gated required check would report success
without running — so a docs-only change runs them too. The `integration` suites are not in the queue: they still run on
`main` after the merge (and on releases).

## Changelog & versioning

- The project uses [Semantic Versioning](https://semver.org/spec/v2.0.0.html). Release tags are `vMAJOR.MINOR.PATCH`
  (e.g. `v0.1.0`); releases and the `version` command use the bare version (e.g. `0.1.0`).
- Add a changelog **fragment**, don't edit `CHANGELOG.md`. For any user-visible change, create
  `changelog.d/<slug>.<type>.md`: `<slug>` is the issue number (or the PR number when there is no issue) and `<type>`
  one of `added`, `changed`, `deprecated`, `docs`, `fixed`. The file holds one bullet's text, without the leading
  `- `, wrapped like the rest of the changelog. A change spanning two sections uses two files. The required
  `changelog` check enforces a fragment for code changes; a docs-only change needs none. `CHANGELOG.md` is assembled
  from the fragments at release time, so no two pull requests touch it. See `changelog.d/README.md`.

## Security

If you find a security vulnerability, do not open a public issue. Report it privately per `SECURITY.md` (or open a
private/confidential issue) so it can be addressed before disclosure.