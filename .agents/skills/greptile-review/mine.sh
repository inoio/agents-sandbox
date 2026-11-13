#!/usr/bin/env bash
# Dump Greptile inline findings for a repo as PR<TAB>path<TAB>sev<TAB>title.
set -uo pipefail
repo="${1:-inoio/agents-sandbox}"
rows="$(
  gh api --paginate "repos/$repo/pulls/comments" \
    --jq '.[] | select(.user.login == "greptile-apps[bot]") | [.pull_request_url, .path, .body] | @tsv' \
  | while IFS=$'\t' read -r prurl path body; do
      sev=$(printf '%s' "$body" | grep -oE 'badges/p[0-9]' | head -1 | sed 's#badges/##')
      [ -n "$sev" ] || continue
      pr="${prurl##*/}"
      title=$(printf '%s' "$body" | grep -oE '\*\*[^*]+\*\*' | head -1 | sed 's/\*\*//g')
      printf '%s\t%s\t%s\t%s\n' "$pr" "$path" "$sev" "$title"
    done | sort -u
)"
if [ -z "$rows" ]; then
  echo "mine.sh: no rows produced (gh auth, rate limit, or a moved login) — refusing to overwrite" >&2
  exit 1
fi
printf '%s\n' "$rows"
