# Design: fork the opencode2 launcher config namespace

## Intent

`opencode` (v1) and `opencode2` share one launcher config directory
(`~/.config/agents-sandbox/opencode/` and the project equivalent), because
`opencode2Profile` embeds `opencodeConfig`, whose `ConfigDirName()` returns
`"opencode"`. The two agents' merged snippets and verbatim mirror would then be
byte-identical by construction, so there is **no way to give v1 and v2 divergent
launcher config**. That is the whole motivation: v2 has a native config schema
(e.g. structured `providers.*.models.*.capabilities.reasoning`) that v1 does not
understand, so a snippet written for one agent is unavoidably fed to the other.

`opencode2` therefore owns its launcher namespace (`.../opencode2/`) so the two
can diverge, **without** changing anything the VM sees. The guest config path
stays `~/.config/opencode/opencode.jsonc` and the host drop-in stays
`~/.config/opencode/` (v2 reads the same on-disk locations as v1 — confirmed
against the v2 docs — so redirecting them is neither possible via config-dir
naming nor desirable).

## Decisions

### The fork

`opencode2Profile` overrides `ConfigDirName() string` to return `"opencode2"`.
Snippet merge and mirror then read `.../opencode2/`, while
`VMConfigPath`/`ProvisionRules`/`ConfigFileNames`/`SnippetPattern` continue to
come from the embedded `opencodeConfig` and remain `opencode`.

Leave opencode (v1) behavior unchanged. The one deliberate exception: the shared
resolver `os.Stat`s candidate dirs for every agent, so a non-"not exist" stat
error on a candidate surfaces instead of being swallowed (previously
`scanSnippets`/`filewalk` ignored such errors). v1 declares no alias, so its dir
selection and warning behavior are identical; only the error-propagation edge
differs. The `home:` manifest and hooks are read from the launcher **root**
(`~/.config/agents-sandbox/`), one level above the agent subdir, and stay shared.

### Fallback: declarative aliases + consumer-side resolver

`opencode2Profile` implements the optional `ConfigDirAliaser` capability
(`agent.ConfigDirAliaser`, `agent.AsConfigDirAliaser` in
`internal/agent/capabilities.go`), returning `[]string{"opencode"}`; agents that
never shared a dir (opencode, pi, claude-code) do not implement it, so the agent
owns declarative names only. This is chosen over "agent resolves concrete paths"
(violates the config-paths seam and puts host I/O in pure profiles) and over
"configpaths stats the filesystem" (smears an I/O decision into the pure path
factory).

Name→path resolution is a pure function, `agent.ConfigDirNames(a) ([]string,
error)` (`internal/agent/config_dir.go`): the primary `ConfigDirName()` first,
then each alias. `reprovision.joinUnder` joins those names under
`configpaths.Get().UserConfigDir()` and `configpaths.Get().ProjectConfigDirAbs()`
(absolute, so the dirs resolution reads are the dirs display reports — one source
of truth for the project root). Each name is validated as a single clean path
element (rejecting `""`, `.`, `..`, and any name containing the path separator —
including a bare `/`) by `validateConfigDirName`, which **returns an error**;
`ConfigDirNames` and `ResolveConfigSource` propagate it. Without this:
`filepath.Join` cleans `..`, so an unvalidated name like `..` would resolve to
the launcher config root and the mirror would copy its files — including
`env.secret` — into the VM, bypassing the msb-secret rule. Resolution is pure
name→path and never creates a directory.

`reprovision.ResolveConfigSource(a agent.Agent) (ConfigSource, error)` owns
existence/precedence/warning. `ConfigSource` carries the resolved
`UserDir`/`ProjectDir`, the `Fallback` legacy dir name actually in use (`""` for
primary), and the primary `PrimaryUserDir`/`PrimaryProjectDir` the agent migrates
to (carried from resolution so a caller need not re-derive, and re-validate,
them). The resolver rejects an empty or length-mismatched candidate pair, and an
`os.Stat` error other than "not exist" is propagated rather than silently treated
as absent. Two intended edge behaviors: a stat error on the **primary** aborts
resolution before aliases are considered (fail-closed — an unreadable primary
never silently falls back to the legacy namespace), and when one scope is
unreadable but the other resolves, resolution proceeds with the readable scope
and emits no warning (the warning is keyed to `Fallback`). The `config agent`
view labels the effective source without flagging the empty-primary case (the
silent-empty tradeoff stands).

### The signal: "an opencode2 dir anywhere"

At any time the agent reads exactly one source per scope — never both, and never
merged; resolution is directory-granular, with no per-file fallback.
`.../opencode2/` is never auto-created: the directory's existence is the switch.

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

Emitted at the single config-load boundary (`reprovision.LoadConfigFilesForHost`)
when `Fallback != ""` (which by construction implies a legacy dir exists), always
— not gated to once. It names the agent, the full legacy user/project paths
actually read (`reprovision.SourceDirs`, which returns the resolved source's dirs
verbatim so display cannot diverge from what was read), and the primary
user/project paths to migrate into (`ConfigSource.PrimaryUserDir`/
`PrimaryProjectDir`). It says **copy** (do not move) because the legacy dir is
also the `opencode` agent's live dir; it states that an `opencode2` dir in either
scope switches both scopes, that a partial copy will not fall back for the rest,
and that the guest `~/.config/opencode` path is unchanged. It names only the
legacy scopes whose dir exists. Paths are absolute: the project dir is resolved
through the `configpaths.ProjectConfigDirAbs` seam method. The load runs exactly
once per process today, and `config agent` invokes it once; a regression test
asserts the warning fires exactly once per load. If a future path loads more than
once, the warning must be deduped there.

### Deprecation stance

No register entry and no alias cycle: the legacy **path** is not a
flag/key/env/command, so `deprecation-policy.md` does not apply. The fallback is
open-ended (it warns for as long as it is used).

### Reconfig / fingerprints

Guest keys (`ConfigFiles.Keys` = VM paths) are unchanged, so reconfig
fingerprints are unaffected. Content differences between v1 and v2 dirs naturally
produce different materialized config per agent VM, as designed. Known
pre-existing limitation (now reachable via the source switch):
`AgentConfigEqual` compares content but not mirror *removals*, so moving to a
source with strictly fewer mirror files leaves the stale VM copies in place
without a daemon restart. Out of scope here; noted for a follow-up.

### Where the effective dir lives

`ResolveConfigSource` lives in `reprovision` — the module that already owns
reading the agent config and its precedence. No shared "effective agent config
dir" API is introduced until a real consumer needs one (YAGNI): a consumer that
needs the primary dirs takes them from `ConfigSource`.

### Accepted tradeoff

The signal is binary: once a primary dir exists in any scope, the legacy dir is
ignored entirely. Two consequences: an accidentally-created **empty**
`.../opencode2/` dir silently disables the fallback, and a **partial** migration
(copied some files into `.../opencode2/`, left the rest in `.../opencode/`)
silently drops the leftovers — the warning does **not** fire in either case,
because it is tied to `Fallback != ""`. This is the deliberate cost of "existence
is the signal". The warning is deliberately **not** widened to fire on partial
migration (a primary dir plus leftover legacy files): that case stays silent.
