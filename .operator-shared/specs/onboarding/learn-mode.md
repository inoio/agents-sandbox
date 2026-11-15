---
description: Contract for learn mode - a session in which the user sets an agent up inside the sandbox and exercises it, after which the launcher reads the sandbox home back, derives what the session actually used, and proposes agents-sandbox managed configuration (agent config, static secrets, network posture) for the per-slug user scope; the three first-run choices, the four journeys, the chosen observation channel (passive read-back plus a launcher-owned terminating proxy), and the invariants that bind the proposal.
read_if: Working on learn mode, the after-run analyser, a `run --learn` / `config learn` surface, the first-run choice between learning and self-configuration, proposing a learned network policy or secrets from a session, folding `config harden` into run analysis, or the observation channel (host DNS resolver, outbound proxy, TLS termination); or the evidence that settled the observation approach (the 2026-11-15 host-side spike).
---

# Learn mode

Status: designed. The observation approach is settled by a host-side spike (run 2026-11-15): passive read-back (A)
always, plus a launcher-owned terminating proxy (C) for the destination and attribution facts. The spike confirmed
that termination works, that the credential header name and destination are visible, and that proxying adds no
measurable cost; two parts are still open — its per-harness read-back check (S1) ran only for opencode2, and the
end-to-end agent turn through the proxy awaits the launcher's proxy surface (`#149`).

## Intent

The launcher asks a user to declare up front what their agent will need: providers, hosts, secrets. That declaration
rests on curated knowledge — known-provider host tables, per-agent credential shapes, custom/enterprise endpoint
special cases — and it is incomplete by construction. Custom gateways, enterprise deployments, and newly released
providers are exactly where declaration fails.

Learn mode inverts it: the user sets the agent up inside the sandbox and uses it, and the launcher derives the
configuration from the one session that defines "working". The user declares nothing; they are shown what was
observed and asked to accept it. Evidence replaces the table.

## User-facing vocabulary

The user sees three first-run choices, a flag, and a command — never the word "mode", never "observation", never
"discovery", "capture", or "telemetry".

**Voice: active, with the tool as the subject for what happens; causative ("let agents-sandbox …") only for the one
moment the user grants permission; never passive.** The object of "learn" is always the setup, never the user: never
"learns from you", "learns your habits", or "learns about your usage".

- One consent sentence above the first-run menu: *"Let agents-sandbox learn from this session: set up your providers
  here, use the agent normally, and when you exit it offers to save what it needed."*
- The choices: **Learn by using it**; **Learn from my host setup** (only when host config exists, and recommended in
  that case); **Configure it myself** (the only option carrying the `provision-host-config: true` warning).
- The exit prompt reports as the tool, with counts: *"agents-sandbox learned: 7 services reached, 2 keys used,
  opencode.jsonc created."* followed by Review and save / Save all / Not now.
- `--learn` help: "let agents-sandbox learn your config from this session". `config learn` help: "review what the last
  learning session needed". The docs page: "Letting agents-sandbox learn your setup".

Both learning choices end in the same save step, which is what makes one name work for both. The mechanism words stay
internal because they describe what the launcher does, not what the user gets.

## The four journeys

One mode, four entry points, one after-run analyser.

| Journey | Entry | Session | After-run |
|---|---|---|---|
| UC1 | New install, no host agent config: **Learn by using it** | The user signs in to providers and exercises the agent | Full: config back-copy, observation analysis, proposal |
| UC2 | `run --learn` in an existing project, or a proposed re-entry at start | The new provider or integration is set up and used | Same as UC1, scoped to what changed |
| UC3 | `config harden`, no session | None | Config-only findings from the same engine |
| UC4 | New install with host agent config: **Learn from my host setup** | Seeded from the host config (static secrets already placeholdered), then used | Same as UC1 |

UC1 and UC4 differ only in what seeds the sandbox. They are not separate flows.

### UC2's detection bar

