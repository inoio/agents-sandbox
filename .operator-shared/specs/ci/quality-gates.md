---
description: Contract for the repo's quality gates - the golangci-lint standard (formatter set and linter posture in `.golangci.yml`), the dead-code gate (`make deadcode` over a pinned x/tools deadcode with the reason-bearing `//deadcode:ignore` marker and `ci/check-deadcode.sh`), and the documentation-fragment gate in `ci/check-docs.sh` (the kramdown slug mirror and `internal/docslink`).
read_if: Changing `.golangci.yml` (formatters or linters) or the `lint` target's `--build-tags` set, deciding whether to enable a linter or a stricter setting, `CONTRIBUTING.md`'s formatting/linting guidance; the dead-code gate (`make deadcode`, the `//deadcode:ignore` marker, `ci/check-deadcode.sh`, the `DEADCODE_VERSION` pin); or the doc-link gate (`ci/check-docs.sh`, the kramdown slug mirror, `internal/docslink`, the doctor/build-error `Docs:` links); or diagnosing a formatting/lint disagreement, an unmarked dead-code finding, or a failing `docs-linkcheck`; or the local check loop (`make check-local-changes`/`check-branch-changes`, the `JOBS` scheduling, `scripts/changed-targets.sh`).
---

# Quality gates

## golangci-lint standard

The single config is `.golangci.yml` at the repo root. CI enforces both halves: the `lint` job runs `golangci-lint run --build-tags 'integration msb microsandbox_ffi_path'` (the golangci-lint-action `args`, mirrored by `make lint`), and `make validate-fmt` (`golangci-lint fmt -d`) fails on any formatting drift. The build tags are required because golangci-lint's default constraints omit files guarded by a custom tag (`integration`, `msb`, `microsandbox_ffi_path`), so without them the `msb`/`integration` code would never be linted. `make check`/`make verify` run the same targets locally.

**Formatter standard.** `formatters.enable` is exactly `gofumpt`, `gci`, `golines`:

- **`gofumpt`** — stricter `gofmt`, backwards compatible; subsumes `gofmt`, so `gofmt` is not listed.
- **`gci`** — canonical import sections, in order `standard`, `default` (third-party), then `prefix(github.com/inoio/agents-sandbox)`. `goimports` is not listed: `gci` owns import ordering, and `goimports`' remaining job (adding/removing imports) never fires here — imports are a compile error in Go, editors resolve them on save, and any import `gci` groups is already present.
- **`golines`** — wraps at 120 columns (`max-len: 120`); the dominant formatter cost.

