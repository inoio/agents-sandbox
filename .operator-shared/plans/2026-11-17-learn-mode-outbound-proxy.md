# S3 — Outbound proxy (#149)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking. A fresh implementer per task; a fresh reviewer gates each task; one whole-subplan review at the end. Host-only steps are a human accept step.

**Goal:** A first-class outbound proxy for sandbox egress — SOCKS4/SOCKS5 and HTTP CONNECT — configured by a `proxy:` block or `--proxy`, fingerprinted as creation-only, with a reachability guard against the network policy.

**Architecture:** Same pipeline as S2 (`network.Policy` → `msb.NetworkSpec` → SDK `WithProxy`), serialized after S2 because they edit the same files. The proxy is a single value (the SDK's `SandboxConfig.Proxy`), so it moves from `sandboxConfigNotModeled` to `sandboxConfigProjected`.

**Tech Stack:** Go 1.27, `internal/sandbox/network`, `internal/sandbox/msb`, `internal/viperconfig`, `internal/runoptions`, microsandbox SDK `v0.7.15` (`WithProxy`, `OutboundProxy`, `SecretSourceEnv`).

**Spec:** `.operator-shared/specs/network-and-secrets/interception-and-proxy.md`; issue #149.

**Consumes (from S2):** the `network.Policy`/`msb.NetworkSpec` seam and `Fingerprint`/`Describe`.
**Consumes (from S1):** the proxy-reachability composition decision (auto-allow the proxy vs fail with an actionable error).
**Produces (for S4 and the program index):** `network.ProxyProtocol` (`ProxyHTTPConnect`/`ProxySOCKS5`/`ProxySOCKS4`), `network.Proxy`, `network.Policy.Proxy`, `msb.ProxySpec`, `msb.NetworkSpec.Proxy`.

## Global Constraints

See `2026-11-17-learn-mode-program.md` → Global Constraints. **Serialize after S2** — do not branch this from `main` while S2 is open.

## Review Focus

1. **A proxy address the policy denies** — silently denies all egress; auto-allow it or fail with an actionable error. (Task 1.)
2. **Credentials on SOCKS4** — the SDK carries credentials for SOCKS5 and HTTP CONNECT but has no credential support for SOCKS4; fail fast at config time, not at creation. (Task 3.)
3. **A stale VM** — a proxy change must recreate the VM. (Task 1 fingerprint; Task 4.)

---

### Task 1: Outbound-proxy policy in `network`

**Files:**
- Create: `internal/sandbox/network/proxy.go`, `internal/sandbox/network/proxy_test.go`
- Modify: `internal/sandbox/network/network.go`, `internal/sandbox/network/network_test.go`

**Interfaces:**
- Consumes: nothing new.
- Produces:
  ```go
  type ProxyProtocol string
  const (ProxyHTTPConnect ProxyProtocol = "http-connect"; ProxySOCKS5 = "socks5"; ProxySOCKS4 = "socks4")
  type Proxy struct {
      Protocol    ProxyProtocol `mapstructure:"protocol"`
      Address     string        `mapstructure:"address"`
      Username    string        `mapstructure:"username"`
      PasswordEnv string        `mapstructure:"password-env"`
  }
  func ParseProxy(s string) (*Proxy, error)      // "protocol://host:port"
  ```
  `Policy` gains `Proxy Proxy \`mapstructure:"proxy"\`` — value-typed so the derived key registry recurses into `network.proxy.*`; `Fingerprint`/`Describe`/`Empty` include it. This task must compile on its own, so it does **not** touch `Policy.Spec()` or any `msb` type. `Validate` enforces the composition rule from S1.

- [ ] **Step 1: Write the failing test**

```go
func TestProxyCredentialsRejectedForSOCKS4(t *testing.T) {
    policy := Policy{Profiles: []Profile{ProfileNone}, Proxy: Proxy{
        Protocol: ProxySOCKS4, Address: "10.0.0.1:1080", Username: "u", PasswordEnv: "P",
    }}
    if err := policy.Validate(); err == nil { t.Fatal("socks4 credentials must be rejected") }
}
func TestProxyAddressMustBeReachableUnderNone(t *testing.T) {
    policy := Policy{Profiles: []Profile{ProfileNone}, Proxy: Proxy{Protocol: ProxySOCKS5, Address: "10.0.0.1:1080"}}
    // Per S1's recorded decision: either Validate rejects it, or the address is auto-allowed.
    err := policy.Validate()
    allowed := slices.Contains(policy.Effective().EgressAllow, "10.0.0.1:1080")
    if err == nil && !allowed { t.Fatal("a proxy the policy accepts must be auto-added to egress-allow") }
    if err != nil && allowed { t.Fatal("Validate rejected an auto-allowed proxy") }
}
func TestProxyChangesFingerprint(t *testing.T) {
    base := Policy{Profiles: []Profile{ProfileNone}}
    withProxy := base
    withProxy.Proxy = Proxy{Protocol: ProxySOCKS5, Address: "10.0.0.1:1080"}
    if base.Fingerprint() == withProxy.Fingerprint() { t.Fatal("proxy must change the fingerprint") }
}
```

- [ ] **Step 2: Run to verify it fails** — `go test ./internal/sandbox/network/ -run 'TestProxy'`. Expected: FAIL.
- [ ] **Step 3: Implement** `proxy.go` (parse, validate, credential rules for SOCKS4, composition guard per S1), extend `Policy` and its `Fingerprint`/`Describe`/`Empty`. No `msb` wiring here — Task 2 adds it.
- [ ] **Step 4: Run the tests** — `go test ./internal/sandbox/network/`. Expected: PASS.
- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/network/
git commit -m "feat(network): add outbound-proxy policy"
```

---

### Task 2: Project the proxy to the SDK in `msb`

**Files:**
- Modify: `internal/sandbox/msb/network_values.go`, `internal/sandbox/msb/translate.go`, `internal/sandbox/msb/translate_test.go`, `internal/sandbox/network/network.go`

**Interfaces:**
- Consumes: `network.Proxy`, `network.ProxyProtocol`, `network.Policy.Proxy` (Task 1).
- Produces: `msb.ProxyProtocol` (mirror), `msb.ProxySpec{Protocol ProxyProtocol; Address, Username, PasswordEnv string}`, `NetworkSpec.Proxy ProxySpec`, and the `network.Policy.Spec()` wiring that fills it from `Policy.Proxy`; `sandboxOptions` appends `msbSdk.WithProxy(...)` (choosing `HTTPConnectProxy`/`SOCKS5Proxy`/`SOCKS4Proxy`, `.Credentials(username, msbSdk.SecretSourceEnv(env))` when set); `"Proxy"` moves from `sandboxConfigNotModeled` to `sandboxConfigProjected`.

- [ ] **Step 1: Write the failing test**

```go
func TestSandboxOptionsProjectProxy(t *testing.T) {
    spec := SandboxSpec{Image: "alpine:3.19", Network: NetworkSpec{
        Proxy: ProxySpec{Protocol: ProxySOCKS5, Address: "10.0.0.1:1080", Username: "u", PasswordEnv: "PW"},
    }}
    opts, err := sandboxOptions(spec)
    if err != nil { t.Fatal(err) }
    var cfg msbSdk.SandboxConfig
    for _, opt := range opts { opt(&cfg) }
    if cfg.Proxy == nil { t.Fatal("proxy not projected") }
}
```

- [ ] **Step 2: Run to verify it fails** — `go test ./internal/sandbox/msb/ -run 'TestSandboxOptionsProjectProxy|TestSDKStructFieldsAreClassified'`. Expected: FAIL.
- [ ] **Step 3: Implement** the `msb` mirror and spec field, the `Policy.Spec()` wiring, the option, and the map move; `TestSDKStructFieldsAreClassified` still passes.
- [ ] **Step 4: Run the tests** — `go test ./internal/sandbox/msb/`. Expected: PASS.
- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/msb/
git commit -m "feat(msb): translate the launcher proxy to the SDK"
```

---

### Task 3: `proxy.*` and `--proxy` config surface

**Files:**
- Modify: `internal/viperconfig/config.go`, `internal/viperconfig/validate.go`, `internal/runoptions/runoptions.go`, `internal/viperconfig/*_test.go`, `cmd/agents-sandbox/cli_run_options_test.go`

**Interfaces:**
- Consumes: `network.ParseProxy` (Task 1).
- Produces: derived keys `network.proxy.protocol`, `network.proxy.address`, `network.proxy.username`, `network.proxy.password-env` (env `AGENTS_SANDBOX_NETWORK_PROXY_*`); a `--proxy protocol://host:port` flag overriding the block; validation failing fast on credentials SOCKS4 cannot carry.

- [ ] **Step 1: Write the failing test**

```go
func TestProxyFlagWinsOverBlock(t *testing.T) {
    // network.proxy block socks5://10.0.0.1:1080, --proxy http-connect://10.0.0.2:3128
    // expect resolved Policy.Proxy.Protocol == http-connect
}
func TestProxySOCKS4WithPasswordEnvFailsFast(t *testing.T) {
    // AGENTS_SANDBOX_NETWORK_PROXY_{PROTOCOL,ADDRESS,USERNAME,PASSWORD_ENV} = socks4,…; expect load error
}
```

- [ ] **Step 2: Run to verify it fails** — `go test ./internal/viperconfig/ ./internal/runoptions/ -run 'TestProxy'`. Expected: FAIL.
- [ ] **Step 3: Implement** the keys, flag binding, `resolveNetwork` extension, validation.
- [ ] **Step 4: Run the tests** — `go test ./internal/viperconfig/ ./internal/runoptions/ ./cmd/agents-sandbox/ -run 'Proxy'`. Expected: PASS.
- [ ] **Step 5: Commit**

```bash
git add internal/viperconfig/ internal/runoptions/runoptions.go cmd/agents-sandbox/cli_run_options_test.go
git commit -m "feat(config): expose the outbound proxy as proxy.* and --proxy"
```

---

### Task 4: Host verification — proxy reachability and composition

**Files:**
- Create: `internal/sandbox/vm/vm_network_proxy_msb_test.go` (`msb` build tag)

**Interfaces:**
- Consumes: Tasks 1–3.
- Produces: an `msb`-tagged test and a recorded host run.

- [ ] **Step 1: Write the failing test** asserting a proxy address absent from a `none` policy either errors or is auto-added, per S1's decision.
- [ ] **Step 2: Run it** — `CGO_ENABLED=1 go test ./internal/sandbox/vm/ -tags msb -run TestNetworkProxy`. Expected: PASS after wiring.
- [ ] **Step 3 (human accept):** on a KVM host, start `mitmdump --mode socks5`; run a VM with `--proxy socks5://<host>:<port>`; confirm egress traverses it and a non-allow-listed destination is denied. Record in the PR body.
- [ ] **Step 4: Commit**

```bash
git add internal/sandbox/vm/vm_network_proxy_msb_test.go
git commit -m "test(vm): pin outbound-proxy reachability and composition"
```

---

### Task 5: Document #149

**Files:**
- Modify: `docs/configuration/networking.md`, `docs/configuration/home-provisioning.md` (cross-link #30)
- Create: `changelog.d/<PR>.added.md`

- [ ] **Step 1:** Document `network.proxy.*`/`--proxy`, the protocol/credential matrix (credentials for SOCKS5 and HTTP CONNECT; none for SOCKS4), and the composition rule under `none`.
- [ ] **Step 2: Verify** — `make check`. Expected: PASS.
- [ ] **Step 3: Commit**

```bash
git add docs/ changelog.d/
git commit -m "docs: document the outbound proxy"
```

---

## Self-review

**Coverage.** #149's proposed change (config block + flag, credential fail-fast, composition guard, fingerprint + creation-only, docs) maps to Tasks 1–5. The VPN cross-link (#30) is Task 5.

**Proportion.** Signatures and assertions only.

**Type consistency.** `ProxyProtocol`/`Proxy` (Task 1) mirror into `msb.ProxySpec` (Task 2), per the program index's frozen contract. Shares the `Policy`/`Spec` seam with S2 without redefining it.
