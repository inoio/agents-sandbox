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
  overlay, and the artefact store. Its one host-only step (a real streaming turn through the proxy) is a human accept
  the PR carries.
- **S5 landed** (#722): `internal/sandbox/readback` + `agent.ReadBackDescriber`.
- **S6 landed**: `internal/sandbox/analyser` — the finding model, the observation finder with the never-widen
  network diff, the interactive applier with the slug-keyed decline memory — plus the `envsecret`
  `WriteSecretSpec`/`RemoveSecretSpec` and `viperconfig.RemoveSetting` write primitives it added.
- **S7 landed**: `internal/sandbox/learn` — the `run --learn` invocation (the channel `Begin`/overlay before prepare,
  the baseline capture, the exit prompt, the after-run), `config learn` for a session whose owning process is gone,
  the `--learn` flag, the `state.LearnSession` record (a `HomeState` schema bump), and the 30-day artefact sweep. It
  removed the S4/S5/S6 `//deadcode:ignore` markers as it wired each package's consumer. Its host-only steps (a real
  turn through the proxy, a killed-session replay) are a human accept the PR carries.
- **S8 landed**: `config harden` — the config-only finder (`analyser.ConfigFindings`, findings C1–C7), the
  `analyser.FileModeWrite`, and the `config harden` command. It runs no sandbox, so the whole subplan is verified in
  the dev VM. The settled finding set and the owner-scope rule live in
  `specs/onboarding/learn-mode/findings-and-apply.md` §`config harden`.
- **S10 landed**: `internal/sandbox/analyser/managed_config.go` — the agent-config write-back finder
  (`ManagedConfigFindings`), the `Write.ManagedConfig`/`ApplyOptions.Agent` applier path that writes the sanitized
  config into the per-slug user scope, the `${ENVVAR}` placeholder (`envsecret.PlaceholderFor`, `SecretSpec.Placeholder`)
  with kimi's native `api_key_env` form (`agent.EnvIndirectionDescriber`), and the agent-independent credential-store
  exclusion (`readback.IsCredentialStorePath`). Its #749 half is S11's; it carries the same host-only accept as S7 (a
  real session's after-run).
