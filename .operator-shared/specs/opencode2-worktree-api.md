# Contract: opencode2 worktree API (v2 routes, project scoping, base refs, tool-free commands)

Date: 2026-10-19 (revised 2026-10-21 for issue #257)
Status: implemented and merged — the v2 routes/project scoping (#235, PR #238) and the guest-`jq` removal (#257, PR #261). The remaining in-guest `curl` dependency is a build-checked custom-base requirement, tracked for removal as #263.

## Problem

`--worktree <name>` with the `opencode2` (opencode v2) agent has never worked: the profile
issues v1 worktree routes that v2 does not serve.

| Launcher call (today) | Result on v1 | Result on v2 |
|---|---|---|
| `GET /api/project/current` (project id) | 200 | **404** |
| `GET /api/worktree/$PID` (list) | 200 | **404** |
| `POST /api/worktree/$PID` (create) | 200 | **404** |

The credential added in #234 (`-u opencode:opencode`) fixed authentication only; the routes
still 404, so the session fails before attach.

Three v2 facts make this more than a route rename:

1. **The worktree routes are project-scoped.** Both need the project id, and the project is
   identified by the *directory* the request is scoped to. The profile cannot resolve it from a
   single command: the id comes from a *separate* response (`/api/location`), and the event that
   would otherwise register a project (attach) happens after `ResolveTarget` runs.
2. **The v1 create body does not fit v2's schema.** `Worktree.CreateInput` is
   `additionalProperties: false`, has no `strategy`, and `from` is an `AbsolutePath` (a directory),
   not a ref.
3. **Base refs differ.** v1 applies the base with `startCommand: "git reset --hard <base>"`; v2's
   native field is `branch`.

Verified against the pinned `opencode v2.0.32` — the version the runner image bakes and
`latestOpenCode2Version` returns (`opencode2_release_test.go`) — by driving a throwaway `opencode2
serve` with real HTTP calls, per the "Verified v2.0.32 API" section.

## Verified v2.0.32 API

- `GET /api/location?location[directory]=<dir>` → `{directory, project:{id, directory, canonical}}`.
  Resolves (and registers) the project for a directory in one request. The project is the enclosing
  git repository: a subdirectory and a linked worktree both resolve to the same `id`; `canonical` is
  the repository root, while `directory` on the project is the requested worktree directory.
- `GET /api/project` → an array of every project the server has seen (`{id, canonical, vcs?, name?,
  time:{created, updated, active}, sandboxes}`). Project ids are stable at 40 hex chars.
- `GET /api/worktree?projectID=<id>` → `[{directory, strategy?}]`. The list contains **every git
  worktree of the project, including the canonical root entry** (which carries no `strategy`). There
  is no `/api/worktree/{id}` route.
- `POST /api/worktree` with `Worktree.CreateInput {projectID (required), name?, directory?, from?,
  branch?}` → `Worktree.Info {"directory": "<absolute path>"}`. Semantics:
  - `name` + `directory`: `directory` is the **parent**; the result is `<directory>/<name>`.
  - `name` only: `$HOME/.local/share/opencode/worktree/<first 6 chars of project id>/<name>`.
  - neither: the default parent plus a randomly generated name.
  - A name that already exists is **never reused**: the server appends `-2`, `-3`, … and creates a
    new worktree. Reuse therefore stays a client-side decision (list first).
  - `from`: an absolute path of an existing worktree directory to create from; defaults to the
    project.
  - `branch`: an existing ref to base the new worktree on. An unresolvable ref fails the create
    (`fatal: invalid reference: <ref>`). Created worktrees are **detached HEAD** — v2 never leaves a
    worktree on a branch.
  - Failures return `WorktreeError {message, forceRequired?}` or `ProjectNotFoundError`.
- `Worktree.Info` and `Worktree.Directory` both carry `directory`, so the shared
  `{"directory": ...}` parser (`parseWorktreeDirectory`) already handles v2 responses.
- `/api/worktree/refresh` (POST) and `DELETE /api/worktree` exist; unused.

## Contract

### The `WorktreeProvider` seam (issue #257)

Every v2 worktree command runs in the guest via `sb.Shell`, and `jq` is not part of the runner's
lineage (the tool base is deliberately independent of the user's base), so a custom base without
`jq` breaks `--worktree`. The launcher therefore moves **all daemon JSON handling into Go, on the
agent that owns the daemon's shape** — the same direction #245 took for the notify session list.
The daemon's field names never appear outside the owning profile.

The seam exposes v2's protocol as explicit steps instead of one merged shell command:

- `WorktreeResolveCmd(target string) string` — the command that resolves the launch directory to a
  project. Empty for an agent whose routes are global (v1), which then skips the step.
- `WorktreeParseProject(stdout string) (WorktreeProject, bool)` — decodes the resolve response into
  `WorktreeProject{ID, Canonical}`, both required non-empty. `ok` is false otherwise, so a request is
  never sent with an empty `projectID` and the list is never filtered against a literal `"null"`.
- `WorktreeListCmd(project WorktreeProject) string` — the command that lists the project's
  worktrees. v1 ignores `project`.
- `WorktreeParseList(stdout string, project WorktreeProject) ([]string, bool)` — decodes the list
  into a **normalized, already-filtered list of directories**. Both providers fail closed: a
  non-array payload, or any entry lacking a non-empty string `directory`, returns `ok == false` (v1
  flattens the array of path strings or `{directory}` objects; v2 additionally drops the canonical
  root entry). The strict shape check is load-bearing for both: an entry whose field was renamed,
  blanked, or retyped would otherwise be skipped by the base-name match and a second worktree created
  beside it.
- `WorktreeCreateCmd(spec WorktreeSpec, project WorktreeProject) string` — the command that creates a
  worktree. For v2 the request body is built as JSON in Go and shell-quoted, so arbitrary names and
  refs need no guest tooling; v1's create body is unchanged (it already carries no `jq`).
- `WorktreeParseDir(stdout string) (string, bool)` — unchanged.

`agent.WorktreeSpec.Target` is removed: the resolved project replaces it, and the launch directory is
passed only to `WorktreeResolveCmd`.

`vm.ResolveTarget` orchestrates the steps and keeps the reuse decision: resolve (skipped when the
command is empty) → list → match a reused worktree by base name → validate the base → create. A
non-zero resolve exit aborts the run, and a `WorktreeParseProject` `ok == false` aborts likewise —
both before the list, each naming the resolve failure (mirroring the list's abort). The v2 create
**reuses the project resolved for the list** rather than re-fetching `/api/location`.

### Route and body semantics (unchanged by #257)

- **v2 resolves the project through `/api/location`**, not by scanning `/api/project`; the resolve
  command seeds `location[directory]=<target>` (shell-quoted) and reads `.project.id` and
  `.project.canonical`. This deviates from the issue's prescribed `GET /api/project` + canonical
  matching: `/api/project` lists only projects the server has already seen, and nothing registers
  this project before `ResolveTarget` runs. `/api/location` resolves and registers in one request.
- **v2 lists with `GET /api/worktree?projectID=<id>`** and the parsed list **excludes the canonical
  root entry**. Otherwise a worktree name equal to the repository directory name would silently
  "reuse" the main checkout and attach the session there instead of creating a worktree.
- **A failed or unparsable worktree list must never be read as "no worktrees".** curl's own non-zero
  exit propagates (`-fsS`), a non-success list aborts in `ResolveTarget` rather than falling through
  to create, and `WorktreeParseList` fails closed on a bad payload **for both providers**. A silent
  fall-through would abandon an existing worktree for a new server-suffixed one (v2 never reuses a
  name) and hide a daemon route or field rename. For v1 this tightens behavior: a malformed exit-0
  list previously read as "no worktrees" and created one — it now aborts. v1's global route returns a
  successful JSON array (`[]` when the project has no worktrees), so a non-success or malformed
  result is always a genuine failure, not an empty list.
- **v2 creates with `POST /api/worktree`** and the body `{"projectID": …, "name": …}`, plus
  `"branch": <base>` when a base is given. Deliberately absent: `strategy`, `directory`, `from`,
  `startCommand`.
  - No `directory`: the launcher keeps v1's convention of letting the daemon choose the worktree
    location, which lands under the opencode data dir on the home volume
    (`$HOME/.local/share/opencode/worktree/<short project id>/<name>`). Choosing a location is not
    part of this fix.
  - No `from`: `--worktree` has no stacked-worktree concept, and `from` is a directory, not a ref.
- **All v2 calls keep the baked credential** (`-u opencode:opencode`, `opencode2CurlAuth`) and the bounded flags
  (`opencode2CurlFlags`, `-fsS -m 30`), so a hung daemon cannot block a run.
- **Reuse matching is unchanged in effect**: `findWorktreeDir(dirs []string, slug string)` over the
  normalized directory list, exact because `--worktree` names are slugs (no separators; see below).
  It lives in `vm`, not in the profile: the base-name rule is not daemon-shape knowledge.
- **Worktree reporting is user-visible.** On both create and reuse the resolved worktree directory is
  reported at info level (default-visible). When the daemon auto-suffixes a name — the created
  directory's base name differing from the requested slug — a warning names the directory actually
  used. An agent whose daemon creates worktrees detached (opencode2) appends a detached-HEAD note via
  the optional `DetachedWorktreeReporter` capability, naming the `git switch -c <branch>` recovery;
  the note is **create-only** — a reused worktree's checkout state is unknown (the user may already
  have run the suggested `git switch`), so reuse must not restate the claim. v1, which keeps a
  branch, adds no note.
- **Base handling: the base is validated *before* the create, against the target directory, for both
  providers** (`git rev-parse --verify <base>^{commit}`, no fetch — the existing
  `validateWorktreeBase`, just moved ahead of the create and pointed at the source directory). The
  post-create validation is dropped. This is what produces v2's only realistic create failure (an
  unresolvable ref) as the existing friendly error instead of a bare `curl` exit code. On reuse the
  base is still ignored with the existing warning.
  - v1 keeps `startCommand: "git reset --hard <base>"` in its create body; v2 uses `branch`.

## Deviations (must be reflected in docs, not hidden)

- **Project resolution deviates from the issue's prescribed `GET /api/project` + canonical
  matching.** See the route-semantics bullet above.
- **Checkout state.** v1 leaves the worktree on its own branch with the base reset into it; v2 leaves
  a **detached HEAD** at the base ref (or at the source tip without a base). `docs/branch-sessions.md`
  must state this per agent.
- **Name validation is already stricter than the issue's repro suggests.** `ResolveWorktreeSpec`
  rejects anything that is not a slug, so `--worktree feature/x` never reaches the daemon; the real
  repro command is `--worktree feature-x`.
- **`curl` remains an in-guest prerequisite for opencode/opencode2** (health probe, event stream,
  session list, and these worktree calls). It is now an enforced custom-base requirement: the daemon
  profiles declare it via `agent.GuestToolRequirer`, and the image build
  checks it, so a base without it fails the build naming the tool. #257 removed `jq` specifically;
  removing `curl` would mean moving the daemon HTTP calls host-side, tracked as #263.

## Non-goals

- No worktree location control: the launcher sends no `directory`, so worktrees stay on the home
  volume. Nesting them inside the checkout remains `bare-worktree-layout.md`'s convention-only story,
  and `--worktree` there stays "kept as-is".
- No `from`/stacked worktrees, no `--worktree` CLI surface change (no new flag, no `:base` change).
- No daemon-side worktree removal or `refresh` wiring, and no worktree listing command in the CLI.
- No caching of project ids or worktree lists: resolution happens per run, only when `--worktree` is
  set (`ResolveTarget` still returns early for an empty name).
- No change to v1's routes or create body. v1 gains the new seam signatures it ignores, its resolve
  command is empty, and its list parser fails closed on a malformed payload (the one deliberate v1
  behavior change; see above).
- No removal of `curl` (see Deviations).

## Verification

- `opencode2Profile` unit tests: the resolve command is the `/api/location` curl and carries no `jq`;
  the list command is the `/api/worktree?projectID=` curl and carries no `jq`; the create command
  carries the Go-built JSON body (`projectID`/`name`/`branch`, no `strategy`) and no `jq`. No
  command string contains `jq`.
- `parseOpencode2WorktreeProject` tests over fixtures: a valid response yields `{ID, Canonical}`; an
  empty or missing `id`/`canonical` returns not-ok (the two fail-closed guards the jq prelude
  carried). `parseOpencode2WorktreeList` tests over the **same fixtures the jq filter used**: the
  canonical root is dropped (including a server-suffixed `name-2`), and a non-array payload or an
  entry without a non-empty string `directory` returns not-ok.
- `opencodeProfile` tests: routes and create body unchanged; the resolve command is empty; the list
  parser flattens strings and objects and returns not-ok on a non-array payload or an entry without a
  non-empty string `directory` (the v1 fail-closed change).
- `vm.ResolveTarget` tests: with a provider whose resolve command is empty the resolve step is
  skipped; the target reaches `WorktreeResolveCmd` and the resolved project reaches the list and
  create calls; a failed or unparsable resolve aborts before the list; the base is validated before
  the create; an unresolvable base aborts without creating; the create carries the detached-HEAD note
  for opencode2 while a reuse does not; the create reuses the project from the list (no second
  resolve).
- Manual end-to-end (user-side; the dev VM cannot start microsandbox VMs):
  `agents-sandbox run --agent opencode2 --worktree feature-x` creates, attaches, and a second run
  reuses the same directory; `-w feature-x:main` checks out at `main` (detached). Re-run against a
  custom base whose `FROM` is `debian:trixie-slim` (no `jq`): `--worktree` works.
