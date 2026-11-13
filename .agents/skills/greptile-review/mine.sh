#!/usr/bin/env bash
# Refresh the Greptile findings corpus: one PR<TAB>path<TAB>severity<TAB>title per line.
# Usage: mine.sh [repo] [output-file]; the output defaults to corpus.tsv beside this script.
set -uo pipefail

repo="${1:-inoio/agents-sandbox}"
script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
output="${2:-$script_dir/corpus.tsv}"

# A failed or partial fetch aborts here, before the corpus is touched.
if ! rows="$(
  gh api --paginate "repos/$repo/pulls/comments" \
    --jq '.[] | select(.user.login == "greptile-apps[bot]") | [.pull_request_url, .path, .body] | @tsv' \
  | while IFS=$'\t' read -r prurl path body; do
      sev=$(printf '%s' "$body" | grep -oE 'badges/p[0-9]' | head -1 | sed 's#badges/##')
      [ -n "$sev" ] || continue
      pr="${prurl##*/}"
      title=$(printf '%s' "$body" | grep -oE '\*\*[^*]+\*\*' | head -1 | sed 's/\*\*//g')
      printf '%s\t%s\t%s\t%s\n' "$pr" "$path" "$sev" "$title"
    done | sort -u
)"; then
  echo "mine.sh: gh api failed for $repo — corpus unchanged" >&2
  exit 1
fi
if [ -z "$rows" ]; then
  echo "mine.sh: no rows produced (gh auth, rate limit, or a moved login) — corpus unchanged" >&2
  exit 1
fi

temporary="$(mktemp "${output}.XXXXXX")" || exit 1
trap 'rm -f "$temporary"' EXIT
printf '%s\n' "$rows" > "$temporary" || exit 1
chmod 644 "$temporary" || exit 1
mv -- "$temporary" "$output" || exit 1
echo "mine.sh: wrote $(printf '%s\n' "$rows" | wc -l | tr -d ' ') rows to $output" >&2
