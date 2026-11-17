---
title: Notifications
description: "Get desktop or audio notifications when a sandboxed agent session needs input, finishes or fails."
layout: default
parent: Configuration
nav_order: 70
---

# Notifications

agents-sandbox can alert you when the agent session status changes (input needed, done, error) via desktop and/or
audio notifications. The `opencode` and `opencode2` agents expose a session event stream; `pi`, `claude-code`, `codex`, and `hermes` do not,
so the notify config is ignored for them.

The `notify:` block controls the channels and the triggers:

| Field                         | Type    | Description                                                                                              |
|-------------------------------|---------|----------------------------------------------------------------------------------------------------------|
| `desktop`                     | boolean | Show a desktop notification (`notify-send` on Linux, `osascript` on macOS). Default `false`.             |
| `audio`                       | string  | Audio channel: `system` (bundled sound via `afplay`/`paplay`/`pw-play`/`aplay`), `bell` (terminal BEL), or `off`. Default `off`. |
| `on-input`                    | boolean | Notify when the agent is waiting on user input. Default `true`.                                          |
| `on-done`                     | boolean | Notify when a top-level session is fully at rest. Default `true`.                                        |
| `on-error`                    | boolean | Notify on a session error. Default `true`.                                                               |
| `on-done-subagents-running`   | boolean | Let a done alert fire while a subagent (child) session is still live. Default `false`.                   |
| `on-done-commands-running`    | boolean | Let a done alert fire while a background command is still running. Default `false`.                      |

Notifications are inactive unless at least one channel is enabled (`desktop` true or `audio` not `off`). When inactive,
the trigger toggles have no effect.

Once the `notify:` section is active (any key set), `on-input`, `on-done`, and `on-error` default to **true**; only the
two `on-done-*-running` reason toggles default to `false`.

A done alert fires when its pending reasons are enabled. `on-done` gates the empty set (the session is fully at rest),
and each reason toggle gates its own reason. `opencode2` reports the reasons; `opencode` (v1) never carries any, so its
`on-done` behaves as before. Under the defaults a done that still has live work stays silent — only the genuinely-quiet
done alerts, and it arrives as the session's later, real execution-succeeded event once the tree is quiet.

A subagent (child) session carries a `parentID`. `opencode2` tracks the whole session tree: a child never notifies for
its own done or error, but its input request surfaces as the top-level session's input, since the tree is blocked on
you. `opencode` (v1) keeps its original trigger semantics and only shares the message wording.

Alerts use a fixed `agents-sandbox:` title and the body `in session '<title>'` when the session title is known:

| Trigger | Title | Body |
|---------|-------|------|
| Input needed | `agents-sandbox: input requested` | `in session '<title>'` |
| Done | `agents-sandbox: agent done` | `in session '<title>'` |
| Error | `agents-sandbox: session error` | `in session '<title>'` |

A done with pending work appends its reasons, e.g. `agents-sandbox: agent done (2 subagents, 1 command running)`.
When the title is unknown, the body falls back to `The agent is waiting for your input.`, `The agent finished.`, or
`The session reported an error.`.

When multiple clients (`run` instances) are attached to the same project VM,
notifications are delivered at most once per agent session: the first client
to observe a transition claims a shared per-project token, and other clients
skip it. A later transition for the same session notifies again.

Note: deduplication is best-effort. If a client's event stream drops and reconnects across a session's busy→done transition, it may retain a stale claim and that one transition may not notify; it self-corrects on the next transition for that session.

```yaml
notify:
  desktop: true                      # notify-send (Linux) / osascript (macOS)
  audio: system                      # system | bell | off
  on-input: true                     # agent waiting on input
  on-done: true                      # a top-level session is fully at rest
  on-error: true                     # session error
  on-done-subagents-running: false   # alert a done while a subagent is live
  on-done-commands-running: false    # alert a done while a background command runs
```

The whole thing can be overridden with the `--notify` flag (`on`, `off`, `desktop`, or `audio`; both `--notify <mode>` and `--notify=<mode>` work, and bare `--notify` = `on`)
or the `AGENTS_SANDBOX_NOTIFY` environment variable. The override sets the **channels only**; it leaves the
`on-input`/`on-done`/`on-error` and `on-done-*-running` trigger toggles from the config file unchanged.
Precedence: **flag > env > config**.
