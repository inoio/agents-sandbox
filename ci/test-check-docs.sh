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

printf '\n%d passed, %d failed\n' "$pass" "$fail"
[[ "$fail" -eq 0 ]]