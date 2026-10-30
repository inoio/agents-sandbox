#!/usr/bin/env bash
#
# cleanup-merged-branches.sh - prune merged branches and their worktrees, then
# report what is left.
#
# Removes, when merged:
#   1. worktrees whose checked-out branch is merged,
#   2. local branches,
#   3. origin branches.
#
# "Merged" is decided by, in order of confidence:
#   - ancestry: the tip is an ancestor of origin/main or any origin/release/*
#     (fast-forward and merge-commit integration);
#   - a merged pull request whose head branch has this name (via `gh`; catches
#     squash- and rebase-merges);
#   - patch equivalence via `git cherry` (catches rebase-merges offline).
#
# Everything not removed is reported afterwards, grouped as worktrees, local
# branches, and origin branches, with fresh entries green and stale ones yellow.
#
# Prunable worktrees - registered worktrees whose directory is missing here - are
# never touched. The directory may still exist on the host while it is absent
# inside this VM, so the host-side checkout may still be in use.
#
# Usage:
#   cleanup-merged-branches.sh [options]
#
# Options:
#   -n, --dry-run            Print the plan and change nothing. Skips the remote
#                            refresh too, so remote-tracking refs stay untouched.
#   -f, --force              Remove worktrees that have uncommitted/untracked changes.
#   -y, --yes                Skip the interactive confirmation. Required when stdin
#                            or stdout is not a terminal, or the run aborts.
#   --older-than <dur>       Only act on entries whose tip is at least this old
#                            (default 0 = no age filter). Suffixes: s m h d w.
#   --stale <dur>            Age at/after which a remaining entry is stale (default 14d).
#   --fresh <dur>            Window for keeping a just-created worktree with no commits
#                            of its own (default 30m; 0 disables just this check). A
#                            worktree at the local default branch or newer is always kept.
#   --local-only             Do not delete origin branches.
#   --remote-only            Do not touch worktrees or local branches.
#   --exclude <glob>         Keep matching branches/worktrees (repeatable).
#   --no-gh                  Do not use the GitHub CLI to detect merged PRs.
#   --no-fetch               Do not run `git fetch --prune origin` first.
#   --color <when>           auto (default), always, never. Also honours NO_COLOR.
#   --format <kind>          text (default) or json.
#   -q, --quiet              Only print the summary.
#   --configure              Set fetch.prune=true in this repo and exit.
#   -h, --help               Show this help.

set -eo pipefail

dry_run=false
force=false
assume_yes=false
do_local=true
do_remote=true
use_gh=true
do_fetch=true
quiet=false
set_config=false
older_than_seconds=0
stale_after_seconds=$((14 * 24 * 60 * 60))
fresh_after_seconds=$((30 * 60))
color_when=auto
output=text
excludes=()

usage() {
  sed -n '/^# Usage:/,/^#   -h, --help/p' "$0" | sed 's/^# \{0,1\}//'
}

die() { printf 'error: %s\n' "$1" >&2; exit 1; }

contains() {
  local needle="$1" element
  shift
  for element in "$@"; do
    [[ "$element" == "$needle" ]] && return 0
  done
  return 1
}

is_excluded() {
  local name="$1" pattern
  for pattern in ${excludes[@]+"${excludes[@]}"}; do
    # shellcheck disable=SC2254
    case "$name" in $pattern) return 0 ;; esac
  done
  return 1
}

parse_duration() {
  local value="$1"
  [[ "$value" =~ ^([0-9]+)([smhdw]?)$ ]] || return 1
  local number="${BASH_REMATCH[1]}" unit="${BASH_REMATCH[2]}"
  case "$unit" in
    s) printf '%s' "$number" ;;
    m) printf '%s' "$((number * 60))" ;;
    h) printf '%s' "$((number * 3600))" ;;
    d | "") printf '%s' "$((number * 86400))" ;;
    w) printf '%s' "$((number * 7 * 86400))" ;;
  esac
  return 0
}

