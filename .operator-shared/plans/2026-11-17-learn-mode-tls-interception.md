# S2 — TLS-interception control (#151) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking. A fresh implementer per task; a fresh reviewer gates each task; one whole-subplan review at the end. Host-only steps are a human accept step.

**Goal:** Expose microsandbox's TLS-interception and strict-hostname controls as launcher config — `network.intercept-tls` (`auto`/`on`/`off`), `network.tls-bypass`, and `network.disable-strict` — folding them into the network fingerprint, with no registry/docker special-casing; the in-VM docker CA case becomes a recipe instead.

**Architecture:** New launcher policy fields in `internal/sandbox/network` flow through the existing pipeline (`network.Policy` → `msb.NetworkSpec` → SDK `NetworkConfig.TLS`/`.DisableStrict`), so the recreate path applies them and `state.NetworkChanged` keeps a stale VM from surviving a change. The launcher never imports the SDK outside `internal/sandbox/msb`.

**Tech Stack:** Go 1.27, `internal/sandbox/network`, `internal/sandbox/msb`, `internal/sandbox/state`, `internal/viperconfig`, microsandbox SDK `v0.7.15` (`NetworkConfig.TLS`, `TLSConfig{Bypass}`, `NetworkConfig.DisableStrict`).

**Spec:** `.operator-shared/specs/network-and-secrets/interception-and-proxy.md`; issue #151. The settled design facts
this plan depends on — the config surface, the projection, the fingerprint emission rule, and the empirically verified
docker recipe — are inlined per task below.

## Global Constraints

- Go 1.27; follow `AGENTS.md`, `.operator-shared/operator.md`, and repo code style.
- Default to TDD: write the failing test, watch it fail, implement, watch it pass. Test a unit in the file named after it.
- `make fmt`, `make lint`, `make check` — never `go fmt`/`go vet` by hand.
- Every behavior change keeps `README.md`/`docs/` (except `docs/superpowers`) current and adds a `changelog.d/<PR>.<type>.md` fragment.
- `internal/sandbox/msb` is the only package that imports the microsandbox SDK. No SDK type crosses the seam.
- Network changes are creation-only and fingerprint-driven: any field that reaches the VM is in `network.Policy.Fingerprint()`.
- The config key registry is derived from `viperconfig.Config` struct tags; `env:"-"` makes a key config-file-only.
- Host-only verification: `/dev/kvm` is unusable in the dev VM. `msb`-tagged tests and any real sandbox run are a **human accept** step, never claimed done by an agent.

## Review Focus

Inputs/failure modes the design implies but no single task's happy path exercises; each is pinned to its owning task.

1. **`off` with a secret configured** — must fail with an actionable error, never silently leave interception on. (Task 2.)
2. **A stale VM** — changing `intercept-tls`, `tls-bypass`, or `disable-strict` in a way that changes the emitted policy
   must recreate the VM (an emission-equivalent flip does not). (Task 4.)
3. **An unknown mode or an empty bypass entry** — must fail at config-load time, not at VM creation. (Tasks 1, 3.)
4. **`auto` vs unset** — must fingerprint identically (no spurious recreate) and must leave `TLS` nil when no bypass is set. (Tasks 1, 2.)
5. **A bypassed-and-allowlisted host under strict mode** — must be documented as still denied unless `disable-strict` is set; never described as reachable by the bypass alone. (Task 5.)

---

### Task 1: TLS-interception policy fields in `network`

**Files:**
- Create: `internal/sandbox/network/tls.go`, `internal/sandbox/network/tls_test.go`
- Modify: `internal/sandbox/network/network.go`, `internal/sandbox/network/network_test.go`

