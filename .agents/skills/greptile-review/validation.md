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

## Negatives (must yield zero findings)

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

_Not yet run._

## Re-mining

To refresh the corpus (new PRs, a ruleset re-tune):

```bash
bash .agents/skills/greptile-review/mine.sh > .agents/skills/greptile-review/corpus.tsv
```

The endpoint yields 377 findings across 161 PRs, `p1`×140 / `p2`×237. When the counts move,
re-check whether a new family has appeared before extending `RULES.md`.
