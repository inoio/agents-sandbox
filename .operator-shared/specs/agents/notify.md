---
description: Contract for the notify event watcher on opencode/opencode2 - the agent owns the harness JSON via `EventStreamSpec.Parse`/`SessionListSpec`, `notify` keeps only SSE framing; a session's own state is live from busy to settle, a subagent (`parentID` non-empty) never notifies except for input, and a top-level `session.execution.succeeded` is classified by subtree-wide pending reasons (`subagents`, `commands`) that gate the done alert.
read_if: Changing notify for opencode/opencode2, the event-stream decode seam (`agent.EventStreamSpec`/`Parse`/`SessionListSpec`/`SessionRef`), the subtree done classification (`DoneReasons`, `SubtreeDoneStates`, `notify.on-done-subagents-running`, `notify.on-done-commands-running`), the tracker (`Seed`, `ResetTransient`, parent links, running shells), the message format, or the per-request dedup; or diagnosing "notify does nothing on opencode2", a done firing while a subagent runs, a blocked subagent that never alarms, or a done that stays silent after a reconnect.
---

# notify: opencode/opencode2 event stream and subtree-aware done

The watcher runs only when the agent implements `agent.EventStreamProvider`: `session/run.go` reads `provider.EventStream()` into a `streamSpec`, and a nil spec makes `startNotifyWatcher` a no-op. Both `opencode` (v1) and `opencode2` implement it; `pi`/`claude-code` do not.

The channels (`desktop`, `audio: system|bell|off`), the trigger toggles, the wording, and the cross-client dedup are unchanged except where this file says otherwise. The opencode2 done model is **subtree-aware**; v1 does not opt in (`SubtreeDoneStates` false) and keeps its existing trigger semantics. Both agents share the message wording.

## Decoder seam: the harness shape lives in the agent

`internal/notify` keeps only SSE **framing** (block assembly, `data:` extraction, comment skipping) and calls `spec.Parse(payload)`; nothing in `notify` knows a harness's JSON. Its local `Event` type was replaced by `agent.StreamEvent`. A payload with no type is not an event; a payload with a type but no session id (e.g. v2's `server.connected`) is still decoded and the tracker neither panics nor notifies for it.

```go
type StreamEvent struct {
    Type            string
    SessionID       string
    ParentID        string // non-empty for a subagent (child) session
    EventID         string // envelope id (v1 .payload.id, v2 top-level .id)
    Title           string // session title, from a lifecycle event
    ShellID         string // sh_… id from a shell event (session.shell.*, shell.created/shell.exited)
    ShellBackground bool   // true when the started shell counts as a running command (a background command, or a tool shell resource)
}

type EventStreamSpec struct {
    StreamCommand     string
    BusyEvents        []string
    AwaitingInput     []string
    IdleEvents        []string
    ErrorEvents       []string
    StickyErrorEvents []string // subset that latches the session error for the watcher's lifetime; empty = none latch
    SessionEvents     []string // lifecycle types whose payload may carry a parent link or title
    // SubtreeDoneStates opts into the subtree-aware done model below. v1 (false)
    // keeps the original "notify on any top-level succeeded" behavior; the new
    // lists are then empty.
    SubtreeDoneStates bool
    ShellStartedEvents []string // types carrying a background shell's start
    ShellEndedEvents   []string // types carrying a shell's end
    InterruptedEvents  []string // types that settle a session without an alert
    SessionList       *SessionListSpec // seeds sessions created before (re)connect; nil disables
    Parse             func(payload []byte) (StreamEvent, bool)
    Name              string // the agent's identity; not read for message text
}

// SessionRef is one session from the seed list. The parser returns every
// session, not children only: a top-level title must be learnable from the
// seed, and child links come from ParentID.
type SessionRef struct { ID, ParentID, Title string }

// SessionListSpec pairs a one-shot session-list command with the parser for its
// stdout. Both fields unexported so an incomplete pair is unrepresentable;
// NewSessionList panics on "", nil.
type SessionListSpec struct { command string; parse func([]byte) []SessionRef }
```

Per-profile decoders:

