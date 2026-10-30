# Probing the running opencode2 daemon's event stream

How to observe the real `opencode2` (v2) SSE events from inside an agents-sandbox VM, without a
second machine or a KVM host. The agent's own VM runs the daemon, so it can probe itself.

## Reach the daemon

- Address and auth: `http://127.0.0.1:4096`, HTTP basic auth `opencode:<OPENCODE_SERVER_PASSWORD>`
  (both are in the VM's env; v2 hardcodes the username to `opencode`).
- `GET /api/info` is the health/version endpoint; `GET /openapi.json` is the full HTTP API schema.
- The event stream is `GET /api/event` (SSE). v1's `/global/event` exists on v2 but serves the web UI
  HTML, not SSE — do not use it to probe v2.
- Capture: `curl -N -s -u "opencode:$OPENCODE_SERVER_PASSWORD" http://127.0.0.1:4096/api/event`.

Envelope: `{"id":"evt_…","created":…,"type":"…","data":{…},"durable":{"aggregateID":…,"seq":…}}`.
`id` is unique per emission; `type` is top-level; the session id is `.data.sessionID` (for
`form.created` it is nested at `.data.form.sessionID`).

## Drive a turn that asks for permission

The default config allows tools, so `permission.asked` never fires. Force it with a per-session
permission rule whose `effect` is `ask` and whose `action` is the *tool name* (opencode2's shell tool
is `shell`, not `bash`):

```
# 1. create a session with an ask rule
curl -s -u "opencode:$OPENCODE_SERVER_PASSWORD" -H 'Content-Type: application/json' \
  -X POST http://127.0.0.1:4096/api/session \
  -d '{"permissions":[{"action":"shell","resource":"*","effect":"ask"}]}'
# -> {…,"id":"ses_…"}

# 2. start a capture first, then prompt it
curl -N -s -u "opencode:$OPENCODE_SERVER_PASSWORD" http://127.0.0.1:4096/api/event > stream.log &
curl -s -u "opencode:$OPENCODE_SERVER_PASSWORD" -H 'Content-Type: application/json' \
  -X POST http://127.0.0.1:4096/api/session/<SID>/prompt \
  -d '{"text":"Run two independent shell commands, each with its own shell tool call, in one message: echo one and echo two."}'

# 3. answer each pending request (decision: once | always | reject)
curl -s -u "opencode:$OPENCODE_SERVER_PASSWORD" http://127.0.0.1:4096/api/session/<SID>/permission
curl -s -u "opencode:$OPENCODE_SERVER_PASSWORD" -H 'Content-Type: application/json' \
  -X POST http://127.0.0.1:4096/api/session/<SID>/permission/<perID>/reply -d '{"decision":"once"}'
```

`Permission.Rule` is `{action, resource, effect}`, effect `allow|deny|ask`. `Permission.Reply` is
`once|always|reject`.

## Input-event shapes

- `permission.asked` → `data` is `Permission.Request`: `.data.id` (`per_…`), `.data.sessionID`,
  `.data.action`, `.data.resources`, `.data.source` (`{type:"tool", messageID, id}`).
- `form.created` → `.data.form.id` (`frm_…`), `.data.form.sessionID`.
- `permission.replied` → `.data.sessionID`, `.data.requestID`, `.data.reply`.

## Why this matters: two requests in one step

A prompt that makes the model call two tools in one assistant message produces both permission
requests **in the same step**, with no `session.step.started` between them. Observed order:

```
session.step.started
session.tool.input.started   (call A)
session.tool.input.started   (call B)
session.tool.input.ended / session.tool.called   (A, then B)
permission.asked             (A)
permission.asked             (B)      <- no step boundary between the two asks
permission.replied           (A) / session.tool.progress / session.tool.success
permission.replied           (B) / session.tool.progress / session.tool.success
session.step.ended
session.step.started         (next step)
session.text.*
session.execution.succeeded
```

So a watcher that re-arms only on a step boundary cannot fire the second ask; the two asks are
distinguishable only by their event ids. This is the evidence behind
`specs/notify-opencode2-subagent-filter.md` finding 6.

## Cleanup

Probe sessions stay in the daemon's session list; delete them with
`DELETE /api/session/<SID>` (see `/openapi.json`) when done so they do not clutter the workspace.