require_value() {
  (($# >= 2)) || die "option $1 requires a value"
}

while (($#)); do
  case "$1" in
    -n | --dry-run) dry_run=true ;;
    -f | --force) force=true ;;
    -y | --yes) assume_yes=true ;;
    -q | --quiet) quiet=true ;;
    --local-only) do_remote=false ;;
    --remote-only) do_local=false ;;
    --no-gh) use_gh=false ;;
    --no-fetch) do_fetch=false ;;
    --configure) set_config=true ;;
    --older-than)
      require_value "$1" "$@"
      older_than_seconds="$(parse_duration "$2")" || die "invalid --older-than value: $2"
      shift
      ;;
    --older-than=*)
      older_than_seconds="$(parse_duration "${1#*=}")" || die "invalid --older-than value: ${1#*=}"
      ;;
    --stale)
      require_value "$1" "$@"
      stale_after_seconds="$(parse_duration "$2")" || die "invalid --stale value: $2"
      shift
      ;;
    --stale=*)
      stale_after_seconds="$(parse_duration "${1#*=}")" || die "invalid --stale value: ${1#*=}"
      ;;
    --fresh)
      require_value "$1" "$@"
      fresh_after_seconds="$(parse_duration "$2")" || die "invalid --fresh value: $2"
      shift
      ;;
    --fresh=*)
      fresh_after_seconds="$(parse_duration "${1#*=}")" || die "invalid --fresh value: ${1#*=}"
      ;;
    --exclude)
      require_value "$1" "$@"
      excludes+=("$2")
      shift
      ;;
    --exclude=*) excludes+=("${1#*=}") ;;
    --color)
      require_value "$1" "$@"
      color_when="$2"
      shift
      ;;
    --color=*) color_when="${1#*=}" ;;
    --format)
      require_value "$1" "$@"
      output="$2"
      shift
      ;;
    --format=*) output="${1#*=}" ;;
    -h | --help)
      usage
      exit 0
      ;;
    *)
      printf 'error: unknown argument: %s\n\n' "$1" >&2
      usage >&2
      exit 2
      ;;
  esac
  shift
done

case "$color_when" in auto | always | never) ;; *) die "invalid --color value: $color_when" ;; esac
case "$output" in text | json) ;; *) die "invalid --format value: $output" ;; esac
[[ "$output" == json ]] && command -v jq >/dev/null 2>&1 || true
if [[ "$output" == json ]] && ! command -v jq >/dev/null 2>&1; then
  die "jq is required for --format json"
fi

git rev-parse --git-dir >/dev/null 2>&1 || die "not inside a git repository"

if $set_config; then
  if $dry_run; then
    printf 'would set fetch.prune=true\n'
  else
    git config fetch.prune true
    printf 'set fetch.prune=true\n'
  fi
  exit 0
fi

use_color=false
case "$color_when" in
  always) use_color=true ;;
  never) use_color=false ;;
  auto) [[ -t 1 && -z "${NO_COLOR:-}" ]] && use_color=true ;;
esac
[[ "$output" == json ]] && use_color=false
if $use_color; then
  c_green=$'\033[32m'
  c_yellow=$'\033[33m'
  c_dim=$'\033[2m'
  c_reset=$'\033[0m'
else
  c_green=""
  c_yellow=""
  c_dim=""
  c_reset=""
fi

# --- refresh remote state ---------------------------------------------------

# --dry-run promises to change nothing, so it neither prunes remote-tracking refs
# (`git fetch --prune`) nor updates origin/HEAD.
if ! $dry_run && $do_fetch; then
  git fetch --prune --quiet origin 2>/dev/null ||
    printf 'warning: git fetch --prune origin failed; merge detection may be stale\n' >&2
fi
if ! $dry_run; then
  git remote set-head origin -a >/dev/null 2>&1 || true
fi

default_ref="$(git symbolic-ref --quiet --short refs/remotes/origin/HEAD 2>/dev/null || true)"
[[ -n "$default_ref" ]] || default_ref="origin/main"

targets=()
add_target() {
  git rev-parse --verify --quiet "$1" >/dev/null || return 0
  contains "$1" ${targets[@]+"${targets[@]}"} || targets+=("$1")
}
add_target "origin/main"
add_target "$default_ref"
while IFS= read -r ref; do
  add_target "$ref"
