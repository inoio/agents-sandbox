---
description: TDD plan for making opencode2's done notification subtree-aware - pending reasons (`subagents`, `commands`), the new `on-done-*` config keys, the agent decode fields (`title`, shell state, `SessionRef`), the reconnect reset, and the `agents-sandbox:` message format.
read_if: A task in `internal/notify`/`internal/agent`/`internal/viperconfig` for the notify change; the symbols `DoneReasons`, `SubtreeDoneStates`, `ResetTransient`, `SessionRef`, or the `on-done-*` keys; or a session where a done fires while a subagent runs, a blocked subagent never alarms, or a done stays silent after a reconnect.
---

# Notify Subtree-Aware Done Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make opencode2's done notification reflect the whole session tree — never "done" while a subagent is running, distinguishable "done with a background command running", with the session title in the message.

**Architecture:** The pure `agent.EventStreamSpec` carries per-harness event-type lists and a decoder; `internal/notify` keeps only SSE framing and a `Tracker` that turns events into notifications. The tracker learns child→parent links and session titles from lifecycle events, tracks each session's live/settled state and running background shells, and classifies every top-level `execution.succeeded` by what is still pending. v1 opts out of the subtree model (`SubtreeDoneStates: false`), so its trigger semantics are unchanged.

**Tech Stack:** Go 1.27; no new dependencies.

**Spec:** `.operator-shared/specs/agents/notify.md`

## Global Constraints

- opencode2 only for the done model. `opencode` (v1) keeps `SubtreeDoneStates: false` and empty new lists; its trigger semantics must not change. Both agents' *wording* moves to the shared `agents-sandbox:` strings.
- No new channels, no new `--notify` value. `--notify`/`AGENTS_SANDBOX_NOTIFY` still set channels only.
- New config keys, exact: `notify.on-done-subagents-running` (default `false`), `notify.on-done-commands-running` (default `true`). Env: `AGENTS_SANDBOX_NOTIFY_ON_DONE_SUBAGENTS_RUNNING`, `AGENTS_SANDBOX_NOTIFY_ON_DONE_COMMANDS_RUNNING`.
- Message title prefix, exact: `agents-sandbox: `. Titles: `agent done`, `input requested`, `session error`; a done with pending work appends the reasons. Body: `in session '<title>'`.
- The watcher's only sandbox dependency stays the two-method `notify.StreamOpener` seam. No daemon changes; only events already on `GET /api/event`.
- Follow `AGENTS.md`: TDD; `make fmt` and `make lint` (never `go vet`/`go fmt`); test a unit in its `<unit>_test.go`; add `changelog.d/<PR>.<type>.md`; keep `docs/` (except `docs/superpowers`) in sync.
- v2 event facts pinned (verified against v2.3.0): `session.created`/`session.renamed` carry `title` flat at `data.title`; `session.shell.started`/`session.shell.ended` carry `data.sessionID` and `data.shell.{id, metadata.background}`; `session.execution.interrupted` carries `data.sessionID`. `/api/session` `outcome` is the *last* execution's outcome and cannot signal liveness.

## Review Focus

- **Reconnect while a child is live** — a session live across a stream drop must not suppress the main's done forever. Expected: (re)connect resets descendant liveness and running shells; at worst one turn's done fires plain.
- **An interrupted child** — `session.execution.interrupted` is a terminal sibling of `succeeded`/`failed`; if it does not settle the child, the main session's done never fires again. Expected: interrupted settles, no alert.
- **Both reasons at once** — a done with a live subagent *and* a running command must be one alert that names both, gated by *either* toggle. Expected: no silent drop and no double alert.
- **Unknown title** — a watcher that connected after creation, or v1 without a title, must not produce `in session ''`. Expected: the documented fallback sentence.
- **Child of a child, and multiple top-level sessions** — input from a nested subagent must attribute to the right top-level ancestor, and one top-level session's done must not be held by another's children.

---

### Task 1: Verify the parent re-emits `execution.succeeded` after subagents settle

The whole "no hold, no synthesis" design rests on this. It is a probe, not code; do it before writing the tracker.

**Files:**
- Evidence goes in the PR description, not the repo.

