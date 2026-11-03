# Dependabot

Keep dependabot PRs moving: merge the safe bumps and flag the rest for a human.
Discovery is read-only; merges happen only as approved [batch-plan](SKILL.md#batch-plan-protocol)
rows.

## Repo facts these flows assume

- Merge method is a **merge commit** (`allow_merge_commit` is on; squash and
  rebase are disabled) → `gh pr merge --merge`.
- `delete_branch_on_merge` is off → delete the branch explicitly with
  `--delete-branch`.
- Branch protection has **no required status checks and 0 required approvals**.
  "CI green" is a policy this skill verifies, not something GitHub enforces, so
  the gate must be checked by hand.
- Re-verify these settings if merges ever behave unexpectedly.

## Policy

Merge a dependabot PR only when **both** hold:

1. Its semver level is **patch** or **minor**.
2. Every check reported for the PR passes.

Never merge a **major** bump, a PR with failing or pending checks, a conflicted
PR, or a draft. A PR that bumps several packages at different levels takes the
**highest** level present.

Dependabot *security-update* PRs travel this same path and are subject to the
same policy.

## Discovery

```sh
gh pr list --state open --author app/dependabot \
  --json number,title,labels,isDraft,mergeStateStatus,headRefName
```

For each PR, include in the survey: ecosystem, level, and check status.

## Classify

- **Ecosystem** — from `headRefName` (`dependabot/go_modules/…` vs
  `dependabot/github_actions/…`) or the `go` / `github_actions` labels.
- **Level** — parse `Bump <pkg> from <A> to <B>` in the title/body and compare
  versions. Grouped PRs ("Bump the … group with N updates") list several bumps;
  use the highest level among them.

## Gate

- Checks: `gh pr checks <n> --json name,state,conclusion`. Merge only if every
  reported check has conclusion `success` (a `skipped` conditional check is
  acceptable). Any `failure` or pending state blocks the merge.
- Mergeability: require `mergeStateStatus` of `CLEAN`.
  - `BEHIND` → propose an update (below).
  - `DIRTY` (conflict) → flag; do not merge.
  - `BLOCKED` / `UNSTABLE` / `UNKNOWN` → flag; do not merge.
- Drafts are never merged.

## Propose

Into the batch plan:

- Safe → `merge #<n>: <level> <pkg> bump, checks green`.
- Major → `flag #<n>: major <pkg> bump — needs human`.
- Failing checks → `flag #<n>: checks failing (<names>)`.
- Conflicted → `flag #<n>: merge conflict`.
- `BEHIND` → `update #<n>: branch behind base` (see below).

## Execute on approval

1. Re-validate immediately before writing: re-run the checks query and confirm
   `mergeStateStatus` is still `CLEAN`. If either changed, skip the row and
   report it.
2. Merge:
   ```sh
   gh pr merge <n> --merge --delete-branch
   ```
3. For an approved `update` row:
   ```sh
   gh pr update-branch <n>
   ```
   then report that the branch was updated; it re-enters the next survey for a
   possible merge.
4. For an approved `flag` row, post a comment naming the blocker and make no
   state change.
