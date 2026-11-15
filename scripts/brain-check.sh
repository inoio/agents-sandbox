#!/usr/bin/env bash
# Structural checks for the Operator brain in .operator-shared/.
#
# The brain is injected into every session and is the only map of the project's
# durable truth, so its structural rules are worth enforcing mechanically:
#
# - Frontmatter: every index and spec file declares a non-empty `description`
#   and `read_if`.
# - Links: every relative markdown link in the brain resolves.
# - Catalog: every path the catalog names exists, and every spec subsystem,
#   guide, and plan is catalogued.
# - Plans: no spec cites a plan (a plan is process, deleted once its work lands;
#   detail belongs in the owning spec).
# - Read If style: a catalog Read If does not open with the empty prefix
#   "Implementing"/"Reviewing"/"Resuming".
# - Partition isolation: no shared document references the private partition.
#
# These are the rules of guides/operator-brain-maintenance.md, not a proof of
# accuracy: a stale-but-consistent document still passes.
#
# Usage: scripts/brain-check.sh [ROOT]
#   ROOT defaults to the repository root; the test harness passes a fixture root.
# Exit status is non-zero when any violation is found.

# Catalog paths are written in Markdown backticks, so the patterns below contain
# backticks that are literal, not command substitution.
# shellcheck disable=SC2016
set -euo pipefail

ROOT="${1:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)}"
BRAIN="$ROOT/.operator-shared"

violations=0

report() {
  printf 'brain-check: %s\n' "$1"
  violations=$((violations + 1))
}

relative() { # absolute path -> brain-relative path
  printf '%s\n' "${1#"$BRAIN"/}"
}

frontmatter_value() { # file key -> value, empty when absent
  awk -v key="$2" '
    NR == 1 && $0 != "---" { exit }
    NR > 1 && $0 == "---" { exit }
    index($0, key ":") == 1 { print substr($0, length(key) + 2); exit }
  ' "$1"
}

check_frontmatter() {
  local file rel key value
  while IFS= read -r file; do
    rel="$(relative "$file")"
    for key in description read_if; do
      value="$(frontmatter_value "$file" "$key")"
      value="${value#"${value%%[![:space:]]*}"}"
      [[ -n "$value" ]] || report "$rel: missing $key"
    done
  done < <(find "$BRAIN/index" "$BRAIN/specs" -name '*.md' -type f | sort)
}

check_links() {
  local file rel target resolved
  while IFS= read -r file; do
    rel="$(relative "$file")"
    while IFS= read -r target; do
      target="${target#<}"
      target="${target%>}"
      target="${target%%#*}"
      case "$target" in
        '' | http://* | https://* | mailto:*) continue ;;
      esac
      resolved="$(dirname "$file")/$target"
      [[ -e "$resolved" ]] || report "$rel: broken link $target"
    done < <(grep -oE '\]\([^)]*\)' "$file" | sed -e 's/^](//' -e 's/)$//' || true)
  done < <(find "$BRAIN" -name '*.md' -type f | sort)
}

check_catalog() {
  local catalog="$BRAIN/catalog.md" entry
  local -A catalogued=()

  # The backticks are literal Markdown delimiters in the catalog, not command substitution.
  while IFS= read -r entry; do
    catalogued["$entry"]=1
    if [[ "$entry" == */ ]]; then
      [[ -d "$BRAIN/$entry" ]] || report "catalog.md: $entry: no such directory"
    else
      [[ -e "$BRAIN/$entry" ]] || report "catalog.md: $entry: no such file"
    fi
  done < <(grep -oE '`(specs|guides|plans|index)/[^`]*`' "$catalog" | tr -d '`' | grep -vE '[<>*]' || true)

  local file rel
  while IFS= read -r file; do
    rel="$(relative "$file")"
    [[ -n "${catalogued["$rel"]:-}" ]] || report "catalog.md: $rel: no catalog entry"
  done < <(find "$BRAIN/guides" "$BRAIN/plans" -name '*.md' -type f | sort)

  local subsystem
  while IFS= read -r subsystem; do
    [[ -n "${catalogued["$subsystem"]:-}" ]] || report "catalog.md: $subsystem: no catalog entry"
  done < <(find "$BRAIN/specs" -mindepth 1 -maxdepth 1 -type d | sed -e "s|^$BRAIN/||" -e 's|$|/|' | sort)
}

check_plan_citations() {
  local match
  while IFS= read -r match; do
    report "$(relative "${match%%:*}"): cites a plan (${match#*:})"
  done < <(grep -rn 'plans/' "$BRAIN/specs" || true)
}

check_read_if_style() {
  local match
  while IFS= read -r match; do
    report "catalog.md: Read If restates the description (${match%%:*}: ${match#*:})"
  done < <(grep -nE '^[[:space:]]*- Read If: (Implementing|Reviewing|Resuming)' "$BRAIN/catalog.md" || true)
}

check_partition_isolation() {
  local match
  while IFS= read -r match; do
    report "$(relative "${match%%:*}"): private partition reference (${match#*:})"
  done < <(grep -rnF '.operator/' "$BRAIN" --include='*.md' --exclude='operator.md' || true)
}

if [[ ! -d "$BRAIN" ]]; then
  report "$BRAIN: no such directory"
else
  check_frontmatter
  check_links
  check_catalog
  check_plan_citations
  check_read_if_style
  check_partition_isolation
fi

if [[ "$violations" -gt 0 ]]; then
  printf 'brain-check: %d violation(s)\n' "$violations" >&2
  exit 1
fi
printf 'brain-check: ok\n'
