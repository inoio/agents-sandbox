---
description: The learn-mode observation channel - which destinations a session attempted, used, and was denied (facts 4-6) and which credential went to which destination (fact 7); the three approaches (passive read-back, opaque egress, TLS termination) and which ships and why; the T1/T2 topology; the capture artefact and its scrubbing; CA generation and trust placement; the #149/#151/#2080 constraints; and the creation-only network policy a learning session needs.
read_if: Working on the learn-mode channel - the launcher-owned terminating proxy, destination or credential-attribution facts, the recreate a learning session needs, the capture artefact and its scrubbing, CA generation and trust placement, or the channel's open validation (QUIC, pinning, msb-trust); or when a learning session observed no destinations.
---

# Learn mode — the observation channel

The launcher observes a real session through two channels. Passive read-back
([home read-back](home-read-back.md)) is always needed for the config and secret facts. A launcher-owned
terminating proxy adds the destination and the credential-attribution facts. This page is the contract for that
second channel.

## What the channel establishes

The analyser establishes seven facts about a finished session; four need a channel.

4. Which destinations the session attempted.
5. Which destinations the session used successfully.
6. Which destinations were denied.
7. **Which credential was sent to which destination**, evidenced rather than inferred from the config.

Facts 4–7 need an observation channel. Fact 7 is the reason the channel must terminate TLS: a CONNECT tunnel is
opaque, so it can say a destination was reached but not which credential the request carried. Without the channel
the analyser still establishes facts 1–3 and proposes no network or secret-substitution change.

## Approaches

Two channels ship. A (passive read-back) is always needed for the config and secret facts; C (a launcher-owned
terminating proxy) adds the destination and attribution facts. B is the same proxy without TLS termination and is
not a separate approach — the attribution requirement (fact 7) makes the terminating setting the one that runs.

### A — passive read-back

Reads the home volume after the session. Establishes facts 1–3 and facts 4–7 only where the harness happens to
record them (usually failure paths, and never as reliable attribution). Its contract is in
[home read-back](home-read-back.md).

### B — opaque egress observation

A CONNECT/SOCKS proxy the launcher owns, with the sandbox pointed at it. Gives facts 4–6 for every TCP
destination, independent of the harness's proxy handling, with counts and timestamps.

- Uses the `proxy:` config surface (the first-class outbound proxy, `#149`, landed). The dial itself is not a policy
  problem — under bare `none`, an HTTP CONNECT and a SOCKS5 proxy on the host loopback are both reachable and the
  dial bypasses the egress policy, so the proxy needs no widening of the user's posture.
- Does not give fact 7. An opaque tunnel cannot see the request, so a credential can be attributed only from the
  config, which is the declaration model this mode exists to replace.
- Needs no certificates. A blind tunnel has no trust problem, so it works with any client.

DNS pointed at a launcher-owned resolver (`network.dns-servers`) is a cheaper variant that yields domains but not
ports, counts, or success, and misses IP-literal and UDP paths. The runtime forwards the guest's queries to a
host-owned resolver (up to the runtime's own NXDOMAIN answer for a non-allowlisted name under `none`), so it is
usable corroboration — but once the proxy exists it is corroboration, not a source.

### C — TLS termination

The same host-owned proxy terminates TLS for the intercepted ports with its own CA, so it sees the request target
and the credential-bearing header. This is the chosen channel.

- Gives everything B gives, plus fact 7, plus per-destination request paths. The runtime's requests expose
  destinations by IP and by `Host`, and the credential-bearing header names (`authorization`, `x-api-key`), for a
  static key.
- Needs a CA the guest trusts, on top of B's proxy. See [CA generation and trust placement](#ca-generation-and-trust-placement).
- Coexists with microsandbox's own interception, which turns on implicitly whenever a secret is configured (a
  seeded first-run session makes this the normal case). The two layers **chain** rather than collide: the runtime
  terminates the guest's connection and re-originates upstream, so once the runtime trusts our proxy's CA the
  runtime's intercepted — and secret-substituted — request reaches our proxy, which attributes the real credential.
  Explicit control (`#151`) is therefore needed only to turn the runtime's layer off, not to coexist with it. This
  is what lets fact 7 hold even when the runtime substitutes in flight, as a seeded session makes it.
- **A terminating proxy handles real credential values in flight.** That is inherent, not avoidable: the value must
  cross the channel to be attributed. It is the ground behind invariant 3 in the [hub](../learn-mode.md) — the
  recorded artefact keeps header names, never values, and the channel and its raw capture are treated as secret
  material.

