# S2 — TLS-interception control (#151)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking. A fresh implementer per task; a fresh reviewer gates each task; one whole-subplan review at the end. Host-only steps are a human accept step.

**Goal:** Make TLS interception explicit and controllable — `network.intercept-tls` (`auto`/`on`/`off`) and `network.tls-bypass` — folding the settings into the network fingerprint, and retire the in-VM docker CA workaround.

**Architecture:** A new launcher policy type in `internal/sandbox/network` flows through the existing pipeline (`network.Policy` → `msb.NetworkSpec` → SDK `NetworkConfig.TLS`), so the recreate path applies it and `state.NetworkChanged` keeps a stale VM from surviving a change. The launcher never imports the SDK outside `internal/sandbox/msb`.

**Tech Stack:** Go 1.27, `internal/sandbox/network`, `internal/sandbox/msb`, `internal/viperconfig`, microsandbox SDK `v0.7.15` (`NetworkConfig.TLS`, `TLSConfig`).

**Spec:** `.operator-shared/specs/network-and-secrets/interception-and-proxy.md`; issue #151. Channel context: `.operator-shared/specs/onboarding/learn-mode/observation-channel.md`.

**Consumes (from S1):** the `off`-with-secrets decision — if `off` is not expressible, Task 2 returns an error naming the limitation instead of silently leaving interception on — and the strict-hostname bypass result, which the `tls-bypass` contract must state.
**Produces (for S3/S4 and the program index):** `network.InterceptMode` (`InterceptAuto`/`InterceptOn`/`InterceptOff`), `network.Policy.InterceptTLS`, `network.Policy.TLSBypass`, `msb.NetworkSpec.InterceptTLS`, `msb.NetworkSpec.TLSBypass`.

## Global Constraints

See `2026-11-17-learn-mode-program.md` → Global Constraints.

## Review Focus

1. **`intercept-tls: off` while secrets are configured** — must truly disable, or fail with an actionable error; never silently leave interception on. (Task 2.)
2. **A stale VM** — adding `tls-bypass` or changing `intercept-tls` must recreate the VM. (Task 1 fingerprint; Task 4 verification.)
3. **The in-VM docker build** — the case #151 exists for; the CA must not break `docker build` pulls. (Task 4.)

---

### Task 1: TLS-interception policy in `network`

**Files:**
- Create: `internal/sandbox/network/tls.go`, `internal/sandbox/network/tls_test.go`
- Modify: `internal/sandbox/network/network.go`, `internal/sandbox/network/network_test.go`

**Interfaces:**
- Consumes: nothing new.
- Produces:
  ```go
  type InterceptMode string
  const (InterceptAuto InterceptMode = "auto"; InterceptOn = "on"; InterceptOff = "off")
  func ParseInterceptMode(s string) (InterceptMode, error) // "", "auto", "on", "off"; else error
  func ValidateTLSBypass(in []string) ([]string, error)    // host or "*.suffix"; reject empty
  ```
  `Policy` gains `InterceptTLS InterceptMode \`mapstructure:"intercept-tls"\`` and `TLSBypass []string \`mapstructure:"tls-bypass" env:"-"\``; `Fingerprint`/`Describe`/`Empty` include both. This task must compile on its own, so it does **not** touch `Policy.Spec()` or any `msb` type — the `msb` mirror, the `NetworkSpec` fields, and the `Spec()` wiring are Task 2's.

- [ ] **Step 1: Write the failing test**

```go
func TestPolicyFingerprintIncludesTLSInterception(t *testing.T) {
    base := Policy{Profiles: []Profile{ProfileNone}}
    withBypass := base
    withBypass.TLSBypass = []string{"*.internal.example"}
    if base.Fingerprint() == withBypass.Fingerprint() { t.Fatal("tls-bypass must change the fingerprint") }
    on := base
    on.InterceptTLS = InterceptOn
    if base.Fingerprint() == on.Fingerprint() { t.Fatal("intercept-tls must change the fingerprint") }
}
func TestParseInterceptModeRejectsUnknown(t *testing.T) {
    if _, err := ParseInterceptMode("sometimes"); err == nil { t.Fatal("want error") }
}
```

