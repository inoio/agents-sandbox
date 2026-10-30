---
description: Survey of Go mutation-testing tools (gomutants, gremlins, go-mutesting, ooze, gomu) against the agents-sandbox constraint of an on-demand, per-package `go run module@version` command that is never a CI gate.
read_if: Adding or changing the on-demand mutation-testing Makefile target, or evaluating a Go mutation-testing tool.
---

# Mutation testing for Go — tool survey

Constraint recap: an **on-demand, per-package** command, invoked as `go run <module>@<version>` (no global install), version pinned by a `Makefile` constant **exactly like `GOVULNCHECK_VERSION`**, and **never** wired into `check`/`verify` or made a required CI check (the advisory `mutation` workflow runs it on every code-changing PR and gates nothing). Whole-module runs take hours, so single-package scoping is required.

All invocations below were executed in this sandbox with `go version go1.27.1 linux/amd64`; failures and successes are quoted from that output.

## Comparison

| | gomutants | gremlins | go-mutesting (avito) | go-mutesting (zimmski) | ooze | gomu |
|---|---|---|---|---|---|---|
| `go run <root>@<ver>` | ✅ root `main.go` — **works** | ❌ root is a library; must use `/cmd/gremlins` | ❌ `/cmd/go-mutesting` | ❌ `/cmd/go-mutesting` | ❌ library only | ❌ `/cmd/gomu` |
| Latest tag / module path | `v0.7.2`, root pkg | `v0.6.0`, `cmd/gremlins` | `v2.3.1` — **unusable** (no `/v2`), pin pseudo-version | no tags (pseudo `v0.0.0-2021…`) | `v0.2.0` | `v0.3.0`, `cmd/gomu` |
| Per-package scope | package patterns (`./internal/foo`) | one path arg (subtree) | targets (dirs/pkg) | targets (dirs/pkg) | package of the test file | one path arg |
| Go supported (go.mod) | `go 1.26.10` | `go 1.25.0` | `go 1.27` | `go 1.10` | `go 1.25` | `go 1.26.0` |
| Maintenance (as of 2026-10-21) | pushed 2026-10-21, 13★, 3 open | pushed 2026-06-26, 448★, 64 open | pushed 2026-10-02, 279★, 25 open | pushed 2024-07-04, 43 open | pushed 2026-10-13, 290★ | pushed 2026-10-18, 46★ |
| Mutant statuses | KILLED/LIVED/NOT COVERED/NOT VIABLE/TIMED OUT/INFRA ERROR/EQUIVALENT | KILLED/LIVED/NOT COVERED/NOT VIABLE/TIMED OUT/RUNNABLE | pass/fail (+score) | pass/fail (+score) | killed/living | killed/lived/etc |
| Machine-readable | JSON (default) + Stryker + HTML | JSON (`--output`) | JSON/HTML via config | none | none | console/json/html/text |
| Ignore / allowlist | inline `// gomutants:disable*` + `--exclude-calls` | `--exclude-files` regex only | `mutator-disable-*` annotations + MD5 blacklist | MD5 `--blacklist` | `IgnoreSourceFiles` regex | `.gomuignore` |
| Mutation applied by | `go test -overlay` (source untouched) | full module copy to temp dir, rewrite | source rewrite in temp, then test | source rewrite in temp, then test | rewrites source | `go test` overlay |
| Default exit gate | none (thresholds 0) | none (thresholds 0) | none | none | threshold default `1.0` | `--fail-on-gate` default **true** |

## gomutants

