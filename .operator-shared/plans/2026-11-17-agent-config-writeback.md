# S10 — Agent-config write-back

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. A fresh implementer per task; a fresh reviewer gates each task; one whole-subagent review at the end. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Complete the learn-mode proposal with its "agent config" half: from a finished session's changed agent config files, write the agent config the user created in the sandbox into the per-slug user scope's agent config directory as a managed snippet, with each static secret value replaced by a runtime placeholder whose secret entry is submitted through `env.secret.yaml` — so the next run reprovisions it and the placeholder substitutes in flight.

**Architecture:** The read-back (S5) names the changed config files and the static credentials; this subplan sanitizes their content and proposes the managed file through the analyser's finding model (a new `ManagedConfig` write), so the after-run (S7) applies it beside the network and secret findings. The placeholder convention is made explicit: the managed config carries `${<ENVVAR>}` literally, and the secret entry is given that same placeholder, so the runtime substitutes it for the allowed hosts. The finder lives in `analyser`; the applier writes the file into the scope's agent config dir; `learn.Analysis.Findings` appends the result.

**Tech Stack:** Go 1.27, `internal/sandbox/{analyser,readback,envsecret,learn}`, `internal/{configpaths,agent,termio}`.

**Spec:** `.operator-shared/specs/onboarding/learn-mode.md` (UC1's "config back-copy", invariants 1/2/3/5); `.operator-shared/specs/onboarding/learn-mode/findings-and-apply.md` (§The credential store, §Scopes); `.operator-shared/specs/onboarding/learn-mode/home-read-back.md` (§What read-back establishes — facts 1–2); `.operator-shared/specs/onboarding/onboarding.md` (§Credential kinds, §Host determination). The frozen contracts are in `2026-11-17-learn-mode-program.md`.

**Depends on:** S5 (read-back), S6 (the finding model/applier), **S7** (the after-run that produces and applies findings). This is why S10 is not fully parallel to S7 — its last task extends `learn.Analysis`, which S7 creates.

**Produces:** `envsecret.PlaceholderFor`, `envsecret.SecretSpec.Placeholder`; `analyser.ManagedConfigWrite`, `analyser.Write.ManagedConfig`, `analyser.ApplyOptions.Agent`; `analyser.ManagedConfigFacts`, `analyser.ManagedConfigFindings`; the placeholder on every applied static secret.

## Global Constraints

See `2026-11-17-learn-mode-program.md` → Global Constraints. Specifically:

- **The credential store is never copied out of the sandbox** (invariant 2). The write-back targets only files the agent *authored as configuration*; it never copies a login credential store or a credential probe's database, and never adds a `home:`/provision rule for one.
- **No value in a finding, artefact, or log** (invariant 3). The sanitized content holds the placeholder, never the value; the value crosses in memory from `readback.Credential.Value` to the substitution and is discarded.
- **No widening, no curated knowledge** (invariants 4/5). The write-back reads the session's own files; it names no provider table.
- `make fmt`, `make lint`, `make check` — never `go fmt`/`go vet` by hand.
- The placeholder's survival through a real agent turn is a **human accept** step (a KVM host).

## Design decisions (calls made; correct if wrong)

1. **The managed layer is a scope's agent config directory.** `reprovision` merges the snippets under `ConfigScope.AgentConfigDir(<config-dir-name>)` and copies mirror files verbatim; writing the sanitized config there is what makes the launcher own it. The write target is the per-slug user scope's agent config dir (trusted, needs no gate).
2. **The placeholder convention is `${<ENVVAR>}` — where the harness passes a config value through verbatim.** The runtime's default placeholder is generated (not referenceable from a config file), so the managed config must carry an explicit one (`msb.SecretEntry.Placeholder`, already projected). `envsecret.PlaceholderFor(envVar) = "${" + envVar + "}"`: a literal JSON/YAML string is not shell-expanded, and if an agent happens to expand `${VAR}` it reads the guest env, which holds the placeholder, so the request carries the placeholder either way. **A harness that does not expand `${VAR}` in its config needs the per-harness escape, and kimi is one (#749):** it reads no key from the process environment and accepts only `api_key_env = "<ENVVAR>"` (`api_key = "${VAR}"` would send the literal). The write-back therefore emits the harness's **native env indirection** where it has one: replace `api_key = "<value>"` with `api_key_env = "<ENVVAR>"` and let msb put the placeholder into that env var. The escape is a property of the write-back (which form the target harness reads), and every other harness keeps `${<ENVVAR>}`. **Task 6 confirms the form per harness.**
3. **Only changed, non-credential files are written back.** The candidates are `readback.ReadBack.ConfigPaths` ∩ `Changed`, minus the login credential-store sources and the `<agent>:<probe>` sources. A file over `readback`'s size cap is reported, not written.
4. **The secret entries are S6's findings, made referenceable.** S10 does not emit secret findings; it makes every applied static secret carry `PlaceholderFor(envVar)` (a change to the applier's secret path), so the managed config's `${ENVVAR}` matches the entry.
5. **The write-back is a finding, accepted like any other.** It appears in the after-run's exit prompt and its writes are applied through `analyser.Apply`, so invariant 1 holds and a decline is remembered by evidence hash.
6. **No `home:` or provision rule is added.** Placing the file in the agent config dir is the existing managed-config mechanism; a `home:` mapping is not.