**Interfaces:**
- Consumes: nothing new.
- Produces:
  ```go
  type InterceptMode string
  const (
      InterceptAuto InterceptMode = "auto" // empty value means auto
      InterceptOn   InterceptMode = "on"
      InterceptOff  InterceptMode = "off"
  )
  func ParseInterceptMode(s string) (InterceptMode, error) // "", "auto", "on", "off"; else error
  func ValidateTLSBypass(in []string) ([]string, error)    // exact host or "*.suffix"; reject empty
  ```
  `Policy` gains `InterceptTLS InterceptMode` (`mapstructure:"intercept-tls"`), `TLSBypass []string` (`mapstructure:"tls-bypass" env:"-"`), and `DisableStrict bool` (`mapstructure:"disable-strict"`). `Fingerprint`, `Describe`, and `Empty` include all three. This task compiles on its own and does **not** touch `Policy.Spec()` or any `msb` type — the `Spec()` wiring is Task 2's.

- [ ] **Step 1: Write the failing test** in `internal/sandbox/network/tls_test.go` (package `network`, external `network_test` is fine if it only uses exported names):

```go
func TestPolicyFingerprintIncludesTLSInterception(t *testing.T) {
    base := Policy{Profiles: []Profile{ProfileNone}}
    withBypass := base
    withBypass.TLSBypass = []string{"*.internal.example"}
    if base.Fingerprint() == withBypass.Fingerprint() {
        t.Fatal("tls-bypass must change the fingerprint")
    }
    on := base
    on.InterceptTLS = InterceptOn
    if base.Fingerprint() == on.Fingerprint() {
        t.Fatal("intercept-tls must change the fingerprint")
    }
    strict := base
    strict.DisableStrict = true
    if base.Fingerprint() == strict.Fingerprint() {
        t.Fatal("disable-strict must change the fingerprint")
    }
}

func TestPolicyFingerprintTreatsEmptyInterceptModeAsAuto(t *testing.T) {
    unset := Policy{Profiles: []Profile{ProfileNone}}
    auto := unset
    auto.InterceptTLS = InterceptAuto
    if unset.Fingerprint() != auto.Fingerprint() {
        t.Fatal("unset and explicit auto must fingerprint the same")
    }
}

func TestParseInterceptModeRejectsUnknown(t *testing.T) {
    if _, err := ParseInterceptMode("sometimes"); err == nil {
        t.Fatal("want error for an unknown mode")
    }
    for in, want := range map[string]InterceptMode{"": InterceptAuto, "auto": InterceptAuto, "on": InterceptOn, "off": InterceptOff} {
        got, err := ParseInterceptMode(in)
        if err != nil || got != want {
            t.Fatalf("ParseInterceptMode(%q) = %q, %v; want %q", in, got, err, want)
        }
    }
}

func TestValidateTLSBypassRejectsEmpty(t *testing.T) {
    if _, err := ValidateTLSBypass([]string{""}); err == nil {
        t.Fatal("want error for an empty bypass entry")
    }
    got, err := ValidateTLSBypass([]string{" example.com ", "*.internal"})
    if err != nil || len(got) != 2 || got[0] != "example.com" {
        t.Fatalf("ValidateTLSBypass = %v, %v", got, err)
    }
}
```

- [ ] **Step 2: Run to verify it fails** — `go test ./internal/sandbox/network/ -run 'TestPolicyFingerprintIncludesTLSInterception|TestPolicyFingerprintTreatsEmptyInterceptModeAsAuto|TestParseInterceptModeRejectsUnknown|TestValidateTLSBypassRejectsEmpty'`. Expected: FAIL (undefined names).

- [ ] **Step 3: Implement** in `tls.go`: `InterceptMode`, the three constants, `ParseInterceptMode` (trim; `""`/`"auto"` → `InterceptAuto`), `ValidateTLSBypass` (trim each entry, reject empty, return the trimmed list). Extend `Policy` in `network.go` with the three fields; add a helper that normalizes `InterceptTLS` (`""` → `InterceptAuto`) for `Fingerprint`/`Describe`; extend `Fingerprint` with sorted `tls-bypass=<host>` lines (only when the mode is not `off`, since the bypass is dropped from the emitted policy there), a `disable-strict=true` line (only when true), and an `intercept-tls=on` line **only when the mode is `on` and no bypass is set** — the only case that emits a TLS config with an empty bypass, so `auto`+bypass and `on`+bypass fingerprint alike (both emit `TLS{Bypass}`) and `off` and `auto` without bypass fingerprint alike (both emit no TLS), and flipping between equivalent modes never forces a recreate; this records the emitted TLS state, not the requested mode, matching `Fingerprint`'s stated rule that a component is added only when it reflects a real change to the emitted policy; extend `Describe` with the same prefixes unconditional on mode (its contract is "what the user configured"); extend `Empty` to report false when any of the three is set.

