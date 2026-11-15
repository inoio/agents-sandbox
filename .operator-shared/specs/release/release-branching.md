---
description: Contract for the release model — `main` as the source of truth, fixes cherry-picked down (`git cherry-pick -x`) to the single maintained `release/<major>.<minor>` branch, the patch-id containment invariant checked by `git cherry` (not ancestry) before the micro's own changelog/tag commit, the micro/minor/major tag roles, the `needs-backport` marker, the `release` skill's `backport-audit` tag precondition, the nightly reconciliation workflow and its rolling `Backport debt` issue, and that a release tag is refused unless it is on `main` or a release branch.
read_if: Deciding where a release tag or a fix lands, cherry-picking a fix down to a release line, backporting a fix, the `needs-backport` marker, the backport audit or its nightly workflow, protecting a release branch, or changing the release branch name or flow.
---

# Release branching

## Contract

- **`main` is the source of truth.** New features and non-urgent fixes land there, and every fix that
  must reach a shipped line is **authored on `main` first**. Agents start from `main`; targeting a
  release branch is never their first move.
- **One maintained release branch.** Exactly one `release/<major>.<minor>` line is maintained at a
  time (for example `release/0.6`). Cutting a minor release advances the line to
  `release/<new minor>`; the previous line is dropped, not kept alive. A user pinned to a dropped
  line moves forward and gets no further fix.
- **Fixes flow down, never up.** A fix that affects the shipped line is authored on `main` and
  cherry-picked onto the current line with `git cherry-pick -x`. The branch therefore never holds a
  fix `main` lacks. The down-port unit is the PR's **constituent commits**, cherry-picked in order —
  not the merge commit (`-m 1` would collapse the PR into one commit whose patch-id matches no `main`
  commit and would defeat the invariant check).
- **The containment invariant is patch-id-based, not ancestry.** A cherry-picked commit is a new SHA,
  so `git merge-base --is-ancestor` is the wrong test and reports a false failure. The test is
  `git cherry main release/<X>` with **no `+` lines**: every branch commit must be patch-equivalent to
  a `main` commit.
- **The invariant is a precondition, not a post-hoc check.** It runs *before* the micro's own
  changelog/tag commit, so that one branch-local commit is never in scope — the branch may
  legitimately hold it. Everything else on the branch must be patch-equivalent to `main`.
- **A `+` line is a question, not a failure.** An adapted port (a conflict resolved differently)
  produces a different patch-id and legitimately shows as `+`; so does an urgent fix authored
  straight on the branch. The skill surfaces each `+` line for the maintainer to confirm "adapted,
  not missed" and proceeds. A fix authored directly on the branch is debt to clear by getting its
  patch onto `main`. The check is a **tripwire in the skill**, not a CI gate.
- **Tag roles.** A **micro** (patch) release is tagged on the current `release/<major>.<minor>`
  branch and contains only fixes cherry-picked from `main`. A **minor** release is tagged on `main`
  and advances the maintenance line to `release/<new minor>`. A **major** release is tagged on `main`
  with the same mechanics as a minor while major is `0`; it is called explicitly and never proposed.
- **Fix branches and the line's fragments.** A down-port carries the PR's `changelog.d/` fragment
  along with the fix (the fragment commit is part of the PR), and the micro's changelog assembly
  consumes it on the branch. `main` keeps its own copy until its next release, so a fix appears in
  both the micro's notes and the next minor's notes.
- **Push the branch before the tag.** `tag-guard` admits a tag only when the tagged commit is already
  reachable from `origin/main` or a pushed `origin/release/*`, so `release/<major>.<minor>` is created
  and pushed before its first tag. A tag whose commit is not on `main` or a release branch is refused,
  so a tag can never publish a commit that bypassed CI checks.

## The `needs-backport` marker

- `needs-backport` is the **only trigger** for a down-port: it marks a fix that affects a shipped line
  and must be cherry-picked onto the current release branch. A bug without it is intentionally
  `main`-only. It is a marker beside `upstream`, orthogonal to category, state, and priority.
