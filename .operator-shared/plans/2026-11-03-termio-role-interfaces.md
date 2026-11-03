# termio Role-Interface Split Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the single 26-method `termio.UI` seam with six narrow role interfaces so a consumer names only the surface it uses, while `UI` remains their union for the composition root and genuinely multi-role consumers.

**Architecture:** `internal/termio` defines `Logger`, `Output`, `Prompter`, `SpinnerFactory`, `TerminalState`, `Streams`; `UI` embeds all six and `New` keeps returning `UI`. A consumer is retyped to a single role only when it uses exactly one role; anything using two or more keeps `termio.UI`. The concrete `*printer` and the test `Mock` satisfy every role, so the change is behavior-preserving and compiler-checked. No method bodies change except `Table`'s field type and `ConfirmAction`'s parameter.

**Tech Stack:** Go 1.26, standard `testing`, the existing `internal/termio` `printer`/`Mock` pair.

**Spec:** `.operator-shared/specs/termio-ui-backends.md`

## Global Constraints

- The role interfaces and `UI` live in `internal/termio/termio.go`; `UI` embeds all six roles; `New` continues to return `UI`.
- **Narrowing rule:** retype a symbol (function parameter, struct field, variable, func type) to a role only when that symbol uses exactly one role; a symbol that uses two or more roles keeps `termio.UI`. "Uses" includes forwarding `ui` to a callee, so a pass-through adopts its callee's role.
- `Mock` (`internal/termio/mock.go`) stays **one struct that implements all six roles**; do not split it into per-role doubles, and keep its mutex-guarded recording.
- No behavior changes: level/quiet gating, spinner rendering, and confirm semantics stay identical; only type declarations change.
- Over-narrowing fails `go build` (a `Logger` cannot be passed where `UI`/another role is required). Fix an over-narrow by leaving that symbol as `termio.UI`; never widen a callee to accommodate it.
- Run `make fmt` then `make lint` after edits; run tests with `make test` (or `go test -tags microsandbox_ffi_path ./...`).
- Every production change needs a `changelog.d/<pr-number>.changed.md` fragment (allowed types: `added|changed|deprecated|docs|fixed`).

## Review Focus

- **`Table` rendering** must stay header-then-rows through `Header`/`Out`, with unchanged alignment (Task 1).
- **`Mock` usability everywhere** — a test that builds `NewTestMock` must still be handable to a consumer narrowed to any single role, and concurrent recording must survive (Task 1).
- **Confirm semantics** — non-interactive without `--yes` returns `ErrConfirmationRequired`, a decline returns `ErrCancelled` — unchanged after `ConfirmAction` narrows to `Prompter` (Tasks 1, 4).
- **`autoPruneOutToVerboseRedirect`** must still turn `Out`/`Outf` into `Verbose`/`Verbosef` (`TestAutoPruneOutToVerboseRedirect`); it deliberately stays a `termio.UI` embed (Task 5).
- **Coverage on changed lines** — a narrowed symbol that no test exercises leaves an uncovered changed line; give it a focused test in its package, or leave it `termio.UI` (all migration tasks).

---

### Task 0: Land the design docs

**Files:**
- Add: `.operator-shared/specs/termio-ui-backends.md` (already written)
- Modify: `.operator-shared/catalog.md` (entry already updated)
- Add: `.operator-shared/plans/2026-11-03-termio-role-interfaces.md` (this file)

- [ ] **Step 1: Commit the spec, catalog entry and plan**

```bash
git add .operator-shared/specs/termio-ui-backends.md .operator-shared/catalog.md .operator-shared/plans/2026-11-03-termio-role-interfaces.md
git commit -m "docs: spec the termio role-interface split (#362)"
```

---

### Task 1: Role interfaces and the seam (S1)

**Files:**
- Modify: `internal/termio/termio.go`
- Modify: `internal/termio/table.go`
- Modify: `internal/termio/mock.go`
- Add: `internal/termio/roles_test.go`

**Interfaces:**
- Consumes: existing `printer` (`internal/termio/printer.go`), `Mock`, `Table`, `Choice`, `Level`, `Spinner`, `ConfirmAction`, `ErrConfirmationRequired`, `ErrCancelled`.
- Produces: the six role interfaces and the `UI` union, plus `ConfirmAction(ui Prompter, prompt string) error` and an unexported `tableWriter` interface.

The role interfaces (place in `termio.go`, above `UI`):