**Interfaces:**
- Consumes: the running opencode2 daemon (`.agents/skills/opencode2-probe`).
- Produces: a recorded event sequence deciding Task 3's approach.

- [ ] **Step 1: Capture a parent/subagent turn**

Follow `.agents/skills/opencode2-probe`: create a top-level session, start `curl -N -s -u "opencode:$OPENCODE_SERVER_PASSWORD" http://127.0.0.1:4096/api/event > stream.log &`, then prompt the session to spawn a subagent (`task` tool). Delete the scratch session when done.

- [ ] **Step 2: Confirm the sequence**

Expected: for the parent session id, a `session.execution.succeeded` appears while the child's `execution.succeeded` has *not* yet fired; after the child's report the parent emits a later `execution.succeeded`.

- [ ] **Step 3: Decide**

If confirmed: proceed. If not (the parent never runs again), **stop and report** — Task 3 must instead hold the classified done and fire it when the last descendant settles (a bounded change to the tracker), and the plan is re-cut.

- [ ] **Step 4: Record**

Nothing to commit; paste the two parent `execution.succeeded` envelope ids into the PR description as evidence.

---

### Task 2: Agent layer — title, shell fields, and session refs

**Files:**
- Modify: `internal/agent/capabilities.go` (`StreamEvent`, `EventStreamSpec`, `SessionListSpec`, `NewSessionList`)
- Modify: `internal/agent/opencode.go` (`sessionParent`, `childSessionIDs`, `parseOpencodeV1SessionList`)
- Modify: `internal/agent/opencode2.go` (`EventStream`, `parseOpencodeV2Event`, `parseOpencodeV2SessionList`)
- Test: `internal/agent/opencode2_internal_test.go`, `internal/agent/opencode2_test.go`, `internal/agent/opencode_test.go`

**Interfaces:**
- Consumes: nothing (leaf).
- Produces:
  - `type StreamEvent struct { Type, SessionID, ParentID, EventID, Title, ShellID string; ShellBackground bool }`
  - `type EventStreamSpec struct { …; SubtreeDoneStates bool; ShellStartedEvents, ShellEndedEvents, InterruptedEvents []string; … }`
  - `type SessionRef struct { ID, ParentID, Title string }`
  - `func NewSessionList(command string, parse func([]byte) []SessionRef) *SessionListSpec`
  - `func (s *SessionListSpec) Parse(payload []byte) []SessionRef`

- [ ] **Step 1: Write the failing decoder test**

In `internal/agent/opencode2_internal_test.go`, extend the table with, and assert:

```go
// title on a lifecycle event
`{"type":"session.created","data":{"sessionID":"ses_root","title":"probe run"}}` ->
    StreamEvent{Type: "session.created", SessionID: "ses_root", Title: "probe run"}
// renamed carries the id and title flat
`{"type":"session.renamed","data":{"sessionID":"ses_root","title":"new name"}}` ->
    StreamEvent{Type: "session.renamed", SessionID: "ses_root", Title: "new name"}
// a background shell
`{"type":"session.shell.started","data":{"sessionID":"ses_root","shell":{"id":"sh_1","metadata":{"background":true}}}}` ->
    StreamEvent{Type: "session.shell.started", SessionID: "ses_root", ShellID: "sh_1", ShellBackground: true}
// a foreground shell leaves ShellBackground false
`{"type":"session.shell.started","data":{"sessionID":"ses_root","shell":{"id":"sh_2","metadata":{"background":false}}}}` ->
    StreamEvent{Type: "session.shell.started", SessionID: "ses_root", ShellID: "sh_2"}
// ended carries the id too
`{"type":"session.shell.ended","data":{"sessionID":"ses_root","shell":{"id":"sh_1"}}}` ->
    StreamEvent{Type: "session.shell.ended", SessionID: "ses_root", ShellID: "sh_1"}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -tags microsandbox_ffi_path ./internal/agent/ -run TestParseOpencodeV2Event -v`
Expected: FAIL (fields not set / no such fields).

- [ ] **Step 3: Extend `StreamEvent` and `parseOpencodeV2Event`**

Add the fields; in the decoder read `raw.Data.Title`, `raw.Data.Shell.ID`, `raw.Data.Shell.Metadata.Background`. (Confirm the JSON shape once against `/openapi.json`: `session.shell.*` data is `{sessionID, shell}`.)

