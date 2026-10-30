# Contract: opencode2 daemon server authentication

Date: 2026-10-19
Status: implemented

## Problem

`opencode2` is opencode **v2**. Unlike v1, v2's `serve` enforces HTTP basic
auth on every `/api/*` route. When `OPENCODE_SERVER_PASSWORD` is unset it
generates a random password and prints it to the log at startup, so the server
is never left unauthenticated. The launcher's health probe (`curl -sf http://127.0.0.1:4096/api/health`) sent no credentials,
got 401, and `ensureDaemon` polled until its 60s timeout — the daemon was
actually up and healthy, it just looked dead. The same applied to the worktree
API calls and the in-VM attach client.

v2's health endpoint is `/api/info`, which returns `ServerInfo` (there is no
`healthy` field). `/api/health` is a v1-preview route that matches nothing on
v2: middleware answered it with 401 before the credential fix, and once
authenticated it returned 404 with an empty body, which fails to parse as
JSON. The health parse therefore treats a decoded `version` as ready rather
than expecting v1's `{"healthy": bool}`.

v1 (`opencode`) is unaffected: its `/global/health` is unauthenticated and its
`OPENCODE_SERVER_PASSWORD` is genuinely opt-in.

## Contract

- The opencode2 runner image bakes a fixed `OPENCODE_SERVER_PASSWORD`
  (`ImageSpec.AgentEnv`). The value is a constant in the profile, not a secret:
  it only gates a loopback/tunnel-exposed dev server, and a known value is what
  lets the launcher and client agree without a state round-trip.
- v2 hardcodes the basic-auth username to `opencode`; `OPENCODE_SERVER_USERNAME`
  no longer exists. The effective credential is `opencode:<password>`.
- **Every** request the launcher makes to the v2 server must authenticate:
  `DaemonHealthCmd`, the project-id lookup, and the worktree list/create calls
  carry the `-u opencode:<password>` curl flag. `DaemonHealthCmd` targets
  `/api/info` and its parser accepts any decoded `ServerInfo` with a version.
  The in-VM attach client
  (`opencode2 --server …`) inherits the baked env var, so it needs no explicit
  flag; this relies on the image `ENV` reaching both the serve daemon and the
  login-shell attach session.
- Changing the fixed password is a breaking change for already-built images and
  for external `--serve-only` clients, which must connect as `opencode` with the
  baked password. A VM keeps serving the password baked into its image until it
  is rebuilt.

## Non-goals

- No per-VM random password and no state persistence of a credential (rejected
  in favour of the fixed env value for simplicity).
- No worktree-route correction. v2's worktree API is project-scoped
  (`GET /api/location?location[directory]=<dir>` resolves the project,
  `GET /api/worktree?projectID=<id>` lists, and `POST /api/worktree` with
  `projectID` in the body creates). At the time of #234 the `opencode2` profile
  still issued the v1 routes (`/api/project/current`, `/api/worktree/$PID`).
  Those calls carried the credential but returned 404, so `--worktree` for
  opencode2 was non-functional; corrected in `opencode2-worktree-api.md`
  (issue #235), which passes the target directory through the `WorktreeProvider`
  commands and resolves the project via `/api/location`.
- No health check via an unauthenticated endpoint (`/` web UI): it would report
  "listening", not "healthy", and would not fix attach/worktree auth.
- No reliance on upstream PR #46270 (make `serve` unauthenticated when the
  password is unset); the pinned beta still generates a password.