```go
type Logger interface {
	Info(msg string)
	Infof(format string, args ...any)
	Warn(msg string)
	Warnf(format string, args ...any)
	Error(msg string, err error)
	Errorf(format string, args ...any)
	Verbose(msg string)
	Verbosef(format string, args ...any)
}

type Output interface {
	Out(msg string)
	Outf(format string, args ...any)
	Print(msg string)
	Header(msg string)
	NewTable(headers ...string) *Table
}

type Prompter interface {
	IsInteractive() bool
	Select(prompt string, choices []Choice, defaultKey string) (string, error)
	Input(prompt, defaultValue string) (string, error)
	Confirm(prompt string, defaultYes bool) (bool, error)
	Acknowledge(message string) error
}

type SpinnerFactory interface {
	Spinner(msg string) Spinner
	Spinnerf(format string, args ...any) Spinner
}

type TerminalState interface {
	SetLevel(level Level)
	SetAssumeYes(assumeYes bool)
	SetQuiet(quiet bool)
	Quiet() bool
}

type Streams interface {
	StdOut() io.Writer
	StdErr() io.Writer
}

type UI interface {
	Logger
	Output
	Prompter
	SpinnerFactory
	TerminalState
	Streams
}
```

- [ ] **Step 1: Write the failing test**

`internal/termio/roles_test.go`:

```go
package termio

import "testing"

// minimalTableWriter implements only the two methods a Table prints through,
// proving a Table no longer depends on the whole UI.
type minimalTableWriter struct {
	headers []string
	rows    []string
}

func (w *minimalTableWriter) Header(msg string) { w.headers = append(w.headers, msg) }
func (w *minimalTableWriter) Out(msg string)    { w.rows = append(w.rows, msg) }

func TestTablePrintsThroughMinimalWriter(t *testing.T) {
	w := &minimalTableWriter{}
	table := &Table{ui: w, headers: []string{"NAME"}}
	table.AddRow("alpha")
	table.Print()

	if len(w.headers) != 1 || w.headers[0] != "NAME" {
		t.Fatalf("header calls = %q, want [NAME]", w.headers)
	}
	if len(w.rows) != 1 || w.rows[0] != "alpha" {
		t.Fatalf("row calls = %q, want [alpha]", w.rows)
	}
}

// prompterOnly implements Prompter alone: ConfirmAction must accept it.
type prompterOnly struct{ confirmed bool }

func (p *prompterOnly) IsInteractive() bool { return true }
func (p *prompterOnly) Select(string, []Choice, string) (string, error) { return "", nil }
func (p *prompterOnly) Input(string, string) (string, error)            { return "", nil }
func (p *prompterOnly) Confirm(string, bool) (bool, error) {
	p.confirmed = true
	return true, nil
}
func (p *prompterOnly) Acknowledge(string) error { return nil }

func TestConfirmActionAcceptsPrompterOnly(t *testing.T) {
	p := &prompterOnly{}
	if err := ConfirmAction(p, "remove?"); err != nil {
		t.Fatalf("ConfirmAction: %v", err)
	}
	if !p.confirmed {
		t.Fatal("Confirm was not called")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test -tags microsandbox_ffi_path ./internal/termio/ -run 'TestTablePrintsThroughMinimalWriter|TestConfirmActionAcceptsPrompterOnly'`
Expected: FAIL to build — `undefined: Prompter` / `cannot use p (variable of type *prompterOnly) as UI value` and `cannot use w ... as UI value`.

- [ ] **Step 3: Define the roles and narrow the two seams**

In `termio.go` add the six interfaces and the `UI` union from the Interfaces block. Change `ConfirmAction`'s parameter from `ui UI` to `ui Prompter`. Add compile-time assertions after the interface block:

```go
var (
	_ Logger         = (*printer)(nil)
	_ Output         = (*printer)(nil)
	_ Prompter       = (*printer)(nil)
	_ SpinnerFactory = (*printer)(nil)
	_ TerminalState  = (*printer)(nil)
	_ Streams        = (*printer)(nil)
	_ UI             = (*printer)(nil)
)
```

In `table.go`, add `type tableWriter interface { Header(msg string); Out(msg string) }` and change the `Table.ui` field type from `UI` to `tableWriter` (the `NewTable` bodies and `Print` are unchanged).

