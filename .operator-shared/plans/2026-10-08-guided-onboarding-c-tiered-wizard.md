# Guided Onboarding C: Tiered Setup Wizard — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a declarative, tiered setup wizard that asks a shallow set of launcher-config questions on first run, can be run explicitly as `config wizard [--tier=N]`, writes user-level `config.yaml` preserving comments and key order, and never re-asks an answered question or overwrites a value set at a more specific scope.

**Architecture:** Three packages. A new `internal/configfile` owns the comment/order-preserving launcher-config editor, extracted verbatim from `internal/configmigration/launcher_config.go` so the wizard and (later) `config harden` can reuse it. A new `internal/configwizard` owns the question catalog, the question runner (essential-first ordering, group collapse, early exit), scope-skip logic, and answered-state persistence. First-run integration reuses the existing migration flow: the wizard runs in `runFunc` *before* `guideConfigMigration`, then run options are re-resolved so credential migration sees the chosen agent and network posture.

**Tech Stack:** Go 1.26, Cobra/Viper, `internal/termio` UI, `internal/configpaths`, `internal/viperconfig`, `internal/sandbox/options`, `internal/agent`, `gopkg.in/yaml.v3`.

**Spec:** `.operator-shared/specs/guided-onboarding.md` — sections "Tiered setup wizard" and "Onboarding state machine". Read alongside this plan.

## Design Decisions (approved)

- **Tiers:** T1 essentials (`agent` when unset, `network.profile`); T2 resources (`cpus` / `memory` / `disk-size` / `workspace-quota`, collapsed behind one confirm); T3 runtime (`dind`, `tmp-size`, `log-level`); T4 integrations (`notify.*`, `upgrade.*`, `auto-prune-age`). First run asks T1–T3; deeper tiers are opt-in via `config wizard --tier=N`; `config wizard` with no flag asks all unanswered tiers.
- **First-run ordering:** the wizard is a separate step in `runFunc`, before `guideConfigMigration`, followed by a run-options refresh.
- **Placement:** shared writer extracted to `internal/configfile`; wizard in `internal/configwizard`; `config harden` (Plan D) will follow the same shape.
- **Recommended resource defaults** (the group-confirm payload): `cpus: 4`, `memory: 8G`, `disk-size: 16G`, `workspace-quota: 16G` — the values already shown in `docs/configuration/launcher.md`. Accepting defaults writes them explicitly.

## Global Constraints

- Go 1.26; idiomatic Go, self-documenting non-abbreviated identifiers; KISS → YAGNI → SOLID → DRY.
- No comments unless the code cannot be made self-documenting.
- TDD for behavior changes: write the failing test, watch it fail, implement, watch it pass, commit. The extraction task is behavior-preserving — its test is that the existing suite stays green plus new `configfile` tests for the moved behavior.
- `make check` (fmt, lint, test, docs-linkcheck) must pass before finalizing; use `golangci-lint` (`make fmt` / `make lint`); never `go vet` or manual `go fmt`. The suppression linter name is `exhaustruct_v5`.
- Run in the isolated worktree `issue-99-guided-config-setup`, not `/workspace`.
- Keep `README.md`, `docs/`, and `CHANGELOG.md` in sync for behavior changes.
- Non-interactive runs never prompt and never hang; the wizard is a no-op when `!ui.IsInteractive()`.
- The wizard writes only user-level `config.yaml`. A value set at a more specific scope (per-slug user config, project config, env, flag) is never overwritten; the question is skipped and reported.
- Do not add new `//nolint:funlen|gocognit|gocyclo|cyclop` suppressions; decompose instead.

## Review Focus

1. A launcher config in a JSON-family file (`.json`/`.jsonc`/`.json5`) → the wizard edits and writes it correctly (valid JSON-family output) rather than corrupting it; a YAML file keeps its comments and key order.
2. A key already set in the per-slug user config or project config (or via env/flag) → the wizard skips only that question, reports it, and never writes a user-level value that overrides the more specific scope.
3. Early exit ("enough questions — use defaults for the rest") → defaults are applied to every remaining in-scope question and recorded, so a second `config wizard` does not re-ask them.
4. A non-interactive run → the wizard returns without prompting; `run` still proceeds.
5. Declining the resources group confirm → the individual resource questions are asked; accepting another group confirm collapses its questions without asking them.

---

## Phase 0 — Shared Config-File Editor

### Task 1: Extract `internal/configfile` and switch `configmigration` to it

