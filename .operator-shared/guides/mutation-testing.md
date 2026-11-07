---
description: Mutation testing for Go in this repo - the tool survey and why gomutants was chosen (on-demand `go run module@version`, never a CI gate), how to close gomutants survivors (the recurring mutant types and their kills, the equivalent-mutant traps, the record-with-a-reason fallback), and the multi-turn sweep runbook for the #268 survivor tracker.
read_if: Evaluating or changing a Go mutation-testing tool or the `make mutation` target; triaging LIVED mutants from a sweep (strengthen a test, delete redundant production code, or record a `// gomutants:disable` reason); picking a `SINCE` scope for a branch/PR sweep; or coordinating a #268 survivor-reduction pass.
---

# Mutation testing for Go

Three concerns, one guide: the tool survey, survivor triage, and the multi-turn sweep runbook.


## Mutation testing for Go — tool survey

Constraint recap: an **on-demand, per-package** command, invoked as `go run <module>@<version>` (no global install), version pinned by a `Makefile` constant **exactly like `GOVULNCHECK_VERSION`**, and **never** wired into `check`/`verify` or made a required CI check (the advisory `mutation` workflow runs it on every code-changing PR and gates nothing). Whole-module runs take hours, so single-package scoping is required.

All invocations below were executed in this sandbox with `go version go1.27.1 linux/amd64`; failures and successes are quoted from that output.

### Comparison

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

### gomutants

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

### gremlins

