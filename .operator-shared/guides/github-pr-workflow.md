# GitHub PR workflow

Pull requests for `inoio/agents-sandbox` live on GitHub; use the `gh` CLI (it infers the repo from
`git remote -v`). This covers the PR mechanics the issue-workflow guide does not: how a PR lands
through the merge queue, and the two side effects that commonly stall one.

## Merging through the queue

`main` carries a `merge_queue` rule and repo auto-merge is disabled, so `gh pr merge` is refused
("merge strategy is set by the merge queue"). To land a PR:

1. Wait for the six required checks: `lint`, `test`, `race`, `deadcode`, `changelog`, and
   `integration (docker build)`.
2. Enqueue it once they pass — the `enqueuePullRequest` GraphQL mutation (it fails until the
   required checks pass), or the maintainer's "Merge when ready" button:

   ```
   id=$(gh pr view <n> --json id -q .id)
   gh api graphql -f query='mutation($id:ID!){ enqueuePullRequest(input:{pullRequestId:$id}){ mergeQueueEntry { position } } }' -f id="$id"
   ```

   `scripts/enqueue-pr.sh <n>` does the wait-and-enqueue in one call.
3. The queue rebuilds the PR on the current `main` and re-runs the required checks there, so a PR
   that only passed against a stale base is rejected. `integration` is skipped on the queue ref (a
   skipped required check reports Success), so the queue does not pay the image build.

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