**Files:**
- Create: `internal/configfile/configfile.go`, `internal/configfile/configfile_test.go`
- Delete: `internal/configmigration/launcher_config.go`
- Modify: `internal/configmigration/spec.go`, `store.go`, `network_plan.go`, `review.go`, `persistence.go`
- Test: existing `internal/configmigration` suite (must stay green) plus the new `configfile_test.go`

**Interfaces:**
- Consumes: the existing `launcherConfig` type, `parseLauncherConfig`, `setString`/`setBool`/`setValue`, `appendListItems`, `filterListItems`, `marshal`, and the YAML-node helpers in `internal/configmigration/launcher_config.go`; `migrationLoadLauncherConfig`/`migrationIsJSONConfig` in `internal/configmigration/store.go`.
- Produces (package `configfile`):
  - `type Config struct { values map[string]any; yamlDocument *yaml.Node }`
  - `func Parse(path string, data []byte) (*Config, error)`
  - `func IsJSON(path string) bool`
  - `func LoadDir(dir string) (path string, data []byte, err error)` — finds `config.{yaml,yml,json,jsonc,json5}` in order, returning the default path and `"{}\n"` when none exists.
  - `func (c *Config) Values() map[string]any`
  - `func (c *Config) GetString(keyPath []string) (string, bool)`
  - `func (c *Config) SetString(keyPath []string, value string)`
  - `func (c *Config) SetBool(keyPath []string, value bool)`
  - `func (c *Config) SetInt(keyPath []string, value int)`
  - `func (c *Config) AppendListItems(keyPath []string, items []string)`
  - `func (c *Config) FilterListItems(keyPath []string, keep func(string) bool)`
  - `func (c *Config) Marshal() ([]byte, error)`
  - `func WriteAtomic(path string, data []byte, mode os.FileMode) error`

- [ ] **Step 1: Create `internal/configfile/configfile.go`** by moving the contents of `launcher_config.go` and the JSON/YAML helpers from `store.go` verbatim, renaming `launcherConfig` → `Config` and exporting the methods named in Interfaces. Move `extJSON`/`extJSONC`/`extJSON5`/`extYAML`/`extYML`/`defaultConfigFile` constants into this package. Keep `WriteAtomic` body identical to `migrationAtomicWrite` (including `commitTempFile`).
- [ ] **Step 2: Update `internal/configmigration`** to use `configfile.Parse`, `configfile.IsJSON`, `configfile.LoadDir`; replace `config.values` with `config.Values()`; keep `migrationLoadLauncherConfig`/`migrationLauncherConfigPath`/`migrationIsJSONConfig` as thin wrappers over `configfile.LoadDir`/`configfile.IsJSON` so existing callers and tests are untouched. Point `migrationAtomicWrite` at `configfile.WriteAtomic`; remove now-unused constants from `persistence.go`.
- [ ] **Step 3: Add `internal/configfile/configfile_test.go`** with table tests for: YAML comments/order preserved through `SetString`+`Marshal`; JSON/JSONC/JSON5 round-trip; `AppendListItems`/`FilterListItems` preserving an existing YAML sequence's comments; `LoadDir` default when no file exists; `GetString` on a nested path.
- [ ] **Step 4: Run the existing suites plus the new package**

Run: `go test ./internal/configfile ./internal/configmigration ./cmd/agents-sandbox`
Expected: PASS.

- [ ] **Step 5: Run the linter**

Run: `golangci-lint cache clean && make lint`
Expected: 0 issues.

- [ ] **Step 6: Commit**

```bash
git add internal/configfile internal/configmigration
git commit -m "refactor(configfile): extract shared launcher-config editor"
```

---

## Phase 1 — Wizard Foundation

### Task 2: Define the `SetupQuestion` model and catalog

**Files:**
- Create: `internal/configwizard/question.go`, `internal/configwizard/catalog.go`, `internal/configwizard/question_test.go`
- Test: `internal/configwizard/question_test.go`

**Interfaces:**
- Consumes: `termio.Choice`, `agent.Names()`, `options.ParseMemoryOK`, `viperconfig.ParseHumanDuration`.
- Produces:
  - `type Kind int` with `ChoiceKind`, `ValueKind`, `ConfirmKind`, `GroupConfirmKind`.
  - `type ValueType int` with `StringValue`, `BoolValue`, `IntValue`.
  - `type SetupQuestion struct { KeyPath []string; Tier int; Kind Kind; ValueType ValueType; Prompt string; Default string; Choices []termio.Choice; Validate func(string) error; Essential bool; AskOnlyIfUnset bool; AskEveryFirstRun bool; Group string }`
  - `type Tier = int` constants `TierEssentials = 1`, `TierResources = 2`, `TierRuntime = 3`, `TierIntegrations = 4`.
  - `func DefaultCatalog() []SetupQuestion` — the catalog below.
  - `func QuestionKey(q SetupQuestion) string` — `strings.Join(q.KeyPath, ".")` (empty for group confirms).

