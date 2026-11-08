---
description: Contract for the launcher's config path resolution and scope order - `internal/configpaths` is the one authority for host paths (the `ConfigPaths` seam, home included), and `configpaths.ConfigScopes` is the one ordered list of file-based config dirs (generic user < per-slug user < project) that the launcher config file, env/secret files, `home:` sources, and agent config dirs all derive from.
read_if: Changing config-scope order or precedence, per-slug user config, host config/home path resolution, `configpaths.ConfigPaths`/`ConfigScopes`, `viperconfig.ConfigLayers`, `envsecret.LoadEnvAndSecrets`, `homeconfig` layer loading, `reprovision.ResolveConfigSource`; or diagnosing a per-slug user file that is silently ignored, or a test that must not touch the real home.
---

# Config paths and scopes

## Host-path seam

`internal/configpaths` is the single authority for the **host** paths agents-sandbox resolves. `configpaths.Get()` returns a `ConfigPaths`; production code obtains host paths through it, never by hand-building them. The resolver is also injected below each entry point (image render helpers, `vm.AdoptLegacyIdentity`, `viperconfig`, the prepare/upgrade migrations) so those call sites take a `ConfigPaths` parameter; `Get()` remains the boundary factory and the process-root residue (`state`, `runtime`/`MSBHome`, `homeconfig`) — see `specs/architecture/client-injection.md`.

- Methods cover the tool's XDG dirs (`UserConfigDir`/`UserSlugConfigDir`/`UserCacheDir`/`UserStateDir`/`UserDataDir`), the project config dir and env/secret/Dockerfile paths, `IgnoredWorktreeConfigDir`, and `UserHomeDir() (string, error)` — the host user's home, the root the home-relative agent config and `home:` manifests resolve against.
- The per-agent config-dir candidates are resolved by `agent.ConfigDirNames(a) ([]string, error)` (the primary name then each `ConfigDirAliaser` alias), joined under the tool's user/project config dir by `reprovision.joinUnder` inside `reprovision.ResolveConfigSource`. `agent.validateConfigDirName` is the one guard: it returns an error when a profile's config-dir name is not a single clean path element (`""`, `"."`, `".."`, or one containing a path separator), so a malformed profile fails at the resolution boundary (`reprovision.ResolveConfigSource` / `LoadConfigFilesForHost`) with a message naming the agent, rather than panicking deep inside a path join.
- `MSBHome()` is a free function (not a method) so the low-level `sandbox/msb` adapter can resolve the private microsandbox home without importing the higher-level runtime package.
- Config-file discovery (`FindConfigFile`, with `ExtJSONC`/`ExtJSON5`) is a free function here too: the launcher-config and home-manifest loaders both call it, so the extension order and the first-existing-file lookup have one definition and the two loaders cannot silently read different `config.<ext>` files from one directory.
- Guest/VM config paths (e.g. `agent.VMConfigPath(home)`, `reprovision.AgentConfigPath`) are sandbox paths, derived from the VM home, and deliberately do **not** use this seam.

