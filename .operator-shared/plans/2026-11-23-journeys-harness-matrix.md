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
  `host`): `stub-provider-turn-completes` completes a real turn. Host-gateway traffic bypasses the observation
  proxy, so the proxy spike is opt-in (`AGENTS_SANDBOX_JOURNEYS_PROXY_SPIKE=1`) and stays #793's open question.
- A later journey attached to the VM an earlier journey left running (its network policy, its still-running
  sessions). `StepOwnProject` gives a journey its own project and VM; Task 5 decides the default.

---

## Phase 1 — Refactor without behaviour change

The four learn-mode journeys are the regression net: after every task in this phase they pass on the host with the
same assertions, and the default suite stays green.

### Task 1: Step keywords, sentence names, and action/check kinds

**Files:** Modify `test/msb/journeys/model.go`, `steps.go`, `uc1.go`, `uc4.go`, `config_learn.go`, `attribution.go`;
tests in `model_test.go` (new), `steps_test.go`, `table_test.go`.

- [ ] Test: a `Step` has a `Keyword` (`Given`/`When`/`Then`/`And`) and a `Kind` (`Action`/`Check`); the table test
      fails for a step with no keyword or an empty name, and for a `Then` step that is an `Action`.
- [ ] Constructors: `Given(...)`, `When(...)`, `Then(...)` wrap a step; assertion steps (`StepAssert*`) become checks,
      everything else actions. Rename steps to sentences ("the user answers the exit prompt with Save all").
- [ ] Test the runner semantics on a fake journey (no VM): an action error stops the journey; a check error is
      recorded with `t.Errorf`, later steps still run, the subtest fails at the end; a check after a failed action
      never runs.
- [ ] Extract the runner loop from `journeys_msb_test.go` into an untagged `runJourney(tb, ctx, journey)` so these
      semantics are unit-testable.

### Task 2: Per-step durations and the scenario log

- [ ] Test: `runJourney` logs the scenario (`Given …/When …/Then …`) before running and one line per step with its
      duration; a failure names the step in the same form.
- [ ] Implement with an injectable clock (no wall-clock assertion in tests).

### Task 3: Journey × harness matrix and the completeness contract

**Files:** `model.go`, `table.go`, `table_test.go`, `journeys_msb_test.go`.

- [ ] Test: a `Journey` declares `Harnesses []string`; `Exemptions` becomes `map[journeyName]map[agent]reason`; the
      completeness test reports every (journey, agent) pair that is neither covered nor exempted, and an exemption for
      a pair that *is* covered.
