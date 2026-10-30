# Design: notify for opencode2 + subagent-session filtering

Date: 2026-10-20
Status: implemented (PR #245, pending merge). The review-driven refinements — event-id input identity, the `NewSessionList` seed value, per-request claim files — are in "Review fixes (PR #245)" below.

## Problem

The notify watcher never runs for the `opencode2` agent.

`internal/sandbox/session/run.go` starts the watcher only when the agent implements
`agent.EventStreamProvider`:

```go
var streamSpec *agent.EventStreamSpec
if provider, ok := agent.AsEventStreamProvider(a); ok {
    eventStream := provider.EventStream()
    streamSpec = &eventStream
}
stopNotify := startNotifyWatcher(ctx, sb, opts.Notify, ui, streamSpec, projectSlug)
```

A nil `streamSpec` makes `startNotifyWatcher` a no-op. Only `opencodeProfile` (v1) implements
`EventStream()`; `opencode2Profile` does not, so `--notify` (and the `notify:` config block) is
silently accepted (opencode2 *is* a `DaemonProvider`, so `resolveNotify`'s warning does not fire)
and does nothing. `internal/agent/capabilities_test.go` asserts this state
(`TestOnlyOpencodeImplementsEventStreamProvider`).

The secondary ask: **do not signal for subagent sessions** — only for top-level sessions the user
can interact with. v1's notify triggers (`session.idle`, `message.part.updated`,
`permission.asked`/`question.asked`) carry only a session id, so at the moment a trigger fires there
was no parent link available; this was previously treated as impossible.

## Goals

- Notify (desktop + audio, unchanged semantics) works for the `opencode2` agent.
- A notification fires only for a **top-level** session. "Top-level" means the session has **no
  `parentID`**; a session with a non-empty `parentID` is a subagent (child) session and never
  notifies — including nested subagents.
- `opencode` (v1) keeps working exactly as today, and gains the same subagent filter.
- No change to the channels (`desktop`, `audio: system|bell|off`), the trigger toggles
  (`on-input`, `on-done`, `on-error`), the notification wording, or the cross-client dedup.

## Non-goals

- Richer notification text (session title, agent/model, cost, error message). Wording stays
  `<agent>: input needed` / `: done` / `: error`.
- New trigger kinds (per-step, per-tool, usage). Only the existing input/done/error triggers.
- Filtering by anything other than `parentID` (no project/agent/title heuristics).
- Any change to `pi`/`claude-code` (no daemon/event stream; still unsupported).

## Context (verified)

Probed live against the pinned binaries: `opencode2 v2.0.32` (`/opt/agents-sandbox/bin/opencode2`)
and `opencode v1.19.3` (installed to a temp HOME for this exercise).

**opencode2 (v2)**

- The SSE endpoint is `GET /api/event` and it requires HTTP basic auth (`-u opencode:opencode`, the
  fixed credential from `opencode2-server-auth.md`). v1's `/global/event` path exists on v2 but
  serves the web-ui HTML (`200`, not SSE).
- Envelope is flat: `data: {"id":…,"created":…,"type":"session.created","data":{…},"durable":{…}}`.
  The event type is top-level `.type`; the session id is `.data.sessionID`.
- Event vocabulary observed over a full turn: `session.execution.started` → `session.step.started`
  → `session.text.started`/`session.text.delta`/`session.text.ended` → `session.step.ended` →
  `session.execution.succeeded`, plus `session.reasoning.*`, `session.usage.updated`,
  `session.renamed`, `session.inbox.*`.
- **One user turn = one `session.execution.started` / `session.execution.succeeded` pair.** A prompt
  containing two tool steps produced one execution pair with two `session.step` pairs.
- `session.status` and `session.idle` are defined in the binary but **never appeared on the server
  stream** during any probe (including a 26s+ idle wait); `session.execution.{started,succeeded}` is
  the observable busy/done pair.
- Input-needed events: `permission.asked` and `form.created` (v2 has no `question.asked`).
  `permission.asked`'s payload (`Permission.Request`) carries `sessionID` at `.data.sessionID`;
  `form.created`'s payload is `{form: Form.Info}` and carries it at **`.data.form.sessionID`**.
  Error events defined: `session.error` (`{sessionID, error}`), `session.execution.failed`,
  `session.execution.interrupted`.
- Execution events are a terminal union: an execution starts (`session.execution.started`) and then
  emits **exactly one** of `succeeded` / `failed` / `interrupted` (`interrupted` carries a
  `reason`: `user`/`shutdown`/`superseded`/`inactivity`). A failed turn does not also emit
  `succeeded`, so there is no spurious done-after-error.
- **Events are attributed to the session that ran.** Prompting a child session directly produced
  `session.execution.*` events carrying the *child's* id, not the parent's; the same holds for
  session-scoped input/error events, so the child check on `.sessionID` suppresses a subagent's
  triggers.
- **Subagent detection:** `session.created` carries the full session info and includes a
  `parentID` when the session is a child:
  ```json
  {"type":"session.created","data":{"sessionID":"ses_ea4f4539…","parentID":"ses_ea4fc6fc…","title":"child-probe",…}}
  ```
  A top-level session omits `parentID`. `GET /api/session` (`{data:[{id,parentID,title,outcome,…}]}`)
  and `GET /api/session/{id}` (`Session.Info`) also expose `parentID`; a live server showed real
  children alongside `parentID:null` roots.

**opencode (v1)**

- `GET /global/event` envelope: `data: {"directory":…,"project":…,"payload":{"type":…,"properties":{…}}}`.
- **v1 also exposes the parent link** — `session.created` carries it at
  `payload.properties.info.parentID`, and `GET /session` lists `{id, parentID, …}`:
  ```json
  {"payload":{"type":"session.created","properties":{"sessionID":"ses_…","info":{"…","parentID":"ses_ea4f037f…"}}}}
  ```
  So the filter is not a v2-only capability; v1 was a dead end only because the *trigger* events
  lack the link, not because the stream never carries it.
- v1 server auth is opt-in: with `OPENCODE_SERVER_PASSWORD` unset the server logs
  `Warning: OPENCODE_SERVER_PASSWORD is not set; server is unsecured.` and answers `200` on
  `/global/health` and `/session`. The launcher's v1 image sets no password, so an unauthenticated
  seed (and the existing `SessionStatusCmd`/`QuestionListCmd`) works; a user-set password would
  disable all three (a pre-existing limitation, not introduced here).
- **The v1 stream has no replay.** A fresh subscription after a session already exists, a
  `Last-Event-ID` header, and `?since=`/`?after=`/`?cursor=`/`?seq=` all emit only new events; the
  `sync` block is a durable *inline duplicate* of the ephemeral event (same tick, same `parentID`),
  not a catch-up. So the list seed is the **only** recovery for a `session.created` missed during a
  gap, and no cursor/`sync` handling is warranted.

**Existing code**

- `internal/agent/capabilities.go` — `EventStreamSpec{StreamCommand, BusyEvents, AwaitingInput,
  IdleEvents, ErrorEvents, Name}` and `AsEventStreamProvider`.
- `internal/notify/watch.go` — `Watch` (reconnect loop), `watchOnce` (one SSE stream),
  `parseSSEBlock` (v1 shape only), `handleEvent`, `streamReader`.
- **Invariant:** `notify.Watch`'s whole dependency on the sandbox is the two-method `StreamOpener`
  seam (`ShellStream`/`Shell`); it never demands `msb.Sandbox`. `SandboxStreamOpener` adapts a sandbox
  to the seam at the call site, so a fake carrying those two operations drives the watcher (#358).
- `internal/notify/event.go` — `Tracker{spec, states}`; `Handle` maps event types to
  bus/aparse input/idle/error transitions.
- `internal/notify/dedup.go` — cross-client claim keyed on (session, trigger, and for input the
  envelope id); a claim's release removes its file, so per-request tokens do not accumulate.
  `SessionBusy` releases a session's claims on the busy events.
- `internal/sandbox/state/claim.go` — `AcquireClaim`'s release removes the claim file while holding
  the flock, with an inode check so a caller that opened a file another holder removed retries.

## Design

### 1. Spec additions (`internal/agent/capabilities.go`)

```go
// StreamEvent is a decoded daemon event: its type, the session it belongs to,
// that session's parent (non-empty for a subagent session), and the event's
// envelope id.
type StreamEvent struct {
    Type      string
    SessionID string
    ParentID  string
    // EventID is the envelope id (v1 .payload.id, v2 top-level .id), unique per
    // emission. It tells two input requests apart when a session already awaits
    // input (a turn can ask for two approvals in one step).
    EventID string
}

type EventStreamSpec struct {
    StreamCommand string
    BusyEvents    []string
    AwaitingInput []string
    IdleEvents    []string
    ErrorEvents   []string
    // StickyErrorEvents is the subset of ErrorEvents that latches the session
    // in the error state for the watcher's lifetime — reconnecting does not
    // clear it, since the watcher reuses one tracker across reconnects. An
    // error event not listed here ends only the current turn. Empty means no
    // error event latches.
    StickyErrorEvents []string
    // SessionEvents are lifecycle event types whose payload may identify a
    // session's parent. A non-empty parent ID marks the session as a subagent
    // (child) session; no notification ever fires for a child session.
    SessionEvents []string
    // SessionList seeds the tracker with sessions created before the watcher
    // (re)connected. Nil disables seeding; a non-nil value always carries both
    // the command and its parser, so seeding can never be disabled by an
    // omitted parser. Build one with NewSessionList.
    SessionList *SessionListSpec
    // Parse decodes one SSE data payload into a StreamEvent. ok is false when
    // the payload carries no event.
    Parse func(payload []byte) (StreamEvent, bool)
    Name  string
}

// SessionListSpec pairs a one-shot session-list command with the parser for
// its stdout (whose JSON-shape ownership keeps the command jq-free, since the
// toolbase is not in a custom base's lineage). Both fields are unexported so an
// incomplete pair is unrepresentable; NewSessionList panics on "", nil.
type SessionListSpec struct {
    command string
    parse   func(payload []byte) []string
}
```

### 2. Decoder seam: the harness shape lives in the agent

`internal/notify` currently owns opencode's SSE *payload* JSON shape
(`parseSSEBlock` reads `payload.type` / `payload.properties.sessionID`). Teaching it a second shape
would put harness-specific code back in the generic package, against the original intent of
`EventStreamSpec` ("`internal/notify` contains no opencode references"). Instead, the agent supplies
the decoder:

- `notify` keeps only SSE **framing** — block assembly, `data:` extraction, comment skipping — and
  calls `spec.Parse(payload)`. Its `Event` type is replaced by `agent.StreamEvent`; nothing in
  `notify` knows a harness's JSON.
- `internal/agent/opencode.go` gets `parseOpencodeV1Event`: type = `.payload.type`; sessionID =
  `.payload.properties.sessionID`; parentID = `.payload.properties.info.parentID`.
- `internal/agent/opencode2.go` gets `parseOpencodeV2Event`: type = top-level `.type`; sessionID =
  `.data.sessionID`, falling back to `.data.form.sessionID` (`form.created` nests it in `form`);
  parentID = `.data.parentID`, falling back to `.data.info.parentID`.
- Each profile sets `Parse` in its `EventStream()` return value. Keep the rule that a payload with
  no type is not an event. A payload that carries a type but no session id (e.g. v2's
  `server.connected`) is still a decoded event; the tracker must neither panic nor notify for it.
- Parser table tests move from `internal/notify` to the agent package (and gain v2 fixtures).
- The existing `reflect.DeepEqual` assertion on the spec (`opencode_test.go`) cannot compare a
  non-nil func: compare a copy with `Parse` nilled and test the decoder separately.

### 3. Tracker (`internal/notify/event.go`)

The tracker consumes `agent.StreamEvent` instead of the removed local `Event`.

- Add `children map[string]bool` and `lastInput map[string]string` (session → last input event id).
- `Handle(e)`:
  1. If `e.SessionID != ""` and `e.Type` is in `spec.SessionEvents`, record
     `children[e.SessionID] = true` when `e.ParentID != ""`. Do **not** early-return: a lifecycle
     event like v1's `session.updated` is also a busy event and must keep marking busy for
     top-level sessions.
  2. If `children[e.SessionID]`, return `nil` (no notification) before any trigger matching.
  3. The busy/idle/error arms are unchanged. The awaiting-input arm notifies from busy or idle as
     before, and from awaiting input only when `e.EventID` is new for the session; it records
     `lastInput[e.SessionID] = e.EventID` and carries the id on the notification. So a second
     request in the same step notifies while a replayed event does not. From `stateError` nothing
     fires (the sticky latch holds).
- `SeedChildren(ids []string)` marks every non-empty id as a child (the empty id is skipped, so a malformed
  seed can never mark the empty session id a child). Called by the watcher.

### 4. Seeding on (re)connect (`internal/notify/watch.go`)

`watchOnce` gains the spec (it already has `sb`). Immediately after `ShellStream` opens, and before
reading the stream, when `spec.SessionList != nil`, run `sb.Shell(ctx, spec.SessionList.Command())`,
hand stdout to `spec.SessionList.Parse`, and call `tracker.SeedChildren(ids)`. Seeding is
best-effort: a shell error, a non-zero exit, or a body the parser cannot read is ignored and does
not abort the stream (the tracker still learns new children from `session.created`). Running it
per-connect, not just once, closes the gap for a child created while the watcher was disconnected.

The list's JSON shape lives in the agent (`SessionListSpec.parse`), like the event decoder: v1's
`/session` is an array of sessions, v2's `/api/session` wraps them under `data`, and each parser
keeps the sessions with a non-empty `parentID`. This keeps the command a bare `curl` and avoids a
`jq` dependency — the tool-owned toolbase is deliberately *not* in the runner's lineage, so a custom
base cannot be assumed to have `jq`.

### 5. Profiles (`internal/agent/opencode.go`, `internal/agent/opencode2.go`)

**opencode2 — new `EventStream()`:**

```go
StreamCommand: "curl -N -s " + opencode2CurlAuth + " " + opencode2ServerURL + "/api/event"
BusyEvents:    {"session.execution.started", "session.step.started"}
AwaitingInput: {"permission.asked", "form.created"}
IdleEvents:    {"session.execution.succeeded"}
ErrorEvents:   {"session.error", "session.execution.failed"}
StickyErrorEvents: {}  // both error events are turn-scoped
SessionEvents: {"session.created"}
SessionList: NewSessionList(
    "curl " + opencode2CurlFlags + " " + opencode2CurlAuth + " " +
        opencode2ServerURL + "/api/session",
    parseOpencodeV2SessionList)
Parse: parseOpencodeV2Event
Name:  opencode2Name
```

**opencode — unchanged behavior, plus the filter.** Keep `StreamCommand` and the four existing event
lists verbatim; add:

```go
SessionEvents: {"session.created", "session.updated"}
SessionList: NewSessionList("curl -sf -m 30 http://127.0.0.1:4096/session",
    parseOpencodeV1SessionList)
StickyErrorEvents: {"session.error"}
Parse: parseOpencodeV1Event
```

### 6. Call-site / test updates

- `internal/agent/capabilities_test.go`: the assertion that only opencode implements
  `EventStreamProvider` becomes "opencode and opencode2 do; pi and claude-code do not".
- `internal/notify/*_test.go`: `NewTracker` specs gain the new fields; `agent.StreamEvent` replaces
  `notify.Event`; the parser table test moves to the agent package; add child-suppression tests.

## Implementation approach

- `internal/agent`: add `StreamEvent`, add `Parse` to `EventStreamSpec`, add the two decoders and
  `EventStream()` to `opencode2Profile`, extend the `opencodeProfile` spec, update tests.
- `internal/notify`: drop the `Event` type and the JSON shape in `parseSSEBlock` (SSE framing only,
  delegating to `spec.Parse`); use `agent.StreamEvent`; `Tracker.children` + `SeedChildren`;
  `watchOnce` seeding; update `event_test.go`/`watch_test.go`/`watch_extra_test.go`.
- No change to `internal/sandbox/session/run.go`: it already passes whatever spec the provider
  returns.
- `docs/configuration/notifications.md` and `docs/commands.md`: opencode2 is supported; document that
  only top-level sessions notify.
- `CHANGELOG.md`: `[Unreleased]` line.

## Testing

- `internal/agent`:
  - Decoder table tests: v1 block → `{type, sessionID, parentID}`; v2 block → same; v2
    `form.created` → sessionID from `data.form.sessionID`; v2 `{sessionID, info}` shape → parentID
    from `info`; a typeless payload → not an event.
  - `opencode2Profile.EventStream()` field contents (with `Parse` compared separately);
    `opencodeProfile` spec gains the two new fields and keeps its others; capability table updated.
- `internal/notify`:
  - Tracker: a child's `session.execution.succeeded` after `session.execution.started` produces no
    notification; a top-level session produces `done`; a child announced by `session.created`
    (`parentID` set) then suppressed; `SeedChildren` suppresses without a `session.created`; a
    top-level `session.updated` still marks busy (v1 regression guard).
  - SSE framing: a `data:`-less block is not an event; `spec.Parse` is invoked with the extracted
    payload.
  - `watchOnce` seeding: seed command output marks children and the stream still processes events.
- Existing notify/session/CLI tests keep passing; `make check` green.

## Documentation & changelog

- `docs/configuration/notifications.md`: replace "Only the opencode agent …" with opencode and
  opencode2; add a line that top-level sessions notify and subagent sessions are skipped.
- `docs/commands.md`: the `--notify` row currently says "Only applies to the opencode agent"; update.
- `CHANGELOG.md`: `[Unreleased]` — notify now works for opencode2, and notifications are skipped for
  subagent sessions.

## Files touched (anticipated)

- `internal/agent/capabilities.go` (`StreamEvent`, `Parse`), `internal/agent/opencode.go`
  (`parseOpencodeV1Event`), `internal/agent/opencode2.go` (`parseOpencodeV2Event`, `EventStream()`)
  (+ `capabilities_test.go`, `opencode_test.go`, `opencode2_test.go`).
- `internal/notify/watch.go` (SSE framing only), `internal/notify/event.go` (uses
  `agent.StreamEvent`) (+ `watch_test.go`, `watch_extra_test.go`, `event_test.go`).
- `docs/configuration/notifications.md`, `docs/commands.md`, `CHANGELOG.md`.

## Calls made (decide-and-note)

- **The harness's JSON shape lives in the agent, not in `notify`.** Instead of teaching
  `parseSSEBlock` a second envelope, the spec carries a `Parse func([]byte) (StreamEvent, bool)`
  implemented per profile; `notify` keeps only SSE framing. A function field (rather than a second
  capability) keeps the stream contract one cohesive value; the cost is that the spec's
  `reflect.DeepEqual` test must exclude `Parse`.
- **Done signal = `session.execution.succeeded`**, not `session.idle`: the latter is defined in v2 but
  never observed on the server stream; the execution pair is the observable per-turn busy/done.
- **`session.execution.interrupted` is not an error trigger**: a user interrupt is not a failure
  worth a signal. It is left out; `session.error` and `session.execution.failed` cover real errors.
- **The seed list's JSON shape lives in the agent, not in `notify`.** `SessionListSpec.parse`
  extracts child ids from the raw session list, so `notify` keeps only `[]string` plumbing and the
  command stays a bare `curl`. The original call used `jq` in the command to normalise the v1 array
  versus v2's `.data[]`; the tool-owned toolbase is not in the runner's lineage, so a custom base
  (which the design says needs neither `curl` nor `tar`) cannot be assumed to have `jq`, and seeding
  would fail silently. The command and parser travel as one `NewSessionList` value, so a profile
  cannot wire one without the other; the spec's `reflect.DeepEqual` test excludes the parser (a func
  field on a pointer).
- **v1 keeps its existing event lists unchanged**; only `SessionEvents`/`SessionList` are added.
  `session.idle` stays v1's done signal.
- **Child suppression keys on the event's own `sessionID`**, verified to be the running session's id
  (a child's execution events carried the child id). No second mechanism is needed for a subagent's
  input/error triggers: they are session-scoped to the child.
- **v2's error events are turn-scoped; only v1's `session.error` latches.** The tracker sets a
  terminal error state on any error event, which used to permanently mute a session after its first
  error. That is right for v1's `session.error` (a session-level error state) but wrong for v2: the
  spec records v2's execution events as explicitly per-turn (each execution emits exactly one of
  succeeded/failed/interrupted), so a `session.execution.failed` ends only that turn and the next
  turn must notify again. `EventStreamSpec` gained `StickyErrorEvents`, the subset of `ErrorEvents`
  that latches; `opencode2` leaves it empty, and its `session.error` is turn-scoped too. The choice
  rests on `session.error`'s v2 semantics never being observed live (the probe recorded only that it
  is *defined*), so a sticky latch risks permanent silent muting; the cost if it is wrong is an extra
  done notification after a recovered error, the benign direction. A live failing-turn probe remains
  unverified. A non-sticky error sets `stateIdle`, so a same-turn `succeeded`/`idle` still cannot
  fire a false done — the idle arm requires a busy or awaiting-input state.
