---
name: handle-pr-feedback
description: Work on a set of PRs, improve their mergeability as far as possible without decisions, raise decisions necessary.
---

# Handle PR feedback

The goal is to handle CI checks and quality feedback on PRs and getting them mergeable.

## Procedure

Make sure a set of PRs has been identified. For all of them, check their merge status, especially:

* ci red/green
* mutation survivors comment (result of mutation CI job)
* leftovers comment (left by implementer)
* greptile comments
* up-to-dateness towards origin/main

Wait for the checks to settle with `gh pr checks <PR> --watch` (it blocks until every check reports and prints the final table). Don't poll `gh pr checks` behind `sleep`s: it burns turns and can read a still-pending state.

Per PR, task a subagent with fixing these issues. If any decision is needed or appropriate, it shall raise that decision, which you then raise to the user for decision. Mutation runs are expensive, therefore mutations shall be handled by reports without running mutation tests. Kill validation is left to CI. Per PR/subagent, review-fix-loop until done (retasking implementer, fresh reviewer subagent every review round), then push.