- [ ] **Step 4: Run test to verify it passes**

Run the Step 2 command. Expected: PASS.

- [ ] **Step 5: Write the failing session-ref test**

In `internal/agent/opencode2_test.go`, assert `parseOpencodeV2SessionList` on `{"data":[{"id":"ses_child","parentID":"ses_top","title":"c"},{"id":"ses_top","title":"t"}]}` returns `[]SessionRef{{"ses_child","ses_top","c"},{"ses_top","","t"}}` — **all** sessions, including the parent.

- [ ] **Step 6: Run it to verify it fails**

Run: `go test -tags microsandbox_ffi_path ./internal/agent/ -run TestParseOpencodeV2SessionList -v`
Expected: FAIL.

- [ ] **Step 7: Introduce `SessionRef`, change `SessionListSpec` to it, update both parsers**

`sessionParent` gains `Title string \`json:"title"\``; replace `childSessionIDs` with `sessionRefs(sessions []sessionParent) []SessionRef` (no parent filter). `parseOpencodeV1SessionList` returns `[]SessionRef` too. Change `NewSessionList` and `Parse` signatures; the panic guards stay.

- [ ] **Step 8: Set opencode2's new spec fields**

In `opencode2.go` `EventStream()`: `SubtreeDoneStates: true`, `ShellStartedEvents: []string{"session.shell.started"}`, `ShellEndedEvents: []string{"session.shell.ended"}`, `InterruptedEvents: []string{"session.execution.interrupted"}`, and add `"session.renamed"` to `SessionEvents`. Leave `opencode.go`'s spec with the zero values.

- [ ] **Step 9: Fix the spec-equality tests and run the package**

`opencode2_test.go`/`opencode_test.go` compare the whole spec (with `Parse` nilled). Update expected specs. Run: `go test -tags microsandbox_ffi_path ./internal/agent/`
Expected: PASS.

- [ ] **Step 10: Lint and format**

Run: `make fmt && make lint`
Expected: clean.

- [ ] **Step 11: Commit**

```bash
git add internal/agent/
git commit -m "feat(agent): decode session title and shell state for notify"
```

---

### Task 3: Tracker — parent links, titles, shells, and done classification

**Files:**
- Modify: `internal/notify/event.go` (`Tracker`, `Handle`, `SeedChildren`→`Seed`)
- Modify: `internal/notify/watch.go:223` (call `Seed`)
- Test: `internal/notify/event_test.go`

**Interfaces:**
- Consumes: `agent.StreamEvent`, `agent.SessionRef`, `agent.EventStreamSpec.{SubtreeDoneStates, ShellStartedEvents, ShellEndedEvents, InterruptedEvents}`, `agent.SessionListSpec.Parse`.
- Produces:
  - `type DoneReason int` with `DoneSubagentsRunning`, `DoneCommandsRunning`
  - `Notification.DoneReasons []DoneReason`
  - `func (t *Tracker) Seed(refs []agent.SessionRef)`
  - `func (t *Tracker) ResetTransient()`

- [ ] **Step 1: Write the failing tests**

In `internal/notify/event_test.go`, using the `opencode2` spec helper (`helpers_test.go`, which must gain the Task 2 fields):

```go
func TestDoneHeldWhileChildLive()       // root busy->live; child created(busy); root succeeded -> nil; child succeeded; root succeeded -> done, no reasons
func TestDoneReasonsCommands()          // root succeeded with a background shell running -> done, [DoneCommandsRunning]
func TestDoneReasonsBoth()              // live child + running shell -> done, [DoneSubagentsRunning, DoneCommandsRunning]
func TestInterruptedSettlesChild()      // child interrupted settles it so a later root succeeded is plain done
func TestChildInputSurfacesAsTopLevel() // child permission.asked -> input notification with SessionID == root, EventID == child's
func TestNestedChildInputUsesRoot()     // grandchild input -> attributed to root
func TestDoneWithoutSubtreeStates()     // a spec with SubtreeDoneStates=false keeps today's behavior: root succeeded with a live child still notifies done
func TestSeedRecordsTitlesAndParents()  // Seed(refs) lets a later succeeded carry the seeded title in the body
```

