---
description: How to close gomutants survivors in this repo — the recurring mutant types, the assertion that kills each, the equivalent/near-equivalent traps (including false "returns nil on error" equivalence claims, unparameterized clamp bounds, yaml.v3's partial decode, map-iteration randomization, seam call-counting, a guard whose error is re-derived downstream, a blocking-flock seam, a negative "drop" sentinel, and a wall-clock budget constant), and the record-with-a-reason fallback.
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
- **A `return err` branch whose fall-through wraps the same error with `%w` is invisible to `errors.Is`: assert
  identity.** `prepareHomeVolume` deliberately returns the sentinel `runtimemigration.ErrMigrationPending` unwrapped so
  the user sees its guidance, and the fall-through wraps it as `fmt.Errorf("…: %w", err)`; `errors.Is(err, sentinel)`
  therefore accepts both and the branch survives. `err != sentinel` (or the absence of the wrap phrase) is the
  assertion that kills it.
- **`RETURN_ZERO` on a constant that is already the type's zero value compiles to the identical constant — record it.**
  `vmBootConnected` is the first `iota` constant of `vmBoot`, so a `RETURN_ZERO` replacement of a `return …,
  vmBootConnected, …` is literally the same value; place a `// gomutants:disable-next-line RETURN_ZERO reason="…"` on
  the return.
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
- **An `&&` operand that is not itself redundant can still have an equivalent removal mutant, when the code it guards
  feeds a consumer that discards the mutant's effect.** `runoptions.ResolveDockerSetting` guarded an `append` of
  `{Source: cfg.DockerSource(), Value: cfg.Docker}` with `cfg != nil && cfg.DockerExplicit()`; a config with no explicit
  source appends `viperconfig.DockerSourceNone`, which `viperconfig.EffectiveDockerSource` drops, so `cfg != nil && true`
  resolved identically and one of the two operand mutants survived. Deleting the operand was not available — it would
  have made `viperconfig.Config.DockerExplicit` test-only and tripped the dead-code gate — and a line-scoped directive
  would also suppress the killable `cfg != nil` sibling, so **nest the guards** (`if cfg != nil { if cfg.DockerExplicit()
  { … } }`): the `&&` mutant family ceases to exist, the inner body stays load-bearing, and the predicate stays
  production-reachable.
- **Two operands that are equivalent under a documented invariant leave no killable mutant — keep the one that states
  the intent.** `session.Run`/`Shell` guarded `if mode.SkipsVM() && sb == nil`, but `PrepareSandbox`'s skipVM contract
  already makes `sb == nil` true exactly when the mode skips the VM, so both operand removals and the `&&`→`||`
  inversion were equivalent (three mutants per guard). Keeping only `mode.SkipsVM()` — the dry-run intent — removed the
  whole family; the guard body stays covered by the dry-run-vm tests, and a real-run path is asserted not to take it.
  Note each table entry "`mode.SkipsVM()` → `true`" was an operand removal *of the `&&`*, not a standalone expression
  replacement — once the `&&` is gone no condition mutant is generated at all.
- **A selector argument that every caller ORs across calls is redundant — drop the parameter.** `hasDockerMarker(data,
  want)` had exactly two callers, each `OR`-ing the current and legacy forms (`... dockerMarker`) `|| (... legacyDockerMarker)`,
  so its `ok && marker == want` was equivalent to `marker != ""`: any match satisfied both calls' `OR`. Returning "any
  marker" and dropping `want` deleted four unfalsifiable mutants (the `ok` removal, `&&`→`||`, `marker == want`→`true`, and
  the `==`→`!=` negation) instead of recording them.
- **A later strip or short-circuit can make an earlier one unobservable, so hand-apply before recording.** In
  `internal/git`, `lastPathSegment`'s `://` strip, its `>= 0` bounds, and its `+3` arithmetic are all masked by the
  following `LastIndex("/")` strip: for any URL containing `://` the scheme is discarded either way, so those mutants
  are equivalent and recorded, not killed. Check whether a subsequent operation already throws the mutated value away.