In `mock.go`, add `var _ UI = (*Mock)(nil)` and the same six per-role assertions for `(*Mock)`.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -tags microsandbox_ffi_path ./internal/termio/...`
Expected: PASS (the new tests plus the existing package suite).

- [ ] **Step 5: Verify the whole module still builds**

Run: `go build ./...`
Expected: no output, exit 0 (no consumer changes are needed yet).

- [ ] **Step 6: Commit**

```bash
git add internal/termio/termio.go internal/termio/table.go internal/termio/mock.go internal/termio/roles_test.go
git commit -m "refactor(termio): split UI into role interfaces (#362)"
```

---

### Task 2: Log-only consumers take `termio.Logger` (S2)

The migration is bottom-up and the compiler is the arbiter: a pass-through symbol cannot narrow until every callee it forwards `ui` to is itself narrowed. A symbol retyped to `termio.Logger` that forwards `ui` to a callee still taking `termio.UI` (or another role) fails `go build`; revert that one occurrence and leave it as `termio.UI`.

**Files:** the log-only symbols across `internal/sandbox/**`, `internal/runoptions/`, and `cmd/agents-sandbox/`. The list is produced by the sweep below, not fixed here.

**Interfaces:**
- Consumes: `termio.Logger` (Task 1).
- Produces: no new exported names; narrowed symbols' `ui` parameters/fields become `termio.Logger`.

- [ ] **Step 1: Baseline build**

Run: `go build ./...`
Expected: success (Task 1 left the tree compiling).

- [ ] **Step 2: Narrow the verified leaves**

Run: `rg -n 'termio\.UI' -g '*.go' -g '!**/*_test.go'`
A symbol that also forwards `ui` as an argument is a pass-through; narrow it later. Start from these verified leaves (each calls only `Logger` methods and forwards `ui` nowhere):
`internal/sandbox/prepare/shared_git_dir_mount.go`, `internal/sandbox/prepare/upgrade_migration.go`, `internal/sandbox/reprovision/dependency_warning.go`, `internal/sandbox/envsecret/secrets.go`, `internal/sandbox/vm/worktree.go`, `cmd/agents-sandbox/tree.go`.

Retype each leaf's `ui` parameter/field from `termio.UI` to `termio.Logger`, e.g.:

```go
// before
func WarnIgnoredProjectConfig(ui termio.UI) { ui.Warnf(...) }
// after
func WarnIgnoredProjectConfig(ui termio.Logger) { ui.Warnf(...) }
```

- [ ] **Step 3: Sweep bottom-up until it converges**

Re-run the `rg` sweep. Each symbol whose direct `ui` calls are all `Logger` methods and whose forwarded callees now all take `Logger` (or take no `ui`) is retypeable. Retype it, run `go build ./...`, and revert any occurrence that fails (it still forwards `ui` to a non-`Logger` callee). Repeat until a full pass adds no narrowing.

- [ ] **Step 4: Verify the suite passes**

Run: `go test -tags microsandbox_ffi_path ./internal/sandbox/... ./internal/runoptions/... ./cmd/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/sandbox internal/runoptions cmd/agents-sandbox
git commit -m "refactor(termio): take Logger for log-only consumers (#362)"
```

Symbols that use two or more roles (the wiring root, `session/run.go`, `vm/vm_lifecycle.go`, the `pruning`/`volume`/`upgrade` aggregates, `image/build.go`, the `runtimemigration` prompter functions, and any pass-through still feeding them) remain `termio.UI`.

---

### Task 3: Rendering-only consumers take `termio.Output` (S3)

**Files:**
- Start from: `internal/cliout/prune.go` (`RenderPruneReport`), `internal/cliout/mode.go` (`WriteJSON`), and the `cmd/agents-sandbox` renderers `inspect_output.go`, `storage_output.go`, `commands_state.go`.
- The same bottom-up rule applies: retype a symbol only if it forwards `ui` nowhere, or only to already-narrowed `Output` consumers.

**Interfaces:**
- Consumes: `termio.Output` (Task 1).
- Produces: no new exported names; narrowed symbols' `ui` parameters become `termio.Output`.

Leave as `termio.UI`: `internal/cliout/list.go` (`PrintList` also calls `ui.Info`), `cmd/agents-sandbox/commands_inspect.go` (uses `Quiet` too), every `internal/sandbox/pruning/*` renderer (they also log).

- [ ] **Step 1: Enumerate the candidates**

Run: `rg -n 'termio\.UI' internal/cliout cmd/agents-sandbox -g '*.go' -g '!**/*_test.go'`
Confirm each candidate calls only `Out`/`Outf`/`Print`/`Header`/`NewTable` and forwards `ui` only to an `Output` consumer.

- [ ] **Step 2: Retype each Output-only symbol to `termio.Output`**

- [ ] **Step 3: Verify it builds and the suite passes**