- [ ] **Step 2: Run to verify it fails** — `go test ./internal/sandbox/network/ -run 'TestPolicyFingerprintIncludesTLSInterception|TestParseInterceptModeRejectsUnknown'`. Expected: FAIL.
- [ ] **Step 3: Implement** `tls.go` and extend `Policy`, `Spec`, `Fingerprint` (sorted `intercept-tls=<mode>`, `tls-bypass=<host>` lines), `Describe`, `Empty`.
- [ ] **Step 4: Run the tests** — `go test ./internal/sandbox/network/`. Expected: PASS.
- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/network/
git commit -m "feat(network): add TLS-interception policy fields"
```

---

### Task 2: Project TLS interception to the SDK in `msb`

**Files:**
- Modify: `internal/sandbox/msb/network_values.go`, `internal/sandbox/msb/translate.go`, `internal/sandbox/msb/translate_test.go`

**Interfaces:**
- Consumes: `network.InterceptMode` (Task 1).
- Produces: an `msb`-local `InterceptMode` mirror (`InterceptAuto`/`InterceptOn`/`InterceptOff` — no SDK type crosses the seam), the `NetworkSpec.InterceptTLS`/`TLSBypass` fields, and the `network.Policy.Spec()` wiring that populates them from the Task 1 `Policy` fields. `networkConfig(spec NetworkSpec) (*msbSdk.NetworkConfig, error)` sets `cfg.TLS`: `nil` when mode is `auto` and no bypass; `&msbSdk.TLSConfig{Bypass: spec.TLSBypass}` when mode is `on` or bypass is set. `off` follows S1's decision; if inexpressible, `networkConfig` returns an error (pinned by a test asserting the message).

- [ ] **Step 1: Write the failing test**

```go
func TestNetworkConfigSetsTLSForInterceptOn(t *testing.T) {
    cfg, err := networkConfig(NetworkSpec{InterceptTLS: InterceptOn, TLSBypass: []string{"*.internal"}})
    if err != nil { t.Fatal(err) }
    if cfg.TLS == nil || len(cfg.TLS.Bypass) != 1 { t.Fatalf("want TLS with bypass, got %+v", cfg.TLS) }
}
func TestNetworkConfigLeavesTLSNilForAuto(t *testing.T) {
    cfg, _ := networkConfig(NetworkSpec{})
    if cfg.TLS != nil { t.Fatal("auto with no bypass must not set TLS") }
}
```

- [ ] **Step 2: Run to verify it fails** — `go test ./internal/sandbox/msb/ -run 'TestNetworkConfigSetsTLSForInterceptOn|TestNetworkConfigLeavesTLSNilForAuto'`. Expected: FAIL.
- [ ] **Step 3: Implement** the spec fields, the mirror type, and the `networkConfig` TLS branch; keep the launcher rule order unchanged; implement the `off` branch per S1.
- [ ] **Step 4: Run the tests** — `go test ./internal/sandbox/msb/`. Expected: PASS.
- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/msb/
git commit -m "feat(msb): translate launcher TLS-interception policy to the SDK"
```

---

### Task 3: `network.intercept-tls` and `network.tls-bypass` config surface

**Files:**
- Modify: `internal/viperconfig/config.go`, `internal/viperconfig/validate.go`, `internal/viperconfig/configkeys_test.go`, `internal/viperconfig/env_names_test.go`, `internal/viperconfig/resolver_flow_test.go`

**Interfaces:**
- Consumes: `network.ParseInterceptMode`, `network.ValidateTLSBypass` (Task 1).
- Produces: derived keys `network.intercept-tls` (env `AGENTS_SANDBOX_NETWORK_INTERCEPT_TLS`) and `network.tls-bypass` (config-file-only). Invalid values fail at config load.

