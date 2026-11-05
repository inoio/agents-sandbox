# Per-Slug Config Scopes Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the per-slug user config dir (`~/.config/agents-sandbox/<repo-slug>/`) contribute env/secret files, `home:` sources, and agent config dirs, at precedence generic-user < per-slug-user < project, through one ordered scope authority.

**Architecture:** A single `configpaths.ConfigScope` list (`ConfigScopes(paths, slug)`) is the authority for "which file-based config dirs, in what order." The launcher config file, env/secret files, `home:` layers, and agent config dirs all derive from it. The effective config slug is resolved once at the CLI boundary and carried on `options.RunOptions`, mirroring `ResolvedAgent`.

**Tech Stack:** Go 1.27, Cobra/Viper, testify, golangci-lint.

**Spec:** `.operator-shared/specs/per-slug-config-scopes.md`

## Global Constraints

- Go 1.27. Run `make fmt` then `make lint` after edits; `make test` for tests (`-tags microsandbox_ffi_path`). Never use `go vet` or hand-run `go fmt`.
- Every task must leave the whole module compiling and `make test` green. Go has no per-package builds, so interface/method removals land together with all callers in one task.
- TDD: write the failing test, watch it fail, implement, watch it pass.
- Add `// gomutants:disable ... reason="..."` only for a branch unreachable by construction, never to silence reachable code (`specs/mutation-testing.md`, `guides/mutation-triage.md`).
- A production-code change needs a changelog fragment: `changelog.d/<PR>.added.md` (slug is the PR number; use `578` and rename before merge if the PR number differs). See `specs/changelog-fragments.md`.
- Keep `agents-sandbox config paths` output byte-identical: same layer names, and the project row stays `ProjectConfigDir()` (relative `.agents-sandbox` outside a bare layout).
- Every host path goes through the `ConfigPaths` seam; tests install the mocks from each package's `TestMain` (`specs/test-isolation.md`, `specs/config-paths-seam.md`).

## Review Focus

Input classes and failure modes the spec implies but no single task's prose calls out; each has a test in the owning task.

1. **Per-key precedence** — for the same env var, secret name, home target, and agent snippet/mirror file, per-slug must override generic user and project must override per-slug. (Tasks 4, 5, 6.)
2. **Secrets-only per-slug dir** — a `<slug>/env.secret.yaml` with no `config.yaml` must still select its slug in `effectiveConfigSlug`. (Task 7.)
3. **Empty slug** — `slug == ""` must omit the per-slug scope entirely, so flag-only runs and existing tests keep generic+project behavior. (Tasks 1, 4.)
4. **Fingerprint stability** — absent per-slug files must leave the env/secret fingerprints (and thus no recreate) unchanged. (Task 4.)
5. **Agent namespace across scopes** — a per-slug primary `opencode2/` suppresses a generic legacy `opencode/`, and the legacy fallback warning still names the legacy dir. (Task 6.)
6. **Bare-root legacy selection** — during the pre-migration read, a per-slug secret file under the repo slug must not be skipped because a legacy slug holds a config file. (Task 7.)

---

### Task 1: Scope authority in `configpaths` (additive)

**Files:**
- Create: `internal/configpaths/scopes.go`
- Test: `internal/configpaths/scopes_test.go`

**Interfaces:**
- Produces:
  - `type ScopeKind int`; consts `ScopeGenericUser`, `ScopePerSlugUser`, `ScopeProject`
  - `type ConfigScope struct { Kind ScopeKind; Dir string }`
  - `func ConfigScopes(paths ConfigPaths, slug string) []ConfigScope`
  - `func ScopeDirs(scopes []ConfigScope) []string`
  - `func (s ConfigScope) EnvFile() string`, `EnvSecretFile() string`, `EnvSecretYAMLFile() string`, `AgentConfigDir(name string) string`

- [ ] **Step 1: Write the failing test**