- **v1's `session.error` latch is unchanged.** The latch's origin was a coverage commit (`dc360d91`),
  not a documented contract; keeping v1 sticky preserves today's behavior exactly.

## Review fixes (PR #245)

Three Greptile findings on the first review, all fixed on the rebased branch:

1. **Later input requests in a v2 turn were silent (P1).** A v2 turn is one
   `session.execution.started`/`succeeded` pair with many steps, so after the first
   `permission.asked`/`form.created` the session stayed in `stateAwaitingInput`; a second request in the
   same turn matched neither the busy nor the idle arm of `Handle` and produced no notification, and the
   `(session, TriggerInput)` dedup claim from the first request stayed held. Fix: add
   `session.step.started` — an already-observed v2 event — to `BusyEvents`. A step start re-arms busy and
   `handleEvent` releases the session's claims (via `SessionBusy`), so the next request notifies and the
   first request is not repeated. Tests: `TestTrackerSecondInputInTurnFiresInput` and
   `TestHandleEventSecondInputInTurnNotifies`. **Residual uncertainty:** the fix relies on a
   `session.step.started` occurring between two input requests in one turn. That holds when each request
   gates its own step; if two requests fall inside one step (e.g. parallel tool calls) the second still
   cannot re-arm. A live probe of a two-permission turn is still needed to close this.
