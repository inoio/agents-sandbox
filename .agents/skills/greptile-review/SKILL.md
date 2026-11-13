---
name: greptile-review
description: Use when reviewing a diff — a PR or a local branch — for the correctness defects Greptile flags: cross-component breakage, invariant bypasses, tests that do not assert, and docs that overstate. Run it before a PR reaches Greptile, or as a Greptile replacement.
disable-model-invocation: true
---

# Greptile review

Review one diff the way Greptile does: for correctness defects at a distance — the change breaks
behaviour in code it does not appear to touch. This is the third axis beside the `code-review`
skill's Standards and Spec: file layout, naming, and spec conformance are out of scope here.

The output is a report a maintainer reads, not a posted review. Nothing reaches GitHub.

## Reference docs

- [RULES.md](RULES.md): the fourteen correctness rule families, each a probe with a spec anchor.
- [validation.md](validation.md): the frozen RED/GREEN sample and how to re-mine the corpus.
- [mine.sh](mine.sh): refreshes `corpus.tsv` from the PR history.

## Process

### 1. Pin the fixed point

The argument is a PR number or a git ref:

- a number — `gh pr diff <n>`; use `gh pr view <n> --json baseRefName,headRefOid` if you need the
  base to compute a merge-base locally.
- anything else — `git diff $(git merge-base <ref> HEAD)...HEAD`.

Confirm the ref resolves and the diff is non-empty before dispatching. A bad ref or empty diff
fails here, not inside the reviewer.

### 2. Load the touched specs

Map the changed paths to the specs under `.operator-shared/specs/**` they belong to (the rule
anchors in RULES.md name them). The reviewer reads those specs; they are the authority for what the
code must do, and a finding that contradicts a spec or leaves one unenforced is a finding.

### 3. Dispatch one reviewer

Dispatch a single `reviewer-large` subagent. Do not pick a model. Give it the diff command, the
ruleset path, and this brief:

> Review the change `<diff command>` against the rule families in
> `.agents/skills/greptile-review/RULES.md`. For each changed hunk, ask every probe of the value it
> changes and of the code that consumes that value; read the specs the rules point at. Work from
> the code — ignore any existing review comments or PR discussion.
>
> Report only correctness defects that should block merge, most severe first. Each finding: a
> title; the concrete triggering input or scenario; the two or three code paths you traced, named;
> a severity (P1/P2); and an optional suggested fix. Stay silent if there is nothing.

### 4. Report

Shape the report like Greptile's, ordered P1 first:

- one verdict line naming the change and whether anything blocks merge;
- a findings list — `[P1|P2] title` and one line each;
- then, per finding, the title, the body (trigger → wrong behaviour), severity, and the evidence
  note.

Keep the reviewer's findings intact — do not rerank across families or merge similar ones. If the
reviewer found nothing, say so; an empty report on a clean diff is correct, not a failure to look.

## Severity

- **P1** — a real input produces a wrong or unsafe result: data loss, a security or invariant
  bypass, a broken build or run, or a required check that no longer gates.
- **P2** — weaker correctness, an edge case, a test that under-asserts, or a doc that misstates.
- No P0.

## Evidence protocol

Every finding carries a concrete trigger input, the named paths traced, and a severity. A finding
that asserts a compile failure or observable behaviour without having run it must say so and soften
to "appears to". Never post an unverified compile claim as fact: three Greptile false positives
(PR 476, 612, 624) were one such claim about Go 1.27 promoted fields.