Catalog (in order):

| Key path | Tier | Kind | Default | Notes |
|---|---|---|---|---|
| `agent` | 1 | Choice | `opencode` | Essential, AskOnlyIfUnset; choices from `agent.Names()` |
| `network.profile` | 1 | Choice | `none` | Essential, AskEveryFirstRun; choices `none`, `private`, `host`, `public` |
| *(group `resources`)* | 2 | GroupConfirm | `yes` | collapses the four resource questions |
| `cpus` | 2 | Value/Int | `4` | Validate 0–255, Group `resources` |
| `memory` | 2 | Value/String | `8G` | Validate `options.ParseMemoryOK`, Group `resources` |
| `disk-size` | 2 | Value/String | `16G` | Validate `options.ParseMemoryOK`, Group `resources` |
| `workspace-quota` | 2 | Value/String | `16G` | Validate `options.ParseMemoryOK`, Group `resources` |
| `dind` | 3 | Confirm/Bool | `false` | |
| `tmp-size` | 3 | Value/String | `2G` | Validate `options.ParseMemoryOK` |
| `log-level` | 3 | Choice | `info` | choices `error`, `warning`, `info`, `verbose` |
| *(group `notify`)* | 4 | GroupConfirm | `yes` | collapses the notify questions |
| `notify.desktop` | 4 | Confirm/Bool | `false` | Group `notify` |
| `notify.audio` | 4 | Choice | `off` | choices `off`, `system`, `bell`; Group `notify` |
| `upgrade.mode` | 4 | Choice | `prompt` | choices `prompt`, `notify`, `auto`, `auto-exit` |
| `upgrade.interval` | 4 | Value/String | `1d` | Validate `>= 1h` via `viperconfig.ParseHumanDuration` |
| `auto-prune-age` | 4 | Value/String | `30d` | Validate `> 0` via `viperconfig.ParseHumanDuration` |

- [ ] **Step 1: Write the failing test**

```go
func TestDefaultCatalogIsWellFormed(t *testing.T) {
	seen := make(map[string]bool)
	for _, q := range DefaultCatalog() {
		if q.Kind == GroupConfirmKind {
			if q.Group == "" {
				t.Errorf("group confirm %q has no group", q.Prompt)
			}
			continue
		}
		key := QuestionKey(q)
		if key == "" {
			t.Errorf("question %q has no key path", q.Prompt)
		}
		if seen[key] {
			t.Errorf("duplicate key %q", key)
		}
		seen[key] = true
		if q.Default == "" {
			t.Errorf("question %q has no default", key)
		}
		if q.Validate != nil {
			if err := q.Validate(q.Default); err != nil {
				t.Errorf("question %q default %q fails its own validation: %v", key, q.Default, err)
			}
		}
	}
	if !seen["agent"] || !seen["network.profile"] || !seen["cpus"] {
		t.Errorf("catalog is missing expected keys: %v", seen)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/configwizard -run TestDefaultCatalogIsWellFormed`
Expected: FAIL (undefined).

- [ ] **Step 3: Implement** `question.go` (types + `QuestionKey`) and `catalog.go` (`DefaultCatalog` with the table above, validators as small named functions).
- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/configwizard`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/configwizard
git commit -m "feat(configwizard): declarative setup question catalog"
```

---

### Task 3: Persist answered and first-run state

**Files:**
- Create: `internal/configwizard/state.go`, `internal/configwizard/state_test.go`
- Test: `internal/configwizard/state_test.go`

**Interfaces:**
- Consumes: `configpaths.Get().UserStateDir()`, `configfile.WriteAtomic` (Task 1).
- Produces:
  - `type State struct { Version int \`yaml:"version"\`; FirstRunDone bool \`yaml:"first_run_done,omitempty"\`; Asked []string \`yaml:"asked,omitempty"\` }`
  - `func LoadState() State`
  - `func SaveState(state State) error` — atomic write at `<UserStateDir>/config-wizard.yaml`, mode `0o600`.
  - `func (s State) WasAsked(key string) bool`
  - `func (s *State) MarkAsked(keys ...string)` — appends keys not already present, keeping a stable (catalog) order.

