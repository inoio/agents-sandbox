# The `greptile-review` skill

`.agents/skills/greptile-review/` is a maintainer-run review for the correctness defects Greptile
flags: a value that breaks behaviour in code the diff does not touch. It is the third axis beside
the `code-review` skill's Standards and Spec — it ignores style, tooling-enforced concerns, and
spec conformance. Run it before a PR reaches Greptile, or as a Greptile replacement. It prints a
report; nothing reaches GitHub.

The ruleset is `.agents/skills/greptile-review/RULES.md`: fourteen invariant families, each a probe
with the `.operator-shared/specs/**` anchor it argues from. One `reviewer-large` applies them.

The ruleset is derived from a mined corpus: `corpus.tsv` is every Greptile inline finding on the
project's PR history (`PR`, `path`, severity, title), refreshed by `bash
.agents/skills/greptile-review/mine.sh`. When a new family appears, extend `RULES.md` rather than
one-off rules.

`validation.md` holds the frozen sample and the RED/GREEN records: four finding-bearing PRs, the
Greptile-silent PRs, and the three false positives (476, 612, 624 — the promoted-field "does not
compile" claim). The one known gap is PR604's reused-input dedupe: a path used as both an
`env`/`secret` source and a `home:` source is gated only by the parsed view. Closing it is a probe
for a value in more than one role.

A validation run must not share a `.git` that exposes the answer-key branch — extract the commit
with `git archive <commit> | tar -x -C <dir>` so there is nothing to walk.