```go
func TestConfigScopesOrderAndDirs(t *testing.T) {
    WithMockConfigPaths(t)
    scopes := ConfigScopes(Get(), "widget-bbb")
    // want exactly [generic user, per-slug user, project], in that order,
    // with Dirs UserConfigDir()/UserSlugConfigDir("widget-bbb")/ProjectConfigDir().
}

func TestConfigScopesOmitsPerSlugForEmptySlug(t *testing.T) {
    WithMockConfigPaths(t)
    scopes := ConfigScopes(Get(), "")
    // want exactly [generic user, project]
}

func TestConfigScopeFileJoins(t *testing.T) {
    // s := ConfigScope{Dir: "/base"}; assert EnvFile()=="/base/env",
    // EnvSecretFile()=="/base/env.secret",
    // EnvSecretYAMLFile()=="/base/env.secret.yaml",
    // AgentConfigDir("opencode2")=="/base/opencode2".
}

func TestScopeDirsMapsOrder(t *testing.T) {
    // ScopeDirs(scopes) == {s.Dir for s in scopes}, same order.
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/configpaths/ -run 'TestConfigScope|TestScopeDirs' -v`
Expected: FAIL — `ConfigScopes` undefined.

- [ ] **Step 3: Implement `internal/configpaths/scopes.go`**

`ConfigScopes` returns `[]ConfigScope{{ScopeGenericUser, paths.UserConfigDir()}}`, then appends `{ScopePerSlugUser, paths.UserSlugConfigDir(slug)}` only when `slug != ""`, then `{ScopeProject, paths.ProjectConfigDir()}`. The joins use the existing file-name constants (`EnvFileName`, `EnvSecretFileName`, `envSecretYAMLFileName`) via `filepath.Join`. `ScopeDirs` maps `Dir` in order.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/configpaths/ -run 'TestConfigScope|TestScopeDirs' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/configpaths/scopes.go internal/configpaths/scopes_test.go
git commit -m "feat(configpaths): add ordered config-scope authority (#578)"
```

---

### Task 2: Carry the effective config slug on `RunOptions`

**Files:**
- Modify: `internal/viperconfig/config.go` (add `ConfigSlug` field)
- Modify: `internal/viperconfig/load.go` (`NewResolver` sets it)
- Modify: `internal/sandbox/options/options.go` (`RunOptions.ConfigSlug`)
- Modify: `internal/runoptions/runoptions.go` (`Build` copies it)
- Test: `internal/viperconfig/viperconfig_test.go`, `internal/runoptions/runoptions_test.go`

**Interfaces:**
- Consumes: `(r *viperconfig.Resolver).Config() *viperconfig.Config`.
- Produces: `viperconfig.Config.ConfigSlug string` (`mapstructure:"-"`); `options.RunOptions.ConfigSlug string`.

- [ ] **Step 1: Write the failing tests**

```go
func TestNewResolverRecordsConfigSlug(t *testing.T) {
    // NewResolver(cmd, Get(), "widget-bbb") -> r.Config().ConfigSlug == "widget-bbb"
}