- `parseOpencodeV1Event`: type = `.payload.type`; sessionID = `.payload.properties.sessionID`; parentID = `.payload.properties.info.parentID`.
- `parseOpencodeV2Event`: type = top-level `.type`; sessionID = `.data.sessionID`, falling back to `.data.form.sessionID` (`form.created` nests it in `form`) and `.data.info.metadata.sessionID` (the shell tool's `shell.created`); parentID = `.data.parentID`, falling back to `.data.info.parentID`; title = `.data.title`. Shell resources come in two shapes: `session.shell.started`/`session.shell.ended` carry `.data.sessionID` and `.data.shell.id`/`.data.shell.metadata.background`; the agent's shell **tool** emits `shell.created` with `.data.info.id` and `.data.info.metadata.sessionID` (decoded as `ShellID`/`SessionID`, `ShellBackground` true) and `shell.exited` with `.data.id` and **no** session id (`ShellID` only).

The spec's `reflect.DeepEqual` assertion cannot compare a non-nil func, so compare a copy with `Parse` nilled and test the decoder separately.

## Tracker

The tracker consumes `agent.StreamEvent` and keeps, per session: its `state` (`idle`/`busy`/`awaitingInput`/`error`), its `parentOf` link, its `title`, its running background `shells`, and `lastInput` (session → last input event id).

1. Record `titles[e.SessionID]` when the event carries a `Title`, and `parentOf[e.SessionID] = e.ParentID` when it carries a `ParentID` on a `SessionEvents` type. Do **not** early-return: a lifecycle event like v1's `session.updated` is also a busy event and must keep marking busy for top-level sessions.
2. `ShellStartedEvents` with `ShellBackground` add `e.ShellID` to the session's shell set; `ShellEndedEvents` remove it. A shell end may carry no session id (the shell tool's `shell.exited`), so it is handled before the empty-session-id guard and removes the id from **every** session's set — a shell id is unique, so the scan is unambiguous.
3. `InterruptedEvents` settle the session (`stateIdle`) with **no** alert: an interrupted child must settle or the parent's done never drains.
4. A **child** session (non-empty `parentOf` chain) never notifies for its own done/error/interrupt. Under the subtree model the one exception is **input**: an `AwaitingInput` event for a child walks `parentOf` to its top-level ancestor and returns an input notification attributed to that ancestor (session id = the ancestor, `EventID` = the child's request id). Being needed is the whole tree blocking on the user. With `SubtreeDoneStates` off (v1) the exception does not apply: a child is fully suppressed, **including its input**, and only its state updates. The gate is `isChild && !SubtreeDoneStates` placed after the state update.
5. The awaiting-input arm notifies from busy or idle as before, and from awaiting input only when `e.EventID` is new for the session; it records `lastInput[e.SessionID] = e.EventID` and carries the id on the notification, so a second request in the same step notifies while a replayed event does not. From `stateError` nothing fires.
6. The error arm is unchanged: a `StickyErrorEvents` type latches `stateError`, any other error type sets `stateIdle` and notifies once.

### Subtree-aware done (`SubtreeDoneStates` on)

Every top-level `execution.succeeded` is classified by the **pending set** — the reasons the tree is not quiet:

- **`subagents`** — a descendant session (a session whose `parentOf` chain reaches this one) is live: busy or awaiting input.
- **`commands`** — at least one background shell (`ShellBackground`) is running **for the top-level session itself**.

The tracker sets `Notification.DoneReasons` to the present reasons and always emits `TriggerDone` (never holds or synthesizes). The alert fires when **any enabled reason is present**; `on-done` gates the empty set, and either of the two reason toggles gates its reason. A done with both reasons is one alert naming both, gated by either toggle. Both toggles default off, so under the default config a classified done (any pending reason) is silent and only the genuinely-quiet done alerts — it arrives as a later, real `execution.succeeded`, because the daemon wakes the parent after each subagent round and, observed on v2.3.0, after a background command exits (see "Verified event facts").

With `SubtreeDoneStates` off (v1), the idle arm behaves exactly as before: any top-level `busy`/`awaitingInput` → `idle` transition notifies done, regardless of descendants.

## Seeding and reconnect

`watchOnce` gains the spec. Immediately after `ShellStream` opens and before reading the stream, when `spec.SessionList != nil`, it runs `sb.Shell(ctx, spec.SessionList.Command())`, hands stdout to `spec.SessionList.Parse`, and calls `tracker.Seed(refs)`. Seeding is best-effort: a shell error, non-zero exit, or unreadable body is ignored and does not abort the stream (new children are still learned from `session.created`). Running it per-connect closes the gap for a child created while the watcher was disconnected.

`Seed(refs []SessionRef)` marks every ref with a non-empty `ParentID` as a child (the empty id is skipped, so a malformed seed can never mark the empty session id a child) and records every ref's `Title`.

A fresh stream has no replay, so in-flight subtree state from before a gap is unreliable. Each (re)connect therefore calls `tracker.ResetTransient()` before seeding: it clears the state of every child session and every running shell. Top-level session states persist across a reconnect. The bounded cost is a single plain done if a child was already live at reconnect — **never** a permanently silent done; `/api/session`'s `outcome` cannot substitute for a live signal (it is the *last* execution's outcome, and a running session reports `succeeded`).