- [ ] **Step 4: Run the tests** — `go test ./internal/sandbox/network/`. Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/network/
git commit -m "feat(network): add TLS-interception and strict-hostname policy fields"
```

---

### Task 2: Project the policy to the SDK in `msb`

**Files:**
- Modify: `internal/sandbox/msb/network_values.go`, `internal/sandbox/msb/translate.go`, `internal/sandbox/msb/translate_test.go`, `internal/sandbox/network/network.go` (the `Spec()` wiring)

**Interfaces:**
- Consumes: `network.InterceptMode`, `ParseInterceptMode` (Task 1).
- Produces:
  ```go
  // internal/sandbox/msb/network_values.go — an msb-local mirror; no SDK type crosses the seam.
  type InterceptMode string
  const (
      InterceptAuto InterceptMode = "auto"
      InterceptOn   InterceptMode = "on"
      InterceptOff  InterceptMode = "off"
  )
  // NetworkSpec gains:
  //   InterceptTLS  InterceptMode
  //   TLSBypass     []string
  //   DisableStrict bool
  ```
  `network.Policy.Spec()` populates all three. `networkConfig(spec NetworkSpec)` sets `cfg.DisableStrict` and sets `cfg.TLS = &msbSdk.TLSConfig{Bypass: spec.TLSBypass}` whenever the mode is `on`, or the mode is `auto` with a non-empty bypass; it leaves `cfg.TLS` nil otherwise. `sandboxOptions(spec SandboxSpec)` returns an error when `spec.Network.InterceptTLS == InterceptOff` and `len(spec.Secrets) > 0`.

- [ ] **Step 1: Write the failing tests** in `internal/sandbox/msb/translate_test.go` (in-package, so it can call `networkConfig` and `sandboxOptions`):

```go
func TestNetworkConfigSetsTLSForInterceptOn(t *testing.T) {
    cfg, err := networkConfig(NetworkSpec{InterceptTLS: InterceptOn, TLSBypass: []string{"*.internal"}})
    if err != nil {
        t.Fatal(err)
    }
    if cfg.TLS == nil || len(cfg.TLS.Bypass) != 1 {
        t.Fatalf("want TLS with bypass, got %+v", cfg.TLS)
    }
}

func TestNetworkConfigLeavesTLSNilForAutoWithoutBypass(t *testing.T) {
    cfg, err := networkConfig(NetworkSpec{})
    if err != nil {
        t.Fatal(err)
    }
    if cfg.TLS != nil {
        t.Fatal("auto with no bypass must not set TLS")
    }
}

func TestNetworkConfigSetsTLSForBypassUnderAuto(t *testing.T) {
    cfg, err := networkConfig(NetworkSpec{TLSBypass: []string{"*.internal"}})
    if err != nil {
        t.Fatal(err)
    }
    if cfg.TLS == nil || len(cfg.TLS.Bypass) != 1 {
        t.Fatalf("want TLS with bypass, got %+v", cfg.TLS)
    }
}

func TestNetworkConfigSetsDisableStrict(t *testing.T) {
    cfg, err := networkConfig(NetworkSpec{DisableStrict: true})
    if err != nil {
        t.Fatal(err)
    }
    if !cfg.DisableStrict {
        t.Fatal("disable-strict must reach the SDK config")
    }
}