2. **v1's seed command had no time limit (P2).** `watchOnce` runs the session-list command synchronously
   before reading the stream, so a stalled `/session` would stop all notifications for the run. The v1
   command gained `-m 30`, matching v2's `opencode2CurlFlags`.
3. **`StickyErrorEvents` comment was wrong (P2).** It claimed the latch lasted "until the watcher
   reconnects", but `Watch` reuses one `Tracker` across reconnects, so the latch lasts the watcher's
   lifetime. Comment corrected; behavior unchanged.

A second review (on the rebased head) added:

4. **Custom runners miss child filtering (P2).** The seed command used `jq` to normalise the session
   list, but the tool-owned toolbase is not in the runner's lineage, so a custom base need not have
   `jq`; the command would fail and seeding would silently no-op, letting a child created before the
   watcher attached alert. Fix: `EventStreamSpec` gained `SessionListParse`, the list's JSON shape
   moved into the agents (`parseOpencodeV1SessionList`, `parseOpencodeV2SessionList`), and the
   command became a bare `curl`. Tests: the two `parseOpencode*SessionList` tables and the updated
   `TestWatchOnceSeedsChildrenFromListCommand`.
5. **The seed step's failure modes were untested, and the nil-parser skip was silent (P2).** A later
   Greptile comment on the rebased head found the seed-failure test no longer reached the command: a
   nil `SessionListParse` short-circuited it. The deeper issue was the seam — two independent fields
   let a profile declare a list command and silently lose seeding by omitting the parser, the same
   silent-no-op class as finding 4. Fix: the pair is one value, `*agent.SessionListSpec`, built only
   by `NewSessionList`, whose unexported fields and panic on an empty command or nil parser make an
   incomplete pair unrepresentable, so `watchOnce`'s guard is a plain `spec.SessionList != nil`.
   Tests pin all three seed outcomes: shell error, non-zero exit
   (`TestWatchOnceSeedNonSuccessDoesNotBlockNotifications`, the custom-base `jq: command not found`
   case), and an unparsable body (`TestWatchOnceSeedUnparsableDoesNotBlockNotifications`), each
   asserting the command ran and the done notification still fired;
   `TestNewSessionListRejectsIncompletePairs` pins the constructor's invariant.
