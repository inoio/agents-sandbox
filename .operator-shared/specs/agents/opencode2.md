---
description: Contract for the opencode2 (opencode v2) agent - its own launcher config namespace (`opencode2/`) with an `opencode` fallback while the guest path stays `opencode`, the baked fixed `OPENCODE_SERVER_PASSWORD` and `opencode:<password>` basic auth on every `/api/*` call, and the v2 worktree API (project-scoped routes, the `WorktreeProvider` seam with no guest `jq`, base-ref handling).
read_if: Changing the opencode2 profile, its config-namespace fork / `ConfigDirAliaser` / snippet merge / mirror resolution, `OPENCODE_SERVER_PASSWORD` / `DaemonHealthCmd` / the daemon health probe, the v2 worktree routes or `WorktreeProvider` seam, or `vm.ResolveTarget`/`validateWorktreeBase`; or diagnosing a dead-looking opencode2 daemon, `--worktree` that 404s or attaches to the main checkout, or a custom base without `jq` breaking `--worktree`.
---

# opencode2

opencode2 is opencode **v2**. Unlike v1, its `serve` enforces HTTP basic auth on every `/api/*` route, uses project-scoped worktree routes, and reads the same on-disk config locations as v1.

## Launcher config namespace fork

`opencode` (v1) and `opencode2` share one launcher config directory by default (`~/.config/agents-sandbox/opencode/` and the project equivalent), because `opencode2Profile` embeds `opencodeConfig`, whose `ConfigDirName()` returns `"opencode"`. That would make the two agents' merged snippets and verbatim mirror byte-identical by construction, with no way to give v1 and v2 divergent launcher config — and v2 has a native config schema (e.g. structured `providers.*.models.*.capabilities.reasoning`) that v1 cannot understand.

`opencode2` therefore owns its launcher namespace (`.../opencode2/`) so the two can diverge, **without** changing anything the VM sees: the guest config path stays `~/.config/opencode/opencode.jsonc` and the host drop-in stays `~/.config/opencode/` (v2 reads the same on-disk locations as v1).

### The fork

`opencode2Profile` overrides `ConfigDirName() string` to return `"opencode2"`. Snippet merge and mirror then read `.../opencode2/`, while `VMConfigPath`/`ProvisionRules`/`ConfigFileNames`/`SnippetPattern` continue to come from the embedded `opencodeConfig` and remain `opencode`.

opencode (v1) behavior is unchanged. The one deliberate exception: the shared resolver `os.Stat`s candidate dirs for every agent, so a non-"not exist" stat error on a candidate surfaces instead of being swallowed. The `home:` manifest and hooks are read from the launcher **root** (`~/.config/agents-sandbox/`), one level above the agent subdir, and stay shared.

### Fallback: declarative aliases + consumer-side resolver

`opencode2Profile` implements the optional `ConfigDirAliaser` capability (`agent.ConfigDirAliaser`, `agent.AsConfigDirAliaser`), returning `[]string{"opencode"}`; agents that never shared a dir (opencode, pi, claude-code) do not implement it, so the agent owns declarative names only. This is chosen over "agent resolves concrete paths" (violates the config-paths seam and puts host I/O in pure profiles) and over "configpaths stats the filesystem" (smears an I/O decision into the pure path factory).

Name→path resolution is a pure function, `agent.ConfigDirNames(a) ([]string, error)`: the primary `ConfigDirName()` first, then each alias. `reprovision.absoluteAgentConfigDir` joins those names under each config scope via `ConfigScope.AgentConfigDir`. Each name is validated as a single clean path element (rejecting `""`, `.`, `..`, and any name containing the path separator — including a bare `/`) by `validateConfigDirName`, which **returns an error**; `ConfigDirNames` and `ResolveConfigSource` propagate it. Without this, `filepath.Join` cleans `..`, so an unvalidated `..` would resolve to the launcher config root and the mirror would copy its files — including `env.secret` — into the VM, bypassing the msb-secret rule. Resolution is pure name→path and never creates a directory.