- [ ] Runner expands to `TestHarnessJourneys/<journey>/<harness>`; the learn-mode journeys declare `opencode2` only,
      with every other agent exempted per journey (reason points at #852/#794).
- [ ] `make test-msb-journeys HARNESS=<agent> JOURNEY=<name>` maps to `-run`; document both in the spec.

### Task 4: The harness adapter (opencode2 only)

**Files:** Create `test/msb/journeys/harness.go`, `harness_test.go`; modify `stub_provider.go`, `steps.go`,
`table.go` (move `opencodeHostConfigRelPath`).

- [ ] Test: `Harnesses[agent]` exists for every agent a journey covers (completeness test), and each adapter field
      a journey's steps use is set.
- [ ] Define `Harness` with only what the current journeys need — the stub provider config (`ProviderConfig(addr)`),
      the host config a UC4 seed writes, and prompt submission. Move the opencode constants out of
      `stub_provider.go`/`table.go` into the opencode2 adapter. Do not add fields for later scenarios yet (YAGNI;
      they arrive with the task that needs them).
- [ ] Steps read the adapter through `Context`; no step references an opencode path.

**[host] Regression gate:** the four learn-mode journeys pass with unchanged assertions. Commit.

---

## Phase 2 — Isolation and the core-session journey on opencode2

### Task 5: Per-journey project isolation

`StepOwnProject(label)` exists (Task 0); the teardown sweep already removes every sandbox in the suite's home.

- [ ] Decide the default: every journey on its own project (the shared VM carries network policy and running
      sessions between journeys), or opt-in per journey. Measure the cost with Task 2's durations and record the
      decision in the spec.
- [ ] Apply it to the learn-mode journeys if the default changes.

### Task 6: Stub provider — protocol layer, script, request log (OpenAI chat first)

**Files:** split `stub_provider.go` into `stub_provider.go` (listener, origin, request log) and
`stub_openai_chat.go`; tests beside each.

- [ ] Test: the stub records every request (method, path, model) so a step can assert it was hit.
- [ ] Test: a `StubScript` is an ordered list of turns; a turn is either a text reply or a tool call
      (`name`, JSON arguments). The OpenAI chat handler serves both, streamed (SSE) and non-streamed, in the shape
      opencode's `@ai-sdk/openai-compatible` accepts (fixture: a minimal recorded request/response).
- [ ] The empty-completion behaviour the learn journeys rely on stays the default when no script is set.

### Task 7: Core-session journey, opencode2

**Files:** Create `test/msb/journeys/core_session.go`; new steps in `steps.go` with unit tests.

The chain, one fresh project, two boots:

```
Given a fresh home and the stub provider scripted to write "journey-c.txt"
When the user starts the harness
Then the TUI comes up
And the experimental notice is shown iff the harness is experimental
And no first-run scaffolding appears                                   (#252)
When the user sends the prompt
Then the stub served the turn
And "journey-c.txt" exists in the project on the host, owned by the host user
When a second client attaches to the same VM
Then both sessions answer, and no reconfig prompt appears              (#810)
When both clients quit and the user starts the harness again with resume
Then the previous session is restored
```

- [ ] New steps (each unit-tested where it has host-side logic): `StepAssertProjectFile(name, ownedByHostUser)`,
      `StepAttachSecondClient` (a second `driver.Session` on the same project; `Context` gains `Sessions`),
      `StepAssertNoReconfigPrompt`, `StepRelaunchWithResume` (adapter: `ResumeArgs`), `StepAssertSessionRestored`
      (adapter: a marker that proves the resumed history, e.g. the earlier prompt text).
- [ ] Adapter fields added now, because this journey needs them: `ResumeArgs`, `ExperimentalNotice` derived from
      `agent.Experimental()` (not hard-coded), `DismissFirstRun` (empty for opencode2).
- [ ] Decide whether the first-run menu appears for a fresh home without host config and how the journey answers it
      (likely "Configure manually" or a `--yes`-free path that does not enable learn mode). Record the decision in the
      spec.
- [ ] **[host]** Core-session passes on opencode2; learn journeys still pass.

---

## Phase 3 — Checkpoint 1: claude-code

### Task 8: Anthropic Messages protocol

- [ ] Test (default suite): the stub serves `POST /v1/messages` with a scripted text and `tool_use` turn, streamed
      (`message_start`/`content_block_*`/`message_delta`/`message_stop`), matching a minimal recorded claude-code
      request. Tool name and argument shape come from the adapter (claude-code's `Write` tool).

### Task 9: claude-code adapter and core-session row

- [ ] Adapter: provider seed (`ANTHROPIC_BASE_URL` pointing at the stub plus a dummy key, provisioned through the
      `home:` manifest or `env`, whichever the launcher passes to the VM without tripping first-run detection),
      onboarding/folder-trust dismissal (reuse `driver.ClaudeCodeSmokeConfig`'s idea), quit `Ctrl-C`, resume
      `--continue`, write-tool script.
- [ ] Remove claude-code's core-session exemption.
- [ ] **[host]** Core-session passes on claude-code.

### Task 10: Checkpoint 1 review

- [ ] Grep the runner, steps, and stub for agent-name literals and harness-specific branches; list every adapter
      field and which harness uses it; list every workaround claude-code needed outside its adapter.
- [ ] Adjust the abstraction where it failed (move knowledge into the adapter, split a step, drop an unused field).
- [ ] Write the outcome into `specs/testing/msb-journeys.md` (a "Harness adapter" section: what held, what changed,
      why) and summarize on #851.

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