- Repo `github.com/gomutants/gomutants`; module root package is `main`. `go install github.com/gomutants/gomutants@<ver>` is the documented install, and the Claude plugin "falls back to `go run github.com/gomutants/gomutants@latest`" ([README](https://github.com/gomutants/gomutants)).
- **`go run github.com/gomutants/gomutants@v0.7.2 --help` works** here on go1.27.1 (downloaded and printed usage). `go.mod` requires only `gopkg.in/yaml.v3`, `go 1.26.10` ([go.mod](https://raw.githubusercontent.com/gomutants/gomutants/main/go.mod)); "The minimum supported version of Go for gomutants is **1.26**" ([README](https://github.com/gomutants/gomutants)).
- Per-package: usage is `gomutants [flags] <package patterns>`, e.g. `gomutants ./internal/mypackage` or `gomutants -v ./internal/foo` ([README](https://github.com/gomutants/gomutants)); package patterns are positional.
- Scope to changed lines: `--changed-since <ref>` keeps only mutants on lines added/modified vs `git diff --unified=0 --merge-base <ref>`, includes uncommitted edits to tracked files, and requires a **new** file to be `git add`ed to be visible. On a shallow clone (no reachable merge base) it fails with a hint rather than silently widening. **The flag is `--changed-since` in the pinned v0.7.2** — verified by running `go run github.com/gomutants/gomutants@v0.7.2 --changed-since v0.5.0 --dry-run ./internal/humanize` (works) and `-n v0.5.0` (`flag provided but not defined: -n`, exit 2). An earlier verbal summary said `-n`; it is wrong for this pin ([README](https://github.com/gomutants/gomutants), [flags.go](https://github.com/gomutants/gomutants/blob/main/flags.go)).
- Output/triage: JSON report by default (`--output`/`-o`, default `mutation-report.json`), plus `--stryker-output`, `--html-output`, `--annotations github`; statuses `KILLED`, `LIVED`, `NOT COVERED`, `NOT VIABLE`, `TIMED OUT`, `INFRA ERROR`, `EQUIVALENT`; `--list-mutators`. Equivalent/uninteresting mutants are recorded via inline `// gomutants:disable`, `disable-next-line`, `disable-func`, `disable-regexp` and call-site `--exclude-calls` globs ([README](https://github.com/gomutants/gomutants)).
- GitHub annotations: `--annotations github` (the only accepted value) writes one `::warning file=<path>,line=<n>,col=<n>::Mutant LIVED — <TYPE> (<original> → <replacement>)` per LIVED mutant to stdout, with `%`/CR/LF escaped in the message and `%`/`:`/`,` in the path. It does **not** gate — a non-zero exit needs an explicit `--threshold-efficacy`/`--threshold-mcover` (both default `0`) — so annotations surface survivors without failing the job. GitHub caps the annotation channel at **10 warnings per step** (50 per job), so a larger survivor set needs an uncapped surface (a PR comment, the step summary, or the report artifact).
- Applied by `go test -overlay` — "the source tree is never rewritten" ([README](https://github.com/gomutants/gomutants)). Speed features: content-addressed cache, per-test coverage routing, adaptive per-mutant timeouts. The README's own benchmarks put it ~1.5–1.8× faster than gremlins on medium single-package targets (self-reported; treat as a claim).
- `-race`: forwarded through `--test-flags` (build flags such as `-race` go to the compile). `-overlay`, `-c` and `-args` are gomutants-managed and not forwarded; `-o`/`--output` is gomutants' own JSON report path (default `mutation-report.json`), not a pass-through to the inner `go test` ([README](https://github.com/gomutants/gomutants), `--help`).
- Not a gate by default: `--threshold-efficacy`/`--threshold-mcover` default `0` (disabled), so exit is `0`; exit `10`/`11` only when thresholds are set ([README](https://github.com/gomutants/gomutants)).
- Caveat: the project is new (created 2026-04-04, 13 stars, 3 open issues, PRs `collaborators_only`) ([repo API](https://api.github.com/repos/gomutants/gomutants)). Single-organisation bus factor; version-pin deliberately.

## gremlins

- Repo `github.com/go-gremlins/gremlins`; **root is not a main package**. `go run github.com/go-gremlins/gremlins@v0.6.0 --help` fails with `module github.com/go-gremlins/gremlins@v0.6.0 found, but does not contain package github.com/go-gremlins/gremlins`; the binary is `cmd/gremlins`, and **`go run github.com/go-gremlins/gremlins/cmd/gremlins@v0.6.0 --help` works**. Documented install is `go install github.com/go-gremlins/gremlins/cmd/gremlins@v0.6.0` ([docs](https://gremlins.dev/latest/install/)); layout in [cmd/](https://github.com/go-gremlins/gremlins/tree/main/cmd).
- `go.mod` is `go 1.25.0` ([go.mod](https://raw.githubusercontent.com/go-gremlins/gremlins/main/go.mod)). gomutants' README claims "gremlins is broken on Go 1.26.x" (competitor claim, unverified here; building gremlins itself succeeded on go1.27.1).
- Per-package: `gremlins unleash [path]` accepts at most one path; that path sets `CallingDir`, the coverage run becomes `./<CallingDir>/...`, and mutant paths are made relative to it ([cmd/unleash.go](https://raw.githubusercontent.com/go-gremlins/gremlins/main/cmd/unleash.go), [coverage.go](https://raw.githubusercontent.com/go-gremlins/gremlins/main/internal/coverage/coverage.go)). So `gremlins unleash ./internal/foo` scopes to that subtree.
- Output/triage: statuses `RUNNABLE`/`NOT COVERED`/`KILLED`/`LIVED`/`TIMED OUT`/`NOT VIABLE`; machine-readable via `--output=output.json` (not pretty-printed); `--output-statuses` filters the human stream; YAML config; `--exclude-files` regex. No per-mutant inline ignore ([unleash docs](https://gremlins.dev/latest/usage/commands/unleash/), [configuration](https://gremlins.dev/latest/usage/configuration/)).
- Applied by copying the **entire module root** into a temp workdir, mutating the copy, and running `go test` there ([workdir.go](https://raw.githubusercontent.com/go-gremlins/gremlins/main/internal/engine/workdir/workdir.go)) — a per-package run pays the whole-module copy.
- Its `go test` invocation is `go test -cover -coverprofile …` with optional `-tags`/`-coverpkg` only; there is no way to pass `-race` ([coverage.go](https://raw.githubusercontent.com/go-gremlins/gremlins/main/internal/coverage/coverage.go)).
- Thresholds default `0`; gremlins never exits non-zero unless a threshold is set ([unleash docs](https://gremlins.dev/latest/usage/commands/unleash/)). 448★, Apache-2.0, docs site, official GitHub Action.

## go-mutesting — avito-tech fork

- Repo `github.com/avito-tech/go-mutesting`; main package at `cmd/go-mutesting`. **Tag pinning is broken**: `go run github.com/avito-tech/go-mutesting/cmd/go-mutesting@v2.3.1` fails — `invalid version: module contains a go.mod file, so module path must match major version ("github.com/avito-tech/go-mutesting/v2")` — because the module path has no `/v2` while tags are `v2.x`. `@latest` resolves to the pseudo-version `v0.0.0-20261002081621-9ce5f731e321` and works ([tags](https://api.github.com/repos/avito-tech/go-mutesting/tags), [go.mod](https://raw.githubusercontent.com/avito-tech/go-mutesting/master/go.mod)). A `Makefile` constant would have to pin a commit/pseudo-version, not a release tag.
- `go.mod` is `go 1.27`; last push 2026-10-02; 279★; MIT.
- Per-package: positional targets — files, directories, packages, `...` patterns, e.g. `go-mutesting ./internal/foo` or `go-mutesting .` ([README](https://github.com/avito-tech/go-mutesting)).
- Output: per-mutation `PASS`/`FAIL` plus a mutation score; YAML config with `json_output`/`html_output`; inline `mutator-disable-func` / `mutator-disable-next-line` / `mutator-disable-regexp` annotations; MD5 `--blacklist` for equivalent mutants ([README](https://github.com/avito-tech/go-mutesting)).
- Applied by AST mutators with the built-in exec replacing the original file with the mutation and running the package's tests (source rewrite); an external `--exec` command can be supplied ([README](https://github.com/avito-tech/go-mutesting)).

## go-mutesting — zimmski original

- Repo `github.com/zimmski/go-mutesting`; main at `cmd/go-mutesting`; no release tags — `@latest` = `v0.0.0-20210610104036-6d9217011a00`; `go run github.com/zimmski/go-mutesting/cmd/go-mutesting@latest --help` works here. `go.mod` is `go 1.10` with 2019-era deps (`golang.org/x/tools` from 2019), last push 2024-07-04 ([go.mod](https://raw.githubusercontent.com/zimmski/go-mutesting/master/go.mod), [repo API](https://api.github.com/repos/zimmski/go-mutesting)). Expect trouble parsing modern Go (generics) — unverified, but the pinned `x/tools` predates them. Output is `PASS`/`FAIL` + score with an MD5 `--blacklist`; no JSON ([README](https://github.com/zimmski/go-mutesting)). 678★.

## ooze

- Repo `github.com/gtramontina/ooze`; **library only** — confirmed: `go run github.com/gtramontina/ooze@v0.2.0` → `package github.com/gtramontina/ooze is not a main package`. Usage is to add a `mutation_test.go` and run `go test -v -tags=mutation`; Ooze becomes a dependency in the project's `go.mod` ([readme](https://raw.githubusercontent.com/gtramontina/ooze/main/readme.md)). This violates the no-global-install-but-`go run` constraint.
- `WithTestCommand` defaults to `go test -count=1 ./...` and is user-overridable (so `-race` is possible in principle); `WithMinimumThreshold` defaults to `1.0` (a gate unless overridden); `Parallel()`, `WithViruses`, `IgnoreSourceFiles` regex ([readme](https://raw.githubusercontent.com/gtramontina/ooze/main/readme.md)).
- No JSON report; prints a report and a diff per living mutant. Scoping is by where you place the test / which package you run. 290★, MIT, supports Go 1.25 through current.

## gomu

- Repo `github.com/sivchari/gomu`; main at `cmd/gomu`; **`go run github.com/sivchari/gomu/cmd/gomu@v0.3.0 version` works** (printed `gomu version dev`). `go.mod` is `go 1.26.0`; created 2025-07-14, pushed 2026-10-18; 46★, MIT ([go.mod](https://raw.githubusercontent.com/sivchari/gomu/main/go.mod)).
- Per-package: `gomu run ./internal/foo`; flags after `--` are forwarded to `go test` (`-short`, `-run`, `-tags`, `-race`) ([README](https://github.com/sivchari/gomu)).
- Output: `--output console|json|html|text`; `.gomuignore`; incremental history. Mutations applied via a managed `go test` overlay (`-overlay` rejected as gomu-managed) ([internal/execution/overlay.go](https://github.com/sivchari/gomu/tree/main/internal/execution)).
- Gate risk: `--fail-on-gate` defaults to **`true`** with `--threshold 80` — for a never-a-gate target you must pass `--fail-on-gate=false` (and/or `--threshold 0`).

## Recommendation

1. **gomutants** — best fit. It is the only candidate whose module root is the binary, so `go run github.com/gomutants/gomutants@<ver>` is clean and was verified here; it takes package patterns for exact per-package scoping; it applies mutations by overlay so the working tree is never corrupted; JSON is the default output; inline disable directives and `--exclude-calls` give a real allowlist for equivalent/uninteresting mutants; and thresholds default off, so it is inert unless explicitly gated. Accept the maturity caveat by pinning the version constant.
2. **gremlins** — the conservative, mature alternative (448★, docs, Action). Costs: `/cmd/gremlins` in the `go run` path, a full-module temp copy per run, no overlay, and a competitor's unverified Go 1.26+ compatibility claim.
3. **gomu** — overlay-based like gomutants, `/cmd/gomu` path; must disable its default gate.
4. **go-mutesting (avito)** — viable only with a pseudo-version pin (v2 tags are not Go-module-legal) and no overlay.
5. **go-mutesting (zimmski)**, **ooze** — reject: stale/`go 1.10`, and library-only respectively.

### Makefile (mirrors the `vulncheck` target; deliberately excluded from `check`/`verify`, never a required CI check)

The canonical target lives in `.operator-shared/specs/mutation-testing.md`; its shape is:

```make
GOMUTANTS_VERSION = v0.7.2

# On-demand mutation testing. Never part of check/verify/CI.
#   make mutation                        # whole module, lines changed since the last release tag
#   make mutation PKG=./internal/foo     # one package, changed lines
#   make mutation FULL=1                 # whole module, all lines (slow)
PKG   ?= ./...
SINCE ?= $(shell git describe --tags --abbrev=0)
FULL  ?=

mutation:
	@test -n "$(FULL)" || test -n "$(SINCE)" || { \
		echo "make mutation: cannot resolve a release tag; pass SINCE=<ref> or FULL=1"; exit 1; }
	CGO_ENABLED=1 go run github.com/gomutants/gomutants@$(GOMUTANTS_VERSION) \
		--detect-equivalent \
		$(if $(FULL),,--changed-since $(SINCE)) \
		$(PKG)
```

`mutation-report.json` and the incremental cache `.gomutants-cache.json` are written to the working directory by default
(the cache makes reruns cheap); pass `--output /tmp/mutation-<pkg>.json` to keep the tree clean instead. Do not add
`mutation` to `check`/`verify`/`all`.
