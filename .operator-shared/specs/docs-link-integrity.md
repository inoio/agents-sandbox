---
description: Contract for the documentation-fragment gate in `ci/check-docs.sh`: every anchor that lands on the published site resolves to a heading that slugs to it (kramdown's derived slug, mirrored), covering rendered-HTML links, `docs/` in-page anchors, and the launcher's doc links through the leaf package `internal/docslink`.
read_if: Changing `ci/check-docs.sh` or `ci/test-check-docs.sh`, the doc-link fragment check, the kramdown slug mirror, the `internal/docslink` package, the doctor/build-error `Docs:` links, or diagnosing a failing `docs-linkcheck`.
---

# Documentation link integrity

## Intent

Every documentation link that lands on the published site carries a fragment naming the section a reader should
read. The anchors are coupled by hand to markdown headings and nothing verified the coupling: renaming a heading
broke a link a user follows *because something already failed*, and `ci/check-docs.sh` stayed green because it
validated the page a rendered link points at but never its fragment (issue #296).

This spec owns the fragment gate. The page- and relative-link checks it builds on are unchanged and documented by
the script itself.

## Goals

- Fail `ci/check-docs.sh` when a fragment landing on the published site matches no heading on the target page.
- Cover all four fragment sources: rendered-HTML links (only in the files the checker scans — root `*.md`, `docs/**`
  (minus `docs/superpowers/`, `docs/_site/`, `docs/vendor/`), and Go sources under `cmd/`/`internal/`), `docs/`
  in-page `#anchor` links, `docs/` cross-page `{% link page.md %}#anchor` links, and the launcher's printed doc
  links.
- Keep the gate Ruby-free: `make check` must not need a Jekyll build.

## Non-goals

- Rendered-HTML validation (needs Ruby + `bundle install`); rejected in #296.
- Root-file same-file in-page anchors (`README.md`, `CHANGELOG.md`): those files render on GitHub, whose slugger is a
  different, server-side algorithm. Only one such link exists (`CHANGELOG.md` `#notifications`); it stays unchecked.
- Declared ids (`### Heading {#anchor}`): the docs do not need them — the gate keys off the derived slug — see
  Decisions. When a heading does carry one, the mirror honours it verbatim.

## Decisions

- **Derived slug, not declared ids.** kramdown generates heading ids itself; the checker mirrors that in a small
  function. Validated against the whole current corpus: every existing fragment resolves under the mirror.
  Declared ids would annotate every linked heading (30+), duplicating each anchor string into an attribute list and
  adding a second place to drift, while the derived algorithm stays relevant to any heading linked later. A heading
  that *does* declare an id is still honoured verbatim, so the gate never false-fails one.
- **Scope: anywhere the fragment lands on the published site** — rendered-HTML links, `docs/` in-page and cross-page
  (`{% link page.md %}#anchor`) anchors, and the launcher's doc links. Also validated for every scanned source file,
  tests included (a test asserting a dead anchor is a dead claim).
- **A leaf package `internal/docslink` composes the launcher's doc links.** It replaces the two duplicated
  `docsBaseURL` consts (`internal/sandbox/doctor`, `internal/sandbox/image`) and, more importantly, makes the
  checker's Go extraction uniform: a call carries both the page and the anchor as literals, so the script needs no
  knowledge of an implied page or a private helper name.

## The GFM slug

The site builds with kramdown's **GFM input** (Jekyll's default; `_config.yml` leaves kramdown unconfigured, so a
local build and GitHub Pages agree). GFM headings are slugged by `kramdown-parser-gfm`'s `generate_gfm_header_id`
(kramdown-parser-gfm 1.1.0, kramdown 2.5.2) on the **post-inline** `raw_text` — the mirror approximates that with
the heading's source-line text, see below — **not** kramdown core's `basic_generate_id`, which strips a leading
`[^a-zA-Z]+` and diverges on a digit-led heading (the corpus has `manage-config.md`'s `## 1. …`–`## 4. …`):

1. lowercase;
2. keep only `[\p{Word}\- \t]` — the checker's ASCII form is `[A-Za-z0-9_-]` plus space and tab;
3. spaces and tabs → `-`;
4. no empty-string fallback: an all-punctuation heading gets no `id` at all (the empty attribute is dropped),
   while a second occurrence of it gets `id="-1"`;
5. a repeated slug within a page gets `-1`, `-2`, … (`-1` is the first duplicate);
6. a heading whose text ends with a declared id (`## Foo {#bar}`) is given that id **verbatim** (`id="bar"`),
   bypassing steps 1–5 — kramdown generates no derived slug and consumes no uniqueness counter for it.

The mirror runs on the heading's **source-line text**; the published site slugs the **post-inline** `raw_text`
`kramdown-parser-gfm` rebuilds from the parsed children, so a link becomes its link text, an entity is decoded, and
emphasis delimiters are gone. The two agree on the whole corpus (0 mismatches over 32 pages), because no corpus
heading carries inline markup or non-ASCII letters. They diverge only for such headings, which the docs style
avoids:

- emphasis: `## __init__ hook` → site `init-hook`, mirror `__init__-hook`
- link: `## [Guide](foo.md)` → site `guide`, mirror `guidefoomd`
- entity: `## A &amp; B` → site `a--b`, mirror `a-amp-b`
- non-ASCII letters: `\p{Word}` is Unicode-aware on the site, ASCII-only in the mirror
- setext headings (`Foo` underlined with `===`/`---`): the site slugs them, the mirror recognises ATX (`#`) only

Calibrated against a real `bundle exec jekyll build`: identical ids on every page.

## `internal/docslink`

A dependency-free leaf package with the site root and one composer:

- `BaseURL` — `https://inoio.github.io/agents-sandbox`.
- `SectionURL(page, anchor string) string` — `BaseURL + "/" + page + ".html#" + anchor`; `page` is the source page
  name without `.md` (e.g. `troubleshooting`).

`doctor` drops `docsBaseURL`, `troubleshootingURL` and `troubleshootingURL("…")` at call sites for
`docslink.SectionURL("troubleshooting", "…")`; `image/build.go` drops its own `docsBaseURL` for the same call. The
five current anchors are unchanged: `microsandbox-runtime`, `kvm-unavailable`, `docker-not-running`,
`not-apple-silicon`, `the-base-image-cannot-be-found`.

A unit test embedding `docslink.SectionURL(p, a)` must use real page/anchor pairs, since the checker reads test
sources too. Compose expected URLs from `BaseURL` rather than a second literal.

The package itself stays dependency-free, but `doctor` imports it, which puts `docslink` in the seam import chain:
its `main_test.go` installs the wrapper's families minus `doctor` directly rather than importing
`internal/testutil/mocks` (which would cycle) — see `specs/test-isolation.md`.

## Checker behaviour

`ci/check-docs.sh` gains:

- a heading-slug extractor: per `docs/<page>.md`, the slug set (with kramdown's uniqueness rule), skipping front
  matter and fenced code, treating `#` only at line start outside a fence, stripping a trailing run of `#`s, and
  honouring a declared `{#id}` verbatim;
- fragment resolution for the four sources:
  - rendered-HTML `<page>.html#<anchor>` (or a directory index) → slug-check `<anchor>` against `docs/<page>.md`;
  - a `docs/` in-page `#<anchor>` → slug-check against the same file;
  - a `docs/` `{% link <page>.md %}#<anchor>` → slug-check against `docs/<page>.md` (the Jekyll path is relative
    to `docs/`; `extract_jekyll_path` currently discards the fragment after `%}`);
  - a Go source `.go` under `cmd/` and `internal/` → extract `docslink.SectionURL("<page>", "<anchor>")` and any
    literal `https://…/agents-sandbox/<page>.html#<anchor>` (kept as a safety net) and slug-check them;
- the Go extractor scans the **whole file**, not one line at a time: a call the author broke across lines (gofmt
  preserves the break) and two matches on one physical line are both resolved, each reported at its own line;
- reference-style link definitions keep their trailing fragment (an angle-bracketed destination is unwrapped), so a
  dead `[ref]: …/page.html#anchor` no longer escapes as a page-only link.
- `report` messages naming the file, line, fragment and target page.

## Calibration

Run the extended checker against the real tree and resolve every finding before landing: fix a genuinely dead
anchor, or adjust the mirror if a heading exposes a divergence. Confirm the mirror against a real
`bundle exec jekyll build` in a checkout with the vendored gems (Ruby and `bundle` are present; the gem bundle is
gitignored and may need `bundle install`), since the published ids are what the gate claims to verify.

## Tests

`ci/test-check-docs.sh` covers, at minimum: a root rendered-HTML link to a matching heading (pass) and a
non-matching one (fail — note the existing `install.html#the-base-image` case currently asserts pass and must be
rewritten); a `docs/` in-page anchor that resolves and one that does not; a `docs/` `{% link page.md %}#anchor`
that resolves and one that does not; Go `docslink.SectionURL` and literal-URL extraction; and slug edge cases
(backticked/punctuated heading, duplicate heading → `-1`/`-2`, a declared `{#id}`, a dash-led slug, and a `#` line
inside YAML front matter that must not become an id).
