---
title: Notifications
layout: default
parent: Configuration
nav_order: 70
---

# Notifications

opencode-sandbox can alert you when the opencode session status changes (input needed, done, error) via desktop and/or
audio notifications. Only daemon-based agents with an event stream support this; for interactive agents (e.g. `pi`,
`claude-code`) the notify config is ignored.

The `notify:` block controls the channels and the triggers:

| Field             | Type    | Description                                                                                              |
|-------------------|---------|----------------------------------------------------------------------------------------------------------|
| `desktop`         | boolean | Show a desktop notification (`notify-send` on Linux, `osascript` on macOS). Default `false`.             |
| `audio`           | string  | Audio channel: `system` (bundled sound via `afplay`/`paplay`/`pw-play`/`aplay`), `bell` (terminal BEL), or `off`. Default `off`. |
| `on-input`        | boolean | Notify when the agent is waiting on user input. Default `false`.                                         |
| `on-done`         | boolean | Notify when a `busy` session returns to `idle`. Default `false`.                                         |
| `on-error`        | boolean | Notify on a `session.error` event. Default `false`.                                                      |

Notifications are inactive unless at least one channel is enabled (`desktop` true or `audio` not `off`). When inactive,
the `on-input`/`on-done`/`on-error` trigger toggles have no effect.

When multiple clients (`run` instances) are attached to the same project VM,
notifications are delivered at most once per opencode session: the first client
to observe a transition claims a shared per-project token, and other clients
skip it. A later transition for the same session notifies again.

Note: deduplication is best-effort. If a client's event stream drops and reconnects across a session's busy→done transition, it may retain a stale claim and that one transition may not notify; it self-corrects on the next transition for that session.

```yaml
notify:
  desktop: true      # notify-send (Linux) / osascript (macOS)
  audio: system      # system | bell | off
  on-input: true     # agent waiting on input
  on-done: true      # busy -> idle
  on-error: true     # session.error
```

The whole thing can be overridden with the `--notify` flag (`on`, `off`, `desktop`, or `audio`; bare `--notify` = `on`)
or the `OPENCODE_SANDBOX_NOTIFY` environment variable. The override sets the **channels only**; it leaves the
`on-input`/`on-done`/`on-error` trigger toggles from the config file unchanged. Precedence: **flag > env > config**.