func TestBuildCarriesConfigSlug(t *testing.T) {
    // Build(&viperconfig.Config{ConfigSlug: "widget-bbb"}, cmd, ui)
    // -> opts.ConfigSlug == "widget-bbb"
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/viperconfig/ ./internal/runoptions/ -run 'ConfigSlug' -v`
Expected: FAIL — unknown field `ConfigSlug`.

- [ ] **Step 3: Implement**

Add `ConfigSlug string \`mapstructure:"-"\`` to `viperconfig.Config`; set `cfg.ConfigSlug = slug` in `NewResolver` before returning the resolver. Add `ConfigSlug string` to `options.RunOptions` (comment: resolved once at the run boundary, like `ResolvedAgent`). In `runoptions.Build`, inside the `if cfg != nil` block, set `opts.ConfigSlug = cfg.ConfigSlug`.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/viperconfig/ ./internal/runoptions/ -run 'ConfigSlug' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/viperconfig internal/sandbox/options internal/runoptions
git commit -m "feat(config): carry effective config slug on RunOptions (#578)"
```

---

### Task 3: `viperconfig` derives config-file and home layers from scopes

**Files:**
- Modify: `internal/viperconfig/load.go` (`ConfigLayers`, `NewResolver` home layers)
- Test: `internal/viperconfig/config_layers_test.go`, `internal/viperconfig/configdiscovery_test.go`

**Interfaces:**
- Consumes: `configpaths.ConfigScopes`, `configpaths.ScopeDirs` (Task 1).
- Produces: `ConfigLayers` file rows built from the scopes; `cfg.Home` loaded from scope dirs.

- [ ] **Step 1: Write the failing test**

```go
func TestHomeKeyFromPerSlugConfig(t *testing.T) {
    // Write <UserSlugConfigDir("slug")>/config.yaml with home: {.gitconfig: ""}.
    // NewResolver(cmd, Get(), "slug") -> r.Config().Home includes that entry.
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/viperconfig/ -run TestHomeKeyFromPerSlugConfig -v`
Expected: FAIL — the per-slug `home:` entry is absent.

- [ ] **Step 3: Implement**

In `ConfigLayers`, replace the three hand-built file layers with a projection of `configpaths.ConfigScopes(paths, slug)`, mapping each scope's `Kind` to the existing layer name (`LayerNameGenericUserConfig` / `LayerNamePerSlugUserConfig` / `LayerNameProjectConfig`) and `Dir` to `Path`, present via `configpaths.FindConfigFile(dir)`. Keep the three non-file rows (`legacy env`, `env`, `flags`) appended unchanged. `NewResolver` keeps merging `ConfigLayers` (no change), and its `homeconfig.LoadLayers` call takes `configpaths.ScopeDirs(configpaths.ConfigScopes(paths, slug))` instead of the two literal dirs.

- [ ] **Step 4: Run the package tests and confirm the layer list is unchanged**

Run: `go test ./internal/viperconfig/ -v`
Expected: PASS; `TestConfigLayersOrderAndPaths` still asserts the same five paths and order.

- [ ] **Step 5: Commit**

```bash
git add internal/viperconfig
git commit -m "feat(viperconfig): derive config-file and home layers from scopes (#578)"
```

---

### Task 4: `envsecret` reads scopes; remove the six env/secret accessors

**Files:**
- Modify: `internal/sandbox/envsecret/env.go` (`LoadEnvAndSecrets` takes scopes)
- Modify: `internal/sandbox/prepare/prepare.go` (`loadEnvFingerprints` builds scopes from `opts.ConfigSlug`)
- Modify: `internal/configpaths/configpaths.go` (remove six accessors + interface entries)
- Modify: `internal/configpaths/testmock.go`, `internal/sandbox/image/render_test.go` (drop the stub methods)
- Test: `internal/sandbox/envsecret/env_secrets_test.go`, `internal/configpaths/*_test.go`, `internal/sandbox/prepare/*_test.go`

**Interfaces:**
- Consumes: `configpaths.ConfigScopes` (Task 1); `options.RunOptions.ConfigSlug` (Task 2).
- Produces: `func envsecret.LoadEnvAndSecrets(ui termio.Logger, scopes []configpaths.ConfigScope, allowPlaceholderFor []string) EnvSecrets`.

- [ ] **Step 1: Write the failing tests**

```go
func TestLoadEnvAndSecretsPerSlugPrecedence(t *testing.T) {
    // generic env SHARED=generic, A=generic; per-slug env SHARED=slug, B=slug;
    // project env SHARED=project, C=project.
    // -> Vars{SHARED: project, A: generic, B: slug, C: project}
}

func TestLoadEnvAndSecretsPerSlugSecretFormats(t *testing.T) {
    // per-slug env.secret TOKEN=a@a.example; per-slug env.secret.yaml TOKEN
    // as YAML -> YAML wins with the YAML hosts; project still overrides per-slug.
}

func TestDesiredFingerprintsUnchangedWithoutPerSlugFiles(t *testing.T) {
    // desiredFingerprints with slug set but no per-slug files equals the
    // fingerprints with slug "" (env/secret hashes identical).
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/sandbox/envsecret/ ./internal/sandbox/prepare/ -run 'PerSlug|FingerprintsUnchanged' -v`
Expected: FAIL — `LoadEnvAndSecrets` has the old signature / per-slug is ignored.

- [ ] **Step 3: Implement**

Change `LoadEnvAndSecrets` to iterate `scopes`: build env maps from every scope's `EnvFile()` merged in order, and build `MergeSecretSpecs` from legacy specs for every scope in order followed by YAML specs for every scope in order (preserving "YAML beats legacy for the same key, across scopes"). In `prepare.loadEnvFingerprints`:

```go
scopes := configpaths.ConfigScopes(configpaths.Get(), opts.ConfigSlug)
env := envsecret.LoadEnvAndSecrets(ui, scopes, opts.AllowPlaceholderFor)
```

Remove `UserEnvFile`, `UserEnvSecretFile`, `UserEnvSecretYAMLFile`, `ProjectEnvFile`, `ProjectEnvSecretFile`, `ProjectEnvSecretYAMLFile` from the `ConfigPaths` interface and `realConfigPaths`, and delete the matching methods from `mockConfigPaths`, `failFastConfigPaths`, and `image/render_test.go`'s `projectDockerfilePaths`, plus their fail-fast assertions.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/sandbox/envsecret/ ./internal/configpaths/ ./internal/sandbox/prepare/ ./internal/sandbox/image/ -v`
Expected: PASS.

- [ ] **Step 5: Lint for dead code and commit**

Run: `make lint && make deadcode`
Expected: no findings (the removed accessors had `envsecret` as their only production caller).

```bash
git add internal/configpaths internal/sandbox/envsecret internal/sandbox/prepare internal/sandbox/image
git commit -m "feat(envsecret): read env/secret files from every config scope (#578)"
```

---

### Task 5: `homeconfig` + `reprovision` load home layers from scopes

**Files:**
- Modify: `internal/homeconfig/homeconfig.go` (`BuildHomeFilesWithModes`, `BuildHooks` take `dirs []string`)
- Modify: `internal/sandbox/reprovision/config_files.go` (`loadHomeFiles`, `loadHooks`, `LoadConfigFiles`, `LoadConfigFilesForHost`, `Describe` take the slug and derive scopes)
- Modify: `internal/sandbox/prepare/prepare.go` (`planReconfigStage` passes `opts.ConfigSlug`)
- Modify: `cmd/agents-sandbox/commands_config.go` (pass the resolver's `ConfigSlug` to `Describe`)
- Test: `internal/homeconfig/*_test.go`, `internal/sandbox/reprovision/*_test.go`

**Interfaces:**
- Consumes: `configpaths.ConfigScopes`/`ScopeDirs` (Task 1); `viperconfig.Config.ConfigSlug` (Task 2).
- Produces:
  - `func homeconfig.BuildHomeFilesWithModes(dirs []string, homeBase string, reserved []string) (map[string]HomeFile, []string, bool, error)`
  - `func homeconfig.BuildHooks(dirs []string, homeBase string, reserved []string) ([]HookSpec, error)`
  - `func reprovision.LoadConfigFiles(a agent.Resolved, configSlug string, ui termio.Logger, provisionHostConfig bool) (*ConfigFiles, error)`
  - `func reprovision.LoadConfigFilesForHost(a agent.Resolved, configSlug, hostHome, vmHome string, ui termio.Logger, provisionHostConfig bool) (*ConfigFiles, error)`
  - `func reprovision.Describe(a agent.Agent, configSlug, hostHome, vmHome string, ui termio.Logger, provisionHostConfig bool) ([]byte, []string, []HostFile, []MirrorFile, ConfigSource, error)`

- [ ] **Step 1: Write the failing test**

```go
func TestHomeFilesFromPerSlugConfig(t *testing.T) {
    // Write <UserSlugConfigDir("slug")>/config.yaml with home: {.toolrc: "toolrc"}.
    // LoadConfigFilesForHost(resolved, "slug", hostHome, vmHome, ui, true)
    // -> HomeFiles contains the vmHome/.toolrc target.
}

func TestHomeFilesPerSlugOverridesGeneric(t *testing.T) {
    // Same target .toolrc in generic and per-slug configs -> per-slug content wins;
    // project still wins over per-slug.
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/sandbox/reprovision/ -run 'HomeFilesFromPerSlug|PerSlugOverrides' -v`
Expected: FAIL — signature mismatch / per-slug ignored.

- [ ] **Step 3: Implement**

`BuildHomeFilesWithModes`/`BuildHooks` take `dirs []string` and pass them straight to `LoadLayers`/the internal `LoadLayers` call. In `reprovision`, compute `scopes := configpaths.ConfigScopes(cp.Get(), configSlug)` once in `LoadConfigFilesForHost`; `loadHomeFiles`/`loadHooks` take `configpaths.ScopeDirs(scopes)` instead of `baseUserConfigDir(src)` + `ProjectConfigDir()`. Thread `configSlug` through the three public entry points and update `Describe`'s caller in `commands_config.go` to pass `resolverFromContext(c.Context()).Config().ConfigSlug` (empty when no resolver). `prepare.planReconfigStage` passes `opts.ConfigSlug`. Update every test call site, passing `""` to keep generic+project behavior (`""` omits the per-slug scope).

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/homeconfig/ ./internal/sandbox/reprovision/ ./internal/sandbox/prepare/ ./cmd/agents-sandbox/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/homeconfig internal/sandbox/reprovision internal/sandbox/prepare cmd/agents-sandbox
git commit -m "feat(reprovision): load home layers from every config scope (#578)"
```

---

### Task 6: Agent config dirs across scopes

**Files:**
- Modify: `internal/configmerge/merge.go` (`BuildMerged`, `ScanMirror` take `dirs []string`)
- Modify: `internal/sandbox/reprovision/config_files.go` (`ConfigSource`, `ResolveConfigSource`, `SourceDirs`, `describeConfigScopes`, `buildMergedConfig`, `buildMirror`, `noLauncherConfigDir`, fallback warning)
- Modify: `cmd/agents-sandbox/commands_config.go` (`printConfigAgent` lists every scope dir)
- Test: `internal/configmerge/*_test.go`, `internal/sandbox/reprovision/*_test.go`, `cmd/agents-sandbox/cli_config_test.go`

**Interfaces:**
- Consumes: `configpaths.ConfigScopes` (Task 1); `configSlug` threaded in Task 5.
- Produces:
  - `func configmerge.BuildMerged(configPath, pattern string, dirs []string) (MergedConfig, error)`
  - `func configmerge.ScanMirror(pattern string, family []string, dirs []string, destDir string) ([]MirrorEntry, error)`
  - `type reprovision.ConfigSource struct { UserDirs []string; ProjectDir string; Fallback string; PrimaryUserDirs []string; PrimaryProjectDir string }`
  - `func reprovision.ResolveConfigSource(a agent.Agent, scopes []configpaths.ConfigScope) (ConfigSource, error)`
  - `func reprovision.SourceDirs(src ConfigSource) ([]string, string)`

- [ ] **Step 1: Write the failing tests**

```go
func TestBuildMergedOrderAcrossDirs(t *testing.T) {
    // snippet in generic sets {"model":"g"}, per-slug sets {"model":"s"},
    // project sets {"model":"p"} -> merged model == "p".
}

func TestScanMirrorPerSlugOverridesGeneric(t *testing.T) {
    // same relative file under generic and per-slug -> per-slug content wins.
}

func TestResolveConfigSourceOneNamespaceAcrossScopes(t *testing.T) {
    // generic legacy opencode/ exists, per-slug primary opencode2/ exists
    // -> primary namespace chosen for all scopes; Fallback == "".
}

func TestResolveConfigSourceFallbackWarningNamesLegacy(t *testing.T) {
    // only generic legacy opencode/ exists -> Fallback == "opencode";
    // describeConfigScopes names the legacy dir.
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/configmerge/ ./internal/sandbox/reprovision/ -run 'OrderAcrossDirs|PerSlugOverrides|OneNamespaceAcrossScopes|FallbackWarningNamesLegacy' -v`
Expected: FAIL — signatures / per-slug ignored.

- [ ] **Step 3: Implement**

`BuildMerged`/`ScanMirror` take `dirs []string` and pass it to `scanSnippets`/their loop. `ResolveConfigSource(a, scopes)` builds, per namespace name, the ordered user dirs from every non-project scope (`scope.AgentConfigDir(name)`, absolute via `filepath.Abs`) and the project candidate from the project scope, then applies the existing primary-vs-alias choice as **one global decision over all scopes** (primary if it exists in any scope, else the first alias existing in any scope; the chosen namespace is used for all scopes). `ConfigSource` carries `UserDirs`, `ProjectDir`, `Fallback`, `PrimaryUserDirs`, `PrimaryProjectDir`. `buildMergedConfig`/`buildMirror` pass `src.UserDirs + src.ProjectDir` as the ordered `dirs`. `SourceDirs` returns `(src.UserDirs, src.ProjectDir)`; `describeConfigScopes` and `printConfigAgent` render every user scope dir plus the project dir. `noLauncherConfigDir` checks the resolved dirs across all user scopes. Update every test call site (pass `""` slug or `[generic, project]` scopes to preserve behavior).

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/configmerge/ ./internal/sandbox/reprovision/ ./cmd/agents-sandbox/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/configmerge internal/sandbox/reprovision cmd/agents-sandbox
git commit -m "feat(reprovision): merge agent config dirs from every config scope (#578)"
```

---

### Task 7: Slug selection treats any per-slug input as populated

**Files:**
- Modify: `cmd/agents-sandbox/commands.go` (`slugHasUserConfig` → any per-slug input)
- Test: `cmd/agents-sandbox/cli_system_coverage_test.go`, plus a new case in `cmd/agents-sandbox/cli_config_test.go`

**Interfaces:**
- Consumes: `configpaths.ConfigScopes`, `ConfigScope` joins (Task 1).
- Produces: `slugHasUserInputs(slug string) bool` (renamed); `effectiveConfigSlug` uses it.

- [ ] **Step 1: Write the failing test**

```go
func TestEffectiveConfigSlugSeesSecretsOnlyPerSlugDir(t *testing.T) {
    // repo slug dir has only env.secret.yaml; legacy slug dir has config.yaml.
    // effectiveConfigSlug -> repo slug (secret-only dir counts as populated).
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/agents-sandbox/ -run TestEffectiveConfigSlugSeesSecretsOnlyPerSlugDir -v`
Expected: FAIL — the repo slug is not selected.

- [ ] **Step 3: Implement**

Replace `slugHasUserConfig` with `slugHasUserInputs`, which reports true when the per-slug dir holds a launcher config file, any of `EnvFile()/EnvSecretFile()/EnvSecretYAMLFile()`, or any registered agent's config dir. `effectiveConfigSlug` keeps its repo-slug-then-legacy order using the new predicate.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./cmd/agents-sandbox/ -run 'TestEffectiveConfigSlug' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/agents-sandbox
git commit -m "fix(config): select the per-slug slug from any per-slug input (#578)"
```

---

### Task 8: Docs and changelog

**Files:**
- Modify: `docs/configuration/launcher.md` (per-slug scope now covers env/secret, `home:`, agent config dirs)
- Modify: `docs/configuration/secrets.md` (add the per-slug `env.secret`/`env.secret.yaml` locations and precedence)
- Modify: `docs/manage-config.md` (mention the per-slug secrets location for machine-specific keys)
- Create: `changelog.d/578.added.md`

- [ ] **Step 1: Update the docs**

State the full precedence (generic user < per-slug user < project) for `env`/`env.secret`/`env.secret.yaml`, `home:` sources, and agent config dirs; list the per-slug paths. Keep link style per `STYLEGUIDE.md`.

- [ ] **Step 2: Add the changelog fragment**

`changelog.d/578.added.md`, one bullet: per-slug user config now contributes env/secret files, `home:` sources, and agent config dirs.

- [ ] **Step 3: Run the docs gate**

Run: `ci/check-docs.sh && make test`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add docs changelog.d
git commit -m "docs: document per-slug config scope parity (#578)"
```

---

### Task 9: Whole-branch verification

- [ ] **Step 1: Format, lint, and test everything**

Run: `make check`
Expected: PASS (fmt, lint, test).

- [ ] **Step 2: Dead code and changelog gates**

Run: `make deadcode && ci/check-changelog.sh`
Expected: PASS.

- [ ] **Step 3: Compile the tagged suites**

Run: `make compile-all`
Expected: compiles every test binary including `integration`-tagged files.