`reprovision.ResolveConfigSource(a agent.Agent, scopes []configpaths.ConfigScope) (ConfigSource, error)` owns existence/precedence/warning. `ConfigSource` carries `UserDirs` (every user scope's dir, in precedence order) and `ProjectDir`, the `Fallback` legacy dir name actually in use (`""` for primary), and the primary `PrimaryUserDirs`/`PrimaryProjectDir` the agent migrates to. The resolver rejects an empty or length-mismatched candidate pair, and an `os.Stat` error other than "not exist" is propagated rather than treated as absent. Two intended edge behaviors: a stat error on the **primary** aborts resolution before aliases are considered (fail-closed), and when one scope is unreadable but the other resolves, resolution proceeds with the readable scope and emits no warning (the warning is keyed to `Fallback`).

### The signal: "an opencode2 dir anywhere"

At any time the agent reads exactly one source per scope — never both, never merged; resolution is directory-granular, with no per-file fallback. `.../opencode2/` is never auto-created: the directory's existence is the switch. Existence is checked on directories (`os.Stat` + `IsDir`) across **both scopes**:

- The resolution picks **one source name globally**, then reads that name in both scopes — it never mixes names across scopes.
- If a **primary** dir exists in *either* the user or the current-project scope, both scopes resolve to the primary dirs. An absent scope contributes nothing; a legacy dir in the other scope is ignored.
- Only when **no** primary dir exists anywhere do both scopes fall back to the **first alias whose dir exists in either scope**; `Fallback` is that alias's name. With multiple aliases, order decides; at most one alias is ever active.

| user `…/opencode2` | user `…/opencode` | proj `…/opencode2` | proj `…/opencode` | reads user | reads proj | warns |
|---|---|---|---|---|---|---|
| ✓ | — | — | — | opencode2 | opencode2 | no |
| — | ✓ | — | — | opencode | opencode | yes |
| — | — | ✓ | — | opencode2 | opencode2 | no |
| — | — | — | ✓ | opencode | opencode | yes |
| — | — | — | — | opencode2 | opencode2 | no |
| ✓ | ✓ | ✓ | ✓ | opencode2 | opencode2 | no |
| — | ✓ | ✓ | — | opencode2 | opencode2 | no |

### Warning

Emitted at the single config-load boundary (`reprovision.LoadConfigFilesForHost`) when `Fallback != ""` (which by construction implies a legacy dir exists), always — not gated to once. It names the agent, the full legacy user/project paths actually read (`reprovision.SourceDirs`, verbatim so display cannot diverge from what was read), and the primary paths to migrate into. It says **copy** (do not move) because the legacy dir is also the `opencode` agent's live dir; it states that an `opencode2` dir in either scope switches both scopes, that a partial copy will not fall back for the rest, and that the guest `~/.config/opencode` path is unchanged. It names only the legacy scopes whose dir exists. Paths are absolute (the project dir resolves through `configpaths.ProjectConfigDirAbs`). The load runs once per process; `config agent` invokes it once. A regression test asserts the warning fires exactly once per load.

### Deprecation stance

No register entry and no alias cycle: the legacy **path** is not a flag/key/env/command, so `../conventions/deprecation-policy.md` does not apply. The fallback is open-ended.

### Reconfig / fingerprints

Guest keys (`ConfigFiles.Keys` = VM paths) are unchanged, so reconfig fingerprints are unaffected. Content differences between v1 and v2 dirs produce different materialized config per agent VM, as designed. Known pre-existing limitation (now reachable via the source switch): `reprovision.ChangedAgentConfigPaths` compares content but not mirror *removals*, so moving to a source with strictly fewer mirror files leaves the stale VM copies without a daemon restart. Out of scope; noted for a follow-up.

### Accepted tradeoff

The signal is binary: once a primary dir exists in any scope, the legacy dir is ignored entirely. An accidentally-created **empty** `.../opencode2/` dir silently disables the fallback, and a **partial** migration silently drops the leftovers — the warning does not fire in either case (it is tied to `Fallback != ""`). Deliberate cost of "existence is the signal"; the warning is deliberately not widened to fire on partial migration.

`ResolveConfigSource` lives in `reprovision` (the module that already owns reading the agent config and its precedence). No shared "effective agent config dir" API is introduced until a real consumer needs one (YAGNI): a consumer that needs the primary dirs takes them from `ConfigSource`.

## Daemon authentication

v2's `serve` enforces HTTP basic auth on every `/api/*` route; when `OPENCODE_SERVER_PASSWORD` is unset it generates a random password and prints it to the log, so the server is never unauthenticated. A health probe without credentials gets 401, so `ensureDaemon` polled until its 60s timeout even though the daemon was healthy.

- The opencode2 runner image bakes a fixed `OPENCODE_SERVER_PASSWORD` (`ImageSpec.AgentEnv`). The value is a constant in the profile, not a secret: it only gates a loopback/tunnel-exposed dev server, and a known value is what lets the launcher and client agree without a state round-trip.
- v2 hardcodes the basic-auth username to `opencode`; `OPENCODE_SERVER_USERNAME` no longer exists. The effective credential is `opencode:<password>`.
- **Every** request the launcher makes to the v2 server must authenticate: `DaemonHealthCmd`, the project-id lookup, and the worktree list/create calls carry the `-u opencode:<password>` curl flag. `DaemonHealthCmd` targets `/api/info` and its parser accepts any decoded `ServerInfo` with a version (v2 has no `/api/health` route and no `healthy` field; `/api/health` is a v1-preview route that returns 404). The in-VM attach client (`opencode2 --server …`) inherits the baked env var, so it needs no explicit flag — this relies on the image `ENV` reaching both the serve daemon and the login-shell attach session.
- Changing the fixed password is a breaking change for already-built images and for external `--serve-only` clients, which must connect as `opencode` with the baked password. A VM keeps serving the password baked into its image until it is rebuilt.

v1 (`opencode`) is unaffected: its `/global/health` is unauthenticated and its `OPENCODE_SERVER_PASSWORD` is genuinely opt-in.

Non-goals: no per-VM random password and no state persistence of a credential (a fixed env value is simpler); no reliance on an unauthenticated endpoint (`/` web UI reports "listening", not "healthy"); no reliance on making `serve` unauthenticated when the password is unset.

## Credential store

Verified on opencode2 2.2.6:

- v2 keeps provider credentials in `opencode.db` (table `credential`), not in `auth.json`. The `import_legacy_credentials` migration reads `~/.local/share/opencode/auth.json` only when it migrates an existing database. A fresh database, which every new home volume starts with, is bootstrapped with every migration marked done, so a provisioned `auth.json` is never imported.
- Copilot activates only through a connection backed by an `oauth` credential; the integration removes the key method. A config `options.apiKey` does not create one. API-key providers declared in `opencode.jsonc` keep working without a credential.
- `POST /api/credential` (`{integrationID, label, value, activate}`, basic auth like every `/api/*` route) stores a credential. v2's own Copilot device login stores `value: {type: "oauth", methodID: "device", refresh: <GitHub OAuth token>, access: <same>, expires: 0, metadata: {enterpriseUrl?}}` and sends `refresh` as the Bearer token to `copilot-api.<domain>` (or the default endpoint). Storing an msb placeholder there keeps the real token out of the VM, which is the documented user workaround (`docs/configuration/secrets.md`, "GitHub Copilot with opencode2").
- This is user-side coupling to an undocumented API. The launcher does not register credentials itself; doing so would put v2 field names into the profile and is not decided.

## Worktree API (v2 routes, project scoping, base refs)

`--worktree <name>` with opencode2 previously issued v1 worktree routes that v2 does not serve (`GET /api/project/current`, `GET /api/worktree/$PID`, `POST /api/worktree/$PID` all 404 on v2). The credential fixed authentication only. Three v2 facts make this more than a route rename: the worktree routes are **project-scoped** (the id comes from a separate response and nothing registers the project before `ResolveTarget` runs), the v1 create body does not fit v2's `additionalProperties: false` schema, and base refs differ (v1 uses `startCommand: "git reset --hard <base>"`, v2 uses `branch`).

### Verified v2.0.32 API

- `GET /api/location?location[directory]=<dir>` → `{directory, project:{id, directory, canonical}}`. Resolves (and registers) the project for a directory in one request. A subdirectory and a linked worktree both resolve to the same `id`; `canonical` is the repository root.
- `GET /api/project` → an array of every project the server has seen. Project ids are stable at 40 hex chars.
- `GET /api/worktree?projectID=<id>` → `[{directory, strategy?}]`, containing **every git worktree including the canonical root entry** (which carries no `strategy`). There is no `/api/worktree/{id}` route.
- `POST /api/worktree` with `Worktree.CreateInput {projectID (required), name?, directory?, from?, branch?}` → `Worktree.Info {"directory": "<absolute path>"}`. `name` + `directory`: `directory` is the **parent**, result `<directory>/<name>`. `name` only: `$HOME/.local/share/opencode/worktree/<first 6 chars of project id>/<name>`. Neither: default parent + random name. An existing name is never reused (server appends `-2`, `-3`, …), so reuse stays client-side. `from`: an absolute path of an existing worktree to create from. `branch`: an existing ref; an unresolvable ref fails the create. Created worktrees are **detached HEAD** — v2 never leaves a worktree on a branch. Failures return `WorktreeError {message, forceRequired?}` or `ProjectNotFoundError`.
- `Worktree.Info` and `Worktree.Directory` both carry `directory`, so the shared `{"directory": ...}` parser (`parseWorktreeDirectory`) handles v2 responses. `/api/worktree/refresh` (POST) and `DELETE /api/worktree` exist; unused.

### The `WorktreeProvider` seam

Every v2 worktree command runs in the guest via `sb.Shell`, and `jq` is not part of the runner's lineage (the tool base is deliberately independent of the user's base), so a custom base without `jq` breaks `--worktree`. The launcher therefore moves **all daemon JSON handling into Go, on the agent that owns the daemon's shape**. The daemon's field names never appear outside the owning profile.

