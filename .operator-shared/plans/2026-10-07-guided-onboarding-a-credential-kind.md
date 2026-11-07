# Guided Onboarding A: Credential Kinds, Explicit Guide, Network-Deny Handling — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Stop placeholder-migrating refreshable OAuth, make every first-run `Guide` branch end in an explicit outcome, and detect/resolve `network.egress-deny` conflicts correctly.

**Architecture:** Two phases. **Phase 0** is behavior-preserving refactoring of the existing `internal/configmigration` code and agent specs: dedupe shared metadata, remove dead code/duplication, and decompose the oversized files. **Phase 1** is the behavior change on top: classify credential entries as `Static` (migrated) or `Login` (OAuth — dropped from generated files and routed to in-sandbox login), reshape `Guide` into a decision tree over `Plan` fields rather than a single `HasChanges()` gate, and detect/resolve network-deny conflicts against the *effective* `network.Policy`.

**Tech Stack:** Go 1.26, Cobra/Viper, `internal/termio` UI, `internal/sandbox/network`, `gopkg.in/yaml.v3`.

**Spec:** `.operator-shared/specs/guided-onboarding.md` (read alongside this plan).

## Global Constraints

- Go 1.26; idiomatic Go, self-documenting non-abbreviated identifiers; KISS → YAGNI → SOLID → DRY.
- No comments unless the code cannot be made self-documenting.
- TDD for behavior changes: write the failing test, watch it fail, implement, watch it pass, commit. Pure refactors add no new behavior — their test is that the existing suite stays green.
- `make check` (fmt, lint, test) must pass before finalizing; use `golangci-lint` (`make fmt` / `make lint`), never `go vet` or manual `go fmt`.
- Run this plan in an isolated worktree, not directly on `/workspace` (see AGENTS.md).
- Keep `README.md`, `docs/`, and `CHANGELOG.md` in sync for behavior changes.
- Do not add new `//nolint` complexity suppressions (`funlen`, `gocognit`, `gocyclo`, `cyclop`); decompose the function instead. Existing ones in touched code are removed by Tasks 3–4.
- Remove genuinely dead/test-only code when you touch it; do not expand abstractions for future needs the spec does not have (YAGNI).

## Phase Order and Why Refactors Come First

Phase 0 runs before Phase 1 ("make the change easy, then make the easy change"):

- The feature edits `migration.go`/`persistence.go` and the three agent specs heavily. Deduplicating and decomposing them first means the feature lands as small deltas in focused files, and the refactor commits (zero behavior change, tests green) can be reviewed in isolation instead of being buried in the feature diff.
- Decomposition moves **only pre-existing** symbols; the feature's new helpers (`guideReviewHandoff`, `resolveNetworkConflicts`, …) are written directly into the already-split files.
- Behavior-changing refactors — the `Build`/`Guide` policy signature and `updateLauncherNetwork` no-hard-fail — are **not** behavior-preserving, so they stay in Phase 1 with the feature.

## Review Focus

1. Native `auth.json` mixing static and OAuth providers → the static entries migrate, OAuth entries are dropped with a login hand-off; no raw OAuth token appears in any generated file.
2. Native config present but only unsupported/malformed entries → warnings and "review required" are surfaced interactively; the run does not silently proceed unconfigured.
3. `network.egress-deny` containing `*`, a `.suffix`, or a CIDR that shadows a derived host → detected and surfaced for interactive resolution, never silently defeated and never aborting the whole migration.
4. No native config for opencode/pi → an explicit host-first hand-off, not silence.
5. Any of the above on a non-interactive run → warn and continue; never block or hang.

---

## Phase 0 — Behavior-Preserving Refactors

### Task 1: Deduplicate shared credential-field metadata across agents

**Files:**
- Modify: `internal/agent/capabilities.go`, `internal/agent/opencode_shared.go`, `internal/agent/pi.go`, `internal/agent/claudecode.go`
- Test: `internal/agent/capabilities_test.go`

**Interfaces:**
- Consumes: the auth-type constants already in `capabilities.go`.
- Produces: package-level `defaultSensitiveAuthFields []string` and `defaultConfigSensitiveFields []string` reused by all three agent specs (replacing three near-identical hand-copied slices).

- [ ] **Step 1: Write the failing test** asserting each agent's `MigrationSpec().Auth.SensitiveFields` and `ConfigSensitiveFields` equal the shared slices and contain no raw string literals that duplicate a named constant.