The list's JSON shape lives in the agent (`SessionListSpec.parse`), like the event decoder: v1's `/session` is an array, v2's `/api/session` wraps them under `data`, and each parser keeps every session (child links come from `parentID`). This keeps the command a bare `curl` and avoids a `jq` dependency — the tool-owned toolbase is deliberately not in the runner's lineage, so a custom base cannot be assumed to have `jq`.

## Profile event lists

**opencode2** (`opencode2Name`):

```
StreamCommand:     "curl -N -s " + opencode2CurlAuth + " " + opencode2ServerURL + "/api/event"
BusyEvents:        {"session.execution.started", "session.step.started"}
AwaitingInput:     {"permission.asked", "form.created"}
IdleEvents:        {"session.execution.succeeded"}
ErrorEvents:       {"session.error", "session.execution.failed"}
StickyErrorEvents: {}                    // both error events are turn-scoped
SessionEvents:     {"session.created", "session.renamed"}
SubtreeDoneStates: true
ShellStartedEvents:{"session.shell.started", "shell.created"}
ShellEndedEvents:  {"session.shell.ended", "shell.exited"}
InterruptedEvents: {"session.execution.interrupted"}
SessionList: NewSessionList("curl " + opencode2CurlFlags + " " + opencode2CurlAuth + " " + opencode2ServerURL + "/api/session", parseOpencodeV2SessionList)
Parse: parseOpencodeV2Event
```

**opencode** (v1): `StreamCommand` and the four existing event lists verbatim, plus `SessionEvents: {"session.created", "session.updated"}`, `SessionList: NewSessionList("curl -sf -m 30 http://127.0.0.1:4096/session", parseOpencodeV1SessionList)`, `StickyErrorEvents: {"session.error"}`, `Parse: parseOpencodeV1Event`. `SubtreeDoneStates` is false and the shell/interrupt lists are empty. `session.idle` stays v1's done signal.

## Messages and config

Title: a fixed `agents-sandbox: ` prefix (the product name; the agent name is dropped), then `agent done` / `input requested` / `session error`. A done with pending work appends the reasons, e.g. `agents-sandbox: agent done (2 subagents, 1 command running)`. Body: `in session '<title>'`, falling back to the original sentence (`The agent finished.` / `The agent is waiting for your input.` / `The session reported an error.`) when the title is unknown. Wording applies to v1 and v2; only v1's trigger semantics are frozen.

Config keys (defaults in parentheses): `notify.on-input` (true), `notify.on-done` (true), `notify.on-error` (true), `notify.on-done-subagents-running` (**false**), `notify.on-done-commands-running` (**false**). Both reason defaults are deliberate: only a main session fully at rest notifies, so a done with a live subagent or a running background command stays silent until the tree is quiet, and the genuinely-quiet done arrives as the later real `execution.succeeded`. Enabling a reason toggle lets that done alert, distinguished by the appended reason. Env vars follow the mechanical convention (`AGENTS_SANDBOX_NOTIFY_ON_DONE_SUBAGENTS_RUNNING`, …). `--notify`/`AGENTS_SANDBOX_NOTIFY` still set channels only.

## Verified event facts