6. **Two input requests in one step (P1, the last thread left open).** Finding 1 fixed the cross-step
   case by re-arming busy on `session.step.started`, but that was a proxy for "work resumed" and left
   the same-step case. A live probe (a session with `shell` set to `effect: ask`, prompted to run two
   commands — see the `opencode2-live-event-probe.md` guide) settled it: opencode2 emits the two
   `permission.asked` events **back to back in one step**, with only `session.tool.*` events between
   the request and the tools, and no `session.step.started`. So no step boundary re-arms, and the
   `(session, TriggerInput)` claim from the first request is still held. A resume rule cannot fix this
   (there is no resume event between the two asks); request identity is required. Fix:
   `agent.StreamEvent` gained `EventID` (the envelope id — v1 `.payload.id`, v2 top-level `.id`, both
   unique per emission), the tracker notifies a same-state input request only when its `EventID` is
   new (and carries it on the notification), and `Dedup` gives each input request its own token (the
   envelope id is part of the claim key), so distinct requests each alert and no client's delivery
   depends on another client's stream. `session.step.started` stays in `BusyEvents` (a step start is
   still busy), it is just no longer the mechanism. Tests:
   `TestTrackerSameStepInputRequestsBothFire`, `TestTrackerRepeatedInputEventFiresOnce`,
   `TestHandleEventSameStepInputRequestsBothNotify`, `TestHandleEventRepeatedInputEventNotifiesOnce`,
   plus the parser tables' `EventID` cases. Mutation-verified: reverting the tracker's id check fails
   the repeated-input test.
