#!/usr/bin/env bash
# Validate internal documentation links.
#
# - Root-level Markdown files (README.md, ...) must link to documentation pages
#   with rendered HTML links (https://inoio.github.io/agents-sandbox/...).
#   Non-docs relative links must resolve to an existing repo file.
# - Pages under docs/ link to each other with Jekyll {% link %} tags that must
#   resolve relative to docs/ (generated diagrams/*.svg resolve to the .puml
#   source, since the SVGs are built by PlantUML and gitignored).
#
# Exit status is non-zero when any violation is found.
#
# Usage: ci/check-docs.sh [ROOT]
#   ROOT defaults to the repository root; the test harness passes a fixture root.

set -euo pipefail

ROOT="${1:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)}"
DOCS="$ROOT/docs"
SITE_BASE="https://inoio.github.io/agents-sandbox"

violations=0

report() {
  printf 'docs: %s\n' "$1"
  violations=$((violations + 1))
}

# Emit "lineno<TAB>target" for every markdown link in a file, resolving
# reference-style links ([text][id], [id], and definitions "[id]: url") and
# stripping titles and angle-bracket wrappers from inline links. Rendered docs
# HTML links are kept (validated by the root handler); other external links and
# bare in-page anchors are skipped.
link_targets() {
  local file="$1" line lineno rest match target id
  local -A refs=()
  local re_def re_inline re_ref
  re_def='^[[:space:]]*\[([^]]+)\]:[[:space:]]*([^#[:space:]]+)'
  re_inline='\[[^]]*\]\(([^()]*\([^()]*\)[^()]*|[^()]*|<[^>]*>)\)'
  re_ref='\[([^]]*)\]\[([^]]*)\]|\[([^]]*)\]'

  # First pass: collect reference definitions "[id]: url".
  while IFS= read -r line; do
    if [[ "$line" =~ $re_def ]]; then
      refs["${BASH_REMATCH[1]}"]="${BASH_REMATCH[2]}"
    fi
  done <"$file"

  lineno=0
  while IFS= read -r line; do
    lineno=$((lineno + 1))
    rest="$line"

    # Inline links [text](target) / [text](target "title"). The target may be
    # wrapped in <...> to permit parentheses (e.g. foo(1).md).
    while [[ "$rest" =~ $re_inline ]]; do
      target="${BASH_REMATCH[1]}"
      if [[ "$target" == \<*\> ]]; then
        target="${target#<}"
        target="${target%>}"
      elif [[ "$target" == *' "'* || "$target" == *" '"* ]]; then
        # Strip an optional title ("target "title"" / "target 'title'").
        target="${target%%[[:space:]]*}"
      fi
      emit_link "$lineno" "$target" || true
      match="${BASH_REMATCH[0]}"
      [[ -n "$match" && "$rest" == *"$match"* ]] || break
      rest="${rest#*"$match"}"
    done

    # Reference-style links: [text][id] and shortcut [id].
    while [[ "$rest" =~ $re_ref ]]; do
      id="${BASH_REMATCH[2]:-${BASH_REMATCH[3]}}"
      if [[ -n "$id" && -n "${refs[$id]:-}" ]]; then
        emit_link "$lineno" "${refs[$id]}" || true
      fi
      match="${BASH_REMATCH[0]}"
      [[ -n "$match" && "$rest" == *"$match"* ]] || break
      rest="${rest#*"$match"}"
    done
  done <"$file"
}