This replaced `gofmt` + `goimports` + `golines`: `gofmt` was redundant with `goimports`, and `goimports` cost roughly half the run for byte-identical output on the repo. The swap (gofumpt + gci ≈ a third of goimports' cost) roughly halved `make fmt` (~9s → ~4s).

**Linter posture.** The linter set tracks the upstream `maratori/golangci-lint-config` golden config for the pinned golangci-lint version, with deliberate repo deviations:

- **Added:** `decorder`, `exhaustruct_v5` (with a project-specific `ignore-patterns` list).
- **Replaced:** `revive.exported` is disabled (stutter detection is intentionally not enforced), and the upstream revive/TODO *text* exclusions are replaced by `testmock`/`_test.go` path rules.
- **Kept off though upstream enables them:** `funcorder`, `gochecknoglobals`, `mnd`, `paralleltest`, `recvcheck`, `testpackage` (each a taste or false-positive call documented inline).

Two free upstream settings are adopted because they surfaced no findings (`exhaustruct_v5 allow-empty-blank-assignments: true`, `perfsprint.error-format: false`). Two stricter settings are **deliberately declined**, both for large manual churn with no auto-fix: `godoclint.require-stdlib-doclink` (~54 doc-comment sites) and `govet.shadow.strict` (~119 `err`-shadow reports, mostly benign). Settings blocks for linters not enabled (`funcorder`, `inamedparam`, `mnd`) are commented out as dormant; uncomment alongside the linter if it is enabled. (`issues.max-issues-per-linter` defaults to 50, capping how many of a linter's findings a sweep shows.)

## Dead-code gate

`staticcheck`'s `unused` (U1000) counts test files as usage, so a production function whose only callers are in `_test.go` looks alive and is invisible to every other linter. `golang.org/x/tools/cmd/deadcode` builds an RTA call graph from **main packages only** and reports everything else as unreachable, by default without loading test files (see `../../guides/dead-code-detection-go.md` for the survey and evidence).

The contract is not "no test-only code exists" — intentional test seams are legitimate and numerous. It is: *every function unreachable from production is either deleted, or carries an explicit reason-bearing marker*. A function that is merely dead fails the build. deadcode has no suppression mechanism, so the marker scheme is ours to define and enforce.

### Contract

- **Hard gate, in `verify` and CI.** Wired into `make verify` (not `make check`) and into a dedicated CI job mirroring `vulncheck`'s shape. Unlike `make mutation`, this **is** a gate: it fails the build.
- **Pinned, no global install.** `DEADCODE_VERSION` is a `Makefile` constant, invoked as `go run golang.org/x/tools/cmd/deadcode@$(DEADCODE_VERSION)`, exactly like `GOVULNCHECK_VERSION`. Dependabot does not see it, so it is bumped by hand.
- **The exit code is not the signal.** `ci/check-deadcode.sh` runs deadcode with `-json`, applies the marker and configuration filters, prints the surviving findings in deadcode's compiler-diagnostic form, and exits non-zero when any survive. Statuses: 0 (clean), 1 (a diagnosis — an unmarked finding, a marker that excuses nothing, or an unusable marker), 2 (the analysis could not be trusted); an empty finding set is never inferred from a failed analysis. The Makefile target is a thin wrapper over the script, in the style of `ci/check-docs.sh` + `ci/test-check-docs.sh`.
- **Offline-testable seams.** `DEADCODE_FILES` (source list), `DEADCODE_FIXTURE_DIR` (per-configuration payloads), and `DEADCODE_CHECKER` (a prebuilt checker binary, so the harness builds once instead of once per case) let `ci/test-check-deadcode.sh` exercise the parse-and-verdict path without invoking deadcode or the network. `deadcode-test` runs it in `check` and `verify`; the analysis itself stays out of the fast loop. The script exports `CGO_ENABLED=1`, so a host without a C compiler fails the analysis (exit 2) rather than loading a package set the module is not built from.
- **`//deadcode:ignore reason="…"` is our contract, in two scopes.** *Declaration scope* — in the contiguous comment block immediately preceding the reported declaration, or as a trailing comment on the declaration line (the repo has one-line methods such as `termio.OutToVerboseRedirect.Out`, which has no doc comment); covers exactly that function or method. *File scope* — in the comment block preceding the `package` clause; covers every finding in the file, for test-support files where a per-function marker would be noise. The `reason` is **required and non-empty** in both scopes, double-quoted like `// gomutants:disable <TYPE> reason="..."`. An empty or missing reason is a failure, not a default.
- **Stale markers fail too.** A marker whose declaration or file is *not* reported as dead is an error — the mechanism's self-defence against markers accumulating as decoration. Enforced by `ci/check-deadcode.sh`.
- **Test-only helpers move to test code; they are not marked.** A production declaration reached only from `_test.go` files that is a convenience helper (a fixture constructor, a string builder) is **moved into a `_test.go` file** (or deleted), not excused. Markers are for declarations that must stay in production code: a substitution seam (`image/fetch.go`, `vm/daemon.go`) or a build-tag/test-support file (`*/testmock.go`, `test/harness/*`). Deciding which a finding is belongs to the reviewer at gate time.
- **Configuration matrix.** deadcode analyses one configuration per run. The gate runs one run per configuration in a matrix over the build tags (`''`, `integration`, `msb`, `integration msb`) and fails if **any** reports an unmarked finding. `GOOS` is the host's own; `GOARCH` stays at the host default (no architecture-specific files).
- **`make deadcode` is available on demand** with the same knobs as the other audit targets: `PKG` to scope, and the tags/GOOS matrix overridable. A `PKG`-scoped run is an inspection aid, not the gate: it prints findings, skips stale-marker enforcement (which needs the whole module), and says so. deadcode needs at least one `main` package in the pattern, so the documented narrow form is `PKG=./cmd/...`.

### Decisions

- **Marker, not a register.** A checked-in `pkg.Function reason` file would be cheaper to implement but puts the reason away from the code and rots. The marker keeps the reason next to what it excuses and survives refactors; the accepted cost is owning a small source parser in `ci/`.
- **Move, do not mark, a test-only helper.** A marker on a test-only helper keeps production surface with no production caller; moving it into `_test.go` keeps the production package and the dead-code baseline honest.
- **`verify`, not `check`.** `check` is the fast local loop; `verify` is the read-only CI-parity target and carries `vulncheck` and the `deadcode` analysis.
- **GOOS: the host's own.** CI (`ubuntu-latest`) analyses linux; a macOS developer analyses darwin natively. No `GOOS` pin and no cross C toolchain. Accepted consequence: the two platforms' finding sets need not agree, and the darwin baseline is unverified from the linux dev VM. `GOARCH` stays at the host default.
- **The CI job blocks PRs, not releases.** `deadcode` is **not** added to `release`'s `needs:`, exactly like `vulncheck`; a release tag can publish past a failing gate. The enforcement point is the pull request.
- **Behavior seams carry no marker after the injected-`Seams` change.** The four run behavior seams (`vm.SetProjectVMLock`, `vm.SetDaemonHealthFunc`, `prepare.SetAgentLatestVersion`, `prepare.SetRuntimeHomeMigrationOffer`) moved to the injected `options.Seams` value on `options.RunOptions`, so they are no longer package state and need no `//deadcode:ignore` (see `../architecture/client-injection.md`).
- **Tag configurations included despite being inert.** The `integration` and `msb` tag sets add no findings today (every file behind them is a `_test.go` file, which deadcode skips by default), but the first non-`_test.go` file to land behind such a tag would otherwise be analysed by nobody. The cost is one analysis per configuration.

Non-goals: covering darwin/arm64 analysis; finding reachable-but-unexercised dead code (coverage's job) or dead types/fields/constants/unused imports (staticcheck's job); gating on the mutation-testing model. Anti-goals: a gate that fails on day one and gets disabled; markers as a rubber stamp (a `reason` that says nothing, or file-scope markers on production files); the gate silently weakening when the script fails to parse deadcode's output.

Constraints: follow `AGENTS.md` (idiomatic Go, `jq` for the JSON, `make fmt`/`make lint`, `make check` before the final commit). `CHANGELOG.md` `[Unreleased]`, `CONTRIBUTING.md` (Testing), and `.agents/skills/release/NON-GO-DEPS.md` (the `DEADCODE_VERSION` row, alongside the `gomutants`/`govulncheck` pins) stay in sync. The CI script follows the `ci/check-docs.sh` precedent: a POSIX-sh script plus a `ci/test-check-*.sh` test.

## Documentation link integrity

Every documentation link landing on the published site carries a fragment naming the section a reader should read. The anchors are coupled by hand to markdown headings (and to an explicit `<a id="…"></a>` where a heading was renamed), so this gate fails `ci/check-docs.sh` when a fragment matches no heading or explicit anchor on its target page. The page- and relative-link checks it builds on are unchanged and documented by the script itself.

Goals: cover all four fragment sources — rendered-HTML links (root `*.md`, `docs/**` minus `docs/superpowers/`, `docs/_site/`, `docs/vendor/`, and Go sources under `cmd/`/`internal/`), `docs/` in-page `#anchor` links, `docs/` cross-page `{% link page.md %}#anchor` links, and the launcher's printed doc links — while keeping the gate Ruby-free (`make check` must not need a Jekyll build).

Non-goals: rendered-HTML validation (needs Ruby + `bundle install`); root-file same-file in-page anchors (`README.md`, `CHANGELOG.md`) — those render on GitHub's different slugger, and only one such link exists (`CHANGELOG.md` `#notifications`); declared ids (`### Heading {#anchor}`) — the gate keys off the derived slug, though a declared id is honoured verbatim so the gate never false-fails one; reading anchors from every HTML shape — only a same-line `id="…"` attribute is honoured, which covers the `<a id="…"></a>` alias a rename leaves behind.

### The GFM slug

The site builds with kramdown's **GFM input** (`_config.yml` leaves kramdown unconfigured, so a local build and GitHub Pages agree). GFM headings are slugged by `kramdown-parser-gfm`'s `generate_gfm_header_id` on the post-inline `raw_text` — the mirror approximates that with the heading's source-line text — **not** kramdown core's `basic_generate_id`:

1. lowercase;
2. keep only `[\p{Word}\- \t]` — the checker's ASCII form is `[A-Za-z0-9_-]` plus space and tab;
3. spaces and tabs → `-`;
4. no empty-string fallback: an all-punctuation heading gets no `id` (the empty attribute is dropped), while a second occurrence gets `id="-1"`;
5. a repeated slug within a page gets `-1`, `-2`, … (`-1` is the first duplicate);
6. a heading whose text ends with a declared id (`## Foo {#bar}`) is given that id **verbatim**, bypassing steps 1–5.

The mirror runs on the heading's **source-line text**; the site slugs the **post-inline** `raw_text`, so a link becomes its link text, an entity is decoded, and emphasis delimiters are gone. The two agree on the whole corpus (0 mismatches) because no corpus heading carries inline markup or non-ASCII letters; they diverge only for emphasis, links, entities, non-ASCII letters, and setext headings, which the docs style avoids. Calibrated against a real `bundle exec jekyll build`.

### `internal/docslink`

A dependency-free leaf package: `BaseURL` (`https://inoio.github.io/agents-sandbox`) and `SectionURL(page, anchor string) string` (`BaseURL + "/" + page + ".html#" + anchor`; `page` is the source page name without `.md`). `doctor` and `image/docker_build.go` drop their own `docsBaseURL` consts for `docslink.SectionURL(...)`. The current anchors: `microsandbox-runtime`, `kvm-unavailable`, `container-engine-not-running`, `not-apple-silicon`, `the-base-image-cannot-be-found`, `image-build-fails`. A unit test embedding `docslink.SectionURL(p, a)` must use real page/anchor pairs (the checker reads test sources too). The package stays dependency-free, but `doctor` imports it, so `docslink` is in the seam import chain: its `main_test.go` installs the wrapper's families minus `doctor` directly rather than importing `internal/testutil/mocks` (which would cycle) — see `../testing/test-isolation.md`.

### Checker behaviour

`ci/check-docs.sh` gains a heading-slug extractor (per `docs/<page>.md`, the slug set with kramdown's uniqueness rule, skipping front matter and fenced code, treating `#` only at line start outside a fence, stripping a trailing run of `#`s, honouring a declared `{#id}` verbatim, and collecting a same-line `id="…"` HTML anchor — e.g. `<a id="…"></a>` — beside the derived slugs); fragment resolution for the four sources (rendered-HTML `<page>.html#<anchor>` → slug-check against `docs/<page>.md`; `docs/` in-page `#<anchor>` → same file; `docs/` `{% link <page>.md %}#<anchor>` → `docs/<page>.md`; Go `.go` under `cmd/`/`internal/` → `docslink.SectionURL("<page>", "<anchor>")` and any literal `https://…/agents-sandbox/<page>.html#<anchor>`); the Go extractor scans the **whole file** so a call broken across lines and two matches on one line are both resolved; reference-style link definitions keep their trailing fragment; and `report` messages name the file, line, fragment, and target page.

`ci/test-check-docs.sh` covers the pass and fail cases for each source: a root rendered-HTML link that matches a heading and one that does not (the `install.html#the-base-image` case asserts fail); a `docs/` in-page anchor and a `docs/` `{% link page.md %}#anchor` that each resolve and each do not; Go `docslink.SectionURL` and literal-URL extraction; the slug edge cases (backticked/punctuated heading, duplicate heading → `-1`/`-2`, declared `{#id}`, dash-led slug, `#` inside YAML front matter); and the explicit-anchor cases (an `<a id="…"></a>` resolving a renamed heading's old fragment, the derived slug still resolving beside it, and an HTML anchor inside a fenced code block ignored).

# Local check loop

`make check` and `make verify` are the finalize/CI-parity gates. They schedule their independent targets as parallel `make` jobs with `JOBS = max(2, nproc/4)`: each target parallelizes internally, so the previous `-j=nproc` oversubscribed the box (measured on 16 cores, a one-leaf edit took 33s at `-j16`, 26s at `-j8`, and 20s at `-j4`, flat over `-j4..-j8`). A user's `-j`/`JOBS=` still wins.

`make check-local-changes` and `make check-branch-changes` are a diff-gated convenience loop, **not** gates: `scripts/changed-targets.sh` maps the changed files to whole `check` targets, `local` diffing the uncommitted tree against `HEAD` and `branch` against the merge base with `origin/main`. An unrecognized path or an unresolvable base selects the full set rather than passing silently. A failure of the map script itself (a `git` error) is propagated and the Makefile treats it as an error, never as "no changed files". Run `make check` before finalizing.