func TestSandboxOptionsRejectsOffWithSecrets(t *testing.T) {
    _, err := sandboxOptions(SandboxSpec{
        Image:   "img",
        Network: NetworkSpec{InterceptTLS: InterceptOff},
        Secrets: []SecretEntry{{EnvVar: "TOKEN", Value: "v"}},
    })
    if err == nil {
        t.Fatal("off with a configured secret must fail with an actionable error")
    }
    if _, err := sandboxOptions(SandboxSpec{Image: "img", Network: NetworkSpec{InterceptTLS: InterceptOff}}); err != nil {
        t.Fatalf("off without secrets must be accepted, got %v", err)
    }
}
```

- [ ] **Step 2: Run to verify they fail** — `go test ./internal/sandbox/msb/ -run 'TestNetworkConfigSetsTLSForInterceptOn|TestNetworkConfigLeavesTLSNilForAutoWithoutBypass|TestNetworkConfigSetsTLSForBypassUnderAuto|TestNetworkConfigSetsDisableStrict|TestSandboxOptionsRejectsOffWithSecrets'`. Expected: FAIL.

- [ ] **Step 3: Implement.**
  - `network_values.go`: the `InterceptMode` mirror type and constants; the three `NetworkSpec` fields.
  - `network.go` `Spec()`: normalize `p.InterceptTLS` through `ParseInterceptMode` (returning its error, so `Policy.Validate()` rejects an unknown mode) and map it to `msb.InterceptMode`; carry `p.TLSBypass` (re-validated through `ValidateTLSBypass`) and `p.DisableStrict`.
  - `translate.go` `networkConfig`: after the existing rule composition, set `cfg.DisableStrict = spec.DisableStrict`; set `cfg.TLS = &msbSdk.TLSConfig{Bypass: spec.TLSBypass}` when the mode is `on` or the bypass list is non-empty; otherwise leave it nil.
  - `translate.go` `sandboxOptions`: before building options, return an `errors.New(...)` naming the limitation when `spec.Network.InterceptTLS == InterceptOff` and `len(spec.Secrets) > 0` (the SDK re-enables interception for substitution, so `off` is not expressible). The message must tell the user to remove the secret(s) or use `auto`/`on`.

- [ ] **Step 4: Run the tests** — `go test ./internal/sandbox/msb/ ./internal/sandbox/network/`. Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/msb/ internal/sandbox/network/network.go
git commit -m "feat(msb): translate TLS-interception and strict-hostname policy to the SDK"
```

---

### Task 3: The config surface in `viperconfig`

**Files:**
- Modify: `internal/viperconfig/config.go`, `internal/viperconfig/validate.go`, `internal/viperconfig/configkeys_test.go`, `internal/viperconfig/env_names_test.go`, `internal/viperconfig/resolver_flow_test.go`

**Interfaces:**
- Consumes: `network.ParseInterceptMode`, `network.ValidateTLSBypass` (Task 1).
- Produces: derived keys `network.intercept-tls` (env `AGENTS_SANDBOX_NETWORK_INTERCEPT_TLS`) and `network.disable-strict` (env `AGENTS_SANDBOX_NETWORK_DISABLE_STRICT`); `network.tls-bypass` is config-file-only (the `env:"-"` tag on `Policy.TLSBypass` already excludes it). Invalid mode or empty bypass entry fails at config load.

- [ ] **Step 1: Write the failing tests** in `internal/viperconfig/resolver_flow_test.go`:

```go
func TestNetworkInterceptTLSKeyAndEnv(t *testing.T) {
    t.Setenv("AGENTS_SANDBOX_NETWORK_INTERCEPT_TLS", "on")
    r, err := NewResolver(nil, configpaths.Get(), "")
    if err != nil {
        t.Fatal(err)
    }
    if r.cfg.Network.InterceptTLS != network.InterceptOn {
        t.Fatalf("intercept-tls not decoded, got %q", r.cfg.Network.InterceptTLS)
    }
}

func TestNetworkInterceptTLSRejectsBogus(t *testing.T) {
    t.Setenv("AGENTS_SANDBOX_NETWORK_INTERCEPT_TLS", "sometimes")
    if _, err := NewResolver(nil, configpaths.Get(), ""); err == nil {
        t.Fatal("want error for an unknown intercept-tls value")
    }
}

func TestNetworkDisableStrictEnv(t *testing.T) {
    t.Setenv("AGENTS_SANDBOX_NETWORK_DISABLE_STRICT", "true")
    r, err := NewResolver(nil, configpaths.Get(), "")
    if err != nil {
        t.Fatal(err)
    }
    if !r.cfg.Network.DisableStrict {
        t.Fatal("disable-strict not decoded")
    }
}
```