```go
func TestAgentSpecsShareCredentialFieldMetadata(t *testing.T) {
	for _, name := range []string{"opencode", "pi", "claude-code"} {
		a, _ := agent.Lookup(name)
		auth := a.(agent.MigrationSpecProvider).MigrationSpec().Auth
		if !slices.Equal(auth.SensitiveFields, defaultSensitiveAuthFields) {
			t.Errorf("%s SensitiveFields diverges from the shared set", name)
		}
		if !slices.Equal(auth.ConfigSensitiveFields, defaultConfigSensitiveFields) {
			t.Errorf("%s ConfigSensitiveFields diverges from the shared set", name)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/agent -run TestAgentSpecsShareCredentialFieldMetadata`
Expected: FAIL (`defaultSensitiveAuthFields` undefined).

- [ ] **Step 3: Implement** the shared slices and replace the three copies; convert Claude's raw literals (`"access"`, `"refresh"`, …) to the named constants.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/agent`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/agent
git commit -m "refactor(agent): share credential-field metadata across agent specs"
```

---

### Task 2: Simplify `internal/configmigration` internals

**Files:**
- Modify: `internal/configmigration/migration.go`, `internal/configmigration/persistence.go`
- Modify: `cmd/agents-sandbox/constants.go`
- Test: existing package tests (must stay green)

**Interfaces:**
- Consumes: existing helpers.
- Produces: no new public API; removes dead code and duplication. `Plan` and existing exported functions keep their signatures.

- [ ] **Step 1: Inventory the target removals** with `rg` before editing, so only genuinely test-only symbols are touched: `rg -n 'sanitizeAuthMap|sanitizeConfigMap\b|providerEndpointHosts\b|isEndpointKey\b|isPlaceholder\b|isSensitiveConfigKey\b|slicesContains' internal/configmigration cmd`. Note that `authFields` is production-reachable through `authFieldsFor` and must **not** be removed.
- [ ] **Step 2: Remove the dead `isSafeField` branch** in `sanitizeAuthMapWithSpec` (`migration.go:255-258`): both branches `continue`, so replace the nested branch with a single `continue`; `SafeFields` remains used by `unsupportedAuthFieldWarnings`.
- [ ] **Step 3: Replace `slicesContains` with `slices.Contains`** and delete the wrapper (`persistence.go:620`).
- [ ] **Step 4: Unify the manifest/status path helpers** (`migrationManifestPath` and `migrationStatusPath` are identical) into one, and stop calling `migrationManifestFor`/`hashNativeFiles` twice in `Build` (`persistence.go:300-306`) by computing them once.
- [ ] **Step 5: Extract the shared home-mapping construction** used by `buildLauncherConfig` (`migration.go:1027`) and `migrationAlreadyConfigured` (`persistence.go:270`) into one helper.
- [ ] **Step 6: Replace `interface{ Name() string }` parameters** (`migrationManifestFor`, `migrationStatusPath`, `migrationStatusFor`) with `agent.Agent`.
- [ ] **Step 7: Name the magic literals**: add a named constant for `"opencode-oauth-dummy-key"` (`persistence.go:344` / `migration.go`), and collapse the duplicate `cmdConfigMigrate`/`cmdMigrate` `"migrate"` constants in `constants.go`.
- [ ] **Step 8: Remove or relocate test-only helpers** identified in Step 1: delete unused production wrappers; where tests still need them, move them into the `_test.go` file. Verify with `rg` that no production caller remains.
- [ ] **Step 9: Run the full package and CLI tests**

Run: `go test ./internal/configmigration ./cmd/agents-sandbox`
Expected: PASS.

- [ ] **Step 10: Commit**

```bash
git add internal/configmigration cmd/agents-sandbox/constants.go
git commit -m "refactor(configmigration): remove dead code and duplication"
```

---

### Task 3: Decompose `persistence.go` and drop its complexity suppressions

**Files:**
- Modify: `internal/configmigration/persistence.go`
- Create: `internal/configmigration/guide.go`, `internal/configmigration/build.go`, `internal/configmigration/apply.go`, `internal/configmigration/review.go`, `internal/configmigration/network_plan.go`, `internal/configmigration/store.go`
- Test: existing package tests (must stay green)

**Interfaces:**
- Consumes: everything already in `persistence.go`.
- Produces: the same exported API (`Build`, `Review`, `Apply`, `Report`, `Guide`, `Dismiss`, `EnableNativeProvisioning`) split across focused files. Move **existing symbols only** — the feature's new helpers are added later by directly writing into these files. No `//nolint:funlen|gocognit|gocyclo|cyclop` remains in the package.

