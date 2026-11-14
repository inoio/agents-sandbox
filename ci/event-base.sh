#!/usr/bin/env bash
#
# event-base.sh - print the base commit SHA for the current GitHub event.
#
# The base SHA is the commit a change should be diffed against. It is decidable
# only when the event carries a trustworthy one:
#
#   push          the event's `before` commit, if it is a real commit. A created
#                 branch (all-zero `before`) or a missing one is undecidable.
#   pull_request  the merge base of the base branch and `GITHUB_SHA`. A failed
#                 fetch or an unresolvable merge base is undecidable.
#   merge_group   the event's `base_sha`, if non-empty.
#
# Anything else (`schedule`, `workflow_dispatch`, an unknown event) is
# undecidable. There is deliberately no `HEAD^` fallback: a silent guess would
# diff against the wrong commit and misclassify the change.
#
# Undecidable events print a diagnostic to stderr and exit 1. This script does
# NOT decide the fallback policy - callers do, and each picks its own: the
# `changes` job fails closed (treats the failure as "everything changed"), the
# `changelog` job degrades to a shape-only check. See specs/ci/ci-pipeline.md.
#
# Reads GITHUB_EVENT_NAME, GITHUB_SHA, GITHUB_BASE_REF, GITHUB_BEFORE, and
# GITHUB_MERGE_GROUP_BASE_SHA from the environment and operates on the git repo
# at the current directory. Prints the base SHA to stdout and exits 0, or prints
# a diagnostic to stderr and exits 1.

set -euo pipefail

undecidable() {
  printf 'event-base: %s\n' "$1" >&2
  exit 1
}

case "${GITHUB_EVENT_NAME:-}" in
  push)
    before="${GITHUB_BEFORE:-}"
    if [[ -z "$before" ]] || ! git cat-file -e "${before}^{commit}" 2>/dev/null; then
      undecidable "push without a resolvable 'before' commit (before='${before}')"
    fi
    printf '%s\n' "$before"
    ;;
  pull_request)
    base_ref="${GITHUB_BASE_REF:-}"
    if [[ -z "$base_ref" ]]; then
      undecidable "pull_request without GITHUB_BASE_REF"
    fi
    if [[ -z "${GITHUB_SHA:-}" ]]; then
      undecidable "pull_request without GITHUB_SHA"
    fi
    if ! git fetch --no-tags origin "refs/heads/${base_ref}:refs/remotes/origin/${base_ref}" >&2; then
      undecidable "pull_request: cannot fetch origin/${base_ref}"
    fi
    merge_base="$(git merge-base "origin/${base_ref}" "$GITHUB_SHA" 2>/dev/null)" ||
      undecidable "pull_request: no merge base for origin/${base_ref} and ${GITHUB_SHA}"
    printf '%s\n' "$merge_base"
    ;;
  merge_group)
    base_sha="${GITHUB_MERGE_GROUP_BASE_SHA:-}"
    if [[ -z "$base_sha" ]]; then
      undecidable "merge_group without GITHUB_MERGE_GROUP_BASE_SHA"
    fi
    printf '%s\n' "$base_sha"
    ;;
  *)
    undecidable "unsupported event '${GITHUB_EVENT_NAME:-}'"
    ;;
esac
