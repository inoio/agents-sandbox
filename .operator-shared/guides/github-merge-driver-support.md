# Does GitHub honor `CHANGELOG.md merge=union`?

## Verdict

**No, not on either path.** GitHub's server-side merge ignores user-defined `.gitattributes` merge
attributes, so `merge=union` does **not** resolve a same-anchor `CHANGELOG.md` conflict for
**(a)** an ordinary PR merge performed by GitHub, nor **(b)** a merge queue (`merge_group`) build.
On the merge queue the conflict instead ejects the PR: GitHub's own docs say a PR whose group build
"has conflicts with the base branch ... will be removed from the queue." Confidence: **high** for
(a) (GitHub Support states it outright; a reproducible live test agrees; the feature request is still
open in Oct 2026). **High-to-medium** for (b) (same merge engine, and the queue's documented
conflict→ejection behavior; no source singles out the queue).

Practical consequence: keep using fragment files (or the existing chain-the-branches workaround in
`merge-queue-prs.md`) — `merge=union` buys nothing server-side, however cleanly it merges with the
CLI git and in local clones.

## What is documented, vs. inferred

- **Documented / stated by a source:** git defines `union` as a built-in driver; libgit2 implements
  it natively; GitHub Support says GitHub does not consider user `.gitattributes`; GitHub's docs say
  conflicts block a PR merge and eject a queued PR; GitLab's Gitaly issue shows a server-side merge
  dropping `merge=union`.
- **Inference:** that the merge queue specifically uses the same attribute-blind merge engine as the
  rest of GitHub, and therefore ejects rather than auto-resolving. GitHub has no doc that says either
  "the merge queue honors custom merge drivers" or "the merge queue ignores them." The inference rests
  on the queue being GitHub's own server-side merge plus the documented conflict→ejection rule.
- **No source at all:** GitHub does not publish which merge engine (libgit2 vs. an internal
  reimplementation) performs server-side merges today, nor a list of supported merge drivers. This
  gap is why the evidence below is support statements and reproducible reports rather than a spec.

## 1. `union` is a git built-in driver (settled)

`gitattributes(5)` lists three built-in low-level merge drivers selectable via the `merge` attribute —
`text`, `binary`, and `union`:

> **union:** Run 3-way file level merge for text files, but take lines from both versions, instead of
> leaving conflict markers. This tends to leave the added lines in the resulting file in random order
> and the user should verify the result.

Source: <https://git-scm.com/docs/gitattributes#_performing_a_three_way_merge> (raw:
<https://github.com/git/git/blob/master/Documentation/gitattributes.adoc>).

This is why the CLI git resolves the conflict cleanly. It says nothing about any other implementation.

## 2. libgit2 implements `union` natively — which is *not* the blocker people assume

libgit2 registers `text`, `union`, and `binary` as built-ins and reads the `merge` attribute to select
one; `merge=union` maps to `git_merge_driver__union` with `GIT_MERGE_FILE_FAVOR_UNION`:

- <https://github.com/libgit2/libgit2/blob/main/src/libgit2/merge_driver.c> (`merge_driver_name__union`,
  `merge_driver_registry_insert(... union ...)`, `merge_driver_name_for_path`).

`merge=union` support landed in libgit2 in **PR #3564** ("Custom merge drivers and proper gitattributes
`merge` handling", merged 2016-03-17): <https://github.com/libgit2/libgit2/pull/3564>.

Two limitations worth recording:

- libgit2 supports the **built-in** drivers and drivers a *consumer* registers via
  `git_merge_driver_register`. It does **not** execute external commands configured through
  `merge.<name>.driver` in git config — merging code only sees a driver name, and an unregistered
  name falls back to the `*` driver, else fails. Source: same file (`merge_driver_lookup_with_wildcard`,
  `merge_driver_registry_find`), and the PR thread: "libgit2 does not execute programs."
  <https://github.com/libgit2/libgit2/pull/3564#issuecomment-170754752>
- **libgit2 having `union` did not change GitHub.** In that same PR thread a maintainer (Edward
  Thomson, then at GitHub) answered the question directly:

  > Will this eventually change the behavior of merge conflict detection on Github pull requests? …
  >
  > **This will not change the behavior of GitHub PR merges directly, no. If GitHub wants to change
  > behavior, that's not something that libgit2 would impose upon them.**

  <https://github.com/libgit2/libgit2/pull/3564#issuecomment-217581428>

So the common inference "GitHub uses libgit2, libgit2 supports union, therefore GitHub should honor it"
is wrong: GitHub's merge path does not delegate attribute-aware merging to libgit2.

## 3. GitHub does not consult a user-defined `.gitattributes` for merges

**GitHub Support, stated outright** (2017, quoted verbatim in the keep-a-changelog thread):

