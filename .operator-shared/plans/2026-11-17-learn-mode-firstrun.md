# S9 — first-run learn mode

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. A fresh implementer per task; a fresh reviewer gates each task; one whole-subplan review at the end. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** On a first run — a checkout and agent with no managed config and no recorded onboarding decision — offer the three first-run choices from the learn-mode vocabulary (**Learn by using it** / **Learn from my host setup** / **Configure it myself**), start a learning session for either learning choice (UC1 unseeded; UC4 seeded from the host config with static secrets placeholdered), record the decision so it stops nagging and re-arms on a host-config change, and carry the `provision-host-config` unsafe opt-in warning on the only choice that needs it.

**Architecture:** A new `internal/firstrun` package owns the decision (`Decide`) and the menu (`Prompt`), the host-config probe (`HostFiles`), and the UC4 seed (`SeedFromHost`); it reads the effective config through `viperconfig`/`configpaths`, the host config through the agent's existing `ProvisionRule`s (`agent.EvalProvisionRules`), and classifies credentials through the `readback` walker the inline-read-back work (S11) added. The decision is recorded in `RootState` keyed by `<repo slug>/<agent>` and re-armed by a host-config source hash — no new state file. `cmd/agents-sandbox` runs the menu in the `run`/`shell` path before `session.Run`, so a learning choice sets `opts.Learn` and the host-seed choice writes the managed per-slug scope before the VM is prepared. The menu drives the existing `termio.Prompter.Select`; the `huh` backend (#119) is a separate UX upgrade and is not a dependency.

**Tech Stack:** Go 1.27, `internal/firstrun` (new), `internal/sandbox/{state,readback,options,envsecret,reprovision}`, `internal/{agent,configpaths,viperconfig,runoptions,termio}`, `cmd/agents-sandbox`.

**Spec:** `.operator-shared/specs/onboarding/learn-mode.md` (§User-facing vocabulary, §The four journeys, UC1/UC4, invariants 1/2/5/6); `.operator-shared/specs/onboarding/onboarding.md` (§Principles, §Credential kinds, §`Guide` outcomes — the learn hand-off and its seeded form, §Tiered setup wizard — out of scope); `.operator-shared/specs/onboarding/learn-mode/home-read-back.md` (the credential classification the seed reuses).

**Consumes:** `analyser`/`learn`/`readback`/`channel` (S4–S7, S10, S11, landed); `options.RunOptions.{Learn,ProvisionHostConfig}`; `state.{WithRootState,RootTxn}`; `agent.{EvalProvisionRules,ProvisionRule,ConfigDirNames}`; `reprovision`'s managed-config target convention; `configpaths.{ConfigPaths,UserSlugConfigDir,ConfigScopes}`; `envsecret.WriteSecretSpec`.

**Produces:** `internal/firstrun.{Facts,Choice,Decide,Prompt,HostFiles,SeedFromHost,Outcome}`; `state.{FirstRunChoice,FirstRunRecord,RecordFirstRun,ReadFirstRun}` (+ a `RootState` field and `RootStateSchema` bump); the `provision-host-config: false` default.

**Supersedes:** #54 (first-run wizard), #97/#98/#99 (safe migration / secure default), and — with the whole learned path — the `config migrate` curated provider table (#54/#99/#103). The first-run wizard half of the stale `issue-99-guided-config-setup` branch (PR #103) and of PR #119 are treated as spikes, not dependencies (see Design decisions 1–2). Its closure marks are Task 7's.

## Global Constraints

See `2026-11-17-learn-mode-program.md` → Global Constraints. Specifically:

- **Propose, never apply silently** (invariant 1). A first-run choice that starts a *learning* session applies nothing; the after-run proposal is S7/S10's, behind the exit prompt.
- **The credential store is never copied out of the sandbox** (invariant 2). The UC4 seed reads the *host* config and writes only placeholders + `env.secret.yaml`; it never writes a login entry to a managed file.
- **No curated knowledge in the learned path** (invariant 5). The seed derives each host from the endpoint the config entry names, never from a provider table.
- **Non-interactive never hangs** (invariant 6). A non-interactive `run`/`shell` warns and continues, offering nothing.
- **No value in the decision record, a finding, or a log.** The seed threads a static value in memory only.
- Any first-run write to the *project* scope goes through `projecttrust`; the seed targets only the per-slug user scope.
- `make fmt`, `make lint`, `make check` — never `go fmt`/`go vet` by hand.
- Host-only verification: the seeded real session is a **human accept** step (Task 8). `verification-before-completion` stops at the PR.