The seam exposes v2's protocol as explicit steps instead of one merged shell command:

- `WorktreeResolveCmd(target string) string` — resolves the launch directory to a project. Empty for an agent whose routes are global (v1), which then skips the step.
- `WorktreeParseProject(stdout string) (WorktreeProject, bool)` — decodes the resolve response into `WorktreeProject{ID, Canonical}`, both required non-empty. `ok` is false otherwise, so a request is never sent with an empty `projectID`.
- `WorktreeListCmd(project WorktreeProject) string` — lists the project's worktrees. v1 ignores `project`.
- `WorktreeParseList(stdout string, project WorktreeProject) ([]string, bool)` — decodes the list into a **normalized, already-filtered list of directories**. Both providers fail closed: a non-array payload, or any entry lacking a non-empty string `directory`, returns `ok == false` (v1 flattens the array of path strings or `{directory}` objects; v2 additionally drops the canonical root entry). The strict shape check is load-bearing: an entry whose field was renamed, blanked, or retyped would otherwise be skipped by the base-name match and a second worktree created beside it.
- `WorktreeCreateCmd(spec WorktreeSpec, project WorktreeProject) string` — creates a worktree. For v2 the request body is built as JSON in Go and shell-quoted, so arbitrary names and refs need no guest tooling; v1's create body is unchanged.
- `WorktreeParseDir(stdout string) (string, bool)` — unchanged.

