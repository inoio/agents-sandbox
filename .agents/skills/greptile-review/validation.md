# greptile-review validation

The fixed sample every RED/GREEN run uses, the findings each positive must reach, and the
records. The sample is frozen: swap a positive only if RED shows the baseline already catches it
(and record why).

Review command for a positive: `git diff <base>...<review-commit>` — the PR as Greptile saw it,
so the findings on that commit are present and unfixed. For a negative: `git diff <base>...<head>`.

## Positives (a finding-bearing PR at its pre-fix review commit)

| PR | Family | base | review commit | Findings to reach (severity) |
|---|---|---|---|---|
| 604 | Invariant bypass (security) | `73caf138` | `adc46bd5` | Large files skip approval (P1); Reused files lose content checks (P1); Approval hides changed commands (P2); Different bytes share approval (P2) |
| 590 | Test guard escapes | `b915968a` | `d1daad67` | Raw strings escape the guard (P2); Inherited git settings remain active (P2) |
| 618 | Shell portability / CI gating | `f5a6d084` | `d5740b4a` | Another checkout's rebase is missed (P1); macOS checks stop at mapfile (P1); Rebase target changes mid-report (P2) |
| 650 | Resource bounds | `86fc9890` | `536fc26a` | Build output grows without bounds (P1) |

PR604 carries two more P1 findings on other review commits (`Key case bypasses approval` on
`192c5448`, `Unapproved files exhaust host memory` on `963cd749`); each row lists only the findings
on its pinned commit. Findings from a PR's other commits are **off-sample** and are labelled so in
the GREEN table.

## Negatives (Greptile-silent — not known-clean)

These PRs got no Greptile finding, which means Greptile did not flag them, not that they are
clean. A finding on one of them is not automatically a false positive; judge it on the code.

| PR | Kind | base | head |
|---|---|---|---|
| 648 | docs-only | `a173df86` | `1bbb2bee` |
| 641 | docs-only | `d12791c7` | `088eca95` |
| 635 | test-only | `d12791c7` | `3e743219` |
| 643 | chore (deps) | `0148eabf` | `d5b98ee5` |
| 476 | false-positive class | `bb312137` | `3d5b4f20` |
| 612 | false-positive class | `7b99c814` | `1a1d40e2` |
| 624 | false-positive class | `a9264ab9` | `fbbaf3cc` |

476, 612 and 624 all contain the code Greptile wrongly flagged as "a promoted field as a
composite-literal key does not compile". Zero findings on them, and specifically no compile claim.

## RED record

Four fresh reviewers ran the diff with no ruleset, in detached worktrees of the pre-fix commits
(no answer key, no `gh`, no network). Result: the baseline **fails** — it misses findings in every
family, including two P1s.

| PR | Target findings | Caught | Missed |
|---|---|---|---|
| 604 | Large files skip approval (P1); Reused files lose content checks (P1); Approval hides changed commands (P2); Different bytes share approval (P2) | Large files skip approval (P1) | the other three |
| 590 | Raw strings escape the guard (P2); Inherited git settings remain active (P2) | none | both (called the isolation correct) |
| 618 | Another checkout's rebase is missed (P1); macOS checks stop at mapfile (P1); Rebase target changes mid-report (P2) | Rebase target changes mid-report (P2) | both P1s |
| 650 | Build output grows without bounds (P1) | the P1 | — |

A first RED run was discarded: its reviewers read the PRs' Greptile comments (via `gh`) and a
concurrent session's activity polluted the check. The rerun is the record. Novel findings the
baseline raised beyond Greptile's set are held separately for triage, not in this file.

## GREEN record

The four reviewers ran the same diffs with the ruleset (served from a neutral path). Recall
improved sharply on the families the baseline missed:

| PR | Target finding | Severity | On/off sample | Result |
|---|---|---|---|---|
| 604 | Large files skip approval | P1 | pinned | found (as an oversized input dropped from the projection) |
| 604 | Reused files lose content checks | P1 | pinned | **missed — the known gap** |
| 604 | Approval hides changed commands (P2), Different bytes share approval (P2) | P2 | pinned | missed |
| 604 | Unapproved files exhaust host memory | P1 | off-sample | found (unbounded / non-regular read in `addTree`) |
| 590 | Raw strings escape the guard | P2 | pinned | found |
| 590 | Inherited git settings remain active | P2 | pinned | missed |
| 618 | Another checkout's rebase is missed | P1 | pinned | found |
| 618 | macOS checks stop at mapfile | P1 | pinned | found |
| 618 | Release cleanup fails CI | P1 | off-sample | found |
| 618 | Rebase target changes mid-report | P2 | pinned | found |
| 650 | Build output grows without bounds | P1 | pinned | found |
| 650 | Download errors wrongly blame DNS | P2 | off-sample | found |

The baseline caught one of PR604's four pinned findings and none of PR590's. GREEN caught PR650's
only target, all three of PR618's pinned findings, and PR590's raw-string P2, plus one of PR604's
pinned four — `Large files skip approval` — while missing the pinned P1 `Reused files lose content
checks` and both pinned P2s. It also reached the off-sample `Unapproved files exhaust host memory`
on PR604 and `Release cleanup fails CI` on PR618. Off-sample rows are real Greptile findings on the
PR's other review commits, not in that PR's frozen row.

The miss is a dedupe: the collector keys an input by path *and* kind, but the verbatim-content check
is recorded in a path-keyed map, so a path used as both an `env`/`secret` source and a `home:`
source keeps only one entry and loses the content check. Its improvement is a probe for a value
that appears in more than one role.

**False-positive class (476, 612, 624): zero findings, no compile claim.** The evidence protocol
held — none of the three reproduced the promoted-field "does not compile" claim.

**Anti-fabrication evidence is narrower than "clean diffs get zero findings".** The Greptile-silent
PRs (641, 635, 643) are not clean — the reviewers found genuine issues on them — so the
over-suppression direction (silence on a diff that regresses) is not exercised by this sample; only
the false-positive class is.

**Negatives.** 641, 635, 643 returned P2 findings that read as genuine (an `asb` binary in
`docs/recipes/git-access.md` that does not exist; the cache-hit report counting the daemon's own
layer cache; the `msb-trust` pin left at v0.7.13 while `go.mod` moved to v0.7.14). Not treated as
fabrication. **648 is discarded**: the reviewer read this file and `corpus.tsv` through the shared
`.git` (`git show feat/greptile-review-skill:...`), so its clean result is not evidence.

**Harness lesson.** A review run must not share a `.git` that exposes the answer-key branch.
Extract the commit with `git archive <commit> | tar -x -C <dir>` so there is no `.git` to walk.

**Bar.** Not fully met: one sample P1 (PR604, reused files) is missed. Shipped as the known gap —
the improvement target — rather than held back.

## Re-mining

To refresh the corpus (new PRs, a ruleset re-tune):

```bash
bash .agents/skills/greptile-review/mine.sh
```

`mine.sh` writes `corpus.tsv` beside itself and replaces it only once the fetch succeeds, so a
failed run leaves the committed corpus intact. Pass a second argument to write elsewhere.

The endpoint yields 377 findings across 161 PRs, `p1`×140 / `p2`×237. When the counts move,
re-check whether a new family has appeared before extending `RULES.md`.
