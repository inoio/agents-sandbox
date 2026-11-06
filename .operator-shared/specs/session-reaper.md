---
description: Contract for what a session in retry does to the detach reaper's VM hold, and where the retry cap applies.
read_if: Changing waitQuiescent/quiescenceOf/sessionPoll, options.ReapPolicy, or auto-stop-max-session-retries; or diagnosing a VM that idles out while a session is still retrying, or one held past the retry cap.
---

# Session reaper quiescence

## Coverage

- The detach reaper in `internal/sandbox/session` (`waitQuiescent`, `pollQuiescence`, `quiescenceOf`, `sessionPoll`).
- `options.ReapPolicy` (`AutoStopOnActiveSessions`, `MaxSessionRetries`).
- The auto-stop section of `docs/sandboxes.md`.

## Contract

After the last client detaches, with `auto-stop-on-active-sessions` false, the reaper holds the VM (keeper exec) while either:

- any session is `busy` and has no pending, unanswered question; or
- any session is in `retry` with `attempt` below the cap (`auto-stop-max-session-retries`, default 10).

It releases — letting the idle timeout stop the VM — once no session is busy and no session is retrying below the cap.

- A `retry` below the cap is in-flight work and holds the VM.
- A `retry` at or beyond the cap is stuck and no longer holds the VM; the boundary is `attempt >= cap`.
- `MaxSessionRetries` is how many retry attempts a detach tolerates before giving up and letting the VM idle out. It is not a hold that only begins once the cap is reached.
- A `busy` session with a pending question counts as quiescent; a `busy` session without one holds. Both unchanged.

The poll that feeds the wait distinguishes three cases — no `retry`, a `retry` below the cap, and a `retry` at or beyond the cap — because the below-cap hold cannot be expressed by a single stuck-retry boolean.
