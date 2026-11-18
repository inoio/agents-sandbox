---
description: TDD plan for a concise `make check` - a check-level runner that captures each target, prints one PASS/FAIL line per target, and dumps failing logs last; heavy targets serial / light in parallel; VERBOSE/SERIAL modes; plus quieting the fixture harnesses' per-case lines.
read_if: Executing the concise-check-output change; or working on `scripts/check.sh`, the `CHECK_HEAVY_TARGETS` split, the `check`/`verify` targets, the `VERBOSE`/`SERIAL` flags, or a fixture harness's `ok()`/section output.
---

# Concise `make check` Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make a green `make check` a short per-target summary with every failing target's captured log at the end, and stop the CPU-heavy targets from overlapping.

**Architecture:** A shell runner (`scripts/check.sh`) replaces the `$(MAKE) -j$(JOBS) -O $(CHECK_TARGETS)` recursion. It launches each target as its own `make` invocation, captures stdout+stderr per target, runs the heavy targets one at a time while the light targets run in a bounded parallel pool, prints `PASS|FAIL <target> <n>s` as each finishes, then prints the captured log of every failure. `VERBOSE=1` bypasses capture and streams; `SERIAL=1` runs one target at a time. Separately, the fixture harnesses that print one line per passing case go quiet by default (print the case only on failure or `VERBOSE=1`).

**Tech Stack:** GNU Make, bash, the repo's `scripts/*.sh` + `scripts/test-*.sh` convention.

**Spec:** The handoff design `/tmp/opencode/handoff-draft.md` and the "Local check loop" contract in `.operator-shared/specs/ci/quality-gates.md`.

## Global Constraints

- The Makefile owns the split: `CHECK_HEAVY_TARGETS`, `CHECK_LIGHT_TARGETS`, `CHECK_TARGETS = $(CHECK_HEAVY_TARGETS) $(CHECK_LIGHT_TARGETS)`, `VERIFY_TARGETS = $(CHECK_TARGETS) test-race vulncheck deadcode`. The fail-safe full set is obtained by **running make**, never by grepping the raw line: a `print-check-targets` target echoes `$(CHECK_TARGETS)`, and `scripts/changed-targets.sh` (and `scripts/test-changed-targets.sh`) invoke it as `MAKEFLAGS= "${MAKE:-make}" -s --no-print-directory -C <repo root> print-check-targets`. This reads the accumulated definition, so the fail-safe cannot drift from what `make check` runs. `print-check-targets` is not a member of `CHECK_TARGETS`.
- Heavy set for `check`, exact: `validate-fmt lint test compile-all`. Heavy set for `verify`, exact: those plus `test-race vulncheck deadcode`.
- Flags: `VERBOSE=1` streams raw output (no capture, no per-target status lines); `SERIAL=1` runs one target at a time in the given order, internal parallelism intact. `JOBS=<n>` bounds the light pool (default `max(2, nproc/4)`, matching the current Makefile expression).
- The runner invokes targets via `${MAKE:-make} --no-print-directory <target>` and clears `MAKEFLAGS` for the child so no jobserver warning is emitted. Tests override `MAKE`.
- `check-test` joins `CHECK_TARGETS` and `changed-targets.sh`'s map (`scripts/check.sh`/`scripts/test-check.sh` → `check-test`).
- Per `AGENTS.md`: default to TDD; `make fmt` / `make lint` (never `go vet`/`go fmt`); test a unit in a file named after it; add `changelog.d/<PR>.changed.md`; keep `docs/` (except `docs/superpowers`) in sync; update `.operator-shared/specs/ci/quality-gates.md` and the `.operator-shared/catalog.md` plan entry.
- Behaviour doubles: `scripts/test-check.sh` is fixture-only (a stub `make`), it never runs the real `make check` (that would recurse).

## Review Focus

