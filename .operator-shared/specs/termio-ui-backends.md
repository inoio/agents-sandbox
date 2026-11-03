---
description: Contract for termio's UI seam and backends - the role-interface split (Logger/Output/Prompter/SpinnerFactory/TerminalState/Streams) composed into termio.UI, the OPENCODE_SANDBOX_PROMPT prompt switch, the decision to confine huh to modal prompts, and the accessibility/non-TTY invariants.
read_if: Changing a termio role interface, `termio.UI` or `New`, which UI surface a caller depends on, the termio mock, interactive prompts or spinners, swapping in or scoping a UI library (huh, bubbletea, ...), or reasoning about mockability and non-TTY/accessibility behavior.
---

# termio UI seam and backend contract

## The seam is the role interfaces; `termio.UI` is their union

`internal/termio/termio.go` defines the terminal surface as a set of narrow **role interfaces**. A caller
depends on the narrowest role(s) it uses rather than on the whole terminal, so an interface as wide as the
implementation no longer exists (the #362 finding: one 26-method `UI` forced a caller that only needs `Out`
to depend on prompts, spinners, tables, terminal state, and raw streams too).

| Role | Methods | Concern |
|------|---------|---------|
| `Logger` | `Info`/`Infof`, `Warn`/`Warnf`, `Error`/`Errorf`, `Verbose`/`Verbosef` | level-gated diagnostics to stderr |
| `Output` | `Out`/`Outf`, `Print`, `Header`, `NewTable` | unleveled stdout data lines and tables |
| `Prompter` | `IsInteractive`, `Select`, `Input`, `Confirm`, `Acknowledge` | interactive prompts and their non-interactive fallbacks |
| `SpinnerFactory` | `Spinner`, `Spinnerf` | progress spinners |
| `TerminalState` | `SetLevel`, `SetAssumeYes`, `SetQuiet`, `Quiet` | CLI settings applied to the single printer |
| `Streams` | `StdOut`, `StdErr` | raw streams handed to subprocesses and other writers |

`UI` is `interface { Logger; Output; Prompter; SpinnerFactory; TerminalState; Streams }`. `New` returns
`UI` because the CLI wiring root threads one value through every command builder; a consumer that only logs
takes a `termio.Logger` in its signature, and so on.

**Mockability is preserved as long as new UI work stays behind these interfaces** - a backend swap changes
only how `Select`/`Input`/`Spinner` render internally; callers and tests keep driving a role interface. The
test `Mock` (`internal/termio/mock.go`) stays **one struct that implements every role**: `NewTestMock`
returns `Mock`, and a caller narrowed to `Logger` can still be handed the same `Mock` its test builds.
Splitting `Mock` into per-role doubles is deliberately not part of this change.

One consequence falls out of the split:
- `Table` holds only the `Header`/`Out` methods it prints through, not a full `UI`.

`autoPruneOutToVerboseRedirect` (`cmd/agents-sandbox/commands.go`) keeps its embedded `UI`: it is handed
to the multi-role `pruning.AutoPrune` aggregate, so narrowing it would force that aggregate to a
two-role composition the single-role rule does not allow.

**Concurrency invariant:** callers may drive a UI from background goroutines while the main goroutine also
uses it (e.g. `session.startNotifyWatcher` logs from a watcher goroutine alongside `Run`). The `Mock`
therefore guards its recorded calls with a mutex; any new implementation of a role interface must tolerate
concurrent use.

## Decision: huh for prompts only; ambient output stays hand-rolled

- **`huh` is used only for interactive prompts** (`Select`/`Input`, arrow-key menus). The `huh` and `huh-accessible` values of
  `OPENCODE_SANDBOX_PROMPT` select it.
- **Spinners, tables, and log/verbose output stay on the hand-rolled line writer.** They are *ambient*: they render while other
  code (Docker/apt streams, subprocess output) writes to the same terminal, so they must emit self-contained full-line writes.
- **The huh (bubbletea) spinner was prototyped and rejected.** See the corruption rationale below.
- Rule of thumb: a UI library that drives the terminal is safe only for **modal** interactions, where the app exclusively owns the
  terminal while blocked on the user. Never use it for ambient output that races with other writers.

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
line/accessible branch behind the `Prompter` role interface. Prefer huh's accessible patterns (injected `io.Reader`/`io.Writer`, no event loop) as
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

## Rollout

S1 is the only contract change; S2-S6 are mechanical consumer migrations, independent of one another once S1
is in (they can land in any order or in parallel). **Narrowing rule:** a symbol (function parameter, struct
field, variable, func type) is narrowed exactly when it uses a single role; a symbol that uses two or more
roles - the CLI wiring root, the session/worktree runners, the `pruning`/`volume`/`upgrade` aggregates -
keeps `termio.UI`. Spinner consumers almost always log alongside the spinner and prompt consumers often log
alongside the prompt, so S5 is expected to be near-empty and S4 small.

| Slice | Scope |
|-------|-------|
| S1 | Define the six role interfaces in `internal/termio`; make `UI` embed them (`New` still returns `UI`); narrow `Table` to `{Header, Out}` and `ConfirmAction` to `Prompter`; add compile-time `var _ <Role> = (*printer)(nil)` and `(*Mock)` assertions. No other consumer changes. |
| S2 | Migrate log-only consumers to `termio.Logger` (the bulk: `internal/sandbox/**` leaves, `image`, `vm`, `prepare`, `reprovision`, `envsecret`, `doctor`, `runoptions`, `cmd` tree/warnings). |
| S3 | Migrate rendering-only consumers to `termio.Output` (`internal/cliout` report/JSON writers, the `cmd/agents-sandbox` inspectors, storage/`df` output). |
| S4 | Migrate prompting-only consumers to `termio.Prompter` (`reconfig/prompts.go`, the image/volume/vm `Remove*` entry points; `ConfirmAction` moved in S1). |
| S5 | Migrate spinner-only consumers to `termio.SpinnerFactory` (expected empty - every spinner consumer also logs, so those symbols keep `UI`). |
| S6 | Migrate terminal-state consumers to `termio.TerminalState` (`cmd/agents-sandbox`'s `applyCLISettings`); no `Streams`-only consumer exists (`buildRootCmd` spans roles and keeps `UI`). |

## Non-goals

- Swapping the hand-rolled renderer for a UI library (#20) and replacing handwritten mocks with a mock
  library (#21) are separate, adjacent efforts; neither is a prerequisite.
- Splitting the test `Mock` into per-role doubles.
- Consumer-defined interfaces: the roles live in `termio` and `UI` stays the union, so `New` and the CLI
  wiring keep threading one value.