- [ ] **Step 1:** Move into `guide.go`: `Guide`, `guideSetup`, `reviewApplyAndConfirmStart`, `dismissGuidedMigration`, `nativeConfigExists`, `nativeAgentPaths`, `migrationEnvPath`, `collectNativeFiles`, `nativeFile`, `hasManagedConfig`, `hashNativeFiles`.
- [ ] **Step 2:** Move into `build.go`: `Build`, `applyRequiredNetworkPlan`, `applyAuthMigration`, `hasGeneratedCredential`, `generatedSupplementalNames`, `nativeFileData`.
- [ ] **Step 3:** Move into `apply.go`: `Apply`, `writeIfChanged`.
- [ ] **Step 4:** Move into `review.go`: `Review`, `Report`, `reportMigrationFiles`, `reviewClaudeAuthentication`, `removeClaudeAuthentication`, `appendClaudeSettingsEnv`, `refreshNetworkPlan`, `warnIfLauncherConfigLosesComments`, `validatePlanOutputs`, `manifestOutputs`, `contentHash`, `completeSecretHostsAtPath`, `sortedSecretNames`, `Dismiss`.
- [ ] **Step 5:** Move into `network_plan.go`: `updateLauncherNetwork`, `plannedNetworkHosts`, `projectNetworkDenyHosts`, `migrationFindLauncherConfig`, `stringList`, `appendUniqueHosts`, `normalizeClaudeEndpoint`, `buildLauncherConfigForPlan`, `migrationHomeMapping`.
- [ ] **Step 6:** Move into `store.go`: `migrationStatus`, `migrationManifest`, `migrationOutput`, `migrationManifestPath`, `migrationManifestFor`, `writeMigrationManifest`, `migrationWriteStatus`, `migrationStatusFor`, `migrationAlreadyConfigured`, `migrationAtomicWrite`, `commitTempFile`, `migrationMergeSecretFile`, `migrationLoadLauncherConfig`, `migrationIsJSONConfig`, `sameStrings`, `sameSecret`, `EnableNativeProvisioning`.
- [ ] **Step 7: Decompose each remaining `//nolint` function** (`Build`, `Review`, and `migrationAlreadyConfigured`) by extracting named helpers until the suppression is unnecessary; remove the directives. (Do not touch `cmd/agents-sandbox/commands_cli.go` here; that is a separate concern.)
- [ ] **Step 8: Verify no suppressions remain and tests pass**

Run: `rg -n 'nolint:(funlen|gocognit|gocyclo|cyclop)' internal/configmigration && go test ./internal/configmigration`
Expected: no matches; PASS.

- [ ] **Step 9: Commit**

```bash
git add internal/configmigration
git commit -m "refactor(configmigration): split persistence into focused files and drop complexity suppressions"
```

---

### Task 4: Decompose `migration.go` and drop its complexity suppressions

**Files:**
- Modify: `internal/configmigration/migration.go`
- Create: `internal/configmigration/auth.go`, `internal/configmigration/native_config.go`, `internal/configmigration/spec.go`
- Test: existing package tests (must stay green)

**Interfaces:**
- Consumes: everything already in `migration.go`.
- Produces: same exported API (`Secret`, `File`, `Plan`, `Build`-support helpers). Move **existing symbols only**. No complexity suppressions remain.

