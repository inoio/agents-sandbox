---
description: Contract for termio's UI backends - the termio.UI mockability seam, the OPENCODE_SANDBOX_PROMPT prompt switch, the decision to confine huh to modal prompts, and the accessibility/non-TTY invariants.
read_if: Changing interactive prompts or spinners, swapping in or scoping a UI library (huh, bubbletea, ...), or reasoning about mockability and non-TTY/accessibility behavior.
---

# termio UI backend contract

## Decision: huh for prompts only; ambient output stays hand-rolled

- **`huh` is used only for interactive prompts** (`Select`/`Input`, arrow-key menus). The `huh` and `huh-accessible` values of
  `OPENCODE_SANDBOX_PROMPT` select it.
- **Spinners, tables, and log/verbose output stay on the hand-rolled line writer.** They are *ambient*: they render while other
  code (Docker/apt streams, subprocess output) writes to the same terminal, so they must emit self-contained full-line writes.
- **The huh (bubbletea) spinner was prototyped and rejected.** See the corruption rationale below.
- Rule of thumb: a UI library that drives the terminal is safe only for **modal** interactions, where the app exclusively owns the
  terminal while blocked on the user. Never use it for ambient output that races with other writers.

## The seam is `termio.UI`, not the renderer

`termio.UI` (`internal/termio/termio.go`) is the abstraction all callers depend on. Two implementations exist: the production
`printer` and the test `Mock` (`internal/termio/mock.go`). **Mockability is preserved as long as new UI work stays behind this
interface** - a backend swap changes only how `Select`/`Input`/`Spinner` render internally; callers and tests keep driving a
`termio.UI`. This is why the huh prompt backend required no `termio.UI` or caller changes.

## Prompt switch

`OPENCODE_SANDBOX_PROMPT` (env only, parsed by `termio.ParsePromptBackend`) selects the **prompt** backend. Values: `line`
(default), `huh`, `huh-accessible`. `huh` and `huh-accessible` use charmbracelet/huh for `Select`/`Input`; `line` is the original
hand-rolled line prompts. Spinners and all other output are unaffected by this setting. Non-interactive invocations (piped input
or `--yes`) always return the default choice regardless of backend.

## Accessibility / non-interactivity is a huh-layer feature, not bubbletea

bubbletea has **no** accessibility or non-interactive/screen-reader mode. It only offers `WithoutRenderer()` (plain output, no
redraw), `WithInput(nil)` (disable input), and `WithWindowSize(...)` for non-interactive environments. huh implements
accessibility itself: `Form.WithAccessible`, plus a per-field `RunAccessible(w, r)`. In accessible mode huh **bypasses
bubbletea entirely** - `Form.Run` loops `field.RunAccessible(...)` and never builds a `tea.Program`.

Invariant: **every interactive termio backend must provide a plain line/accessible path that degrades to ordinary text.** The line
backend is that path; `huh-accessible` is huh's. A bubbletea-native component gets no free fallback and must ship its own
line/accessible branch behind `termio.UI`. Prefer huh's accessible patterns (injected `io.Reader`/`io.Writer`, no event loop) as
the deterministic, testable path.

## bubbletea inline vs. window

bubbletea v2 is **inline by default**: the alternate screen ("window") is opt-in via `View.AltScreen`, so line-oriented output in
normal scrollback is the natural mode. The interactive `huh` prompts render inline (to stderr) and leave scrollback intact. Any
interactive backend that needs a real terminal must fall back to the line backend when output is not a TTY.

## Why the huh spinner was rejected (ambient output corruption)

bubbletea's renderer assumes it **exclusively owns the output stream**: it tracks the previous frame and redraws only changed
cells, relying on unchanged content (the title) remaining on screen. When other code writes to the same stderr concurrently, the
cursor is no longer where the renderer expects, so the diff corrupts - the title prints once and only the spinner glyph is
redrawn, glued to whatever line was written between ticks. The image build does exactly this: `build.go` animates a spinner on
stderr while `scanBuildOutput` streams decoded Docker/apt lines through `ui.Verbose` to the same stderr.

The hand-rolled line spinner tolerates it because every frame re-emits a full, self-contained `\r\033[K<msg> <glyph>(elapsed)`
line and never depends on stale screen content (though it too has no lock against the concurrent writer).

Correct fixes would require output ownership (route concurrent output through the bubbletea program via `Program.Println`, or
serialise a single console writer). Given the cost and that spinners are ambient, the chosen solution is to keep spinners
hand-rolled. The underlying "no single console owner" weakness remains in the line spinner and is a known limitation if concurrent
output becomes a bigger problem.