## Review Focus

1. **A credential store or login entry copied out** — the write-back must skip every `readback.Login` source and every probe source, and never write a file whose content is a credential store. (Task 3.)
2. **A raw secret value in the managed config** — every occurrence of a static credential's value must be replaced before the file is written; the value must not reach the `Finding`, the artefact, or a log. (Task 3.)
3. **A placeholder that does not match its secret entry** — the `${ENVVAR}` in the file and the applied entry's `Placeholder` must be the same string. (Tasks 1, 4.)
4. **A missing expansion of `${VAR}` by a harness** — the host accept confirms the request carries the placeholder; until then the convention is unverified. (Task 6.)
5. **A changed file the read-back could not read** — over-cap or unreadable files must be reported, not written truncated or empty. (Task 3.)
6. **Re-running the session** — the write-back is idempotent (same content → same file); a decline is remembered. (Tasks 2, 3.)

---

### Task 1: The placeholder plumbing

**Files:**
- Modify: `internal/sandbox/envsecret/secrets.go` (the `SecretSpec` type, `BuildSecretsFromSpecs`), `internal/sandbox/envsecret/secrets_test.go`, `internal/sandbox/envsecret/write.go` (`secretSpecValue`), `write_test.go`

**Interfaces:**
- Consumes: `msb.Secret.Env` (`msb.SecretEnvOptions.Placeholder`), `write.go`'s YAML round-trip.
- Produces:

```go
// PlaceholderFor returns the guest-visible placeholder a managed config uses to
// reference envVar. It is the explicit placeholder the runtime substitutes, so
// a config file can carry it verbatim.
func PlaceholderFor(envVar string) string // "${" + envVar + "}"
// SecretSpec gains: Placeholder string `yaml:"placeholder,omitempty"`
// BuildSecretsFromSpecs passes spec.Placeholder through to msb.Secret.Env.
```

- [ ] **Step 1: Write the failing tests**

```go
func TestPlaceholderFor(t *testing.T)                       // PlaceholderFor("OPENAI_API_KEY") == "${OPENAI_API_KEY}"
func TestBuildSecretsFromSpecsCarriesPlaceholder(t *testing.T) // a spec with a Placeholder -> the built entry's Placeholder matches
func TestWriteSecretSpecRoundTripsPlaceholder(t *testing.T)  // write then parse -> the placeholder survives
```

- [ ] **Step 2: Run to verify it fails** — `go test ./internal/sandbox/envsecret/ -run 'TestPlaceholderFor|TestBuildSecretsFromSpecsCarriesPlaceholder'`. Expected: FAIL.

- [ ] **Step 3: Implement** `PlaceholderFor`, the `SecretSpec.Placeholder` field, its pass-through in `BuildSecretsFromSpecs` (`Placeholder: spec.Placeholder`), and its YAML round-trip in `secretSpecValue`.