Deliberate exceptions (host paths that are not the tool's config and keep their own resolution): `mounts.expandHome` (user-supplied `~/` bind-mount sources), `sandbox/docker`'s `~/.docker` (third-party CLI config with its own `DOCKER_CONFIG` override), and the `xdgBaseDir` home fallback inside `configpaths` itself.

### Test isolation

Under tests `configpaths.Get()` defaults to a fail-fast implementation that panics on first use; a test exercising home-relative resolution installs `WithMockConfigPaths(t)` (which also redirects `$HOME` into its temp tree) or `WithMockConfigPathsHomeError(t, err)`. `homeconfig` and `runtime` tests that resolve the home via an empty or `~` manifest source therefore opt in explicitly. See `../testing/test-isolation.md` for the full fail-fast and host-home-redirect contract, and `specs/vm/reconfig-decision.md` for the spurious "agent config changed" prompt that motivated it.

## Ordered config scopes

`configpaths.ConfigScopes(paths, slug)` is the one list of file-based config dirs, lowest precedence first. It is the single authority for "which config dirs exist, in what order"; no reader owns a copy. The per-slug user dir (`~/.config/agents-sandbox/<repo-slug>/`) is a full configuration scope — a layer in `config paths` — not a launcher-config-file-only dir.

- All file-based inputs read the same ordered scopes with the same precedence: **generic user < per-slug user < project**.
- One authority for the scope list and its order. Adding a scope or a file input is a one-site change, and a reader cannot carry a stale order because it does not own one.
- Absent per-slug files change nothing: no new fingerprint, recreate, or restart prompt for an existing project.

Non-goals:

- The project layer's trust gate (`specs/configuration/project-config-trust.md`) is unchanged. The per-slug user dir is in the trusted user layer and is never gated. An unapproved project layer is left out of the scope list itself: `ConfigScopes` omits the project scope when the paths carry no project config dir (`configpaths.WithoutProjectLayer`), so every reader loses it at once.
- No user-level Dockerfile; the project Dockerfile stays project-only.
- No new config-file formats or new file-based inputs beyond the existing four.
- No change to the `config paths` displayed layer names or the relative project row.

Anti-goals:

- `ConfigScope` must not become a behavior hub. It exposes its dir and role and joins known file names under it; readers must not branch on its kind.
- The order must not be restated in any reader or in any per-input list.

The authority lives in `internal/configpaths` — the lowest-level host-path seam, already imported by `homeconfig`, `envsecret`, `reprovision`, and `viperconfig`. `homeconfig` cannot import `viperconfig` (the reverse edge exists), so the authority cannot live there; `configpaths` has no such constraint and no import cycle. This was chosen over per-slug accessors read by each reader (which keeps two authorities and re-encodes the order per reader) and over per-scope provenance mirroring the trust gate (the user scopes are trusted and need no provenance).

### The value

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

The project scope's `Dir` is the value its readers already use — `ProjectConfigDir()`, absolute in a bare layout and relative to the CWD otherwise — so the env/secret/home readers and the `config paths` display are unchanged. A consumer that needs an absolute path resolves it there with `filepath.Abs`; the agent-candidate resolver, which already renders and stats absolute paths (`ProjectConfigDirAbs`), is the one such boundary.

File-name joins live on the scope so the names have one home:

```go
func (s ConfigScope) EnvFile() string
func (s ConfigScope) EnvSecretFile() string
func (s ConfigScope) EnvSecretYAMLFile() string
func (s ConfigScope) AgentConfigDir(name string) string
```

### Per-input wiring

- **Launcher config file** — `viperconfig.ConfigLayers` projects `ConfigScopes(paths, slug)` to the three file layers and appends the non-file `legacy env` / `env` / `flags` rows. `NewResolver` merges the same scopes.
- **`home:` sources** — `NewResolver` and `reprovision` both load layers from `ConfigScopes(paths, slug)` dirs, ending the former double spelling.
- **env files** — `envsecret.LoadEnvAndSecrets` reads each scope's `EnvFile()` in order, merging so later scopes win: generic < per-slug < project.
- **secret files** — legacy specs for every scope in order, then YAML specs for every scope in order. This preserves the rule that a YAML entry always beats a legacy entry for the same key even across scopes; within a tier, later scopes win.
- **agent config dirs** — `reprovision.ResolveConfigSource` builds user candidates from every user scope (`ScopeGenericUser`, `ScopePerSlugUser`) and project candidates from the project scope; the primary/legacy-namespace selection considers all of them. `ConfigSource` carries the ordered user dirs (`UserDirs []string`) plus `ProjectDir`, and `configmerge.BuildMerged` / `ScanMirror` take an ordered dir list so generic and per-slug snippets/mirrors both merge with the project dir last.

#### Agent config namespace selection

The primary/legacy-namespace choice is one global decision, extended to the three scopes: the primary dir (`opencode2`) wins when it exists in **any** of generic user, per-slug user, or project; otherwise the first alias (`opencode`) that exists in any scope is used; the chosen namespace applies to all three scopes. The rule switches all scopes together because a partial fallback — some snippets from the primary namespace, some from an alias — is unsafe; a per-slug scope is one more equal participant in that single decision, not an independent chooser. The consequence, that a generic legacy dir is dropped when a per-slug primary dir exists, is intended (the user is migrating) and is surfaced by the existing fallback warning.

### Slug threading

The scope list needs the effective config slug. It is resolved once at the CLI boundary as `effectiveConfigSlug(identity)` in `cmd/agents-sandbox` and threaded the same way `ResolvedAgent` is — resolved at the boundary and carried on `options.RunOptions` (see `specs/architecture/package-boundaries.md`).

- `viperconfig.Config` gains `ConfigSlug string` (`mapstructure:"-"`), set by `NewResolver` from its existing slug parameter. `NewResolverWithConfig` leaves it empty, so tests keep generic-only behavior unless they set it.
- `runoptions.Build` copies it to `RunOptions.ConfigSlug`.
- `prepare` passes it to the env/secret load and to `reprovision.LoadConfigFiles`.
- The `config agent` path passes `effectiveConfigSlug` to `reprovision.Describe`.
- `SlugHasUserInputs`/`EffectiveConfigSlug` live in `reprovision` (exported; the legacy slug list is passed in so the helper does not import `git`), shared by the CLI boundary, `prepare`, and `vm`'s adoption. They cannot live in `configpaths`: the predicate iterates the agent registry (`internal/agent`), and `internal/agent`'s tests import `configpaths`, so `configpaths` importing `agent` would create a test import cycle. They treat a per-slug dir as populated when it holds **any** per-slug user input — the launcher config file, an env/secret file, or an agent config dir — not only the config file. This keeps a secrets-only per-slug dir from being skipped during bare-root legacy-slug selection, which would silently reintroduce the underlying failure.

#### Recompute after adoption

Adoption renames a bare root's legacy per-slug dir onto the repo slug, so the slug the CLI boundary resolved before adoption can be stale. `prepare`'s resolve-identity stage recomputes it through `reprovision.EffectiveConfigSlug` and overwrites `opts.ConfigSlug` before the later readers (`reprovision.LoadConfigFiles`, the env/secret fingerprint load) use it; otherwise the migration run loads defaults and skips the user's env, secrets, `home:` files, hooks, and agent config dirs. The recomputation is filesystem-derived, so a read-only run (`--dry-run-vm`, `apply=false`) and a left-in-place target that holds no inputs keep naming the legacy dir.

#### Adoption warning

`vm`'s `migrateLegacyConfigDirs` leaves a legacy dir in place when the repo-slug target exists. It claims the legacy launcher config is "no longer read" only when the repo-slug dir actually takes over (`SlugHasUserInputs(repoSlug)` is true); an empty target leaves the legacy slug selected, so the report instead says the legacy config is still read. A legacy dir with no launcher config stays a verbose note.

### Blast radius

- `configpaths` — new scope types/functions; remove all six env/secret file accessors (`UserEnvFile`, `UserEnvSecretFile`, `UserEnvSecretYAMLFile`, `ProjectEnvFile`, `ProjectEnvSecretFile`, `ProjectEnvSecretYAMLFile`) from the interface, since `envsecret` is their only production caller and scopes now carry every join; update `testmock.go`, the fail-fast mock, `image/render_test.go`'s `projectDockerfilePaths`, and the path tests.
- `envsecret`, `homeconfig`, `configmerge`, `reprovision` — signatures take ordered dirs; test call sites updated.
- `viperconfig` — `ConfigSlug`, `ConfigLayers` derived from scopes, home layers from scopes.
- `options`, `runoptions`, `prepare`, `cmd` — carry and consume the slug.

Non-impact: env/secret fingerprints derive from loaded values, so absent per-slug files leave them unchanged; `home:` and agent config changes already apply on the next startup / daemon restart (no new recreate path); the project trust projection is unchanged (it reads the project scope alone).

`config harden` / `config wizard` do not exist in this tree (they are described by `specs/onboarding/onboarding.md` but unlanded), so no scope inspection is added for them. When they land, they should consume `ConfigScopes` for their per-scope findings.
