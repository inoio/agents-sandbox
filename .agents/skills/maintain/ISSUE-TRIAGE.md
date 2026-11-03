# Issue Maintenance

Move issues through the project's state machine: categorise, set priority,
verify, grill if needed, and apply the outcome. Non-dependabot PRs are not a
maintenance target here (see [SKILL.md](SKILL.md)); dependabot PRs are handled in
[DEPENDABOT.md](DEPENDABOT.md).

Actions are proposed in the [batch plan](SKILL.md#batch-plan-protocol) and
executed only on approval. Discovery and recommendation are read-only.

## Discovery

Present three buckets, oldest first:

1. **Unlabeled**: never triaged.
2. **`needs-triage`**: evaluation in progress.
3. **`needs-info` with reporter activity since the last notes**: needs
   re-evaluation.

Show counts and a one-line summary per item, each with a proposed
category/state/priority, plus the `upstream` marker when the request depends on
an external project. Let the maintainer pick.

## Maintain a specific issue

1. **Gather context.** Read the full issue (body, comments, labels, author,
   dates). Parse any prior notes so you don't re-ask resolved questions.
   Explore the codebase using the project's domain glossary, respecting ADRs in
   the area. Run two checks: (a) **redundancy**: search for an existing
   implementation by domain concept (not just the request's wording) and report
   where you looked — if found, it's an already-implemented `wontfix`
   (step 5); (b) **prior rejection**: read `.out-of-scope/*.md` and surface any
   that resembles this request.

2. **Recommend.** State your category, state, and priority recommendation with
   reasoning, plus the `upstream` marker when the work depends on an external
   project (microsandbox, opencode, ...), and a brief codebase summary relevant
   to the request (including whether it's already implemented). Wait for
   direction.

3. **Verify the claim.** Before any grilling, check that the claim holds up. For
   a bug, reproduce it from the reporter's steps. For an enhancement, confirm
   the gap exists. Report what happened: confirmed (with code path), failed, or
   insufficient detail (a strong `needs-info` signal). A confirmed verification
   makes a much stronger agent brief.

4. **Grill (if needed).** If the request needs fleshing out, call the Skill tool
   twice, for "grilling" and "domain-modeling", and grill it into shape a round
   of questions at a time, sharpening domain terms and updating
   `GLOSSARY.md`/ADRs inline as decisions land.

5. **Apply the outcome** (as an approved plan row):
   - `ready-for-agent`: post an agent brief comment ([AGENT-BRIEF.md](AGENT-BRIEF.md)).
   - `ready-for-human`: same structure as an agent brief, but note why it can't
     be delegated (judgment calls, external access, design decisions, manual
     testing).
   - `needs-info`: post maintenance notes (template below).
   - For `wontfix`, close the issue, with the comment depending on *why*:
     - **Already implemented**: the change already exists. Point to where it
       lives; do **not** write to `.out-of-scope/` (that KB is for *rejected*
       requests, not built ones).
     - **Rejected (bug)**: give a polite explanation, then close.
     - **Rejected (enhancement)**: write to `.out-of-scope/`, link to it from a
       comment, then close ([OUT-OF-SCOPE.md](OUT-OF-SCOPE.md)).
   - `needs-triage`: apply the role. Optional comment if there's partial
     progress.

## Priority

Set priority per [PRIORITIES.md](PRIORITIES.md). Propose it alongside category
and state; apply only on approval; leave it unset when there is no committed
urgency.

## Quick state override

If the maintainer says "move #42 to ready-for-agent", trust them and apply the
role directly (still as a confirmed plan row). Skip grilling. If moving to
`ready-for-agent` without a grilling session, ask whether they want to write an
agent brief.

## Needs-info template

```markdown
## Maintenance Notes

**What we've established so far:**

- point 1
- point 2

**What we still need from you (@reporter):**

- question 1
- question 2
```

Capture everything resolved during grilling under "established so far" so the
work isn't lost. Questions must be specific and actionable, not "please provide
more info".

## Resuming a previous session

If prior notes exist on the issue, read them, check whether the reporter has
answered any outstanding questions, and present an updated picture before
continuing. Don't re-ask resolved questions.
