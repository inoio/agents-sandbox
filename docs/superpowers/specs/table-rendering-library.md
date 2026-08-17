# Instructional Spec: Table-rendering library for list output (Spec 0)

## Status

**Predecessor / optional.** This spec is a *possible* prerequisite to Chunks A, B, C
(the richer list-output specs). It is **not** required to implement them — see
"Decision needed" below. If adopted, do it before the list-output chunks so they build
on the chosen renderer.

## Goal

Decide whether to adopt a table/terminal-rendering library for the `list` commands, and
if so which one. Today every list command renders via `printItems` (cmd/opencode-sandbox/
commands.go) using a fixed `fmt` format string like `%-40s %s`, and coloring is handled
by `termio.printer` (internal/termio/printer.go) with a small set of ANSI codes and a
global `color` toggle.

Candidate: https://github.com/pterm/pterm (styled text + tables + spinners).

## The question

For the richer, column-aligned list output in Chunks A/B/C, do we:

1. Keep the existing `printItems` + `termio` approach and hand-roll alignment/truncation,
   or
2. Add a library (e.g. pterm) for aligned tables and optional styling?

## Trade-offs

**Keep termio/printItems**
- No new dependency.
- Consistent with existing code and tests (which already normalize whitespace, so
  alignment is not asserted — see `containsNormalized` in cli_list_subcommand_test.go).
- Column alignment and truncation must be written by hand, but for 2–4 columns this is
  small.
- Least risk; fastest path.

**Adopt pterm**
- Aligned tables, truncation, and styling (colors/bold) out of the box — overlaps the
  (declined) coloring wish from the list specs.
- New third-party dependency; must evaluate its interactive behavior, TTY detection,
  and how it interacts with the existing `termio` color/level toggles.
- Adds a renderer layer that the current `printItems` avoids.
- YAGNI risk: Chunks A/B/C only add a few columns; a library may be overkill.

## Recommendation

Keep `termio`/`printItems` for Chunks A/B/C. The added columns are few, alignment is
handled by a format string, and existing tests already tolerate column-width drift. If a
future need for JSON output (see `--format json` in the sandbox-labels-and-list-filter
spec) or rich interactive tables arises, revisit this decision then.

If the decision is to adopt pterm, do so in its own implementation session with:
- A small spike comparing pterm table output vs. current `printItems` for all three
  list commands.
- Verification that colors/levels respect the existing `--quiet`/`--verbose` and
  non-TTY behavior.
- Updating `containsNormalized`-style assertions if pterm changes output spacing.

## Out of scope

- Spinner or progress rework (termio already has spinners).
- Coloring the list output — the launcher decided to keep list output plain.