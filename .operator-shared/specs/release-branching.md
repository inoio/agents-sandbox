---
description: Contract for the per-minor release branch `release/<major>.<minor>` — cut at the release commit, the branch the line's patch releases are tagged from, and where fixes for a released line land while `main` keeps new work.
read_if: Deciding where a release tag or a fix lands, cutting a patch release for a released line, backporting a fix, or changing the release branch name or flow.
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
