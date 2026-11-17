# S1 — SDK channel semantics spike (host-gated)

> **For agentic workers:** This is a host-only validation spike, not an SDD run. Its deliverable is a recorded decision, not code. The probe scripts are throwaway and are deleted or kept only as evidence, per `specs/testing/release-surface-testing.md` precedent for host spikes. A human runs it on a KVM host; an agent cannot.

**Goal:** Settle the four questions that decide the observation channel's topology and the `#151` acceptance, before any channel code is written.

**Architecture:** A throwaway Go program using the real SDK (`-tags msb`) drives the pinned `v0.7.15` surfaces directly — `WithInterceptTLS`, `NetworkConfig.TLS`, `WithProxy` — and records what the runtime actually does. Findings are folded into the subspecs, not kept in code.

**Tech Stack:** Go 1.27, microsandbox SDK `v0.7.15`, `mitmdump` (SOCKS5/HTTP CONNECT listener), a KVM host with a docker daemon and network.

**Spec:** `.operator-shared/specs/onboarding/learn-mode/observation-channel.md` (the questions) and `.operator-shared/specs/network-and-secrets/interception-and-proxy.md` (the `off` semantics and the composition rule). Evidence table: `.operator-shared/plans/2026-11-15-learn-mode-spike.md`.

## Global Constraints

See `2026-11-17-learn-mode-program.md` → Global Constraints. For this subplan: the spike is host-only (`/dev/kvm` unusable in the dev VM, so the agent writes the script and runbook; the human runs it and pastes the results). Everything is throwaway except the recorded decisions, which land in the subspecs in this PR.

## The four questions (the deliverable)

1. **Topology.** Does the runtime re-originate intercepted traffic to a host endpoint so our observer sees the request (plaintext or our-CA TLS) — topology **T2**? Or must the launcher intercept the guest directly, planting its CA in the guest — **T1**? Evidence: which consumers work, where our endpoint sees the credential header name, whether the guest ever trusts our CA.
2. **`off` semantics.** With a secret configured (interception on by implication), does `NetworkConfig.TLS = nil` actually disable interception, or does the runtime re-enable it? If `off` cannot be expressed, record that, and `#151` must reject `off`-with-secrets with an actionable error rather than claim to disable it.
3. **Proxy composition.** Is a proxy the launcher configures reachable under `bare none` without an `egress-allow` entry for it? If not, the composition rule is "auto-allow the proxy address" or "fail with an actionable error" — record which.
4. **Strict-hostname bypass.** Does a host that is both `tls-bypass`ed and egress-allowlisted still pass under the runtime's strict-hostname fail-closed (v0.7.3 #1643)? #151 lists this as an open question, so record pass/fail: S2's `tls-bypass` contract depends on it.

---

### Task 1: Write the probe program

**Files:**
- Create: `spike/learn-mode/s8_sdk_channel/main.go` (`-tags msb`, throwaway)

**Interfaces:**
- Consumes: `msbSdk.WithInterceptTLS`, `msbSdk.NetworkConfig{TLS: *TLSConfig}`, `msbSdk.HTTPConnectProxy`, `msbSdk.SOCKS5Proxy`, `msbSdk.OutboundProxy.Credentials`, `msbSdk.SecretSourceEnv`.
- Produces: printed per-case verdicts for the four questions.

- [ ] **Step 1:** Write the program to run, in order: (a) a sandbox with a configured secret and `TLS = nil` under `bare none`, requesting the secret's host, reporting whether interception occurred; (b) a sandbox with `WithProxy(HTTPConnectProxy("<host>:<port>"))` under `none`, with and without an `egress-allow` entry for the proxy address; (c) the same with `SOCKS5Proxy`; (d) the T2 chain (`TLS` set with `UpstreamCACerts` trusting our proxy CA) with a real request through a host `mitmdump`; (e) a sandbox with a `TLS` bypass entry for a host that is also egress-allowlisted, requesting that host, reporting whether it passes.
- [ ] **Step 2:** Each case prints one line: case id, whether the connection completed, whether our endpoint saw the credential header name, and the guest's error if any.
- [ ] **Step 3:** Add a `run.sh` that starts `mitmdump` for the required modes and invokes the program, so the human runs one command.
- [ ] **Step 4: Commit.**

```bash
git add spike/learn-mode/s8_sdk_channel/
git commit -m "spike(learn-mode): probe SDK interception and proxy semantics"
```

---

### Task 2: Run it on the host and record the decisions

**Files:**
- Modify: `.operator-shared/specs/onboarding/learn-mode/observation-channel.md` (topology, `off` semantics, composition rule)
- Modify: `.operator-shared/specs/network-and-secrets/interception-and-proxy.md` (the composition rule and the `off` contract)
- Modify: `.operator-shared/plans/2026-11-15-learn-mode-spike.md` (Results table rows)

- [ ] **Step 1 (human):** On a KVM host, run `spike/learn-mode/s8_sdk_channel/run.sh` and paste the output into the PR.
- [ ] **Step 2:** Record T1 or T2 in `observation-channel.md`, with the one-line evidence.
- [ ] **Step 3:** Record the `off` semantics in `interception-and-proxy.md`; if `off` is not expressible, state that `#151` rejects `off`-with-secrets (this becomes S2's `networkConfig` error branch).
- [ ] **Step 4:** Record the composition rule in `interception-and-proxy.md` (auto-allow vs fail-fast).
- [ ] **Step 5:** Record the strict-hostname bypass result in `interception-and-proxy.md` (does a bypassed-but-allowlisted host still pass?); S2 consumes it.
- [ ] **Step 6: Verify and commit.**

Run: `make brain-check`
Expected: `brain-check: ok`

```bash
git add .operator-shared/
git commit -m "docs(operator): record SDK channel semantics"
```

---

## Self-review

**Coverage.** The four questions are the channel decisions S1 owns: topology, `off`, proxy composition, and the #151 strict-hostname bypass. The spec's other open-validation items — per-harness read-back, QUIC, pinning, `msb-trust java`/`chrome` — are owned elsewhere (S5 and S4's spec section) and are not re-tested here.

**Proportion.** No code beyond the throwaway probe; the decisions are the artifact.

**Type consistency.** Consumes only SDK symbols; produces no repo types.