- [ ] **Step 4: Run** — `go test ./internal/sandbox/envsecret/`. Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/envsecret/
git commit -m "feat(envsecret): carry an explicit secret placeholder"
```

---

### Task 2: The managed-config write

**Files:**
- Modify: `internal/sandbox/analyser/finding.go`, `internal/sandbox/analyser/apply.go`, `internal/sandbox/analyser/apply_test.go`

**Interfaces:**
- Consumes: `configpaths.ConfigPaths`, `configpaths.ConfigScope.AgentConfigDir`, `agent.ConfigDirNames`.
- Produces:

```go
// ManagedConfigWrite writes one host-side managed agent config file into the
// scope's agent config directory. Content is the file's bytes with each static
// secret value replaced by its placeholder; it carries no value.
type ManagedConfigWrite struct { Name string; Content []byte }
// Write gains: ManagedConfig *ManagedConfigWrite
// ApplyOptions gains: Agent string // the agent whose config-dir name the write targets
```

  `planWrites`/`planTarget` gain a `ManagedConfig != nil` branch: the target dir is the scope's `AgentConfigDir(<primary config-dir name>)`; `executeWrite` writes `Content` atomically (mode `0o644`), creating the directory.

- [ ] **Step 1: Write the failing tests**

```go
func TestApplyManagedConfigWritesTheFile(t *testing.T)      // a per-slug finding -> <UserSlugConfigDir>/<agentdir>/<Name> holds Content
func TestApplyManagedConfigCreatesTheDir(t *testing.T)      // a missing agent config dir is created
func TestApplyManagedConfigProjectScopeIsGated(t *testing.T) // ScopeProject -> the trust gate
```

- [ ] **Step 2: Run to verify it fails** — `go test ./internal/sandbox/analyser/ -run TestApplyManagedConfig`.

- [ ] **Step 3: Implement** the type, the field, `ApplyOptions.Agent`, and the plan/execute branch (atomic write, `0o644`).

- [ ] **Step 4: Run** — `go test ./internal/sandbox/analyser/`. Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/analyser/
git commit -m "feat(analyser): add the managed-config write"
```

---

### Task 3: The write-back finder

**Files:**
- Create: `internal/sandbox/analyser/managed_config.go`, `internal/sandbox/analyser/managed_config_test.go`

**Interfaces:**
- Consumes: `readback.ReadBack`, `readback.Credential`, `agent.ConfigDirNames`, the home-volume host path.
- Produces:

```go
// ManagedConfigFacts is a finished session's home and its read-back.
type ManagedConfigFacts struct {
    Paths    configpaths.ConfigPaths
    Slug     string
    Home     string          // home-volume host path
    Agent    string
    ReadBack readback.ReadBack
}
// ManagedConfigFindings returns one finding per changed, non-credential agent
// config file, whose single write is the sanitized managed config. The static
// credentials' values are replaced by PlaceholderFor(EnvVar) in the content and
// never appear in the finding.
func ManagedConfigFindings(f ManagedConfigFacts) []Finding
```

- [ ] **Step 1: Write the failing tests**

```go
func TestManagedConfigReplacesStaticSecretValues(t *testing.T) // a changed jsonc carrying a credential value -> the content holds ${ENVVAR}, not the value
func TestManagedConfigSkipsLoginStores(t *testing.T)          // a changed login/credential source -> no finding
func TestManagedConfigSkipsUnreadable(t *testing.T)           // an over-cap or missing file -> reported (returns no finding for it)
func TestManagedConfigOnlyChangedFiles(t *testing.T)          // a config path not in Changed -> no finding
func TestManagedConfigNeverCarriesAValue(t *testing.T)        // no field of the finding holds the raw value
func TestManagedConfigUsesNativeEnvIndirection(t *testing.T)  // kimi: api_key = "<value>" -> api_key_env = "<ENVVAR>" (not a ${VAR} literal) (#749)
```

- [ ] **Step 2: Run to verify it fails** — `go test ./internal/sandbox/analyser/ -run TestManagedConfig`.

