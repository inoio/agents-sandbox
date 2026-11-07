---
description: Contract for the notify event watcher on opencode/opencode2 - the agent owns the harness JSON shape via `EventStreamSpec.Parse`/`SessionListSpec`, `notify` keeps only SSE framing; opencode2's authed `GET /api/event` envelope maps to the same triggers as v1, subagent sessions (`parentID` non-empty) never notify, and v2's turn-scoped errors do not latch while v1's `session.error` does.
read_if: Changing notify for opencode/opencode2, the event-stream decode seam (`agent.EventStreamSpec`/`Parse`/`SessionListSpec`), subagent-session filtering via `parentID`, the v1/v2 SSE event mapping, or the tracker's child/busy/idle/error state; or diagnosing "notify does nothing on opencode2" or a notification firing for a subagent session.
---

# notify: opencode/opencode2 event stream and subagent filtering

The watcher runs only when the agent implements `agent.EventStreamProvider`: `session/run.go` reads `provider.EventStream()` into a `streamSpec`, and a nil spec makes `startNotifyWatcher` a no-op. Both `opencode` (v1) and `opencode2` implement it; `pi`/`claude-code` do not.

A notification fires only for a **top-level** session — one with no `parentID`. A session with a non-empty `parentID` is a subagent (child) session and never notifies, including nested subagents. The channels (`desktop`, `audio: system|bell|off`), the trigger toggles (`on-input`, `on-done`, `on-error`), the wording (`<agent>: input needed` / `: done` / `: error`), and the cross-client dedup are unchanged.

Non-goals: richer notification text; new trigger kinds; filtering by anything other than `parentID`; any change to `pi`/`claude-code`.

## Decoder seam: the harness shape lives in the agent

`internal/notify` keeps only SSE **framing** (block assembly, `data:` extraction, comment skipping) and calls `spec.Parse(payload)`; nothing in `notify` knows a harness's JSON. Its local `Event` type is replaced by `agent.StreamEvent`. A payload with no type is not an event; a payload with a type but no session id (e.g. v2's `server.connected`) is still decoded and the tracker neither panics nor notifies for it.

```go
type StreamEvent struct {
    Type      string
    SessionID string
    ParentID  string // non-empty for a subagent (child) session
    EventID   string // envelope id (v1 .payload.id, v2 top-level .id)
}

type EventStreamSpec struct {
    StreamCommand     string
    BusyEvents        []string
    AwaitingInput     []string
    IdleEvents        []string
    ErrorEvents       []string
    StickyErrorEvents []string // subset that latches the session error for the watcher's lifetime; empty = none latch
    SessionEvents     []string // lifecycle types whose payload may carry a parent link
    SessionList       *SessionListSpec // seeds sessions created before (re)connect; nil disables
    Parse             func(payload []byte) (StreamEvent, bool)
    Name              string
}

// SessionListSpec pairs a one-shot session-list command with the parser for its
// stdout. Both fields unexported so an incomplete pair is unrepresentable;
// NewSessionList panics on "", nil.
type SessionListSpec struct { command string; parse func([]byte) []string }
```

Per-profile decoders:

- `parseOpencodeV1Event`: type = `.payload.type`; sessionID = `.payload.properties.sessionID`; parentID = `.payload.properties.info.parentID`.
- `parseOpencodeV2Event`: type = top-level `.type`; sessionID = `.data.sessionID`, falling back to `.data.form.sessionID` (`form.created` nests it in `form`); parentID = `.data.parentID`, falling back to `.data.info.parentID`.

The spec's `reflect.DeepEqual` assertion cannot compare a non-nil func, so compare a copy with `Parse` nilled and test the decoder separately.

## Tracker

The tracker consumes `agent.StreamEvent` and keeps `children map[string]bool` and `lastInput map[string]string` (session → last input event id):

1. If `e.SessionID != ""` and `e.Type` is in `spec.SessionEvents`, record `children[e.SessionID] = true` when `e.ParentID != ""`. Do **not** early-return: a lifecycle event like v1's `session.updated` is also a busy event and must keep marking busy for top-level sessions.
2. If `children[e.SessionID]`, return `nil` (no notification) before any trigger matching.
3. The busy/idle/error arms are unchanged. The awaiting-input arm notifies from busy or idle as before, and from awaiting input only when `e.EventID` is new for the session; it records `lastInput[e.SessionID] = e.EventID` and carries the id on the notification, so a second request in the same step notifies while a replayed event does not. From `stateError` nothing fires (the sticky latch holds).

`SeedChildren(ids []string)` marks every non-empty id as a child (the empty id is skipped, so a malformed seed can never mark the empty session id a child). Called by the watcher.

## Seeding on (re)connect

`watchOnce` gains the spec. Immediately after `ShellStream` opens and before reading the stream, when `spec.SessionList != nil`, it runs `sb.Shell(ctx, spec.SessionList.Command())`, hands stdout to `spec.SessionList.Parse`, and calls `tracker.SeedChildren(ids)`. Seeding is best-effort: a shell error, non-zero exit, or unreadable body is ignored and does not abort the stream (new children are still learned from `session.created`). Running it per-connect closes the gap for a child created while the watcher was disconnected.

