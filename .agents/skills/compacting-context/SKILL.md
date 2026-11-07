---
name: compacting-context
description: Use when told to compact or shrink your own context, especially at the end of a task ("implement the plan, afterwards compact your context"), or when the context window is filling.
---

# Compacting Your Own Context

There is no built-in "compact" tool — request the compaction with the opencode2 CLI:

```sh
opencode2 api session.compact --param sessionID="$OPENCODE_SESSION_ID" -d '{}'
```

- `$OPENCODE_SESSION_ID` (set in your shell) is your own session.
- Delivery is `steer`: it fires at the next step boundary. Run it as your **last** action, after any final answer or handoff.
- Exit 0 with a `"type":"compaction"` body means the request was admitted; the summary replaces the conversation afterwards.
- No `--server` needed — the CLI's background service shares the session database. If it reports the session missing, target the live server explicitly: `opencode2 --server http://127.0.0.1:4096 api session.compact --param sessionID="$OPENCODE_SESSION_ID" -d '{}'`.