## Design decisions (calls made; correct if wrong)

1. **S9 is written against the current tree; PR #103 is not a dependency.** `issue-99-guided-config-setup` is a 2026-11-09 spike (~14.8k insertions, unmerged) whose `internal/configmigration`/`internal/configwizard` predate S4–S11. S9 reuses its *ideas* — the derived first-run decision, the static-vs-login credential kinds, the host-first seed — and none of its code. The tiered `config wizard` (`configwizard`) and the curated-table `config migrate` are **out of scope**: learn mode supersedes the declaration model (program §Enablers, §Retirement).
2. **The `huh` prompt backend (#119) is a separate UX upgrade, not an S9 dependency.** The menu is delivered on `termio.Prompter.Select` (the line backend). `specs/architecture/termio-ui.md` already carries the deferred backend contract; when #119 is reimplemented it changes only how `Select` renders. S9 must not add a `charmbracelet/*` dependency. (Program E2: "land before S9" becomes "may land before or after; S9 works either way".)
3. **The secure default ships with S9.** `provision-host-config` defaults to `false` (Task 1), so the first-run menu is the path to a working sandbox and the drop-in copy of raw host credentials is the explicit, warned opt-in. This is the documented onboarding direction (`onboarding.md` preamble, principle 1) and the `#98` change folded into this subplan rather than a separate one.
4. **The decision lives in `RootState`, keyed by `<repo slug>/<agent>`, re-armed by a host-config source hash.** It mirrors `LearnDeclines` (`state/learn_decline.go`): a locked `WithRootState` transaction, an `omitempty` map, and a `RootStateSchema` bump. A repo-slug key means every clone of one remote shares the decision, matching the per-slug proposal scope. No new state file (the onboarding spec's `config-migration.yaml` is #103's; not recreated).
5. **UC4 seeds through the agent's existing provision rules, not a new host-config reader.** `agent.EvalProvisionRules` already resolves the drop-in copy (host path → VM path) — the same machinery `provision-host-config: true` uses. The seed reads those host files, classifies each credential with the shared `readback` walker (Task 5), writes static secrets to the per-slug `env.secret.yaml` and a placeholdered copy of each config file to the per-slug managed config dir, and drops login entries. It never raw-copies a value into the sandbox home.
6. **The menu runs in the cmd layer, before `session.Run`.** It must set `opts.Learn` / `opts.ProvisionHostConfig` before options are consumed, exactly as `onboarding.md` §Tiered setup wizard describes the wizard re-resolving options. It is therefore wired in `commands_cli.go`'s `run`/`shell` path, not inside `session.Run`. `build` gets no menu (it starts no agent).
7. **E3 (#252) is the acceptance harness.** S9's end-to-end acceptance (Task 8) is the UC1/UC4 journey on a KVM host and is the concrete seed for #252; the harness itself is not re-specified here.
8. **Two choices, not three, when the host has no config.** "Learn from my host setup" is offered only when `HostFiles` is non-empty (the hub: *only when host config exists, and recommended in that case*). With no host config the menu is two choices and the recommended default is **Learn by using it**.

## Review Focus

1. **A first-run choice that writes a raw credential** — the UC4 seed must placeholder every static value and drop every login; a raw value in the managed config, `env.secret.yaml`'s placeholders aside, or a log is the worst failure. (Task 5.)
2. **The menu firing when it must not** — a non-interactive run, a `--bound` project with managed config already present, or a decided source hash must all offer nothing and never hang. (Tasks 3, 4.)
3. **A decision that never re-arms or never quiets** — a declined/manual decision must stop prompting, and a host-config change must re-arm it exactly once. (Task 2, Task 3.)
4. **The default flip leaking into an existing install silently** — turning `provision-host-config` off must be visible in the changelog, the docs, and the first-run flow, not a silent behaviour change. (Tasks 1, 7.)
5. **`Configure it myself` writing `provision-host-config: true` without the warning** — the unsafe opt-in is the only choice that sets it and it must carry the warning prompt first. (Task 6.)
6. **A learning choice that fails to reach `RunOptions.Learn`** — the menu's outcome must be applied to the options the session actually consumes, not a copy. (Task 4.)
7. **A seed that widens the network policy** — the seed writes managed agent config and secrets only; it proposes no egress and removes no user entry (invariant 4). (Task 5.)

---

### Task 1: The secure default

**Files:**
- Modify: `internal/viperconfig/load.go:94`, `internal/viperconfig/load_test.go` (or the config-defaults test)
- Modify: `docs/configuration/launcher.md`, `docs/configuration/agent.md`, `docs/configuration/secrets.md`, `docs/introduction.md`, `docs/manage-config.md` (the passages that state the default)
- Test: `internal/viperconfig/load_test.go`

**Interfaces:**
- Consumes: `keyProvisionHostConfig`.
- Produces: `provision-host-config` defaults to `false`.

- [ ] **Step 1: Write the failing test**

```go
func TestProvisionHostConfigDefaultsFalse(t *testing.T) // an unset config -> cfg.ProvisionHostConfig == false
func TestProvisionHostConfigExplicitTrueStillHonored(t *testing.T) // provision-host-config: true -> true
```

- [ ] **Step 2: Run to verify it fails** — `go test ./internal/viperconfig/ -run TestProvisionHostConfig`. Expected: FAIL.

- [ ] **Step 3: Implement** the default flip (`v.SetDefault(keyProvisionHostConfig, false)`) and the comment ("host config is opt-in; the first run offers a guided path"). Leave `prepare.provisionHostConfig`'s nil-means-true fallback as the internal contract but note in the comment that cmd always sets it explicitly.

- [ ] **Step 4: Run** — `go test ./internal/viperconfig/ ./internal/runoptions/ ./internal/sandbox/prepare/`. Expected: PASS (update any test that assumed the old default).

- [ ] **Step 5: Commit**

```bash
git add internal/viperconfig/ docs/
git commit -m "feat(config): make host-config provisioning opt-in"
```

### Task 2: The first-run decision record

**Files:**
- Create: `internal/sandbox/state/firstrun.go`, `internal/sandbox/state/firstrun_test.go`
- Modify: `internal/sandbox/state/root.go` (the `RootState` field), `internal/sandbox/state/schema.go` (`RootStateSchema`)

**Interfaces:**
- Consumes: `WithRootState`, `RootTxn`.
- Produces:

```go
// FirstRunChoice is what a first run decided, so it does not nag again for the
// same host config.
type FirstRunChoice string
const (
    FirstRunLearned   FirstRunChoice = "learned"   // a learning path was chosen
    FirstRunManual    FirstRunChoice = "manual"    // configure it myself
    FirstRunDismissed FirstRunChoice = "dismissed" // the run deferred a decision
)
// FirstRunRecord is one recorded first-run outcome.
type FirstRunRecord struct {
    Choice     FirstRunChoice `yaml:"choice"`
    SourceHash string         `yaml:"source_hash"`
    At         time.Time      `yaml:"at"`
}
// RootState gains: FirstRuns map[string]FirstRunRecord `yaml:"first_runs,omitempty"` // key "<repo slug>/<agent>"
func RecordFirstRun(slug, agentName string, r FirstRunRecord) error // idempotent per key
func ReadFirstRun(slug, agentName string) (FirstRunRecord, bool, error)
```

- [ ] **Step 1: Write the failing tests**

```go
func TestFirstRunRoundTrips(t *testing.T)              // Record then Read returns the same record
func TestFirstRunSecondRecordReplaces(t *testing.T)    // a new source hash overwrites the old record
func TestFirstRunAbsentIsFalse(t *testing.T)           // no state -> (zero, false, nil)
func TestFirstRunKeyedBySlugAndAgent(t *testing.T)     // two agents under one slug are independent
func TestFirstRunDroppedFromYAMLWhenAbsent(t *testing.T)
```

- [ ] **Step 2: Run to verify it fails** — `go test ./internal/sandbox/state/ -run TestFirstRun`. Expected: FAIL.

- [ ] **Step 3: Implement** the type, the two accessors through `WithRootState`, the `RootState.FirstRuns` field, and the `RootStateSchema` bump (update `TestRootStateSchemaFieldsArePinned`).

- [ ] **Step 4: Run** — `go test ./internal/sandbox/state/`. Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/state/
git commit -m "feat(state): record the first-run decision"
```

### Task 3: The first-run decision and the host-config probe

**Files:**
- Create: `internal/firstrun/firstrun.go`, `internal/firstrun/firstrun_test.go`, `internal/firstrun/host.go`, `internal/firstrun/host_test.go`
- Create: `internal/firstrun/main_test.go` (fail-fast mocks)

**Interfaces:**
- Consumes: `state.ReadFirstRun`, `state.FirstRunRecord`; `agent.{Agent,EvalProvisionRules,Provisioner}`; `configpaths.Get()`.
- Produces:

```go
// Choice is the first-run path the user picks (the hub's vocabulary).
type Choice string
const (
    ChoiceLearnByUsing  Choice = "learn-by-using"
    ChoiceLearnFromHost Choice = "learn-from-host"
    ChoiceManual        Choice = "configure-it-myself"
)
// Facts is what the decision needs about this checkout and agent.
type Facts struct {
    Agent          string
    Slug           string   // repo slug: the per-slug scope and the decision key
    ManagedConfig  bool     // a managed config layer exists for the agent
    HostFiles      []string // the agent's host config files the seed would read; empty: no host setup
    HostSourceHash string   // hash of HostFiles' paths+contents; "" when HostFiles is empty
    Prior          state.FirstRunRecord
}
// Offer reports whether the first-run menu should be shown: no managed config,
// and either no decision or one whose source hash no longer matches.
func Offer(f Facts) bool
// HostFiles resolves the agent's host config files the seed reads and their
// source hash, by evaluating the agent's provision rules against hostHome.
func HostFiles(a agent.Agent, hostHome string) (files []string, sourceHash string)
```

- [ ] **Step 1: Write the failing tests**

```go
// firstrun_test.go
func TestOfferWhenNoManagedConfigAndUndecided(t *testing.T)
func TestOfferFalseWhenManagedConfigPresent(t *testing.T)
func TestOfferFalseWhenDecidedForSameSource(t *testing.T)
func TestOfferTrueWhenSourceHashChanged(t *testing.T) // prior hash != current -> re-arm
func TestOfferFalseWhenNoHostConfigAndAlreadyLearned(t *testing.T) // no source + learned -> quiet
// host_test.go
func TestHostFilesFindsProvisionedConfig(t *testing.T) // a fake rule set -> the host paths + a stable hash
func TestHostFilesEmptyForAnAgentWithoutHostSetup(t *testing.T)
func TestHostSourceHashChangesWithContent(t *testing.T)
```

- [ ] **Step 2: Run to verify it fails** — `go test ./internal/firstrun/ -run 'TestOffer|TestHostFiles'`. Expected: FAIL.

- [ ] **Step 3: Implement** `Offer` (the three-part predicate) and `HostFiles` (evaluate the agent's provision rules against `hostHome`, collect the host-side paths, hash paths+contents). `HostFiles` returns empty + `""` for an agent that declares no host config.

- [ ] **Step 4: Run** — `go test ./internal/firstrun/`. Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/firstrun/
git commit -m "feat(firstrun): decide and probe the first-run menu"
```

### Task 4: The menu and the learn hand-off (UC1)

**Files:**
- Create: `internal/firstrun/prompt.go`, `internal/firstrun/prompt_test.go`
- Modify: `cmd/agents-sandbox/commands_cli.go` (the `run`/`shell` path), `cmd/agents-sandbox/cli_run_options_test.go`

**Interfaces:**
- Consumes: `Facts`, `Offer`; `termio.Prompter.Select`, `termio.Prompter.IsInteractive`; `state.RecordFirstRun`, `state.FirstRunLearned`.
- Produces:

```go
// Outcome is the resolved first-run choice applied to the run options.
type Outcome struct {
    Choice              Choice
    Learn               bool // the session starts in learn mode (UC1/UC4)
    ProvisionHostConfig bool // "Configure it myself" set the unsafe opt-in
}
// Prompt asks the first-run question and returns the outcome. offered is false
// when the run is non-interactive or Offer(f) is false; a non-interactive run
// warns and returns the zero Outcome.
func Prompt(ui termio.UI, f Facts) (out Outcome, offered bool)
// Apply sets the outcome's effect on the run options and records the decision.
func (o Outcome) Apply(opts *options.RunOptions, slug, agentName string) error
```

- [ ] **Step 1: Write the failing tests**

```go
func TestPromptLearnByUsingSetsLearn(t *testing.T)         // "learn-by-using" -> Outcome.Learn true, no host seed
func TestPromptOfferedFalseWhenNotInteractive(t *testing.T) // non-interactive -> zero Outcome, no Select
func TestPromptOfferedFalseWhenManaged(t *testing.T)       // Offer false -> no Select
func TestPromptTwoChoicesWithoutHostConfig(t *testing.T)   // no host files -> "learn-from-host" absent
func TestOutcomeApplyRecordsLearned(t *testing.T)          // Apply -> opts.Learn true and state learns choice
// cli_run_options_test.go
func TestRunFirstRunMenuSetsLearn(t *testing.T)            // a stubbed menu -> session.Run sees opts.Learn
func TestRunFirstRunMenuSkippedNonInteractive(t *testing.T)
```

- [ ] **Step 2: Run to verify it fails** — `go test ./internal/firstrun/ ./cmd/agents-sandbox/ -run 'TestPrompt|TestOutcomeApply|TestRunFirstRunMenu'`. Expected: FAIL.

- [ ] **Step 3: Implement** the consent sentence + the `Select` menu (`Learn by using it` / `Learn from my host setup` when `HostFiles` is non-empty / `Configure it myself`), the non-interactive warn-and-continue, and `Apply` (set `opts.Learn`; record `state.FirstRunLearned`). For `ChoiceLearnFromHost`, call Task 5's `SeedFromHost` before setting `opts.Learn`. Wire `Prompt` into the `run`/`shell` path before `session.Run`, behind a `firstRunMenu` seam so the CLI test drives it without a host config.

- [ ] **Step 4: Run** — `go test ./internal/firstrun/ ./cmd/agents-sandbox/`. Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/firstrun/ cmd/agents-sandbox/
git commit -m "feat(firstrun): offer the first-run choices and start a learning session"
```

### Task 5: The UC4 host-config seed

**Files:**
- Create: `internal/firstrun/seed.go`, `internal/firstrun/seed_test.go`
- Modify: `internal/sandbox/readback/read.go` (export the shared classification walker), `internal/sandbox/readback/read_test.go`

**Interfaces:**
- Consumes: `agent.Provisioner.ProvisionRules`, `reprovision`'s managed-config target (`<per-slug user scope>/<agent config-dir>/<file>`) and `agent.ConfigDirNames`; `envsecret.WriteSecretSpec`, `envsecret.SecretSpec`; `readback.Credential`, `readback.CredentialStatic`/`CredentialLogin`.
- Produces:

```go
// SeedFromHost writes the host config into the per-slug user scope: each static
// credential value becomes a placeholder in the copied config and its value goes
// to env.secret.yaml; each login entry is dropped. It reads every value in
// memory only. It returns the number of static secrets migrated.
func SeedFromHost(f Facts, paths configpaths.ConfigPaths, hostHome string) (int, error)

// internal/sandbox/readback — exported for the seed and shared with the
// inline walker (no value is returned; the caller re-reads to placeholder).
// ClassifyConfigContent splits a config file's credential-bearing entries into
// static (provider/host/env-var) and login classifications.
func ClassifyConfigContent(path string, content []byte, agentName string) (static, login []Credential, err error)
```

- [ ] **Step 1: Write the failing tests**

```go
func TestSeedFromHostPlaceholdersStaticSecret(t *testing.T) // a host config with an api_key -> placeholder in the copy, value in env.secret.yaml, never the raw value
func TestSeedFromHostDropsLoginEntry(t *testing.T)          // an oauth entry -> absent from the copy, no secret written
func TestSeedFromHostDerivesHostFromTheEntry(t *testing.T)  // a custom baseURL -> that host, no curated host
func TestSeedFromHostWritesOnlyThePerSlugScope(t *testing.T) // no project-scope write
func TestClassifyConfigContentNeverReturnsAValue(t *testing.T)
```

- [ ] **Step 2: Run to verify it fails** — `go test ./internal/firstrun/ ./internal/sandbox/readback/ -run 'TestSeedFromHost|TestClassifyConfigContent'`. Expected: FAIL.

- [ ] **Step 3: Implement** `ClassifyConfigContent` by lifting the read-back's existing classification walk (the S11 inline/dotenv split) into a shared function, then `SeedFromHost`: read each `HostFiles` entry, classify, write the placeholdered copy to the per-slug managed config dir and the static values to the per-slug `env.secret.yaml` through `envsecret.WriteSecretSpec`, and write nothing for a login. A file the classification cannot read is reported and left unseeded, never copied raw.

- [ ] **Step 4: Run** — `go test ./internal/firstrun/ ./internal/sandbox/readback/`. Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/firstrun/ internal/sandbox/readback/
git commit -m "feat(firstrun): seed a learning session from the host config"
```

### Task 6: The manual path

**Files:**
- Modify: `internal/firstrun/prompt.go` (the manual branch), `internal/firstrun/prompt_test.go`
- Modify: `cmd/agents-sandbox/commands_cli.go`

**Interfaces:**
- Consumes: `Facts`, `termio.Prompter.Confirm`.
- Produces: `ChoiceManual` sets `Outcome.ProvisionHostConfig` after the warning, and records `state.FirstRunManual`.

- [ ] **Step 1: Write the failing test**

```go
func TestPromptManualWarnsBeforeProvisioning(t *testing.T) // "configure-it-myself" -> Confirm; yes -> Outcome.ProvisionHostConfig true
func TestPromptManualDeclinedLeavesProvisioningOff(t *testing.T) // Confirm no -> ProvisionHostConfig false, still manual
func TestOutcomeApplyRecordsManual(t *testing.T)
```

- [ ] **Step 2: Run to verify it fails** — `go test ./internal/firstrun/ -run 'TestPromptManual|TestOutcomeApplyRecordsManual'`. Expected: FAIL.

- [ ] **Step 3: Implement** the warning (`"Provisioning copies your host agent config, including credentials, into the sandbox. Continue?"`), set `Outcome.ProvisionHostConfig` on confirm, and record `state.FirstRunManual`. `Apply` sets `opts.ProvisionHostConfig` accordingly.

- [ ] **Step 4: Run** — `go test ./internal/firstrun/ ./cmd/agents-sandbox/`. Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/firstrun/ cmd/agents-sandbox/
git commit -m "feat(firstrun): make the manual path carry the provisioning warning"
```

### Task 7: Docs, changelog, the brain, and the superseded-issue marks

**Files:**
- Create: `changelog.d/<PR>.changed.md` (the default flip) and, if the PR splits, `changelog.d/<PR>.added.md` (first-run)
- Modify: `docs/learn-your-setup.md` (add the first-run section: the three choices and the consent sentence), `docs/manage-config.md` (host config is opt-in), `docs/quick start`/`tutorial.md` if they name the default
- Modify: `.operator-shared/specs/onboarding/onboarding.md` (re-settle the first-run section: the learn hand-off is real; the tiered wizard and the curated `config migrate` table are superseded; the default is false; the decision record lives in `RootState`), `.operator-shared/specs/onboarding/learn-mode.md` (mark S9 landed in the phasing), `.operator-shared/specs/architecture/termio-ui.md` (the prompt backend is #119's, reimplemented; the menu is backend-agnostic), `.operator-shared/index/index.md` (the `internal/firstrun` package)
- Modify: `.operator-shared/plans/2026-11-17-learn-mode-program.md` (flip S9 to written/landed; record the #103/#119 spikes and the retirement)
- Modify: `.operator-shared/plans/2026-11-17-learn-mode-firstrun.md` (delete this plan once the work lands — program §Enablers §Retirement)

- [ ] **Step 1:** Write the changelog fragment(s); the default flip is a `changed` entry naming the opt-in.
- [ ] **Step 2:** Update the user docs per `STYLEGUIDE.md` and the linking rules in `AGENTS.md`; the first-run page states the three choices and which one is recommended.
- [ ] **Step 3:** Update the specs, the index, and the program.
- [ ] **Step 4: Mark the superseded issues for closure** (a comment naming the delivering PR, then close):

  - **#118** (`config harden`) — delivered by PR #757 / S8.
  - **#54** (first-run wizard) — delivered by this subplan.
  - **#99** and its references **#97**/**#98** (safe migration, secure default) — delivered by this subplan; the curated-table `config migrate` is retired by learn mode.
  - **#20** stays the open UX umbrella; **#119** (the stale `huh`-backend PR) is closed as a superseded spike, to be reimplemented against `specs/architecture/termio-ui.md`.

- [ ] **Step 5: Verify** — `make check && make brain-check`. Expected: PASS.
- [ ] **Step 6: Commit**

```bash
git add changelog.d/ docs/ .operator-shared/
git commit -m "docs(operator): record first-run learn mode and retire the superseded onboarding"
```

### Task 8: Host accept (human, not an agent)

- [ ] On a KVM host, from a clean `~/.config/agents-sandbox/` with an existing host agent config: `make build`, then `./agents-sandbox run` in a scratch project. Confirm the menu appears with **Learn from my host setup** recommended, and that choosing it seeds the per-slug scope with placeholders (no raw value in the sandbox home), then starts a learning session whose after-run proposes the managed config.
- [ ] With the host config removed, confirm the menu has two choices and **Learn by using it** is recommended (UC1), and that exiting the session runs the S7 after-run.
- [ ] Confirm **Configure it myself** warns before enabling host-config provisioning, and that declining leaves it off.
- [ ] Confirm a second run offers nothing, and that editing the host agent config re-arms the menu once.

The subplan is not done until these are checked; they cannot run in the dev VM (`/dev/kvm` absent).

---

## Self-review

**Coverage.** The three choices and the consent sentence are Task 4 (hub §User-facing vocabulary); the learn hand-off (UC1) is Task 4; the seeded learn hand-off (UC4) is Task 5; the unsafe opt-in and its warning is Task 6; the decision record and its re-arm is Tasks 2–3; the secure default is Task 1; the non-interactive warn-and-continue is Tasks 3–4 (invariant 6); "no curated knowledge" is Task 5 (host from the entry, not a table); "the credential store is never copied" is Task 5 (logins dropped); docs/changelog/brain/issue marks are Task 7; the host-only acceptance is Task 8 (E3/#252's seed). The **tiered `config wizard`** and the **curated `config migrate` table** are out of scope (Design decisions 1, 8) and are marked for closure in Task 7; the **`huh` backend** is out (Design decision 2).

**Proportion.** Signatures, test names, and assertions; the only new persisted model is one `RootState` map entry.

**Type consistency.** `state.FirstRunChoice`/`FirstRunRecord`/`RecordFirstRun`/`ReadFirstRun` (Task 2); `firstrun.{Choice,Facts,Offer,HostFiles}` (Task 3); `firstrun.{Outcome,Prompt}` and `Outcome.Apply` (Tasks 4, 6); `readback.ClassifyConfigContent` and `firstrun.SeedFromHost` (Task 5). Every name is defined once and consumed under the same spelling.

**Open item carried forward.** The `readback` classification lift (Task 5) touches S11's walker; if the S11 code keeps the split unexported, the seed's exact seam is the reviewer's call — the contract is that no value crosses it.
