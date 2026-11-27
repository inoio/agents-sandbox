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
so GitHub does not retarget a stacked PR by itself.

`gh pr checks <n> --watch` (and `gh run watch`) wait for results in one call — prefer them over a
`sleep`/`gh pr checks` poll loop.

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
