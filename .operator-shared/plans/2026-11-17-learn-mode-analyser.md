# S6 — The analyser and applier

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking. A fresh implementer per task; a fresh reviewer gates each task; one whole-subplan review at the end.

**Goal:** Turn the facts the read-back (and, later, the observation channel) established into **findings** — evidence, one or more proposed writes each naming its scope — and apply them interactively, per finding, to the per-slug user config, remembering declines; and provide the scoped write/removal primitives `config harden` (S8) will reuse.

**Architecture:** A new `internal/sandbox/analyser` package is a pure "facts → findings" engine plus an applier. It consumes `internal/sandbox/readback` (facts 1–3), `internal/sandbox/channel` (facts 4–7, when S4 lands), and the resolved `viperconfig.Config`/`network.Policy`. It writes through `viperconfig.WriteSetting` (per-slug default) or the project-trust-approved path (project scope), writes/removes static secrets through a new `env.secret.yaml` writer/remover, and records declines in a slug-keyed `state.RootState` section. The learn-mode **proposal** emits per-slug findings only; the applier supports the project scope and removal because **S8's `config harden`** reuses it to move a secret out of the committed project config.

**Tech Stack:** Go 1.27, `internal/sandbox/{analyser,readback,channel,state,network}`, `internal/{viperconfig,configpaths,envsecret,projecttrust,termio,runoptions}`.

**Spec:** `.operator-shared/specs/onboarding/learn-mode/findings-and-apply.md` (the finding shape, scopes, credential-store rule, attribution, the network diff, applying, after-run ownership, `config harden`); hub `.operator-shared/specs/onboarding/learn-mode.md` (facts, invariants 1/2/4/5/6); `.operator-shared/specs/configuration/config-scopes.md` (the `generic user < per-slug user < project` precedence) and `config-keys.md`.

**Consumes:** `readback.{ReadBack,Credential,CredentialKind,Manifest,TakeManifest,HomeVolumePath}` and `agent.AsReadBackDescriber` (S5, incl. `Credential.Hosts` this plan fills); `channel.Observation` (S4 — the frozen contract; the observation finder's tests construct one, so S6 does not depend on S4's runtime).
**Produces (for S7/S8 and the program index):** `analyser.{Scope,Kind,Evidence,Write,SecretWrite,Finding,Outcome,ApplyResult,SessionFacts}`, `analyser.ObservationFindings`, `analyser.Apply`; `envsecret.WriteSecretSpec`/`RemoveSecretSpec`; `viperconfig.RemoveSetting`; `state.RootState.LearnDeclines` + `state.RecordLearnDecline`/`state.LearnDeclined` (schema bump).

## Global Constraints

See `2026-11-17-learn-mode-program.md` → Global Constraints. Specifically:
- **Declines and dry-run write nothing.** Non-interactive prints one line naming the producing command; a dry run records nothing and proposes nothing (invariant 6).
- **No widening** (invariant 4): the learn-mode proposal never removes a user-set entry or broadens an existing policy. Removal is the *harden* contract (S8), not the proposal's.
- **No curated knowledge in the learned path** (invariant 5): every proposal derives from the read-back or the channel, never a provider table.
- **The credential store is never owned by the managed layer** (invariant 2): static secrets go to `env.secret.yaml`; login credentials are never copied; the applier never adds a `home:`/provision rule for a credential store.
- No value ever reaches a manifest, a finding, a decline record, or a log: only `readback.Credential.Value` carries a static value, in memory, threaded straight to the writer.
- `make fmt`, `make lint`, `make check` — never `go fmt`/`go vet` by hand.

## Design decisions (calls made; correct if wrong)

