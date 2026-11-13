#!/usr/bin/env bash
# Dump Greptile inline findings for inoio/agents-sandbox as PR<TAB>path<TAB>sev<TAB>title.
set -u
repo="${1:-inoio/agents-sandbox}"
gh api --paginate "repos/$repo/pulls/comments" \
  --jq '.[] | select(.user.login|test("greptile")) | [.pull_request_url, .path, .body] | @tsv' \
| while IFS=$'\t' read -r prurl path body; do
    sev=$(printf '%s' "$body" | grep -oE 'badges/p[0-9]' | head -1 | sed 's#badges/##')
    [ -n "$sev" ] || continue
    pr="${prurl##*/}"
    title=$(printf '%s' "$body" | grep -oE '\*\*[^*]+\*\*' | head -1 | sed 's/\*\*//g')
    printf '%s\t%s\t%s\t%s\n' "$pr" "$path" "$sev" "$title"
  done | sort -u