`agent.WorktreeSpec.Target` is removed: the resolved project replaces it, and the launch directory is passed only to `WorktreeResolveCmd`.

`vm.ResolveTarget` orchestrates the steps and keeps the reuse decision: resolve (skipped when the command is empty) → list → match a reused worktree by base name → validate the base → create. A non-zero resolve exit aborts the run, and a `WorktreeParseProject` `ok == false` aborts likewise — both before the list. The v2 create **reuses the project resolved for the list** rather than re-fetching `/api/location`.

### Route and body semantics

- **v2 resolves the project through `/api/location`**, not by scanning `/api/project`; the resolve command seeds `location[directory]=<target>` (shell-quoted) and reads `.project.id` and `.project.canonical`. This deviates from the prescribed `GET /api/project` + canonical matching: `/api/project` lists only projects the server has already seen, and nothing registers this project before `ResolveTarget` runs.
- **v2 lists with `GET /api/worktree?projectID=<id>`** and the parsed list **excludes the canonical root entry**. Otherwise a worktree name equal to the repository directory name would silently "reuse" the main checkout.
- **A failed or unparsable worktree list must never be read as "no worktrees".** curl's own non-zero exit propagates (`-fsS`), a non-success list aborts in `ResolveTarget`, and `WorktreeParseList` fails closed on a bad payload **for both providers**. A silent fall-through would abandon an existing worktree for a new server-suffixed one and hide a daemon route or field rename. For v1 this tightens behavior: a malformed exit-0 list previously read as "no worktrees"; it now aborts. v1's global route returns `[]` when the project has no worktrees, so a non-success result is a genuine failure.
- **v2 creates with `POST /api/worktree`** and the body `{"projectID": …, "name": …}`, plus `"branch": <base>` when a base is given. Deliberately absent: `strategy`, `directory`, `from`, `startCommand`. No `directory`: the launcher keeps v1's convention of letting the daemon choose the location under the opencode data dir on the home volume. No `from`: `--worktree` has no stacked-worktree concept.
- **All v2 calls keep the baked credential** (`-u opencode:opencode`, `opencode2CurlAuth`) and the bounded flags (`opencode2CurlFlags`, `-fsS -m 30`), so a hung daemon cannot block a run.
- **Reuse matching is unchanged in effect**: `findWorktreeDir(dirs []string, slug string)` over the normalized directory list, exact because `--worktree` names are slugs. It lives in `vm`, not the profile: the base-name rule is not daemon-shape knowledge.
- **Worktree reporting is user-visible.** On both create and reuse the resolved directory is reported at info level. When the daemon auto-suffixes a name, a warning names the directory actually used. An agent whose daemon creates worktrees detached (opencode2) appends a detached-HEAD note via the optional `DetachedWorktreeReporter` capability, naming the `git switch -c <branch>` recovery; the note is **create-only** — a reused worktree's checkout state is unknown. v1 adds no note.
- **Base handling: the base is validated *before* the create, against the target directory, for both providers** (`git rev-parse --verify <base>^{commit}`, no fetch — the existing `validateWorktreeBase`, moved ahead of the create and pointed at the source directory). The post-create validation is dropped. On reuse the base is still ignored with the existing warning. v1 keeps `startCommand: "git reset --hard <base>"`; v2 uses `branch`.