- [ ] **Step 3: Implement** the candidate selection (`ConfigPaths` ∩ `Changed`, minus login/probe sources), the bounded read, the value→placeholder substitution (every occurrence of each `CredentialStatic.Value`), the `ManagedConfig` write, and `withEvidenceHash`. The substitution has two forms (Design decision 2): the default replaces the value with `${<ENVVAR>}`; a harness that does not expand `${VAR}` replaces its credential key with the harness's **native env-indirection key** carrying the env-var name (kimi's `api_key` → `api_key_env = "<ENVVAR>"`). Resolve the form from the agent through a small capability the profile implements (the name is the implementer's call; it returns the indirection key, e.g. `"api_key_env"`, or nothing for the default form). The kimi profile (PR #752) implements it when that profile lands. Report skipped candidates through `Findings`' caller (a returned `[]string` or the logger — pick one and pin it in the test).

- [ ] **Step 4: Run** — `go test ./internal/sandbox/analyser/`. Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/analyser/
git commit -m "feat(analyser): derive the managed agent config from a session"
```

---

### Task 4: Wire the write-back into the after-run

**Files:**
- Modify: `internal/sandbox/analyser/apply.go` (`executeSecret` sets the placeholder), `apply_test.go`
- Modify: `internal/sandbox/learn/analyse.go` (`Analysis.Findings`), `learn/analyse_test.go`

**Interfaces:**
- Consumes: `analyser.ManagedConfigFindings`, `envsecret.PlaceholderFor`, the `learn.Analysis` from S7.
- Produces: `Analysis.Findings` appends `ManagedConfigFindings` for the session's read-back; every applied static secret carries `PlaceholderFor(EnvVar)`.

- [ ] **Step 1: Write the failing tests**

```go
func TestApplySecretSetsThePlaceholder(t *testing.T)       // an applied static secret's env.secret.yaml entry carries the placeholder
func TestAnalysisFindingsIncludeManagedConfig(t *testing.T) // a changed config file -> a managed-config finding appended
```

- [ ] **Step 2: Run to verify it fails** — `go test ./internal/sandbox/analyser/ ./internal/sandbox/learn/ -run 'TestApplySecretSetsThePlaceholder|TestAnalysisFindingsIncludeManagedConfig'`.

- [ ] **Step 3: Implement** `executeSecret`'s `Placeholder: envsecret.PlaceholderFor(secret.EnvVar)` and `Analysis.Findings`'s append. `learn.Analysis` already carries `Home`, `Agent`, `Paths`, `Slug`, and `ReadBack`, so no new field is needed.

- [ ] **Step 4: Run** — `go test ./internal/sandbox/analyser/ ./internal/sandbox/learn/`. Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/analyser/ internal/sandbox/learn/
git commit -m "feat(learn): propose the managed agent config in the after-run"
```

---

### Task 5: Docs, changelog, and the brain

**Files:**
- Create: `changelog.d/<PR>.added.md`
- Modify: `.operator-shared/specs/onboarding/learn-mode/findings-and-apply.md` (§The agent-config write-back exists from #749; extend it with the managed-config write, the credential-store exclusion, and the per-harness native form if the implementation named the capability differently), `.operator-shared/specs/onboarding/learn-mode/home-read-back.md` (the changed-config consumer), `.operator-shared/specs/onboarding/learn-mode.md` if the hub's UC1 row needs its "config back-copy" named, `.operator-shared/index/sandbox.md`, `.operator-shared/catalog.md`, `.operator-shared/plans/2026-11-17-learn-mode-program.md` (flip S10 to landed on merge; S9's dependency now includes S10)
- Modify: `docs/` (the learn-mode page: the exit prompt's `opencode.jsonc created` line)

- [ ] **Step 1:** Write the changelog fragment.
- [ ] **Step 2:** Update the specs, indexes, and docs (`STYLEGUIDE.md`, the `AGENTS.md` linking rules). State the placeholder convention where a future reader will find it.
- [ ] **Step 3: Verify** — `make check && make brain-check`. Expected: PASS.
- [ ] **Step 4: Commit**

```bash
git add changelog.d/ docs/ .operator-shared/
git commit -m "docs(operator): record the agent-config write-back and its placeholder"
```

---

### Task 6: Host accept (human, not an agent)

- [ ] On a KVM host: run a learning session (`run --learn`), let a harness write its config with a pasted API key, and confirm the after-run proposes the managed config with `${<ENVVAR>}` in place of the key and the key only in `env.secret.yaml`.
- [ ] Re-run normally and confirm the agent reaches its provider through the placeholder (the request carries the placeholder; the runtime substitutes for the allowed host) for opencode/opencode2 at least, and for kimi through its native `api_key_env` form (#749). Record which harnesses transform or reject `${VAR}`.
- [ ] Confirm no credential-store file (a login store, a credential DB) was copied host-side.

The subplan is not done until these are checked; they cannot run in the dev VM (`/dev/kvm` absent). If a harness transforms `$VAR`, record it and adjust the convention (Design decision 2) before closing.

---

## Self-review

**Coverage.** UC1's "config back-copy" is Tasks 2–4; the credential-store invariant is Task 3 + Design decision 3; the value-never-persisted invariant is Tasks 1/3; static-secret attribution reuses S6's secret findings made referenceable (Task 4); the placeholder contract is Task 1 + Design decision 2 + Task 6; docs are Task 5. The **network** and **secret** halves of the proposal are S6/S7's and unchanged.

**Proportion.** Signatures, test names, and assertions; the only new model shapes are one write kind and one secret field.

**Type consistency.** `envsecret.PlaceholderFor`/`SecretSpec.Placeholder` (Task 1); `analyser.ManagedConfigWrite`/`Write.ManagedConfig`/`ApplyOptions.Agent` (Task 2); `analyser.ManagedConfigFacts`/`ManagedConfigFindings` (Task 3); `Analysis.Findings` (Task 4). The placeholder string is produced only by `envsecret.PlaceholderFor` in both the config content (Task 3) and the secret entry (Task 4), so the two cannot drift.
