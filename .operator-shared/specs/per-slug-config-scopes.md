---
description: Contract for the single ordered config-scope authority (#578) - `configpaths.ConfigScope`/`ConfigScopes` is the one list of file-based config dirs (generic user, per-slug user, project) that the launcher config file, env/secret files, `home:` sources, and agent config dirs all derive from, so adding a scope or a file-based input is a one-site change and no reader carries a stale order.
read_if: Changing config-scope order or precedence, per-slug user config, `configpaths.ConfigScopes`, `viperconfig.ConfigLayers`, `envsecret.LoadEnvAndSecrets`, `homeconfig` layer loading, `reprovision.ResolveConfigSource`, or a file-based config input (env/secret files, home sources, agent config dirs); or diagnosing a per-slug user file that is silently ignored.
---

# Config scopes

Date: 2026-11-05. Issue #578.

## Why

The per-slug user config dir (`~/.config/agents-sandbox/<repo-slug>/`) is presented as a full
configuration scope — it is a layer in `agents-sandbox config paths` and documented as per-project
configuration — but only its launcher config file is read. The file-based inputs the launcher
consumes (`env`, `env.secret`, `env.secret.yaml`, `home:` sources, agent config dirs) are read from
the generic user dir and the project dir only, so a user's per-project `env.secret.yaml` is silently
ignored. The user layer is trusted and never gated, so the natural home for a machine-specific,
per-project secret — the per-slug dir — was the one place secrets did not work.

The root cause is that "which config dirs exist, in what order" is not stated once. `ConfigLayers`
owns it for the launcher config file, while `homeconfig` and `reprovision` each assemble their own
copy. Adding a scope to one authority is exactly the drift this issue is.

## Goals

- All file-based inputs read the same ordered scopes, with the same precedence:
  generic user < per-slug user < project.
- One authority for the scope list and its order; no reader owns a copy.
- Absent per-slug files change nothing: no new fingerprint, recreate, or restart prompt for an
  existing project.

## Non-goals

- The project layer's trust gate (`project-config-trust.md`) is unchanged. The per-slug user dir is
  in the trusted user layer and is never gated.
- No user-level Dockerfile; the project Dockerfile stays project-only.
- No new config-file formats or new file-based inputs beyond the existing four.
- No change to the `config paths` displayed layer names or the relative project row.

## Anti-goals

- `ConfigScope` must not become a behavior hub. It exposes its dir and role and joins known file
  names under it; readers must not branch on its kind.
- The order must not be restated in any reader or in any per-input list.

## Approaches considered

**A. Per-slug accessors plus a slug threaded on `RunOptions`.** Add `UserSlug*File` accessors, read
them in each input reader, and carry the effective slug `viperconfig.Config` → `RunOptions`. Small
per-package surface, but keeps two authorities (`ConfigLayers` plus each reader's own list) and
re-encodes the cross-scope order and the project dir in each of `envsecret`, `homeconfig`, and
`reprovision`. The same class of drift that caused #578 stays possible; it also leaves the `home:`
layer set spelled twice (`NewResolver` for display, `reprovision` for provisioning).

**B. One ordered scope list (chosen).** A single `configpaths` value yields the ordered scopes; the
launcher config file, env/secret files, `home:` sources, and agent config dirs all derive from it.
Adding a scope or a file input becomes a one-site change, and a reader cannot carry a stale order
because it does not own one.

**C. Per-scope resolution mirroring the trust gate.** Every input resolved per scope with its own
provenance, as the project gate does. Rejected: the project gate needs per-scope provenance because
it must attribute untrusted content; the user scopes are trusted and need no provenance, so this is
machinery with no consumer.

B was chosen. Its one failure mode — `ConfigScope` growing methods readers branch on — is an
explicit anti-goal above.

## Where the authority lives

`internal/configpaths` — the lowest-level host-path seam, already imported by `homeconfig`,
`envsecret`, `reprovision`, and `viperconfig`. `homeconfig` cannot import `viperconfig` (the reverse
edge already exists), so the authority cannot live there; `configpaths` has no such constraint and
no import cycle.

## The value

```go
// ScopeKind is a configuration scope's role.
type ScopeKind int

const (
    ScopeGenericUser ScopeKind = iota
    ScopePerSlugUser
    ScopeProject
)

// ConfigScope is one ordered layer of file-based config inputs: the base dir and
// its role. It carries no behavior beyond joining the known file names under it.
type ConfigScope struct {
    Kind ScopeKind
    Dir  string
}
```

Free function over the seam (no new interface method, so the mock stays a mock of host paths):

```go
// ConfigScopes returns the file-based config scopes lowest precedence first:
// generic user, per-slug user (when slug is non-empty), project.
func ConfigScopes(paths ConfigPaths, slug string) []ConfigScope
```

The project scope's `Dir` is the value its readers already use today — `ProjectConfigDir()`, which
is absolute in a bare layout and relative to the CWD otherwise — so the env/secret/home readers and
the `config paths` display are unchanged. A consumer that needs an absolute path resolves it there
with `filepath.Abs`; the agent-candidate resolver, which already renders and stats absolute paths
(`ProjectConfigDirAbs`), is the one such boundary.

File-name joins live on the scope so the names have one home:

```go
func (s ConfigScope) EnvFile() string
func (s ConfigScope) EnvSecretFile() string
func (s ConfigScope) EnvSecretYAMLFile() string
func (s ConfigScope) AgentConfigDir(name string) string
```

## Per-input wiring

- **Launcher config file** — `viperconfig.ConfigLayers` projects `ConfigScopes(paths, slug)` to the
  three file layers and appends the non-file `legacy env` / `env` / `flags` rows. `NewResolver`
  merges the same scopes.
- **`home:` sources** — `NewResolver` and `reprovision` both load layers from
  `ConfigScopes(paths, slug)` dirs, ending the current double spelling.
- **env files** — `envsecret.LoadEnvAndSecrets` reads each scope's `EnvFile()` in order, merging so
  later scopes win: generic < per-slug < project.
- **secret files** — legacy specs for every scope in order, then YAML specs for every scope in
  order. This preserves today's rule that a YAML entry always beats a legacy entry for the same key
  even across scopes; within a tier, later scopes win.
- **agent config dirs** — `reprovision.ResolveConfigSource` builds user candidates from every user
  scope (`ScopeGenericUser`, `ScopePerSlugUser`) and project candidates from the project scope; the
  primary/legacy-namespace selection considers all of them. `ConfigSource` carries the ordered user
  dirs (`UserDirs []string`) plus `ProjectDir`, and `configmerge.BuildMerged` / `ScanMirror` take an
  ordered dir list so generic and per-slug snippets/mirrors both merge with the project dir last.

### Agent config namespace selection

The primary/legacy-namespace choice stays one global decision, extended to the three scopes: the
primary dir (`opencode2`) wins when it exists in **any** of generic user, per-slug user, or project;
otherwise the first alias (`opencode`) that exists in any scope is used; the chosen namespace
applies to all three scopes. The existing rule already switches both scopes together because a
partial fallback — some snippets from the primary namespace, some from an alias — is unsafe; a
per-slug scope is one more equal participant in that single decision, not an independent chooser.
The consequence, that a generic legacy dir is dropped when a per-slug primary dir exists, is the
intended reading (the user is migrating) and is surfaced by the existing fallback warning.

## Slug threading

The scope list needs the effective config slug. It is already resolved once at the CLI boundary as
`effectiveConfigSlug(identity)` in `cmd/agents-sandbox`. It is threaded the same way `ResolvedAgent`
is: resolved at the boundary and carried on `options.RunOptions` (`run-orchestration-layer.md`).

- `viperconfig.Config` gains `ConfigSlug string` (`mapstructure:"-"`), set by `NewResolver` from its
  existing slug parameter. `NewResolverWithConfig` leaves it empty, so tests keep generic-only
  behavior unless they set it.
- `runoptions.Build` copies it to `RunOptions.ConfigSlug`.
- `prepare` passes it to the env/secret load and to `reprovision.LoadConfigFiles`.
- The `config agent` path passes `effectiveConfigSlug` to `reprovision.Describe`.
- `effectiveConfigSlug`/`slugHasUserInputs` treat a per-slug dir as populated when it holds **any**
  per-slug user input — the launcher config file, an env/secret file, or an agent config dir — not
  only the config file. This keeps a secrets-only per-slug dir from being skipped during bare-root
  legacy-slug selection, which would silently reintroduce the #578 failure.

## Blast radius

- `configpaths` — new scope types/functions; remove all six env/secret file accessors
  (`UserEnvFile`, `UserEnvSecretFile`, `UserEnvSecretYAMLFile`, `ProjectEnvFile`,
  `ProjectEnvSecretFile`, `ProjectEnvSecretYAMLFile`) from the interface, since `envsecret` is their
  only production caller and scopes now carry every join; update `testmock.go`, the fail-fast mock,
  `image/render_test.go`'s `projectDockerfilePaths`, and the path tests.
- `envsecret`, `homeconfig`, `configmerge`, `reprovision` — signatures take ordered dirs; test call
  sites updated (~mechanical).
- `viperconfig` — `ConfigSlug`, `ConfigLayers` derived from scopes, home layers from scopes.
- `options`, `runoptions`, `prepare`, `cmd` — carry and consume the slug.

## Non-impact

- Env/secret fingerprints derive from loaded values, so absent per-slug files leave them unchanged.
- `home:` and agent config changes already apply on the next startup / daemon restart; no new
  recreate path.
- The project trust projection is unchanged: it reads the project scope alone.

## Settled builder calls

- `BuildHomeFilesWithModes`/`BuildHooks` take an ordered `dirs []string` (as `LoadLayers` already
  does); callers derive the dirs from the scope list. This avoids forcing a `configpaths` import into
  `homeconfig`'s already-importing callers and keeps the functions as pure dir consumers.
- `configmerge.BuildMerged`/`ScanMirror` take an ordered `dirs []string` (they already build
  `[]string{userDir, projectDir}` internally), so no new type crosses that boundary.
- `ConfigSource` carries `UserDirs []string` (ordered: generic, per-slug) plus `ProjectDir`, and
  keeps the `Fallback` marker's meaning (the legacy namespace name in use, or "").
- `config agent` and the fallback warning list every resolved scope dir (generic, per-slug, project)
  rather than the old fixed `user=`/`project=` pair; `SourceDirs` returns the ordered list plus the
  project dir.

## Out of scope

- `config harden` / `config wizard` do not exist in this tree (they are described by
  `guided-onboarding.md` but unlanded), so no scope inspection is added for them. When they land,
  they should consume `ConfigScopes` for their per-scope findings.

## Open questions

- Should `env`/`env.secret` under the per-slug dir be documented, and where (Secrets page,
  `launcher.md`, `manage-config.md`)? A changelog fragment is required.
