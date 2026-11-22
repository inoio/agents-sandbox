---
description: Contract for learn mode - a session in which the user sets an agent up inside the sandbox and exercises it, after which the launcher reads the sandbox home back, derives what the session actually used, and proposes agents-sandbox managed configuration (agent config, static secrets, network posture) for the per-slug user scope; the three first-run choices, the four journeys, the invariants that bind the proposal, and the phasing. This is the hub; the observation channel, the home read-back, and the findings and apply contracts are subspecs.
read_if: Working on learn mode - the after-run analyser, a `run --learn` / `config learn` surface, the first-run choice between learning and self-configuration, proposing a learned network policy or secrets from a session, or folding `config harden` into run analysis.
---

# Learn mode

Status: designed. The observation approach is settled by a host-side spike (run 2026-11-15): passive read-back (A)
always, plus a launcher-owned terminating proxy (C) for the destination and attribution facts. The spike confirmed
that termination works, that the credential header name and destination are visible, and that proxying adds no
measurable cost. The end-to-end agent turn through the proxy was verified on 2026-11-22 for claude-code, pi, and
codex. One part is still open: the per-harness read-back check (S1) ran only for opencode2.

This page is the hub. The contracts it points at:

- [The observation channel](learn-mode/observation-channel.md) — the destinations and credential attribution facts,
  the approaches, the topology, the capture artefact, CA trust, and the creation-only policy.
- [Home read-back](learn-mode/home-read-back.md) — the config and secret facts, the per-harness layout truth, and
  the baseline manifest.
- [Findings and apply](learn-mode/findings-and-apply.md) — the finding shape, scopes, the credential-store rule,
  attribution, the network diff, applying, and the after-run ownership.

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

## What the analyser establishes

The analyser establishes seven facts about a finished session. Facts 1–3 — which agent config files changed and
which entries are credentials, which static secrets exist, and which login credentials exist — come from reading the
home volume: [home read-back](learn-mode/home-read-back.md). Facts 4–7 — which destinations were attempted, used,
and denied, and **which credential was sent to which destination** — need an observation channel:
[the observation channel](learn-mode/observation-channel.md). Fact 7 is why that channel terminates TLS.

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

## Prerequisites and phasing

Approach A, the analyser, `config harden`, and the channel need nothing that is not in `main`: the launcher's proxy and
interception config surfaces (`#149`, `#151`) and the channel's own upstream-CA projection (S4) have landed. Their
constraints are in [the observation channel](learn-mode/observation-channel.md).

Phasing (landed): the spike (run 2026-11-15; S1 for opencode2 only), the read-back (S5), the analyser and `config
harden` (S6/S8), the channel's own code (S4, which adds facts 4–7 and therefore UC2's detection signal), the `run
--learn` invocation and after-run (S7), the managed-config write-back (S10), the inline read-back (S11), and the
first-run flow (S9) have all landed. The `header_fields` scope derived from attribution remains deferred (#150).

## Deferred

- A `header_fields` scope derived from attribution (`#150`): attribution names the header, so the scope can be derived
  once the proxy exists, but narrowing substitution is a separate change.
- UC2 signals that need an always-on observation channel; until the channel is permanent, UC2 detection is
  file-attributed only.
- Promoting a per-slug proposal to the project scope as a first-class flow.
- Auto-accepting a proposal for a user who has opted into that (the mode stays explicit for now).