# Emit a single link target unless it is external or a bare in-page anchor.
# Rendered docs HTML links are kept for the root handler to validate.
emit_link() {
  local lineno="$1" target="$2"
  case "$target" in
    "$SITE_BASE"/*) ;; # rendered docs html link - keep (validated by the root handler)
    http://* | https://* | mailto:* | tel:* | ftp://* | \#*) return ;;
  esac
  printf '%s\t%s\n' "$lineno" "$target"
}

# Extract the path from a Jekyll "{% link some/path.md %}" tag.
extract_jekyll_path() {
  local path="${1#\{% link }"
  path="${path%%%\}*}"
  path="${path% }"
  printf '%s\n' "$path"
}

# Validate a single Jekyll {% link %} tag against a base directory.
check_jekyll_link() {
  local rel="$1" lineno="$2" base="$3" target="$4" path
  path="$(extract_jekyll_path "$target")"
  if [[ "$path" == /* ]]; then
    report "$rel:$lineno: {% link %} path '$path' must be relative (no leading slash)"
    return
  fi
  # Generated diagram SVGs are gitignored; check the .puml source instead.
  if [[ "$path" == diagrams/*.svg ]]; then
    path="${path%.svg}.puml"
  fi
  if [[ ! -e "$base/$path" ]]; then
    report "$rel:$lineno: {% link %} target '$path' does not exist"
  fi
}

# Validate one link target found in a file, dispatching on the file's role.
handle_target() {
  local mode="$1" rel="$2" lineno="$3" target="$4" path
  case "$mode" in
    root)
      case "$target" in
        "$SITE_BASE"/*)
          # Rendered docs HTML link: map back to the source page.
          path="${target#"$SITE_BASE"/}"
          if [[ -z "$path" ]]; then
            [[ -f "$DOCS/index.md" ]] ||
              report "$rel:$lineno: docs html link '$target' has no source index page"
          elif [[ "$path" == */ ]]; then
            [[ -f "$DOCS/${path}index.md" ]] ||
              report "$rel:$lineno: docs html link '$path' has no source index page"
          else
            path="${path%.html}.md"
            [[ -f "$DOCS/$path" ]] ||
              report "$rel:$lineno: docs html link '$path' does not exist"
          fi
          ;;
        *'{% link'*) report "$rel:$lineno: {% link %} tags only work under docs/; use a rendered docs html link" ;;
        docs | docs/* | /docs | /docs/*)
          report "$rel:$lineno: docs link '$target' must be a rendered html link ($SITE_BASE/...)" ;;
        /*) report "$rel:$lineno: internal link '$target' must be relative (no leading slash)" ;;
        *)
          path="${target%%#*}"
          [[ -e "$ROOT/$path" ]] || report "$rel:$lineno: link target '$path' does not exist"
          ;;
      esac
      ;;
    docs)
      case "$target" in
        "$SITE_BASE"/*) ;; # rendered docs link (external) - not a Jekyll tag
        *'{% link'*) check_jekyll_link "$rel" "$lineno" "$DOCS" "$target" ;;
        *) report "$rel:$lineno: internal link '$target' must use a Jekyll {% link %} tag" ;;
      esac
      ;;
  esac
}

# Run the link check over every .md file under a directory.
check_files() {
  local mode="$1" dir="$2"
  shift 2
  local file rel lineno target
  while IFS= read -r file; do
    rel="${file#"$ROOT"/}"
    while IFS=$'\t' read -r lineno target; do
      handle_target "$mode" "$rel" "$lineno" "$target"
    done < <(link_targets "$file")
  done < <(find "$dir" "$@" -name '*.md' | sort)
}

# Root-level markdown files (README.md, ...): docs links must be rendered HTML.
check_files root "$ROOT" -maxdepth 1

# Files under docs/: internal links must be Jekyll {% link %} tags.
# Skip gitignored trees that are not project documentation: docs/superpowers/
# is local tooling (see AGENTS.md), docs/_site/ is Jekyll build output, and
# docs/vendor/ is the Ruby gem bundle. They are absent in CI but present
# locally, and their markdown would otherwise produce false violations.
check_files docs "$DOCS" \
  -not -path "$DOCS/superpowers/*" \
  -not -path "$DOCS/_site/*" \
  -not -path "$DOCS/vendor/*"

if [[ "$violations" -gt 0 ]]; then
  printf 'docs: %d violation(s) found\n' "$violations"
  exit 1
fi
printf 'docs: all internal links are valid\n'