done < <(git for-each-ref --format='%(refname:short)' 'refs/remotes/origin/release/*')

((${#targets[@]})) || die "no origin/main or origin/release/* refs found (missing remote?)"

# --- merged pull requests (via gh) ------------------------------------------

gh_available=false
gh_merged_pairs=() # "<headRefName> <headRefOid>" for merged PRs
repo_slug=""
if $use_gh && command -v gh >/dev/null 2>&1; then
  if repo_slug="$(gh repo view --json nameWithOwner --jq '.nameWithOwner' 2>/dev/null)" && [[ -n "$repo_slug" ]]; then
    if merged="$(gh pr list --state merged --limit 2000 --json headRefName,headRefOid --jq '.[] | "\(.headRefName) \(.headRefOid)"' 2>/dev/null)"; then
      gh_available=true
      while IFS= read -r pair; do
        [[ -n "$pair" ]] && gh_merged_pairs+=("$pair")
      done <<<"$merged"
    fi
  fi
fi
if $use_gh && ! $gh_available; then
  printf 'warning: gh merged-PR detection unavailable; squash-merges may be missed\n' >&2
fi

# A merged PR proves the branch merged only if the branch has not moved since:
# its tip must equal the PR's head commit.
gh_merge_matches() {
  local name="$1" tip="$2" pair pair_name pair_oid
  [[ -n "$tip" ]] || return 1
  for pair in ${gh_merged_pairs[@]+"${gh_merged_pairs[@]}"}; do
    pair_name="${pair%% *}"
    pair_oid="${pair##* }"
    [[ "$pair_name" == "$name" && "$pair_oid" == "$tip" ]] && return 0
  done
  return 1
}

# --- merge detection --------------------------------------------------------

local_merge_method() {
  local branch="$1" target output
  for target in ${targets[@]+"${targets[@]}"}; do
    git merge-base --is-ancestor "$branch" "$target" 2>/dev/null && {
      printf 'ancestry'
      return 0
    }
  done
  gh_merge_matches "$branch" "$(git rev-parse "$branch" 2>/dev/null || true)" && {
    printf 'pr'
    return 0
  }
  for target in ${targets[@]+"${targets[@]}"}; do
    output="$(git cherry "$target" "$branch" 2>/dev/null || true)"
    # git cherry ignores merge commits, so a branch that merged a ref other than the
    # target and added a unique conflict resolution can look patch-equivalent. Only
    # trust the patch check when no merge commits remain beyond the target.
    if [[ -n "$output" ]] && ! grep -q '^+' <<<"$output" &&
      [[ -z "$(git rev-list --merges "$target..$branch" 2>/dev/null)" ]]; then
      printf 'patch'
      return 0
    fi
  done
  return 1
}

remote_merge_method() {
  local name="$1" ref="origin/$1" target
  for target in ${targets[@]+"${targets[@]}"}; do
    git merge-base --is-ancestor "$ref" "$target" 2>/dev/null && {
      printf 'ancestry'
      return 0
    }
  done
  gh_merge_matches "$name" "$(git rev-parse "$ref" 2>/dev/null || true)" && {
    printf 'pr'
    return 0
  }
  return 1
}

NOW="$(date +%s)"
commit_age() {
  local committed
  committed="$(git log -1 --format=%ct "$1" 2>/dev/null || true)"
  [[ -n "$committed" ]] || committed="$NOW"
  printf '%s' "$((NOW - committed))"
}
worktree_commit_age() {
  local path="$1" committed
  committed="$(git -C "$path" log -1 --format=%ct HEAD 2>/dev/null || true)"
  [[ -n "$committed" ]] || committed="$NOW"
  printf '%s' "$((NOW - committed))"
}
worktree_creation_age() {
  local path="$1" oldest birth
  # The oldest HEAD reflog entry is the worktree's creation checkout. If the worktree
  # has no reflog of its own, git reports the branch reflog instead (the branch's
  # creation, i.e. older, so the worktree is never seen as fresher than it is).
  # Prints the age in seconds, or nothing when it cannot be established.
  oldest="$(git -C "$path" reflog show --date=unix HEAD 2>/dev/null | tail -1 || true)"
  if [[ "$oldest" =~ HEAD@\{([0-9]+)\} ]]; then
    printf '%s' "$((NOW - BASH_REMATCH[1]))"
    return 0
  fi
  birth="$(stat -c %W "$path" 2>/dev/null || true)"
  [[ "$birth" =~ ^[0-9]+$ ]] || birth="$(stat -f %B "$path" 2>/dev/null || true)"
  if [[ "$birth" =~ ^[0-9]+$ ]] && ((birth > 0)); then
    printf '%s' "$((NOW - birth))"
    return 0
  fi
  return 0
}
worktree_at_or_ahead_of_default() {
  # A: the branch tip is the local default branch or a descendant of it.
  git merge-base --is-ancestor "$local_default" "$1" 2>/dev/null
}
worktree_created_recently() {
  # B: the worktree was created within the fresh window. When the creation time
  # cannot be established, keep the worktree: its tip may be an old ancestor.
  ((fresh_after_seconds > 0)) || return 1
  local age
  age="$(worktree_creation_age "$1")"
  [[ -n "$age" ]] || return 0
  ((age < fresh_after_seconds))
}
older_than_allows() { ((older_than_seconds == 0 || $1 >= older_than_seconds)); }
is_stale() { (($1 >= stale_after_seconds)); }

format_age() {
  local seconds="$1"
  if ((seconds < 60)); then printf '%ss' "$seconds"
  elif ((seconds < 3600)); then printf '%sm' "$((seconds / 60))"
  elif ((seconds < 86400)); then printf '%sh' "$((seconds / 3600))"
  elif ((seconds < 604800)); then printf '%sd' "$((seconds / 86400))"
  else printf '%sw' "$((seconds / 604800))"; fi
}

# --- worktree inventory -----------------------------------------------------

worktree_path=()
worktree_branch=()
worktree_bare=()
worktree_prunable=()

flush_worktree() {
  [[ -n "$_wt_path" ]] || return 0
  worktree_path+=("$_wt_path")
  worktree_branch+=("$_wt_branch")
  worktree_bare+=("$_wt_bare")
  worktree_prunable+=("$_wt_prunable")
}

_wt_path=""
_wt_branch=""
_wt_bare=0
_wt_prunable=0
while IFS= read -r line || [[ -n "$line" ]]; do
  case "$line" in
    'worktree '*)
      flush_worktree
      _wt_path="${line#worktree }"
      _wt_branch=""
      _wt_bare=0
      _wt_prunable=0
      ;;
    'branch '*) _wt_branch="${line#branch refs/heads/}" ;;
    'bare') _wt_bare=1 ;;
    'prunable'*) _wt_prunable=1 ;;
  esac