The launcher may propose re-entering learn mode on a later start only for signals it can attribute deterministically: a
credential file added or changed in the sandbox home, an agent config entry the managed config does not cover, or a
denied destination observed by the observation channel. It must not infer a problem from heuristics (a long session, a
slow turn, an agent-side error string). A proposal is a one-time prompt tied to the observed signal, declined with a
remembered answer, re-armed when the signal changes.

## What the analyser must establish

1. Which agent config files the session created or changed, and which of their entries are credentials.
2. Which **static** secrets exist in the sandbox home.
3. Which **login** credentials exist in the sandbox home (recorded, never copied).
4. Which destinations the session attempted.
5. Which destinations the session used successfully.
6. Which destinations were denied.
7. **Which credential was sent to which destination**, evidenced rather than inferred from the config.

1–3 come from reading the home volume. 4–7 need an observation channel. 7 is the reason the channel must terminate TLS:
a CONNECT tunnel is opaque, so it can say a destination was reached but not which credential the request carried.
Without the channel the analyser still establishes 1–3 and proposes no network or secret-substitution change.

## Observation approaches

Two channels ship. A (passive read-back) is always needed for the config and secret facts; C (a launcher-owned
terminating proxy) adds the destination and attribution facts. B is the same proxy without TLS termination and is not
a separate approach — the attribution requirement (fact 7) makes the terminating setting the one that runs.

### A — passive read-back

After the session, read the home volume: agent config, credential stores, session transcripts, agent logs, shell
history, and the agent's own databases. Establishes 1–3 and 4–7 only where the harness happens to record them (usually
failure paths, and never as reliable attribution).

- Needs: nothing new. The home volume is host-readable by path.
- Gives: configured providers, static secrets, login presence, harness-specific host and error mentions.
- Does not give: the destinations actually used, or which credential went where. Alone, it cannot propose a network or
  secret-substitution change.
- Reading a credential is not always reading a file. opencode2 writes no config file and keeps its API key inline in
  `~/.local/share/opencode/opencode.db` (a SQLite `credential` row), with its sessions and logs in the same DB or
  beside it, so A must parse the agent's database, not only config files. The spike verified this for opencode2 alone;
  the other harnesses' layouts are unverified.

### B — opaque egress observation

A CONNECT/SOCKS proxy the launcher owns, with the sandbox pointed at it. Gives facts 4–6 for every TCP destination,
independent of the harness's proxy handling, with counts and timestamps.

- Needs: the `proxy:` config surface (first-class outbound proxy, `#149`), which is unlanded. The spike confirmed the
  dial itself is not a policy problem — under bare `none`, an HTTP CONNECT and a SOCKS5 proxy on the host loopback are
  both reachable and the dial bypasses the egress policy, so the proxy needs no widening of the user's posture.
- Does not give: fact 7. An opaque tunnel cannot see the request, so a credential can be attributed only from the
  config, which is the declaration model this mode exists to replace.
- Worth noting what it does not need: certificates. A blind tunnel has no trust problem, so it works with any client.

DNS pointed at a launcher-owned resolver (`network.dns-servers`) is a cheaper variant that yields domains but not
ports, counts, or success, and misses IP-literal and UDP paths. The spike confirmed the runtime forwards the guest's
queries to a host-owned resolver (up to the runtime's own NXDOMAIN answer for a non-allowlisted name under `none`), so
it is usable corroboration — but once the proxy exists it is corroboration, not a source.

### C — TLS termination

The same host-owned proxy terminates TLS for the intercepted ports with its own CA, so it sees the request target and
the credential-bearing header. This is the chosen channel.

- Gives: everything B gives, plus fact 7, plus per-destination request paths. The spike saw destinations by IP and by
  `Host`, and the credential-bearing header names (`authorization`, `x-api-key`), for a static key.
- Needs: a CA the guest trusts, on top of B's proxy. agentd installs microsandbox's own interception CA at
  `/.msb/tls/ca.pem` and points `SSL_CERT_FILE`, `REQUESTS_CA_BUNDLE`, `CURL_CA_BUNDLE`, and `NODE_EXTRA_CA_CERTS` at
  it, so our CA has to join that file (startup hook) for the system-store consumers, and `msb-trust` has to import it
  for Java and Chrome. The spike confirmed `curl`, `git` over HTTPS and `npm` work through termination once the CA is
  planted; it did not exercise `msb-trust java`/`chrome` (absent from the base image) or a certificate-pinning client
  (none installed), so pinning still falls back to config-derived attribution with a warning, unverified.
- Coexists with microsandbox's own interception, which turns on implicitly whenever a secret is configured (UC4 seeds
  placeholders, so this is the normal case). The two layers **chain** rather than collide: the runtime terminates the
  guest's connection and re-originates upstream, so once the runtime trusts our proxy's CA the runtime's intercepted —
  and secret-substituted — request reaches our proxy, which attributes the real credential. Explicit control (`#151`) is
  therefore needed only to turn the runtime's layer off, not to coexist with it. This is what lets fact 7 hold even
  when the runtime substitutes in flight, as UC4's placeholders make it; the spike observed exactly that.