- It is a **permanent record**, like `upstream` — it is not cleared when the port lands. Done-ness is
  decided by patch-id presence, so a stale label cannot mask an unported fix and a cleared-then-failed
  port cannot lose its record.
- `scripts/enqueue-pr.sh` copies the marker from the linked issue onto the PR at enqueue time, which
  is what makes the audit (below) see the fix.

## The release skill's backport checks

The `release` skill runs these as the **tag precondition** for a micro release, before authoring the
micro's changelog/tag commit:

- **`scripts/backport-audit.sh`** — the audit. It reports, and `scripts/open-backport-pr.sh <n>`
  creates a port (cherry-pick down, open the PR against the branch); the workflow runs both. The
  audit:
  - derives the **expected set** from merged `needs-backport` PRs on `main`, recomputed from scratch
    and independent of whether any creation step ran;
  - tests **equivalence by patch-id set containment**, not the `cherry-pick -x` trailer — the merge
    queue rebuilds a PR on `main`, so the SHAs `-x` recorded may not exist on `main`, while a
    patch-id is content-based and survives the rebase;
  - controls **noise** by skipping a PR merged within a 2-hour grace period and skipping one whose
    commit is already covered by an open backport PR targeting the line;
  - exits `1` when a labeled fix is missing from the latest line. An unported `needs-backport` fix
    **blocks the tag** unless the maintainer overrides — this is what makes "you cannot ship a micro
    with an unported fix" true by construction. An adapted port is a different patch-id and shows as
    a false positive, advisory for a human to confirm.
- **`git cherry main release/<X>`** — the wrong-direction tripwire described above, asserting no `+`
  lines.

The latest line is the highest-semver `release/<major>.<minor>` among local and origin refs; with no
release branch the audit reports nothing to audit and exits clean.

## Nightly reconciliation

`.github/workflows/backport-audit.yml` is a **non-gating nightly** (a schedule and a manual dispatch,
never a PR check and never a member of `release.needs`). It runs the same audit off `main`, opens the
missing backport PRs, and keeps **one rolling, idempotent `Backport debt` issue** current — updated in
place and closed when the debt clears, rather than failing a scheduled build. Git's `git cherry`
cannot see a missed port (an absent commit is simply absent), so correctness rests on this reverse
query: enumerate what should be on the branch and verify each is.

## CI and protection

- A `release/*` push runs the **same CI as `main`**: every required check (`lint`, `test`, `race`,
  `deadcode`, `changelog`) plus the Docker `integration` build when the push is code-affecting, so a
  backport merged onto the line is verified at the merge rather than only at the tag. The build is
  import-only — the cache-reading `integration` reader runs, the single cache writer stays on `main`.
- **Protecting the line is a manual admin step**: a `protect release/*` ruleset must require the same
  six branch checks `protect main` does, without the merge queue. Until it exists, the branch runs CI
  but does not enforce it.

## Cutting a release

A minor release creates the branch and tags `main` at the same commit — the merged `main` tip that
carries the release commit:

```
repo=$(gh repo view --json nameWithOwner -q .nameWithOwner)
sha=$(gh api "repos/$repo/commits/main" --jq .sha)
gh api "repos/$repo/git/refs" -f ref="refs/heads/release/<major>.<minor>" -f sha="$sha"
gh api "repos/$repo/git/refs" -f ref="refs/tags/vX.Y.Z" -f sha="$sha"
```

A micro release tags the tip of the current `release/<major>.<minor>` branch instead:

```
sha=$(gh api "repos/$repo/commits/release/<major>.<minor>" --jq .sha)
gh api "repos/$repo/git/refs" -f ref="refs/tags/vX.Y.Z" -f sha="$sha"
```

Dropping the previous line is **offered, not automatic**: the `release` skill flags
`release/<old>` for deletion in its sign-off gate after the new line's tag is verified, and the
maintainer confirms. Either push can be done from the host rather than through `gh`. CI then
cross-compiles the targets, publishes the GitHub Release, opens the Homebrew bump PR, and publishes
the docs; verify the release artifacts, the bump PR, and the docs deploy.