- Repo `github.com/go-gremlins/gremlins`; **root is not a main package**. `go run github.com/go-gremlins/gremlins@v0.6.0 --help` fails with `module github.com/go-gremlins/gremlins@v0.6.0 found, but does not contain package github.com/go-gremlins/gremlins`; the binary is `cmd/gremlins`, and **`go run github.com/go-gremlins/gremlins/cmd/gremlins@v0.6.0 --help` works**. Documented install is `go install github.com/go-gremlins/gremlins/cmd/gremlins@v0.6.0` ([docs](https://gremlins.dev/latest/install/)); layout in [cmd/](https://github.com/go-gremlins/gremlins/tree/main/cmd).
- `go.mod` is `go 1.25.0` ([go.mod](https://raw.githubusercontent.com/go-gremlins/gremlins/main/go.mod)). gomutants' README claims "gremlins is broken on Go 1.26.x" (competitor claim, unverified here; building gremlins itself succeeded on go1.27.1).
- Per-package: `gremlins unleash [path]` accepts at most one path; that path sets `CallingDir`, the coverage run becomes `./<CallingDir>/...`, and mutant paths are made relative to it ([cmd/unleash.go](https://raw.githubusercontent.com/go-gremlins/gremlins/main/cmd/unleash.go), [coverage.go](https://raw.githubusercontent.com/go-gremlins/gremlins/main/internal/coverage/coverage.go)). So `gremlins unleash ./internal/foo` scopes to that subtree.
- Output/triage: statuses `RUNNABLE`/`NOT COVERED`/`KILLED`/`LIVED`/`TIMED OUT`/`NOT VIABLE`; machine-readable via `--output=output.json` (not pretty-printed); `--output-statuses` filters the human stream; YAML config; `--exclude-files` regex. No per-mutant inline ignore ([unleash docs](https://gremlins.dev/latest/usage/commands/unleash/), [configuration](https://gremlins.dev/latest/usage/configuration/)).
- Applied by copying the **entire module root** into a temp workdir, mutating the copy, and running `go test` there ([workdir.go](https://raw.githubusercontent.com/go-gremlins/gremlins/main/internal/engine/workdir/workdir.go)) — a per-package run pays the whole-module copy.
- Its `go test` invocation is `go test -cover -coverprofile …` with optional `-tags`/`-coverpkg` only; there is no way to pass `-race` ([coverage.go](https://raw.githubusercontent.com/go-gremlins/gremlins/main/internal/coverage/coverage.go)).
- Thresholds default `0`; gremlins never exits non-zero unless a threshold is set ([unleash docs](https://gremlins.dev/latest/usage/commands/unleash/)). 448★, Apache-2.0, docs site, official GitHub Action.

### go-mutesting — avito-tech fork

- Repo `github.com/avito-tech/go-mutesting`; main package at `cmd/go-mutesting`. **Tag pinning is broken**: `go run github.com/avito-tech/go-mutesting/cmd/go-mutesting@v2.3.1` fails — `invalid version: module contains a go.mod file, so module path must match major version ("github.com/avito-tech/go-mutesting/v2")` — because the module path has no `/v2` while tags are `v2.x`. `@latest` resolves to the pseudo-version `v0.0.0-20261002081621-9ce5f731e321` and works ([tags](https://api.github.com/repos/avito-tech/go-mutesting/tags), [go.mod](https://raw.githubusercontent.com/avito-tech/go-mutesting/master/go.mod)). A `Makefile` constant would have to pin a commit/pseudo-version, not a release tag.
- `go.mod` is `go 1.27`; last push 2026-10-02; 279★; MIT.
- Per-package: positional targets — files, directories, packages, `...` patterns, e.g. `go-mutesting ./internal/foo` or `go-mutesting .` ([README](https://github.com/avito-tech/go-mutesting)).
- Output: per-mutation `PASS`/`FAIL` plus a mutation score; YAML config with `json_output`/`html_output`; inline `mutator-disable-func` / `mutator-disable-next-line` / `mutator-disable-regexp` annotations; MD5 `--blacklist` for equivalent mutants ([README](https://github.com/avito-tech/go-mutesting)).
- Applied by AST mutators with the built-in exec replacing the original file with the mutation and running the package's tests (source rewrite); an external `--exec` command can be supplied ([README](https://github.com/avito-tech/go-mutesting)).

### go-mutesting — zimmski original

- Repo `github.com/zimmski/go-mutesting`; main at `cmd/go-mutesting`; no release tags — `@latest` = `v0.0.0-20210610104036-6d9217011a00`; `go run github.com/zimmski/go-mutesting/cmd/go-mutesting@latest --help` works here. `go.mod` is `go 1.10` with 2019-era deps (`golang.org/x/tools` from 2019), last push 2024-07-04 ([go.mod](https://raw.githubusercontent.com/zimmski/go-mutesting/master/go.mod), [repo API](https://api.github.com/repos/zimmski/go-mutesting)). Expect trouble parsing modern Go (generics) — unverified, but the pinned `x/tools` predates them. Output is `PASS`/`FAIL` + score with an MD5 `--blacklist`; no JSON ([README](https://github.com/zimmski/go-mutesting)). 678★.

### ooze

- Repo `github.com/gtramontina/ooze`; **library only** — confirmed: `go run github.com/gtramontina/ooze@v0.2.0` → `package github.com/gtramontina/ooze is not a main package`. Usage is to add a `mutation_test.go` and run `go test -v -tags=mutation`; Ooze becomes a dependency in the project's `go.mod` ([readme](https://raw.githubusercontent.com/gtramontina/ooze/main/readme.md)). This violates the no-global-install-but-`go run` constraint.
- `WithTestCommand` defaults to `go test -count=1 ./...` and is user-overridable (so `-race` is possible in principle); `WithMinimumThreshold` defaults to `1.0` (a gate unless overridden); `Parallel()`, `WithViruses`, `IgnoreSourceFiles` regex ([readme](https://raw.githubusercontent.com/gtramontina/ooze/main/readme.md)).
- No JSON report; prints a report and a diff per living mutant. Scoping is by where you place the test / which package you run. 290★, MIT, supports Go 1.25 through current.

### gomu

- Repo `github.com/sivchari/gomu`; main at `cmd/gomu`; **`go run github.com/sivchari/gomu/cmd/gomu@v0.3.0 version` works** (printed `gomu version dev`). `go.mod` is `go 1.26.0`; created 2025-07-14, pushed 2026-10-18; 46★, MIT ([go.mod](https://raw.githubusercontent.com/sivchari/gomu/main/go.mod)).
- Per-package: `gomu run ./internal/foo`; flags after `--` are forwarded to `go test` (`-short`, `-run`, `-tags`, `-race`) ([README](https://github.com/sivchari/gomu)).
- Output: `--output console|json|html|text`; `.gomuignore`; incremental history. Mutations applied via a managed `go test` overlay (`-overlay` rejected as gomu-managed) ([internal/execution/overlay.go](https://github.com/sivchari/gomu/tree/main/internal/execution)).
- Gate risk: `--fail-on-gate` defaults to **`true`** with `--threshold 80` — for a never-a-gate target you must pass `--fail-on-gate=false` (and/or `--threshold 0`).

### Recommendation

1. **gomutants** — best fit. It is the only candidate whose module root is the binary, so `go run github.com/gomutants/gomutants@<ver>` is clean and was verified here; it takes package patterns for exact per-package scoping; it applies mutations by overlay so the working tree is never corrupted; JSON is the default output; inline disable directives and `--exclude-calls` give a real allowlist for equivalent/uninteresting mutants; and thresholds default off, so it is inert unless explicitly gated. Accept the maturity caveat by pinning the version constant.
2. **gremlins** — the conservative, mature alternative (448★, docs, Action). Costs: `/cmd/gremlins` in the `go run` path, a full-module temp copy per run, no overlay, and a competitor's unverified Go 1.26+ compatibility claim.
3. **gomu** — overlay-based like gomutants, `/cmd/gomu` path; must disable its default gate.
4. **go-mutesting (avito)** — viable only with a pseudo-version pin (v2 tags are not Go-module-legal) and no overlay.
5. **go-mutesting (zimmski)**, **ooze** — reject: stale/`go 1.10`, and library-only respectively.

#### Makefile

The canonical `make mutation` target, its `GOMUTANTS_VERSION` pin, and the `ANNOTATIONS`/`CGO_ENABLED` handling live in `specs/ci/mutation-testing.md`.


## Triaging mutation survivors

The rule lives in `specs/ci/mutation-testing.md`: a survivor is killed, deleted, or recorded, and an unclosed survivor is
tracked debt rather than a merge blocker. This is the field guide for the recurring types. It was distilled from the
first full sweep of `internal/agent` (`make mutation FULL=1 PKG=./internal/agent/`): 36 survivors from 322 mutants, all
under-asserted tests, no defects.

### Redundant production code: delete it

A survivor that no input can kill because the code it mutates is implied by other code points at code to delete, in
the same change. It is one of the most valuable sweep results. Prove the redundancy before deleting: trace every
caller and every return of the helpers involved. In `prepare`'s `pendingUpgrade`, the `latest == ""` guard and the
always-true `hasChecker` flag (its only caller returns earlier for a checker-less agent) were redundant, but the
`!checked` guard was not: a root lock taken during the version request returns the fresh entry with `checked=false`,
and only that guard defers the offer. Its survivor was a missing test. An early `return err` whose error the next step
would re-derive is not redundant code either: dropping it would ignore an error, so record it as equivalent.

### Recurring mutant types and their kills

- **`ERRORF_WRAP` (`%w` → `%v`)** is invisible to `err != nil` because the message text is unchanged; only the wrapping
  relationship is lost. Kill it by reaching the preserved cause with `errors.Is`/`errors.AsType`. Causes seen here:
  `*url.Error` (both `http.NewRequestWithContext` on a malformed URL and `client.Do`),
  `*json.SyntaxError` (`json.Unmarshal` and `json.NewDecoder(...).Decode`), `semver.ErrInvalidSemVer` (returned
  unwrapped by `semver.NewVersion`), `context.Canceled` (reached through `client.Do`'s `*url.Error`).
- **When the wrapped cause has no sentinel, assert the wrap itself.** `%w` yields a `*fmt.wrapError` (reachable through
  `errors.Unwrap`), while `%v` yields an unwrappable error with the same message. `errors.Unwrap(err) != nil` kills the
  `ERRORF_WRAP` when the cause is a fresh `errors.New` from a mock (e.g. `doctor`'s docker check, `docker`'s
  default-error mock) and `errors.Is` to a sentinel is unavailable.
- **`BRANCH_IF` (body removal)** hides behind a *later* error on the same path: deleting an `if resp.StatusCode != 200`
  guard still fails, later, while decoding the error body, so `err != nil` stays true. Kill it by asserting the branch's
  own message (`strings.Contains(err.Error(), "unexpected status")`) or a value the branch alone produces.
- **A `return err` branch whose fall-through wraps the same error with `%w` is invisible to `errors.Is`: assert
  identity.** `prepareHomeVolume` deliberately returns the sentinel `runtimemigration.ErrMigrationPending` unwrapped so
  the user sees its guidance, and the fall-through wraps it as `fmt.Errorf("…: %w", err)`; `errors.Is(err, sentinel)`
  therefore accepts both and the branch survives. `err != sentinel` (or the absence of the wrap phrase) is the
  assertion that kills it.
- **`RETURN_ZERO` on a constant that is already the type's zero value compiles to the identical constant — record it.**
  `vmBootConnected` is the first `iota` constant of `vmBoot`, so a `RETURN_ZERO` replacement of a `return …,
  vmBootConnected, …` is literally the same value; place a `// gomutants:disable-next-line RETURN_ZERO reason="…"` on
  the return.
- **`STATEMENT_REMOVE` on `req.Header.Set`** needs the test server to assert what it received
  (`Accept: application/json`, `User-Agent: agents-sandbox`).
- **`STATEMENT_REMOVE` inside a conditional record-build needs an exact-value assertion, not an inequality.** Two
  folded writes (`b.WriteString("|")`, `b.WriteString(fields)`) in `state.SecretsContentHash` survived a test that
  asserted only `scoped != allHeaders`: dropping either write still left the scoped digest differing from the unscoped
  one. Pin a golden digest of the scoped record (and assert two distinct field sets hash differently) so dropping a
  write changes the asserted value.
- **`RETURN_TRUE` / `RETURN_FALSE` / `RETURN_ZERO`** need the *value* asserted alongside the error: the returned `ok`,
  the copied `count` before a later rule failed, the boolean from a version compare. `if _, err := f(); err == nil`
  leaves the value free.
- **`INTEGER_INCREMENT` / `INTEGER_DECREMENT` / `ARITHMETIC_BASE` on a defensive `http.Client{Timeout: …}`** cannot be
  observed against a local server that answers immediately, and the value is not a contract. Record it:
  `// gomutants:disable-next-line INTEGER_INCREMENT,INTEGER_DECREMENT,ARITHMETIC_BASE reason="…"`.

### Traps

- **`EXPRESSION_REMOVE` on `A && B` emits two mutants, one per operand.** If an operand is redundant the mutant is
  equivalent and unfalsifiable: `strings.HasPrefix(p, "!") && p == "!"` with the left operand removed becomes
  `true && p == "!"`, which is the original. A same-line or `disable-next-line` directive is **type-scoped** (it cannot
  name a stable-id), so recording that one also suppresses the *killable* sibling. Delete the redundant operand — the
  mutant ceases to exist — and let the surviving condition be killed normally. On a multi-line `a || b || c || d` chain
  each candidate reports at its operand's own start, so the left operand of every nested pair lands on the first
  operand's line (three candidates there for a four-term chain) while `b`, `c`, and `d` each report on their own line:
  one `disable-next-line` above the `if` therefore suppresses only the left-side family, and a directive on a later
  operand's line matches just that operand. A redundant first operand (a `!exists ||` before a nil-check that a zero
  value already satisfies) is what makes that left family suppression-only, so deleting it exposes the rest as
  killable — and any operand removal that leaves the next one dereferencing a nil pointer is then killed by the test
  that exercises the nil case (it panics, it does not merely fail).
- **An `&&` operand that is not itself redundant can still have an equivalent removal mutant, when the code it guards
  feeds a consumer that discards the mutant's effect.** `runoptions.ResolveDockerSetting` guarded an `append` of
  `{Source: cfg.DockerSource(), Value: cfg.Docker}` with `cfg != nil && cfg.DockerExplicit()`; a config with no explicit
  source appends `viperconfig.DockerSourceNone`, which `viperconfig.EffectiveDockerSource` drops, so `cfg != nil && true`
  resolved identically and one of the two operand mutants survived. Deleting the operand was not available — it would
  have made `viperconfig.Config.DockerExplicit` test-only and tripped the dead-code gate — and a line-scoped directive
  would also suppress the killable `cfg != nil` sibling, so **nest the guards** (`if cfg != nil { if cfg.DockerExplicit()
  { … } }`): the `&&` mutant family ceases to exist, the inner body stays load-bearing, and the predicate stays
  production-reachable.
- **Two operands that are equivalent under a documented invariant leave no killable mutant — keep the one that states
  the intent.** `session.Run`/`Shell` guarded `if mode.SkipsVM() && sb == nil`, but `PrepareSandbox`'s skipVM contract
  already makes `sb == nil` true exactly when the mode skips the VM, so both operand removals and the `&&`→`||`
  inversion were equivalent (three mutants per guard). Keeping only `mode.SkipsVM()` — the dry-run intent — removed the
  whole family; the guard body stays covered by the dry-run-vm tests, and a real-run path is asserted not to take it.
  Note each table entry "`mode.SkipsVM()` → `true`" was an operand removal *of the `&&`*, not a standalone expression
  replacement — once the `&&` is gone no condition mutant is generated at all.
- **A selector argument that every caller ORs across calls is redundant — drop the parameter.** `hasDockerMarker(data,
  want)` had exactly two callers, each `OR`-ing the current and legacy forms (`... dockerMarker`) `|| (... legacyDockerMarker)`,
  so its `ok && marker == want` was equivalent to `marker != ""`: any match satisfied both calls' `OR`. Returning "any
  marker" and dropping `want` deleted four unfalsifiable mutants (the `ok` removal, `&&`→`||`, `marker == want`→`true`, and
  the `==`→`!=` negation) instead of recording them.
- **A later strip or short-circuit can make an earlier one unobservable, so hand-apply before recording.** In
  `internal/git`, `lastPathSegment`'s `://` strip, its `>= 0` bounds, and its `+3` arithmetic are all masked by the
  following `LastIndex("/")` strip: for any URL containing `://` the scheme is discarded either way, so those mutants
  are equivalent and recorded, not killed. Check whether a subsequent operation already throws the mutated value away.
- **A stdlib guarantee can make one operand of a condition redundant; delete it, do not record it.** `strings.CutLast`
  returns an empty `name` whenever the separator is absent, so `if !found || name == "" || strings.Contains(name, "/")`
  is exactly `if name == "" || strings.Contains(name, "/")`. A line-scoped `disable` for the `!found` operand would also
  suppress the killable `name == ""` and `Contains` siblings on the same line, so remove the `!found` operand (taking
  `CutLast`'s third result as `_`) instead.
- **A length guard before an index is redundant when the container is provably non-empty; swap the index for a stdlib
  suffix check to delete the family.** `setting_writer.go`'s `if len(edited) > 0 && edited[len(edited)-1] != '\n'`
  guarded an index on `document.Bytes()` that the appended key always makes non-empty, so the `len(edited) > 0` operand
  was equivalent (its `EXPRESSION_REMOVE`, `CONDITIONALS_BOUNDARY`, and both `INTEGER_*` mutants all survived).
  `if !bytes.HasSuffix(edited, []byte("\n"))` removes the whole family rather than recording it, and also drops the
  index-arithmetic mutants.
- **A kind guard in front of a method that already no-ops on the other kinds is equivalent; delete the guard.**
  `findEntryFold`'s `if mapping.Kind() != oneyaml.MappingNode { return oneyaml.Node{}, oneyaml.Node{}, false }` fell
  through to `for … range mapping.Entries()` and a final `return …, false`, but oneyaml's `Entries()` yields nothing for
  a scalar, sequence, alias, or invalid node, so both the `BRANCH_IF` (body removal) and the `RETURN_TRUE` (flipped
  bool) were unobservable. Deleting the guard removed both; the loop's own `EqualFold` match stays killed by the
  overwrite tests.
- **A `RETURN_ZERO` on a branch-local return the caller ignores in that same branch is equivalent; record it.**
  `resolveExistingPath` returns `(tokens, node, found)`, but the caller reads `node` only when `found` is true and
  `tokens` only when it is false, so the `current` on the not-found return and the `tokens` on the found return are both
  discarded. The two `RETURN_ZERO` mutants are unfalsifiable and were each closed with a `disable-next-line` reason.
- **Splitting a guard to narrow a type-scoped exclusion can mint a new equivalent survivor.** `gitdirFile`'s `if err !=
  nil || info.IsDir()` bundled a killable operand (`err != nil` removal leaves `info` nil, so `info.IsDir()` panics on a
  missing `.git`) with a redundant one (`info.IsDir()`: a directory makes the following `os.ReadFile` fail identically).
  Splitting into two `if`s exposes the killable operand but the added `return "", false` mints an unobservable
  `RETURN_TRUE` — `("", true)` for a directory is invisible because `resolveGitDir` returns before `gitdirFile` for a
  directory and `isLinkedWorktree`'s `worktreeRegistration(root, "")` rejects it. Delete the redundant operand instead
  (drop the stat; let `os.ReadFile` reject the directory), which removes the mutant family rather than recording a new
  one.
- **`cmd.Args = cobra.ArbitraryArgs` is only observable through `Command.Find`, not `ValidateArgs`.** cobra's
  `ValidateArgs` defaults to `ArbitraryArgs` when `Command.Args` is nil, so a `ValidateArgs` assertion passes under the
  removal; `Find` instead falls back to `legacyArgs`, which rejects extra args on a root with subcommands. Call
  `cmd.Find([]string{"<bare-positional>"})` — a `--flag value` pair is stripped before `Find` checks, so it would not
  reach the fallback.
- **A try-verify-fallback design hides every mutant before the verifier: make the verifier the only gate.**
  `viperconfig.WriteSetting` edits the file text in place, checks the edit reads back as the value, and re-encodes the
  document otherwise. Every pre-check before the read-back (node kind, anchor, quote scanning, a flow-root guard) and
  every wrong edit just fell back to the re-encode, which still wrote the right value, so ~100 mutants lived. Delete
  the pre-checks the read-back subsumes, keep the verifier minimal (decode, compare), and pin the in-place result byte
  for byte with inputs whose re-encode differs (a blank line, two-space indentation, a non-first key), so a broken edit
  shows as lost formatting.
- **gomutants runs only the mutated package's tests: assert a return value in the package's own tests.** A
  `return 0` → `return 1` in `resourceproposal` survived although `prepare` and `session` tests checked the value the
  callers received. Return a value the callers cannot misread (the persisted setting string rather than a MiB count
  with a zero sentinel) and assert it on every path in the package's tests.
- **`tb.Setenv` inside a `tb.Cleanup` is a no-op restore; use `os.Setenv` (error ignored).** `testing.T.Setenv`
  registers its *own* cleanup that resets the variable to its pre-call value; called from within a cleanup that inner
  cleanup runs afterward (LIFO) and undoes the restore. `testutil.UnsetEnv`'s `tb.Setenv(name, value)` therefore left a
  previously-present variable **unset** after the test — restoring neither its value nor its presence. The three
  survivors on the cleanup body (the `STATEMENT_REMOVE` of the whole `tb.Cleanup`, the `BRANCH_IF` body removal, and the
  `STATEMENT_REMOVE` of the `Setenv`) were a real isolation defect, not inert code: they were the only changed-line
  mutants on the PR, all LIVED, and the existing tests asserted only that the variable got unset, never that cleanup
  restored it. Replace the call with `os.Setenv(name, value)` and pin the restore with a subtest whose *parent* asserts
  the value after the subtest returns (the subtest's cleanups run before the parent's assertion).
- **A `termio.Mock` needs `IsInteractiveResult` (or `assumeYes`) for `Confirm` to reach the code under test.** With
  neither, `Confirm` returns `ErrConfirmationRequired` before the prompt, so a test that sets only `ConfirmResult` never
  exercises the path it meant to — a `RETURN_ERROR_NIL` on the pruner's error return stayed green that way.
- **A `RETURN_ERROR_NIL` on a function's sole `return x` orphans the variable and fails to compile (`NOT VIABLE`).**
  Wrap it as `if x != nil { return x }; return nil` so the mutant compiles; the non-nil branch is then killable.
- **A guard whose error is re-derived identically downstream needs to add its own context.** `buildBuildCmd`'s adoption
  failure fell through to a later step that re-raised the same "check for a pending legacy identity" message, so the
  branch-body removal was invisible; wrapping the guard as `fmt.Errorf("adopt legacy identity: %w", err)` makes the
  wrapper text (kills the `BRANCH_IF`) and the preserved cause (kills the `ERRORF_WRAP`) observable.
- **A sub-unit factor mutant can be equivalent over small inputs; test one near a unit boundary.** `mibString`'s
  `1024`→`1025` leaves `mibString(1) == "1 MiB"` unchanged, but `mibString(1023)` flips `"1023 MiB"`→`"1024 MiB"`.
- **A guard can be operationally equivalent to its fall-through.** `if err := json.Unmarshal(...); err != nil { return
  "", false }` is indistinguishable from falling through to `return resp.Directory, resp.Directory != ""` for every
  payload *except* one that sets the field and then errors — a duplicate JSON key
  (`{"directory":"/x","directory":123}`). `json.Unmarshal` pre-validates syntax over the whole input, so a trailing
  syntax error aborts before any field is set; only a type error on a later duplicate key leaves the field populated.
- **`yaml.v3` does NOT pre-validate like `encoding/json`: it sets earlier fields before a later type error.** A parse
  guard (`if err := yaml.Unmarshal(...); err != nil { return zero }`) is therefore killable — `last_checked: <ts>`
  followed by `agents: 5` (a scalar where a map is expected) returns an error *and* leaves `LastChecked` populated, so
  the guard must discard that partial state. Do not generalize the json bullet above to yaml.
- **`map[string]json.RawMessage` never partially populates: no input leaves the map filled when `Unmarshal` errors.** A
  guard that returns a zero value on an unmarshal error is equivalent to the fall-through's missing-key check; record it.
  Likewise `string(raw) == "null"` is a redundant operand (unmarshalling literal `null` into a `string` yields `""` with
  no error); delete it so a type-scoped directive need not suppress the sibling `!ok` operand.
- **Go randomizes map iteration, so a `continue` in a `for … range map` is not testable against a `break` without
  controlling the order.** Restructure to iterate `slices.Sorted(maps.Keys(m))` (behaviour-preserving when the body's
  effects are independent), then a test whose skipped entry sorts first makes a `break` deterministically drop a later
  entry.
- **A `<`-vs-`<=` comparator boundary is unobservable through a caller that feeds it unique keys.** Extract the comparator
  to a named function (`lessMountByGuestPath`) and assert strict weak ordering directly: distinct keys order by value
  (kills a constant-true body) and an equal-key call is irreflexive (kills `<=`).
- **A caller guard duplicated by the callee is an equivalent `BRANCH_IF`.** When `if _, ok := asX(a); !ok { return }`
  merely precedes a call that performs the same guard internally, removing the caller's branch is unobservable; delete
  the redundant guard rather than recording it (the callee's own branch still kills the mutant's intent).
- **An assertion in a *consumer* package does not kill a mutant in a *test-support* package.** gomutants runs only the
  mutated package's tests, so `termio.Mock`'s spinner recording (asserted from `internal/sandbox/vm`) stayed LIVED when
  `internal/termio` was swept — and the double's new guard `return`s were the codecov patch lines, uncovered the same way.
  Assert a test double's recording where the double lives, in the double's own `*_test.go`.
- **`--detect-equivalent` compares assembly, not behaviour.** A mutation that is logically equivalent but compiles to
  different code still reports LIVED, and must be closed by hand (delete or record).
- **A "restore the factory" mutant (`Get = orig` removed from a `t.Cleanup`) needs a type or behavioural check, not a
  func-pointer comparison.** `reflect.ValueOf(fn).Pointer()` returns the *code* pointer, so two closures minted from the
  same function literal compare equal — a mock installed by the helper reads as "restored" against a leak of that same
  literal from an earlier test (and the test then passes under the mutant). Assert the concrete type
  (`Get().(*mockConfigPaths)` must be false after the installing subtest returns) or the restored factory's behaviour
  (the fail-fast default panics).
- **`BRANCH_IF`/`STATEMENT_REMOVE` on a `fmt.Fprintln(os.Stdout, err)` failure report is only visible as text.** Swap
  `os.Stdout` for an `os.Pipe` write end around the call, restore `os.Stdout` and close the write end *before* any
  `t.Errorf` (the test framework's own output goes to stdout too), then assert the read end contains the error's path —
  the mutated branch prints nothing, or `<nil>` when the condition is negated.
- **`// gomutants:disable` is same-line-scoped, so a standalone comment above a statement records nothing.** gomutants
  parses a bare `disable` as `directiveSameLine` and matches it against mutants on *the comment's own line*
  (`internal/discover/directives.go` in v0.7.1); the only other forms are `disable-next-line`, `disable-func`, and
  `disable-regexp`. Place the directive **same-line** on the guarded statement — `if err != nil { // gomutants:disable
  BRANCH_IF reason="…"` — or use `disable-next-line` on the line directly above it (blank lines and other comments are
  skipped, so it binds to the next code line). A `BRANCH_IF` reports its line at the body's `{`, i.e. the `if` line, not
  the body line. A mis-placed directive is a **silent no-op**: after adding one, re-run the tool and confirm the
  package's `mutants_lived` drops and its suppressed count rises.
- **A formatter can move an inline `disable` off the mutant's line and silently un-suppress it.** `golangci-lint fmt`
  wraps a call whose inline `// gomutants:disable …` pushes it past the width, so the directive lands on the closing `)`
  line while gomutants reports the mutant (e.g. an argument literal like `64`) on an earlier line; the directive then
  matches nothing and the mutant survives. Prefer a `// gomutants:disable-next-line <TYPE> reason="…"` on its own line
  above the statement, which the formatter leaves intact — then re-sweep and confirm the suppressed count rose and
  `Lived` dropped. (A wrapped `if f(x) {` is the exception: `BRANCH_IF` reports at the `{`, which stays on the `) {`
  line, so an inline directive there can still bind.)
- **A `disable` reason must argue the mutation cannot change behaviour, not that the condition is hard to reach.** A
  suppressed branch whose body is removed can still be observable through a caller-supplied input or predicate:
  `filewalk.Walk`'s `rel == "."` root skip looked unreachable until a filter that rejects `"."` was considered — the
  mutation then calls `filter(".", true)`, which returns `SkipDir` and prunes the whole tree. Trace the fall-through
  under a hostile caller before recording a branch.
- **Prove unobservability before recording — a too-eager record hides a testable change and the package's real state.**
  Hand-apply the mutant and try to make a test fail before falling back to a record; "the condition looks unreachable" is
  not a reason. `filewalk.Walk`'s `err != nil` guard was recorded as unreachable, but `filepath.WalkDir` calls the
  callback a *second* time carrying the `ReadDir` error when a directory cannot be read, so a filter that removes the
  directory mid-walk reaches the mutated guard, which re-invokes the filter for the same entry — a call-counting test
  kills it. The wrong record also masked the package's true state: `filewalk` read "0 LIVED" in the PR while two
  `readFile` success-return mutants survived untriaged, so re-sweep the whole package (`FULL=1 PKG=…`) after triage
  instead of trusting the count.
- **`os.ReadDir` and `os.ReadFile` return partial results *alongside* an error, so "returns nil on error" is a false
  equivalence.** Both document returning what they read before the error (`os.ReadDir`: "the entries it was able to read
  before the error"; `os.ReadFile`: the bytes read so far). Removing an `if err != nil { continue }` guard after either
  is observable: the partial directory entries get merged, or the partial bytes get parsed (a truncated `MemTotal` line
  yields a nonzero memory figure). Kill it with a function-variable seam (`var readDir = os.ReadDir` /
  `var readMeminfo = os.ReadFile`), have the test return `(partial, err)`, and assert the partial result is discarded —
  the directory is skipped, or the value is 0. Check any "returns nil on error" reason against the stdlib's documented
  behaviour before recording it; the wrong claim is worse than an honest "untestable" record.
- **A clamp's literal bounds are observable: parameterize the helper and table-test literal limits, never suppress the
  constants.** `min(max(runtime.NumCPU(), 1), maxCPUs)` reads as unreachable, but `1`→`2` changes the result on a
  one-CPU host and `maxCPUs` (255)→`254` changes it at 255 CPUs. Extract `clampNumCPUs(cpus int) uint8` and test the
  boundaries. The table's values must be **literals** (0/1, 254/255/256), not `maxCPUs`-relative: a symbolic expectation
  moves with the mutated constant, so the decrement compiles, passes, and LIVEs (`{maxCPUs, maxCPUs}` expected 254 once
  the constant became 254). A literal table kills all four integer mutants on the bounds.
- **Removing `tb.Fatalf` from a test-support helper can be killed with a recording `testing.TB`, or a child process.**
  A test-package fake that embeds `testing.TB` (which satisfies its private method), overrides `Helper`/`TempDir`, and
  makes `Fatalf` append the message then `panic` a sentinel lets the test `recover` and assert the report
  deterministically — cheaper than the child-process form (see `internal/upgrade/upgrade_extra_test.go`). Only the
  helper's input and `tb.TempDir()` are injectable: a failure mid-sequence (between `PlainInit` and `Commit`) still
  needs a production seam, so record those when the test costs more than it is worth. `yaml.Marshal` rejects a `chan`
  with a `panic`, not an error — reach a marshal-error branch with a value implementing `MarshalYAML() (any, error)`.
- **`t.Helper()` changes stack-trace metadata, not the helper's result.** Record its removal when checking the reported
  location would add little value; do not invent a production seam just to make it killable.
- **In `make([]T, len, cap)` the length is observable and the capacity is not; delete the capacity hint.** The length
  mutant (`0`→`1`) leaves a leading zero element, so an exact-contents assertion kills it, but the capacity mutants
  (`cap`→`cap±1`) change nothing a caller can see. Replace `make([]T, 0, 3)` with `var xs []T` to remove the capacity
  family outright, and assert the returned slice exactly to kill the length one.
- **A map's size hint is the same trap with no observable fallback: drop the hint.** Go exposes no capacity (or length)
  for a map, so `make(map[string]string, len(a)+len(b))` yields a LIVED `ARITHMETIC_BASE` on the `+` that no assertion
  can reach — the negative hint is clamped to zero internally. Write `make(map[string]string)`; the pre-size is
  unobservable at any input, so there is nothing to record and nothing to test.
- **A guard whose fields are only *partly* redundant is best extracted and table-tested with synthetic values.**
  `parsedA.Slug == "" || parsedA.Agent == "" || parsedA.Digest != ""` (`sameImageIdentity`): `ParseImageTag` never yields
  a digest with an agent, so `Digest != ""` is fully redundant, and it yields an agent with an empty slug only for a
  pathological `"agents-sandbox/runner-:agent"` ref, so `Slug == ""` is redundant for every real ref. Rather than craft
  that ref, pull the guard into a predicate (`isNameImage`/`hasNameIdentity`) returning `Slug != "" && Agent != "" [&&
  Digest == ""]` and table-test it directly: one row per field makes that field's removal change the result (`{Agent:
  "opencode"}` with no slug fails the `Slug` row), and the `&&`→`||` inversion fails too. Asserting the *predicate*
  keeps the caller's guard intact instead of deleting a check for good. The predicate's rows do **not** kill the
  caller's own `if !isNameImage(parsed) { return false }` (`sameImageRef`, #412): the following `parsed.Slug !=
  image.Slug || parsed.Agent != image.Agent` subsumes it for every complete desired identity, because `ParseImageTag`
  never yields a digest together with an agent, so a digest-pinned parse is rejected by the pair comparison too. Only a
  direct call with an *incomplete* identity — a slug-only or empty `ImageIdentity`, which the sole production caller's
  `image.Ref() != ""` precondition excludes — makes removing the guard observable; assert that row rather than deleting
  a check that is not redundant as a total function.
- **A `x != "" && x == y` guard is killable when *both* sides can be empty; "the caller's `y` is never empty" is not proof of redundancy.**
  `cachedImageMatches` returns `mInspect.Digest != "" && mInspect.Digest == info.Digest` (#410). Each earlier table row varied
  only one side, so removing the middle operand looked equivalent — but with a cached image that records no digest *and* an
  empty `info.Digest`, the mutant's bare `"" == ""` returns `true` while the original returns `false` (reload). The
  distinguishing input is the one where both sides are empty, which is exactly the state the guard exists to reject. Add
  that row; do not generalize from "only one side is ever empty in production".
- **`if len(x) > N { x = x[:N] }`: only one of the condition's integer mutants is observable.** `>N`↔`>=N` and `N`→`N-1`
  differ only when `len==N`, where both branches leave the same `N` characters, so they are equivalent — record them
  with `disable-next-line CONDITIONALS_BOUNDARY,INTEGER_DECREMENT`. Only `N`→`N+1` changes behaviour (an `N+1`-length
  input stops truncating), so kill it with a boundary-length input. The `x[:N]` on the next line is a *separate* mutant
  family (`INTEGER_*` on the slice constant) already killed by a long-enough input.
- **A reflective derivation's skip guards can be unobservable through the production type.** `viperconfig`'s
  `walkConfigKeys` skips unexported fields and untagged fields, but `Config` has no unexported *tagged* field and no
  untagged exported field, so removing either guard changed no derived key (the `BRANCH_IF` body removal and the
  `EXPRESSION_REMOVE` on `!tagged` → `false` both survived). Pass the walk a synthetic `reflect.Type` with one field
  per guard (an unexported tagged field, an untagged exported field) and assert the returned keys — both guards are
  real contracts, so recording them would be wrong.
- **A non-positive-age guard is killable with a *future* timestamp, so do not record it as equivalent.**
  `if age <= 0 { return false }` (`imageWithinAge`) reads as always-true-equivalent because for a past timestamp the
  fall-through `time.Since(used) < age` is also false. A hostile caller gets through: set `LastUsedAt` to
  `now.Add(time.Hour)` so `time.Since` is negative, and the fall-through returns true — that single subtest kills the
  `BRANCH_IF`, the `<=`→`<` boundary, and the `0`→`-1`/`0`→`1` decrement/increment. Only the downstream `<` vs `<=` on
  the final comparison (nanosecond equality, no injectable clock) still needs a record.
- **Two calls to the same test seam need an invocation count, not a presence flag.** `adoptKeysLockedHook` fires once
  before the destroy and again after the state move; a test that sets `probed = true` in the hook passes when either
  call alone is removed, so the `BRANCH_IF` and `STATEMENT_REMOVE` at *both* sites stayed LIVED. Count the calls and
  assert the exact number.
- **A guard whose error is re-derived identically by the step it protects is unobservable until the guard adds its own
  context.** `adoptLegacyIdentity`'s `if err != nil { return err }` after the lock-set derivation falls through, under
  the mutant, into the migration that re-derives the same enumeration and fails with the same message — so a
  message-only assertion still passes. Wrap the guard's error (`fmt.Errorf("derive adoption lock set: %w", err)`),
  assert the wrapper's text to kill the `BRANCH_IF`, and assert `errors.Is` to the cause to kill the `ERRORF_WRAP`
  (`%w`→`%v`) the wrapper itself introduces.
- **Assert a mode literal through the injected seam, not by `os.Stat`.** `os.MkdirAll(dir, 0o700)` /
  `OpenFile(…, 0o600)` mutate to `0o701`/`0o601`, which the process umask may or may not preserve (`0o701 &^ 0o022`
  is `0o701`, but `&^ 0o077` is `0o700`). Replacing the `mkdir`/`open` seams with a recorder makes the argument
  assertion deterministic and umask-free.
- **A `blocking bool` flock seam's blocking branch is observable in-process.** Mutating `if blocking { return
  hostlock.Exclusive(f) }` to fall through to the non-blocking call is invisible while the lock is free (both succeed)
  and unreachable through the same-process registry (it fails before the flock), but a test that holds a raw flock on a
  second open file description and calls the seam from a goroutine with `blocking=true` sees an immediate contention
  error under the mutant and a block-then-success under the original. The "must not have returned" window costs
  nothing and cannot be flaky: the correct code cannot return while the lock is held.
- **A "drop the rune" sentinel is equivalent under any negative value — record it.** `strings.Map`'s contract drops the
  rune for *any* negative mapping result, so `markerText`'s `return -1` mutated to `return -2` changes nothing; a
  `// gomutants:disable-next-line INTEGER_INCREMENT reason="strings.Map drops the rune for any negative mapping"` closes
  it. Confirm the stdlib's documented tolerance before assuming a sentinel's exact value is load-bearing.
- **A wall-clock budget constant is observable as an exact value; pin it with a literal test.** `heavyweightReadyTimeout`'s
  `120` mutated to `119`/`121` and its `*` to `/` (integer constant division makes the Duration `0`, silently disabling
  the ceiling) all survived a test that compared the spec field *to the constant itself*; a test asserting
  `heavyweightReadyTimeout != 120*time.Second` — a literal expected value in the test file, as for
  `autoPruneDefaultThreshold` — kills the whole family.
- **A "nothing happens" assertion is vacuous when the fixture lacks what would make it happen.** Three
  `resolveReconfigDecision` "fingerprint unchanged → no recreate" tests ran without a live VM, and without a VM the
  decision never recreates; their env file was also written to a directory nothing read. Hand-apply the *inverse*
  mutant (`envHasChanged := true`): a negative test that stays green is vacuous. Rebuild it from a fixture where the
  outcome is reachable and change one input per case.
- **A fixture value equal to the fallback's result hides the fallback.** A pass-through test planned port 4096, which
  is `ServeOnlyBasePort`, so `planned == 0` → `true` (probe a free port instead) returned the same value. Pick a value
  the fallback cannot produce.
- **A unit test that calls the next pipeline step by hand does not pin the wiring.** A test that returned the offered
  release from `resolveBuildVersion` and then called `persistOfferedVersion` itself stayed green with the call removed
  from `ensureHomeStage`. Pin a hand-off between stages with a flow test through the entry point.
- **A failure-path test must stub every wait seam it can fall through to.** When a mutant drops an error return, the
  run continues into later stages; a case without the daemon-health stub then waits out the production probe timeout
  and the mutant reports `TIMED OUT` instead of `KILLED`. A jump in `TIMED OUT` against a baseline sweep of the same
  package is the signal; re-run those ids with `--run-mutant-id` to see which test hangs.
- **A test readiness budget must stay small against the whole suite's runtime.** In `vm`, `TestMain` and a helper set
  the daemon and dockerd readiness budgets to 2 s while the package ran in 0.3 s, so a mutant that broke the health
  probe made a dozen tests each poll out 2 s and the sweep reported 17 `TIMED OUT`. Passing cases are healthy on their
  first probe, so 200 ms costs nothing and turns them into kills.

### Operational

- A cold `FULL=1` run of one package (`internal/agent`, ~320 mutants) exceeds 15 minutes in a fresh worktree; reruns
  resume from `.gomutants-cache.json` and are much faster. Run it in the background.
- **Scope a branch or PR sweep with `SINCE=<base ref>`, not the release-tag default.** The spec documents the default
  and the CI scoping; use `SINCE=origin/main` locally to match a PR. Measured on `internal/sandbox/image` (cold
  `.gomutants-cache.json` each run, warm Go build cache): the default `SINCE=v0.6.0` found 42 mutants in 45s,
  `SINCE=origin/main` found 3 in 19s, and `SINCE=HEAD` found 0 and paid a ~7s floor — discovery and the coverage
  pass are package-scoped, so the module-wide floor the CI job pays is larger than this single-package one. Reserve the
  bare default for the release sweep.
- **A mutant that removes loop progress hangs; it reports `TIMED OUT`, not `LIVED`.** `STATEMENT_REMOVE` on a loop's
  `i++` or `i = f(...)` never advances, so the package's test run never returns (locally the worker hits the 2048 MiB
  cap and is killed — same symptom). No assertion can catch it; leave it and read `LIVED`, not `TIMED OUT`, as the
  survivor count.
- A mutant can make a test write into the package directory (a mutated config dir that resolves to `""` wrote
  `internal/sandbox/resourceproposal/config.yaml`). Check `git status` after a sweep and delete such leftovers.
- `make mutation FULL=1 PKG=./internal/agent/` writes `mutation-report.json` (gitignored) plus the cache into the
  worktree. Read the result with `jq`:
  `jq -r '[.files[] as $f | $f.mutations[] | select(.status=="LIVED") | "\($f.file_name):\(.line) \(.type)"] | .[]'`.


## Mutation sweep execution

### Intent

`#268` records 1811 LIVED survivors of 10315 mutants across 36 packages (the 37 attached reports, one per package plus
an empty `notify/assets`). One row is out of the working queue (see *Snapshot caveats*): `internal/agent` (36) was closed
by #259, leaving **1775 survivors across 35 packages**. The reports are the expensive artifact, so the goal is to work the
queue down **over turns without putting `make mutation` in anyone's inner loop**: a coordinator owns the reports,
assignment, verification and issue text, while implementer subagents only fix. The reduction goal, the closing rule (a
survivor is killed, deleted, or recorded), and the recurring types live in `specs/ci/mutation-testing.md` and
this guide; this runbook is only the execution around them. A turn ends with a smaller, re-verified
survivor count, and a human decides whether another turn is worth it.

### Inputs

The source data is the **37 JSON reports attached to `#268`**, treated as an immutable snapshot.

- Download with plain unauthenticated `curl -L` (302-redirects). The URLs are the issue's **"Done when" checklist**,
  which is the source of truth for the list; the `#issuecomment-6327799337` link in the prose is dead (404). Reading the
  list from the issue (`gh issue view 268 --json body`) means no hardcoded URL table to rot.
- Verified: 37 files download, and their top-level `mutants_lived` sums to exactly 1811 (10315 mutants).
- Report shape: `{go_module, files: [{file_name, mutations: [{id, type, status, line, column, original, replacement}]}],
  mutants_lived, …}`. A mutant **id** is `<module-path>:<func>:<TYPE>#n` (`<func>` may be empty → `::`), carrying the
  package and file. **`#n` is an ordinal per (file, function, type) in source order, not a stable key** (see
  *Verification*).
- **Scope is a FULL per-package sweep, not changed-line.** Verified against the tree: `./internal/humanize` full-sweeps
  to 34 mutants and the report holds 34, while `--changed-since v0.5.0` yields 0; `yamlfmt` likewise 6/6 vs 0. The
  issue's "default scope `--changed-since v0.5.0`" line is inconsistent with the attachments — reproduce with
  `make mutation FULL=1 PKG=<pkg>`, not the default scope.
- The snapshot is **immutable**. Progress is never written back into it — it lives in the issue text (below).

### Snapshot caveats

- `notify_assets` is an empty report (`files: null`, 0 mutants) — nothing to close.
- The `./cmd/agents-sandbox` row is a real full sweep now (`mutation-report._cmd_agents-sandbox.json`), **184 survivors of
  1190** — the largest concentrated rows are `commands_system.go` (59), `commands_cli.go` (49), `inspect_output.go` (26),
  `tree.go` (26). The earlier `mutation-report-cmd-agents-sandbox.json` (322 ids byte-identical to `internal/agent`'s) was
  a duplicate and is superseded; read the `._cmd_agents-sandbox` attachment.
- Report → package maps by the filename convention (`mutation-report-._internal_sandbox_vm.json` → `./internal/sandbox/vm`).

### Roles

- **Coordinator** — one agent session in a dedicated git worktree. Owns the snapshot, the queue, assignment,
  verification, the `#268` text, and the turn PR. The only writer of the issue.
- **Worker** — an `implementer-small` subagent. **Fixes only.** It never runs `make mutation`, and never runs the
  module-wide `make fmt`/`make lint`/`make check` (see *Worker protocol*). Workers do not self-select work.

### Queue

- The **worker unit** is a source file plus the `_test.go` files that exercise it (a cluster inside one package) — never a
  single file, because a `kill` edits tests and a test-support helper's mutants need tests in the test files. Units are
  **file-disjoint**, which is what lets several workers run without colliding. The **coordinator assigns** each unit;
  workers do not pull from a shared queue, because disjointness depends on assignment and the coordinator cannot
  arbitrate a mid-flight collision.
- **Two workers in one package interfere even when file-disjoint: they share the package's compile and test run.** A
  worker that runs `go test ./pkg/...` while a peer is mid-edit sees the peer's broken intermediate state and reports
  phantom failures (observed in the spike). Either serialize workers within a package — parallelize across packages
  instead — or have each worker re-run at the end and treat mid-run failures as noise.
- **Across packages they still interfere: the module build and the gomutants cache are shared.** The first real turn
  (14 packages, one `implementer-small` per package) hit this hard: one peer's broken in-flight edit
  (`filewalk.go:63: undefined: data`) broke every other worker's `go test` and coverage build, because a changed
  dependency recompiles for all of them; `.gomutants-cache.json` is a single file too. One worker also wandered off its
  unit into another package and reverted that worker's edits. **Run units serially** (one worker; commit each handback,
  then start the next) or give each worker **its own worktree**; do not fan a dozen out in one checkout.
- **A worker's step budget bounds the unit.** The per-survivor evidence loop (hand-apply → run the named test → revert)
  is expensive, and in the first real turn almost every worker exhausted its step budget, several mid-investigation with
  nothing landed. Size a unit to what one worker closes in a session — a file-cluster of **≲10 survivors** — and keep
  the analysis (mutant-type grouping, reachability) inside the same budget, not ahead of it.
- The **package is the verification unit** (see *Verification*), which is coarser than the worker unit. A turn's
  breadth — how many packages it targets — is the main cost knob: verification re-sweeps every touched package, so a
  turn that spreads across all 35 pays for all 35. Prefer concentrating a turn in fewer packages.
- Inside a unit, survivors are ordered **by mutant type**, using this guide as the playbook: a worker
  that closes ten `ERRORF_WRAP`s back to back applies `errors.Is`/`errors.AsType` once instead of re-deriving it.
- **The order that made turn 3's single-agent sweep work: re-sweep first, drive off the fresh report, batch by type
  within a file, and restructure an order-dependent loop/comparator first.** (1) Re-sweep before working: the attached
  snapshot drifts (its mutant counts and even the functions it names go stale), so the fresh per-package LIVED set is
  the worklist. (2) The **fresh report is the one with mutant types** — a posted residual summary often carries only
  `file:line` and function names, which cannot be batched; take `(file, type, original → replacement)` from the sweep's
  `mutation-report.json` (and, when only counts were posted, the per-package `jq` extracts survive in the session's
  shell-output logs). (3) Batch same-type survivors inside one file. (4) Restructure an order-dependent loop or
  comparator **before** the rest: a pre-sorted loop makes a redundant `sort.Slice` untestable, and a map-ordered loop
  makes a `continue`→`break` non-deterministic, so make the iteration order deterministic and the comparator
  load-bearing (or delete the redundant construct) first — otherwise later test edits are written against masked
  ordering and have to be redone.
- **Max unit size is discovered in the first turns.** A package is not a unit: in `internal/sandbox/vm`, 109 of 310
  survivors sit in `vm_lifecycle.go` and 61 in `reconfig.go`. The first real turn fixed the working figure: **one
  file-cluster of ≲10 survivors per worker** (e.g. `internal/sandbox/doctor`'s eight, all in `testmock.go`, closed
  cleanly; `internal/sysinfo`'s sixteen did not).
- **Durable progress** is the `#268` body's "Survivors by package" table, rewritten once per turn; a per-turn comment
  carries the detail. The in-turn queue, per-unit cursors, and worker dispositions live in a gitignored working file so
  that compaction or a fresh session loses nothing.

### Worker protocol

Given a unit, the worker closes each survivor by the spec's rule — kill, delete, or record — and hands back:

- a **disposition per id** — closed-by (`kill`/`delete`/`record`) or deferred with a reason;
- the **evidence** for each close: for a `kill`, the named test that fails once the mutant is applied; for a `record`,
  that applying the mutant left the suite green; for a `delete`, the reason the construct is redundant;
- a **resume cursor** (the last id it closed); and
- the **diff**.

**The prompt carries the mutant's content, not its line.** Snapshot line numbers drift as `main` moves — the spike's
report was 8 lines stale — so hand the worker `file:type:original → replacement` and let it locate the site.

**Only the coordinator writes shared files.** A worker never edits the triage guide, the issue, or another unit's files;
it surfaces a generalizable lesson in its handback and the coordinator folds it in. Two workers editing one shared
document collide exactly like two workers editing one source file — and the spike's worker did wander into
this guide unprompted.

**Partial returns are expected.** A unit larger than one session stops at its cursor, and the coordinator hands the
remainder to the next worker (or the next turn). The coordinator reuses a worker's session for its next unit until the
session has run long, then rotates to a fresh one — amortizing startup without letting context sprawl.

**Formatting and lint.** A worker formats only the files it touched (`gofmt -w <files>`), which is file-scoped and safe
beside other workers. The module-wide `make fmt`/`make lint`/`make check` runs run in the **coordinator**, serially, at
the turn boundary — a worker's `make fmt` would rewrite a peer's in-flight files, and concurrent `golangci-lint` shares
a cache.

### Review

Every unit's diff gets an adversarial review before acceptance. The `delete` and `record` closes edit production code,
and a green suite hides fragile tests: the spike's review caught a umask-fragile mode assertion (a false failure under
`umask 0077`) and a global `os.Stdout` swap with no restore-on-panic, neither visible from a passing run. Dispatch a fresh
reviewer with the diff and the invariant each deletion leans on, and require a counterexample or a confirmation.

### Landing

**One PR per turn.** Workers share the coordinator's worktree — a subagent inherits the coordinator session's directory, so
moving the coordinator into the worktree puts every worker there (verified) — so disjoint files compose without merges
and the coordinator commits each handback as its own commit on the turn branch, each independently revertable, with no
per-worker worktrees or cherry-picks.

**Turns sequence through `main`.** Turn N+1 branches from `main` **after** turn N is merged; if N is still open, stack
N+1 on N's branch rather than branching from `main`. This matters because verification re-sweeps from the branch's base:
branching from `main` before N merges would re-report N's own fixes as LIVED and regress the `#268` table. The table is
always written from the newest turn's base.

### Coordinator lifecycle

All progress state is external, so the coordinator survives context pressure losslessly:

- **Compaction** is safe. OpenCode auto-compacts near the model's context limit (keeping ~15k recent tokens beside a
  structured summary), and the coordinator may request one deliberately at a safe point:
  `opencode api post /api/session/<id>/compact`. The summary format (objective / next-move / decisions / files) is built
  for handoff, so even dropping the session and starting fresh costs only re-reading the queue.
- **Stops** are sized. The natural end is the **turn boundary** (sweep done, issue updated, PR opened); inside a turn, the
  **cursor** makes any stop resumable.

### Verification

At **end of turn**, for each package the turn touched, re-sweep with the snapshot's scope —
`make mutation FULL=1 PKG=<pkg>` — and join the fresh LIVED mutants to the snapshot's:

- **closed** = a snapshot survivor absent from the fresh LIVED set;
- **residual** = a snapshot survivor still LIVED, left for a later turn.

**Join on mutant identity as a multiset, not on the id.** The `#n` ordinal renumbers when a delete removes a sibling, so
a snapshot id can silently denote a different mutant; do not join on ids, and do not "strip `#n`" either — that key is
even coarser, merging every same-type mutant in a function. Join on `(file, type, original, replacement)`, **by count**:
three identical `t.Helper()` calls share that key, so a set-based comparison would mistake two still-LIVED copies for the
progress of closing the third. Compare the snapshot's per-key count against the fresh count; the difference is closed. A
key whose count reaches zero is fully closed — legitimately, whether we deleted it or an unrelated merge removed it. Do
**not** count a newly-appearing key as a survivor: our dispositions are tests, deletes, and directives.

**The sweep's `Suppressed` count is a cheap self-check.** It counts the `// gomutants:disable` directives that took effect,
so it should equal the unit's `record` closes (the spike's 6 records → `Suppressed: 6`). A `delete` disposition lowers the
package's `mutants_total`, so compare against the snapshot by identity, never by counts.

**Cost, honestly.** The incremental cache keys on the mutant's package **and its covering tests**, so test-only `kill`
dispositions re-run just the affected mutants, while a source edit (`delete`/`record`) invalidates the whole package and
forces a full re-sweep of it. The sweep's own cache is not in the repo, so a fresh worktree starts cold. Budget one
package sweep per touched package, and let that bound a turn's breadth. `-run-mutant-id <id>` re-measures a single
mutant fresh (skipping the cache) for a spot check — safe only while its `#n` has not shifted.

### Turns

One turn = one pass over the queue (or the subset of packages it chose to target), ending at verification + issue
update + PR.

### Not in scope

The spec's non-goals and anti-goals apply: this runbook adds no gate and no zero-survivor target.

### Open to the builder

- The unit-size / clustering rule and a turn's package breadth, tuned from the first turn's measured throughput and
  verification cost.
- Whether the in-turn queue and cursors are one file or per-unit files.
- The downloader's home (a `ci/` script plus a `make` target, or a standalone script).
