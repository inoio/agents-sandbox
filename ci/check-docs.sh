#!/usr/bin/env bash
# Validate internal documentation links.
#
# - Root-level Markdown files (README.md, ...) link with relative paths that
#   must resolve relative to the repo root and must not start with a slash.
# - Pages under docs/ link to each other with Jekyll {% link %} tags that must
#   resolve relative to docs/ (generated diagrams/*.svg resolve to the .puml
#   source, since the SVGs are built by PlantUML and gitignored).
#
# Exit status is non-zero when any violation is found.

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
violations=0

report() {
  printf 'docs: %s\n' "$1"
  violations=$((violations + 1))
}

# Emit "lineno<TAB>target" for every markdown link in a file, skipping
# external links and bare in-page anchors.
link_targets() {
  local line lineno target
  while IFS= read -r line; do
    lineno="${line%%:*}"
    target="${line#*:}"
    target="${target#*](}"
    target="${target%)}"
    case "$target" in
      # external links and bare in-page anchors
      http://* | https://* | mailto:* | tel:* | ftp://* | \#*) continue ;;
    esac
    printf '%s\t%s\n' "$lineno" "$target"
  done < <(grep -Eo -n '\]\([^)]*\)' "$1")
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

# Root-level files: plain relative links only, resolved against the repo root.
check_root_file() {
  local file="$1"
  local rel="${file#"$ROOT"/}"
  local lineno target path
  while IFS=$'\t' read -r lineno target; do
    if [[ "$target" == *'{% link'* ]]; then
      report "$rel:$lineno: {% link %} tags only work under docs/; use a relative path"
    elif [[ "$target" == /* ]]; then
      report "$rel:$lineno: internal link '$target' must be relative (no leading slash)"
    else
      path="${target%%#*}"
      if [[ ! -e "$ROOT/$path" ]]; then
        report "$rel:$lineno: link target '$path' does not exist"
      fi
    fi
  done < <(link_targets "$file")
}

# Files under docs/: internal links must be Jekyll {% link %} tags.
check_docs_file() {
  local file="$1"
  local rel="${file#"$ROOT"/}"
  local lineno target
  while IFS=$'\t' read -r lineno target; do
    if [[ "$target" == *'{% link'* ]]; then
      check_jekyll_link "$rel" "$lineno" "$ROOT/docs" "$target"
    else
      report "$rel:$lineno: internal link '$target' must use a Jekyll {% link %} tag"
    fi
  done < <(link_targets "$file")
}

# Root-level markdown files (README.md, ...): plain relative links only,
# resolved against the repo root.
while IFS= read -r f; do
  check_root_file "$f"
done < <(find "$ROOT" -maxdepth 1 -name '*.md' | sort)

while IFS= read -r f; do
  check_docs_file "$f"
done < <(find "$ROOT/docs" -name '*.md' | sort)

if [[ "$violations" -gt 0 ]]; then
  printf 'docs: %d violation(s) found\n' "$violations"
  exit 1
fi
printf 'docs: all internal links are valid\n'