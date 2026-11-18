# Learn mode — program index

> **For agentic workers:** This is a program index, not a plan. It carries the DAG, the cross-subplan contracts, and the shared constraints. Each subplan in the table below is its own plan document and its own subagent-driven-development run. Do not execute this file.

**Goal:** Deliver learn mode (#697): a launcher observes a real sandbox session, derives what it actually used (agent config, static secrets, destinations, credential→destination attribution), and proposes agents-sandbox managed configuration for the per-slug user scope.

**Architecture:** Two tracks. The **channel track** opens the launcher's network surfaces and builds the observation channel that makes attribution possible; the **learn track** reads the sandbox home back and turns its own facts plus the channel's into findings the user accepts. The two run in parallel until they join at `run --learn`.

**Tech Stack:** Go 1.27, Cobra/Viper, microsandbox SDK `v0.7.16`, `internal/sandbox/{network,msb,volume,state}`,
`internal/agent`, `internal/viperconfig`, `modernc.org/sqlite` (read-back only), `github.com/elazarl/goproxy`
(the S4 terminating proxy).

**Specs:** the hub `.operator-shared/specs/onboarding/learn-mode.md` plus its subspecs under `.operator-shared/specs/onboarding/learn-mode/` (`observation-channel.md`, `home-read-back.md`, `findings-and-apply.md`), and `.operator-shared/specs/network-and-secrets/interception-and-proxy.md` — all created by subplan `settled-design` (S0) and landed on `main`.

## State and handoff

**Read this first if you are picking the work up cold.**

What is on `main` now:

- The **settled contract** is on `main`: the hub `specs/onboarding/learn-mode.md` (status: designed) and its subspecs
  `learn-mode/{observation-channel,home-read-back,findings-and-apply}.md`, plus
  `network-and-secrets/interception-and-proxy.md`. **S0 landed** (#716).
- **S1 landed** (#718 docs, #719 probe): it settled topology **T2**, the `off` semantics, the proxy-composition rule,
  and the strict-hostname bypass. Its per-harness read-back check covered **opencode2 only** (still partial).
- **S2 landed** (#720): `network.intercept-tls`/`tls-bypass`/`disable-strict`.
- **S3 landed** (#721): `network.proxy.*`/`--proxy` (SOCKS4/5, HTTP CONNECT).
- **S4 landed**: `internal/sandbox/channel` — the terminating proxy, the scrubbed capture, the in-memory network
  overlay, and the artefact store — still carrying its temporary `//deadcode:ignore` markers until S7 wires the
  invocation. Its one host-only step (a real streaming turn through the proxy) is a human accept the PR carries.
- **S5 landed** (#722): `internal/sandbox/readback` + `agent.ReadBackDescriber`, still carrying its temporary
  `//deadcode:ignore` markers (see below).

**The frontier is S6 (`analyser`)** — every predecessor has landed. S7/S8/S9 are named in the DAG but **not yet
written** — write each when its predecessor lands, never in advance.

**S6 must remove S5's dead-code markers.** S5 (`readback`) lands a library before its only consumer, so its
declarations are unreachable from any `main` package and carry temporary declaration-scope `//deadcode:ignore
reason="…"` markers (on the `internal/sandbox/readback` package and `agent.AsReadBackDescriber`). As S6 imports the
package and wires its caller, S6 **deletes each marker**: a marker left on a declaration that is now reachable is a
stale marker, which also fails the `deadcode` gate (the gate's self-defence against markers-as-decoration; see
`../specs/ci/quality-gates.md` §Dead-code gate).

**S6 also closes two read-back gaps it consumes.** `readback.Credential.Hosts` is declared but nothing populates it,
so the config-entry attribution fallback in `findings-and-apply.md` has no source until S6 fills it; and no
`env.secret.yaml` writer exists, so S6 builds one. Both are tasks in the S6 plan.

**How to run a subplan:** create a worktree off `origin/main` (`superpowers:using-git-worktrees`), load `superpowers:subagent-driven-development`, and execute its tasks one at a time (a fresh implementer per task, a fresh reviewer gating each). Steps marked **human accept** run on a KVM host and cannot be done in the dev VM; the subplan is not done until they are checked. Finish with one `code-review`/`reviewer-large` pass over the whole subplan, then a PR into the merge queue.

**S2, S3, and S4 have all landed** (S2 and S3 shared files; S4 followed them). **S4 and S6 share no files** — S4 is
the channel track (`network`/`msb`/`session`/`state`), S6 the learn track (`analyser`/`config`/`envsecret`) — so they
could run in parallel; with S4 landed, S6 is the last of the two. Within each, serialize where the plan says so.

## Global Constraints

Shared by every subplan; a subplan may add to these but not weaken them.

- Go 1.27; follow `.operator-shared/operator.md`, `AGENTS.md`, and repo code style.
- TDD: failing test first, watch it fail, implement, watch it pass. Test a unit in the file named after it.
- `make fmt`, `make lint`, `make check` — never `go fmt`/`go vet` by hand.
- Every behavior change updates `README.md`/`docs/` and adds a `changelog.d/<PR>.<type>.md` fragment.
- `internal/sandbox/msb` is the only package that imports the microsandbox SDK. Every SDK `SandboxConfig` field stays classified in `sandboxConfigProjected`/`sandboxConfigNotModeled` (`TestSDKStructFieldsAreClassified` enforces it).
- Network changes are creation-only and fingerprint-driven: `network.Policy.Fingerprint()` feeds `state.NetworkState`, `state.NetworkChanged` triggers the recreate path. Any field that reaches the VM is in the fingerprint.
- The config key registry is derived from `viperconfig.Config` struct tags; `env:"-"` makes a key config-file-only.
- Host-only verification: `/dev/kvm` is unusable in the dev VM. `msb`-tagged tests and any real sandbox run are a **human accept** step, never claimed done by an agent. `verification-before-completion` stops at the PR.
- Secrets: static secret values go only to the host-side `env.secret.yaml`; login credentials are never copied out of the sandbox home (hub invariant 2).

## Cross-subplan contracts (frozen)

These types cross subplan boundaries. A fresh subagent sees only its own task plus its Interfaces block, so a producer and a consumer must agree on exactly these names and shapes. The owning subplan defines them; consumers import, never redefine.

```go
// internal/sandbox/network — owned by S2 (tls-interception)
type InterceptMode string
const (
    InterceptAuto InterceptMode = "auto" // empty value means auto
    InterceptOn   InterceptMode = "on"
    InterceptOff  InterceptMode = "off"
)

// internal/sandbox/network — owned by S3 (outbound-proxy)
type ProxyProtocol string
const (
    ProxyHTTPConnect ProxyProtocol = "http-connect"
    ProxySOCKS5      ProxyProtocol = "socks5"
    ProxySOCKS4      ProxyProtocol = "socks4"
)
type Proxy struct {
    Protocol    ProxyProtocol `mapstructure:"protocol"`
    Address     string        `mapstructure:"address"`
    Username    string        `mapstructure:"username"`
    PasswordEnv string        `mapstructure:"password-env"` // host env var name; SOCKS5 and HTTP CONNECT carry credentials, SOCKS4 does not
}

// Policy (internal/sandbox/network) gains these value fields — value-typed so the
// derived key registry recurses and `network.<key>.*` derives:
//   InterceptTLS  InterceptMode `mapstructure:"intercept-tls"`          // S2
//   TLSBypass     []string      `mapstructure:"tls-bypass" env:"-"`     // S2
//   DisableStrict bool          `mapstructure:"disable-strict"`         // S2
//   Proxy         Proxy         `mapstructure:"proxy"`                  // S3
// Fingerprint/Describe/Empty account for all four.
//
// As re-scoped (see the S2 re-scope note below): S2 exposes the controls only. It
// does no registry handling, and the in-VM `docker build` CA case is documented as
// a recipe (docs/recipes/docker-ca-trust.md) rather than handled by a bypass.

// internal/sandbox/msb — the projection handed to the SDK. Owned by the subplan
// named; no SDK type crosses the seam.
//   S2: type InterceptMode string (mirror of network.InterceptMode)
//       NetworkSpec gains: InterceptTLS InterceptMode; TLSBypass []string
//                          DisableStrict bool
//   S3: type ProxyProtocol string (mirror); type ProxySpec struct{ Protocol ProxyProtocol; Address, Username, PasswordEnv string }
//       NetworkSpec gains: Proxy ProxySpec
```

```go
// internal/sandbox/readback — owned by S5 (readback)
const maxHashedBytes = 256 * 1024
type ManifestEntry struct { Key string; Size int64; ModTime time.Time; Hash string }
type Manifest struct { Entries []ManifestEntry }
func TakeManifest(home, agentName string) (Manifest, error)
func (m Manifest) Changed(prev Manifest) []string

type CredentialKind int
const (CredentialStatic CredentialKind = iota; CredentialLogin)
type Credential struct { Kind CredentialKind; Source, Provider, Field string; Hosts []string; Value string }
type ReadBack struct { ConfigPaths []string; Static, Login []Credential; Changed, Unreadable []string }
func Read(home, agentName string, baseline Manifest) (ReadBack, error)
func HomeVolumePath(ctx context.Context, k state.Key) (string, error)

// ReadBack.Unreadable names the credential-probe sources whose read failed this
// run (e.g. "opencode2:opencode2-sqlite"), so a consumer can tell "no
// credentials" from "the store could not be read".

// internal/agent — owned by S5
type ReadBackDescriber interface { ReadBackPaths() []string; CredentialProbes() []string }
```

**(the channel artefact S4 owns is frozen below, then S6's `Finding`/`ApplyResult`; the S5 read-back block above is unchanged.)**

```go
// internal/sandbox/channel — owned by S4 (observation-channel), consumed by S6.
// The scrubbed capture: destinations, request paths, counts, and credential-bearing
// header NAMES — never request bodies and never header values (hub invariant 3).
// A raw flow capture (values in flight) is deleted after analysis; this value-free
// type is what persists. No field can carry a value, by construction.
type Destination struct {
    Host      string // Host header / SNI
    IP        string // literal IP for an IP-literal CONNECT target; empty for a hostname target (unknown to the analyser)
    Port      uint16
    Protocol  string // "https" | "http" | "connect" | "socks5" | "tcp"
    Path      string
    Success   bool
    Denied    bool
    Count     int
    FirstSeen time.Time
    LastSeen  time.Time
    Channel   string // which observer saw it, e.g. "proxy-terminate"
}
type Attribution struct {
    Host           string   // destination this credential went to
    Path           string
    Credential     string   // configured secret's env-var name; S4 leaves it empty, so the analyser falls back to readback.Credential.Hosts
    HeaderNames    []string // credential-bearing header names only; never values
    SawReal        bool     // a real value crossed (never stored)
    SawPlaceholder bool     // a placeholder crossed unsubstituted
}
type Observation struct { Destinations []Destination; Attribution []Attribution }
```

```go
// internal/sandbox/analyser — owned by S6 (analyser). The learn-mode proposal
// (S6) emits per-slug, non-removing writes only; the applier supports both scopes
// and removal because S8's `config harden` reuses it (e.g. move a secret out of
// the committed project config). The config-only finder is S8's; S6 owns the model,
// the observation finder, and the applier.
type Scope string
const (ScopePerSlugUser Scope = "per-slug-user"; ScopeProject Scope = "project")
type Kind string
const (KindConfig Kind = "config"; KindObservation Kind = "observation")

// Evidence is the observed basis for a finding; it holds destinations, counts, and
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
    Writes       []Write // applied together; a move is two writes (remove project, add per-slug)
    Kind         Kind
    EvidenceHash string // keys the decline memory (append-only; re-armed when it changes)
}
type Outcome string
const (OutcomeApplied Outcome = "applied"; OutcomeDeclined Outcome = "declined"; OutcomeSkipped Outcome = "skipped"; OutcomeFailed Outcome = "failed"; OutcomeNotApplicable Outcome = "not-applicable")
type ApplyResult struct { Finding Finding; Outcome Outcome; WroteDirs []string; Note, Err string }

// Shadowing: precedence is generic user < per-slug user < project, so a per-slug
// write for a key the project layer sets is a no-op. The applier consults
// viperconfig.LayerSettingOwner and skips such a write (the proposal never overrides
// a user-set project entry: invariant 4).
//
// Primitives the applier uses (consumed by S7/S8 too):
//   envsecret.WriteSecretSpec(dir, envVar, spec) error   // S6
//   envsecret.RemoveSecretSpec(dir, envVar) error        // S6 (the harden move)
//   viperconfig.RemoveSetting(dir, key) error            // S6 (the harden path)
```

```go
// internal/sandbox/state — owned by S6 (the decline memory, a RootState section keyed
// by repo slug; bumps RootStateSchema).
type LearnDecline struct { EvidenceHash string; At time.Time }
// RootState gains: LearnDeclines map[string][]LearnDecline `yaml:"learn_declines,omitempty"` // key: repo slug
```

## Subplan DAG

```
CHANNEL TRACK                                          LEARN TRACK
 S0 settled-design      docs     (no SDD; merge first)
 S1 sdk-semantics       spike    (no SDD; HOST-GATED)
 S2 tls-interception    SDD  ─┐                          S5  readback    SDD ─┐
 S3 outbound-proxy      SDD  ←┘ serialize after S2        S6  analyser    SDD ←┘
 S4 observation-channel SDD  ← after S1,S2,S3              ├─ S7 run      SDD
                                                           └─ S8 harden   SDD
                                                          S9  firstrun    SDD ← S4,S5,E2,E3
```

| Subplan | Plan file | Deliverable | SDD? | Depends on | Parallel with |
|---|---|---|---|---|---|
| S0 settled-design | `2026-11-17-learn-mode-settled-design.md` | hub + subspecs on `main` | no | — | all |
| S1 sdk-semantics | `2026-11-17-learn-mode-sdk-semantics.md` | T1/T2, `off` semantics, proxy-dial exemption, strict-hostname bypass recorded | **no** — host spike | S0 | — |
| S2 tls-interception | `2026-11-17-learn-mode-tls-interception.md` | #151 shipped | yes | S1 (only the `off` branch) | S5 |
| S3 outbound-proxy | `2026-11-17-learn-mode-outbound-proxy.md` | #149 shipped | yes | S2 (shared files) | S5 |
| S4 observation-channel | `2026-11-17-learn-mode-observation-channel.md` | channel (proxy + capture) + overlay + teardown | yes | S1, S2, S3 | S6 |
| S5 readback | `2026-11-17-learn-mode-readback.md` | home-volume reader + manifest | yes | E1 (SQLite) | S2, S3 |
| S6 analyser | `2026-11-17-learn-mode-analyser.md` | findings engine + applier | yes | S5 | S4 |
| S7 run-learn | `2026-11-17-learn-mode-run.md` (stub) | `run --learn` + after-run + session boundary | yes | S4, S6 | S8 |
| S8 config-harden | `2026-11-17-config-harden.md` (stub) | `config harden` | yes | S6 | S7 |
| S9 firstrun | `2026-11-17-learn-mode-firstrun.md` (stub) | first-run `Guide` choices + UC4 seed | yes | S4, S5, E2, E3 | — |

S2, S3, and S4 landed (S2/S3 shared `network.Policy`, `msb.NetworkSpec`, `msb/translate.go`,
`viperconfig/config.go`). **S4 and S6 share no file**: S4 is done and S6 is the remaining frontier. Within each,
follow its own serialization. Stubs and their interfaces are written when their predecessor lands, never in advance.

**S2 re-scope.** S2 ships the TLS-interception **controls only** (`intercept-tls`/`tls-bypass`/`disable-strict`). It does
no registry handling — no auto-bypass, no allowlist derivation — and the in-VM `docker build` CA case stays a documented
recipe (`docs/recipes/docker-ca-trust.md`): the container gets the CA added to its bundle rather than the registry being
bypassed. The `off`-with-secrets state is kept named and rejected, not silently accepted.

## Enablers (owned outside this DAG)

- **E1 — `modernc.org/sqlite`.** Approach A reads opencode2's `opencode.db` credential row. Added by S5, not by a channel subplan.
- **E2 — prompt backend (#20 / PR #119).** Interactive first-run, per-finding review, harden. Land before S9.
- **E3 — new-user-journey e2e (#252).** Acceptance harness. Define before S9.
- **E4 — superradcompany/microsandbox#2080 (filed).** Requests the runtime log successful substitutions with destination + header field. If it lands, S4 shrinks (read the runtime's surface) and S1's T1/T2 decision is moot. Track; do not block.
- **Retirement (deferred, not in this DAG).** #697 supersedes the declaration model in #54/#99/#118. Once the learned path ships, retire `config migrate`'s curated provider table (and the `config harden` proposal in #118). Record it as the follow-up that closes #697; it needs no plan until the learned path lands.

## Rollout

Per subplan: a worktree off `origin/main` → `subagent-driven-development` (fresh implementer per task, fresh reviewer gating it) → one `code-review`/`reviewer-large` whole-subplan review → PR → merge queue (`scripts/enqueue-pr.sh`). Host-only steps are a **human accept** step and the subplan is not done until they are checked. S1 and S0 are not SDD runs: S1 is a host spike whose deliverable is a recorded decision, S0 is prose drafting.