- [ ] **Step 1: Write the failing test**

```go
func TestStateRoundTripsAskedKeys(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	state := LoadState()
	if state.FirstRunDone || len(state.Asked) != 0 {
		t.Fatalf("fresh state = %+v, want zero value", state)
	}
	state.FirstRunDone = true
	state.MarkAsked("agent", "cpus", "agent")
	if err := SaveState(state); err != nil {
		t.Fatal(err)
	}
	reloaded := LoadState()
	if !reloaded.FirstRunDone {
		t.Error("FirstRunDone not persisted")
	}
	if !reloaded.WasAsked("cpus") || reloaded.WasAsked("nope") {
		t.Errorf("WasAsked wrong: %v", reloaded.Asked)
	}
	if len(reloaded.Asked) != 2 {
		t.Errorf("Asked = %v, want 2 unique keys", reloaded.Asked)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/configwizard -run TestStateRoundTripsAskedKeys`
Expected: FAIL (undefined).

- [ ] **Step 3: Implement** `state.go`. `SaveState` marshals via `yaml.Marshal` and writes through `configfile.WriteAtomic`; `LoadState` returns the zero value on a missing or malformed file.
- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/configwizard`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/configwizard
git commit -m "feat(configwizard): persist answered and first-run wizard state"
```

---

### Task 4: Report keys set at a more specific scope

**Files:**
- Modify: `internal/viperconfig/viperconfig.go` (`Resolver` field + `NewResolver` + new method)
- Test: `internal/viperconfig/viperconfig_test.go`

**Interfaces:**
- Consumes: the resolver's existing merge loop (`mergeDir`), `configEnvKeys`, `configFlagKeys`, `findFlag`.
- Produces:
  - `func (r *Resolver) KeySetAboveUserConfig(key string) bool` — true when `key` is present in the per-slug user config file or the project config file, or set via its `OPENCODE_SANDBOX_` env var, or set by an explicit command-line flag (`configFlagKeys`, plus the `network.profile` ↔ `--network` special case). Keys use dotted form (`"network.profile"`).

- [ ] **Step 1: Write the failing test**

```go
func TestKeySetAboveUserConfig(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	writeConfigFile(t, configpaths.Get().ProjectConfigDir(), "config.yaml", "cpus: 8\nnetwork:\n  profile: public\n")
	r, err := NewResolver(nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if !r.KeySetAboveUserConfig("cpus") {
		t.Error("project cpus not reported above user scope")
	}
	if !r.KeySetAboveUserConfig("network.profile") {
		t.Error("project network.profile not reported above user scope")
	}
	if r.KeySetAboveUserConfig("memory") {
		t.Error("unset memory reported above user scope")
	}
}
```