The list's JSON shape lives in the agent (`SessionListSpec.parse`), like the event decoder: v1's `/session` is an array, v2's `/api/session` wraps them under `data`, and each parser keeps sessions with a non-empty `parentID`. This keeps the command a bare `curl` and avoids a `jq` dependency — the tool-owned toolbase is deliberately not in the runner's lineage, so a custom base cannot be assumed to have `jq`.

## Profile event lists

**opencode2** (`opencode2Name`):

```
StreamCommand: "curl -N -s " + opencode2CurlAuth + " " + opencode2ServerURL + "/api/event"
BusyEvents:    {"session.execution.started", "session.step.started"}
AwaitingInput: {"permission.asked", "form.created"}
IdleEvents:    {"session.execution.succeeded"}
ErrorEvents:   {"session.error", "session.execution.failed"}
StickyErrorEvents: {}                    // both error events are turn-scoped
SessionEvents: {"session.created"}
SessionList: NewSessionList("curl " + opencode2CurlFlags + " " + opencode2CurlAuth + " " + opencode2ServerURL + "/api/session", parseOpencodeV2SessionList)
Parse: parseOpencodeV2Event
```

**opencode** (v1): `StreamCommand` and the four existing event lists verbatim, plus `SessionEvents: {"session.created", "session.updated"}`, `SessionList: NewSessionList("curl -sf -m 30 http://127.0.0.1:4096/session", parseOpencodeV1SessionList)`, `StickyErrorEvents: {"session.error"}`, `Parse: parseOpencodeV1Event`. `session.idle` stays v1's done signal.

## Verified event facts

**opencode2 (v2.0.32):** the SSE endpoint is `GET /api/event` and requires HTTP basic auth (the fixed credential from `opencode2.md`). Envelope is flat: `data: {"id":…,"created":…,"type":"session.created","data":{…},"durable":{…}}`. One user turn = one `session.execution.started`/`session.execution.succeeded` pair. `session.status` and `session.idle` are defined but never appeared on the server stream, so the execution pair is the observable busy/done. Input-needed events are `permission.asked` (sessionID at `.data.sessionID`) and `form.created` (sessionID at `.data.form.sessionID`; v2 has no `question.asked`). Execution events are a terminal union: an execution starts then emits exactly one of `succeeded`/`failed`/`interrupted` (`interrupted` carries a `reason`), so a failed turn does not also emit `succeeded`. Events are attributed to the session that ran (a child's execution events carry the child id), so the child check on `.sessionID` suppresses a subagent's triggers. `session.created` carries `parentID` for a child; a top-level session omits it. `GET /api/session` (`{data:[{id,parentID,title,outcome,…}]}`) also exposes `parentID`.

**opencode (v1):** `GET /global/event` envelope `data: {"directory":…,"project":…,"payload":{"type":…,"properties":{…}}}`. v1 also exposes the parent link (`session.created` at `payload.properties.info.parentID`; `GET /session` lists `{id, parentID, …}`), so the filter is not v2-only — v1 was a dead end only because the *trigger* events lack the link. v1 server auth is opt-in; the launcher's v1 image sets no password. The v1 stream has no replay (a fresh subscription, `Last-Event-ID`, and `?since=`/`?after=`/`?cursor=`/`?seq=` emit only new events; the `sync` block is a durable inline duplicate), so the list seed is the only recovery for a missed `session.created`.

## Invariants

- **The harness's JSON shape lives in the agent, not in `notify`.** The spec carries `Parse func([]byte) (StreamEvent, bool)` per profile; `notify` keeps only SSE framing. The cost is that the spec's `reflect.DeepEqual` test must exclude `Parse`.
- **Done signal = `session.execution.succeeded` for v2**, not `session.idle` (defined but never observed). v1 keeps `session.idle`.
- **`session.execution.interrupted` is not an error trigger** (a user interrupt is not a failure worth a signal); `session.error` and `session.execution.failed` cover real errors.
- **Child suppression keys on the event's own `sessionID`**, verified to be the running session's id, so no second mechanism is needed for a subagent's input/error triggers.
- **v2's error events are turn-scoped; only v1's `session.error` latches.** The tracker sets a terminal error state on any error event; that is right for v1's session-level `session.error` but wrong for v2, whose execution events are explicitly per-turn, so `session.execution.failed` ends only that turn and the next turn notifies again. A non-sticky error sets `stateIdle`, so a same-turn `succeeded`/`idle` still cannot fire a false done (the idle arm requires a busy or awaiting-input state). v1's `session.error` latch is unchanged.
- **`notify.Watch`'s whole dependency on the sandbox is the two-method `StreamOpener` seam** (`ShellStream`/`Shell`); it never demands `msb.Sandbox`. `SandboxStreamOpener` adapts a sandbox to the seam at the call site, so a fake carrying those two operations drives the watcher.
- **Cross-client dedup** (`internal/notify/dedup.go`) claims on (session, trigger, and for input the envelope id); a claim's release removes its file, so per-request tokens do not accumulate. `SessionBusy` releases a session's claims on the busy events. `state.AcquireClaim`'s release removes the claim file while holding the flock, with an inode check so a caller that opened a file another holder removed retries.

## Call-site / test updates

- `internal/agent/capabilities_test.go`: the assertion becomes "opencode and opencode2 implement `EventStreamProvider`; pi and claude-code do not".
- `internal/notify/*_test.go`: `NewTracker` specs gain the new fields; `agent.StreamEvent` replaces `notify.Event`; the parser table test moves to the agent package; add child-suppression tests.
