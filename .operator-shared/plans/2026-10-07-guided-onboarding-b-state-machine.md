# Guided Onboarding B: Onboarding State Machine — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `login-pending` a real verification loop and persist a `manual` outcome, so first-run onboarding remembers terminal decisions (and re-arms on native-config change) instead of silently no-opping or nagging on every start.

**Architecture:** Extend the per-agent manifest (`config-migration.yaml`) with the already-specified `state` values `manual` and `login-pending` and a `pending` login list. `Guide` derives the decision from the manifest keyed by the native-config source hash (derived state stays authoritative); `Apply` records `login-pending` when the plan also carries login providers; `Guide` routes a matching `login-pending` into an interactive verification question (yes → completed, no → re-enter the login hand-off, not-yet → quiet) and a matching `manual` into silence. Non-interactive runs print a one-line reminder and never prompt.

**Tech Stack:** Go 1.26, Cobra/Viper, `internal/termio` UI, `gopkg.in/yaml.v3`.

**Spec:** `.operator-shared/specs/guided-onboarding.md` — sections "Onboarding state machine", "`Guide` outcomes", "Secondary fixes (bundled)". Read alongside this plan.

## Global Constraints

- Go 1.26; idiomatic Go, self-documenting non-abbreviated identifiers; KISS → YAGNI → SOLID → DRY.
- No comments unless the code cannot be made self-documenting.
- TDD: write the failing test, watch it fail, implement, watch it pass, commit.
- `make check` (fmt, lint, test, docs-linkcheck) must pass before finalizing; use `golangci-lint` (`make fmt` / `make lint`); the current suppression linter name is `exhaustruct_v5`, not `exhaustruct`.
- Run in the isolated worktree `issue-99-guided-onboarding-b`, not `/workspace`.
- Keep `README.md`, `docs/`, and `CHANGELOG.md` in sync for behavior changes.
- Derived state is authoritative: never persist anything except user decisions (`manual`, `dismissed`), the login verification state (`login-pending`, `completed`), and the existing apply journal (`outputs`). Any native-config change (source-hash change) re-arms the whole flow.
- Non-interactive runs warn and continue; they never prompt and never hang.

## Review Focus

1. A static+OAuth migration (`Files` written **and** `HasLogin`) must still reach the verification loop on the next run, even though managed config now exists and would otherwise short-circuit `Guide`.
2. Login-only (`OAuth` only) native config: `login-pending` is recorded, the next interactive run asks the question, and "not yet" does not re-prompt in the same run or write a new manifest.
3. Changing the native config file (source-hash change) re-arms a `manual`/`dismissed`/`login-pending` decision as if it were new.
4. Non-interactive runs with a pending login print exactly one reminder line and return without prompting.
5. `login-pending` for a provider whose login host is later denied must not silently claim completion; the verification question (not the host check) decides.

---

### Task 1: Persist `pending` logins and record `login-pending` from `Apply`

**Files:**
- Modify: `internal/configmigration/store.go` (manifest struct, state constants)
- Modify: `internal/configmigration/migration.go` (Plan fields)
- Modify: `internal/configmigration/build.go` (`applyLoginProviders` populates pending)
- Modify: `internal/configmigration/apply.go` (final manifest state)
- Test: `internal/configmigration/persistence_test.go`

**Interfaces:**
- Consumes: `LoginProvider{Name string; Hosts []string}` (`auth.go`), `migrationManifest` (`store.go`), `Plan` (`migration.go`).
- Produces:
  - `migrationStateManual = "manual"`, `migrationStateLoginPending = "login-pending"` (consts in `store.go`).
  - `type pendingLogin struct { Provider string \`yaml:"provider"\`; Hosts []string \`yaml:"hosts,omitempty"\` }` (store.go).
  - `migrationManifest.Pending []pendingLogin \`yaml:"pending,omitempty"\`` (store.go).
  - `Plan.PendingLogins []LoginProvider` (migration.go), populated by `applyLoginProviders`.
  - `migrationWriteDecision(agentName, sourceHash, state string, pending []LoginProvider) error` (store.go); `migrationWriteStatus` becomes a thin wrapper for `completed`/`dismissed`.
  - `RecordLoginPending(plan *Plan) error` (store.go), writing `login-pending` + `pendingLoginsFromPlan(plan)`.
  - `pendingLoginsFromPlan(plan *Plan) []pendingLogin` (store.go), converting `Plan.PendingLogins`, sorted by provider name.
  - `loginProviderNames(logins []LoginProvider) string` (guide.go), comma-joined names.

- [ ] **Step 1: Write the failing test**

In `internal/configmigration/persistence_test.go`:

```go
func TestApplyRecordsLoginPendingForMixedConfig(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	hostHome := t.TempDir()
	a, _ := agent.Lookup("opencode")
	writeNativeAuth(t, hostHome, `{
  "openai": {"type": "api", "key": "sk-static"},
  "github-copilot": {"type": "oauth", "access": "a", "refresh": "r"}
}`)

	plan, err := Build(a, hostHome, testNetworkPolicy())
	if err != nil {
		t.Fatal(err)
	}
	ui := &termio.Mock{}
	if err := Apply(plan, ui); err != nil {
		t.Fatal(err)
	}

	manifest := migrationManifestFor(a)
	if manifest.State != migrationStateLoginPending {
		t.Fatalf("state = %q, want %q", manifest.State, migrationStateLoginPending)
	}
	if len(manifest.Pending) != 1 || manifest.Pending[0].Provider != "github-copilot" {
		t.Fatalf("pending = %+v, want github-copilot", manifest.Pending)
	}
}
```

Use the existing native-config helper used by `TestBuildReportsLoginForOAuthOnlyConfig` (adapt `writeNativeAuth` to that helper's actual name in this file).

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/configmigration/ -run TestApplyRecordsLoginPendingForMixedConfig -v`
Expected: FAIL — `migrationStateLoginPending` undefined / state is `completed`.

- [ ] **Step 3: Implement the manifest and Plan fields**

In `store.go` add the constants and structs as named in Interfaces; extend `migrationManifest` with `Pending`. Implement `migrationWriteDecision` (writes `Version`, `State`, `Agent`, `SourceHash`, `Outputs: nil`, `Pending`), reduce `migrationWriteStatus` to call it, and add `RecordLoginPending` + `pendingLoginsFromPlan`. In `migration.go` add `PendingLogins []LoginProvider` to `Plan`. In `build.go` `applyLoginProviders` append `LoginProvider{Name: provider.Name, Hosts: provider.Hosts}` to `result.PendingLogins` (dedupe by name).

- [ ] **Step 4: Make `Apply` record the pending state**

In `apply.go`, replace the final manifest write's fixed `State: migrationStateCompleted` with a computed `state` and `pending`, where the plan has login providers:

```go
	state := migrationStateCompleted
	var pending []pendingLogin
	if plan.HasLogin() {
		state = migrationStateLoginPending
		pending = pendingLoginsFromPlan(plan)
	}
```

Keep the intermediate `migrationStateApplying` journal write unchanged.

- [ ] **Step 5: Run tests and `make lint`**

Run: `go test ./internal/configmigration/ -run 'TestApplyRecordsLoginPending|TestGuide|TestBuild' -v && make lint`
Expected: PASS, 0 lint issues.

- [ ] **Step 6: Commit**

```bash
git add internal/configmigration
git commit -m "feat(configmigration): record login-pending with providers after apply"
```

---

### Task 2: Derive the pending decision and run the login verification loop

**Files:**
- Modify: `internal/configmigration/store.go` (state lookup)
- Modify: `internal/configmigration/migration.go` (Plan derived fields)
- Modify: `internal/configmigration/build.go` (populate derived fields)
- Modify: `internal/configmigration/guide.go` (verification loop, non-interactive reminder)
- Modify: `internal/configmigration/persistence_test.go`

**Interfaces:**
- Consumes: `migrationStateLoginPending`, `migrationStateManual`, `pendingLogin` (Task 1).
- Produces:
  - `migrationStateFor(a agent.Agent, sourceHash string) string` (store.go) — the persisted `state` when `manifest.SourceHash == sourceHash` and `Version > 0`, else `""`.
  - `Plan.Manual bool`, `Plan.LoginPending bool`, `Plan.PendingLogins []LoginProvider` set in `finalizeMigrationPlan` (`build.go`) from `migrationStateFor`; for `login-pending`, `PendingLogins` comes from `migrationManifestFor(a).Pending`.
  - `guideLoginVerification(a agent.Agent, plan *Plan, ui termio.UI) error` (guide.go).
  - `Complete(plan *Plan) error` (store.go) — writes `completed`.
  - `warnLoginPending(a agent.Agent, ui termio.UI) bool` (guide.go) — reads the manifest and prints a one-line reminder when `state == login-pending`; returns whether it warned.

- [ ] **Step 1: Write the failing test**

In `internal/configmigration/persistence_test.go`:

```go
func TestGuideLoginPendingVerificationYesCompletes(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	hostHome := t.TempDir()
	a, _ := agent.Lookup("opencode")
	writeNativeAuth(t, hostHome, `{"github-copilot": {"type": "oauth", "access": "a", "refresh": "r"}}`)

	ui := &termio.Mock{SelectKey: "yes"}
	if err := Guide(a, hostHome, testNetworkPolicy(), false, ui); err != nil {
		t.Fatalf("first Guide: %v", err)
	}
	if migrationManifestFor(a).State != migrationStateLoginPending {
		t.Fatalf("after handoff state = %q", migrationManifestFor(a).State)
	}

	ui = &termio.Mock{SelectKey: "yes"}
	if err := Guide(a, hostHome, testNetworkPolicy(), false, ui); err != nil {
		t.Fatalf("verify Guide: %v", err)
	}
	if got := migrationManifestFor(a).State; got != migrationStateCompleted {
		t.Fatalf("state after confirmation = %q, want completed", got)
	}
}
```

Match the `termio.Mock` select seam actually used by the existing Guide tests (e.g. `SelectKeys []string`); adapt the field name.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/configmigration/ -run TestGuideLoginPendingVerificationYesCompletes -v`
Expected: FAIL — second `Guide` returns early (managed/config quiet gate) and state stays `login-pending`.

- [ ] **Step 3: Implement derived state**

In `store.go` add `migrationStateFor`. In `build.go` `finalizeMigrationPlan` (and the no-native early return in `Build`, which must also set them) set:

```go
	result.PersistedState = migrationStateFor(a, result.SourceHash)
	result.Manual = result.PersistedState == migrationStateManual
	result.LoginPending = result.PersistedState == migrationStateLoginPending
	if result.LoginPending {
		result.PendingLogins = pendingLoginsFromManifest(a)
	}
```

Add `Plan.PersistedState string` and the corresponding helper `pendingLoginsFromManifest(a agent.Agent) []LoginProvider`.

- [ ] **Step 4: Route `Guide` to the verification loop**

In `guide.go`, after `Build` succeeds and before the `HasManagedConfig`/`Dismissed`/`Handled` quiet gate, insert:

```go
	if plan.LoginPending {
		return guideLoginVerification(a, plan, ui)
	}
```

and add the interactive question:

```go
func guideLoginVerification(a agent.Agent, plan *Plan, ui termio.UI) error {
	names := loginProviderNames(plan.PendingLogins)
	key, err := ui.Select(
		fmt.Sprintf("Did the in-sandbox login for %s succeed?", names),
		[]termio.Choice{
			{Key: "yes", Label: "Yes", Description: "mark onboarding complete"},
			{Key: "no", Label: "No", Description: "review the host configuration again"},
			{Key: "later", Label: "Not yet", Description: "remind me next time"},
		},
		"yes",
	)
	if err != nil {
		return err
	}
	switch key {
	case "yes":
		ui.Infof("%s login confirmed", a.Name())
		return Complete(plan)
	case "no":
		return guideLoginHandoff(a, plan, ui)
	case "later":
		ui.Infof("%s login is still pending; agents-sandbox will ask again next time", a.Name())
		return nil
	default:
		return fmt.Errorf("unknown login verification choice %q", key)
	}
}
```

In the non-interactive branch of `Guide`, call `warnLoginPending(a, ui)` before `warnUnmigratedNativeConfig` and return.

- [ ] **Step 5: Run tests and `make lint`**

Run: `go test ./internal/configmigration/ -run 'TestGuideLoginPending|TestGuide|TestBuild' -v && make lint`
Expected: PASS, 0 lint issues.

- [ ] **Step 6: Commit**

```bash
git add internal/configmigration
git commit -m "feat(configmigration): login-pending verification loop in Guide"
```

---

### Task 3: Persist the `manual` outcome and stay quiet

**Files:**
- Modify: `internal/configmigration/store.go` (`RecordManual`)
- Modify: `internal/configmigration/guide.go` (host-first/review choices; quiet gate)
- Modify: `internal/configmigration/persistence_test.go`

**Interfaces:**
- Consumes: `migrationStateManual`, `Plan.Manual` (Tasks 1–2).
- Produces: `RecordManual(plan *Plan) error` (store.go), writing `manual` keyed by `SourceHash`.

- [ ] **Step 1: Write the failing test**

```go
func TestGuideHostFirstManualIsRemembered(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	hostHome := t.TempDir()
	a, _ := agent.Lookup("opencode")

	ui := &termio.Mock{SelectKey: "manual"}
	if err := Guide(a, hostHome, testNetworkPolicy(), false, ui); err != nil {
		t.Fatalf("host-first Guide: %v", err)
	}
	if got := migrationManifestFor(a).State; got != migrationStateManual {
		t.Fatalf("state = %q, want manual", got)
	}

	ui = &termio.Mock{SelectKey: "manual"}
	if err := Guide(a, hostHome, testNetworkPolicy(), false, ui); err != nil {
		t.Fatalf("second Guide: %v", err)
	}
	if len(ui.SelectCalls) != 0 {
		t.Fatalf("second run prompted %d times, want quiet", len(ui.SelectCalls))
	}
}
```

Adapt `SelectCalls` to the mock's actual recording field.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/configmigration/ -run TestGuideHostFirstManualIsRemembered -v`
Expected: FAIL — state not written / second run re-prompts.

- [ ] **Step 3: Implement**

Add `RecordManual`. In `guide.go`:
- `guideHostFirst`: `case guideManualChoice, guideStartChoice:` → `return dismissOrManual(plan, false)` using a shared helper `persistManual(plan, ui)` that calls `RecordManual` and prints "continuing with manual configuration". (`guideStartChoice` also records manual per the spec's persisted terminal decision set.)
- `guideReviewHandoff`: `case guideManualChoice, guideStartChoice:` → same `persistManual`; keep `guideHostFirstChoice` → `ErrStartDeferred`.
- `guideLoginHandoff`: `case guideManualChoice:` → `persistManual`.
- Add `plan.Manual` to the quiet gate: `if plan.HasManagedConfig || plan.Dismissed || plan.Handled || plan.Manual { return nil }`.
- Keep the explicit "Continue without migration" (`guideDismissChoice`) on `dismissGuidedMigration` (state `dismissed`).

- [ ] **Step 4: Run tests and `make lint`**

Run: `go test ./internal/configmigration/ -v && make lint`
Expected: PASS, 0 lint issues.

- [ ] **Step 5: Commit**

```bash
git add internal/configmigration
git commit -m "feat(configmigration): remember manual onboarding outcome"
```

---

### Task 4: Explicit `config migrate` re-entry, docs, and CHANGELOG

**Files:**
- Modify: `cmd/agents-sandbox/commands_system.go` (`buildConfigMigrateCmd` note for `manual`/`login-pending`)
- Modify: `cmd/agents-sandbox/cli_config_test.go`
- Modify: `docs/manage-config.md` (or `docs/configuration/agent.md`)
- Modify: `CHANGELOG.md`

**Interfaces:**
- Consumes: `Plan.Manual`, `Plan.LoginPending`, `RecordManual`, `RecordLoginPending`.
- Produces: user-facing copy; no new exported symbols.

- [ ] **Step 1: Write the failing CLI test**

In `cmd/agents-sandbox/cli_config_test.go`, add a case asserting that `config migrate` proceeds (and applies) when the manifest is `manual` for the current native config, printing the explicit re-entry note. Reuse the existing `config migrate` fixture pattern in that file.

Run: `go test ./cmd/agents-sandbox/ -run TestConfigMigrate -v`
Expected: FAIL — note/behavior not present.

- [ ] **Step 2: Implement the CLI note**

In `buildConfigMigrateCmd`, broaden the existing `plan.Dismissed` note to also cover `plan.Manual || plan.LoginPending`, e.g. "Onboarding was previously deferred for the current native configuration; applying it explicitly now."

- [ ] **Step 3: Docs + CHANGELOG**

Document the verification loop and remembered manual outcome in `docs/manage-config.md` (the login host section): a `login-pending` start asks whether the in-sandbox login worked; "No" re-enters the host review; a manual setup is not re-prompted until the native config changes. Add one `### Changed` bullet to the `[Unreleased]` section of `CHANGELOG.md` describing the verification loop and remembered manual outcome.

- [ ] **Step 4: Run `make check`**

Run: `make check`
Expected: fmt, lint, tests, and docs-linkcheck all pass.

- [ ] **Step 5: Commit**

```bash
git add cmd/agents-sandbox docs CHANGELOG.md
git commit -m "feat(configmigration): explicit onboarding re-entry and docs"
```

---

## Self-Review

- **Spec coverage:** state values `manual`/`login-pending` + `pending` (Task 1); the derived table and verification loop (Task 2); explicit outcomes and dismissal re-arming (Tasks 2–3); `config migrate` voluntary re-entry + secondary-fix dismissal behavior (Task 4). The optional VM-home auto-confirm and the tiered wizard/`config harden` are Plan C/D or explicitly optional and out of scope.
- **Type consistency:** `LoginProvider` is reused for pending; `pendingLogin` is the YAML shape; `Plan.PendingLogins` is the host-side shape. `migrationStateFor` is the single source of the persisted-state lookup.
- **Review Focus:** each item maps to a Task (1 mixed static+OAuth; 2 login-only + non-interactive + source-hash re-arm; 3 manual quiet; 5 verification decides).