- **A stdlib guarantee can make one operand of a condition redundant; delete it, do not record it.** `strings.CutLast`
  returns an empty `name` whenever the separator is absent, so `if !found || name == "" || strings.Contains(name, "/")`
  is exactly `if name == "" || strings.Contains(name, "/")`. A line-scoped `disable` for the `!found` operand would also
  suppress the killable `name == ""` and `Contains` siblings on the same line, so remove the `!found` operand (taking
  `CutLast`'s third result as `_`) instead.
- **Splitting a guard to narrow a type-scoped exclusion can mint a new equivalent survivor.** `gitdirFile`'s `if err !=
  nil || info.IsDir()` bundled a killable operand (`err != nil` removal leaves `info` nil, so `info.IsDir()` panics on a
  missing `.git`) with a redundant one (`info.IsDir()`: a directory makes the following `os.ReadFile` fail identically).
  Splitting into two `if`s exposes the killable operand but the added `return "", false` mints an unobservable
  `RETURN_TRUE` — `("", true)` for a directory is invisible because `resolveGitDir` returns before `gitdirFile` for a
  directory and `isLinkedWorktree`'s `worktreeRegistration(root, "")` rejects it. Delete the redundant operand instead
  (drop the stat; let `os.ReadFile` reject the directory), which removes the mutant family rather than recording a new
  one.
- **A guard can be operationally equivalent to its fall-through.** `if err := json.Unmarshal(...); err != nil { return
  "", false }` is indistinguishable from falling through to `return resp.Directory, resp.Directory != ""` for every
  payload *except* one that sets the field and then errors — a duplicate JSON key
  (`{"directory":"/x","directory":123}`). `json.Unmarshal` pre-validates syntax over the whole input, so a trailing
  syntax error aborts before any field is set; only a type error on a later duplicate key leaves the field populated.
- **`yaml.v3` does NOT pre-validate like `encoding/json`: it sets earlier fields before a later type error.** A parse
  guard (`if err := yaml.Unmarshal(...); err != nil { return zero }`) is therefore killable — `last_checked: <ts>`
  followed by `agents: 5` (a scalar where a map is expected) returns an error *and* leaves `LastChecked` populated, so
  the guard must discard that partial state. Do not generalize the json bullet above to yaml.
- **`map[string]json.RawMessage` never partially populates: no input leaves the map filled when `Unmarshal` errors.** A
  guard that returns a zero value on an unmarshal error is equivalent to the fall-through's missing-key check; record it.
  Likewise `string(raw) == "null"` is a redundant operand (unmarshalling literal `null` into a `string` yields `""` with
  no error); delete it so a type-scoped directive need not suppress the sibling `!ok` operand.
- **Go randomizes map iteration, so a `continue` in a `for … range map` is not testable against a `break` without
  controlling the order.** Restructure to iterate `slices.Sorted(maps.Keys(m))` (behaviour-preserving when the body's
  effects are independent), then a test whose skipped entry sorts first makes a `break` deterministically drop a later
  entry.
- **A `<`-vs-`<=` comparator boundary is unobservable through a caller that feeds it unique keys.** Extract the comparator
  to a named function (`lessMountByGuestPath`) and assert strict weak ordering directly: distinct keys order by value
  (kills a constant-true body) and an equal-key call is irreflexive (kills `<=`).
- **A caller guard duplicated by the callee is an equivalent `BRANCH_IF`.** When `if _, ok := asX(a); !ok { return }`
  merely precedes a call that performs the same guard internally, removing the caller's branch is unobservable; delete
  the redundant guard rather than recording it (the callee's own branch still kills the mutant's intent).
- **An assertion in a *consumer* package does not kill a mutant in a *test-support* package.** gomutants runs only the
  mutated package's tests, so `termio.Mock`'s spinner recording (asserted from `internal/sandbox/vm`) stayed LIVED when
  `internal/termio` was swept — and the double's new guard `return`s were the codecov patch lines, uncovered the same way.
  Assert a test double's recording where the double lives, in the double's own `*_test.go`.
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
- **A formatter can move an inline `disable` off the mutant's line and silently un-suppress it.** `golangci-lint fmt`
  wraps a call whose inline `// gomutants:disable …` pushes it past the width, so the directive lands on the closing `)`
  line while gomutants reports the mutant (e.g. an argument literal like `64`) on an earlier line; the directive then
  matches nothing and the mutant survives. Prefer a `// gomutants:disable-next-line <TYPE> reason="…"` on its own line
  above the statement, which the formatter leaves intact — then re-sweep and confirm the suppressed count rose and
  `Lived` dropped. (A wrapped `if f(x) {` is the exception: `BRANCH_IF` reports at the `{`, which stays on the `) {`
  line, so an inline directive there can still bind.)
- **A `disable` reason must argue the mutation cannot change behaviour, not that the condition is hard to reach.** A
  suppressed branch whose body is removed can still be observable through a caller-supplied input or predicate:
  `filewalk.Walk`'s `rel == "."` root skip looked unreachable until a filter that rejects `"."` was considered — the
  mutation then calls `filter(".", true)`, which returns `SkipDir` and prunes the whole tree. Trace the fall-through
  under a hostile caller before recording a branch.
- **Prove unobservability before recording — a too-eager record hides a testable change and the package's real state.**
  Hand-apply the mutant and try to make a test fail before falling back to a record; "the condition looks unreachable" is
  not a reason. `filewalk.Walk`'s `err != nil` guard was recorded as unreachable, but `filepath.WalkDir` calls the
  callback a *second* time carrying the `ReadDir` error when a directory cannot be read, so a filter that removes the
  directory mid-walk reaches the mutated guard, which re-invokes the filter for the same entry — a call-counting test
  kills it. The wrong record also masked the package's true state: `filewalk` read "0 LIVED" in the PR while two
  `readFile` success-return mutants survived untriaged, so re-sweep the whole package (`FULL=1 PKG=…`) after triage
  instead of trusting the count.
- **`os.ReadDir` and `os.ReadFile` return partial results *alongside* an error, so "returns nil on error" is a false
  equivalence.** Both document returning what they read before the error (`os.ReadDir`: "the entries it was able to read
  before the error"; `os.ReadFile`: the bytes read so far). Removing an `if err != nil { continue }` guard after either
  is observable: the partial directory entries get merged, or the partial bytes get parsed (a truncated `MemTotal` line
  yields a nonzero memory figure). Kill it with a function-variable seam (`var readDir = os.ReadDir` /
  `var readMeminfo = os.ReadFile`), have the test return `(partial, err)`, and assert the partial result is discarded —
  the directory is skipped, or the value is 0. Check any "returns nil on error" reason against the stdlib's documented
  behaviour before recording it; the wrong claim is worse than an honest "untestable" record.
- **A clamp's literal bounds are observable: parameterize the helper and table-test literal limits, never suppress the
  constants.** `min(max(runtime.NumCPU(), 1), maxCPUs)` reads as unreachable, but `1`→`2` changes the result on a
  one-CPU host and `maxCPUs` (255)→`254` changes it at 255 CPUs. Extract `clampNumCPUs(cpus int) uint8` and test the
  boundaries. The table's values must be **literals** (0/1, 254/255/256), not `maxCPUs`-relative: a symbolic expectation
  moves with the mutated constant, so the decrement compiles, passes, and LIVEs (`{maxCPUs, maxCPUs}` expected 254 once
  the constant became 254). A literal table kills all four integer mutants on the bounds.
- **Removing `tb.Fatalf` from a test-support helper can be killed with a recording `testing.TB`, or a child process.**
  A test-package fake that embeds `testing.TB` (which satisfies its private method), overrides `Helper`/`TempDir`, and
  makes `Fatalf` append the message then `panic` a sentinel lets the test `recover` and assert the report
  deterministically — cheaper than the child-process form (see `internal/upgrade/upgrade_extra_test.go`). Only the
  helper's input and `tb.TempDir()` are injectable: a failure mid-sequence (between `PlainInit` and `Commit`) still
  needs a production seam, so record those when the test costs more than it is worth. `yaml.Marshal` rejects a `chan`
  with a `panic`, not an error — reach a marshal-error branch with a value implementing `MarshalYAML() (any, error)`.
- **`t.Helper()` changes stack-trace metadata, not the helper's result.** Record its removal when checking the reported
  location would add little value; do not invent a production seam just to make it killable.
- **In `make([]T, len, cap)` the length is observable and the capacity is not; delete the capacity hint.** The length
  mutant (`0`→`1`) leaves a leading zero element, so an exact-contents assertion kills it, but the capacity mutants
  (`cap`→`cap±1`) change nothing a caller can see. Replace `make([]T, 0, 3)` with `var xs []T` to remove the capacity
  family outright, and assert the returned slice exactly to kill the length one.
- **A map's size hint is the same trap with no observable fallback: drop the hint.** Go exposes no capacity (or length)
  for a map, so `make(map[string]string, len(a)+len(b))` yields a LIVED `ARITHMETIC_BASE` on the `+` that no assertion
  can reach — the negative hint is clamped to zero internally. Write `make(map[string]string)`; the pre-size is
  unobservable at any input, so there is nothing to record and nothing to test.
- **A guard whose fields are only *partly* redundant is best extracted and table-tested with synthetic values.**
  `parsedA.Slug == "" || parsedA.Agent == "" || parsedA.Digest != ""` (`sameImageIdentity`): `ParseImageTag` never yields
  a digest with an agent, so `Digest != ""` is fully redundant, and it yields an agent with an empty slug only for a
  pathological `"agents-sandbox/runner-:agent"` ref, so `Slug == ""` is redundant for every real ref. Rather than craft
  that ref, pull the guard into a predicate (`isNameImage`/`hasNameIdentity`) returning `Slug != "" && Agent != "" [&&
  Digest == ""]` and table-test it directly: one row per field makes that field's removal change the result (`{Agent:
  "opencode"}` with no slug fails the `Slug` row), and the `&&`→`||` inversion fails too. Asserting the *predicate*
  keeps the caller's guard intact instead of deleting a check for good. The predicate's rows do **not** kill the
  caller's own `if !isNameImage(parsed) { return false }` (`sameImageRef`, #412): the following `parsed.Slug !=
  image.Slug || parsed.Agent != image.Agent` subsumes it for every complete desired identity, because `ParseImageTag`
  never yields a digest together with an agent, so a digest-pinned parse is rejected by the pair comparison too. Only a
  direct call with an *incomplete* identity — a slug-only or empty `ImageIdentity`, which the sole production caller's
  `image.Ref() != ""` precondition excludes — makes removing the guard observable; assert that row rather than deleting
  a check that is not redundant as a total function.
- **A `x != "" && x == y` guard is killable when *both* sides can be empty; "the caller's `y` is never empty" is not proof of redundancy.**
  `cachedImageMatches` returns `mInspect.Digest != "" && mInspect.Digest == info.Digest` (#410). Each earlier table row varied
  only one side, so removing the middle operand looked equivalent — but with a cached image that records no digest *and* an
  empty `info.Digest`, the mutant's bare `"" == ""` returns `true` while the original returns `false` (reload). The
  distinguishing input is the one where both sides are empty, which is exactly the state the guard exists to reject. Add
  that row; do not generalize from "only one side is ever empty in production".
- **`if len(x) > N { x = x[:N] }`: only one of the condition's integer mutants is observable.** `>N`↔`>=N` and `N`→`N-1`
  differ only when `len==N`, where both branches leave the same `N` characters, so they are equivalent — record them
  with `disable-next-line CONDITIONALS_BOUNDARY,INTEGER_DECREMENT`. Only `N`→`N+1` changes behaviour (an `N+1`-length
  input stops truncating), so kill it with a boundary-length input. The `x[:N]` on the next line is a *separate* mutant
  family (`INTEGER_*` on the slice constant) already killed by a long-enough input.
- **A reflective derivation's skip guards can be unobservable through the production type.** `viperconfig`'s
  `walkConfigKeys` skips unexported fields and untagged fields, but `Config` has no unexported *tagged* field and no
  untagged exported field, so removing either guard changed no derived key (the `BRANCH_IF` body removal and the
  `EXPRESSION_REMOVE` on `!tagged` → `false` both survived). Pass the walk a synthetic `reflect.Type` with one field
  per guard (an unexported tagged field, an untagged exported field) and assert the returned keys — both guards are
  real contracts, so recording them would be wrong.
- **A non-positive-age guard is killable with a *future* timestamp, so do not record it as equivalent.**
  `if age <= 0 { return false }` (`imageWithinAge`) reads as always-true-equivalent because for a past timestamp the
  fall-through `time.Since(used) < age` is also false. A hostile caller gets through: set `LastUsedAt` to
  `now.Add(time.Hour)` so `time.Since` is negative, and the fall-through returns true — that single subtest kills the
  `BRANCH_IF`, the `<=`→`<` boundary, and the `0`→`-1`/`0`→`1` decrement/increment. Only the downstream `<` vs `<=` on
  the final comparison (nanosecond equality, no injectable clock) still needs a record.
- **Two calls to the same test seam need an invocation count, not a presence flag.** `adoptKeysLockedHook` fires once
  before the destroy and again after the state move; a test that sets `probed = true` in the hook passes when either
  call alone is removed, so the `BRANCH_IF` and `STATEMENT_REMOVE` at *both* sites stayed LIVED. Count the calls and
  assert the exact number.
- **A guard whose error is re-derived identically by the step it protects is unobservable until the guard adds its own
  context.** `adoptLegacyIdentity`'s `if err != nil { return err }` after the lock-set derivation falls through, under
  the mutant, into the migration that re-derives the same enumeration and fails with the same message — so a
  message-only assertion still passes. Wrap the guard's error (`fmt.Errorf("derive adoption lock set: %w", err)`),
  assert the wrapper's text to kill the `BRANCH_IF`, and assert `errors.Is` to the cause to kill the `ERRORF_WRAP`
  (`%w`→`%v`) the wrapper itself introduces.
- **Assert a mode literal through the injected seam, not by `os.Stat`.** `os.MkdirAll(dir, 0o700)` /
  `OpenFile(…, 0o600)` mutate to `0o701`/`0o601`, which the process umask may or may not preserve (`0o701 &^ 0o022`
  is `0o701`, but `&^ 0o077` is `0o700`). Replacing the `mkdir`/`open` seams with a recorder makes the argument
  assertion deterministic and umask-free.
- **A `blocking bool` flock seam's blocking branch is observable in-process.** Mutating `if blocking { return
  hostlock.Exclusive(f) }` to fall through to the non-blocking call is invisible while the lock is free (both succeed)
  and unreachable through the same-process registry (it fails before the flock), but a test that holds a raw flock on a
  second open file description and calls the seam from a goroutine with `blocking=true` sees an immediate contention
  error under the mutant and a block-then-success under the original. The "must not have returned" window costs
  nothing and cannot be flaky: the correct code cannot return while the lock is held.
- **A "drop the rune" sentinel is equivalent under any negative value — record it.** `strings.Map`'s contract drops the
  rune for *any* negative mapping result, so `markerText`'s `return -1` mutated to `return -2` changes nothing; a
  `// gomutants:disable-next-line INTEGER_INCREMENT reason="strings.Map drops the rune for any negative mapping"` closes
  it. Confirm the stdlib's documented tolerance before assuming a sentinel's exact value is load-bearing.
- **A wall-clock budget constant is observable as an exact value; pin it with a literal test.** `heavyweightReadyTimeout`'s
  `120` mutated to `119`/`121` and its `*` to `/` (integer constant division makes the Duration `0`, silently disabling
  the ceiling) all survived a test that compared the spec field *to the constant itself*; a test asserting
  `heavyweightReadyTimeout != 120*time.Second` — a literal expected value in the test file, as for
  `autoPruneDefaultThreshold` — kills the whole family.

## Operational

- A cold `FULL=1` run of one package (`internal/agent`, ~320 mutants) exceeds 15 minutes in a fresh worktree; reruns
  resume from `.gomutants-cache.json` and are much faster. Run it in the background.
- **Scope a branch or PR sweep with `SINCE=<base ref>`, not the release-tag default.** `SINCE` defaults to the last
  release tag, so a branch sweep covers every unreleased line rather than the branch's own. Measured on
  `internal/sandbox/image` (cold `.gomutants-cache.json` each run, warm Go build cache): the default `SINCE=v0.6.0` found
  42 mutants in 45s, `SINCE=origin/main` found 3 in 19s, and `SINCE=HEAD` found 0 and paid a ~7s floor — discovery and the
  coverage pass are package-scoped, so the module-wide floor the CI job pays is larger than this single-package one. The
  CI `mutation` workflow already scopes a PR to `SINCE=origin/<base_ref>`; use `SINCE=origin/main` locally to match, and
  reserve the bare default for the release sweep.
- **A mutant that removes loop progress hangs; it reports `TIMED OUT`, not `LIVED`.** `STATEMENT_REMOVE` on a loop's
  `i++` or `i = f(...)` never advances, so the package's test run never returns (locally the worker hits the 2048 MiB
  cap and is killed — same symptom). No assertion can catch it; leave it and read `LIVED`, not `TIMED OUT`, as the
  survivor count.
- `make mutation FULL=1 PKG=./internal/agent/` writes `mutation-report.json` (gitignored) plus the cache into the
  worktree. Read the result with `jq`:
  `jq -r '[.files[] as $f | $f.mutations[] | select(.status=="LIVED") | "\($f.file_name):\(.line) \(.type)"] | .[]'`.
