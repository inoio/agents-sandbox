# Design: fork the opencode2 launcher config namespace

Date: 2026-10-19
Status: implemented (pending merge).

## Intent

`opencode` (v1) and `opencode2` currently share one launcher config directory
(`~/.config/agents-sandbox/opencode/` and the project equivalent), because
`opencode2Profile` embeds `opencodeConfig`, whose `ConfigDirName()` returns
`"opencode"`. As a result the two agents' merged snippets and verbatim mirror
are byte-identical by construction: there is **no way to give v1 and v2
divergent launcher config**, which is the whole motivation. v2 has a native
config schema (e.g. structured `providers.*.models.*.capabilities.reasoning`)
that v1 does not understand, so a snippet written for one agent is unavoidably
fed to the other.

We want opencode2 to own its launcher namespace (`.../opencode2/`) so the two
can diverge, **without** changing anything the VM sees. The guest config path
stays `~/.config/opencode/opencode.jsonc` and the host drop-in stays
`~/.config/opencode/` (v2 reads the same on-disk locations as v1 — confirmed
against the v2 docs — so redirecting them is neither possible via config-dir
naming nor desirable).

## Goals

- Give opencode2 a distinct launcher config dir name, `opencode2`, covering both
  the user (`~/.config/agents-sandbox/opencode2/`) and project
  (`.agents-sandbox/opencode2/`) scopes.
- Keep the guest target (`VMConfigPath`) and the host drop-in
  (`ProvisionRules`) exactly `opencode`.
- Preserve existing users' opencode2 config, which lives in the shared
  `.../opencode/` dir today, via a fallback.
- Leave opencode (v1) behavior unchanged. The one deliberate exception: the
  shared resolver now `os.Stat`s candidate dirs for every agent, so a
  non-"not exist" stat error on a candidate surfaces instead of being swallowed
  (previously `scanSnippets`/`filewalk` ignored such errors). v1 declares no
  alias, so its dir selection and warning behavior are identical; only the
  error-propagation edge differs.

## Non-goals

- No change to guest/VM paths, the host drop-in rules, the `home:` manifest, or
  startup hooks. The `home:` manifest and hooks are read from the launcher
  **root** (`~/.config/agents-sandbox/`), one level above the agent subdir, and
  are already agent-independent; they stay shared.
- No per-file fallback. Resolution is directory-granular.
- No deprecation-register entry (see Deprecation stance). No
  `CHANGELOG`/register alias cycle.
