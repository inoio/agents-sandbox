# Hermetic test envelope Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `make test-scrubbed` and `make test-hermetic`, which run the default suite from a copy of the module outside any git checkout with a scrubbed environment (and, for the strict target, a network namespace), then use them to record the failing inventory and runtime budget.

**Architecture:** One bash driver, `scripts/test-envelope.sh`, copies the module into a temp work dir (rsync, `.git` excluded), builds a minimal `env -i` environment, and runs `make test` (or an injected command) there; `--netns` wraps the command in `unshare` with loopback brought up. A fixture-only test script asserts the envelope's properties and never touches this repo. Makefile targets expose the driver, and the first real run produces the inventory that scopes the follow-up plans.

**Tech Stack:** bash, rsync, `env -i`, `unshare`/`ip` (Linux), make, Go toolchain.

**Spec:** `.operator-shared/plans/2026-11-07-hermetic-test-suite-design.md` (sections "Acceptance bar", "Sequencing" step 1, "Left to the builder"). This is Plan 1 of that design; the surface fixes (identity, host figures + sentinel, wall-clock) and the cadence wiring become follow-up plans once this plan's inventory exists.

## Global Constraints

- The driver never writes into the source tree. All writes go under the temp work dir; `GOCACHE`/`GOMODCACHE` stay the developer's pre-warmed caches so `--netns` never needs to download.
- The suite runs with no test-result cache: `GOFLAGS` must contain `-count=1`, so a cached PASS from an in-tree run cannot stand in.
- The `env -i` allowlist is exactly `PATH`, `HOME`, `TMPDIR`, `GOCACHE`, `GOMODCACHE`, `GOFLAGS`, `CGO_ENABLED`, `LANG`, `TZ`. `HOME` and `TMPDIR` point inside the work dir; everything else is dropped.
- The command is `make test` (not bare `go test`), so the `microsandbox_ffi_path` tag from the Makefile applies.
- Shell scripts use `set -euo pipefail`, a usage header, a `--` separator before the command, and the `ok`/`fail` harness of `scripts/test-cleanup-merged-branches.sh`.
- `--netns` is Linux-only and fails loudly off Linux or without `unshare`/`ip`; it never silently degrades to the scrubbed run.

## Review Focus

1. **Source tree untouched** — a run must create or modify nothing under the source dir (a stray marker or cache would make the envelope lie about where tests ran). Task 1 test.
2. **Inherited env dropped** — a host-only variable exported by the caller must not reach the child. Task 1 test.
3. **Cache defeated** — `-count=1` must reach the child and no earlier result may be reused. Task 1 test.
4. **netns not silently skipped** — `--netns` off Linux or without `unshare` exits non-zero with a clear message. Task 2 test.
5. **Paths with spaces** — a source or work path containing a space must survive quoting. Task 1 test.

---

### Task 1: The scrubbed envelope driver

**Files:**
- Create: `scripts/test-envelope.sh`
- Create: `scripts/test-envelope_test.sh`