`writeConfigFile` reuses the existing test helper for writing a config file into a directory (adapt to the file's actual helper name).

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/viperconfig -run TestKeySetAboveUserConfig`
Expected: FAIL (`KeySetAboveUserConfig` undefined).

- [ ] **Step 3: Implement** — add `higherScope map[string]bool` to `Resolver`; in `NewResolver`, before/while merging the per-slug and project dirs record each file's leaf key paths (dotted), then fold in env presence for `configEnvKeys` and `flag.Changed` for `configFlagKeys` (when `cmd != nil`). Add the `network.profile`/`--network` flag special case. Set `higherScope: map[string]bool{}` in `NewResolverWithConfig`.
- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/viperconfig`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/viperconfig
git commit -m "feat(viperconfig): report keys set above the user config scope"
```

---

### Task 5: Run questions with group collapse and early exit

**Files:**
- Create: `internal/configwizard/runner.go`, `internal/configwizard/runner_test.go`
- Test: `internal/configwizard/runner_test.go`

**Interfaces:**
- Consumes: `SetupQuestion`, `DefaultCatalog` (Task 2), `State` (Task 3), `configfile.Config` (Task 1), `termio.UI`.
- Produces:
  - `const stopChoice = "enough"`
  - `type ScopeResolver interface { KeySetAboveUserConfig(key string) bool }`
  - `type runner struct { questions []SetupQuestion; tiers map[int]bool; firstRun bool; scope ScopeResolver; state State; ui termio.UI; config *configfile.Config }`
  - `func (r *runner) run() (stopped int, err error)` — asks every in-scope, unanswered, not-higher-scope question. On early exit it applies each remaining in-scope question's default **and** marks it asked (respecting the higher-scope/already-set skip rules), then returns the index of the question that stopped the interview (`len(questions)` when none). The runner owns `state` by value, so `Run` (Task 6) reads `r.state` afterwards.
  - `func (r *runner) Changed() bool` — whether any value was written to the config.
  - helper `func (r *runner) ask(q SetupQuestion) (value string, stop bool, err error)` for one question, and `func (r *runner) applyDefault(q SetupQuestion) error`.
  - `func setQuestionValue(cfg *configfile.Config, q SetupQuestion, value string) error` — `SetString`/`SetBool`/`SetInt` based on `ValueType` (`IntValue` uses `strconv.Atoi`).

Behavior to implement:
- Skip a question when `WasAsked(QuestionKey(q))`, or `KeySetAboveUserConfig(QuestionKey(q))` (report `"<key> is already set at a more specific scope; skipping"` and mark asked).
- `AskOnlyIfUnset`: also skip when `config.GetString(q.KeyPath)` reports the key present.
- `AskEveryFirstRun`: ask even when `WasAsked`, when `firstRun`.
- A `GroupConfirmKind` question: `Select` yes/no; `yes` applies each group member's default **through the same skip rules** (`applyDefault` must not overwrite a member whose key is higher-scope or already set) and marks the members asked; `no` falls through (members are asked individually later in the catalog).
- A non-essential question appends a `termio.Choice{Key: stopChoice, Label: "Enough questions — use defaults for the rest"}`. On `stopChoice`, the runner returns the current index; the caller applies defaults to the remaining in-scope questions and marks them asked.
- `ChoiceKind`: `Select` over `Choices` with `Default` as the default key; the selected key is the value.
- `ConfirmKind`: `Select` yes/no defaulting to `q.Default`; value `"true"`/`"false"`.
- `ValueKind`: `Select` over `{default, custom, [enough]}`; `custom` prompts `ui.Input(q.Prompt, q.Default)` and validates via `q.Validate`; empty input is rejected back to the default.
- Essential questions never carry the stop choice and are asked first (catalog order already places them first).

- [ ] **Step 1: Write the failing tests**

```go
func TestRunnerAcceptsGroupDefaultsWithOneKeystroke(t *testing.T) {
	cfg := mustConfig(t, "{}\n")
	mock := &termio.Mock{IsInteractiveResult: true}
	mock.SelectFn = func(prompt string, choices []termio.Choice, def string) (string, error) {
		if strings.Contains(prompt, "recommended resource") {
			return "yes", nil
		}
		return def, nil
	}
	r := &runner{
		questions: DefaultCatalog(), tiers: map[int]bool{TierResources: true},
		scope: noScope{}, state: State{}, ui: mock, config: cfg,
	}
	if _, err := r.run(); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{"cpus": "4", "memory": "8G", "disk-size": "16G", "workspace-quota": "16G"} {
		if got, _ := cfg.GetString(strings.Split(key, ".")); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
}

func TestRunnerEarlyExitAppliesDefaultsToRest(t *testing.T) {
	cfg := mustConfig(t, "{}\n")
	mock := &termio.Mock{IsInteractiveResult: true}
	mock.SelectFn = func(prompt string, _ []termio.Choice, def string) (string, error) {
		switch {
		case strings.Contains(prompt, "recommended resource"):
			return "no", nil
		case strings.Contains(prompt, "disk"):
			return stopChoice, nil
		default:
			return def, nil
		}
	}
	r := &runner{
		questions: DefaultCatalog(), tiers: map[int]bool{TierResources: true},
		scope: noScope{}, state: State{}, ui: mock, config: cfg,
	}
	stop, err := r.run()
	if err != nil {
		t.Fatal(err)
	}
	if stop >= len(DefaultCatalog()) {
		t.Fatalf("expected an early stop, got %d", stop)
	}
	if got, _ := cfg.GetString([]string{"workspace-quota"}); got != "16G" {
		t.Errorf("workspace-quota default not applied after early exit: %q", got)
	}
}

func TestRunnerSkipsHigherScopeAndUnsetCollapses(t *testing.T) {
	cfg := mustConfig(t, "cpus: 12\n")
	mock := &termio.Mock{IsInteractiveResult: true}
	r := &runner{
		questions: DefaultCatalog(), tiers: map[int]bool{TierResources: true},
		scope: higherScope{"cpus": true}, state: State{}, ui: mock, config: cfg,
	}
	if _, err := r.run(); err != nil {
		t.Fatal(err)
	}
	if len(mock.WarnCalls)+len(mock.InfoCalls) == 0 {
		t.Error("expected a skip to be reported")
	}
}
```

Define the test helpers in `runner_test.go`: `mustConfig(t, data string) *configfile.Config` (wraps `configfile.Parse("config.yaml", []byte(data))`), `type noScope struct{}`, and `type higherScope map[string]bool`, both implementing `KeySetAboveUserConfig`.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/configwizard -run TestRunner`
Expected: FAIL (undefined).

- [ ] **Step 3: Implement** `runner.go` per the behavior list. Extract the per-question branches into small methods to stay under complexity limits.
- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/configwizard`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/configwizard
git commit -m "feat(configwizard): question runner with group collapse and early exit"
```

---

### Task 6: Public `Run` with tier selection and first-run gating

**Files:**
- Create: `internal/configwizard/wizard.go`, `internal/configwizard/wizard_test.go`
- Test: `internal/configwizard/wizard_test.go`

**Interfaces:**
- Consumes: `runner` (Task 5), `State` (Task 3), `configfile.LoadDir`/`Parse`/`WriteAtomic` (Task 1), `configpaths.Get().UserConfigDir()`.
- Produces:
  - `type Selection struct { FirstRun bool; Tier int }`
  - `func Run(scope ScopeResolver, ui termio.UI, sel Selection) error`

Behavior:
- No-op when `!ui.IsInteractive()`.
- Load state. When `sel.FirstRun && state.FirstRunDone`, return nil (quiet).
- Determine `tiers`: `sel.Tier > 0` → that tier only; else `sel.FirstRun` → tiers 1–3; else all tiers 1–4.
- Load the user config (`configfile.LoadDir` then `configfile.Parse`); build the runner. `run()` already applies defaults to the remaining in-scope questions on early exit; `Run` only reads the runner's resulting `state` and `Changed()`.
- Write the config only when `runner.Changed()`; write state always when a question was processed. When the essentials tier is in scope, set `r.state.FirstRunDone = true`.
- Report a short summary of written values.

- [ ] **Step 1: Write the failing tests**

```go
func TestRunFirstRunAsksShallowTiersOnly(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	mock := &termio.Mock{IsInteractiveResult: true}
	mock.SelectFn = func(prompt string, _ []termio.Choice, def string) (string, error) { return def, nil }
	if err := Run(noScope{}, mock, Selection{FirstRun: true}); err != nil {
		t.Fatal(err)
	}
	data := readUserConfig(t)
	if strings.Contains(data, "upgrade:") || strings.Contains(data, "notify:") {
		t.Errorf("first run wrote an integration key:\n%s", data)
	}
	if !LoadState().FirstRunDone {
		t.Error("FirstRunDone not recorded")
	}
}

func TestRunIsQuietAfterFirstRun(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	SaveState(State{Version: 1, FirstRunDone: true}) //nolint:errcheck // test setup
	mock := &termio.Mock{IsInteractiveResult: true}
	if err := Run(noScope{}, mock, Selection{FirstRun: true}); err != nil {
		t.Fatal(err)
	}
	if len(mock.SelectCalls) != 0 {
		t.Fatalf("quiet first run prompted %d times", len(mock.SelectCalls))
	}
}

func TestRunNonInteractiveDoesNothing(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	mock := &termio.Mock{}
	if err := Run(noScope{}, mock, Selection{FirstRun: true}); err != nil {
		t.Fatal(err)
	}
	if len(mock.SelectCalls) != 0 {
		t.Fatal("non-interactive run prompted")
	}
}

func TestRunExplicitTierAsksThatTier(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	mock := &termio.Mock{IsInteractiveResult: true}
	mock.SelectFn = func(prompt string, _ []termio.Choice, def string) (string, error) { return def, nil }
	if err := Run(noScope{}, mock, Selection{Tier: TierRuntime}); err != nil {
		t.Fatal(err)
	}
	if data := readUserConfig(t); !strings.Contains(data, "dind") {
		t.Errorf("runtime tier not written:\n%s", data)
	}
}
```

Define `readUserConfig(t) string` in `wizard_test.go` (reads `<UserConfigDir>/config.yaml`). `termio.Mock` does not yet record prompts; Task step 3 below adds a `SelectCalls []string` recorder so `TestRunIsQuietAfterFirstRun` can assert zero prompts.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/configwizard -run TestRun`
Expected: FAIL (undefined).

- [ ] **Step 3: Add select/input call recording to `internal/termio/mock.go`.** Append the prompt to `SelectCalls`/`InputCalls` inside `Select`/`Input` before delegating to the `Fn`, then return. Update `internal/termio/mock_test.go` if it asserts on the recording.
- [ ] **Step 4: Implement** `wizard.go`.
- [ ] **Step 5: Run the package plus `termio`**

Run: `go test ./internal/configwizard ./internal/termio`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/configwizard internal/termio
git commit -m "feat(configwizard): tiered Run entrypoint with first-run gating"
```

---

## Phase 2 — CLI and First-Run Integration

### Task 7: Add the `config wizard` command

**Files:**
- Modify: `cmd/agents-sandbox/commands_system.go` (`buildConfigCmd`), `cmd/agents-sandbox/constants.go`
- Test: `cmd/agents-sandbox/cli_config_test.go`

**Interfaces:**
- Consumes: `configwizard.Run` (Task 6), `resolverFromContext` (`commands.go`), `flagTier`.
- Produces: `cmdWizard = "wizard"`, `flagTier = "tier"`; a `buildConfigWizardCmd(ui termio.UI) *cobra.Command` added to `buildConfigCmd`.

- [ ] **Step 1: Write the failing CLI tests**

```go
func TestConfigWizardAsksSelectedTier(t *testing.T) {
	cmd, ui := setupCommandFixtures(t, "config", "wizard", "--tier=3")
	ui.IsInteractiveResult = true
	ui.SelectFn = func(_ string, _ []termio.Choice, def string) (string, error) { return def, nil }
	if err := cmd.Execute(); err != nil {
		t.Fatalf("config wizard: %v", err)
	}
	cfg, err := os.ReadFile(filepath.Join(configpaths.Get().UserConfigDir(), "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(cfg), "tmp-size") {
		t.Errorf("runtime tier not written:\n%s", cfg)
	}
}

func TestConfigWizardRejectsUnknownTier(t *testing.T) {
	cmd, _ := setupCommandFixtures(t, "config", "wizard", "--tier=9")
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected an error for tier 9")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./cmd/agents-sandbox -run TestConfigWizard`
Expected: FAIL (unknown command).

- [ ] **Step 3: Implement** `buildConfigWizardCmd`: `Use: cmdWizard`, `Args: cobra.NoArgs`, `--tier` int flag defaulting to 0; validate the tier is one of 0..4 (0 = all unanswered); call `configwizard.Run(resolverFromContext(c.Context()), ui, configwizard.Selection{Tier: tier})`. Because `commandNeedsMSBRuntime` already exempts `config`, the command works without an msb runtime. Add the command in `buildConfigCmd`.
- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./cmd/agents-sandbox -run 'TestConfigWizard|TestTree'`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/agents-sandbox
git commit -m "feat(cli): add config wizard command with tier selection"
```

---

### Task 8: Run the wizard on first run, before credential migration

**Files:**
- Modify: `cmd/agents-sandbox/commands_cli.go` (`runFunc`, new seam)
- Test: `cmd/agents-sandbox/cli_run_shell_test.go` or a new `cli_config_wizard_test.go`

**Interfaces:**
- Consumes: `configwizard.Run` (Task 6), `refreshRunOptionsAfterMigrationFn` (existing).
- Produces:
  - `var runConfigWizardFn = runConfigWizard` (test seam, marked `//nolint:gochecknoglobals`).
  - `func runConfigWizard(r *launcherconfig.Resolver, ui termio.UI) error` — calls `configwizard.Run(r, ui, configwizard.Selection{FirstRun: true})`.

Behavior in `runFunc`, after the preflight/upgrade block and before `guideConfigMigration`:
```go
if !isDryRun {
    if wizardErr := runConfigWizardFn(r, ui); wizardErr != nil {
        return wizardErr
    }
    opts, r, err = refreshRunOptionsAfterMigrationFn(cmd, args, ui)
    if err != nil {
        return err
    }
}
```
The existing post-Guide refresh stays as is.

- [ ] **Step 1: Write the failing test** asserting that a non-dry-run `run` on a fresh config invokes the wizard seam exactly once before migration, and that its error is propagated. Use the existing `runFunc` test fixtures; override `runConfigWizardFn` with a recorder and `guideConfigMigration`'s dependencies as the file already does. Assert the wizard seam is called once and that the refreshed resolver is what `guideConfigMigration` observes (e.g. by having the seam write `network.profile: public` and the migration stub capture the resolved policy).

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/agents-sandbox -run TestRunInvokesFirstRunWizard`
Expected: FAIL (seam undefined / not called).

- [ ] **Step 3: Implement** the seam, `runConfigWizard`, and the `runFunc` insertion.
- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./cmd/agents-sandbox`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/agents-sandbox
git commit -m "feat(cli): run setup wizard before first-run credential migration"
```

---

## Phase 3 — Documentation and Brain

### Task 9: Docs, CHANGELOG, and brain updates

**Files:**
- Create: `docs/configuration/wizard.md`
- Modify: `docs/commands.md`, `docs/configuration/launcher.md` (link the wizard page), `CHANGELOG.md`
- Modify: `.operator-shared/specs/guided-onboarding.md` (record the fixed tier catalog and first-run ordering)
- Modify: `.operator-shared/catalog.md` (catalog the new plan)

- [ ] **Step 1:** Write `docs/configuration/wizard.md`: the tier table, first-run behavior, `config wizard [--tier=N]` semantics, the group-collapse and early-exit behavior, the skip-what-is-already-set rule, and where answered state is stored (`$XDG_STATE_HOME/agents-sandbox/config-wizard.yaml`). Use Jekyll `{% link %}` tags for cross-page links per the docs rules.
- [ ] **Step 2:** Add the `config wizard` row to `docs/commands.md` and link `configuration/wizard.md` from `docs/configuration/launcher.md`.
- [ ] **Step 3:** Add an `[Unreleased]` `### Added` bullet to `CHANGELOG.md` describing the tiered wizard and first-run behavior.
- [ ] **Step 4:** In `.operator-shared/specs/guided-onboarding.md`, update the "Tiered setup wizard" section to state the now-fixed tier membership (T1–T4), the first-run subset (T1–T3), and the pre-migration ordering; keep the re-tierable design note but point at the wizard catalog as the source.
- [ ] **Step 5:** Add the Plan C entry to `.operator-shared/catalog.md` under `plans/`.
- [ ] **Step 6: Verify docs and links**

Run: `ci/check-docs.sh && make check`
Expected: PASS (fmt, lint, tests, docs-linkcheck).

- [ ] **Step 7: Commit**

```bash
git add docs CHANGELOG.md .operator-shared
git commit -m "docs(configwizard): document tiered setup wizard and update brain"
```

---

## Self-Review

**Spec coverage:** declarative `SetupQuestion` catalog → T2; first-run shallow tiers + `config wizard --tier=N` → T6, T7, T8; group collapse → T5; "enough questions" early exit → T5; skip-when-set + report → T4, T5; answered/dismissed state (never nag) → T3, T6; comment/order-preserving user-config write → T1, T6; credential flow after the wizard establishes agent + network → T8. The "Virtual/each question has a default; one keystroke" requirement is pinned by T2 (`Default` non-empty test) and T5 (group defaults + choice defaults).

**Step scan:** every step is one action with a checkable result; code steps give signatures and exact values, not bodies; test steps give assertions with the spec's values.

**Type consistency:** `configfile.Config` methods (`GetString`/`SetString`/`SetBool`/`AppendListItems`/`FilterListItems`/`Marshal`) are defined once in T1 and used in T5/T6; `SetupQuestion`/`Kind`/`ValueType`/`QuestionKey` in T2 used in T5/T6; `State` in T3 used in T5/T6; `Runner`/`run`/`ScopeResolver`/`stopChoice` in T5 used in T6; `Run`/`Selection` in T6 used in T7/T8; `KeySetAboveUserConfig` in T4 used in T5.

**Review Focus mapping:** (1) T1 (JSON-family round-trip + comment preservation); (2) T4 (higher-scope) + T5 (skip/report); (3) T5 (early-exit test) + T6 (defaults applied, state recorded); (4) T6 (non-interactive no-op); (5) T5 (group default test) + T5 (individual questions when declined).

**Known open points for reviewer:** the recommended resource defaults (`4` / `8G` / `16G` / `16G`) are a product choice; the T4 membership (notify/upgrade/auto-prune) can be re-tiered without structural change.

## Execution Handoff

Choose an execution method after review:
- **Subagent-driven** (recommended): T1 is a wide mechanical extraction across `configmigration`; T4–T6 introduce shared types (`SetupQuestion`, `State`, `runner`) whose interfaces must stay consistent; fresh review per task catches breakage early.
- **Native**: faster, single-session, one final review.