- **A terminating proxy handles real credential values in flight.** That is inherent, not avoidable: the value must
  cross the channel to be attributed. It is the ground behind invariant 3 — the recorded artefact keeps header names,
  never values, and the channel and its raw capture are treated as secret material.

### Chosen approach

A launcher-owned host proxy that logs every destination and terminates TLS for attribution, plus the passive read-back
that is always needed. B's blind tunnel and C's termination are the same proxy at two settings; the attribution
requirement makes the terminating setting the one that ships. A remains the only source of the config and secret
facts, and the only path that works when the channel is unavailable.

The alternatives not carried forward: the runtime's own interception (the spike read its debug logs — it records
destinations but never a successful substitution or a header field name, so it cannot supply fact 7 and C runs
regardless); harvesting denial messages from the agent's own output (brittle per harness, and only sees failures,
which the spike also confirmed — a denial leaves no launcher-readable record); keeping the curated provider table as
the source (the declaration model this replaces); and leaving attribution to the config alone (it answers "which key is
configured for this provider", never "which key this provider received", which is what the proposal promises).

## Prerequisites and phasing

Approach A, the analyser, and `config harden` need nothing that is not in `main`. The pinned SDK (v0.7.15) already
exposes the outbound proxy (SOCKS4/5, HTTP CONNECT) and TLS interception with a launcher-supplied CA; `#149` and
`#151` are the launcher's own config surfaces for those, not SDK prerequisites. `#149` is what lets the proxy ship as
a config surface. `#151` is needed only to turn the runtime's own interception off; with it left on, the two layers
chain and attribution still holds.

Phasing: the spike (run 2026-11-15; S1 still partial, covering opencode2 only); then A plus the analyser plus harden;
then the proxy, which adds facts 4–7 and therefore UC2's detection signal; then the `header_fields` scope derived from
attribution.

## Session boundary

The **invocation that started the learning session owns the after-run**: `run --learn` performs it at teardown, in that
process, with that terminal. It is not hooked to VM detach, the reaper, or the idle timeout, because a VM is
one-per-checkout with several attachable clients and the last-detach path runs with an often-cancelled context.

If the owning process is gone (killed harness, closed terminal), the session record stays in state and `config learn`
performs the same analysis later. A recorded session whose home volume has since been recreated is reported and
discarded, not analysed.

**Baseline for change detection** is a manifest of the agent config dirs and the known credential paths — path, size,
mtime, and content hash for files under a small size cap — taken when the learning session starts. It is not a scan of
the home volume. UC2's file-attributed detection needs a baseline from an ordinary run too, so the same manifest is
refreshed at the end of every run once the mode exists.

**Observation requires the policy at creation.** A learning session needs the launcher's proxy in the sandbox's network
config, which is creation-only. On an existing project VM the session therefore recreates it through the normal
reconfiguration path and its guards — the per-project creation lock, and the same keep/quit/restart prompt when other
clients are attached — with the prompt naming the temporary observation settings. The user's config is never written
for the overlay. A user who declines the recreate gets a passive-read-back session, which establishes facts 1–3 and
proposes no network or secret-substitution change.

## The proposal

The analyser emits **findings**, each carrying the evidence, the proposed write, and the scope it would write to. One
finding shape serves both learn mode and config-only analysis.

- **Evidence is part of the finding.** A proposed `egress-allow` entry names the destination, how often it was used,
  whether it succeeded, and which channel saw it. A proposed secret names the provider and the field, never the value.
- **Scope is the per-slug user scope** (`~/.config/agents-sandbox/<repo>-<hash>/`), because it is a trusted user layer
  and needs no approval gate. It is keyed by the **repo slug**, so it is shared by every clone of the remote — the
  proposal must state that.
- The project scope is offered as an explicit second choice only (team sharing, committed config), and the project
  trust gate then applies.
- **The managed layer never owns the credential store.** `reprovision.Provision` writes merged and mirror files
  unconditionally on every start, so a managed file that carries a login entry the agent refreshed in the sandbox
  would destroy it. The copy-back therefore never targets a file the agent refreshes, and never adds a `home:` or
  provision rule for the credential store. Static secrets go to `env.secret.yaml` and appear as placeholders in the
  managed agent config; login entries stay only in the persistent sandbox home.
- **A learned static secret is attributed before it is scoped.** Its host set comes from the destinations the session
  was observed sending that credential to; with no attribution (client pins certificates, non-HTTP protocol) it falls
  back to the endpoint in the same config entry, then to a question at apply time. When none is derivable the secret is
  not migrated: it stays in the sandbox home. A learned value never produces `allow_any_host_dangerous`.
- **Network changes are proposed as a diff**, normally `profile: none` plus the observed allow set, and never widen a
  policy the user already set. Denied destinations are reported separately and never auto-allowed.
- Applying is interactive and per finding. A decline is remembered, keyed on a hash of the evidence, and re-armed when
  the evidence changes. The record lives in the user state dir, keyed by repo slug, so it survives a config-dir reset
  and is shared by clones.
- A non-interactive run writes no proposal and prints one line naming the command that produces it. A dry run records
  nothing and proposes nothing.
- The applied network policy is creation-only, so it takes effect at the next VM creation through the existing
  recreate path; the proposal states that.

## `config harden`

Harden is the analyser with no session: the same findings engine, the same applier, the config-only subset of
findings. It is re-runnable and audits the effective configuration (file modes, `provision-host-config: true`, an
over-broad posture, an over-broad secret scope), and it is the home of "reduce egress later". A finding declares
whether it needs configuration or observation; harden runs the former, the after-run analyser runs both.

Harden does not need a provider knowledge table. Its network findings are either structural (a broad profile, a
wildcard entry) or, when a session has been analysed, evidenced by that session.

## Invariants

1. **Propose, never apply silently.** No destination, secret, or posture change lands without an explicit acceptance.
2. **The credential store is never copied out of the sandbox.** Static secrets are extracted into the host-side secret
   file; login credentials stay where they are.
3. **Observation is local, minimal, and ephemeral.** A terminating channel handles real credential values in flight —
   necessarily, since attribution needs them — so the channel and any raw capture are secret material. The *recorded
   artefact* holds destinations, request paths, and credential-bearing header **names**, never request bodies and
   never header values; a raw flow capture is scrubbed of values before it is kept. Artefacts stay on the host, are
   deleted after analysis by default, and are never uploaded.
4. **No widening.** Learn mode learns what was used; it never removes a user-set entry or broadens a policy.
5. **No curated knowledge in the learned path.** A proposal derived from a table rather than from the session is a
   different feature and must not be mixed in.
6. **Non-interactive never hangs.** Warn and continue, as everywhere else in onboarding.

## Deferred

- A `header_fields` scope derived from attribution (`#150`): attribution names the header, so the scope can be derived
  once the proxy exists, but narrowing substitution is a separate change.
- UC2 signals that need an always-on observation channel; until the channel is permanent, UC2 detection is
  file-attributed only.
- Promoting a per-slug proposal to the project scope as a first-class flow.
- Auto-accepting a proposal for a user who has opted into that (the mode stays explicit for now).