- [ ] **Step 1:** Move auth helpers into `auth.go`: `sanitizeAuth*`, `replaceAuthField`, `parseAuthEntry`, `authHosts`, `jsonFieldString`, `unsupportedAuthFieldWarnings`, `isSensitiveField`, `isSafeField`, `isPlaceholderWithSpec`, `isPlaceholderWithAuthSpec`, `secretPart`, `knownHost`, `authFields`, `authFieldsFor`.
- [ ] **Step 2:** Move native-config migration into `native_config.go`: `migrateNativeConfig`, `migrateNativeSupplementalConfig`, `parseNativeConfig*`, `mergeConfigMaps`, `sanitizeConfigMap*`, `configSecretHosts`, `findEndpointHost`, `providerEndpointHosts*`, `isEndpointField`, `isEndpointKey`, `rejectEmbeddedEndpointCredential`, `migrateClaudeSecretField`, `claudeSecretVariable`, `isClaudeEndpointVariable`, `isClaudeUnsafeSettingKey`, `isSensitiveConfigKey`, `isPlaceholder`, `isCredentialContainerPath`, `isUnresolvedPiConfigValue`, `mergePlannedSecrets`.
- [ ] **Step 3:** Move spec/bootstrap helpers into `spec.go`: `defaultMigrationSpec`, `migrationSpecProviderFn`, `lookupMigrationSpec`, `buildLauncherConfig`, `cloneSecrets`, `shortHash`, and the package const/var declarations that belong with them.
- [ ] **Step 4: Decompose `sanitizeAuthMapWithSpec` and `findEndpointHost`** until their `//nolint` directives are unnecessary; remove the directives.
- [ ] **Step 5: Verify no suppressions remain and tests pass**