7. **Per-request tokens must not leak (P2), and a shared token must not couple clients (P1).**
   Keying the token on the event id (finding 6) left a claim file per request, because
   `state.AcquireClaim` created `<key>.claim` and release only closed the file. Greptile flagged that
   leak, so a first pass shared one input token per session — which then coupled a later request to
   the token holder's stream: if the holder's stream dropped it never saw the later request, and the
   other client dropped it too, so **neither** client alerted (a P1). The two constraints together
   force per-request tokens *and* removal: the token stays per (session, trigger, event id), and
   `state.AcquireClaim`'s release now removes the file while holding the flock, with an inode check
   (`sameFile`) so a caller that opened a file another holder removed retries on a fresh one instead
   of claiming a removed token. Bounded because release happens on the busy events (`SessionBusy`).
   Tests: `TestDedupReleasesInputClaims` (no files left after release),
   `TestDedupLaterInputNotBlockedByAnotherClient`, `TestDedupCrossClientInputsDeliveredOnce`,
   `TestDedupSameInputEventDeliveredOnce`, `TestAcquireClaimReleaseRemovesFile`,
   `TestAcquireClaimRetriesWhenFileRemovedUnderIt` and `TestAcquireClaimFlockErrorSurfaces` (via the
   `claimOpened` test seam), and `TestSameFile`. Mutation-verified: a per-session token fails the
   liveness test, and a non-removing release fails the cleanup test.

The review also surfaced an unrelated **latent race in `internal/sandbox/state.AcquireClientLease`/
`CountActiveClients`** (not part of this change): a lease file is created and only then flocked, and
`CountActiveClients` removed any file it could lock, so a poll landing between create and flock deleted the
lease and the live client became invisible. This made
`internal/sandbox/session.TestReapOnLastClient_ClientReattachesDuringWait` hang to its 10-minute timeout on
CI. Fixed separately in #298: `CountActiveClients` counts an unlocked lease younger than a grace window
(1s) as live, and removes an older one only while holding its own probe lock; `AcquireClientLease`
re-checks with `sameFile` after locking and retries on a fresh name when a poll unlinked the file first,
so a lease is never left locked on an anonymous inode.