### Deviations (must be reflected in docs)

- Project resolution uses `/api/location`, not the prescribed `GET /api/project` + canonical matching.
- Checkout state: v1 leaves the worktree on its own branch with the base reset into it; v2 leaves a **detached HEAD** at the base ref (or the source tip without a base). `docs/branch-sessions.md` states this per agent.
- Name validation is stricter than a naive repro: `ResolveWorktreeSpec` rejects anything that is not a slug, so `--worktree feature/x` never reaches the daemon; the repro command is `--worktree feature-x`.
- **`curl` remains an in-guest prerequisite for opencode/opencode2** (health probe, event stream, session list, worktree calls). It is an enforced custom-base requirement: the daemon profiles declare it via `agent.GuestToolRequirer`, and the image build checks it, so a base without it fails the build naming the tool.

### Non-goals

- No worktree location control: the launcher sends no `directory`, so worktrees stay on the home volume. Nesting them inside the checkout remains `../worktrees/worktree-layout.md`'s convention-only story, and `--worktree` there stays "kept as-is".
- No `from`/stacked worktrees, no `--worktree` CLI surface change.
- No daemon-side worktree removal or `refresh` wiring, and no worktree listing command in the CLI.
- No caching of project ids or worktree lists: resolution happens per run, only when `--worktree` is set.
- No change to v1's routes or create body. v1 gains the new seam signatures it ignores, its resolve command is empty, and its list parser fails closed on a malformed payload.
- No removal of `curl`.

### Verification

- `opencode2Profile` unit tests: the resolve command is the `/api/location` curl and carries no `jq`; the list command is the `/api/worktree?projectID=` curl and carries no `jq`; the create command carries the Go-built JSON body (`projectID`/`name`/`branch`, no `strategy`) and no `jq`.
- `parseOpencode2WorktreeProject`/`parseOpencode2WorktreeList` fixtures: a valid response yields `{ID, Canonical}`; an empty/missing `id`/`canonical` is not-ok; the canonical root is dropped (including a server-suffixed `name-2`); a non-array payload or an entry without a non-empty string `directory` is not-ok.
- `opencodeProfile` tests: routes and create body unchanged; the resolve command is empty; the list parser flattens strings and objects and returns not-ok on a malformed payload.
- `vm.ResolveTarget` tests: an empty resolve command skips the step; a failed or unparsable resolve aborts before the list; the base is validated before the create; the create carries the detached-HEAD note for opencode2 while a reuse does not; the create reuses the project from the list.
- Manual end-to-end (user-side; the dev VM cannot start microsandbox VMs): `agents-sandbox run --agent opencode2 --worktree feature-x` creates, attaches, and a second run reuses the same directory; `-w feature-x:main` checks out at `main` (detached); against a custom base without `jq`, `--worktree` works.