- **S11 landed** (#754): `internal/sandbox/readback`'s inline config-file secret and glob-declared directory login
  store, extending S5 (#749).
- **S9 landed**: `internal/firstrun` — the first-run decision (`Offer`/`BuildFacts`), the three-choice menu (`Prompt`)
  on `termio.Prompter.Select`, the host-config probe (`HostFiles`), the UC4 host-config seed (`SeedFromHost`), and the
  `provision-host-config: false` default. The decision is recorded in `state.RootState` keyed by `<repo slug>/<agent>`,
  re-armed by a host-config source hash. Its Task 8 host accept is a human step the PR carries.

**Every subplan has landed.** S7, S8, S10, and S11 landed first; **S9 (`firstrun`) landed last** — its plan
`2026-11-17-learn-mode-firstrun.md` was written against the current tree and delivered `internal/firstrun`, the
three-choice first-run menu, the UC4 host seed, and the `provision-host-config: false` default. S8 landed as
`feat/697-config-harden` (#757); S10's managed-config write-back is where S8's C7 declarative reference write becomes a
real agent-config edit (`api_key_env` for kimi).

**S9 was the retirement point for the declaration model.** It delivered the first-run flow (the three choices from the
hub) and folded in the secure `provision-host-config: false` default, retiring #54/#97/#98/#99; the stale
`issue-99-guided-config-setup` branch (**PR #103**) and the stale `huh` backend (**PR #119**) are superseded spikes
whose ideas informed S9, not dependencies. The full ticket disposition is in §Related tickets and retirement; the
tracker closures are applied separately by the controller, not by S9's Task 7.

**#749 splits across three subplans.** Issue #749 ("read back inline config secrets and directory login stores") is the
follow-up to the kimi-code profile (#748 / PR #752): its read-back classification was deferred by that profile. Its
read-back half is **S11** (`2026-11-19-learn-mode-inline-readback.md`, written alongside this note): the inline
config-file key and the glob directory login store. Its `config harden` half is S8's finding **C7**, and its
write-back half is S10's per-harness native form (`api_key_env` for kimi). The specs carry the whole contract from
S11's PR: `learn-mode/home-read-back.md` facts 2–3 and `learn-mode/findings-and-apply.md` §The agent-config write-back.
S11 depends on S5 (landed); its kimi end-to-end path is exercised when PR #752 lands.

**The S4/S5/S6 markers are gone.** S7 removed every temporary declaration-scope `//deadcode:ignore` marker as it
wired each package's consumer. A marker left on a now-reachable declaration is a stale marker, which the `deadcode`
gate fails (its self-defence against markers-as-decoration; see `../specs/ci/quality-gates.md` §Dead-code gate).

**S6 closed three read-back gaps it consumes.** `readback.Credential.Hosts` was declared but nothing populated it,
so S6 wired `read.go` to fill it from the provider's config entry, giving the config-entry attribution fallback in
`findings-and-apply.md` a source; no `env.secret.yaml` writer existed, so S6 added `envsecret.WriteSecretSpec`/
`RemoveSecretSpec`; and `readback.Credential.EnvVar` now carries the dotenv key (the configured secret's env-var
name) so the analyser can match a credential to its channel attribution.

**How to run a subplan:** create a worktree off `origin/main` (`superpowers:using-git-worktrees`), load `superpowers:subagent-driven-development`, and execute its tasks one at a time (a fresh implementer per task, a fresh reviewer gating each). Steps marked **human accept** run on a KVM host and cannot be done in the dev VM; the subplan is not done until they are checked. Finish with one `code-review`/`reviewer-large` pass over the whole subplan, then a PR into the merge queue.

**S2, S3, and S4 have all landed** (S2 and S3 shared files; S4 followed them). **S4 and S6 share no files** — S4 is
the channel track (`network`/`msb`/`session`/`state`), S6 the learn track (`analyser`/`config`/`envsecret`) — so they
could run in parallel; both have now landed. Within each, serialize where the plan says so.

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
type Credential struct { Kind CredentialKind; Source, Provider, Field string; EnvVar string; Hosts []string; Value string } // EnvVar is the dotenv key (the configured secret's env-var name), empty for a login/probe credential
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
// internal/sandbox/analyser — owned by S6 (analyser). Wired by S7's `run --learn`
// and `config learn`; the temporary //deadcode:ignore markers it carried before
// its consumer existed were removed in S7.
// The learn-mode proposal (S6) emits per-slug, non-removing writes only; the applier
// supports both scopes and removal because S8's `config harden` reuses it (e.g. move
// a secret out of the committed project config). The config-only finder remains S8's;
// S6 owns the model, the observation finder, and the applier.
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
// internal/sandbox/analyser — S8 and S10 additions to the S6 model.
//   S8 (config harden): type FileModeWrite struct { Mode fs.FileMode }
//       Write gains: FileMode *FileModeWrite // chmods the scope's env.secret.yaml
//       func ConfigFindings(f ConfigFacts) []Finding   // KindConfig, never widens
//       type ConfigFacts struct { Paths configpaths.ConfigPaths; Slug string; Config *viperconfig.Config; Scopes []configpaths.ConfigScope; Logger termio.Logger }
//   S10 (agent-config write-back):
//       type ManagedConfigWrite struct { Name string; Content []byte }
//       Write gains: ManagedConfig *ManagedConfigWrite
//       ApplyOptions gains: Agent string // the agent whose config-dir name the write targets
//       func ManagedConfigFindings(f ManagedConfigFacts) []Finding
//       type ManagedConfigFacts struct { Paths configpaths.ConfigPaths; Slug, Home, Agent string; ReadBack readback.ReadBack; SecretEnvVars []string; Logger termio.Logger }
//
// internal/agent — S10
//   type EnvIndirectionDescriber interface { EnvIndirectionKey() string } // the config key a harness reads an env-var name from (kimi's "api_key_env"); "" when it takes ${VAR} verbatim
//   func AsEnvIndirectionDescriber(a Agent) (EnvIndirectionDescriber, bool)
//
// internal/sandbox/readback — S10 exports (the shared bounded-read cap and the store-pattern test)
//   const MaxHashedBytes = 256 * 1024           // exported from S5's unexported maxHashedBytes
//   func IsCredentialStorePath(path string) bool // agent-independent: refuses a declared store path regardless of the caller's agent
//
// internal/sandbox/envsecret — S10
//   func PlaceholderFor(envVar string) string // "${" + envVar + "}"; SecretSpec gains Placeholder string
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
 S4 observation-channel SDD  ← after S1,S2,S3              ├─ S7 run       SDD
                                                           ├─ S8 harden    SDD (C7 ← #749/S11)
                                                           └─ S10 writeback SDD ← S7 (native form ← #749/S11)
                                                          S11 inline-readback SDD ← S5 (S8/S10 consume)
                                                          S9  firstrun    SDD ← S4,S5,S7,S10,E2,E3
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
| S7 run-learn | `2026-11-17-learn-mode-run.md` | `run --learn` + after-run + session boundary | yes | S4, S6 | S8 |
| S8 config-harden | `2026-11-17-config-harden.md` | `config harden` | yes | S6 | S7 |
| S10 agent-config-writeback | `2026-11-17-agent-config-writeback.md` | managed agent config from a session + placeholders (+ #749 native form) | yes | S5, S6, S7 | S8, S11 |
| S11 inline-readback | `2026-11-19-learn-mode-inline-readback.md` | inline config secrets + directory login stores (#749) | yes | S5 | S8, S10 |
| S9 firstrun | `2026-11-17-learn-mode-firstrun.md` | first-run `Guide` choices + UC4 seed | yes | S4, S5, S7, S10, E2, E3 | — |

S2, S3, S4, and S6 landed (S2/S3 shared `network.Policy`, `msb.NetworkSpec`, `msb/translate.go`,
`viperconfig/config.go`); S6 landed on top of S5; S7 then landed on top of S4 and S6, and S10 on top of S7. **S4 and S6
share no file**, so S7 was the join; S8 then landed on top of S6 (S11 landed, extending S5 and feeding S8's C7 and
S10's native form). S9 is the last subplan, and the only one written after its predecessors landed — it was written
against the current tree, adding the `internal/firstrun` package, and it has landed. E2 is no longer a hard dependency: the menu works on
the existing `termio.Prompter` and only its rendering changes when the `huh` backend is reimplemented (#119). Within
each subplan, follow its own serialization. A subplan's plan is
written when its
predecessors land, never in advance — S11 was written ahead of PR #752 at the maintainer's call because #749's read-back
half depends only on the landed S5.

**S2 re-scope.** S2 ships the TLS-interception **controls only** (`intercept-tls`/`tls-bypass`/`disable-strict`). It does
no registry handling — no auto-bypass, no allowlist derivation — and the in-VM `docker build` CA case stays a documented
recipe (`docs/recipes/docker-ca-trust.md`): the container gets the CA added to its bundle rather than the registry being
bypassed. The `off`-with-secrets state is kept named and rejected, not silently accepted.

## Enablers (owned outside this DAG)

- **E1 — `modernc.org/sqlite`.** Approach A reads opencode2's `opencode.db` credential row. Added by S5, not by a channel subplan.
- **E2 — prompt backend (#20 / PR #119).** Interactive first-run, per-finding review, harden. It is a UX upgrade on the `termio.Prompter` menu, not an S9 dependency: S9 works with the line backend and only the rendering changes when the `huh` backend lands (S9 plan, Design decision 2). #119 is a superseded spike to reimplement against `specs/architecture/termio-ui.md`.
- **E3 — new-user-journey e2e (#252).** Acceptance harness. Define before S9; S9's host-accept task is its UC1/UC4 seed.
- **E4 — superradcompany/microsandbox#2080 (filed).** Requests the runtime log successful substitutions with destination + header field. If it lands, S4 shrinks (read the runtime's surface) and S1's T1/T2 decision is moot. Track; do not block.

## Related tickets and retirement

#697 supersedes the **declaration model**. Every ticket that carried it, and the disposition now that the learned path
has landed:

| Ticket | What it carried | Disposition |
|---|---|---|
| #118 | `config harden` subcommand | **Delivered** — S8 / PR #757. Close. |
| #54 | first-run wizard | **Delivered** — S9. Close. |
| #99 (+ #97, #98) | safe host-config migration; the secure `provision-host-config: false` default | **Delivered / superseded** — S9 folds in the default and the first-run flow; the curated provider table is retired. Close #97/#98/#99. |
| `config migrate` | the host-first migration command | **Never landed on `main`** (only on the stale #103 branch). Replaced by UC4's *Learn from my host setup* (S9). |
| #20 | replace the hand-rolled CLI prompt/UI | **Open** — the UX umbrella; low priority. |
| #119 | the `huh` prompt backend (PR) | **Superseded spike** — close; reimplement against `specs/architecture/termio-ui.md`. Not an S9 dependency. |
| #103 | `issue-99-guided-config-setup` (guided migration + tiered wizard) | **Superseded spike** — close. S9 reuses its ideas, not its code; the tiered `config wizard` is out of scope. |
| #150 | derive a `header_fields` scope from attribution | **Deferred** (spec-level; `learn-mode.md` §Deferred). |
| #697 | learn mode | The program's parent; the "close #697" follow-up *is* this retirement. |

S9's Task 7 recorded the docs, changelog, brain, and the retirement disposition; the tracker closure marks (a comment
naming the delivering PR, then close) are applied separately by the controller. E4 is tracked but never blocks.

## Rollout

Per subplan: a worktree off `origin/main` → `subagent-driven-development` (fresh implementer per task, fresh reviewer gating it) → one `code-review`/`reviewer-large` whole-subplan review → PR → merge queue (`scripts/enqueue-pr.sh`). Host-only steps are a **human accept** step and the subplan is not done until they are checked. S1 and S0 are not SDD runs: S1 is a host spike whose deliverable is a recorded decision, S0 is prose drafting.