1. **Precedence decides the shadowing rule.** Config precedence is `generic user < per-slug user < project` (`config-scopes.md:28`; `LayerSettingOwner`, `setting_writer.go:36`). A per-slug write for a key the **project** layer also sets is shadowed and has no effect. The applier therefore consults `LayerSettingOwner`: the proposal writes per-slug, and when the project layer owns the key it **reports and skips** (`OutcomeSkipped`, a note) rather than writing a line that does nothing.
2. **Scope is per write, not per finding.** A write names its own `Scope`. A finding carries an **ordered set of writes**, so a *move* — remove a secret from the project scope, add it to the per-slug scope — is one finding with two writes sharing one piece of evidence.
3. **The learn-mode proposal emits per-slug only.** `ObservationFindings` never sets `ScopeProject` and never sets a removal. The applier supports both scopes and removal because S8's harden reuses it.
4. **The applier's project path is gated** via `projecttrust.ApproveSettingWrite` + `WriteSetting` (the same path `resourceproposal.PersistSetting` uses).
5. **The decline record** is a new slug-keyed `RootState.LearnDeclines` section (one document, no new file), so `RootStateSchema` bumps `2 → 3`.
6. **`EvidenceHash`** is a sha256 over a canonical rendering of `Evidence` (sorted lists) — stable across runs, changed by any evidence change.
7. **`Credential.Hosts`** is populated in read-back (Task 2) from the endpoints the config entry names, so the spec's config-entry attribution fallback has a source; channel attribution, when present, supersedes it.
8. **S6 ships no CLI surface and no user-visible docs.** `config harden` (S8) and `run --learn` (S7) own those; S6 updates only the spec/index where a settled detail belongs. The **config-only (harden) finder is S8's**; S6 keeps the `KindConfig` value in the model so S8 can use it.

## Review Focus

1. **A finding about a destination the session never used, or one that widens the policy** — must not be produced; the network diff is observed ⊆ proposed and never drops a user-set entry. (Task 3.)
2. **A per-slug write for a key the project layer owns** — must be reported and skipped, never written as a shadowed no-op. (Tasks 3, 7.)
3. **A static secret with no derivable host** — must not migrate (and must not acquire `allow_any_host_dangerous`); it stays in the sandbox home. (Tasks 3, 7.)
4. **A login credential** — must never be read or written; only static credentials are migrated. (Tasks 2, 3.)
5. **A dry run / non-interactive terminal / `--yes`** — must write nothing, record nothing, and never hang. (Task 7.)
6. **A per-slug config that is JSON/JSON5** — `WriteSetting` returns `ErrUnsupportedConfigFormat`; the applier must skip with a reported outcome, not crash or write a stray file. (Task 7.)
7. **A declined finding whose evidence is unchanged** — must be skipped silently; once it changes it re-arms. (Tasks 6, 7.)
8. **A move whose project removal fails** — the whole move must fail (the per-slug addition must not run), so the secret is never duplicated. (Task 7.)

---

### Task 1: The finding model

**Files:**
- Create: `internal/sandbox/analyser/finding.go`, `internal/sandbox/analyser/finding_test.go`

**Interfaces:**
- Consumes: nothing new.
- Produces:

```go
type Scope string
const (ScopePerSlugUser Scope = "per-slug-user"; ScopeProject Scope = "project")
type Kind string
const (KindConfig Kind = "config"; KindObservation Kind = "observation")

// Evidence is the observed basis for a finding; destinations, counts, and
// credential-bearing header NAMES — never a value (hub invariant 3).
type Evidence struct {
    Summary      string
    Destinations []string
    Denied       []string
    Count        int
    Succeeded    bool
    Channel      string   // "readback" | "proxy"
    HeaderFields []string // credential-bearing header names, never values
}
// Write is one proposed edit, in its own scope. Remove true deletes ConfigKey from
// that scope's launcher config; Secret targets env.secret.yaml instead.
type Write struct {
    Scope       Scope
    ConfigKey   string
    ConfigValue any          // ignored when Remove
    Remove      bool
    Secret      *SecretWrite // nil unless a static secret is proposed
    Text        string
}
// SecretWrite carries no Value field — the value is threaded in memory from
// readback.Credential.Value to the writer and never persisted in a finding.
type SecretWrite struct { EnvVar, Provider string; Hosts, HeaderFields []string; Remove bool }

type Finding struct {
    Evidence     Evidence
    Writes       []Write // applied together; a move is two writes (remove, add)
    Kind         Kind
    EvidenceHash string   // keys the decline memory; re-armed when the evidence changes
}
type Outcome string
const (OutcomeApplied Outcome = "applied"; OutcomeDeclined Outcome = "declined"; OutcomeSkipped Outcome = "skipped"; OutcomeFailed Outcome = "failed"; OutcomeNotApplicable Outcome = "not-applicable")
type ApplyResult struct { Finding Finding; Outcome Outcome; WroteDirs []string; Note, Err string }

// evidenceHash is a stable digest of the evidence alone.
func (e Evidence) hash() string
func (f Finding) withEvidenceHash() Finding
```

- [ ] **Step 1: Write the failing test**