Assert exact `Title`/`Body` per the Global Constraints and `DoneReasons` per case.

- [ ] **Step 2: Run to verify they fail**

Run: `go test -tags microsandbox_ffi_path ./internal/notify/ -run 'TestDone|TestInterrupted|TestChildInput|TestNested|TestSeed' -v`
Expected: FAIL to compile (unknown fields/functions).

- [ ] **Step 3: Implement the tracker state and `Handle`**

Add `parentOf map[string]string`, `titles map[string]string`, `shells map[string]map[string]bool`. In `Handle`, before trigger matching: record `titles[sessionID]` from any event with a `Title`; on a `SessionEvents` event with a `ParentID`, record `parentOf`; on `ShellStartedEvents` with `ShellBackground`, add `ShellID`; on `ShellEndedEvents`, remove `ShellID`. Settle (`stateIdle`, no alert) on `InterruptedEvents`. For the child early-return: if the event is `AwaitingInput`, walk `parentOf` to the top-level ancestor and return an input `Notification` for it (Title `agents-sandbox: input requested`, Body `in session '<ancestor title>'`); otherwise return nil. On the idle arm, when `spec.SubtreeDoneStates`, compute the pending set:

```
subagents = any session s with parentOf chain through this id whose state is busy/awaiting
commands  = len(shells[thisID]) > 0
```

set `DoneReasons`, and title `agents-sandbox: agent done` (+ pending suffix); when `!SubtreeDoneStates`, keep today's behavior and title `agents-sandbox: agent done`.

- [ ] **Step 4: Run to verify they pass**

Run the Step 2 command. Expected: PASS.

- [ ] **Step 5: Rewrite the wording test**

`TestTrackerTitlesUseSpecName` becomes a test of the fixed `agents-sandbox:` strings and the `in session '<title>'` body (with the fallback sentence when no title is known).

- [ ] **Step 6: Lint, format, full notify package**

Run: `make fmt && make lint && go test -tags microsandbox_ffi_path ./internal/notify/`
Expected: PASS. (Other notify tests may need the new titles; fix them.)

- [ ] **Step 7: Commit**

```bash
git add internal/notify/event.go internal/notify/watch.go internal/notify/*_test.go
git commit -m "feat(notify): classify done by pending subagents and commands"
```

---

### Task 4: Backend — reasons-aware gating

**Files:**
- Modify: `internal/notify/backend.go` (`compositeBackend.triggerEnabled`), `internal/notify/notify.go` (`Config`)
- Test: `internal/notify/backend_test.go`, `internal/notify/notify_test.go`

**Interfaces:**
- Consumes: `Notification.DoneReasons`, `DoneReason`.
- Produces: `Config.OnDoneSubagentsRunning`, `Config.OnDoneCommandsRunning`; `triggerEnabled` takes a `Notification`.

- [ ] **Step 1: Write the failing gating tests**

```go
func TestDoneGatingByReason() // TriggerDone: {}+OnDone; {subagents}+OnDoneSubagentsRunning; {commands}+OnDoneCommandsRunning; {subagents,commands} true if EITHER toggle on
```

- [ ] **Step 2: Run to verify it fails.** `go test -tags microsandbox_ffi_path ./internal/notify/ -run TestDoneGating -v` → FAIL.

- [ ] **Step 3: Implement.** Change `triggerEnabled(t Trigger)` to `triggerEnabled(n Notification)`; for `TriggerDone` return true when `len(n.DoneReasons) == 0 && c.cfg.OnDone`, or any present reason's toggle is on.

- [ ] **Step 4: Run to verify it passes.**

- [ ] **Step 5: Lint/format/test package, commit.**

```bash
git commit -am "feat(notify): gate done alerts by pending reason"
```

---

### Task 5: Reconnect reset

**Files:**
- Modify: `internal/notify/watch.go` (`watchOnce` before seeding), `internal/notify/event.go` (`ResetTransient`)
- Test: `internal/notify/watch_test.go`

**Interfaces:**
- Consumes: `Tracker.ResetTransient`.
- Produces: nothing new.

- [ ] **Step 1: Write the failing test**

```go
func TestReconnectResetsChildLiveness() // a child left busy, then a re-connect; a root succeeded afterwards is plain done, not held
func TestReconnectClearsRunningShells() // a shell left running across a drop no longer claims DoneCommandsRunning
```