- **A heavy target that is not a real target** — a typo in `CHECK_HEAVY_TARGETS` now lands in `CHECK_TARGETS` too, so `make check` fails with "No rule to make target", and the runner must reject a heavy target absent from the list it was handed. Expected: a loud failure, never a silent skip or an overlap.
- **A target whose recipe is already running under `make -j`** — a user's own `-j8` must not leak into a child `make` and double-parallelize. Expected: child `make` runs with `MAKEFLAGS` cleared; `JOBS=` is the only knob.
- **Space in `CHECK_HEAVY_TARGETS`/`JOBS` unset** — `JOBS` unset must fall back to the computed default, not empty. Expected: default pool size.
- **A failing target's log is huge** — dumping all failures must not interleave other targets' output. Expected: failure logs are printed after all targets finish, in the order the targets were given.
- **Direct harness invocation** — a developer running `ci/test-check-ci-guards.sh` by hand with no `VERBOSE` must still see which case failed and the final tally. Expected: failing cases and the summary always print.

---

### Task 1: `scripts/check.sh` — classify, run serially with capture, summarise, dump failures

**Files:**
- Create: `scripts/check.sh`
- Create: `scripts/test-check.sh`

**Interfaces:**
- Consumes: env `CHECK_HEAVY_TARGETS` (space list), `JOBS`, `MAKE`, `VERBOSE`, `SERIAL`.
- Produces: `scripts/check.sh [--help] <target>...`, exit 0 all pass / 1 any fail / 2 usage or a heavy target missing from the target list. Prints `PASS <target> <n>s` / `FAIL <target> <n>s` per target, then `--- FAIL <target> ---` and the captured log for each failure. Also `--heavy-subset <target>...`, which prints the `CHECK_HEAVY_TARGETS` entries among its arguments (used by Task 4's recipes).

- [ ] **Step 1: Write the failing test** in `scripts/test-check.sh` (fixture-only; pass/fail core, no concurrency yet).

Create a temp dir holding a stub `make` and helpers, then assert on the runner's stdout and status. Assertions:

```bash
# stub make: prints a marker to stdout+stderr, exits per a control file
#   $STUB_DIR/fail-<target> present => exit 1
# cases:
#  - all pass:  status 0; stdout has "PASS alpha 0s"; no "--- FAIL"
#  - one light fails: status 1; stdout has "FAIL beta"; stdout has "--- FAIL beta ---"
#               and beta's captured "stderr beta" line appears after all PASS lines
#  - one heavy fails: same, with a heavy target
#  - heavy target not in the target list: status 2; stderr names the target
```

- [ ] **Step 2: Run to verify it fails**

Run: `bash scripts/test-check.sh`
Expected: FAIL (no `scripts/check.sh`).

- [ ] **Step 3: Implement `scripts/check.sh`.**

Structure: `set -euo pipefail`; a usage block in the `sed -n '2,/^$/p'` house style; resolve `root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"`; parse `--help` and the target list; read `heavy="${CHECK_HEAVY_TARGETS:-}"`; compute the light set; validate every heavy token is among the targets (else exit 2). Then, for Task 1, run every target **serially** in the given order:

```bash
run_target() {  # <target> <logfile>
  local target="$1" log="$2" status
  ( cd "$root" && MAKEFLAGS= "${MAKE:-make}" --no-print-directory "$target" ) >"$log" 2>&1 && status=0 || status=$?
  ...
}
```

Record each target's status in `$tmp/status/<index>`; print `PASS|FAIL <target> <SECONDS delta>s`; keep the ordered list `run_order` as an indexed array. After all targets, print each failure as `--- FAIL <target> ---` followed by its log, in `run_order`. Exit 1 if any failed. `trap 'rm -rf "$tmp"' EXIT`.

- [ ] **Step 4: Run to verify it passes**

Run: `bash scripts/test-check.sh`
Expected: summary count, no `FAIL`.

- [ ] **Step 5: Commit**

```bash
git add scripts/check.sh scripts/test-check.sh
git commit -m "chore(check): add a capturing check runner"
```

### Task 2: Scheduling — heavy serial, light parallel pool, `SERIAL`/`VERBOSE`

**Files:**
- Modify: `scripts/check.sh`
- Modify: `scripts/test-check.sh`

**Interfaces:**
- Produces: default schedule = heavy one-at-a-time, light in a pool of `JOBS`; `SERIAL=1` = everything one-at-a-time; `VERBOSE=1` = stream raw output, no capture and no status lines.

- [ ] **Step 1: Write the failing tests** (append to `scripts/test-check.sh`).

The stub `make` writes `start <target>` / `end <target>` lines to `$STUB_DIR/order` around a `sleep 0.3`, so ordering is observable. Assertions:
```bash
# light parallelism: two light targets => "start A" before "end A" AND "start B" before "end A"
# heavy serial: two heavy targets  => "end A" before "start B" (no interleave)
# SERIAL=1: two light targets      => "end A" before "start B"
# VERBOSE=1: stub's stdout marker appears verbatim; no "PASS " line
```

- [ ] **Step 2: Run to verify it fails**

Run: `bash scripts/test-check.sh`
Expected: FAIL on the new ordering assertions.

- [ ] **Step 3: Implement the scheduling in `scripts/check.sh`.**

Portable bash-3.2 pool (no `${arr[-1]}`, no `wait -n`): maintain a `pids=()` indexed array and a counter; `launch` starts a background `run_target`, appends `$!`; `reap_one` does `wait "${pids[0]}"` then drops the head. Membership test for heavy targets via a loop over `heavy`. Light pool width `${JOBS:-$(...)}` where the fallback is the same `nproc`/`hw.ncpu` expression the Makefile uses. Default schedule: start the light pool in the background, run the heavy targets serially in the foreground, then wait for the pool. `SERIAL=1` forces width 1 and puts everything through one ordered queue. `VERBOSE=1` skips capture: run targets with output inherited (`>&2` is fine) and print no status lines.

- [ ] **Step 4: Run to verify it passes**

Run: `bash scripts/test-check.sh`
Expected: PASS.

- [ ] **Step 5: Run the real runner once** against a couple of fast targets to check the feel:
`CHECK_HEAVY_TARGETS="validate-fmt" JOBS=2 ./scripts/check.sh validate-fmt shellcheck`
Expected: two `PASS` lines, exit 0.

- [ ] **Step 6: Commit**

```bash
git add scripts/check.sh scripts/test-check.sh
git commit -m "chore(check): schedule heavy targets serially and light in parallel"
```

### Task 3: Wire the Makefile and the change map

**Files:**
- Modify: `Makefile` (the `JOBS`/`CHECK_TARGETS` block, a `print-check-targets` recipe, `check`/`verify`, a `check-test` recipe, `.PHONY`)
- Modify: `scripts/changed-targets.sh` (read the full set via make; add a `scripts/check.sh|scripts/test-check.sh` case)
- Modify: `scripts/test-changed-targets.sh` (read `ALL` via make; cases for the new mapping)

**Interfaces:**
- Produces: `CHECK_HEAVY_TARGETS`, `CHECK_LIGHT_TARGETS`, `CHECK_TARGETS = $(CHECK_HEAVY_TARGETS) $(CHECK_LIGHT_TARGETS)`, `VERIFY_HEAVY_TARGETS`, a `print-check-targets` target, a `check-test` target, and `check`/`verify` delegating to the runner.
- Consumes: `scripts/check.sh` from Tasks 1–2.

- [ ] **Step 1: Write the failing tests.**

In `scripts/test-changed-targets.sh`, replace the `ALL="$(grep … CHECK_TARGETS …)"` computation with the same make invocation the map will use:
```bash
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
ALL="$(MAKEFLAGS= "${MAKE:-make}" -s --no-print-directory -C "$REPO_ROOT" print-check-targets \
  | tr ' ' '\n' | LC_ALL=C sort -u | tr '\n' ' ' | sed 's/ *$//')"
```
and add `run_local` cases:
```bash
run_local "modified check runner"      scripts/check.sh      "# x" "check-test shellcheck"
run_local "modified check runner test" scripts/test-check.sh "# x" "check-test shellcheck"
```

- [ ] **Step 2: Run to verify they fail**

Run: `bash scripts/test-changed-targets.sh`
Expected: FAIL (no `print-check-targets` target, so `ALL` is empty; no `check-test` mapping).

- [ ] **Step 3: Implement the Makefile.**

Replace the literal `CHECK_TARGETS` with the split (heavy = the four CPU-bound targets; light = the rest in their current order), add `check-test` to the light list, and add the print target:
```make
CHECK_HEAVY_TARGETS = validate-fmt lint test compile-all
CHECK_LIGHT_TARGETS = actionlint shellcheck docs-linkcheck docs-linkcheck-test ci-guards-check ci-guards-check-test event-base-test classify-changes-test changelog-check changelog-check-test deadcode-test changed-files-test changed-targets-test cleanup-branches-test surface-sweep-test envelope-test upstream-drift-test rebase-status-test enqueue-pr-test backport-audit-test open-backport-pr-test check-test
CHECK_TARGETS = $(CHECK_HEAVY_TARGETS) $(CHECK_LIGHT_TARGETS)
VERIFY_HEAVY_TARGETS = $(CHECK_HEAVY_TARGETS) test-race vulncheck deadcode
VERIFY_TARGETS = $(CHECK_TARGETS) test-race vulncheck deadcode
```
Add the print recipe (internal; never a `CHECK_TARGETS` member) and the `check-test` recipe; add both names to `.PHONY`:
```make
print-check-targets:
	@printf '%s\n' $(CHECK_TARGETS)

check-test:
	bash scripts/test-check.sh
```
Rewrite the `JOBS` comment to describe the runner. Replace the `check`/`verify` recipes with the runner delegation:
```make
check:
	@CHECK_HEAVY_TARGETS="$(CHECK_HEAVY_TARGETS)" JOBS=$(JOBS) ./scripts/check.sh $(CHECK_TARGETS)
verify:
	@CHECK_HEAVY_TARGETS="$(VERIFY_HEAVY_TARGETS)" JOBS=$(JOBS) ./scripts/check.sh $(VERIFY_TARGETS)
```

- [ ] **Step 4: Implement the map change in `scripts/changed-targets.sh`.**

Replace the `makefile="…/Makefile"` + `grep`/`head` block (lines ~63–72) with the make invocation, keeping the same error path (`exit 2`, message naming the repo root) when it fails or is empty:
```bash
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
all_targets_line="$(MAKEFLAGS= "${MAKE:-make}" -s --no-print-directory -C "$repo_root" print-check-targets 2>/dev/null)" || all_targets_line=""
if [[ -z "$all_targets_line" ]]; then
  printf 'changed-targets: cannot read CHECK_TARGETS from %s\n' "$repo_root/Makefile" >&2
  exit 2
fi
read -r -a all_targets <<<"$all_targets_line"
```
Update the header comment (lines 7–9) and the block comment (lines 63–65) to say the full set is printed by make's `print-check-targets`. Add the mapping case beside the other `scripts/*` cases:
```bash
scripts/check.sh|scripts/test-check.sh) add_targets check-test; known=1 ;;
```
(the generic `*.sh` rule adds `shellcheck`).

- [ ] **Step 5: Run to verify they pass**

Run: `bash scripts/test-changed-targets.sh`
Expected: PASS, including the existing "branch with missing base selects all" case comparing against the make-printed set.

- [ ] **Step 6: Run the wired gate**

Run: `make check-local-changes`
Expected: concise PASS lines, exit 0.

- [ ] **Step 7: Commit**

```bash
git add Makefile scripts/changed-targets.sh scripts/test-changed-targets.sh
git commit -m "chore(check): split heavy targets and delegate check/verify to the runner"
```

### Task 4: Delegate the inner-loop targets to the runner

**Files:**
- Modify: `Makefile` (`check-local-changes`, `check-branch-changes`)

**Interfaces:**
- Consumes: the runner from Task 1–2.
- Produces: the two loops print the same concise PASS/FAIL form as `check`.

- [ ] **Step 1: Change both recipes**

Replace the final `$(MAKE) --no-print-directory -j$(JOBS) -O $$targets changelog-presence MODE=local` (and the `branch` analogue) with the runner. The runner rejects a heavy target absent from its target list, and the selection is a subset of `CHECK_HEAVY_TARGETS`, so pass only the heavy targets that are in the selection:
```make
heavy="$$(CHECK_HEAVY_TARGETS="$(CHECK_HEAVY_TARGETS)" ./scripts/check.sh --heavy-subset $$targets)"; \
MODE=local CHECK_HEAVY_TARGETS="$$heavy" JOBS=$(JOBS) ./scripts/check.sh $$targets changelog-presence
```
and the `branch` one with `MODE=branch` plus the existing `$${BASE:+BASE=$$BASE}`. `changelog-presence` uses `MODE ?= local`, which respects an environment `MODE`. `--heavy-subset` is the runner's internal mode that prints the `CHECK_HEAVY_TARGETS` entries among its arguments; it is unit-tested in `scripts/test-check.sh`.

- [ ] **Step 2: Verify manually**

Run: `make check-local-changes`
Expected: concise PASS lines for the selected targets plus `changelog-presence`, exit 0.

- [ ] **Step 3: Commit**

```bash
git add Makefile
git commit -m "chore(check): use the capturing runner for the inner-loop checks"
```

### Task 5: Quiet the `ok()`/`bad()` fixture harnesses

**Files (all 13, each has an identical `ok()`/`bad()` pair):**
- Modify: `ci/test-check-ci-guards.sh`, `ci/test-check-classify-changes.sh`, `ci/test-check-event-base.sh`, `scripts/test-backport-audit.sh`, `scripts/test-changed-files.sh`, `scripts/test-changed-targets.sh`, `scripts/test-cleanup-merged-branches.sh`, `scripts/test-enqueue-pr.sh`, `scripts/test-envelope_test.sh`, `scripts/test-open-backport-pr.sh`, `scripts/test-rebase-status.sh`, `scripts/test-surface-sweep.sh`, `scripts/test-upstream-drift.sh`

**Interfaces:**
- Produces: `ok()` prints only when `VERBOSE=1`; `bad()` always prints; the final `N passed, M failed` always prints (already present). `ci/test-check-ci-guards.sh` and `scripts/test-surface-sweep.sh` do not have the shared `pass/fail` pair, so adapt each to the local names it already uses (`pass`/`fail`, or `failures`).

- [ ] **Step 1: Rewrite each `ok()`**

`scripts/test-surface-sweep.sh` uses `ok() { echo "ok - $1"; }`; change it and every `ok()  { pass=$((pass + 1)); printf 'ok   - %s\n' "$1"; }` to:
```bash
ok() { pass=$((pass + 1)); if [[ "${VERBOSE:-0}" == 1 ]]; then printf 'ok   - %s\n' "$1"; fi; }
```
Keep each file's existing counter variable and exact `printf` format. `bad()` is unchanged.

- [ ] **Step 2: Make section headers conditional**

For each line matching `^echo "# ` (65 lines across these files), add a `section()` helper beside `ok()`:
```bash
section() { if [[ "${VERBOSE:-0}" == 1 ]]; then printf '# %s\n' "$1"; fi; }
```
and rewrite the header as `section "<text>"` (drop the leading `# `). Do this only for top-level header lines — verify with `grep -nE '^echo "# ' <file>` that no match sits inside a fixture heredoc before rewriting, and re-run that grep after to confirm every one moved.

- [ ] **Step 3: Verify each harness still passes (quietly and verbosely)**

Run: `for f in ci/test-check-ci-guards.sh ci/test-check-classify-changes.sh ci/test-check-event-base.sh scripts/test-backport-audit.sh scripts/test-changed-files.sh scripts/test-changed-targets.sh scripts/test-cleanup-merged-branches.sh scripts/test-enqueue-pr.sh scripts/test-envelope_test.sh scripts/test-open-backport-pr.sh scripts/test-rebase-status.sh scripts/test-surface-sweep.sh scripts/test-upstream-drift.sh; do bash "$f" || echo "FAILED $f"; done`
Expected: each prints only its summary line; none says FAILED. Then `VERBOSE=1 bash scripts/test-changed-files.sh` shows the case lines and headers.

- [ ] **Step 4: Commit**

```bash
git add ci/test-check-*.sh scripts/test-*.sh
git commit -m "test(check): print fixture cases only on failure or VERBOSE"
```

### Task 6: Quiet the three bare `ok` printers

**Files:**
- Modify: `ci/test-check-docs.sh:77`, `ci/test-check-changelog.sh:39,109,191`, `ci/test-check-deadcode.sh:111`

**Interfaces:**
- Produces: `printf 'ok   %s\n'` / `printf 'ok %s\n'` becomes conditional on `VERBOSE=1`; the `FAIL` branches and the final tally are unchanged.

- [ ] **Step 1: Guard each success print**

Wrap the `printf 'ok ...'` / `printf 'ok %s\n'` line (each is immediately before `pass=$((pass + 1))` inside an `expect*` helper) in `if [[ "${VERBOSE:-0}" == 1 ]]; then ... fi`, keeping the exact existing format string.

- [ ] **Step 2: Verify**

Run: `bash ci/test-check-docs.sh && bash ci/test-check-changelog.sh && bash ci/test-check-deadcode.sh`
Expected: one summary line each; `VERBOSE=1 bash ci/test-check-docs.sh` prints the `ok   <case>` lines.

- [ ] **Step 3: Commit**

```bash
git add ci/test-check-docs.sh ci/test-check-changelog.sh ci/test-check-deadcode.sh
git commit -m "test(check): print docs/changelog/deadcode cases only on failure or VERBOSE"
```

### Task 7: Document the runner and close the plan

**Files:**
- Modify: `.operator-shared/specs/ci/quality-gates.md` (the "Local check loop" section, lines ~108–114)
- Modify: `AGENTS.md` (`make check` bullet, lines ~65–67)
- Modify: `CONTRIBUTING.md` (the command table and the GNU Make `-O` note, ~lines 15 and 71–74)
- Modify: `.operator-shared/catalog.md` (add the plan entry under `plans/`)
- Create: `changelog.d/<PR>.changed.md`
- Modify: `docs/` if any page documents `make check` (grep first; none expected)

**Interfaces:**
- Produces: the spec states the runner contract (capture, heavy-serial/light-parallel, `VERBOSE`/`SERIAL`, failure logs last) and the new `CHECK_HEAVY_TARGETS` split; the plan entry is cataloged.

- [ ] **Step 1: Update the spec**

In `.operator-shared/specs/ci/quality-gates.md`, rewrite the "Local check loop" paragraph so it describes: `check`/`verify` delegating to `scripts/check.sh`; the `CHECK_HEAVY_TARGETS`/`CHECK_LIGHT_TARGETS` split with `CHECK_TARGETS = $(CHECK_HEAVY_TARGETS) $(CHECK_LIGHT_TARGETS)`; that `changed-targets.sh` reads the full set by running make's `print-check-targets` (so the fail-safe reflects the accumulated definition); heavy serial + light parallel; `VERBOSE=1` streaming; `SERIAL=1`; failure logs last. Keep the `changed-targets.sh`/changelog-presence paragraphs, updating only what changed.

- [ ] **Step 2: Update `AGENTS.md` and `CONTRIBUTING.md`**

State that `make check` captures each target and prints a one-line summary, dumping failures last; `VERBOSE=1` streams; `SERIAL=1` for a shared box; `JOBS=` bounds the light pool. Drop the GNU Make 4.0+ `-O` requirement for `make check` (it is only used by the inner-loop loops, if still used) or reword it accurately.

- [ ] **Step 3: Catalog the plan and add the fragment**

Add the plan's catalog entry under `plans/` in `.operator-shared/catalog.md` (Description + Read If), and create `changelog.d/<PR>.changed.md` with a one-paragraph summary. Run `./ci/check-changelog.sh` and `./scripts/brain-check.sh` to confirm both are valid.

- [ ] **Step 4: Run the gate**

Run: `make check`
Expected: ~25 concise PASS lines, exit 0, peak load near core count (compare against the handoff's ~31 baseline). Then `JOBS=2 make check` and `SERIAL=1 make check` for the other modes.

- [ ] **Step 5: Commit**

```bash
git add .operator-shared/specs/ci/quality-gates.md AGENTS.md CONTRIBUTING.md .operator-shared/catalog.md changelog.d/
git commit -m "docs(check): document the concise check runner"
```

## Open items carried from the handoff

- The `JOBS` sweep was single-mode only; Task 7's Step 4 records the mixed schedule's wall time and peak against the `-j4` figure (86.4 s / peak 16.2) before/after.
- A true single-core mode (`GOMAXPROCS=1`, `go test -p=1`, `golangci-lint --concurrency=1`) is out of scope; add only if asked.
- Making `check-branch-changes` a *gate* (needs a reachability test over the change map and a fetched `origin/main`) is a separate policy change, not this plan.
