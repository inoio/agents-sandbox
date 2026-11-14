---
description: Contract for the per-minor release branch `release/<major>.<minor>` — cut at the release commit, the branch the line's patch releases are tagged from, and where fixes for a released line land while `main` keeps new work; including that the branch runs the same CI as `main` and that a release tag is refused unless it is on `main` or a release branch.
read_if: Deciding where a release tag or a fix lands, cutting a patch release for a released line, backporting a fix, protecting a release branch, or changing the release branch name or flow.
---

# Release branching

## Contract

- `main` is the development line: new features and non-urgent fixes land there.
- Every **minor** (and **major**) release cuts a long-lived release branch
  `release/<major>.<minor>` (for example `release/0.6`) at the commit the release is tagged on.
- **Patch releases for a line are cut from that line's release branch.** The fix merges into
  `release/<major>.<minor>` and the tag `vX.Y.(Z+1)` lands on that branch's tip.
- A fix that also applies to `main` is forward-ported there. `main` is never reset to, or held at,
  a release branch.
- At a minor release the tag is reachable from both lines (the branch is created at the same
  commit); at a patch release it is on the release branch only.
- A `release/<major>.<minor>` branch runs the **same CI as `main`**: a push to it triggers
  `.github/workflows/ci.yml` and runs every required check (`lint`, `test`, `race`, `deadcode`,
  `changelog`) plus the Docker `integration` build, so a backport merged onto the line is verified
  at the merge rather than only when the line is tagged. The build is import-only — the
  cache-reading `integration` reader runs, the single cache writer stays on `main` — so a release
  push never overwrites `main`'s GHCR build cache.
- **Protecting the line is a manual admin step**: a `protect release/*` ruleset must require the
  same six branch checks `protect main` does (`lint`, `test`, `race`, `deadcode`, `changelog`, and
  the `integration (docker build)` context), without the merge queue. The workflow file cannot set
  it; until it exists, the branch runs CI but does not enforce it.
- **A release tag is refused unless its commit is on `main` or a release branch.** The `tag-guard`
  job fails the run (and `release` waits on it) when the tagged SHA is not an ancestor of `main`
  or of some `release/*`, so a tag can never publish a commit that bypassed the checks. Both
  lines are accepted because a patch release tags the release branch's tip, not `main`; a
  `main`-only rule would reject legitimate patch tags.

## Cutting a release

At a minor release the branch and the tag are created at the same commit — the merged `main` tip
that carries the release commit. Through `gh`:

```
repo=$(gh repo view --json nameWithOwner -q .nameWithOwner)
sha=$(gh api "repos/$repo/commits/main" --jq .sha)
gh api "repos/$repo/git/refs" -f ref="refs/heads/release/<major>.<minor>" -f sha="$sha"
gh api "repos/$repo/git/refs" -f ref="refs/tags/vX.Y.Z" -f sha="$sha"
```

A patch release tags the tip of the existing `release/<major>.<minor>` branch instead:

```
sha=$(gh api "repos/$repo/commits/release/<major>.<minor>" --jq .sha)
gh api "repos/$repo/git/refs" -f ref="refs/tags/vX.Y.Z" -f sha="$sha"
```

Either can be pushed from the host rather than through `gh`. CI then cross-compiles the targets,
publishes the GitHub Release, and opens the Homebrew bump PR; verify the release artifacts, the
bump PR, and the docs deploy.