- [ ] **Step 2: Run to verify it fails.** `go test -tags microsandbox_ffi_path ./internal/notify/ -run TestReconnect -v` → FAIL.

- [ ] **Step 3: Implement.** `ResetTransient` deletes `states[s]` for every `s` in `parentOf` and clears `shells`; `watchOnce` calls it immediately before `Seed`.

- [ ] **Step 4: Run to verify it passes.** Also run the whole `./internal/notify/` package and `-count=2` to catch state leaks.

- [ ] **Step 5: Lint/format/commit.**

```bash
git commit -am "fix(notify): reset subtree liveness on reconnect"
```

---

### Task 6: Config keys and decode

**Files:**
- Modify: `internal/viperconfig/config.go`, `internal/viperconfig/configkeys.go`, `internal/viperconfig/decode.go`
- Test: the notify-decode test file in `internal/viperconfig/`

**Interfaces:**
- Consumes: `notify.Config.OnDoneSubagentsRunning`, `OnDoneCommandsRunning`.
- Produces: `keyNotifyOnDoneSubagentsRunning`, `keyNotifyOnDoneCommandsRunning`.

- [ ] **Step 1: Write the failing test.**

```go
func TestDecodeNotifyDoneReasons() // section present, keys unset -> OnDoneSubagentsRunning false, OnDoneCommandsRunning true, others true; keys set override each
```

- [ ] **Step 2: Run to verify it fails.** `go test -tags microsandbox_ffi_path ./internal/viperconfig/ -run TestDecodeNotify -v` → FAIL.

- [ ] **Step 3: Implement.** Add the two key consts (`"notify.on-done-subagents-running"`, `"notify.on-done-commands-running"`), add both to `notifyEnvKeys`, and in `decodeNotify` include them in the "section set" check, default `OnDoneSubagentsRunning=false`/`OnDoneCommandsRunning=true` after the existing defaults, and override when set.

- [ ] **Step 4: Run to verify it passes.** Also confirm env binding: `go test -tags microsandbox_ffi_path ./internal/viperconfig/`.

- [ ] **Step 5: Lint/format/commit.**

```bash
git commit -am "feat(config): notify done-with-subagents/commands toggles"
```

---

### Task 7: Docs, changelog

**Files:**
- Modify: `docs/configuration/notifications.md` (trigger table + message format + corrected default note)
- Create: `changelog.d/<PR-number>.feature.md`

**Interfaces:** none (prose).

The `.operator-shared/specs/agents/notify.md` contract is already written (this plan's spec); no further brain edit is needed unless a clause proves wrong.

- [ ] **Step 1: Update `docs/configuration/notifications.md`.** Add `on-done-subagents-running` (default `false`) and `on-done-commands-running` (default `true`); state that a done alert names the session title and that v1's wording also moved to `agents-sandbox:`.
- [ ] **Step 2: Add the changelog fragment** (`changelog.d/<PR-number>.feature.md`; see `CONTRIBUTING.md`).
- [ ] **Step 3: Run the doc gate.** `make check-docs` (or `bash ci/check-docs.sh`). Expected: pass.
- [ ] **Step 4: Commit.**

```bash
git add docs/configuration/notifications.md changelog.d/
git commit -m "docs: notify subtree-aware done"
```

---

### Task 8: Final verification

- [ ] **Step 1:** `make check` from the module root. Expected: pass (run `make fmt` first if it flags formatting).
- [ ] **Step 2:** `make compile-all`. Expected: compile-only, no errors.
- [ ] **Step 3:** Re-read the spec's Goals/Non-goals against the diff; confirm each is met and v1's spec is untouched (`SubtreeDoneStates` false, new lists empty).
- [ ] **Step 4:** Commit any fixups.

## Notes for the executor

- The spec lives at `.operator-shared/specs/agents/notify.md` (tracked, published). Keep it in sync if a clause proves wrong while implementing; do not cite this plan from the spec (brain-check forbids it).
- `agent.EventStreamSpec.Name` stays — it still identifies the agent in spec-equality tests and docs even though titles no longer read it.
- Add the brain catalog's plan entry only if this plan is not yet catalogued (it is).
