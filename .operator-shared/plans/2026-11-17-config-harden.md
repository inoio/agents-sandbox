# S8 — `config harden`

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. A fresh implementer per task; a fresh reviewer gates each task; one whole-subplan review at the end. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship `config harden` — the analyser with no session: a re-runnable audit of the effective launcher configuration that emits config-only findings through the same `analyser.Finding`/`analyser.Apply` engine, including the project-scope removals the proposal never emits (moving a static secret committed in the project scope to the per-slug user scope, narrowing an over-broad posture or secret scope, fixing a secret file's mode, and turning off host-config provisioning).

**Architecture:** The finder lives in `analyser` (`analyser/config.go`, `ConfigFindings`) beside `ObservationFindings`, so it shares the evidence-hash stamping and the model, and the command lives in `cmd/agents-sandbox` under `config`. The finder reads the effective `viperconfig.Config`, the ordered `configpaths.ConfigScopes`, and each scope's `env.secret.yaml` (through `envsecret`, whose reads carry the project approval); it chooses each write's scope from `viperconfig.LayerSettingOwner`, so an effective project-owned setting is narrowed in the project scope (through the trust gate) and everything else in the per-slug user scope. The applier gains one write kind, a file-mode change on a scope's secret file. `config harden` needs no microsandbox runtime, so the whole subplan is verifiable in the dev VM.

**Tech Stack:** Go 1.27, `internal/sandbox/{analyser,envsecret,network,options}`, `internal/{configpaths,viperconfig,projecttrust,termio,runoptions}`, `cmd/agents-sandbox`.

**Spec:** `.operator-shared/specs/onboarding/learn-mode/findings-and-apply.md` (§Scopes, §The credential store, §Applying, §`config harden`); `.operator-shared/specs/onboarding/onboarding.md` (§`config harden`); `.operator-shared/specs/configuration/config-scopes.md` (the ordered scopes); `.operator-shared/specs/configuration/project-config-trust.md` (§Approval contract, the launcher-write rule).

**Consumes:** `analyser.{Finding,Evidence,Write,SecretWrite,Scope,Kind,Outcome,ApplyResult,Apply,ApplyOptions}` (S6, landed), `envsecret.{SecretSpec,ParseSecretSpecYAML,WriteSecretSpec,RemoveSecretSpec,ValidateSecretFile}`, `viperconfig.{Config,LayerSettingOwner}`, `configpaths.{ConfigPaths,ConfigScope,ConfigScopes}`.

**Produces:** `analyser.FileModeWrite`; `analyser.Write.FileMode`; `analyser.ConfigFacts`; `analyser.ConfigFindings`; the `config harden` command.

## Global Constraints

See `2026-11-17-learn-mode-program.md` → Global Constraints. Specifically:

- **No widening** (invariant 4): harden only narrows or removes. Its writes drop a wildcard entry, drop `allow_any_host_dangerous`, set a profile to `none`, set `provision-host-config` to `false`, or move a secret to a lower-precedence scope. It never adds an allow entry and never broadens a scope.
- **No curated knowledge** (invariant 5): every finding derives from the effective configuration alone. S8 reads no provider table.
- **The credential store is never owned by the managed layer** (invariant 2): harden moves a secret between *secret files* (`env.secret.yaml`); it never copies a login credential and never adds a `home:`/provision rule.
- **No value in evidence, a decline record, or a log.** A secret value is read in memory only to re-write it; the finding carries the env-var name and hosts, never the value.
- A project-scope write goes through the trust gate; a refusal is reported, never forced.
- `make fmt`, `make lint`, `make check` — never `go fmt`/`go vet` by hand.
- This subplan has **no human-accept task**: it touches only host files.

## Design decisions (calls made; correct if wrong)

1. **The finder is `analyser.ConfigFindings`, producing `KindConfig` findings.** `KindConfig` has been in the model since S6 and unused until now; the config/observation split is exactly this. The command calls `ConfigFindings` then `analyser.Apply`.
2. **The finding set (six):**
   - **C1 — a static secret in the project scope.** For each key in the project scope's `env.secret.yaml`, a finding whose writes are a project-scope `SecretWrite{Remove: true}` (remove from the committed file) and a per-slug `SecretWrite` carrying its hosts/header fields (add at the trusted user scope). Text states that removing it from the file does not remove it from git history, and to rotate the credential.
   - **C2 — an over-broad secret scope.** A secret with `AllowAnyHostDangerous: true` and at least one host becomes a finding that rewrites the spec through `envsecret.WriteSecretSpec` with the flag dropped (the applier's `executeSecret` always writes `AllowAnyHostDangerous: false`). A dangerous secret with *no* host has nothing to narrow to and is reported, not written.
   - **C3 — an over-broad egress profile.** When the effective profile includes `public`, `private`, or `host` and `network.egress-allow` is non-empty, a finding proposing `network.profile: none`, keeping the allow list. An empty allow list is reported (narrowing to `none` would deny all egress) and not written.
   - **C4 — a wildcard egress entry.** An `egress-allow` entry `*` (or `0.0.0.0/0`, `::/0`) becomes a finding proposing `network.egress-allow` with that entry removed.
   - **C5 — a secret file mode.** Each scope's `env.secret.yaml` whose mode is not `0600` becomes a finding whose single write is a `FileModeWrite{Mode: 0o600}` (Task 1).
   - **C6 — host-config provisioning on.** `provision-host-config: true` becomes a finding proposing `provision-host-config: false`, its Text pointing at `run --learn` to derive managed config first. This is the one finding whose "safe path" (onboarding.md's `config migrate`) learn mode supersedes; recorded here because the onboarding text predates that.
3. **A write targets the key's owner.** `viperconfig.LayerSettingOwner(paths, slug, key)` names the highest-precedence file layer that sets a key. Harden writes the project scope when the project owns the key (through the gate) and the per-slug user scope otherwise. This is what makes a narrowing *take effect* where the proposal's per-slug write would be shadowed.
4. **Harden reads the project scope through the approval.** `envsecret.ParseSecretSpecYAML` already refuses an unapproved project file (`projectguard`); a project scope the user has not approved contributes no finding, and the command says so once.
5. **No session evidence.** The evidence-driven reduction ("offer to reduce to the hosts a session used") is deferred: it couples harden to the S7 artefact store. The catalogue's `config harden` §network finding is structural only in S8.
6. **Declines are keyed by the evidence hash** (S6's `RootState.LearnDeclines`), so harden is re-runnable without re-prompting per unchanged finding.
7. **`--yes` never answers a finding.** `analyser.Apply` already uses `Select` with a declining default, which assume-yes does not satisfy; harden passes `SetByFlag` that reports nothing set.
8. **No new config key and no `viperconfig.Config` field.** `config harden` reads the effective config; it adds no key.

## Review Focus

1. **A harden write that widens** — a profile set to anything but `none`, an allow entry added, a secret scope broadened, or a new host. Every write must narrow. (Tasks 2, 3.)
2. **A project-scope write bypassing the trust gate** — a secret removal or config narrowing in the project scope must go through `projectguard`/`projecttrust` and be refused when unapproved. (Task 2, Task 3.)
3. **A secret value in a finding, evidence, or log** — C1/C2 read the value only to re-write it; the finding carries the env-var name and hosts. (Tasks 1, 2, 3.)
4. **A `mode` change hitting an absent file or a directory** — C5 must skip a scope with no `env.secret.yaml`, never create one to chmod it. (Task 1, Task 2.)
5. **An unapproved project layer** — harden must still run against the user scopes and say the project scope was skipped, never read it unapproved. (Task 3.)
6. **A non-interactive run / `--dry-run`** — writes nothing, prompts nothing, never hangs. (Task 3.)
7. **A finding whose evidence changed** — a recorded decline stops matching and the finding re-arms. (Tasks 2, 3.)
8. **C3/C6 foot-guns** — narrowing to `none` with an empty allow list, or disabling host-config provisioning, must be surfaced in the Text, not applied silently. (Task 2.)

---

### Task 1: The file-mode write

**Files:**
- Modify: `internal/sandbox/analyser/finding.go` (the `Write` type), `internal/sandbox/analyser/apply.go`, `internal/sandbox/analyser/apply_test.go`

**Interfaces:**
- Consumes: `configpaths.EnvSecretFileName`, `envsecret.ValidateSecretFile`.
- Produces:

```go
// FileModeWrite changes the mode of one of the scope's known files. It targets
// the scope's env.secret.yaml and carries no content.
type FileModeWrite struct { Mode fs.FileMode }
// Write gains: FileMode *FileModeWrite
```

  `planWrites`/`planTarget` gain a `FileMode != nil` branch: the target dir is the write's scope dir (`UserSlugConfigDir(slug)` for `ScopePerSlugUser`, `ProjectConfigDir()` — gated — for `ScopeProject`); validation refuses when the scope's `env.secret.yaml` is absent or unparseable (nothing to chmod, or not the file harden means). `executeWrite` chmods it.

- [ ] **Step 1: Write the failing tests**

```go
func TestApplyFileModeChmodsTheSecretFile(t *testing.T)       // a 0644 per-slug env.secret.yaml -> 0600
func TestApplyFileModeSkipsAnAbsentFile(t *testing.T)         // no file -> OutcomeSkipped, no file created
func TestApplyFileModeProjectScopeIsGated(t *testing.T)       // ScopeProject -> projecttrust.ApproveSettingWrite path
```

- [ ] **Step 2: Run to verify it fails** — `go test ./internal/sandbox/analyser/ -run TestApplyFileMode`. Expected: FAIL.

- [ ] **Step 3: Implement** the type, the `Write` field, the plan/validate/execute branch (chmod `filepath.Join(dir, configpaths.EnvSecretFileName+".yaml")`), and an `os.Chmod` failure surfaced as `OutcomeFailed`.

- [ ] **Step 4: Run** — `go test ./internal/sandbox/analyser/`. Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/analyser/
git commit -m "feat(analyser): add the file-mode write for the harden secret-file check"
```

---

### Task 2: The config-only finder

**Files:**
- Create: `internal/sandbox/analyser/config.go`, `internal/sandbox/analyser/config_test.go`

**Interfaces:**
- Consumes: `ReadBack`-free config inputs — `viperconfig.Config`, `configpaths.ConfigScope`, `envsecret.ParseSecretSpecYAML`, `network.Policy`, `viperconfig.LayerSettingOwner`.
- Produces:

```go
// ConfigFacts is the effective configuration harden audits.
type ConfigFacts struct {
    Paths  configpaths.ConfigPaths
    Slug   string                    // the per-slug user scope + decline key
    Config *viperconfig.Config       // effective launcher config (network, provisioning)
    Scopes []configpaths.ConfigScope // to locate each scope's env.secret.yaml
    Logger termio.Logger
}
// ConfigFindings returns the config-only (KindConfig) findings the effective
// configuration supports (Design decision 2). It never widens and never removes
// a user-set allow entry.
func ConfigFindings(f ConfigFacts) []Finding
```

- [ ] **Step 1: Write the failing tests**

```go
func TestConfigFindingsMovesAProjectSecret(t *testing.T)      // project env.secret.yaml key -> one finding, two writes (project remove + per-slug add)
func TestConfigFindingsDropsDangerousFlagWithHosts(t *testing.T) // allow_any_host_dangerous + hosts -> rewrite without the flag
func TestConfigFindingsReportsDangerousWithoutHosts(t *testing.T) // no hosts -> reported, no write
func TestConfigFindingsNarrowsBroadProfile(t *testing.T)      // public + egress-allow -> profile none, allow kept
func TestConfigFindingsEmptyAllowIsReportedNotWritten(t *testing.T) // public + no allow -> no profile write
func TestConfigFindingsRemovesWildcardEntry(t *testing.T)     // egress-allow ["*","api.example"] -> proposal drops "*"
func TestConfigFindingsSecretFileMode(t *testing.T)          // a 0644 env.secret.yaml -> a FileMode finding
func TestConfigFindingsProvisionHostConfig(t *testing.T)      // true -> proposal sets false, Text names run --learn
func TestConfigFindingsNeverWidens(t *testing.T)              // every emitted write either removes or narrows
```

- [ ] **Step 2: Run to verify it fails** — `go test ./internal/sandbox/analyser/ -run TestConfigFindings`. Expected: FAIL.

- [ ] **Step 3: Implement** `ConfigFindings`: iterate the scopes and parse each `EnvSecretYAMLFile`; build C1/C2/C5 from the secret files and C3/C4/C6 from `f.Config`; choose each write's scope via `LayerSettingOwner` (Design decision 3); stamp the evidence hash through `withEvidenceHash` (same package). Sort findings deterministically.

- [ ] **Step 4: Run** — `go test ./internal/sandbox/analyser/`. Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/analyser/
git commit -m "feat(analyser): add the config-only harden finder"
```

---

### Task 3: The `config harden` command

**Files:**
- Modify: `cmd/agents-sandbox/commands_config.go`, `cmd/agents-sandbox/constants.go`, `cmd/agents-sandbox/cli_config_test.go`

**Interfaces:**
- Consumes: `analyser.ConfigFindings`, `analyser.Apply`, `git.ResolveIdentity`, `resolverFromContext`, `configSlugFromContext`, `configpaths.ConfigScopes`, `envsecret.ParseSecretSpecYAML`, `runoptions.ResolveMode`.
- Produces: `cmdHarden = "harden"`; `buildConfigHardenCmd(ui)`; registered in `buildConfigCmd`.

- [ ] **Step 1: Write the failing tests** (`cli_config_test.go`)

```go
func TestConfigHardenNonInteractiveWritesNothing(t *testing.T) // one line naming run --learn; no file written
func TestConfigHardenDryRunWritesNothing(t *testing.T)         // --dry-run -> no write, no prompt
func TestConfigHardenNoFindingsIsQuiet(t *testing.T)           // a clean config -> no proposal
func TestConfigHardenHelpAndTree(t *testing.T)                 // config harden present in --help and `tree`
```

- [ ] **Step 2: Run to verify it fails** — `go test ./cmd/agents-sandbox/ -run TestConfigHarden`. Expected: FAIL.

- [ ] **Step 3: Implement** the subcommand: resolve identity and the effective slug, build `analyser.ConfigFacts` (`Scopes: configpaths.ConfigScopes(paths, slug)`, `Logger: ui`), call `analyser.ConfigFindings`, then `analyser.Apply(findings, analyser.ApplyOptions{Paths, Slug, CheckoutSlug, UI: ui, Mode: runoptions.ResolveMode(cmd), SetByFlag: func(string) bool { return false }, SecretValue: <parse the scopes for the env var's value>})`, and print a one-line outcome summary. When the project layer is unapproved and thus unread, say so once. `config harden` stays out of `commandNeedsMSBRuntime` (already true for `config`).

- [ ] **Step 4: Run** — `go test ./cmd/agents-sandbox/ -run TestConfigHarden`. Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/agents-sandbox/
git commit -m "feat(config): add config harden"
```

---

### Task 4: Docs, changelog, and the brain

**Files:**
- Create: `changelog.d/<PR>.added.md`
- Modify: `.operator-shared/specs/onboarding/learn-mode/findings-and-apply.md` (§`config harden`: record the settled finding set C1–C6, the owner-scope rule, the file-mode write, and that the session-evidenced reduction is deferred), `.operator-shared/specs/onboarding/onboarding.md` (§`config harden`: replace the `config migrate` remediation with `run --learn`), `.operator-shared/index/index.md` (the `config harden` command), `.operator-shared/plans/2026-11-17-learn-mode-program.md` (flip S8 to landed on merge)
- Modify: `docs/` (`manage-config.md` or the config reference: name `config harden` and what it audits)

- [ ] **Step 1:** Write the changelog fragment.
- [ ] **Step 2:** Update the specs, the index, the program, and the user docs (`STYLEGUIDE.md`, the linking rules in `AGENTS.md`). Keep the Onboarding entry's `read_if` accurate if the finding set changed.
- [ ] **Step 3: Verify** — `make check && make brain-check`. Expected: PASS.
- [ ] **Step 4: Commit**

```bash
git add changelog.d/ docs/ .operator-shared/
git commit -m "docs(operator): record the config harden finding set"
```

---

## Self-review

**Coverage.** Scopes and the owner rule are Design decision 3 (Tasks 2–3); the credential-store rule is C1 (Tasks 2–3); the never-widen rule is C3/C4 (Task 2, Review Focus 1); the project trust gate is Tasks 2–3; the file-mode audit is Task 1 + C5; `provision-host-config` is C6; the non-interactive/dry-run contract is Task 3; decline re-arming is Task 2 + `analyser.Apply`. The **session-evidenced network reduction** and the **agent-config** concern are deliberately out (Design decision 5; S10).

**Proportion.** Signatures, test names, and assertions; the only new model shape is one write kind.

**Type consistency.** `FileModeWrite`/`Write.FileMode` (Task 1); `ConfigFacts`/`ConfigFindings` (Task 2); `cmdHarden`/`buildConfigHardenCmd` (Task 3). `analyser.Apply` and `ApplyOptions` are used unchanged except for the file-mode branch in `analyser` itself.