And in `internal/viperconfig/configkeys_test.go`, (a) extend the config-file-only assertion in `TestConfigEgressListsAreConfigFileOnly` (the existing `network.egress-allow`/`network.egress-deny` loop) to include `network.tls-bypass`, and (b) add `keyNetworkInterceptTLS` and `keyNetworkDisableStrict` to the `want` list in `TestConfigEnvKeysGoldenSet`.

- [ ] **Step 2: Run to verify they fail** — `go test ./internal/viperconfig/ -run 'TestNetworkInterceptTLSKeyAndEnv|TestNetworkInterceptTLSRejectsBogus|TestNetworkDisableStrictEnv'`. Expected: FAIL.

- [ ] **Step 3: Implement.**
  - `config.go`: add constants `keyNetworkInterceptTLS = "network.intercept-tls"`, `keyNetworkTLSBypass = "network.tls-bypass"`, `keyNetworkDisableStrict = "network.disable-strict"`.
  - `validate.go`: add `validateNetworkInterceptTLS` (call `network.ParseInterceptMode` on the raw value, wrapping with `launcher config network.intercept-tls:`) and `validateNetworkTLSBypass` (call `network.ValidateTLSBypass`); call both from `validate`. The bypass value may be a comma-separated string or a YAML/JSON list — reuse `stringListEntries`.
  - Update `configkeys_test.go` and `env_names_test.go` as above; the derived registry picks the fields up from the `network.Policy` tags automatically.