**opencode2 (v2.0.32; shell/title/interrupt re-verified on v2.3.0):** the SSE endpoint is `GET /api/event` and requires HTTP basic auth (the fixed credential from `specs/agents/opencode2.md`). Envelope is flat: `data: {"id":…,"created":…,"type":"session.created","data":{…},"durable":{…}}`. One user turn = one `session.execution.started`/`session.execution.succeeded` pair. `session.status` and `session.idle` are defined but never appeared on the server stream, so the execution pair is the observable busy/done. Input-needed events are `permission.asked` (sessionID at `.data.sessionID`) and `form.created` (sessionID at `.data.form.sessionID`). Execution events are a terminal union: an execution starts then emits exactly one of `succeeded`/`failed`/`interrupted` (with a `reason`: user/shutdown/superseded/inactivity). Events are attributed to the session that ran (a child's execution events carry the child id). `session.created` and `session.renamed` carry `title` flat at `data.title`; `session.created` carries `parentID` for a child, a top-level session omits it. `GET /api/session` (`{data:[{id,parentID,title,outcome,…}]}`) exposes `parentID` and `title`, but `outcome` is the last execution's outcome, not a live flag. A subagent is created with `parentID`; the daemon's API documents that after each completed round the child reports to the parent's inbox and **wakes the parent**. The same parent wake was observed for a background command: a background shell's `shell.exited` is followed by a new top-level `session.execution.started`/`session.execution.succeeded` pair, so a command-running turn also gets its later, quiet done. This is observed v2.3.0 behavior, not a documented daemon contract. Shell resources appear in two shapes. The session-shell API (`POST /api/session/{id}/shell`) emits `session.shell.started`/`session.shell.ended` carrying `data.sessionID` and `data.shell` (`Shell.Info`) whose `metadata.background` is true for a background command and whose `id` is `sh_…`. The agent's `shell` **tool** instead emits `shell.created` with `data.info` = `Shell.Info` (`{id, status:"running", command, …, metadata:{sessionID}}`) and `shell.exited` with `data = {id, exit, status}` and **no** session id; the tool never emits `session.shell.*`. A tool shell still running when the top-level execution succeeds is a leave-running command, so `shell.created` is decoded as `ShellBackground` true; a foreground tool shell exits before the idle. The session-shell API emits both `shell.created` and `session.shell.started` for one shell, so an id can be added twice (the set dedups) and removed twice (the second removal is a no-op).

**opencode (v1):** `GET /global/event` envelope `data: {"directory":…,"project":…,"payload":{"type":…,"properties":{…}}}`. v1 also exposes the parent link (`session.created` at `payload.properties.info.parentID`; `GET /session` lists `{id, parentID, …}`). v1 server auth is opt-in; the launcher's v1 image sets no password. The v1 stream has no replay, so the list seed is the only recovery for a missed `session.created`.

## Invariants

- **The harness's JSON shape lives in the agent, not in `notify`.** The spec carries `Parse func([]byte) (StreamEvent, bool)` per profile; `notify` keeps only SSE framing. The cost is that the spec's `reflect.DeepEqual` test must exclude `Parse`.
- **Done signal = `session.execution.succeeded` for v2**, not `session.idle`; v1 keeps `session.idle`.
- **`session.execution.interrupted` is not an error trigger** (a user interrupt is not a failure worth a signal), but it settles a session so a parent's done can drain.
- **A subagent's own done/error/interrupt never notifies; under the subtree model its input surfaces as the top-level ancestor's `input`.** Child suppression keys on the event's own `sessionID`; walking `parentOf` upward is the one exception, and it applies only when `SubtreeDoneStates` is on — v1 suppresses a child entirely, input included.
- **The opencode2 done is subtree-aware; v1's is not.** `SubtreeDoneStates` is the opt-in; the reasons are a set, so `subagents` and `commands` at once need no extra state. Nothing is held or synthesized — the daemon's parent wake produces the later, quiet `execution.succeeded`.
- **v2's error events are turn-scoped; only v1's `session.error` latches.** A non-sticky error sets `stateIdle`, so a same-turn `succeeded`/`idle` still cannot fire a false done.
- **Reconnect resets subtree liveness and shells** (`ResetTransient`) and re-seeds, so a missed child `succeeded` cannot suppress the main session's done forever.
- **`notify.Watch`'s whole dependency on the sandbox is the two-method `StreamOpener` seam** (`ShellStream`/`Shell`, each taking only a command); it never demands `msb.Sandbox`. `SandboxStreamOpener` adapts an `msb.SandboxStreamer` to the seam at the call site.
- **Cross-client dedup** (`internal/notify/dedup.go`) claims on (session, trigger, and for input the envelope id); a claim's release removes its file, so per-request tokens do not accumulate. `SessionBusy` releases a session's claims on the busy events. `state.AcquireClaim`'s release removes the claim file while holding the flock, with an inode check so a caller that opened a file another holder removed retries.