Run: `rg -n 'nolint:(funlen|gocognit|gocyclo|cyclop)' internal/configmigration && go test ./internal/configmigration`
Expected: no matches; PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/configmigration
git commit -m "refactor(configmigration): split migration into focused files and drop complexity suppressions"
```

---

## Phase 1 — Feature

### Task 5: Declare credential kinds on the agent migration spec

**Files:**
- Modify: `internal/agent/capabilities.go` (`AuthMigrationSpec`, `ConfigMigrationSpec`)
- Modify: `internal/agent/opencode_shared.go` (remove `oauth` from `AuthFields`; add login hosts)
- Modify: `internal/agent/pi.go` (remove `oauth` from `AuthFields`; add login hosts)
- Modify: `internal/agent/claudecode.go` (declare `LoginAuthTypes`; document it is login-only)
- Test: `internal/agent/capabilities_test.go`

**Interfaces:**
- Consumes: Task 1's shared field metadata.
- Produces:
  - `agent.AuthMigrationSpec.LoginAuthTypes []string` — auth `type` values that must not be migrated (empty means `["oauth"]`).
  - `agent.ConfigMigrationSpec.LoginHosts map[string][]string` — provider name → hosts needed for the provider's interactive login (empty means fall back to `KnownProviderHosts`).

- [ ] **Step 1: Write the failing test** asserting opencode/pi declare `oauth` as a login type and do not list it in `AuthFields`, and that claude-code's spec declares no static oauth fields.

```go
func TestMigrationSpecDeclaresOAuthAsLoginType(t *testing.T) {
	for _, name := range []string{"opencode", "opencode2", "pi"} {
		a, ok := agent.Lookup(name)
		if !ok { t.Fatalf("agent %q not registered", name) }
		spec := a.(agent.MigrationSpecProvider).MigrationSpec()
		if _, ok := spec.Auth.AuthFields["oauth"]; ok {
			t.Errorf("%s still lists oauth in AuthFields", name)
		}
		types := spec.Auth.LoginAuthTypes
		if len(types) != 1 || types[0] != "oauth" {
			t.Errorf("%s LoginAuthTypes = %v; want [oauth]", name, types)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/agent -run TestMigrationSpecDeclaresOAuthAsLoginType`
Expected: FAIL (field `LoginAuthTypes` undefined / `oauth` still in `AuthFields`).

- [ ] **Step 3: Implement the fields and per-agent declarations**

Add the two fields to the structs in `capabilities.go`. In `opencode_shared.go` and `pi.go`, delete the `"oauth"` key from `Auth.AuthFields` and set `LoginAuthTypes: []string{"oauth"}`. Add `LoginHosts` where login touches extra hosts (e.g. `github-copilot: {"github.com", "api.githubcopilot.com"}` for opencode/pi); leave empty when `KnownProviderHosts` already covers it. In `claudecode.go`, set `LoginAuthTypes: []string{"oauth"}` and keep `RequiredNetworkHosts` as its login hosts.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/agent`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/agent
git commit -m "feat(agent): declare login credential kinds on migration specs"
```

---

### Task 6: Detect login auth entries and drop them from generated auth

**Files:**
- Modify: `internal/configmigration/auth.go` (`LoginProvider`; classify in `sanitizeAuthWithSpec`)
- Modify: `internal/configmigration/build.go` (`applyAuthMigration`, seam update)
- Test: `internal/configmigration/migration_test.go`

**Interfaces:**
- Consumes: `agent.AuthMigrationSpec.LoginAuthTypes`, `agent.ConfigMigrationSpec.LoginHosts` (Task 5).
- Produces:
  - `type LoginProvider struct { Name string; Hosts []string }`
  - `func loginAuthTypes(spec agent.AuthMigrationSpec) []string` — returns `spec.LoginAuthTypes` or `["oauth"]`.
  - `func sanitizeAuthWithSpec(data []byte, spec agent.ConfigMigrationSpec, knownHosts, providerHosts map[string]string) (data []byte, secrets map[string]Secret, warnings []string, login []LoginProvider, err error)` — new `login` return; OAuth-type providers are omitted from `data` and reported in `login`.
  - `func loginProviderHosts(provider string, auth map[string]json.RawMessage, spec agent.ConfigMigrationSpec, knownHosts, providerHosts map[string]string) []string` — `spec.LoginHosts[provider]` if present, else `authHosts(provider, auth, knownHosts, providerHosts)`.

- [ ] **Step 1: Write the failing test** with a mixed auth file.

```go
func TestSanitizeAuthDropsOAuthAndReportsLogin(t *testing.T) {
	data := []byte(`{
	  "github-copilot": {"type":"oauth","access":"a","refresh":"r"},
	  "openai": {"type":"api","key":"sk-x"}
	}`)
	spec, _ := agent.Lookup("opencode")
	out, secrets, _, login, err := sanitizeAuthWithSpec(data, agentMigrationSpec(t, spec), map[string]string{"openai":"api.openai.com"}, nil)
	if err != nil { t.Fatal(err) }
	if len(login) != 1 || login[0].Name != "github-copilot" { t.Fatalf("login = %+v", login) }
	if !slices.Contains(login[0].Hosts, "github.com") { t.Errorf("login hosts = %v; want github.com", login[0].Hosts) }
	if strings.Contains(string(out), `"access"`) || strings.Contains(string(out), "a\"") {
		t.Errorf("generated auth still contains oauth material: %s", out)
	}
	if v := secrets["OPENCODE_OPENAI_KEY"].Value; v != "sk-x" { t.Errorf("static key not migrated: %+v", secrets) }
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/configmigration -run TestSanitizeAuthDropsOAuthAndReportsLogin`
Expected: FAIL (signature mismatch / oauth still migrated).

- [ ] **Step 3: Implement classification**

In `sanitizeAuthWithSpec`, before `parseAuthEntry`, read the entry's `type`; if it is in `loginAuthTypes(spec)`, append a `LoginProvider{Name: provider, Hosts: loginProviderHosts(...)}` and `continue` without adding to `result`. In `applyAuthMigration` (`build.go`), record `result.LoginProviders`/`result.LoginHosts` (fields added in Task 7) and always append warnings to `result.Warnings` **before** the `len(generated) == 0` early return. Update the `sanitizeAuthWithSpecFn` seam and all callers.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/configmigration`
Expected: PASS (existing tests updated to the new return arity).

- [ ] **Step 5: Commit**

```bash
git add internal/configmigration
git commit -m "feat(configmigration): classify oauth as login, drop it from generated auth"
```

---

### Task 7: Expose login state on the plan and populate it in Build

**Files:**
- Modify: `internal/configmigration/migration.go` (`Plan` fields + helpers)
- Modify: `internal/configmigration/build.go` (`Build`)
- Test: `internal/configmigration/persistence_test.go`

**Interfaces:**
- Consumes: `LoginProvider` (Task 6).
- Produces:
  - `Plan.LoginProviders []string`; `Plan.LoginHosts []string`.
  - `func (p *Plan) HasLogin() bool`
  - `func (p *Plan) NeedsAttention() bool` — true when `HasChanges()` is true or login is present or any warning/review warning exists.
  - `Build` adds login hosts to `NetworkHosts` (so they are egress-allowed) and sets `LoginProviders`/`LoginHosts`.

- [ ] **Step 1: Write the failing test**: an OAuth-only opencode auth file yields a plan with `HasChanges()==false`, `HasLogin()==true`, `NeedsAttention()==true`, and the login host present in `NetworkHosts`.
- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/configmigration -run TestBuildReportsLoginForOAuthOnlyConfig`
Expected: FAIL (fields undefined).

- [ ] **Step 3: Implement** the fields/helpers and populate them in `Build` after `applyAuthMigration`, appending login hosts via `appendUniqueHosts`.
- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/configmigration`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/configmigration
git commit -m "feat(configmigration): expose login providers and hosts on the plan"
```

---

### Task 8: Add destination matching for egress deny rules

**Files:**
- Create: `internal/sandbox/network/match.go`
- Test: `internal/sandbox/network/match_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `func MatchesDestination(rule, host string) bool` — true when `rule` is `"*"`, equals `host`, is a `.suffix` matching `host` (host equals or ends with `.suffix`), or is a CIDR containing an IP literal `host`.

- [ ] **Step 1: Write the failing table test.**

```go
func TestMatchesDestination(t *testing.T) {
	cases := []struct{ rule, host string; want bool }{
		{"*", "api.example.com", true},
		{"api.example.com", "api.example.com", true},
		{".example.com", "api.example.com", true},
		{".example.com", "example.com", true},
		{".example.com", "notexample.com", false},
		{"10.0.0.0/8", "10.1.2.3", true},
		{"10.0.0.0/8", "11.1.2.3", false},
		{"10.0.0.0/8", "api.example.com", false},
	}
	for _, c := range cases {
		if got := MatchesDestination(c.rule, c.host); got != c.want {
			t.Errorf("MatchesDestination(%q, %q) = %v; want %v", c.rule, c.host, got, c.want)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/sandbox/network -run TestMatchesDestination`
Expected: FAIL (undefined).

- [ ] **Step 3: Implement** `MatchesDestination` using `net.ParseCIDR` for CIDR rules and suffix/equality checks otherwise. Non-IP hosts fail CIDR rules.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/sandbox/network`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/network
git commit -m "feat(network): destination matcher for egress rules"
```

---

### Task 9: Thread the effective network policy into Build and Guide

**Files:**
- Modify: `internal/configmigration/migration.go` (`Plan` stores the policy)
- Modify: `internal/configmigration/build.go` (`Build` signature), `internal/configmigration/guide.go` (`Guide` signature)
- Modify: `cmd/agents-sandbox/commands_cli.go` (`guideConfigMigration` passes `opts.Network`)
- Modify: `cmd/agents-sandbox/commands_system.go` (`config migrate` builds a resolver for the policy)
- Test: `internal/configmigration/persistence_test.go`, `cmd/agents-sandbox/cli_config_test.go`

**Interfaces:**
- Consumes: `network.Policy` (`internal/sandbox/network`), `Resolver.Network()` (`internal/viperconfig`).
- Produces:
  - `func Build(a agent.Agent, hostHome string, policy network.Policy) (*Plan, error)`
  - `func Guide(a agent.Agent, hostHome string, policy network.Policy, provisionHostConfig bool, ui termio.UI) error`
  - unexported `Plan.policy network.Policy` used by `Build`/`Review` for deny detection.
  - `cmd/agents-sandbox` `buildMigrationPlan` seam updated to the new `Build` arity.

- [ ] **Step 1: Write the failing test** (details land in Task 10) asserting `Build` accepts and stores the policy and `cmd` compiles with the new arity.
- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/configmigration ./cmd/agents-sandbox`
Expected: FAIL (arity mismatch).

- [ ] **Step 3: Implement** the signature changes; in `commands_cli.go` pass `opts.Network`; in `commands_system.go` resolve `r, err := launcherconfig.NewResolver(c, git.ProjectSlug())` and pass `r.Network()`.
- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./...`
Expected: PASS (all callers updated).

- [ ] **Step 5: Commit**

```bash
git add internal/configmigration cmd/agents-sandbox
git commit -m "refactor(configmigration): pass effective network policy into build and guide"
```

---

### Task 10: Detect deny conflicts instead of hard-failing

**Files:**
- Modify: `internal/configmigration/migration.go` (`Plan.NetworkConflicts`)
- Modify: `internal/configmigration/network_plan.go` (`updateLauncherNetwork`), `internal/configmigration/build.go` (`Build`)
- Test: `internal/configmigration/persistence_test.go`

**Interfaces:**
- Consumes: `network.MatchesDestination` (Task 8), `Plan.policy` (Task 9).
- Produces:
  - `Plan.NetworkConflicts []string`
  - `func updateLauncherNetwork(path string, data []byte, hosts []string, deny []string) (string, []byte, added []string, skipped []string, err error)` — no longer returns an error for a denied host; it records the host in `skipped`.
  - `Build` sets `Plan.NetworkConflicts` from the skipped hosts (deny = `plan.policy.EgressDeny`).

- [ ] **Step 1: Write the failing test**: a policy whose `EgressDeny` is `["*"]` or `[".example.com"]` causes the conflicting derived host to be reported in `NetworkConflicts` and omitted from the added allow list, with no error.
- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/configmigration -run TestBuildReportsNetworkDenyConflicts`
Expected: FAIL.

- [ ] **Step 3: Implement** the signature change and conflict recording; replace the exact-string check with `network.MatchesDestination(rule, host)`.
- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/configmigration`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/configmigration
git commit -m "fix(configmigration): detect egress-deny conflicts without aborting"
```

---

### Task 11: Make `Guide` always end in an explicit outcome

**Files:**
- Modify: `internal/configmigration/guide.go` (`Guide`, new helpers)
- Test: `internal/configmigration/persistence_test.go`, `cmd/agents-sandbox/cli_config_test.go`

**Interfaces:**
- Consumes: `Plan.NeedsAttention()`, `Plan.HasLogin()`, `Plan.LoginProviders`, `Plan.LoginHosts` (Task 7).
- Produces:
  - `func guideReviewHandoff(plan *Plan, ui termio.UI) error`
  - `func guideLoginHandoff(a agent.Agent, plan *Plan, ui termio.UI) error`
  - `func guideHostFirst(a agent.Agent, ui termio.UI) error`
  - `Guide` branches: `provisionHostConfig` → nil; `!NeedsAttention()` → nil; `SetupOnly` → `guideSetup`; `HasLogin()` and no migratable files → `guideLoginHandoff`; unsupported/malformed → `guideReviewHandoff`; no native config → `guideHostFirst`; otherwise the existing migrate/use-native/continue select.

- [ ] **Step 1: Write the failing tests** for the three hand-offs, asserting no silent return and that warnings are printed.

```go
func TestGuideSurfacesWarningsWhenNothingToMigrate(t *testing.T) {
	ui := newGuideUI(t) // interactive
	err := Guide(mustAgent(t, "opencode"), hostHome, network.Policy{}, false, ui)
	if err != nil { t.Fatal(err) }
	if !ui.warnedContains("review required") { t.Error("expected review-required warning to be shown") }
}

func TestGuideOffersHostFirstWhenNoNativeConfig(t *testing.T) {
	ui := newGuideUI(t)
	_ = Guide(mustAgent(t, "pi"), t.TempDir(), network.Policy{}, false, ui)
	if !ui.selected("host-first") && !ui.selected("manual") {
		t.Errorf("expected an explicit hand-off choice; got %v", ui.choices())
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/configmigration -run 'TestGuideSurfacesWarnings|TestGuideOffersHostFirst'`
Expected: FAIL.

- [ ] **Step 3: Implement** the helpers and branch `Guide` on `NeedsAttention()`/`HasLogin()`/native-config presence instead of the `HasChanges()` short-circuit.
- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/configmigration ./cmd/agents-sandbox`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/configmigration cmd/agents-sandbox/cli_config_test.go
git commit -m "feat(configmigration): explicit Guide outcomes for login, review, and host-first"
```

---

### Task 12: Resolve deny conflicts interactively in Review

**Files:**
- Modify: `internal/configmigration/review.go` (`Review`, `refreshNetworkPlan`), `internal/configmigration/network_plan.go`
- Test: `internal/configmigration/persistence_test.go`

**Interfaces:**
- Consumes: `Plan.NetworkConflicts` (Task 10), `Plan.policy`.
- Produces: `func resolveNetworkConflicts(plan *Plan, ui termio.UI) error` — for each conflicted host, offers `allow-anyway` (remove the matching deny entry, keeping the other denies), `skip` (drop the host from required/network hosts), or `abort`. Non-interactive returns an error naming the conflict. `Review` calls it before printing the egress summary.

- [ ] **Step 1: Write the failing tests**: interactive `skip` removes the host and proceeds; interactive `allow-anyway` deletes the specific deny entry from `ConfigData`; non-interactive errors with the conflicting host named.
- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/configmigration -run TestReviewResolveNetworkConflicts`
Expected: FAIL.

- [ ] **Step 3: Implement** `resolveNetworkConflicts` and call it from `Review`; update `refreshNetworkPlan` to drop skipped hosts.
- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/configmigration`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/configmigration
git commit -m "feat(configmigration): interactive egress-deny conflict resolution"
```

---

### Task 13: Update docs and CHANGELOG

**Files:**
- Modify: `docs/configuration/agent.md`, `docs/configuration/secrets.md`, `docs/manage-config.md`
- Modify: `CHANGELOG.md` (`[Unreleased]`)

- [ ] **Step 1:** In `secrets.md`/`agent.md`, state the credential-kind rule: static API keys/tokens migrate via placeholders; refreshable OAuth is **not** copied and instead logs in inside the persistent sandbox home; Claude Code is the login-only case because its credential is keychain/external. Remove text implying OAuth is migrated.
- [ ] **Step 2:** In `manage-config.md`, document the explicit first-run outcomes (migrate / login hand-off / host-first hand-off / review hand-off) and that missing config never silently no-ops.
- [ ] **Step 3:** Add a `CHANGELOG.md` `[Unreleased]` line describing the OAuth handling change and the explicit onboarding outcomes.
- [ ] **Step 4: Verify docs build and links**

Run: the repo's docs check used in CI if available (`ci/check-docs.sh`) and `make check`.
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add docs CHANGELOG.md
git commit -m "docs: describe credential kinds and explicit onboarding outcomes"
```

---

## Roadmap: Plans B–D

These are deliberately out of scope for Plan A. Each maps to spec sections in
`.operator-shared/specs/guided-onboarding.md`; start each from the spec section, then expand into a
plan of the same TDD shape as above.

| Plan | Spec section(s) | Goal | Task outline |
|---|---|---|---|
| **B — Onboarding state machine** | "Onboarding state machine", "`Guide` outcomes" | Derived state + persisted `manual`/`login-pending`; cross-run login verification. | Extend `migrationManifest` with `pending` (providers/hosts) and the `manual`/`login-pending` states; add a derived-state function consumed by `Guide`; add the next-start "did login work?" verification loop; non-interactive reminders; tests per transition. |
| **C — Tiered setup wizard** | "Tiered setup wizard" | Declarative `SetupQuestion` catalog; first-run tiers; `config wizard --tier=N`; early exit. | `SetupQuestion` type + catalog; tier runner with per-question defaults; reuse the comment-preserving launcher-config writer; `config wizard` command and `--tier` flag; "enough questions" early exit; answered/dismissed state; skip-when-already-set; CLI tests. |
| **D — `config harden`** | "`config harden`" | Re-runnable security audit + step-by-step remediation; hosts the "reduce egress later" path. | Finding/remediation catalog; audit over effective config; interactive apply with remembered declines; `config harden` command; network-reduction finding reusing host derivation; tests. |

## Self-Review

**Spec coverage:** credential kinds → T5–T7; always-explicit Guide → T11; host determination (network side) + effective policy → T8–T12; secondary deny fix → T10, T12; docs/CHANGELOG → T13. Necessary refactorings from the PR review → T1 (shared credential metadata), T2 (dead code/duplication/middle-man/magic constants), T3–T4 (file decomposition, complexity-suppression removal). Onboarding state machine, tiered wizard, and `config harden` are Plan B/C/D, mapped in the Roadmap section and the spec.

**Step scan:** each step has one checkable action; code steps give signatures and exact values, not bodies.

**Type consistency:** `LoginProvider`, `Plan.LoginProviders/LoginHosts/NetworkConflicts`, `HasLogin`, `NeedsAttention`, `MatchesDestination`, and the new `Build`/`Guide`/`updateLauncherNetwork` signatures are defined once (Task numbers above) and used consistently in later tasks.

**Review Focus mapping:** (1) T6/T7; (2) T11; (3) T8/T10/T12; (4) T11; (5) T11 (non-interactive test) and T12 (non-interactive conflict).

## Execution Handoff

Choose an execution method after review:
- **Subagent-driven** (recommended): Phase 0 refactors are mechanical but wide, and Phase 1 touches shared types (`Plan`, agent specs) whose interfaces must stay consistent; a fresh reviewer per task catches breakage early.
- **Native**: faster, single-session, one final review.