> I'm afraid what you're seeing is currently expected behavior. **GitHub doesn't consider user-defined
> .gitattributes files** (normally, we use our own .gitattrbutes file which you can't change). … The
> only workaround I could suggest is to merge pull requests in your local clone (and not via the web UI)…

Source: <https://github.com/olivierlacan/keep-a-changelog/issues/56#issuecomment-350291283>.

**Reproducible live test** (2021): a user created two PRs that only collide on a `merge=union` changelog
and found GitHub still reported a conflict — "as of 7 Feb 2021 the answer is still no":
<https://github.com/olivierlacan/keep-a-changelog/discussions/478#discussioncomment-5234841>, test PRs
<https://github.com/moreati/changelog-merge-test/pull/1> and
<https://github.com/moreati/changelog-merge-test/pull/2>.

**Still open and unanswered in Oct 2026**: the GitHub Community feature request "Pull request conflicts:
Support `merge=union` in `.gitattributes` file" (opened Dec 2021) remains *Unanswered*, with complaints
through 2024 ("We can avoid the conflicts by doing client-side branch merging (as that respects
`.gitattributes` merge rules)") and a final comment on 2026-10-02: "Still waiting for this 5 years
later…." <https://github.com/orgs/community/discussions/9288>. The predecessor issue is
<https://github.com/isaacs/github/issues/487> (open since 2015).

There is **no** GitHub changelog entry or doc announcing support, and `gitattributes` appears in GitHub's
docs only for line endings, `linguist-generated`, and Git LFS — never for merge drivers.

## 4. GitHub's documented conflict model: block or eject, never auto-resolve

GitHub's merge-conflict doc describes only manual/branch resolution and never mentions merge drivers:

> Merge conflicts block merging because Git cannot safely choose which version of the conflicting
> content to keep. You must resolve all merge conflicts before you can merge a pull request on GitHub.

<https://docs.github.com/en/pull-requests/collaborating-with-pull-requests/addressing-merge-conflicts/about-merge-conflicts>

For the **merge queue**, GitHub's own doc makes the conflict outcome explicit:

> After grouping a pull request with the latest version of the target branch and changes ahead of it
> in the queue, **if there are failed required status checks or conflicts with the base branch, the
> pull request will be removed from the queue.**

<https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/configuring-pull-request-merges/managing-a-merge-queue>

The removal reasons list adds "Branch protection failure that could not automatically be resolved."
The queue builds temporary `gh-readonly-queue/<base>` refs holding the base plus the queued PRs; a
content conflict there is not resolved by the queue — it ejects. That is the behavior to expect for a
same-anchor `CHANGELOG.md` collision, because §3 means the `merge=union` attribute is not consulted
while building that ref.

## 5. Contrast: GitLab *did* honor it, then stopped

GitLab historically resolved `merge=union` (they wired libgit2/rugged's support), which is why so many
guides recommend the trick. GitLab **stopped honoring it in 17.1** (2024), tracked in Gitaly:

> Summary: GitLab has stopped honouring the `merge=union` attribute in the `.gitattributes` file. …
> GitLab ignores the `merge` attribute.

<https://gitlab.com/gitlab-org/gitaly/-/issues/6193>

This does not speak to GitHub directly, but it shows that "some server-side implementation once honored
`merge=union`" is not durable and must not be assumed — the platform's merge engine, not git's, decides.

## Recommendation

Do not rely on `merge=union` for `CHANGELOG.md` on GitHub. Prefer **per-PR fragment files** (one file per
change, assembled at release), which cannot collide by construction. If keeping the single-file changelog,
the existing workaround stands: enqueue only mutually non-conflicting PRs, or chain branches
(`git merge origin/<predecessor>` per `merge-queue-prs.md`) so the queue's sequential merge is clean.

## Sources

| Claim | Source |
| --- | --- |
| `union` is a git built-in merge driver | <https://git-scm.com/docs/gitattributes#_performing_a_three_way_merge> |
| libgit2 implements `union` built-in | <https://github.com/libgit2/libgit2/blob/main/src/libgit2/merge_driver.c> |
| libgit2 `union` support added 2016 | <https://github.com/libgit2/libgit2/pull/3564> |
| libgit2 change does not affect GitHub PR merges | <https://github.com/libgit2/libgit2/pull/3564#issuecomment-217581428> |
| libgit2 does not execute external merge drivers | <https://github.com/libgit2/libgit2/pull/3564#issuecomment-170754752> |
| GitHub Support: user `.gitattributes` not considered | <https://github.com/olivierlacan/keep-a-changelog/issues/56#issuecomment-350291283> |
| Reproducible live test (2021) | <https://github.com/olivierlacan/keep-a-changelog/discussions/478#discussioncomment-5234841> |
| Feature request still open (Oct 2026) | <https://github.com/orgs/community/discussions/9288> |
| Conflicts block a PR merge (no driver mention) | <https://docs.github.com/en/pull-requests/collaborating-with-pull-requests/addressing-merge-conflicts/about-merge-conflicts> |
| Merge queue ejects on conflict | <https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/configuring-pull-request-merges/managing-a-merge-queue> |
| GitLab honored, then dropped it (17.1) | <https://gitlab.com/gitlab-org/gitaly/-/issues/6193> |
