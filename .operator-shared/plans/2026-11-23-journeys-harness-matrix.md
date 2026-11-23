---
description: Implementation plan for #851 - make the msb journeys suite extensible across harnesses (scenario × harness matrix, per-harness adapter, scriptable multi-protocol stub, a stub route that completes a turn, per-journey isolation, Given/When/Then steps with action/check semantics) and prove it with one core-session journey on opencode2, claude-code, and codex, verified at two checkpoints.
read_if: Executing #851; changing the journey model (`test/msb/journeys` `Journey`/`Step`/`Context`), the harness adapter, the stub provider's protocols or route, per-journey project isolation, or the core-session journey; or running one of the plan's checkpoints.
---

# msb journeys: harness matrix (#851) Implementation Plan

> **For agentic workers:** Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans
> to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking. Host-run steps are marked
> **[host]**: they need `/dev/kvm`, a docker daemon, and network, and an agent never claims them done without the run's
> output.

**Goal:** Refactor `test/msb/journeys` so a journey is harness-independent and a harness plugs in through one
adapter, then prove the abstraction with one long core-session journey (#783's A → C → E → D) on opencode2,
claude-code, and codex. The refactoring is a **hypothesis**: two checkpoints verify it before #852/#794 build on it.

**Architecture:** A journey stays a Go table of named steps (no `.feature` files, no godog). Each step gains a
Given/When/Then keyword, a sentence name, and a kind (*action*: fatal, *check*: recorded and continued). A journey
declares the harnesses it runs on; the runner expands journey × harness into subtests
`TestHarnessJourneys/<journey>/<harness>`. Everything a step needs to know about a harness lives in a journeys-side
`Harness` adapter keyed by agent name, next to (not inside) `driver.Spec`, which stays the startup smoke's table. The
host-side stub provider grows a protocol layer (OpenAI chat completions, Anthropic Messages, OpenAI Responses) and a
per-journey script of canned turns.

**Tech Stack:** Go 1.27; `github.com/creack/pty`; `net/http` (stub); the built launcher binary; microsandbox `msb`.

**Spec:** `.operator-shared/specs/testing/msb-journeys.md` (design, updated by this plan). Supporting:
`specs/testing/harness-smoke-tests.md`, `specs/testing/test-isolation.md`,
`specs/testing/release-surface-testing.md`, `specs/onboarding/learn-mode/observation-channel.md`,
`specs/network-and-secrets/interception-and-proxy.md`.

## Global Constraints

- Go 1.27; `AGENTS.md` code style; TDD for every unit (failing test, watch it fail, implement, watch it pass).
  `make fmt`, `make lint`, `make check`; never `go fmt`/`go vet`.
- Untagged code under `test/msb/journeys` (model, steps, adapters, stub) keeps its unit tests in the default
  `make test`; only the host runner (`*_msb_test.go`) carries the `msb` tag. A tagged-only package is dropped by
  `go test ./...`.
- The completeness contract runs in the default suite and becomes: every (journey, registered harness) pair is
  covered or exempted with a non-empty reason. A newly registered agent fails `make test` until it is.
- **Few, long journeys.** A harness boot dominates runtime. A new check extends an existing journey when it can run
  on the state that journey reached; a new journey only for a different starting state.
- Assertions stay semantic (a file exists and is owned by the host user, a marker appears, a key is set), never a
  full-file or full-screen golden.
- No retry in the driver that hides a product race (#838). A flake is a finding.
- The existing learn-mode journeys keep exactly their assertions; the migration may only change how they are
  expressed.
- `driver.Spec` keeps the startup smoke's contract; journeys-only knowledge (provider seed, prompt, resume) never
  moves into it.
- Non-test `.go` under `test/` counts as production code for the changelog fragment rule: add
  `changelog.d/<PR>.chore.md` once the PR number is known.

## Review Focus

1. **A harness-name branch outside the adapter.** Any `switch`/`if` on an agent name in a step, the runner, or the
   stub is a design failure, not a shortcut. Grep for agent-name literals in `steps.go`/runner at each checkpoint.
2. **A check that silently passes.** A non-fatal check must still fail the subtest at the end (`t.Errorf`
   semantics), and a check that depends on a failed action must not run against missing state.
3. **A stub turn that "passes" without reaching the stub.** C must assert the host-side effect (the file), not that
   the prompt was sent; the stub records the requests it served so a journey can assert it was hit.
4. **Shared-VM leakage.** A journey on its own project must not attach to another journey's VM (see Task 0's
   finding); the sweep must remove per-journey projects' sandboxes.
5. **Wire-protocol drift.** Each protocol handler is unit-tested against a recorded minimal request from the real
   harness, so a harness upgrade that changes the request shape fails in the default suite where possible.

---

## Phase 0 — Settle the stub route (hard dependency, #793)

### Task 0: Make a turn reach the host stub — done

Outcome (host runs, 2026-11-23; the durable part is in the spec's "Stub provider", "Typing into a harness", and
"Isolation between journeys"):

- The prompt was swallowed: opencode answers its terminal-capability queries before it takes input. Fixed by
  `typeUntilEchoed` (retype until echoed); the learn journeys now really send their prompt and still pass.
- `.invalid` never resolves in the guest, and the runtime enforces the policy before the proxy, so it is never
  observed. The default route is now the host gateway (`http://host.microsandbox.internal`, network profile
  `host`): a real turn completes (now part of `core-session`). Host-gateway traffic bypasses the observation
  proxy, so the proxy spike is opt-in (`AGENTS_SANDBOX_JOURNEYS_PROXY_SPIKE=1`) and stays #793's open question.
- A later journey attached to the VM an earlier journey left running (its network policy, its still-running
  sessions). `StepOwnProject` gives a journey its own project and VM; Task 5 decides the default.

---

## Phase 1 — Refactor without behaviour change — done

Tasks 1–4 landed together (the durable part is the spec's "Journey model"): keywords and sentence names,
action/check kinds, the untagged `runJourney` with the scenario log and per-step durations, journey × harness
with per-pair exemptions and `JOURNEY`/`HARNESS` selection, and the opencode2 adapter (`StubConfig`,
`HostConfig`, `HostLoginStore`). Deviations from the original task text:

- The kind follows dependency, not the keyword: "Then the harness TUI comes up" is an action, so the planned
  "a `Then` step must be a check" rule was dropped.
- The stub-turn subtest became a table journey, later folded into `core-session` (Phase 2).
- Prompt submission stays out of the adapter until a harness submits differently (claude-code, Task 9).
- The four learn-mode journeys passed on the host after the refactor with unchanged assertions.

---

## Phase 2 — Isolation and the core-session journey on opencode2 — done

Tasks 5–7 landed together (durable part: the spec's "The core-session journey"). Outcome:

- Per-journey projects stay opt-in (`StepOwnProject`); `core-session` uses one.
- The stub's protocol layer gained a one-shot scripted tool call (`ScriptToolCall`); the per-protocol file
  split waits for the second protocol (Task 8).
- The adapter gained `WriteFileCall` and `ResumeArgs`; the experimental notice is derived from the profile.
- Finding: opencode2's `write` tool takes `path`, not opencode 1.x's `filePath`; a wrong key is a tool error the
  turn survives, so the adapter's call is now pinned by a unit test.
- `core-session` passes on opencode2 in ~14 s; `stub-turn-completes` was folded into it.

---

## Phase 3 — Checkpoint 1: claude-code — done

`core-session` passes on claude-code (~8 s) and opencode2. The review outcome is in the spec's "Checkpoint 1":
the abstraction held (no harness literal or branch in shared code; one shared launch change, one new step, one
journey reorder); the adapter gained `StubEnv` and `LaunchArgs`, core-session starts configured instead of through
the first-run menu, and the quit order makes both harnesses' resume pick the same session.

---

## Phase 4 — Checkpoint 2: codex

### Task 11: OpenAI Responses protocol

- [ ] Test (default suite): the stub serves `POST /v1/responses` with a scripted text and function-call turn,
      streamed (`response.created`/`response.output_item.*`/`response.completed`), matching a minimal recorded codex
      request. Codex's file-writing path is `apply_patch`/shell; the script uses whichever the recorded request
      advertises.

### Task 12: codex adapter and core-session row

- [ ] Adapter: provider seed via `config.toml` (`model_provider` with `base_url`, `wire_api = "responses"`,
      `env_key`), folder-trust handling, quit `Ctrl-C`, resume `resume --last`.
- [ ] Remove codex's core-session exemption.
- [ ] **[host]** Core-session passes on codex.

### Task 13: Checkpoint 2 review

- [ ] Codex should have needed only a table entry, an adapter, and a protocol. List anything more as a finding
      about the abstraction, fix it, and record the outcome in the spec and on #851.

---

## Phase 5 — Finalize

### Task 14: Catalogue, docs, brain

- [ ] A small generator renders the journey table as Gherkin Markdown (`go test -run TestRenderJourneyCatalogue
      -update` writes `test/msb/journeys/JOURNEYS.md`; the default test fails when the file is stale).
- [ ] `specs/testing/msb-journeys.md`: matrix model, adapter boundary, stub protocols and route, per-journey isolation,
      the few-long-journeys rule, action/check semantics, the checkpoint outcomes. Remove v1 wording that no longer
      holds.
- [ ] `specs/testing/release-surface-testing.md`: the spine runs the matrix; hand-driven steps keep only exploratory
      checks.
- [ ] `.operator-shared/guides/adding-an-agent-profile.md` (if #772 has landed): adding a harness includes its
      journeys adapter or a recorded exemption.
- [ ] Catalog entry for this plan removed once the work lands (fold remaining clauses into the spec, delete the
      plan); changelog fragment.
- [ ] `make check`, `make compile-all`; **[host]** `make test-msb-journeys` and `make test-msb` green.

## Self-Review

- Every #851 scope item maps to a task: matrix (3), adapter (4, 7, 9, 12), stub protocols and script (6, 8, 11),
  route (0), isolation (5), Given/When/Then and action/check (1, 2), selection (3), checkpoints (10, 13), catalogue
  (14).
- The learn-mode journeys are the regression net from Task 1 to the end; no task changes what they assert.
- The plan adds adapter fields only in the task whose journey uses them.
