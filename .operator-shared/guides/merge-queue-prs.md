# Merging PRs through the merge queue

`main` takes no direct merge: the `protect main` ruleset requires a merge queue (`ci-job-gating.md`). This is how to
drive it from the CLI.

## Enqueue with the GraphQL mutation, not `gh pr merge`

The repository has `allow_auto_merge` disabled and only the merge-commit method enabled, so `gh pr merge <n>` fails with
`Auto merge is not allowed for this repository (enablePullRequestAutoMerge)` instead of enqueuing. Enqueue directly:

```
id=$(gh api graphql -f query='query { repository(owner:"inoio",name:"agents-sandbox"){ pullRequest(number:<n>){ id } } }' --jq .data.repository.pullRequest.id)
gh api graphql -f query="mutation { enqueuePullRequest(input:{pullRequestId:\"$id\"}) { mergeQueueEntry { state position } } }"
```

An entry may be enqueued while its checks are still pending; the queue then holds it in `AWAITING_CHECKS` and ejects it
if the combined build fails. `gh api graphql` on `mergeQueue(branch:"main")` lists the entries and their states.

## Branch updates and CHANGELOG conflicts

`strict_required_status_checks_policy` is false, so a branch that is behind `main` still enqueues; the queue re-runs the
four required checks on the combined ref.

Every behavior change adds a bullet to `CHANGELOG.md`'s `[Unreleased]` section, so PRs whose bullets share an anchor
conflict when the queue builds them on top of one another — in practice `CHANGELOG.md` is the only file that conflicts.
Either enqueue only non-conflicting PRs together (the queue builds up to five on one ref), or merge `origin/main` into
each later branch, resolve the `CHANGELOG.md` conflict, and push before enqueuing.

A stacked PR (base = another PR's branch) cannot be enqueued until it is retargeted with `gh pr edit <n> --base main`
after its base merges. `main` is reachable only through the queue and branches are not deleted on merge, so the base
branch survives and the retarget is always manual.

## Same-anchor and semantic conflicts: chain the branches

`CHANGELOG.md` is not the only conflict. Two PRs that each add an `[Unreleased]` bullet at the *same anchor*
collide even when they touch no other file, and a shared code region can conflict with **no textual overlap** when
one PR's test encodes behaviour a sibling changed: #429 made `skipVM` create neither a home volume nor state, which
broke #431's race tests that had used `skipVM=true` to avoid the prefill VM while still creating a volume. Preview
both kinds without burning a queue cycle with `git merge-tree --write-tree <base> <branch>`.

When the entries conflict with each other, do not enqueue them as a group. **Chain** them instead: merge each
predecessor's pushed branch (`git merge origin/<predecessor-branch>`) into the next, resolve the conflict, and push
— the predecessor's commits are then already present, so the queue's sequential merge of the chained branch onto
`main` is clean. Enqueue chained branches **serially** (wait for each to merge before enqueuing the next); a
combined group build is not guaranteed to succeed. Resolve a code conflict by combining both behaviours and
adapting the older PR's tests to the newer contract, never by reverting one side's fix.

## A required check that predates the PR head

Actions resolves the workflow from the **PR's branch**, not from `main`. If a required job is added to `ci.yml` after
a branch is cut, that branch has no such job, so the queue rejects the enqueue with
`Required status check "<job>" is expected.` — the check has never reported for that head.

`gh run rerun` and a close/reopen reuse the run's original workflow definition and still omit the job (verified for the
`changelog` job). Only a **new commit** on the branch works: merge `origin/main` in (also the pre-enqueue step above) and
push. Prefer retarget → merge `main` → push in one step so the PR diff greptile sees is the PR's own changes, not
`main`'s.

## `make check` formats in place

`make check` runs `make fmt` and then lints the **formatted working tree**, but does not stage the formatter's output.
A local `make check` therefore passes while the pushed commit still holds the unwrapped line, and CI `lint` fails
(`golines`). Commit the formatter's changes (or re-run `make lint` after a clean `git status`) before pushing.