- [ ] **Step 1: Write the failing test**

```go
func TestNetworkInterceptTLSKeyAndEnv(t *testing.T) {
    t.Setenv("AGENTS_SANDBOX_NETWORK_INTERCEPT_TLS", "on")
    r, err := NewResolver(nil, configpaths.Get(), "")
    if err != nil { t.Fatal(err) }
    if r.cfg.Network.InterceptTLS != network.InterceptOn { t.Fatal("mode not decoded") }
}
func TestNetworkInterceptTLSRejectsBogus(t *testing.T) {
    t.Setenv("AGENTS_SANDBOX_NETWORK_INTERCEPT_TLS", "sometimes")
    if _, err := NewResolver(nil, configpaths.Get(), ""); err == nil { t.Fatal("want error") }
}
```

- [ ] **Step 2: Run to verify it fails** — `go test ./internal/viperconfig/ -run 'TestNetworkInterceptTLS'`. Expected: FAIL.
- [ ] **Step 3: Implement** the fields, decode/validation wiring, and updated key/env tests.
- [ ] **Step 4: Run the tests** — `go test ./internal/viperconfig/`. Expected: PASS.
- [ ] **Step 5: Commit**

```bash
git add internal/viperconfig/
git commit -m "feat(config): expose network.intercept-tls and network.tls-bypass"
```

---

### Task 4: Recreate pin and the docker build

**Files:**
- Create: `internal/sandbox/state/network_tls_recreate_test.go` (untagged — the assertion is pure fingerprints and needs no VM)

**Interfaces:**
- Consumes: Tasks 1–3.
- Produces: an untagged regression pin on the recreate, and a recorded host run of the acceptance case.

- [ ] **Step 1: Write the failing test** asserting `state.NetworkChanged(state.BuildNetworkState(policyWithoutBypass), policyWithBypass)` is true.
- [ ] **Step 2: Run it** — `go test ./internal/sandbox/state/ -run TestNetworkTLSRecreate`. Expected: PASS via the Task 1 fingerprint (keep as a regression pin; it runs under `make test`, unlike an `msb`-tagged `vm` test no gate runs).
- [ ] **Step 3 (human accept):** on a KVM host, create a project VM with `intercept-tls: on`; confirm an in-VM `docker build` pulling a base image succeeds (the #151 case), and a `tls-bypass` entry makes a listed host skip interception. Record in the PR body.
- [ ] **Step 4: Commit**

```bash
git add internal/sandbox/state/network_tls_recreate_test.go
git commit -m "test(state): pin the TLS-interception recreate"
```

---

### Task 5: Document #151

**Files:**
- Modify: `docs/configuration/networking.md`, `docs/configuration/secrets.md` (the "Containers built inside the VM" docker-CA note)
- Create: `changelog.d/<PR>.added.md`

- [ ] **Step 1:** Document `network.intercept-tls` (auto/on/off and the secret-implied default) and `network.tls-bypass`; update the docker-CA note in `docs/configuration/secrets.md` if bypass supersedes it.
- [ ] **Step 2: Verify** — `make check`. Expected: PASS.
- [ ] **Step 3: Commit**

```bash
git add docs/ changelog.d/
git commit -m "docs: document TLS-interception control"
```

---

## Self-review

**Coverage.** #151's proposed change (explicit control, `tls-bypass`, fingerprint the TLS settings, strict-hostname verification) maps to Tasks 1–3; the strict-hostname verification is S1's fourth question, recorded in the spec and consumed here. The docker CA workaround is Task 4/5.

**Proportion.** Only signatures and test assertions; no bodies.

**Type consistency.** `InterceptMode`/`InterceptAuto`/`InterceptOn`/`InterceptOff` are owned by `network` (Task 1) and mirrored by an `msb`-local type (Task 2), matching the frozen contract in the program index. S3 reuses the same `Policy`/`Spec` seam.