```go
func TestEvidenceHashStableAndEvidenceSensitive(t *testing.T) {
    a := Evidence{Summary: "used api.example", Destinations: []string{"api.example", "cdn.example"}, Count: 3}
    b := Evidence{Summary: "used api.example", Destinations: []string{"cdn.example", "api.example"}, Count: 3}
    if a.hash() != b.hash() { t.Fatal("list order must not change the hash") }
    c := a; c.Count = 4
    if a.hash() == c.hash() { t.Fatal("a changed count must change the hash") }
}
func TestMoveIsOneFindingTwoWrites(t *testing.T) {
    f := Finding{Kind: KindConfig, Evidence: Evidence{Summary: "secret in the project config"},
        Writes: []Write{
            {Scope: ScopeProject, Secret: &SecretWrite{EnvVar: "OPENAI_API_KEY", Remove: true}},
            {Scope: ScopePerSlugUser, Secret: &SecretWrite{EnvVar: "OPENAI_API_KEY", Hosts: []string{"api.openai.com"}}},
        }}
    if len(f.Writes) != 2 || f.Writes[0].Scope != ScopeProject || f.Writes[1].Scope != ScopePerSlugUser {
        t.Fatalf("a move must be two scoped writes, got %+v", f.Writes)
    }
}
```

- [ ] **Step 2: Run to verify it fails** — `go test ./internal/sandbox/analyser/ -run TestEvidence`. Expected: FAIL.
- [ ] **Step 3: Implement** the types and `hash` (sort copies of the slice fields, marshal a canonical struct, sha256 hex).
- [ ] **Step 4: Run** — `go test ./internal/sandbox/analyser/`. Expected: PASS.
- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/analyser/
git commit -m "feat(analyser): add the finding model and evidence hash"
```

---

### Task 2: Populate `readback.Credential.Hosts` (close the S5 gap)

**Files:**
- Modify: `internal/sandbox/readback/read.go`, `internal/sandbox/readback/read_test.go` (and the per-agent declaration if the endpoints live there)

**Interfaces:**
- Consumes: the existing `Credential` type (`read.go:37`).
- Produces: `Read` fills `Credential.Hosts` from the endpoints the same config entry names (e.g. `models.providers.<id>.baseURL`, an OpenAI-compatible `base_url`) — the spec's "endpoint in the same config entry" attribution fallback.

- [ ] **Step 1: Write the failing test** — a home with an agent config entry naming `base_url: https://api.example/v1` yields a static `Credential` whose `Hosts == []string{"api.example"}`; an entry with no endpoint yields empty `Hosts`.
- [ ] **Step 2: Run to verify it fails** — `go test ./internal/sandbox/readback/ -run TestReadFillsCredentialHosts`. Expected: FAIL.
- [ ] **Step 3: Implement** host extraction from the config entry the credential was found in (normalize to host; drop scheme/port/path). Unknown shapes yield empty hosts, never a guess.
- [ ] **Step 4: Run** — `go test ./internal/sandbox/readback/`. Expected: PASS.
- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/readback/
git commit -m "feat(readback): fill credential hosts from the config entry"
```

---

### Task 3: Observation findings and the network diff

**Files:**
- Create: `internal/sandbox/analyser/observation.go`, `internal/sandbox/analyser/observation_test.go`
- Modify: `internal/sandbox/readback/*.go`, `internal/agent/readback.go` — **remove the S5 `//deadcode:ignore` markers** (this task is the first production consumer)

**Interfaces:**
- Consumes: `readback.ReadBack`, `channel.Observation`, `network.Policy`, `viperconfig.LayerSettingOwner` (the shadow check).
- Produces:

```go
type SessionFacts struct {
    Paths       configpaths.ConfigPaths // to resolve the project layer's ownership of a key
    Slug        string
    ReadBack    readback.ReadBack
    Observation channel.Observation
    Current     network.Policy // the effective policy, for the never-widen diff
}
// ObservationFindings derives the network and secret findings from the read-back
// and the channel. It emits per-slug writes only and never removes; it never
// widens: proposed allow ⊆ observed ∪ current allow.
func ObservationFindings(s SessionFacts) []Finding
```

- [ ] **Step 1: Write the failing tests**

```go
func TestObservationNetworkDiffNeverWidens(t *testing.T) {
    s := SessionFacts{
        Current:     network.Policy{Profiles: []network.Profile{network.ProfileNone}, EgressAllow: []string{"already.example"}},
        Observation: channel.Observation{Destinations: []channel.Destination{{Host: "api.example", Success: true, Count: 2}}},
    }
    // expect a per-slug network.egress-allow finding whose proposed set is
    // {already.example, api.example}, and never a removal of already.example
}
func TestObservationDeniedIsReportedNotAllowed(t *testing.T) {
    s := SessionFacts{Observation: channel.Observation{Destinations: []channel.Destination{{Host: "blocked.example", Denied: true}}}}
    for _, f := range ObservationFindings(s) {
        for _, w := range f.Writes {
            if w.ConfigKey == "network.egress-allow" && contains(w.ConfigValue, "blocked.example") {
                t.Fatal("a denied destination must never be auto-allowed")
            }
        }
    }
}
func TestProposalSkipsAProjectOwnedKey(t *testing.T) {
    // project config sets network.egress-allow -> the finding is OutcomeSkipped at
    // apply time (a per-slug write would be shadowed), never written
}
```

- [ ] **Step 2: Run to verify it fails** — `go test ./internal/sandbox/analyser/ -run TestObservation`. Expected: FAIL.
- [ ] **Step 3: Implement** `ObservationFindings`: (a) the network diff — propose `network.profile: none` plus `currentAllow ∪ observed-used` when the current policy is broader; nothing when it is already as narrow; report denied destinations separately with `Evidence.Denied`; (b) secret migration — for each `CredentialStatic`, hosts come from the channel attribution first, then `Credential.Hosts`; with no hosts, no finding. Every write is `ScopePerSlugUser` and non-removing; a key the project layer owns is still emitted (the applier skips it) or not emitted — pick one and pin it in the test. Then **delete every `//deadcode:ignore` marker** in `internal/sandbox/readback/` and `internal/agent/readback.go`.
- [ ] **Step 4: Run** — `go test ./internal/sandbox/analyser/ && make deadcode`. Expected: PASS (the gate rejects a marker left on a now-reachable declaration).
- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/analyser/ internal/sandbox/readback/ internal/agent/
git commit -m "feat(analyser): add observation findings and wire the read-back consumer"
```

---

### Task 4: The `env.secret.yaml` writer and remover

**Files:**
- Create: `internal/sandbox/envsecret/write.go`, `internal/sandbox/envsecret/write_test.go`

**Interfaces:**
- Consumes: `SecretSpec`, `ParseSecretSpecYAMLData` (round-trip), `atomicwrite`.
- Produces:

```go
// WriteSecretSpec merges one keyed secret spec into <dir>/env.secret.yaml, creating
// the file mode 0600 when absent and preserving every other key and comment. It
// never writes a spec with an empty value or with neither hosts nor an explicit
// dangerous flag (that spec would be dropped at VM build).
func WriteSecretSpec(dir, envVar string, spec SecretSpec) error
// RemoveSecretSpec deletes envVar from <dir>/env.secret.yaml, preserving the rest;
// a no-op when the file or key is absent.
func RemoveSecretSpec(dir, envVar string) error
```

- [ ] **Step 1: Write the failing test** — `WriteSecretSpec` creates `env.secret.yaml` 0600 and a second key preserves the first; `RemoveSecretSpec` deletes one key and leaves the other; a hostless spec with no dangerous flag is refused and leaves the file untouched.
- [ ] **Step 2: Run to verify it fails** — `go test ./internal/sandbox/envsecret/ -run 'TestWriteSecretSpec|TestRemoveSecretSpec'`. Expected: FAIL.
- [ ] **Step 3: Implement** the writer and remover (load existing → merge/delete → atomic write under the scope dir; mode 0600).
- [ ] **Step 4: Run** — `go test ./internal/sandbox/envsecret/`. Expected: PASS.
- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/envsecret/
git commit -m "feat(envsecret): add the env.secret.yaml writer and remover"
```

---

### Task 5: The config-key remover

**Files:**
- Modify: `internal/viperconfig/setting_writer.go`, `internal/viperconfig/setting_writer_test.go`

**Interfaces:**
- Consumes: the existing read/modify/write path behind `WriteSetting`.
- Produces:

```go
// RemoveSetting deletes key from the config file in dir, preserving every other key,
// comment, and the file mode. It is a no-op when the file or the key is absent, and
// returns ErrUnsupportedConfigFormat for a non-YAML config.
func RemoveSetting(dir, key string) error
```

  (Removal is the *harden* contract — it narrows a broad setting or strips a misplaced secret; the learn-mode proposal never calls it.)

- [ ] **Step 1: Write the failing test** — `WriteSetting` a key then `RemoveSetting` it leaves the other keys and the mode intact; `RemoveSetting` on an absent key is a no-op; a `config.json5` dir returns `ErrUnsupportedConfigFormat`.
- [ ] **Step 2: Run to verify it fails** — `go test ./internal/viperconfig/ -run TestRemoveSetting`. Expected: FAIL.
- [ ] **Step 3: Implement** `RemoveSetting` through the same loader path.
- [ ] **Step 4: Run** — `go test ./internal/viperconfig/`. Expected: PASS.
- [ ] **Step 5: Commit**

```bash
git add internal/viperconfig/
git commit -m "feat(viperconfig): add RemoveSetting for the harden path"
```

---

### Task 6: The decline memory

**Files:**
- Create: `internal/sandbox/state/learn_decline.go`, `internal/sandbox/state/learn_decline_test.go`
- Modify: `internal/sandbox/state/root.go` (the `RootState` field), `internal/sandbox/state/schema.go` (`RootStateSchema = 3`), `internal/sandbox/state/*_test.go` (the pinned-field test)

**Interfaces:**
- Consumes: `state.WithRootState`, `RootTxn.State()`/`Commit()`.
- Produces:

```go
// RootState gains:
//   LearnDeclines map[string][]LearnDecline `yaml:"learn_declines,omitempty"` // key: repo slug
type LearnDecline struct { EvidenceHash string `yaml:"evidence_hash"`; At time.Time `yaml:"at"` }

func RecordLearnDecline(slug, evidenceHash string) error // idempotent on hash
func LearnDeclined(slug, evidenceHash string) (bool, error)
```

- [ ] **Step 1: Write the failing test** — `RecordLearnDecline("widget-abc","h1")` then `LearnDeclined("widget-abc","h1")` is true and `("widget-abc","h2")` is false; a second identical record does not duplicate; the field is dropped from YAML when empty (`omitempty`).
- [ ] **Step 2: Run to verify it fails** — `go test ./internal/sandbox/state/ -run TestLearnDecline`. Expected: FAIL.
- [ ] **Step 3: Implement** the field, the two accessors through `WithRootState(true, …)`, and bump `RootStateSchema` to 3; update the pinned-field test.
- [ ] **Step 4: Run** — `go test ./internal/sandbox/state/`. Expected: PASS.
- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/state/
git commit -m "feat(state): add the slug-keyed learn-decline memory"
```

---

### Task 7: The applier

**Files:**
- Create: `internal/sandbox/analyser/apply.go`, `internal/sandbox/analyser/apply_test.go`

**Interfaces:**
- Consumes: Tasks 4–6's writers/removers and decline store, `configpaths.ConfigPaths`, `viperconfig.WriteSetting`/`RemoveSetting`/`LayerSettingOwner`, `resourceproposal.ShadowSource`, `projecttrust.ApproveSettingWrite`, `termio.Prompter`, `options.Mode`.
- Produces:

```go
type ApplyOptions struct {
    Paths        configpaths.ConfigPaths
    Slug         string // repo slug (per-slug default + decline key)
    CheckoutSlug string // for a project-scope write's approval
    UI           termio.Prompter
    Mode         options.Mode
    SetByFlag    func(key string) bool
    // SecretValue resolves a finding's static secret value in memory (from the
    // read-back); it is never persisted and never logged. nil for config-only.
    SecretValue func(w SecretWrite) (string, bool)
}

// Apply walks findings, applying each finding's writes together after one explicit
// acceptance. It writes nothing under a preview mode, prints one line and writes
// nothing when the terminal is non-interactive, skips a finding the decline memory
// holds for its evidence hash, and skips a per-slug write whose key the project
// layer owns (a shadowed no-op). A move applies its writes or none: if the project
// removal fails, the per-slug addition does not run.
func Apply(findings []Finding, opts ApplyOptions) []ApplyResult
```

- [ ] **Step 1: Write the failing tests**

```go
func TestApplyDryRunWritesNothing(t *testing.T)          // ModePreview -> all OutcomeSkipped, no file
func TestApplyNonInteractivePrintsOneLineAndWritesNothing(t *testing.T) // !UI.IsInteractive() -> skipped
func TestApplyDeclinedEvidenceIsSkipped(t *testing.T)    // a recorded decline -> OutcomeSkipped, no write
func TestApplyPerSlugWritesThroughWriteSetting(t *testing.T) // accept network.profile -> <UserSlugConfigDir(slug)>/config.yaml
func TestApplySkipsProjectOwnedKey(t *testing.T)         // project owns network.egress-allow -> OutcomeSkipped with a note
func TestApplyUnsupportedFormatSkips(t *testing.T)       // config.json5 -> OutcomeSkipped/Failed with ErrUnsupportedConfigFormat, no stray file
func TestApplyMoveIsAtomic(t *testing.T)                 // project removal fails -> per-slug addition not applied
func TestApplySecretMigratesStaticOnly(t *testing.T)     // SecretWrite with hosts -> env.secret.yaml gains the key; a login never appears
func TestApplyProjectScopeIsGated(t *testing.T)          // ScopeProject -> projecttrust.ApproveSettingWrite called; ScopePerSlugUser -> not
func TestApplyNeverShadowedByFlagOrEnv(t *testing.T)     // ShadowSource reports an override -> OutcomeSkipped, nothing written
```

- [ ] **Step 2: Run to verify it fails** — `go test ./internal/sandbox/analyser/ -run TestApply`. Expected: FAIL.
- [ ] **Step 3: Implement** `Apply`: gate on `opts.Mode.Previews()` and `opts.UI.IsInteractive()`; check `state.LearnDeclined(slug, f.EvidenceHash)`; prompt once per finding, then apply its writes in order:
  - a `ConfigKey` write to `ScopePerSlugUser` → if `LayerSettingOwner` says the project owns the key, `OutcomeSkipped` with a note; otherwise `viperconfig.WriteSetting(paths.UserSlugConfigDir(slug), key, value)` (or `RemoveSetting` when `Remove`);
  - a `ConfigKey` write to `ScopeProject` → `projecttrust.ApproveSettingWrite(checkoutSlug, key, value)` via `WriteSetting`/`RemoveSetting`;
  - a `Secret` write → resolve the value through `opts.SecretValue` and call `envsecret.WriteSecretSpec` (or `RemoveSecretSpec` when `Remove`);
  - if any write of a multi-write finding fails, record `OutcomeFailed` and apply none of the finding's writes; on decline, `state.RecordLearnDecline`. `--yes` must not satisfy the prompt (mirror the trust gate).
- [ ] **Step 4: Run** — `go test ./internal/sandbox/analyser/ ./internal/sandbox/state/ ./internal/sandbox/envsecret/ ./internal/viperconfig/`. Expected: PASS.
- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/analyser/
git commit -m "feat(analyser): apply scoped findings with removal and the trust gate"
```

---

### Task 8: Spec reconciliation and the index

**Files:**
- Modify: `.operator-shared/specs/onboarding/learn-mode/findings-and-apply.md` (record the settled details: the learn-mode proposal is per-slug and never removes; a per-slug write is skipped when the project layer owns the key, because the project layer wins; harden's project→per-slug secret move is the removal case that S8 emits; a per-slug write is YAML-only), `.operator-shared/index/sandbox.md` (the new `analyser` package), `.operator-shared/plans/2026-11-17-learn-mode-program.md` (flip S6 to landed on merge; drop the S6 placeholder)

- [ ] **Step 1:** Update the spec's §Scopes, §Applying, §Attribution and the network diff, and §`config harden` with the settled mechanics above (only where the spec was silent or loose; do not restate the code).
- [ ] **Step 2:** Add `internal/sandbox/analyser` to the sandbox subindex with its one-line role.
- [ ] **Step 3: Verify** — `make check` and `make brain-check`. Expected: PASS.
- [ ] **Step 4: Commit**

```bash
git add .operator-shared/
git commit -m "docs(operator): record the analyser contract"
```

---

## Self-review

**Coverage.** The finding shape (§finding shape), the per-write scope, and removal are Task 1; the config/observation split is delegated to S8 (config) and Task 3 (observation); scopes, shadowing, and the trust gate are Tasks 3 and 7; the credential-store rule and the never-widen/never-dangerous rules are Tasks 3, 4, and 7; the project→per-slug secret move is Tasks 4, 5, and 7 (the move), with the harden *emitter* in S8; decline memory and re-arming are Tasks 6–7; non-interactive/dry-run are Task 7. The **session record** (identity + when) is deliberately **not** here — it is S7's; `Apply` takes the slug it needs as an option.

**Proportion.** Signatures, test names, and assertions; no function bodies beyond what the tests do not determine.

**Type consistency.** `Scope`/`Kind`/`Evidence`/`Write`/`SecretWrite`/`Finding`/`Outcome`/`ApplyResult` are defined once in Task 1 and are the frozen contract in the program index; `envsecret.WriteSecretSpec`/`RemoveSecretSpec` (Task 4), `viperconfig.RemoveSetting` (Task 5), and `state.RecordLearnDecline`/`LearnDeclined` (Task 6) are consumed by Task 7 under those exact names.