Run: `go build ./... && go test -tags microsandbox_ffi_path ./internal/cliout/... ./cmd/...`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/cliout cmd/agents-sandbox
git commit -m "refactor(termio): take Output for render-only consumers (#362)"
```

---

### Task 4: Prompt-only consumers take `termio.Prompter` (S4)

**Files:**
- `internal/sandbox/reconfig/prompts.go`
- `internal/sandbox/image/remove.go` (`RemoveImages`), `internal/sandbox/volume/remove.go` (`RemoveVolumes`), `internal/sandbox/vm/remove.go` (`RemoveSandbox`)

**Interfaces:**
- Consumes: `termio.Prompter` (Task 1), `termio.ConfirmAction(ui Prompter, prompt string) error` (Task 1).
- Produces: no new exported names; these symbols' `ui` parameters are typed `termio.Prompter`.

Leave as `termio.UI`: `cmd/agents-sandbox/prune_cli.go` (`confirmPrune` calls `Confirm` but `resolvePruneOutputMode` calls `Quiet`), `internal/sandbox/volume/prompt.go`, `internal/sandbox/prepare/upgrade.go`, `internal/sandbox/runtimemigration/offer.go`, `internal/sandbox/runtimemigration/notice.go` (each also logs).

- [ ] **Step 1: Ensure the confirm paths stay covered**

In `internal/sandbox/volume/remove_test.go` (and the sibling `image`/`vm` remove tests), make sure a case drives each `termio.Confirm` outcome: a declined confirm returns `termio.ErrCancelled`, and a non-interactive mock without assume-yes returns `termio.ErrConfirmationRequired`. Add whichever case is missing so the narrowed parameter's lines stay covered; the behavior itself is unchanged.

- [ ] **Step 2: Run the remove tests**

Run: `go test -tags microsandbox_ffi_path ./internal/sandbox/volume/ ./internal/sandbox/image/ ./internal/sandbox/vm/ -run 'Remove|Confirm'`
Expected: PASS on the current `termio.UI` signature.

- [ ] **Step 3: Retype `RemoveImages`, `RemoveVolumes`, `RemoveSandbox`, and the `prompts.go` functions to `termio.Prompter`**

- [ ] **Step 4: Verify it builds and the suite passes**

Run: `go build ./... && go test -tags microsandbox_ffi_path ./internal/sandbox/...`
Expected: PASS. Revert any symbol that fails `go build` because it forwards `ui` to a non-`Prompter` callee.

- [ ] **Step 5: Commit**

```bash
git add internal/sandbox
git commit -m "refactor(termio): take Prompter for prompt-only consumers (#362)"
```

---

### Task 5: Terminal-state consumers; confirm no spinner-only consumer (S5/S6)

**Files:**
- `cmd/agents-sandbox/cli.go` (`applyCLISettings`)

**Interfaces:**
- Consumes: `termio.TerminalState` (Task 1).
- Produces: `applyCLISettings(cmd *cobra.Command, ui termio.TerminalState, r *launcherconfig.Resolver) error`.

No spinner-only consumer exists (every spinner caller also logs), so `SpinnerFactory` gets no migration. `Streams` has no single-role consumer (`buildRootCmd` uses `Streams` plus several roles and keeps `UI`). `autoPruneOutToVerboseRedirect` keeps its `termio.UI` embed — its consumer `pruning.AutoPrune` spans roles.

- [ ] **Step 1: Confirm the S5 claim**

Run: `rg -n '\bui\.Spinnerf?\(' -g '*.go' -g '!**/*_test.go'`
For each file, check it also calls a `Logger` method; if any is spinner-only, retype it to `termio.SpinnerFactory` instead of leaving it. (Expected: none.)

- [ ] **Step 2: Retype `applyCLISettings` to `termio.TerminalState`**

- [ ] **Step 3: Verify it builds and the suite passes**

Run: `go build ./... && go test -tags microsandbox_ffi_path ./cmd/...`
Expected: PASS, including `TestAutoPruneOutToVerboseRedirect`.

- [ ] **Step 4: Commit**

```bash
git add cmd/agents-sandbox
git commit -m "refactor(termio): take TerminalState for CLI settings (#362)"
```

---

### Task 6: Changelog and full verification

**Files:**
- Add: `changelog.d/<pr-number>.changed.md`

- [ ] **Step 1: Add the changelog fragment**

Create `changelog.d/<pr-number>.changed.md` (one bullet, present tense), e.g.:

```markdown
Split the terminal UI seam into narrow `termio` role interfaces (`Logger`, `Output`, `Prompter`, `SpinnerFactory`, `TerminalState`, `Streams`) so consumers depend only on the surface they use. (#362)
```

- [ ] **Step 2: Run the full check**

Run: `make check`
Expected: fmt, lint, docs and tests all pass.

- [ ] **Step 3: Confirm no stray `termio.UI` remains on a single-role symbol**

Run: `rg -n 'termio\.UI' -g '*.go' -g '!**/*_test.go'` and spot-check that every remaining match uses two or more roles (wiring root, session runners, pruning/volume/upgrade aggregates, the redirect decorator).

- [ ] **Step 4: Commit**

```bash
git add changelog.d
git commit -m "docs: changelog for termio role-interface split (#362)"
```
