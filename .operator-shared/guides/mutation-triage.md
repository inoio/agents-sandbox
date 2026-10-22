---
description: How to close gomutants survivors in this repo — the recurring mutant types, the assertion that kills each, the equivalent/near-equivalent traps, and the record-with-a-reason fallback; observed on the first full sweep of internal/agent.
read_if: Triaging LIVED mutants from a `make mutation` sweep, or deciding whether to strengthen a test or record a `// gomutants:disable` reason.
---

# Triaging mutation survivors

The rule lives in `specs/mutation-testing.md`: every LIVED mutant is closed by a strengthened test or a recorded reason.
This is the field guide for doing it. It was distilled from the first full sweep of `internal/agent`
(`make mutation FULL=1 PKG=./internal/agent/`): 36 survivors from 322 mutants, all under-asserted tests, no defects.

## Recurring mutant types and their kills

- **`ERRORF_WRAP` (`%w` → `%v`)** is invisible to `err != nil` because the message text is unchanged; only the wrapping
  relationship is lost. Kill it by reaching the preserved cause with `errors.Is`/`errors.AsType`. Causes seen here:
  `*url.Error` (both `http.NewRequestWithContext` on a malformed URL and `client.Do`),
  `*json.SyntaxError` (`json.Unmarshal` and `json.NewDecoder(...).Decode`), `semver.ErrInvalidSemVer` (returned
  unwrapped by `semver.NewVersion`), `context.Canceled` (reached through `client.Do`'s `*url.Error`).
- **`BRANCH_IF` (body removal)** hides behind a *later* error on the same path: deleting an `if resp.StatusCode != 200`
  guard still fails, later, while decoding the error body, so `err != nil` stays true. Kill it by asserting the branch's
  own message (`strings.Contains(err.Error(), "unexpected status")`) or a value the branch alone produces.
- **`STATEMENT_REMOVE` on `req.Header.Set`** needs the test server to assert what it received
  (`Accept: application/json`, `User-Agent: agents-sandbox`).
- **`RETURN_TRUE` / `RETURN_FALSE` / `RETURN_ZERO`** need the *value* asserted alongside the error: the returned `ok`,
  the copied `count` before a later rule failed, the boolean from a version compare. `if _, err := f(); err == nil`
  leaves the value free.
- **`INTEGER_INCREMENT` / `INTEGER_DECREMENT` / `ARITHMETIC_BASE` on a defensive `http.Client{Timeout: …}`** cannot be
  observed against a local server that answers immediately, and the value is not a contract. Record it:
  `// gomutants:disable-next-line INTEGER_INCREMENT,INTEGER_DECREMENT,ARITHMETIC_BASE reason="…"`.

## Traps

- **`EXPRESSION_REMOVE` on `A && B` emits two mutants, one per operand.** If an operand is redundant the mutant is
  equivalent and unfalsifiable: `strings.HasPrefix(p, "!") && p == "!"` with the left operand removed becomes
  `true && p == "!"`, which is the original. A same-line or `disable-next-line` directive is **type-scoped** (it cannot
  name a stable-id), so recording that one also suppresses the *killable* sibling. Delete the redundant operand — the
  mutant ceases to exist — and let the surviving condition be killed normally.
- **A guard can be operationally equivalent to its fall-through.** `if err := json.Unmarshal(...); err != nil { return
  "", false }` is indistinguishable from falling through to `return resp.Directory, resp.Directory != ""` for every
  payload *except* one that sets the field and then errors — a duplicate JSON key
  (`{"directory":"/x","directory":123}`). `json.Unmarshal` pre-validates syntax over the whole input, so a trailing
  syntax error aborts before any field is set; only a type error on a later duplicate key leaves the field populated.
- **`--detect-equivalent` compares assembly, not behaviour.** A mutation that is logically equivalent but compiles to
  different code still reports LIVED, and must be closed by hand (delete or record).
- **A "restore the factory" mutant (`Get = orig` removed from a `t.Cleanup`) needs a type or behavioural check, not a
  func-pointer comparison.** `reflect.ValueOf(fn).Pointer()` returns the *code* pointer, so two closures minted from the
  same function literal compare equal — a mock installed by the helper reads as "restored" against a leak of that same
  literal from an earlier test (and the test then passes under the mutant). Assert the concrete type
  (`Get().(*mockConfigPaths)` must be false after the installing subtest returns) or the restored factory's behaviour
  (the fail-fast default panics).
- **`BRANCH_IF`/`STATEMENT_REMOVE` on a `fmt.Fprintln(os.Stdout, err)` failure report is only visible as text.** Swap
  `os.Stdout` for an `os.Pipe` write end around the call, restore `os.Stdout` and close the write end *before* any
  `t.Errorf` (the test framework's own output goes to stdout too), then assert the read end contains the error's path —
  the mutated branch prints nothing, or `<nil>` when the condition is negated.
- **`// gomutants:disable` is same-line-scoped, so a standalone comment above a statement records nothing.** gomutants
  parses a bare `disable` as `directiveSameLine` and matches it against mutants on *the comment's own line*
  (`internal/discover/directives.go` in v0.7.1); the only other forms are `disable-next-line`, `disable-func`, and
  `disable-regexp`. Place the directive **same-line** on the guarded statement — `if err != nil { // gomutants:disable
  BRANCH_IF reason="…"` — or use `disable-next-line` on the line directly above it (blank lines and other comments are
  skipped, so it binds to the next code line). A `BRANCH_IF` reports its line at the body's `{`, i.e. the `if` line, not
  the body line. A mis-placed directive is a **silent no-op**: after adding one, re-run the tool and confirm the
  package's `mutants_lived` drops and its suppressed count rises.
- **`tb.Fatalf`/`t.Helper()` inside a test-support helper are structurally unfalsifiable.** `Fatalf` calls
  `runtime.Goexit()` in the helper and `Helper` is stack-trace metadata only, so no assertion inside the test binary can
  observe their removal. Record them; do not invent a production seam to make them killable.

## Operational

- A cold `FULL=1` run of one package (`internal/agent`, ~320 mutants) exceeds 15 minutes in a fresh worktree; reruns
  resume from `.gomutants-cache.json` and are much faster. Run it in the background.
- `make mutation FULL=1 PKG=./internal/agent/` writes `mutation-report.json` (gitignored) plus the cache into the
  worktree. Read the result with `jq`:
  `jq -r '[.files[] as $f | $f.mutations[] | select(.status=="LIVED") | "\($f.file_name):\(.line) \(.type)"] | .[]'`.
