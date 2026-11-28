# GitHub PR workflow

Pull requests for `inoio/agents-sandbox` live on GitHub; use the `gh` CLI (it infers the repo from
`git remote -v`). This covers the PR mechanics the issue-workflow guide does not: how a PR lands
through the merge queue, and the two side effects that commonly stall one.

## Merging through the queue

`main` carries a `merge_queue` rule and repo auto-merge is disabled, so `gh pr merge` is refused
("merge strategy is set by the merge queue"). To land a PR:

1. Wait for the six required checks: `lint`, `test`, `race`, `deadcode`, `changelog`, and
   `integration (docker build)`.
2. Enqueue it once they pass, through the REST async merge pinned to the head SHA (it fails until
   the required checks pass), or the maintainer's "Merge when ready" button:

   ```
   gh api -X PUT "repos/{owner}/{repo}/pulls/<n>/merge-async" -H "X-GitHub-Api-Version: 2026-03-10" \
     -f merge_action=merge_queue -f sha="$(gh pr view <n> --json headRefOid -q .headRefOid)"
   ```

   The GraphQL `enqueuePullRequest` mutation also works for a plain PR but is **refused for a PR that
   was stacked on another one**, which is why the script uses the REST call for every PR.
   `scripts/enqueue-pr.sh <n>` does the wait-and-enqueue in one call, and refuses (non-zero) a PR that
   has merge conflicts, since a conflicted PR runs no checks and the queue will eject it. It watches
   exactly the required checks (`gh pr checks --required`), so a red advisory check (`codecov/patch`,
   `mutation`, `hermetic`) does not hold an enqueue back — only a required failure or a conflict does.
3. The queue rebuilds the PR on the current `main` and re-runs the required checks there, so a PR
   that only passed against a stale base is rejected. `integration` is skipped on the queue ref (a
   skipped required check reports Success), so the queue does not pay the image build.

**Stacked PRs** land bottom-up, one at a time: `scripts/enqueue-pr.sh --stack <bottom> … <top>` retargets
each PR whose base is the previous PR's branch onto `main` once that one merged, enqueues it, and waits
until it merges, so a PR never enters the queue while the PR under it is still open. The list starts at
the stack's bottom PR. It refuses a PR based on an unmerged branch outside the list, and stops
when the queue drops a PR (three polls outside the queue). The repo does not delete merged branches,
so GitHub does not retarget a manually stacked PR by itself.

`gh pr checks <n> --watch` (and `gh run watch`) wait for results in one call — prefer them over a
`sleep`/`gh pr checks` poll loop.

## Native GitHub stacks

GitHub also models a stack itself: a PR carries a `stack` object (its number, size, position, and
`base.ref`), readable with `gh api repos/{owner}/{repo}/pulls/<n> --jq .stack` and listed with
`gh api repos/{owner}/{repo}/stacks`. The stack's **trunk** is the base branch of its bottom PR
(usually `main`), and every PR is evaluated — and the whole stack lands — against the trunk, not the
branch a mid-stack PR directly targets. A stack rooted on another branch merges into *that* branch, so
a stack whose bottom PR still targets an already-merged feature branch would land there rather than
`main`; retarget the bottom PR first.

A PR's base is **fixed while it is in a stack**: `gh pr edit <n> --base main` is refused ("Cannot
change the base branch because the pull request is part of a stack"), and the Stacks API has no
base-changing endpoint. To move the trunk, dissolve the stack and retarget the bottom PR:

```
gh api --method POST repos/{owner}/{repo}/stacks/<stack_number>/unstack   # lift the open PRs
gh pr edit <bottom> --base main
```

Then land the PRs bottom-up as a manual stack with `scripts/enqueue-pr.sh --stack <bottom> … <top>`.
The script refuses the bottom PR when it is based on a merged branch that is not `main` — its base is
neither `main` nor the previous PR's head — so retarget it before running the script. A native stack
left intact lands bottom-up the same way, and GitHub rebases the remaining PRs onto the trunk as each
one merges. (Deleting the stack's base branch also makes GitHub retarget the stack to the default
branch, but a merged branch is not deleted here by convention.)

Changing a PR's base — retargeting it, or dissolving a stack — fires `pull_request` `edited`, which
`ci.yml` does not subscribe to, so its checks do **not** re-run: a red check from before the change
persists until the branch is updated. `gh pr update-branch <n>` merges the current base into the
branch and re-reports the checks, and it also clears the stale-branch `codecov/patch` miss (codecov
measures the head merged with the base, so a branch behind `main` shows a spurious patch miss — see
`specs/testing/test-coverage.md`). `scripts/enqueue-pr.sh` retargets a base but does not update the
branch, so update each upper PR after the one below merges before enqueueing it.

The `changelog` slug gate diffs each PR against the branch it directly targets (the CI job passes
`GH_PR_BASE_REF`), so a mid-stack PR's diff no longer carries the lower PRs' fragments and their slugs
do not trip it. See `specs/ci/ci-pipeline.md`.

## Bot-authored PRs need a workflow approval

A PR opened by the release workflow's `publish` job (the Homebrew formula bump) is authored by
`GITHUB_TOKEN`, so GitHub parks its workflow run at `action_required`: no checks report until a
maintainer approves the run. It then merges through the queue like any other PR. A bot PR that shows
no checks is waiting here, not broken.

## Changelog fragments

A behavior change needs a `changelog.d/<PR-number>.<type>.md` fragment, `<type>` one of
`added`/`changed`/`deprecated`/`docs`/`fixed`; docs-only and test-only changes need none. The
filename's `<slug>` **must equal the pull-request number**, which CI enforces — so create the
fragment after the PR exists, or guess it as one past the highest PR/issue number:

```
gh pr list --state all --limit 1 --json number -q '.[0].number'
```

`make check-local-changes` runs the presence check (`changelog-presence`) locally before you push.