done < <(git worktree list --porcelain)
flush_worktree

for i in "${!worktree_path[@]}"; do
  if [[ "${worktree_bare[$i]}" != 1 && "${worktree_prunable[$i]}" != 1 && ! -d "${worktree_path[$i]}" ]]; then
    worktree_prunable[i]=1
  fi
done

current_worktree="$(git rev-parse --show-toplevel 2>/dev/null || printf '%s' "$PWD")"
current_worktree="$(cd "$current_worktree" && pwd -P)"
current_branch="$(git symbolic-ref --quiet --short HEAD 2>/dev/null || true)"

local_default="${default_ref#origin/}"

# --- plan: worktrees --------------------------------------------------------

plan_wt_path=()
plan_wt_force=()
skip=()
rem_wt_name=()
rem_wt_branch=()
rem_wt_age=()
rem_wt_stale=()
rem_wt_note=()

for i in "${!worktree_path[@]}"; do
  path="${worktree_path[$i]}"
  branch="${worktree_branch[$i]}"
  [[ "${worktree_bare[$i]}" == 1 ]] && continue

  if [[ -d "$path" ]]; then
    age="$(worktree_commit_age "$path")"
  elif [[ -n "$branch" ]]; then
    age="$(commit_age "$branch")"
  else
    age=0
  fi
  note=""
  if [[ "${worktree_prunable[$i]}" == 1 ]]; then
    note="prunable; left for the host"
  elif [[ "$(cd "$path" && pwd -P)" == "$current_worktree" ]]; then
    note="current worktree"
  elif [[ -z "$branch" ]]; then
    note="detached"
  else
    method="$(local_merge_method "$branch" || true)"
    if [[ -z "$method" ]]; then
      note="unmerged"
    elif [[ "$branch" == release/* ]]; then
      note="release branch"
    elif is_excluded "$branch"; then
      note="excluded (merged)"
    elif [[ "$method" == ancestry ]] && worktree_at_or_ahead_of_default "$branch"; then
      note="default branch or newer; kept"
    elif [[ "$method" == ancestry ]] && worktree_created_recently "$path"; then
      note="fresh session; kept"
    elif ! older_than_allows "$age"; then
      note="younger than --older-than (merged)"
    elif [[ -n "$(git -C "$path" status --porcelain)" ]] && ! $force; then
      note="dirty (merged); use --force"
    elif ! $do_local; then
      note="merged (--remote-only)"
    else
      plan_wt_path+=("$path")
      if [[ -n "$(git -C "$path" status --porcelain)" ]]; then
        plan_wt_force+=(1)
      else
        plan_wt_force+=(0)
      fi
      continue
    fi
  fi

  rem_wt_name+=("$path")
  rem_wt_branch+=("$branch")
  rem_wt_age+=("$age")
  if is_stale "$age"; then rem_wt_stale+=(1); else rem_wt_stale+=(0); fi
  rem_wt_note+=("$note")
done

# Branches still checked out once the planned worktrees are gone.
checked_out=()
for i in "${!worktree_path[@]}"; do
  [[ -n "${worktree_branch[$i]}" ]] || continue
  contains "${worktree_path[$i]}" ${plan_wt_path[@]+"${plan_wt_path[@]}"} && continue
  checked_out+=("${worktree_branch[$i]}")
done

# --- plan: local branches ---------------------------------------------------

plan_lb_name=()
rem_lb_name=()
rem_lb_age=()
rem_lb_stale=()
rem_lb_note=()

while IFS= read -r branch; do
  [[ -n "$branch" ]] || continue
  age="$(commit_age "$branch")"
  method="$(local_merge_method "$branch" || true)"
  upstream_track="$(git for-each-ref --format='%(upstream:track)' "refs/heads/$branch")"
  gone=false
  [[ "$upstream_track" == "[gone]" ]] && gone=true

  note=""
  if [[ -z "$method" ]]; then
    note="unmerged"
  elif [[ "$branch" == "$local_default" ]]; then
    note="default branch"
  elif [[ "$branch" == "$current_branch" ]]; then
    note="current branch"
  elif [[ "$branch" == release/* ]]; then
    note="release branch"
  elif contains "$branch" ${checked_out[@]+"${checked_out[@]}"}; then
    note="checked out in a worktree"
  elif is_excluded "$branch"; then
    note="excluded (merged)"
  elif ! older_than_allows "$age"; then
    note="younger than --older-than (merged)"
  elif ! $do_local; then
    note="merged (--remote-only)"
  else
    plan_lb_name+=("$branch")
    continue
  fi

  $gone && note="${note}${note:+, }gone upstream"
  rem_lb_name+=("$branch")
  rem_lb_age+=("$age")
  if is_stale "$age"; then rem_lb_stale+=(1); else rem_lb_stale+=(0); fi
  rem_lb_note+=("$note")
done < <(git for-each-ref --format='%(refname:short)' refs/heads)

# --- plan: origin branches --------------------------------------------------

plan_rb_name=()
plan_rb_sha=()
rem_rb_name=()
rem_rb_age=()
rem_rb_stale=()
rem_rb_note=()

is_protected_remote() {
  $gh_available || return 1
  [[ -n "$repo_slug" ]] || return 1
  gh api --silent "repos/$repo_slug/branches/$1/protection" >/dev/null 2>&1
}

while IFS= read -r ref; do
  [[ -n "$ref" ]] || continue
  name="${ref#refs/remotes/origin/}"
  [[ "$name" == HEAD ]] && continue
  short="origin/$name"
  age="$(commit_age "$ref")"
  method="$(remote_merge_method "$name" || true)"

  note=""
  if [[ -z "$method" ]]; then
    note="unmerged"
  elif [[ "$short" == "$default_ref" || "$name" == main ]]; then
    note="default branch"
  elif [[ "$name" == release/* ]]; then
    note="release branch"
  elif is_excluded "$name"; then
    note="excluded (merged)"
  elif ! older_than_allows "$age"; then
    note="younger than --older-than (merged)"
  elif ! $do_remote; then
    note="merged (--local-only)"
  elif is_protected_remote "$name"; then
    note="protected on origin"
  else
    plan_rb_name+=("$name")
    plan_rb_sha+=("$(git rev-parse "$ref" 2>/dev/null || true)")
    continue
  fi

  rem_rb_name+=("$short")
  rem_rb_age+=("$age")
  if is_stale "$age"; then rem_rb_stale+=(1); else rem_rb_stale+=(0); fi
  rem_rb_note+=("$note")
done < <(git for-each-ref --format='%(refname)' refs/remotes/origin)

# --- order the remaining lists (stale first, then oldest to newest) ----------

age_desc_indices() {
  local array_name="$1" i
  local -a values=()
  eval "values=( \"\${${array_name}[@]}\" )"
  for i in "${!values[@]}"; do
    printf '%s %s\n' "${values[$i]}" "$i"
  done | sort -k1,1nr -s | awk '{print $2}'
}
rem_wt_order="$(age_desc_indices rem_wt_age)"
rem_lb_order="$(age_desc_indices rem_lb_age)"
rem_rb_order="$(age_desc_indices rem_rb_age)"

# --- confirm ----------------------------------------------------------------

action_count=$((${#plan_wt_path[@]} + ${#plan_lb_name[@]} + ${#plan_rb_name[@]}))
if ! $dry_run && ! $assume_yes && ((action_count > 0)); then
  if [[ -t 0 && -t 1 ]]; then
    printf 'About to remove %d worktree(s), delete %d local and %d origin branch(es). Continue? [y/N] ' \
      "${#plan_wt_path[@]}" "${#plan_lb_name[@]}" "${#plan_rb_name[@]}"
    read -r reply
    [[ "$reply" == [yY] || "$reply" == [yY][eE][sS] ]] || die "aborted"
  else
    die "refusing to delete without a terminal; re-run with --yes to confirm"
  fi
fi

# --- execute ----------------------------------------------------------------

failures=0
ex_wt=()
ex_lb_name=()
ex_lb_sha=()
ex_rb=()

for i in "${!plan_wt_path[@]}"; do
  path="${plan_wt_path[$i]}"
  if [[ "${plan_wt_force[$i]}" == 1 ]]; then
    command=(git worktree remove --force "$path")
  else
    command=(git worktree remove "$path")
  fi
  if $dry_run; then
    ex_wt+=("$path")
  elif "${command[@]}"; then
    ex_wt+=("$path")
  else
    printf 'error: could not remove worktree %s\n' "$path" >&2
    failures=$((failures + 1))
  fi
done

for branch in ${plan_lb_name[@]+"${plan_lb_name[@]}"}; do
  sha="$(git rev-parse --short "$branch")"
  # The branch was verified merged, so it holds no unique commits and -D is safe.
  if $dry_run; then
    ex_lb_name+=("$branch")
    ex_lb_sha+=("$sha")
  elif git branch -D "$branch" >/dev/null; then
    ex_lb_name+=("$branch")
    ex_lb_sha+=("$sha")
  else
    printf 'error: could not delete local branch %s\n' "$branch" >&2
    failures=$((failures + 1))
  fi
done

for i in "${!plan_rb_name[@]}"; do
  name="${plan_rb_name[$i]}"
  # Pin the expected tip: a push that landed after planning must not be deleted.
  if $dry_run; then
    ex_rb+=("$name")
  elif git push --quiet origin --delete --force-with-lease="refs/heads/$name:${plan_rb_sha[$i]}" "$name"; then
    ex_rb+=("$name")
  else
    printf 'error: could not delete origin/%s\n' "$name" >&2
    failures=$((failures + 1))
  fi
done

# --- render -----------------------------------------------------------------

render_entry() {
  local color="$1" name="$2" age="$3" note="$4"
  printf '  %s%s%s %s(%s)%s' "$color" "$name" "$c_reset" "$c_dim" "$(format_age "$age")" "$c_reset"
  [[ -n "$note" ]] && printf ' %s[%s]%s' "$c_dim" "$note" "$c_reset"
  printf '\n'
}

render_remaining_worktrees() {
  ((${#rem_wt_name[@]})) || return 0
  printf 'Remaining worktrees:\n'
  local i color name
  for i in $rem_wt_order; do
    color="$c_green"
    (( ${rem_wt_stale[$i]} )) && color="$c_yellow"
    name="${rem_wt_name[$i]}"
    [[ -n "${rem_wt_branch[$i]}" ]] && name="$name [${rem_wt_branch[$i]}]"
    render_entry "$color" "$name" "${rem_wt_age[$i]}" "${rem_wt_note[$i]}"
  done
}

render_remaining_local() {
  ((${#rem_lb_name[@]})) || return 0
  printf 'Remaining local branches:\n'
  local i color
  for i in $rem_lb_order; do
    color="$c_green"
    (( ${rem_lb_stale[$i]} )) && color="$c_yellow"
    render_entry "$color" "${rem_lb_name[$i]}" "${rem_lb_age[$i]}" "${rem_lb_note[$i]}"
  done
}

render_remaining_origin() {
  ((${#rem_rb_name[@]})) || return 0
  printf 'Remaining origin branches:\n'
  local i color
  for i in $rem_rb_order; do
    color="$c_green"
    (( ${rem_rb_stale[$i]} )) && color="$c_yellow"
    render_entry "$color" "${rem_rb_name[$i]}" "${rem_rb_age[$i]}" "${rem_rb_note[$i]}"
  done
}

json_strings() {
  if (($# == 0)); then
    printf '[]'
  else
    printf '%s\n' "$@" | jq -Rsc 'split("\n")[:-1]'
  fi
}

json_removed_worktrees() {
  if ((${#ex_wt[@]} == 0)); then
    printf '[]'
    return 0
  fi
  local path
  for path in "${ex_wt[@]}"; do
    jq -cn --arg path "$path" '{path:$path}'
  done | jq -sc '.'
}

json_removed_local() {
  if ((${#ex_lb_name[@]} == 0)); then
    printf '[]'
    return 0
  fi
  local i
  for i in "${!ex_lb_name[@]}"; do
    jq -cn --arg name "${ex_lb_name[$i]}" --arg sha "${ex_lb_sha[$i]}" '{name:$name,sha:$sha}'
  done | jq -sc '.'
}

json_removed_origin() {
  if ((${#ex_rb[@]} == 0)); then
    printf '[]'
    return 0
  fi
  local name
  for name in "${ex_rb[@]}"; do
    jq -cn --arg name "$name" '{name:$name}'
  done | jq -sc '.'
}

json_remaining_worktrees() {
  if ((${#rem_wt_name[@]} == 0)); then
    printf '[]'
    return 0
  fi
  local i stale
  for i in $rem_wt_order; do
    stale=false
    (( ${rem_wt_stale[$i]} )) && stale=true
    jq -cn --arg path "${rem_wt_name[$i]}" --arg branch "${rem_wt_branch[$i]}" \
      --argjson ageSeconds "${rem_wt_age[$i]}" --argjson stale "$stale" --arg note "${rem_wt_note[$i]}" \
      '{path:$path,branch:$branch,ageSeconds:$ageSeconds,stale:$stale,note:$note}'
  done | jq -sc '.'
}

json_remaining_local() {
  if ((${#rem_lb_name[@]} == 0)); then
    printf '[]'
    return 0
  fi
  local i stale
  for i in $rem_lb_order; do
    stale=false
    (( ${rem_lb_stale[$i]} )) && stale=true
    jq -cn --arg name "${rem_lb_name[$i]}" --argjson ageSeconds "${rem_lb_age[$i]}" \
      --argjson stale "$stale" --arg note "${rem_lb_note[$i]}" \
      '{name:$name,ageSeconds:$ageSeconds,stale:$stale,note:$note}'
  done | jq -sc '.'
}

json_remaining_origin() {
  if ((${#rem_rb_name[@]} == 0)); then
    printf '[]'
    return 0
  fi
  local i stale
  for i in $rem_rb_order; do
    stale=false
    (( ${rem_rb_stale[$i]} )) && stale=true
    jq -cn --arg name "${rem_rb_name[$i]}" --argjson ageSeconds "${rem_rb_age[$i]}" \
      --argjson stale "$stale" --arg note "${rem_rb_note[$i]}" \
      '{name:$name,ageSeconds:$ageSeconds,stale:$stale,note:$note}'
  done | jq -sc '.'
}

if [[ "$output" == json ]]; then
  dry_json=false
  $dry_run && dry_json=true
  jq -n \
    --argjson dryRun "$dry_json" \
    --argjson targets "$(json_strings ${targets[@]+"${targets[@]}"})" \
    --argjson removedWorktrees "$(json_removed_worktrees)" \
    --argjson removedLocalBranches "$(json_removed_local)" \
    --argjson removedOriginBranches "$(json_removed_origin)" \
    --argjson skipped "$(json_strings ${skip[@]+"${skip[@]}"})" \
    --argjson remainingWorktrees "$(json_remaining_worktrees)" \
    --argjson remainingLocalBranches "$(json_remaining_local)" \
    --argjson remainingOriginBranches "$(json_remaining_origin)" \
    --argjson failures "$failures" \
    '{dryRun:$dryRun,
      targets:$targets,
      removed:{worktrees:$removedWorktrees,localBranches:$removedLocalBranches,originBranches:$removedOriginBranches},
      skipped:$skipped,
      remaining:{worktrees:$remainingWorktrees,localBranches:$remainingLocalBranches,originBranches:$remainingOriginBranches},
      failures:$failures}'
else
  if ((${#skip[@]})); then
    printf 'Skipped:\n'
    printf '  %s\n' "${skip[@]}"
    printf '\n'
  fi

  if ! $quiet; then
    if ((${#ex_wt[@]})); then
      if $dry_run; then printf 'Worktrees to remove:\n'; else printf 'Removed worktrees:\n'; fi
      printf '  %s\n' "${ex_wt[@]}"
    fi
    if ((${#ex_lb_name[@]})); then
      if $dry_run; then printf 'Local branches to delete:\n'; else printf 'Deleted local branches:\n'; fi
      for i in "${!ex_lb_name[@]}"; do
        printf '  %s (was %s)\n' "${ex_lb_name[$i]}" "${ex_lb_sha[$i]}"
      done
    fi
    if ((${#ex_rb[@]})); then
      if $dry_run; then printf 'Origin branches to delete:\n'; else printf 'Deleted origin branches:\n'; fi
      printf '  origin/%s\n' "${ex_rb[@]}"
    fi
    ((${#ex_wt[@]} + ${#ex_lb_name[@]} + ${#ex_rb[@]})) && printf '\n'

    render_remaining_worktrees
    ((${#rem_wt_name[@]})) && printf '\n'
    render_remaining_local
    ((${#rem_lb_name[@]})) && printf '\n'
    render_remaining_origin
    ((${#rem_rb_name[@]})) && printf '\n'
  fi

  if $dry_run; then
    printf 'dry run: would remove %d worktree(s), delete %d local branch(es), delete %d origin branch(es)\n' \
      "${#ex_wt[@]}" "${#ex_lb_name[@]}" "${#ex_rb[@]}"
  else
    printf 'done: removed %d worktree(s), deleted %d local branch(es), deleted %d origin branch(es)\n' \
      "${#ex_wt[@]}" "${#ex_lb_name[@]}" "${#ex_rb[@]}"
  fi
fi

((failures == 0)) || {
  printf '%d action(s) failed\n' "$failures" >&2
  exit 1
}