**Interfaces:**
- Produces: `scripts/test-envelope.sh [--source DIR] [--work-dir DIR] [--keep] [--print] [-- COMMAND...]`.
  - `--source` defaults to the repo root (the script's parent dir, resolved physical).
  - `--work-dir` defaults to `mktemp -d "${TMPDIR:-/tmp}/test-envelope.XXXXXX"`; removed on exit unless `--keep`.
  - Default command is `make test`.
  - `--print` writes `work_dir=`, one `env KEY=VALUE` line per allowlisted var, and `command: ...`, then exits 0 without running.

- [ ] **Step 1: Write the failing test script**

Create `scripts/test-envelope_test.sh` using the `ok`/`fail` harness. It builds fixture modules under one `fixture_root` (removed by an `EXIT` trap), each a directory holding `Makefile` and `check.sh`:

```make
test:
	@sh check.sh
```

```sh
test -n "${HOME:-}" && echo "HOME=$HOME"
echo "PWD=$PWD"
echo "TMPDIR=${TMPDIR:-}"
echo "GOFLAGS=${GOFLAGS:-}"
echo "HOST_ONLY_VAR=${HOST_ONLY_VAR:-}"
: > marker
echo "MARKER"
```

Cases and their assertions:

- `runs in a copy with a scrubbed env`: run `HOST_ONLY_VAR=leak "$ENVELOPE" --source "$fixture" --work-dir "$fixture_root/work" -- make test`; assert exit 0; assert output contains `PWD=$fixture_root/work`; assert `HOME=$fixture_root/work/home` and `TMPDIR=$fixture_root/work/tmp`; assert output does **not** contain `leak`.
- `source tree is untouched`: after the run, assert the fixture dir contains only `Makefile` and `check.sh` (no `marker`).
- `no test-result cache`: assert output contains `GOFLAGS=` with `-count=1`.
- `print does not run`: run `"$ENVELOPE" --source "$fixture" --print`; assert output contains `command: make test` and does not contain `MARKER`.
- `a source path with a space works`: copy the fixture into `"$fixture_root/with space"`, run, assert exit 0 and `PWD=` points at the work dir.

- [ ] **Step 2: Run the test to verify it fails**

Run: `./scripts/test-envelope_test.sh`
Expected: FAIL — `scripts/test-envelope.sh` does not exist.

- [ ] **Step 3: Implement `scripts/test-envelope.sh`**

Parse the flags above. Resolve the source physically. Create the work dir and `"$work/home"`, `"$work/tmp"`. Copy with:

```bash
rsync -a --exclude=.git --exclude=.gomutants-cache.json "$source"/ "$work"/
```

Build the environment as an explicit array and run the child with `env -i`:

```bash
env_args=(
  "PATH=$PATH"
  "HOME=$work/home"
  "TMPDIR=$work/tmp"
  "GOCACHE=${GOCACHE:-$(go env GOCACHE)}"
  "GOMODCACHE=${GOMODCACHE:-$(go env GOMODCACHE)}"
  "GOFLAGS=-count=1${GOFLAGS:+ $GOFLAGS}"
  "CGO_ENABLED=${CGO_ENABLED:-0}"
  "LANG=${LANG:-C.UTF-8}"
  "TZ=${TZ:-UTC}"
)
( cd "$work" && env -i "${env_args[@]}" "$@" )
```

`GOFLAGS` is forced to lead with `-count=1`. `--print` prints the work dir, the array, and the command and returns before the `cd`. `--keep` suppresses the cleanup trap. A non-zero child exit propagates.

- [ ] **Step 4: Run the test to verify it passes**

Run: `./scripts/test-envelope_test.sh`
Expected: PASS — all cases `ok`.

- [ ] **Step 5: Commit**

```bash
git add scripts/test-envelope.sh scripts/test-envelope_test.sh
git commit -m "test: add the hermetic envelope driver"
```

---

### Task 2: The strict network-namespace wrapper

**Files:**
- Modify: `scripts/test-envelope.sh`
- Modify: `scripts/test-envelope_test.sh`

**Interfaces:**
- Consumes: Task 1's flag parser and command runner.
- Produces: a new `--netns` flag. When set, the child runs as
  `unshare --net [--map-root-user] bash -c 'ip link set lo up; exec "$@"' bash COMMAND...`, so
  loopback is up and everything else egresses nowhere.

- [ ] **Step 1: Write the failing tests**

Add to `scripts/test-envelope_test.sh`:

- `netns is rejected off Linux or without unshare`: on a host where `uname -s` is not `Linux` or `command -v unshare`/`command -v ip` is empty, run `"$ENVELOPE" --source "$fixture" --netns --print` and assert a non-zero exit with a message naming the missing prerequisite. On a capable Linux host this case self-skips.
- `netns wraps the command`: when the prerequisites exist, assert `--print --netns` output contains `netns: yes`, `unshare --net`, and `ip link set lo up`.
- `netns actually blocks egress`: guarded by `unshare --net --map-root-user true` succeeding; a fixture whose `check.sh` runs `getent hosts example.com` (or `curl -sS --max-time 2 https://example.com`) must fail, proving the namespace denies external name resolution/egress.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `./scripts/test-envelope_test.sh`
Expected: FAIL — `--netns` is an unknown flag; no `netns:` line.

- [ ] **Step 3: Implement `--netns`**

Detect `Linux` and require both `unshare` and `ip` before doing anything; error otherwise with a message naming the missing piece and the host OS. Build the wrapper (`--map-root-user` when not already root), have `--print` show `netns: yes` and the wrapped command, and prefix the child with it.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `./scripts/test-envelope_test.sh`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add scripts/test-envelope.sh scripts/test-envelope_test.sh
git commit -m "test: add the strict network-namespace envelope"
```

---

### Task 3: Makefile targets

**Files:**
- Modify: `Makefile`

**Interfaces:**
- Consumes: `scripts/test-envelope.sh`.
- Produces: `test-scrubbed`, `test-hermetic`, `envelope-test` targets; `envelope-test` added to `check` and `verify`.

- [ ] **Step 1: Add the targets and the `.PHONY` entries**

```make
# Run the default suite in a hermetic envelope: a module copy outside any git
# checkout and a scrubbed environment. See specs/test-isolation.md.
test-scrubbed:
	./scripts/test-envelope.sh -- make test

# Strict envelope: adds a network namespace with only loopback (Linux).
test-hermetic:
	./scripts/test-envelope.sh --netns -- make test

# Test the hermetic envelope driver itself (fixtures only: never touches this repo).
envelope-test:
	./scripts/test-envelope_test.sh
```

Append `envelope-test` to the `check:` and `verify:` recipe lines, after `cleanup-branches-test`.

- [ ] **Step 2: Verify by inspection and by running**

Run: `make -n test-scrubbed test-hermetic`
Expected: prints `./scripts/test-envelope.sh -- make test` and `./scripts/test-envelope.sh --netns -- make test`.

Run: `make envelope-test`
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add Makefile
git commit -m "test: expose the hermetic envelope as make targets"
```

---

### Task 4: Record the inventory and the runtime budget

**Files:**
- Create: `.operator-shared/plans/2026-11-07-hermetic-inventory.md`
- Modify: `.operator-shared/catalog.md`

**Interfaces:**
- Consumes: the three targets.
- Produces: the failing inventory and timing that scope the follow-up plans.

- [ ] **Step 1: Run the scrubbed envelope and capture failures**

Run: `make test-scrubbed 2>&1 | tee /tmp/opencode/test-scrubbed.log`
Expected: it may fail; capture the failing packages and test names.

- [ ] **Step 2: Measure the budget**

Run, on an otherwise idle machine: `time make test` and `time make test-scrubbed`
Expected: two wall times; record both.

- [ ] **Step 3: Write the inventory**

Create `.operator-shared/plans/2026-11-07-hermetic-inventory.md`: the two timings, the ratio, and the failing packages/tests grouped by surface (identity/CWD, host figures, network, wall-clock, other), one line each. Empty groups are stated as empty.

- [ ] **Step 4: Catalog the new documents**

Add to `.operator-shared/catalog.md`: the design, this plan, and the inventory — each with a one-line Description and a `Read If`.

- [ ] **Step 5: Commit**

```bash
git add .operator-shared/plans/2026-11-07-hermetic-inventory.md .operator-shared/catalog.md
git commit -m "docs(operator): record the hermetic envelope inventory and budget"
```

---

## Self-Review

- **Spec coverage:** the design's "Sequencing" step 1 (envelope + inventory + budget) is Tasks 1-4; the `test-scrubbed`/`test-hermetic` names match the design's "Enforcement" section. Steps 2-6 (identity seam, host figures + sentinel, wall-clock, cadence, contract fold) are deliberately follow-up plans, because their task lists depend on the inventory this plan produces.
- **Step scan:** each step names one action and its checkable result.
- **Type consistency:** `scripts/test-envelope.sh`, `scripts/test-envelope_test.sh`, `test-scrubbed`, `test-hermetic`, `envelope-test`, and the env allowlist are used with the same names throughout.
- **Review Focus:** five lines, each with the task that pins it (Tasks 1-2).
- **Proportion:** the plan is the envelope plus its test and the first run; no surface fix is transcribed.
