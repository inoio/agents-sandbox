---
description: Repo tooling - maintenance scripts, CI gates, the Go test harnesses, the docs site, Homebrew, the dev-VM image, and the repo skills.
read_if: Working on scripts/, ci/, test/, docs/, Formula/, .github/, .agents-sandbox/, .agents/skills/, or the learn-mode spike.
---

# Tooling and supporting trees

## Coverage

- `scripts/`, `ci/`, `test/`, `docs/`, `Formula/`, `.github/`, `.agents-sandbox/`, `.agents/skills/`, `spike/`

## Architecture

- Three layers meet here: the **shell maintenance tools** in `scripts/` and the **CI gate scripts** in `ci/`, both driven by the Go harnesses in `test/scripts/`; the **docs site** in `docs/` (Jekyll, just-the-docs); and the **dev-VM image** in `.agents-sandbox/` (multistage Dockerfile).
- `test/harness/` is the top-level shared test infrastructure beside the per-package `test/scripts/` harnesses.

## `scripts/` Index

- `scripts/` — Repo-maintenance shell scripts: `cleanup-merged-branches.sh` (`make cleanup-branches`), `upstream-drift.sh`, `rebase-status.sh`, `enqueue-pr.sh` (the merge-queue enqueue/watch helper behind `make enqueue-pr-test`), `surface-sweep.sh` (the release-surface sweep isolation helper; see `specs/testing/release-surface-testing.md`), `brain-check.sh` (the Operator brain's structural gate, `make brain-check`; see `guides/operator-brain-maintenance.md`), `kvm-guest.sh` (the libvirt KVM-guest provisioner for the host-only msb suites, `make kvm-up`/`kvm-run`; see `guides/kvm-guest.md`), the hermetic envelope driver `hermetic-envelope.sh` (`make test-scrubbed`/`test-hermetic`; see `specs/testing/test-isolation.md`), and the changed-file pair `changed-files.sh` (the local/branch diff, the single source of truth, behind `make changelog-presence`) and `changed-targets.sh` (the changed-file→`check`-target map behind `make check-local-changes`/`check-branch-changes`; see `specs/ci/quality-gates.md`). Every harness now lives in the Go `test/scripts` package; no bash test harness remains (see `specs/testing/shell-script-harnesses.md`).
- `spike/learn-mode/` — Throwaway host-run harness for the learn-mode observation spike (runbook + one driver per experiment + `d_runtime_interception.sh` + `s8_sdk_channel/`, the SDK channel-semantics probe as its own Go module); kept until S1 finishes for all harnesses, then deleted and folded into the `learn-mode/` subspecs.

## `test/` Index

- `test/harness/` — `msb`-tagged harness smoke tests: starts every registered harness in a real microsandbox VM under a sized pty and asserts it reaches its TUI and exits (the `*_helpers_test.go` untagged test-only support: harness table, pty driver, runner-image tooling; `smoke_msb_test.go`; untagged unit tests for the table/pty). See `specs/testing/harness-smoke-tests.md`.
- `test/scripts/` — Go harnesses for the standalone `scripts/` and `ci/` tools: drives the shell tools through `RunScript` with `gittest` fixtures and re-exec fakes, all `*_helpers_test.go`/`*_test.go` in the package; `main_test.go` installs the fail-fast mocks and dispatches the fakes, one `<tool>_test.go` per migrated harness. See `specs/testing/shell-script-harnesses.md`.

## `ci/` Index

- The gate scripts: `check-docs.sh` (doc link/style gate), `check-deadcode.sh` + `deadcodecheck/` (dead-code gate driver and helper), `check-changelog.sh` (changelog fragments), `check-ci-guards.sh` (the `github.event_name`/`on.pull_request.types` invariants and the four exempt-path copies), `event-base.sh` (the single base-SHA resolver), and `classify-changes.sh` (the `changes` job's `code`/`exec` classification); see `specs/ci/ci-pipeline.md`. Their harnesses are Go `test/scripts/*_test.go` tests.

## `docs/` Index

- Navigation groups pages by user need (Get started / Guides / Recipes / Configuration / Concepts / Reference); per-feature pages live in `configuration/`, C4/lifecycle diagrams in `diagrams/*.puml`, and `vendor/` is a gitignored gem bundle.

## `.github/` Index

- `workflows/` — `ci.yml`, `pages.yml`, `codeql.yml`, `fuzz.yml`, `mutation.yml`.
- `stubs/*.tbd` — macOS framework/library stubs the darwin cross-build needs (see `specs/release/darwin-cross-compile.md`); `ISSUE_TEMPLATE/`, `PULL_REQUEST_TEMPLATE.md`, `dependabot.yml`.

## `Formula/` Index

- `agents-sandbox.rb` — Formula for macOS install.

## `.agents-sandbox/` Index

- `Dockerfile`, `config.yaml`, `*.sh`, `opencode2/` — dev environment image (go, msb, docker, …) and its provisioning; the Dockerfile is multistage, one stage per toolchain component (`system`/`fetch`/`go`/`zig`/`extras`).

## `.agents/skills/` Index

- `release/` — the release runbook (`SKILL.md`, `RELEASE-NOTES.md`, `NON-GO-DEPS.md`); `bare-worktrees/` — the bare-checkout layout this repo uses.
- `handle-pr-feedback/`, `maintain/`, `pr/` — repo-adapted skills: the PR-mergeability loop, the GitHub maintainer flow, and the PR-body template.
- `compacting-context/` — the `session.compact` recipe so an agent told to compact its own context loads the skill instead of scanning for a nonexistent tool; `opencode2-self-management/`, `opencode2-probe/`, `terminal-reset/` — runbooks retired from the shared brain (the `session.compact` API, driving the opencode2 daemon's event stream, and verifying the post-attach terminal reset).
- `upstream-drift/`, `rebase-status/` — the two reporter scripts (`scripts/upstream-drift.sh`, `scripts/rebase-status.sh`) for checking how far `origin/main` moved before a rebase, and the state of an in-progress rebase.
- `greptile-review/` — a maintainer-run correctness review (cross-component bugs, invariant bypasses, under-asserting tests, overstated docs), the third axis beside `code-review`; ships `RULES.md` (14 families), `mine.sh`+`corpus.tsv` (the mined Greptile corpus), and `validation.md` (RED/GREEN records).
