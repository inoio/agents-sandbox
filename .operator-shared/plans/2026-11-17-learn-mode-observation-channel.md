# S4 — The observation channel

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking. A fresh implementer per task; a fresh reviewer gates each task; one whole-subplan review at the end. Host-only steps are a **human accept** step.

**Goal:** A launcher-owned host proxy that terminates TLS for the sandbox's egress, records which destination each request used and which credential-bearing header it carried (names only), points the sandbox at itself at creation, and hands the scrubbed capture to the after-run analysis.

**Architecture:** A new `internal/sandbox/channel` package runs an `elazarl/goproxy` MITM proxy on a host-loopback port with a CA it generates per session. Under the settled **T2** topology the runtime terminates the guest's connection and re-originates to our proxy, so the **runtime** must trust our CA as an upstream CA: the channel adds a `TLSConfig.UpstreamCACerts` projection to the `msb` seam and forces interception on. The launcher never plants its CA in the guest. The proxy's address and CA are exposed as an **in-memory network overlay** value (`Observer.Overlay()`, never written to the user's config); the scrubbed `Observation` is persisted for the after-run and any raw capture is discarded.

**Ownership split with S7.** The channel is a **library**: the proxy, the CA, the capture/scrub, the overlay value, and the artefact store (all testable without a run). **S7 owns the invocation** — starting the observer before `prepareSandbox` (`session/run.go:212`), assigning `opts.Network = obs.Overlay()` there, and calling `Stop()`/`Save` at teardown — because S7 already edits both ends of `session.Run` for `--learn` and the after-run, and because wiring with no flag would be unreachable production code (the S5 dead-code-marker problem again). The one thing S4 must state for its caller is the ordering contract: **apply `Overlay()` to the run's `Network` before the VM is prepared** (the overlay is creation-only; applying it later is a no-op). That contract lives in the `Observer` doc comment.

**Tech Stack:** Go 1.27, `github.com/elazarl/goproxy` (BSD-3; already in the module graph as a `go-git` test dep — promote to direct), `crypto/tls`/`crypto/x509`, `internal/sandbox/{network,msb,session,state}`, microsandbox SDK `v0.7.16` (`TLSConfig.UpstreamCACerts`).

**Spec:** `.operator-shared/specs/onboarding/learn-mode/observation-channel.md` (the facts, approaches, topology, CA trust, capture artefact, constraints, open validation); hub `.operator-shared/specs/onboarding/learn-mode.md` (facts 4–7, invariants 3 and 4); `.operator-shared/specs/network-and-secrets/interception-and-proxy.md` (the T2 trust and strict-hostname interaction).

**Consumes (landed):** `network.Policy`/`Proxy`/`InterceptMode` (S2, S3), the `msb` seam and its SDK classification (`sandboxConfigProjected`/`NotModeled`), the recreate path (`state.NetworkChanged`/`reconfig`), `session.startHostOpener` (the host-channel lifecycle precedent), `configpaths.UserStateDir`.
**Produces (for S6/S7 and the program index):** `channel.Observation`/`Destination`/`Attribution` (the frozen scrubbed-artefact contract); `channel.Start`/`Handle.Stop`/`Handle.CAPEM`; `channel.Save`/`Load`/`Prune`; `network.Policy.WithUpstreamCACerts`/`UpstreamCACerts`; `msb.NetworkSpec.UpstreamCACerts`.

## Global Constraints

See `2026-11-17-learn-mode-program.md` → Global Constraints. Specifically:
- **The recorded artefact holds destinations, request paths, and credential-bearing header NAMES** — never request bodies and never header values; a raw flow capture is scrubbed (here: never serialised) before it is kept (hub invariant 3). The `Observation` type has no field that can carry a value.
- The artefact stays on the host, is deleted after analysis by default, and is never uploaded.
- **The launcher's config is never written for the overlay** (`observation-channel.md` §Prerequisites): the proxy address, forced interception, and the CA reach creation in memory only.
- **The guest is not modified.** T2: the runtime trusts our CA (`TLSConfig.UpstreamCACerts`); no startup hook, no `/.msb/tls/ca.pem` edit, no `msb-trust`.
- `internal/sandbox/msb` is the only package that imports the microsandbox SDK. Any new SDK field is classified in `sandboxConfigProjected`/`sandboxConfigNotModeled` (`TestSDKStructFieldsAreClassified`).
- Every field that reaches the VM is in `network.Policy.Fingerprint()` (creation-only).
- `make fmt`, `make lint`, `make check` — never `go fmt`/`go vet` by hand.

## Design decisions (calls made; correct if wrong)

1. **Proxy library: `github.com/elazarl/goproxy`** (BSD-3-Clause). It is already in the module graph as a test-only dep of `go-git`, so adopting it adds no new module; it has HTTP/2 MITM (`h2.go`) and a small tree (`x/net`, `testify`). Promote it to a direct dependency.
2. **Topology T2, no guest modification.** Our CA is trusted by the **runtime** through a new `TLSConfig.UpstreamCACerts` projection. The spec's T1 CA-planting section is a fallback, not this path.
3. **The overlay is in-memory only.** The channel builds an overlay `network.Policy` (`InterceptTLS = on`, `Proxy = <loopback addr>`, the CA) and the run mutates `opts.Network` with it; nothing is written to config. The resulting network fingerprint **is** recorded (it reflects the running VM), so the first ordinary run afterwards sees the overlay gone and recreates — which is the reconfiguration path the spec names.
4. **The CA and the overlay field are off the user surface.** `network.Policy` gains an **unexported, untagged** `upstreamCACerts []string` field with `WithUpstreamCACerts`/`UpstreamCACerts`; the derived key registry must ignore it (asserted by a test). It is folded into `Fingerprint`.
5. **The overlay lives in `network` and the CA-projection in `msb`; the channel owns the proxy, the capture, the artefact, and the store.** The channel exposes `Start`/`Observer`/`Overlay`/`Stop`/`Save`; **S7** wires them into `session.Run` (start before `prepareSandbox`, `opts.Network = Overlay()`, `Stop`/`Save` at teardown), mirroring `session.startHostOpener`'s shape.
6. **The artefact persists as the scrubbed `Observation` only**, under `<UserStateDir>/learn/<checkoutSlug>/<sessionStartUnix>.json`; no raw capture is ever written. S7 owns the session record that names which artefact to analyse.
7. **HTTPS moves to `crypto/tls` per host via goproxy's CA;** the upstream transport is goproxy's default. Blocking QUIC (`TLSConfig.BlockQUIC`) and an interception-port list are **out of scope** here and recorded as the spec's open validation.

## Review Focus

1. **A header value reaching the artefact** — the recorded `Observation` must never carry a value, body, or query. (Tasks 1, 2.)
2. **A streaming (SSE) response** — must not be buffered; the after-run agent turn is the untested case. (Task 2, Task 7.)
3. **A non-intercepted destination** (a non-HTTP protocol, or a client that pins certificates) — must not crash the proxy, must be recorded as unattributed, and the fallback is config-derived. (Tasks 1, 2.)
4. **The overlay leaking into the user's config or a stale VM surviving it** — the overlay must not be written, and its fingerprint must force a recreate when it is removed. (Tasks 4, 5, 6.)
5. **A recreate while another client is attached** — the learning overlay goes through the existing keep/quit/restart prompt naming the temporary settings. (Task 6.)
6. **The capture surviving a crash / the raw capture at rest** — only the scrubbed artefact is on disk; a killed launcher leaves no values. (Task 6.)

---

### Task 1: The scrubbed artefact

**Files:**
- Create: `internal/sandbox/channel/observation.go`, `internal/sandbox/channel/observation_test.go`

**Interfaces:**
- Consumes: nothing new.
- Produces: the frozen types (`Destination`, `Attribution`, `Observation`) exactly as in the program index's "Cross-subplan contracts" block, plus:

```go
// recorder accumulates the artefact as requests are observed. Its methods take the
// destination and the request's header NAMES; no value is ever passed in.
type recorder struct{ obs Observation }
func (r *recorder) observe(host, ip, path, protocol string, headerNames []string) *Destination
func (r *recorder) deny(host, ip, path string)
func (r *recorder) result(obs Observation) Observation // sorts for a deterministic artefact
```

- [ ] **Step 1: Write the failing test** — observing two requests to `api.example` aggregates to one `Destination{Count: 2}`; `headerNames` land in `Attribution.HeaderNames`; a `deny` sets `Destination.Denied` and no allow; `result` is deterministic (sorted).
- [ ] **Step 2: Run to verify it fails** — `go test ./internal/sandbox/channel/ -run TestRecorder`. Expected: FAIL.
- [ ] **Step 3: Implement** the types and `recorder`.
- [ ] **Step 4: Run** — `go test ./internal/sandbox/channel/`. Expected: PASS.
- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/channel/
git commit -m "feat(channel): add the scrubbed observation artefact"
```

---

### Task 2: The terminating proxy

**Files:**
- Create: `internal/sandbox/channel/proxy.go`, `internal/sandbox/channel/proxy_test.go`
- Modify: `go.mod`, `go.sum` (promote `github.com/elazarl/goproxy` to direct)

**Interfaces:**
- Consumes: `recorder` (Task 1).
- Produces:

```go
// Handle is a running observation proxy.
type Handle struct {
    Addr    string // host:port the sandbox's Proxy.Address points at
    caPEM   string // our CA, for the runtime to trust as an upstream CA
}
// Start binds a proxy on a host-loopback port with a freshly generated CA and
// returns a handle. Stop tears it down and returns the scrubbed artefact.
func Start(bindHost string) (Handle, error)
func (h Handle) Stop() Observation
func (h Handle) CAPEM() string
```

- [ ] **Step 1: Write the failing test** — `httptest.NewTLSServer` as a fake upstream; a client that trusts the proxy CA makes a request through `Handle.Addr` and the upstream is reached; `Stop()` returns an `Observation` naming the upstream host with the credential-bearing header name (`authorization`) and **no value**. A second test drives an SSE-style response and asserts the bytes arrive incrementally (a `Flush` per chunk), not buffered.
- [ ] **Step 2: Run to verify it fails** — `go test ./internal/sandbox/channel/ -run TestProxy`. Expected: FAIL.
- [ ] **Step 3: Implement** `Start`/`Stop`: generate a CA (+ per-host leaf on demand via goproxy's `SetCA`), `goproxy.NewProxyHttpServer()` with `OnRequest().HandleConnect(goproxy.AlwaysMitm)`, a request hook recording destination + header names, a response hook recording success, and a `RoundTrip` error hook recording denial. Bind `net.Listen("tcp", bindHost+":0")`; be idempotent on `Stop`.
- [ ] **Step 4: Run** — `go test ./internal/sandbox/channel/`. Expected: PASS.
- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/channel/ go.mod go.sum
git commit -m "feat(channel): add the goproxy-based terminating proxy"
```

---

### Task 3: Project our CA into the runtime's upstream trust

**Files:**
- Modify: `internal/sandbox/msb/network_values.go`, `internal/sandbox/msb/translate.go`, `internal/sandbox/msb/translate_test.go`

**Interfaces:**
- Consumes: the existing TLS emission (`tlsConfig`/`networkConfig`).
- Produces: `NetworkSpec.UpstreamCACerts []string`; `networkConfig` sets `cfg.TLS.UpstreamCACerts = spec.UpstreamCACerts` (creating `cfg.TLS` when the list is non-empty even if the bypass is empty); `"TLS.UpstreamCACerts"` recorded in the classification register (the parent `TLS` stays projected).

- [ ] **Step 1: Write the failing test** in `translate_test.go`:

```go
func TestNetworkConfigSetsUpstreamCACerts(t *testing.T) {
    cfg, err := networkConfig(NetworkSpec{UpstreamCACerts: []string{"PEM"}})
    if err != nil { t.Fatal(err) }
    if cfg.TLS == nil || len(cfg.TLS.UpstreamCACerts) != 1 { t.Fatalf("want upstream CA, got %+v", cfg.TLS) }
}
```

- [ ] **Step 2: Run to verify it fails** — `go test ./internal/sandbox/msb/ -run 'TestNetworkConfigSetsUpstreamCACerts|TestSDKStructFieldsAreClassified'`. Expected: FAIL.
- [ ] **Step 3: Implement** the field, the emission, and the classification; `sandboxOptions` stops rejecting `off`-with-secrets only for the existing case (unchanged).
- [ ] **Step 4: Run** — `go test ./internal/sandbox/msb/`. Expected: PASS.
- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/msb/
git commit -m "feat(msb): project the runtime's upstream CA trust to the SDK"
```

---

### Task 4: The in-memory observation overlay

**Files:**
- Create: `internal/sandbox/network/overlay.go`, `internal/sandbox/network/overlay_test.go`
- Modify: `internal/sandbox/network/network.go` (the unexported field; `Spec()`; `Fingerprint()`), `internal/sandbox/viperconfig/configkeys_test.go` (no new key)

**Interfaces:**
- Consumes: `Policy`, `Proxy`, `InterceptMode`.
- Produces:

```go
// WithUpstreamCACerts returns a copy of the policy carrying the PEMs the runtime
// must trust as upstream CAs, and forcing interception on for the observation.
func (p Policy) WithUpstreamCACerts(pems ...string) Policy
func (p Policy) UpstreamCACerts() []string
```

  `upstreamCACerts []string` is **unexported and untagged**; `Spec()` copies it into `msb.NetworkSpec.UpstreamCACerts`; `Fingerprint()` adds an `upstream-ca=<hash>` line when set.

- [ ] **Step 1: Write the failing tests**

```go
func TestObservationOverlayChangesFingerprint(t *testing.T) {
    base := Policy{Profiles: []Profile{ProfileNone}}
    ov := base.WithUpstreamCACerts("PEM")
    ov.Proxy = Proxy{Protocol: ProxyHTTPConnect, Address: "127.0.0.1:1234"}
    if base.Fingerprint() == ov.Fingerprint() { t.Fatal("the overlay must change the fingerprint") }
}
func TestUpstreamCACertsIsNotAConfigKey(t *testing.T) {
    // the derived key registry must not contain any "network.upstream-ca" key
}
```

- [ ] **Step 2: Run to verify it fails** — `go test ./internal/sandbox/network/ ./internal/viperconfig/ -run 'TestObservationOverlay|TestUpstreamCACertsIsNotAConfigKey'`. Expected: FAIL.
- [ ] **Step 3: Implement** the field, accessor, `Spec()` copy, the fingerprint line, and the configkey negative test. **If the registry does not ignore an untagged field, carry the CA on the creation spec instead** (a `msb.SandboxSpec` field set by the vm stage) and say so in the PR; the point is that no `network.upstream-ca*` key exists.
- [ ] **Step 4: Run** — `go test ./internal/sandbox/network/ ./internal/viperconfig/ ./internal/sandbox/msb/`. Expected: PASS.
- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/network/ internal/viperconfig/
git commit -m "feat(network): add the in-memory observation overlay"
```

---

### Task 5: The channel facade (`Observer`)

**Files:**
- Create: `internal/sandbox/channel/session.go`, `internal/sandbox/channel/session_test.go`

**Interfaces:**
- Consumes: `channel.Start`/`Handle.Stop` (Task 2), `network.Policy.WithUpstreamCACerts` (Task 4).
- Produces:

```go
// StartObservation binds the proxy (Task 2) and returns the Observer a run consults.
// The Observer's Overlay is the policy the run must apply to its options.Network
// BEFORE the VM is prepared — the overlay is creation-only; applying it later is a
// no-op. Stop tears the proxy down and returns the scrubbed artefact.
//
// The invocation is S7's: S7 starts the observer before prepareSandbox
// (session/run.go:212), assigns opts.Network = obs.Overlay() there, and calls
// Stop/Save at teardown. This subplan does not touch session/options/prepare/vm.
func StartObservation(bindHost string, base network.Policy) (Observer, error)
type Observer interface {
    Overlay() network.Policy
    Stop() Observation
}
```

- [ ] **Step 1: Write the failing test** — `StartObservation` on `127.0.0.1` returns an `Observer` whose `Overlay()` carries `InterceptTLS == InterceptOn`, the bound proxy address, and a non-empty `UpstreamCACerts`; `Overlay().Fingerprint()` differs from `base.Fingerprint()`; `Stop()` is idempotent and returns an `Observation`.
- [ ] **Step 2: Run to verify it fails** — `go test ./internal/sandbox/channel/ -run TestStartObservation`. Expected: FAIL.
- [ ] **Step 3: Implement** `StartObservation`/`Observer`: bind the proxy (Task 2), build the overlay (`base.WithUpstreamCACerts(caPEM)` with `InterceptTLS = InterceptOn` and `Proxy = {Protocol: ProxyHTTPConnect, Address: addr}`), and delegate `Stop` to `Handle.Stop`.
- [ ] **Step 4: Run** — `go test ./internal/sandbox/channel/`. Expected: PASS.
- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/channel/
git commit -m "feat(channel): add the Observer facade the run consults"
```

---

### Task 6: Persist the scrubbed artefact

**Files:**
- Create: `internal/sandbox/channel/store.go`, `internal/sandbox/channel/store_test.go`

**Interfaces:**
- Consumes: `channel.Observation` (Task 1), `configpaths.UserStateDir()`.
- Produces:

```go
// Save writes the scrubbed artefact for one session under
// <UserStateDir>/learn/<checkoutSlug>/<sessionStartUnix>.json (mode 0600) and
// returns its path. Only the value-free Observation is written; no raw capture
// is ever persisted.
func Save(checkoutSlug string, startedUnix int64, obs Observation) (string, error)
// Load reads a saved artefact back, and Prune deletes artefacts older than a
// cutoff (the after-run deletes its own by default).
func Load(path string) (Observation, error)
```

- [ ] **Step 1: Write the failing test** — `Save` then `Load` round-trips; the file is 0600; a JSON-encoded artefact contains no header value (grep the bytes for the secret value used in the test) and no request body.
- [ ] **Step 2: Run to verify it fails** — `go test ./internal/sandbox/channel/ -run TestArtefactStore`. Expected: FAIL.
- [ ] **Step 3: Implement** the atomic write under `UserStateDir`, the loader, and `Prune`.
- [ ] **Step 4: Run** — `go test ./internal/sandbox/channel/`. Expected: PASS.
- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/channel/
git commit -m "feat(channel): persist the scrubbed observation artefact"
```

---

### Task 7: Host acceptance, the spec, and the index

**Files:**
- Create: `internal/sandbox/channel/channel_msb_test.go` (`msb` build tag)
- Modify: `.operator-shared/specs/onboarding/learn-mode/observation-channel.md` (fold any settled detail), `.operator-shared/index/sandbox.md` (the `channel` package), `.operator-shared/plans/2026-11-17-learn-mode-program.md` (flip S4 to landed; drop stale notes on merge)

- [ ] **Step 1: Write the `msb`-tagged test** creating a project VM under an observation overlay with a configured secret, driving a real request through the proxy, and asserting the `Observation` names the destination and the credential-bearing header name (never a value).
- [ ] **Step 2 (human accept):** on a KVM host (`CGO_ENABLED=1 go test ./internal/sandbox/channel/ -tags msb -run TestChannel`), run a real streaming agent turn through the proxy (the spec's open item) and paste the result. Confirm SSE arrives incrementally, and record the QUIC/pinning/`msb-trust` items as still-open where they held.
- [ ] **Step 3:** Add `internal/sandbox/channel` to the sandbox subindex; fold any settled detail into the spec (§CA trust, the capture artefact, the open-validation list).
- [ ] **Step 4: Verify** — `make check` and `make brain-check`. Expected: PASS.
- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/channel/ .operator-shared/
git commit -m "test(channel): verify a real session through the observation proxy"
```

---

## Self-review

**Coverage.** The proxy and its capture are Tasks 1–2; facts 4–6 (destination, success, denial) are Task 2's hooks into Task 1's artefact; fact 7 (attribution) is the request hook plus the T2 trust in Tasks 3–4; the CA trust placement is Tasks 2–4; the creation-only overlay and the recreate are Tasks 4–5; the capture-artefact scrubbing and lifetime are Tasks 1, 2, and 6; the open-validation items (QUIC, pinning, `msb-trust`, the streaming turn) are Task 7. The `run --learn` flag, the session record, the **invocation** (start before `prepareSandbox`, `opts.Network = Overlay()`, `Stop`/`Save` at teardown), and the after-run are **S7**; S4 exposes `StartObservation`/`Observer`, the overlay value, and the store.

**Proportion.** Signatures, test names, and assertions; the goproxy wiring is named at the level the SDK/spec fixes it, with no transcribed bodies.

**Type consistency.** `Observation`/`Destination`/`Attribution` (Task 1) are the frozen contract S6 consumes; `network.Policy.WithUpstreamCACerts` (Task 4) feeds `msb.NetworkSpec.UpstreamCACerts` (Task 3); `channel.Start`/`Handle.Stop` (Task 2) are wrapped by `StartObservation`/`Observation.Stop` (Task 5) and persisted by Task 6 under those exact names.

## Known open items (carried, not solved here)

The spec's §Open validation stays open until Task 7's host run: **QUIC/HTTP3** (only TCP consumers observed), **certificate pinning** (config-derived attribution fallback, unverified), **`msb-trust` java/chrome** (not in the base image), and the **end-to-end streaming turn**. Blocking QUIC (`TLSConfig.BlockQUIC`) and an explicit intercepted-port list are deliberately out of scope; record them as the way the destination set could be made exhaustive later.

**Policy-denied destinations are invisible under T2.** The runtime enforces the guest's egress before our proxy is consulted, so a policy-denied destination never reaches the request hook; a recorded `deny` is our proxy's own dial failure. Our proxy's own dial also originates host-side and bypasses the sandbox egress policy — a temporary widening S7 must either close (compose the base policy onto the proxy's dial) or explicitly announce and accept.

**The capture is unbounded in sandbox-controlled requests.** The recorder holds one entry per distinct `(host, ip, path, protocol)` for the whole session, and `Save`/`Load` do not cap the artefact, so a session that generates many distinct paths can grow the launcher's memory and write an arbitrarily large file under the user state dir. Bounding it needs a deliberate change to the frozen `Observation` contract (a cap plus an overflow representation S6 understands) or a per-session cap at the invocation; S7 must bound the capture before it wires the channel.