### Chosen approach

A launcher-owned host proxy that logs every destination and terminates TLS for attribution, plus the passive
read-back that is always needed. B's blind tunnel and C's termination are the same proxy at two settings; the
attribution requirement makes the terminating setting the one that ships. A remains the only source of the config
and secret facts, and the only path that works when the channel is unavailable.

The alternatives not carried forward: the runtime's own interception (it records destinations but never a
successful substitution or a header field name, so it cannot supply fact 7 and C runs regardless); harvesting
denial messages from the agent's own output (brittle per harness, and only sees failures — a denial leaves no
launcher-readable record); keeping the curated provider table as the source (the declaration model this replaces);
and leaving attribution to the config alone (it answers "which key is configured for this provider", never "which
key this provider received", which is what the proposal promises).

## Topology

The channel can sit in one of two places:

- **T1** — the launcher intercepts the guest directly, planting its CA in the guest, so our endpoint terminates the
  guest's connection.
- **T2** — the runtime terminates the guest's connection and re-originates upstream to a host endpoint the launcher
  owns, which the runtime must trust as an upstream CA.

The settled topology is **T2**: with a configured secret and `TLS.UpstreamCACerts` trusting our proxy's CA, the runtime
intercepted the guest's connection, substituted the placeholder in flight, and re-originated to our proxy, where the
real credential was attributed exactly once (`authorization`) while the placeholder never appeared. The launcher does
not plant its CA in the guest; facts 4–7 hold under T2.

## CA generation and trust placement

The terminating proxy generates its own CA. Under the settled T2 topology the **guest never sees it**: the runtime
terminates the guest's connection and re-originates to our proxy, so **the runtime must trust our CA as an upstream
CA**. That is the SDK's `TLSConfig.UpstreamCACerts`, which takes CA bundle **file paths**, not PEM contents: the
channel writes its CA to a 0600 host file and projects that path. The launcher does not expose the field today, so the
channel adds a projection for it (`msb.NetworkSpec` → `cfg.TLS.UpstreamCACerts`), in the network fingerprint and
therefore creation-only like every other field that reaches the VM.

The T1 placement — planting our CA in the guest (joining `/.msb/tls/ca.pem`, whose path agentd exports through
`SSL_CERT_FILE`, `REQUESTS_CA_BUNDLE`, `CURL_CA_BUNDLE`, and `NODE_EXTRA_CA_CERTS`, plus the Alpine bundle append,
and `msb-trust` for Java/Chrome) — is **not** used under T2: the guest trusts the runtime's own interception CA, and
the runtime trusts ours. It remains the fallback only if a future runtime cannot re-originate to a launcher endpoint.

Once our CA is trusted upstream, the spike confirmed `curl`, `git` over HTTPS, and `npm` work through termination.

**The proxy issues its terminating certificate for the SNI name, not the CONNECT target.** The runtime opens its tunnel
to our proxy by the destination's IP (`CONNECT 160.79.104.10:443`) but verifies our certificate against the hostname it
sends as SNI (`api.anthropic.com`). A certificate for the CONNECT target fails that handshake (`tls: bad certificate`),
and every request through the channel dies before it is seen. The leaf is therefore signed per ClientHello name, with
the CONNECT target as the fallback for a client that sends no SNI. Under T2 the CONNECT target is an IP, so the
hostname of a destination is known only from the TLS handshake and the terminated request, never from the tunnel.

The trust is VM-creation state and does not survive a recreate on its own; a learning session that recreates the VM
re-establishes it with that session's fresh CA.

The proxy verifies the upstream certificate when it dials the real destination, because it forwards the real credential:
an upstream whose certificate the system roots do not contain is refused and the destination is recorded as a denial,
so a forwarded credential cannot reach an impostor. `network.observe.skip-upstream-verify` (default `false`) drops that
verification for a host whose egress is transparently intercepted with a private CA. It is launcher-side — it never
reaches the VM, the SDK spec, or the network fingerprint — so changing it does not recreate the VM.

## The capture artefact

The channel records, per observed request: the destination (`Host` and IP), the port and protocol, whether the
request succeeded or was denied, the request path, and the credential-bearing header **names**. It never records
request bodies or header **values**. A raw flow capture is scrubbed of values before it is kept. `IP` is empty for a
hostname target (the analyser treats it as unknown, not as a resolved address).

A destination is identified by host, IP, port, path, and protocol, so the same host reached on two ports — two
CONNECT tunnels, say — is two entries, each with its own port and count, rather than one entry whose port is
whichever request arrived last.

A CONNECT tunnel is recorded as its own `connect` destination, accepted-successful, alongside any terminated `https`
entry for the same host. The tunnel is the floor: a client that pins certificates or speaks a non-HTTP protocol never
produces an inner request, so its destination would otherwise be invisible; the `connect` entry keeps it visible, and
its absence of attribution is what tells the analyser the request inside was never seen. A `connect` entry's `Success`
means the proxy accepted the tunnel, not that the upstream answered. A `deny` entry recorded before any request was
seen carries `Port: 0`, since no port was observed.

The recorded `Attribution` carries the credential-bearing header **names** only. In S4 it carries no credential
identity — the placeholder-versus-real distinction the topology establishes is not threaded into the artefact yet —
so the analyser attributes a header to a configured secret by falling back to `readback.Credential.Hosts`.

The artefact stays on the host, is deleted after analysis by default, and is never uploaded. It is secret material
(invariant 3). An unreviewed artefact — a session whose owning process died — is swept at the next launcher start once
it is 30 days old. The artefact is uncapped: the recorder keeps one entry per distinct `(host, ip, port, path,
protocol)` for the whole session and `Save`/`Load` impose no size bound, so a session that generates many distinct
paths can grow the launcher's memory and write a large file under the user state dir.

## Prerequisites and constraints

The pinned SDK (v0.7.16) exposes the outbound proxy (SOCKS4/5, HTTP CONNECT) and TLS interception, and its
`TLSConfig` carries the upstream-CA list T2 needs. The launcher config surfaces are in place:

- `#149` (landed) — the first-class outbound proxy surface; the channel points the sandbox at its proxy through it.
- `#151` (landed) — explicit interception control. Needed only to turn the runtime's own interception off; with it
  left on, the two layers chain and attribution still holds.
- The projection of our CA into the runtime's upstream-CA list (`TLSConfig.UpstreamCACerts`) is landed with the
  channel. See [CA generation and trust placement](#ca-generation-and-trust-placement).
- superradcompany/microsandbox#2080 — asks the runtime to log successful substitutions with destination and header
  field. If it lands, the channel work shrinks and the T1/T2 question is moot.

**Observation requires the policy at creation.** A learning session needs the launcher's proxy in the sandbox's
network config, which is creation-only. On an existing project VM the session therefore recreates it through the
normal reconfiguration path and its guards — the per-project creation lock, and the same keep/quit/restart prompt
when other clients are attached — with the prompt naming the temporary observation settings. The user's config is
never written for the overlay. If the observation channel cannot start, the run continues as a passive read-back
session, which establishes facts 1–3 and proposes no network change. It can still propose a static secret whose
endpoint the entry's own config names (the config-entry fallback in [findings and apply](findings-and-apply.md)),
since that needs no attribution. The launcher records the overlay's fingerprint as the running VM's network state, so
the first ordinary run after a learning session sees the overlay gone and recreates the VM again through the same
path.

## Open validation

The spike settled the channel but left four checks unrun; none blocks the design, and each is a known limitation of
what the proposal may claim until it is closed:

- **Policy-denied destinations are invisible under T2** — the runtime enforces the guest's egress before the proxy is
  consulted, so a destination the sandbox policy denies never reaches the proxy and the channel records no `deny` for
  it; a recorded `deny` is the proxy's own dial failure, not a policy denial. The proxy's own dial originates host-side
  and bypasses the sandbox egress policy — a temporary widening, so S7 must either compose the base policy onto the
  proxy's dial or explicitly announce and accept it.
- **QUIC/HTTP3 bypass** — with the proxy active, only TCP consumers were tested; a QUIC client may reach the network
  directly, so the learned destination set is authoritative for TCP, not exhaustive. The set could later be made
  exhaustive by blocking QUIC (`TLSConfig.BlockQUIC`) and by an explicit intercepted-port list; both are deliberately
  out of scope today.
- **Certificate pinning** — no pinning client was available to test, so the fallback to config-derived attribution
  is used with a warning, unverified.
- **`msb-trust java` / `chrome`** — the base image has neither, so the Java/Chrome trust imports are unverified.

The end-to-end agent turn through the proxy was verified on 2026-11-22: a model turn completes in a learning session for
claude-code, pi, and codex (it had failed on the CONNECT-target certificate above, #812).

The per-harness read-back check (S1) is an open item on the [home read-back](home-read-back.md) page.