- No auto-migration (rename/copy) of existing files.
- No guest-side redirect via `OPENCODE_CONFIG_DIR` (v2's handling of it is
  inconsistent — upstream issue #32825 — and the docs no longer mention it).
- No change to VM/home-volume/state scoping.

## Anti-goals

- Reading **both** the primary and legacy dirs and merging across them. At any
  time the agent reads exactly one source per scope.
- Auto-creating `.../opencode2/`. The directory's existence is the switch;
  creating it eagerly would defeat the signal.

## Decisions

### The fork

`opencode2Profile` overrides `ConfigDirName() string` to return `"opencode2"`.
Snippet merge and mirror then read `.../opencode2/`, while
`VMConfigPath`/`ProvisionRules`/`ConfigFileNames`/`SnippetPattern` continue to
come from the embedded `opencodeConfig` and remain `opencode`.

### Fallback: declarative aliases + consumer-side resolver

Chosen over "agent resolves concrete paths" (violates the config-paths seam and
puts host I/O in pure profiles) and over "configpaths stats the filesystem"
(smears an I/O decision into the pure path factory). Responsibilities:

- **agent** — declarative names only. New optional capability:
  ```go
  type ConfigDirAliaser interface { ConfigDirAliases() []string }
  func AsConfigDirAliaser(a Agent) (ConfigDirAliaser, bool)
  ```
  `opencode2Profile` returns `[]string{"opencode"}`; agents that never shared a
  dir (opencode, pi, claude-code) do not implement it.
- **configpaths** — name→path, pure, no I/O. New methods:
  ```go
  UserAgentConfigDirCandidates(a agent.Agent) []string    // [primary, alias1, ...]
  ProjectAgentConfigDirCandidates(a agent.Agent) []string
  ```
  Built as `filepath.Join(base, name)` over `ConfigDirName()` then
  `ConfigDirAliases()`. The project base is `ProjectConfigDirAbs()` (absolute),
  so the dirs resolution reads are the same dirs display reports — one source of
  truth, no second derivation of the project root.
  `UserAgentConfigDir`/`ProjectAgentConfigDir` stay (primary path) and are also
  absolute for the project scope. Each name is validated to be a single clean path element
  (rejecting `""`, `.`, `..`, and any name containing the path separator —
  including a bare `/`) and a bad name panics; the primary path constructors
  (`UserAgentConfigDir`/`ProjectAgentConfigDir`) validate too, so the guard is
  intrinsic rather than call-order-dependent. Without this:
  `filepath.Join` cleans `..`, so an unvalidated name like `..` would resolve to
  the launcher config root and the mirror would copy its files — including
  `env.secret` — into the VM, bypassing the msb-secret rule.
- **reprovision** — existence/precedence/warning. A resolver picks the source:
  ```go
  type ConfigSource struct {
      UserDir    string
      ProjectDir string
      Fallback   string // legacy dir name actually in use; "" for primary
  }
  func ResolveConfigSource(a agent.Agent) (ConfigSource, error)
  ```
  The resolver rejects an empty or length-mismatched candidate pair, and a
  `os.Stat` error other than "not exist" is propagated rather than silently
  treated as absent. Two intended edge behaviors: a stat error on the
  **primary** aborts resolution before aliases are considered (fail-closed — an
  unreadable primary never silently falls back to the legacy namespace), and
  when one scope is unreadable but the other resolves, resolution proceeds with
  the readable scope and emits no warning (the warning is keyed to `Fallback`).
  The `config agent` view labels the effective source without flagging the
  empty-primary case (the spec's silent-empty tradeoff stands).

### The signal: "an opencode2 dir anywhere"

Existence check is on directories (`os.Stat` + `IsDir`) across **both scopes**:

- The resolution picks **one source name globally**, then reads that name in
  both scopes — it never mixes names across scopes.
- If a **primary** dir exists in *either* the user or the current-project scope,
  both scopes resolve to the primary dirs. An absent scope simply contributes
  nothing; a legacy dir in the other scope is ignored.
- Only when **no** primary dir exists anywhere do both scopes fall back to the
  **first alias whose dir exists in either scope**; `Fallback` is that alias's
  name. With multiple aliases, order is the only thing that decides — at most
  one alias is ever active, which is why `Fallback` stays a single `string`.
  (Per-scope alias resolution was rejected: it would force `Fallback` to be
  per-scope and contradict the "a dir anywhere is the full signal" decision.)

Behavior matrix (user/project, opencode2 = primary, opencode = legacy):

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

Emitted at the single config-load boundary when `Fallback != ""` (which by
construction implies a legacy dir exists), always — not gated to once. It names
the agent, the full legacy user/project paths actually read, and the primary
user/project paths to migrate into, and says **copy** (do not move) because the
legacy dir is also the `opencode` agent's live dir; it states that an
`opencode2` dir in either scope switches both scopes, and that the guest
`~/.config/opencode` path is unchanged. Paths are absolute: the project dir is
resolved through the `configpaths.ProjectConfigDirAbs` seam method (the relative
`ProjectConfigDir` resolved against the cwd, degrading to the relative form only
if the cwd is unreadable), so `config agent` and the warning show explicit
locations rather than a cwd-relative path. `reprovision.SourceDirs` returns the
resolved source's dirs verbatim (display cannot diverge from what was read), and
`reprovision.PrimaryDirs` reads candidate[0] from the non-creating candidate
lists — naming the migration target must never manufacture the primary dir,
since its existence is the switch. It names only the scopes whose dir exists.
The load runs exactly once per process today
(`run_orchestrate.go`: "Load the merged agent config … exactly once per
startup"), and `config agent` invokes it once; a regression test asserts the
warning fires exactly once per load. If a future path loads more than once, the
warning must be deduped there.

### Deprecation stance

No register entry and no alias cycle: the legacy **path** is not a
flag/key/env/command, so `deprecation-policy.md` does not apply. The fallback is
open-ended (it warns for as long as it is used). The behavior change is recorded
in `CHANGELOG.md` (`Unreleased` → `Changed`) and in the agent docs.

### Reconfig / fingerprints

Guest keys (`ConfigFiles.Keys` = VM paths) are unchanged, so reconfig
fingerprints are unaffected. Content differences between v1 and v2 dirs
naturally produce different materialized config per agent VM, as designed.
Known pre-existing limitation (now reachable via the source switch):
`AgentConfigEqual` compares content but not mirror *removals*, so moving to a
source with strictly fewer mirror files leaves the stale VM copies in place
without a daemon restart. Out of scope here; noted for a follow-up.

### Candidate-method purity (test seam)

The new `configpaths` candidate methods are **pure name→path** and never create
directories, in both `realConfigPaths` and the mock. `UserAgentConfigDir` /
`ProjectAgentConfigDir` keep their current behavior (the mock eagerly creates the
primary dir), so existing tests are unaffected. Fallback fixtures are built with
explicitly created dirs (`os.MkdirAll`), since `testutil.WriteFile` does not
create parents.

### Where the effective dir lives

`ResolveConfigSource` lives in `reprovision` — the module that already owns
reading the agent config and its precedence. `UserAgentConfigDir` /
`ProjectAgentConfigDir` remain primary-only path constructors. No shared
"effective agent config dir" API is introduced until a real consumer needs one
(YAGNI): the config-migration/onboarding flow that would ask for it is unmerged,
and importing `reprovision` there would drag in the sandbox/msb stack.

### Accepted tradeoff

The signal is binary: once a primary dir exists in any scope, the legacy dir is
ignored entirely. Two consequences: an accidentally-created **empty**
`.../opencode2/` dir silently disables the fallback, and a **partial** migration
(copied some files into `.../opencode2/`, left the rest in `.../opencode/`)
silently drops the leftovers — the warning does **not** fire in either case,
because it is tied to `Fallback != ""`. This is the deliberate cost of "existence
is the signal". The warning is deliberately **not** widened to fire on partial
migration (a primary dir plus leftover legacy files): that case stays silent.

## Touch points

- `internal/agent/opencode_shared.go`, `opencode.go`, `opencode2.go` — override
  `ConfigDirName` on opencode2; add `ConfigDirAliases`; update the comments that
  currently assert the two share a config dir.
- `internal/agent/capabilities.go` — `ConfigDirAliaser` + `AsConfigDirAliaser`.
- `internal/configpaths/configpaths.go` + `testmock.go` + fail-fast — candidate
  methods; interface churn (mock + fail-fast) per `test-isolation.md`.
- `internal/sandbox/reprovision/config_files.go` — `ConfigSource` +
  `ResolveConfigSource`; feed resolved dirs into `buildMergedConfig`,
  `buildMirror`, and `mirrorFilesFromConfig`; carry `ConfigSource` on
  `ConfigFiles`; emit the warning.
- `internal/sandbox/reprovision/*_test.go` — resolver + behavior-matrix +
  exactly-once-warning tests.
- `docs/configuration/agent.md` — opencode2 section: own launcher dir, unchanged
  guest path/drop-in, fallback behavior.
- `README.md` — the built-in-agents list's opencode2 bullet: own launcher dir,
  unchanged guest path/drop-in.
- `CHANGELOG.md` — `Unreleased` → `Changed`.

Implementation notes: `configpaths.agentConfigDirCandidates` does no
de-duplication (aliases are distinct legacy names by contract), and
`anyDirExists` tries every candidate before surfacing an error. `config agent`
prints the effective source plus a legacy-fallback marker. `ConfigFiles.Source`
is set once per load and reused by `Describe`.

## Left to the builder

- Exact naming of the alias capability/methods and the resolver.
- Warning wording.
- Whether `ConfigSource` is stored on `ConfigFiles` and reused by `Describe`
  (preferred, avoids re-resolving for `mirrorFilesFromConfig`) or resolved
  twice.
- Test layout.
