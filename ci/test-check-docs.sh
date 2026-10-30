#!/usr/bin/env bash
# Tests for ci/check-docs.sh.
#
# Builds throwaway fixture trees and asserts the checker's exit status and
# messages. Run directly (ci/test-check-docs.sh) or via `make docs-linkcheck-test`.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CHECK="$SCRIPT_DIR/check-docs.sh"

pass=0
fail=0

# Create a fixture root with a valid tree: a root README whose docs links are
# rendered HTML, and docs pages that link with Jekyll {% link %} tags.
new_fixture() {
  local dir
  dir="$(mktemp -d)"
  mkdir -p "$dir/docs/install" "$dir/docs/configuration" "$dir/docs/diagrams"
  cat >"$dir/README.md" <<'EOF'
# Root

- [Intro](https://inoio.github.io/agents-sandbox/introduction.html)
- [Configuration](https://inoio.github.io/agents-sandbox/configuration/)
- [Contributing](CONTRIBUTING.md)
EOF
  : >"$dir/CONTRIBUTING.md"
  cat >"$dir/docs/introduction.md" <<'EOF'
# Introduction

See [Install]({% link install.md %}) and [Launcher]({% link configuration/launcher.md %}).

- [top](#introduction)
- [Install section]({% link install.md %}#install)
EOF
  cat >"$dir/docs/install.md" <<'EOF'
# Install
EOF
  cat >"$dir/docs/configuration/index.md" <<'EOF'
# Configuration
EOF
  cat >"$dir/docs/configuration/launcher.md" <<'EOF'
# Launcher
EOF
  printf '@startuml\n@enduml\n' >"$dir/docs/diagrams/arch.puml"
  mkdir -p "$dir/internal/fixture"
  cat >"$dir/internal/fixture/fixture.go" <<'EOF'
package fixture

import "github.com/inoio/agents-sandbox/internal/docslink"

var _ = docslink.SectionURL("troubleshooting", "docker-not-running")
EOF
  cat >"$dir/docs/troubleshooting.md" <<'EOF'
# Troubleshooting

## Docker not running
EOF
  echo "$dir"
}

# expect <name> <expected-exit> <expected-substring> <fixture-dir>
expect() {
  local name="$1" want_status="$2" want_msg="$3" dir="$4"
  local out status
  out="$(bash "$CHECK" "$dir" 2>&1)" && status=0 || status=$?
  if [[ "$status" != "$want_status" ]]; then
    printf 'FAIL %s: exit %s, want %s\n%s\n' "$name" "$status" "$want_status" "$out"
    fail=$((fail + 1))
    return
  fi
  if [[ -n "$want_msg" && "$out" != *"$want_msg"* ]]; then
    printf 'FAIL %s: output missing %q\n%s\n' "$name" "$want_msg" "$out"
    fail=$((fail + 1))
    return
  fi
  printf 'ok   %s\n' "$name"
  pass=$((pass + 1))
}

# A valid tree passes.
d="$(new_fixture)"
expect "valid tree" 0 "all internal links are valid" "$d"
rm -rf "$d"

# Root-level docs links must be rendered HTML, not markdown paths.
d="$(new_fixture)"
printf '\n[Install](docs/install.md)\n' >>"$d/README.md"
expect "root docs markdown link" 1 "must be a rendered html link" "$d"
rm -rf "$d"

d="$(new_fixture)"
printf '\n[Install](/docs/install.md)\n' >>"$d/README.md"
expect "root docs leading-slash link" 1 "must be a rendered html link" "$d"
rm -rf "$d"

# Rendered HTML links are validated against their source page.
d="$(new_fixture)"
printf '\n[Missing](https://inoio.github.io/agents-sandbox/nope.html)\n' >>"$d/README.md"
expect "root broken html link" 1 "does not exist" "$d"
rm -rf "$d"

d="$(new_fixture)"
printf '\n[Missing](https://inoio.github.io/agents-sandbox/nope/)\n' >>"$d/README.md"
expect "root broken html dir link" 1 "no source index page" "$d"
rm -rf "$d"

# An in-page anchor names a heading: the link is mapped back to its source page
# and the fragment checked against that page's heading ids, so an anchored link
# to a real page is valid only when the fragment resolves (a broken page still
# reports the page, not a path with the fragment).
d="$(new_fixture)"
printf '\n[Install](https://inoio.github.io/agents-sandbox/install.html#install)\n' >>"$d/README.md"
expect "root anchored html link resolves" 0 "all internal links are valid" "$d"
rm -rf "$d"

d="$(new_fixture)"
printf '\n[Install](https://inoio.github.io/agents-sandbox/install.html#the-base-image)\n' >>"$d/README.md"
expect "root anchored html link dead fragment" 1 "fragment 'the-base-image' matches no heading in docs/install.md" "$d"
rm -rf "$d"

d="$(new_fixture)"
printf '\n[Missing](https://inoio.github.io/agents-sandbox/nope.html#the-base-image)\n' >>"$d/README.md"
expect "root anchored broken html link" 1 "docs html link 'nope.md' does not exist" "$d"
rm -rf "$d"

# Titled and angle-bracketed inline links parse to the real target.
d="$(new_fixture)"
printf '\n[Titled](https://inoio.github.io/agents-sandbox/nope.html "a title")\n' >>"$d/README.md"
expect "root titled link" 1 "docs html link 'nope.md' does not exist" "$d"
rm -rf "$d"

# Reference-style links are resolved.
d="$(new_fixture)"
printf '\n[Commands][ref]\n\n[ref]: https://inoio.github.io/agents-sandbox/nope.html\n' >>"$d/README.md"
expect "root reference link" 1 "docs html link 'nope.md' does not exist" "$d"
rm -rf "$d"

# A reference definition keeps its fragment, so a dead one is caught rather than
# passing as a page-only link.
d="$(new_fixture)"
printf '\n[Commands][ref]\n\n[ref]: https://inoio.github.io/agents-sandbox/install.html#no-such-heading\n' >>"$d/README.md"
expect "root reference dead fragment" 1 "fragment 'no-such-heading' matches no heading in docs/install.md" "$d"
rm -rf "$d"

# An angle-bracketed reference destination resolves, brackets stripped.
d="$(new_fixture)"
printf '\n[Commands][ref]\n\n[ref]: <https://inoio.github.io/agents-sandbox/install.html#install>\n' >>"$d/README.md"
expect "root reference angle-bracket fragment resolves" 0 "all internal links are valid" "$d"
rm -rf "$d"

# A same-page reference in a docs page names a heading in that page.
d="$(new_fixture)"
printf '\n[Top][here]\n\n[here]: #introduction\n' >>"$d/docs/introduction.md"
expect "docs same-page reference fragment resolves" 0 "all internal links are valid" "$d"
rm -rf "$d"

d="$(new_fixture)"
printf '\n[Top][here]\n\n[here]: #no-such-heading\n' >>"$d/docs/introduction.md"
expect "docs same-page reference dead fragment" 1 "fragment 'no-such-heading' matches no heading in docs/introduction.md" "$d"
rm -rf "$d"

# Non-docs relative links in root files must still resolve.
d="$(new_fixture)"
printf '\n[Gone](MISSING.md)\n' >>"$d/README.md"
expect "root broken relative link" 1 "link target 'MISSING.md' does not exist" "$d"
rm -rf "$d"

# Balanced parentheses inside a destination are parsed, not truncated.
d="$(new_fixture)"
printf '\n[Paren](foo(1).md)\n' >>"$d/README.md"
expect "root balanced-paren link" 1 "link target 'foo(1).md' does not exist" "$d"
rm -rf "$d"

# docs/ pages must use Jekyll {% link %} tags.
d="$(new_fixture)"
printf '\n[Plain](install.md)\n' >>"$d/docs/introduction.md"
expect "docs plain relative link" 1 "must use a Jekyll {% link %} tag" "$d"
rm -rf "$d"

# Broken {% link %} targets are reported.
d="$(new_fixture)"
printf '\n[Missing]({%% link nope.md %%})\n' >>"$d/docs/introduction.md"
expect "docs broken jekyll link" 1 "{% link %} target 'nope.md' does not exist" "$d"
rm -rf "$d"

# {% link %} paths must be relative.
d="$(new_fixture)"
printf '\n[Bad]({%% link /install.md %%})\n' >>"$d/docs/introduction.md"
expect "docs leading-slash jekyll link" 1 "must be relative" "$d"
rm -rf "$d"

# Generated diagram SVGs resolve to their .puml source.
d="$(new_fixture)"
printf '\n![diagram]({%% link diagrams/arch.svg %%})\n' >>"$d/docs/introduction.md"
expect "docs diagram svg link" 0 "all internal links are valid" "$d"
rm -rf "$d"

# External links and anchors are ignored everywhere.
d="$(new_fixture)"
printf '\n[ext](https://opencode.ai) [mail](mailto:x@example.com) [top](#top)\n' >>"$d/README.md"
printf '\n[ext](https://opencode.ai)\n' >>"$d/docs/introduction.md"
expect "external links ignored" 0 "all internal links are valid" "$d"
rm -rf "$d"

# Jekyll build output under docs/_site/ is not project documentation.
d="$(new_fixture)"
mkdir -p "$d/docs/_site/superpowers/plans"
cat >"$d/docs/_site/superpowers/plans/note.md" <<'EOF'
[Missing]({% link nope.md %})
EOF
expect "docs _site build output ignored" 0 "all internal links are valid" "$d"
rm -rf "$d"

# Vendored dependencies under docs/vendor/ are not project documentation.
d="$(new_fixture)"
mkdir -p "$d/docs/vendor/bundle"
cat >"$d/docs/vendor/bundle/README.md" <<'EOF'
[Missing](missing.md)
EOF
expect "docs vendor dependency ignored" 0 "all internal links are valid" "$d"
rm -rf "$d"

# Terminology: "harness" is internal vocabulary and never appears in docs prose.
d="$(new_fixture)"
printf '\nThe harness starts the agent.\n' >>"$d/docs/introduction.md"
expect "terminology harness" 1 'terminology: "harness"' "$d"
rm -rf "$d"

# Terminology: an inline code span or a URL is not prose and is not flagged.
d="$(new_fixture)"
printf '\nThe `harness` name and [x](https://example.com/harness) are fine.\n' >>"$d/docs/introduction.md"
expect "terminology harness in code/url ignored" 0 "all internal links are valid" "$d"
rm -rf "$d"

# Terminology: fenced code blocks (plain and inside a blockquote) are skipped.
d="$(new_fixture)"
printf '\n```text\nharness simply above\n```\n\n> ```console\n> harness\n> ```\n' >>"$d/docs/introduction.md"
expect "terminology fenced code ignored" 0 "all internal links are valid" "$d"
rm -rf "$d"

# A longer fence opening may contain a shorter run as content; it must not be
# mistaken for the close, which would leak the enclosed prose into the checks.
d="$(new_fixture)"
cat >>"$d/docs/introduction.md" <<'EOF'

````markdown
```console
harness simply above
```
````
EOF
expect "terminology nested fence ignored" 0 "all internal links are valid" "$d"
rm -rf "$d"

# Terminology: the old OPENCODE_SANDBOX_ prefix is flagged off the migration pages.
d="$(new_fixture)"
printf '\nSet OPENCODE_SANDBOX_FOO.\n' >>"$d/docs/introduction.md"
expect "terminology legacy prefix flagged" 1 'terminology: the OPENCODE_SANDBOX_ prefix' "$d"
rm -rf "$d"

d="$(new_fixture)"
cat >"$d/docs/deprecations.md" <<'EOF'
# Deprecations

The old prefix OPENCODE_SANDBOX_* still works.
EOF
expect "terminology legacy prefix allowed on deprecations" 0 "all internal links are valid" "$d"
rm -rf "$d"

d="$(new_fixture)"
cat >"$d/docs/install/upgrading.md" <<'EOF'
# Upgrading

The old prefix OPENCODE_SANDBOX_* still works.
EOF
expect "terminology legacy prefix allowed on upgrading" 0 "all internal links are valid" "$d"
rm -rf "$d"

# Terminology: banned filler words, including "let's".
d="$(new_fixture)"
printf '\nThis is simply done.\n' >>"$d/docs/introduction.md"
expect "terminology banned word" 1 'terminology: avoid the filler words' "$d"
rm -rf "$d"

d="$(new_fixture)"
printf "\nLet's begin.\n" >>"$d/docs/introduction.md"
expect "terminology lets" 1 'terminology: avoid the filler words' "$d"
rm -rf "$d"

# Terminology: no positional "above"/"below" references.
d="$(new_fixture)"
printf '\nSee the note above.\n' >>"$d/docs/introduction.md"
expect "terminology above/below" 1 'terminology: do not use positional' "$d"
rm -rf "$d"

# A '#' line inside a fenced code block is not a heading.
d="$(new_fixture)"
cat >>"$d/docs/install.md" <<'EOF'

```console
# not a heading
```
EOF
printf '\n[Bad](https://inoio.github.io/agents-sandbox/install.html#not-a-heading)\n' >>"$d/README.md"
expect "fenced-hash not a heading" 1 "fragment 'not-a-heading' matches no heading in docs/install.md" "$d"
rm -rf "$d"

# A backticked heading slugs its raw source text.
d="$(new_fixture)"
printf '\n## `agents-sandbox doctor` fails\n' >>"$d/docs/install.md"
printf '\n[Doctor](https://inoio.github.io/agents-sandbox/install.html#agents-sandbox-doctor-fails)\n' >>"$d/README.md"
expect "backticked heading slug" 0 "all internal links are valid" "$d"
rm -rf "$d"

# A repeated heading gets kramdown's -1 suffix.
d="$(new_fixture)"
printf '\n## Foo\n\n## Foo\n' >>"$d/docs/install.md"
printf '\n[Second](https://inoio.github.io/agents-sandbox/install.html#foo-1)\n' >>"$d/README.md"
expect "duplicate heading suffix" 0 "all internal links are valid" "$d"
rm -rf "$d"

# A third occurrence gets -2; a -3 link names no heading, so the counter stays
# pinned to the occurrence count.
d="$(new_fixture)"
printf '\n## Foo\n\n## Foo\n\n## Foo\n' >>"$d/docs/install.md"
printf '\n[Third](https://inoio.github.io/agents-sandbox/install.html#foo-2)\n' >>"$d/README.md"
expect "third duplicate heading suffix" 0 "all internal links are valid" "$d"
rm -rf "$d"

d="$(new_fixture)"
printf '\n## Foo\n\n## Foo\n\n## Foo\n' >>"$d/docs/install.md"
printf '\n[Fourth](https://inoio.github.io/agents-sandbox/install.html#foo-3)\n' >>"$d/README.md"
expect "over-counted duplicate heading suffix rejected" 1 "fragment 'foo-3' matches no heading in docs/install.md" "$d"
rm -rf "$d"

# A heading's declared id is honoured verbatim: "{#bar}" makes id="bar", and the
# derived "foo" is not generated for it.
d="$(new_fixture)"
printf '\n## Foo {#bar}\n' >>"$d/docs/install.md"
printf '\n[Declared](https://inoio.github.io/agents-sandbox/install.html#bar)\n' >>"$d/README.md"
expect "declared heading id resolves" 0 "all internal links are valid" "$d"
rm -rf "$d"

d="$(new_fixture)"
printf '\n## Foo {#bar}\n' >>"$d/docs/install.md"
printf '\n[Derived](https://inoio.github.io/agents-sandbox/install.html#foo)\n' >>"$d/README.md"
expect "declared heading id suppresses derived slug" 1 "fragment 'foo' matches no heading in docs/install.md" "$d"
rm -rf "$d"

# A '#' line inside YAML front matter is not a heading, even when it looks like
# one: only the post-front-matter body contributes ids.
d="$(new_fixture)"
cat >"$d/docs/install.md" <<'EOF'
---
title: Install
# install-fragment
---

# Install
EOF
printf '\n[Hidden](https://inoio.github.io/agents-sandbox/install.html#install-fragment)\n' >>"$d/README.md"
expect "front-matter hash not a heading" 1 "fragment 'install-fragment' matches no heading in docs/install.md" "$d"
rm -rf "$d"

# A GFM slug can begin with a dash; the checker must not read it as a grep
# option (and must not SIGPIPE under pipefail).
d="$(new_fixture)"
printf '\n## -foo bar\n' >>"$d/docs/install.md"
printf '\n[Dash](https://inoio.github.io/agents-sandbox/install.html#-foo-bar)\n' >>"$d/README.md"
expect "dash-led heading slug resolves" 0 "all internal links are valid" "$d"
rm -rf "$d"

# A heading that begins with a digit keeps it: the GFM parser slugs the raw
# text without stripping leading non-letters (kramdown's core basic_generate_id
# does strip them, so the two algorithms diverge here).
d="$(new_fixture)"
printf '\n## 1. Turn off host-config fallback\n' >>"$d/docs/install.md"
printf '\n[Step](https://inoio.github.io/agents-sandbox/install.html#1-turn-off-host-config-fallback)\n' >>"$d/README.md"
expect "digit-led heading slug" 0 "all internal links are valid" "$d"
rm -rf "$d"

d="$(new_fixture)"
printf '\n## 1. Turn off host-config fallback\n' >>"$d/docs/install.md"
printf '\n[Step](https://inoio.github.io/agents-sandbox/install.html#turn-off-host-config-fallback)\n' >>"$d/README.md"
expect "digit-led heading stripped form rejected" 1 "fragment 'turn-off-host-config-fallback' matches no heading in docs/install.md" "$d"
rm -rf "$d"

# An intraword underscore survives a GFM slug (its \p{Word} class keeps it);
# dropping it is the character-class regression this guards.
d="$(new_fixture)"
printf '\n## foo_bar baz\n' >>"$d/docs/install.md"
printf '\n[Hook](https://inoio.github.io/agents-sandbox/install.html#foo_bar-baz)\n' >>"$d/README.md"
expect "intraword underscore heading slug" 0 "all internal links are valid" "$d"
rm -rf "$d"

# The heading detector counts one to six leading hashes without an interval
# expression (older mawk cannot do {1,6}): six is a heading, seven is not.
d="$(new_fixture)"
printf '\n###### Six\n' >>"$d/docs/install.md"
printf '\n[Six](https://inoio.github.io/agents-sandbox/install.html#six)\n' >>"$d/README.md"
expect "six-hash heading recognized" 0 "all internal links are valid" "$d"
rm -rf "$d"

d="$(new_fixture)"
printf '\n####### Seven\n' >>"$d/docs/install.md"
printf '\n[Seven](https://inoio.github.io/agents-sandbox/install.html#seven)\n' >>"$d/README.md"
expect "seven-hash line not a heading" 1 "fragment 'seven' matches no heading in docs/install.md" "$d"
rm -rf "$d"

# A same-file anchor in a docs page names a heading in that page.
d="$(new_fixture)"
printf '\n[Gone](#no-such-heading)\n' >>"$d/docs/introduction.md"
expect "docs in-page dead fragment" 1 "fragment 'no-such-heading' matches no heading in docs/introduction.md" "$d"
rm -rf "$d"

# A Jekyll tag's trailing fragment names a heading in the target page.
d="$(new_fixture)"
printf '\n[Gone]({%% link install.md %%}#no-such-heading)\n' >>"$d/docs/introduction.md"
expect "docs cross-page dead fragment" 1 "fragment 'no-such-heading' matches no heading in docs/install.md" "$d"
rm -rf "$d"

# A fragment on a non-.md Jekyll target (a generated diagram) is not checked.
d="$(new_fixture)"
printf '\n![diagram]({%% link diagrams/arch.svg %%}#whatever)\n' >>"$d/docs/introduction.md"
expect "diagram fragment ignored" 0 "all internal links are valid" "$d"
rm -rf "$d"

# A rendered docs HTML link in a docs page is mapped back to its source page and
# its anchor checked there.
d="$(new_fixture)"
printf '\n[Rendered](https://inoio.github.io/agents-sandbox/install.html#no-such-heading)\n' >>"$d/docs/introduction.md"
expect "docs rendered html link dead fragment" 1 "fragment 'no-such-heading' matches no heading in docs/install.md" "$d"
rm -rf "$d"

d="$(new_fixture)"
printf '\n[Rendered](https://inoio.github.io/agents-sandbox/install.html#install)\n' >>"$d/docs/introduction.md"
expect "docs rendered html link resolves" 0 "all internal links are valid" "$d"
rm -rf "$d"

# Go sources: a docslink.SectionURL / literal site URL whose anchor names no
# heading on its page, or whose page has no source markdown, is a dead link.
d="$(new_fixture)"
sed 's/docker-not-running/dead-anchor/' \
  "$d/internal/fixture/fixture.go" >"$d/internal/fixture/fixture.go.tmp"
mv "$d/internal/fixture/fixture.go.tmp" "$d/internal/fixture/fixture.go"
expect "go docslink dead fragment" 1 "fragment 'dead-anchor' matches no heading in docs/troubleshooting.md" "$d"
rm -rf "$d"

d="$(new_fixture)"
cat >>"$d/internal/fixture/fixture.go" <<'EOF'

var _ = docslink.SectionURL("nope", "whatever")
EOF
expect "go docslink missing page" 1 "doc link points at missing page docs/nope.md" "$d"
rm -rf "$d"

d="$(new_fixture)"
cat >>"$d/internal/fixture/fixture.go" <<'EOF'

const literal = "https://inoio.github.io/agents-sandbox/troubleshooting.html#dead-anchor"
EOF
expect "go literal url dead fragment" 1 "fragment 'dead-anchor' matches no heading in docs/troubleshooting.md" "$d"
rm -rf "$d"

# The checker honours the page argument rather than assuming the doctor page.
d="$(new_fixture)"
cat >"$d/internal/fixture/fixture.go" <<'EOF'
package fixture

import "github.com/inoio/agents-sandbox/internal/docslink"

var _ = docslink.SectionURL("install", "install")
EOF
expect "go docslink non-troubleshooting page" 0 "all internal links are valid" "$d"
rm -rf "$d"

# The extractor matches SectionURL whether or not it is package-qualified, so an
# in-package unqualified call (as in internal/docslink/docslink_test.go) is
# checked too — otherwise the "test sources are checked" invariant is vacuous.
d="$(new_fixture)"
mkdir -p "$d/internal/docslink"
cat >"$d/internal/docslink/fixture.go" <<'EOF'
package docslink

var _ = SectionURL("troubleshooting", "dead-anchor")
EOF
expect "go unqualified SectionURL extracted" 1 "fragment 'dead-anchor' matches no heading in docs/troubleshooting.md" "$d"
rm -rf "$d"

# The extractor needs a word boundary: a differently named function ending in
# SectionURL is not a doc link.
d="$(new_fixture)"
cat >"$d/internal/fixture/fixture.go" <<'EOF'
package fixture

var _ = mySectionURL("troubleshooting", "dead-anchor")
EOF
expect "go mySectionURL not extracted" 0 "all internal links are valid" "$d"
rm -rf "$d"

# A SectionURL call the author broke across lines (gofmt preserves the break) is
# still resolved, and the line reported is the call's first line, not the
# preceding newline's.
d="$(new_fixture)"
cat >>"$d/internal/fixture/fixture.go" <<'EOF'

var _ = docslink.SectionURL(
	"troubleshooting",
	"dead-anchor",
)
EOF
expect "go multiline SectionURL dead fragment" 1 "internal/fixture/fixture.go:7: fragment 'dead-anchor'" "$d"
rm -rf "$d"

d="$(new_fixture)"
cat >>"$d/internal/fixture/fixture.go" <<'EOF'

var _ = docslink.SectionURL(
	"troubleshooting",
	"docker-not-running",
)
EOF
expect "go multiline SectionURL resolves" 0 "all internal links are valid" "$d"
rm -rf "$d"

# Two calls on one physical line are both checked: the extractor keeps scanning
# after each match instead of taking only the first.
d="$(new_fixture)"
cat >>"$d/internal/fixture/fixture.go" <<'EOF'

var _ = []string{docslink.SectionURL("troubleshooting", "docker-not-running"), docslink.SectionURL("troubleshooting", "dead-anchor")}
EOF
expect "go two SectionURLs on one line" 1 "internal/fixture/fixture.go:7: fragment 'dead-anchor'" "$d"
rm -rf "$d"

# Two literal site URLs on one physical line are both checked.
d="$(new_fixture)"
cat >>"$d/internal/fixture/fixture.go" <<'EOF'

var _ = []string{"https://inoio.github.io/agents-sandbox/troubleshooting.html#docker-not-running", "https://inoio.github.io/agents-sandbox/troubleshooting.html#dead-anchor"}
EOF
expect "go two literal urls on one line" 1 "internal/fixture/fixture.go:7: fragment 'dead-anchor'" "$d"
rm -rf "$d"

# A call at column zero makes the preceding newline the boundary byte. It must
# be counted once, so a link after it is not reported a line too low.
d="$(new_fixture)"
cat >>"$d/internal/fixture/fixture.go" <<'EOF'

SectionURL("troubleshooting", "docker-not-running")
var _ = docslink.SectionURL("troubleshooting", "dead-anchor")
EOF
expect "go column-zero call keeps later line numbers" 1 "internal/fixture/fixture.go:8: fragment 'dead-anchor'" "$d"
rm -rf "$d"

printf '\n%d passed, %d failed\n' "$pass" "$fail"
[[ "$fail" -eq 0 ]]