- [ ] **Step 4: Run the tests** — `go test ./internal/viperconfig/`. Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/viperconfig/
git commit -m "feat(config): expose network.intercept-tls, tls-bypass, and disable-strict"
```

---

### Task 4: Recreate pin and host acceptance

**Files:**
- Create: `internal/sandbox/state/network_tls_recreate_test.go` (untagged — the assertion is pure fingerprints and needs no VM)

**Interfaces:**
- Consumes: Tasks 1–3.
- Produces: an untagged regression pin on the recreate, and a recorded host run of the acceptance case.

- [ ] **Step 1: Write the test** asserting the recreate fires for each new field:

```go
func TestNetworkTLSRecreate(t *testing.T) {
    base := network.Policy{Profiles: []network.Profile{network.ProfileNone}}
    withBypass := base
    withBypass.TLSBypass = []string{"*.internal.example"}
    if !NetworkChanged(BuildNetworkState(base), withBypass) {
        t.Error("adding tls-bypass must recreate the VM")
    }
    on := base
    on.InterceptTLS = network.InterceptOn
    if !NetworkChanged(BuildNetworkState(base), on) {
        t.Error("changing intercept-tls must recreate the VM")
    }
    strict := base
    strict.DisableStrict = true
    if !NetworkChanged(BuildNetworkState(base), strict) {
        t.Error("changing disable-strict must recreate the VM")
    }
    if NetworkChanged(BuildNetworkState(withBypass), withBypass) {
        t.Error("an unchanged policy must not recreate the VM")
    }
}
```

- [ ] **Step 2: Run it** — `go test ./internal/sandbox/state/ -run TestNetworkTLSRecreate`. Expected: PASS via the Task 1 fingerprint; keep as a regression pin (it runs under `make test`, unlike an `msb`-tagged test no gate runs).

- [ ] **Step 3 (human accept):** on a KVM host, create a project VM with `network.disable-strict: true` and a `tls-bypass` entry for a plain egress-allowlisted host; confirm the bypassed host is reachable (and, with `disable-strict` removed, is denied). Confirm that adding/removing any of the three settings recreates the VM. Record the result in the PR body.

- [ ] **Step 4: Commit**

```bash
git add internal/sandbox/state/network_tls_recreate_test.go
git commit -m "test(state): pin the TLS-interception recreate"
```

---

### Task 5: Documentation, spec, and the docker recipe

**Files:**
- Modify: `docs/configuration/networking.md`, `docs/configuration/secrets.md`, `.operator-shared/specs/network-and-secrets/interception-and-proxy.md`, `.operator-shared/plans/2026-11-17-learn-mode-program.md`
- Create: `docs/recipes/docker-ca-trust.md`, `changelog.d/<PR>.added.md`

**Interfaces:** none new.

- [ ] **Step 1:** In `docs/configuration/networking.md`, document the three keys in the field table and a short section: `intercept-tls` (`auto`/`on`/`off`, secret-implied default, `off`-with-secrets rejected), `tls-bypass` (exact host or `*.suffix`), `disable-strict`; state that all three are creation-only (a change recreates the VM) and that a bypassed-and-allowlisted host is still denied under strict mode unless `disable-strict` is set. Link the recipe for the docker case.

- [ ] **Step 2:** In `docs/configuration/secrets.md`, replace the inline docker CA note (the "Two things the helper does not cover" paragraph) with a one-line pointer to the new recipe.

- [ ] **Step 3:** Create `docs/recipes/docker-ca-trust.md` modeled on `docs/recipes/java-ca-trust.md` (frontmatter `title`, `description`, `layout: default`, `parent: Recipes`, `nav_order`). Content, as settled in the design:
  - No daemon step is needed: a base-image pull already succeeds (microsandbox installs its CA into the VM system store and the in-VM dockerd reads it).
  - Build context: copy `/usr/local/share/ca-certificates/microsandbox-ca.crt` into the build context and add it to the container's bundle before any HTTPS — Alpine: append to `/etc/ssl/certs/ca-certificates.crt` (no `update-ca-certificates` in the base); Debian/Ubuntu: `COPY` into `/usr/local/share/ca-certificates/` and `update-ca-certificates` (apt's default sources are `http`). Include both Dockerfile snippets.
  - Runtime context (`docker run`): mount `microsandbox-ca.crt` read-only and add it to the bundle at entry, with a one-line example.

- [ ] **Step 4:** Update `.operator-shared/specs/network-and-secrets/interception-and-proxy.md`: add `network.disable-strict` to the surface and its frontmatter `description`; **correct** the sentence claiming a bypass "supersedes the earlier workaround" (it does not — a bypassed host is denied under strict mode); point the docker case at the recipe. Update `.operator-shared/plans/2026-11-17-learn-mode-program.md`: add `DisableStrict bool` to the frozen `Policy`/`NetworkSpec` contract and note the S2 re-scope (controls only, no registry handling, docker case as a recipe).

- [ ] **Step 5:** Create `changelog.d/<PR>.added.md` (use the eventual PR number; a stand-in is fine until the PR exists) describing the new `network.intercept-tls`, `network.tls-bypass`, and `network.disable-strict` keys and the new recipe.

- [ ] **Step 6: Verify** — `make check`. Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add docs/ .operator-shared/ changelog.d/
git commit -m "docs: document TLS-interception control and the docker CA recipe"
```

---

## Self-review

**Coverage.** Every design section maps to a task: the config surface and semantics to Tasks 1–3; the `off`-with-secrets error and the projection to Task 2; fingerprint/creation-only to Tasks 1 and 4; docs, the corrected spec, and the recipe to Task 5. The recipe's three claims (no daemon step, build context, runtime context) were settled empirically and are written down in Task 5.

**Proportion.** Signatures, test assertions, and the empirically verified recipe facts; no function bodies beyond what the tests do not determine.

**Type consistency.** `InterceptMode`/`InterceptAuto`/`InterceptOn`/`InterceptOff` are owned by `network` (Task 1) and mirrored by an `msb`-local type (Task 2), matching the program index's frozen contract; `DisableStrict` is added there too. `Spec()` (Task 2) is the only place the mirror conversion happens.
