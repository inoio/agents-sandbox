---
description: Validation spike for learn mode - experiments deciding whether the launcher can observe what a session actually used (home-volume read-back per harness, host DNS resolver under each profile, outbound-proxy reachability and completeness, denial observability, TLS-termination prerequisites, streaming cost). Run 2026-11-15 on the host; S1 is still partial (opencode2 only). The host-runnable harness is `spike/learn-mode/`.
read_if: Picking up the unrun part of the learn-mode spike (S1's per-harness home read-back), rerunning an experiment, or checking an unverified observation-channel assumption (proxy reachability under `none`, egress bypass, denial logging, DNS coverage, per-harness credential layout).
---

# Learn-mode validation spike

Status: partially run (2026-11-15, host: Linux x86_64, `/dev/kvm`, docker 29.1.3, `msb`/SDK 0.7.15). Results are in
`spike/learn-mode/results.md`, and the durable outcome is folded into the `specs/onboarding/learn-mode/` subspecs. S1 is not
finished: it ran for opencode2 only, so this plan and the `spike/learn-mode/` harness stay until the other six
harnesses are read back. Host-only: needs `/dev/kvm`, a docker daemon and network, so it cannot run in the development
VM.

S8 (the SDK channel-semantics rerun, 2026-11-17, host-only, throwaway probe) settled the four open channel questions —
topology, `off`-with-secrets, proxy composition, and the strict-hostname bypass — recorded in
`specs/network-and-secrets/interception-and-proxy.md` and `specs/onboarding/learn-mode/observation-channel.md`. The
probe is not landed: it drives the SDK directly, and only `internal/sandbox/msb` may import the microsandbox SDK
(`specs/architecture/msb-seam.md`), which `TestNoProductionPackageImportsSDK` enforces repo-wide.

The harness in `spike/learn-mode/` (tracked, throwaway) is the runbook `README.md`, one driver per experiment, and raw
evidence in the git-ignored `spike-capture/`. Run the runbook, not the prose below, which stays as the experiment's
definition.

## What it decides

Whether the launcher can establish, for one finished session, **which destinations were attempted, used, and
denied, and which credential went to which destination** — and at what cost. Attribution is required, so the pivotal
experiment is S6; the others size the rest of the channel. The outcome: C (a launcher-owned terminating proxy) is the
channel, with A always; `specs/onboarding/learn-mode/observation-channel.md` carries the evidence. Whether UC2 can be detected
without a permanent channel is settled by S5 (it cannot).

Run S6 first if there is any doubt about TLS termination being workable; the rest can follow in any order.

## S1 — What the home volume yields per harness

**Question.** After a harness has been configured and used for one short turn, what does the home volume contain, and
which of it names hosts, credentials, or both?

**Method.** For each harness (the smoke table's `claude-code codex hermes oh-my-pi opencode opencode2 pi`): build the
launcher (`make build`), start it in a scratch project so it creates a home volume, complete the harness's own setup
(paste a key or run its login), send one prompt, then exit. Dump the volume with `s1_home_readback.sh <volume-name>`
(the driver takes a volume name, not a harness; `--list` names the volumes). The launcher's private home keeps the
runner image warm between harnesses, so run all seven before reading anything back.

The harness smoke suite (`make test-msb`; `test-msb-reuse` is a stale phony target with no recipe) only drives each harness to its TUI and
quits, so it never completes a provider setup and cannot answer S1.

**Record.** For each harness: config file paths, credential file paths and whether values are inline or referenced,
transcript/session files and whether they contain request hosts or URLs, log files and whether they contain denial
or host strings, and whether any file is unreadable or changes while the VM is stopped.

**Pass / fail.** Pass if facts 1–3 of the analyser's list are available per harness from files alone. Fail if a
harness keeps its config outside the home volume (a socket, a keychain, a read-only image path) — that harness then
needs the observation channel for even the credential facts.

**Changes if it fails.** The copy-back source for that harness becomes the observation channel or a startup hook
export, not the home volume.

**Progress (2026-11-15).** Only opencode2 was read back: its config dir is empty and its API key sits inline in
`~/.local/share/opencode/opencode.db` (SQLite), so facts 1–3 are available but A must parse the database, not only
config files. The other six harnesses are outstanding.

## S2 — Is DNS worth keeping as corroboration

**Question.** With `network.dns-servers` pointed at a listener the host owns, which queries arrive under `public`,
under `none` + an allow list, and from which processes?

**Method.** Run a logging DNS listener on the host (a few dozen lines, or `dnsmasq --log-queries`). Set
`network.dns-servers` to it. Under `public`: run an agent turn, `apt-get update`, a `docker build` pull, and `git
fetch` over HTTPS. Repeat under `profile: none` with the same hosts in `egress-allow`.

**Record.** Whether the listener receives every query; whether the in-VM resolver forwards all processes' queries to
it (including the container engine's); what happens to a query for a host not in `egress-allow` under `none` (NXDOMAIN,
timeout, or forwarded-then-blocked); and whether queries continue to arrive when the proxy is configured.

**Pass / fail.** Informational: the proxy log supersedes DNS as the destination source. What matters is whether DNS
catches anything the proxy misses (UDP, IP-literal fallbacks), which would make it a cheap corroborating source.

**Changes.** Only relevant if S4 shows the proxy has blind spots; otherwise DNS is not part of the channel.

## S3 — Is the outbound proxy reachable, and under which policy

**Question.** Can the sandbox dial a proxy the host owns, and does that dial itself need an egress allow rule?
This is the open composition question in the outbound-proxy issue.

**Method.** A throwaway Go program using the SDK: create a sandbox with `WithProxy(HTTPConnectProxy(...))` and
`WithProxy(SOCKS5Proxy(...))`, pointed at (a) a host listener on the host gateway address and (b) a unix socket
exposed over vsock if the runtime offers one. Try each against `profile: none`, `public`, and `[public, host]`, with
and without an `egress-allow` entry for the proxy address.

**Record.** Which combinations establish a connection; whether denial is silent (the guest sees a connection failure)
or reported; whether the proxy dial bypasses the policy entirely; and whether a vsock route avoids the policy.

**Pass / fail.** Pass if the proxy is reachable under `none` without widening the policy beyond the proxy itself.

**Changes if it fails.** B's proxy requires the `host` profile or an allow rule for the proxy, which is a posture
change during a learning session; the spec must present that cost, or B waits for a runtime feature.

## S4 — Is the proxy complete

**Question.** With the proxy configured, what still bypasses it?

**Method.** From inside the sandbox, with the proxy log watched: fetch by IP literal, make an HTTP/3 (QUIC) request,
`git fetch` over HTTPS, `ssh` to a host, `docker build` that pulls a base image, and a package manager install.

**Record.** Which of those appear in the proxy log and which reach the network directly.

**Pass / fail.** Pass if a policy that denies direct egress and permits only the proxy leaves no bypass.

**Changes if it fails.** The learned destination set is advisory, not authoritative: anything that bypassed the proxy
is invisible, and the proposal must say so rather than implying completeness.

## S5 — Are denials observable

**Question.** Under `profile: none`, when a connection is denied, does the launcher learn the destination — from the
system log, from a metric, or only from the guest's error?

**Method.** Deny a known host, attempt it, then inspect `Logs` with every source, `Metrics`, and the guest's own HTTP
response in JSON deny mode.

**Pass / fail.** Pass if a denied destination appears in a launcher-readable log.

**Changes.** Pass means UC2 detection needs no permanent proxy — the runtime log is enough — and the spec's UC2
detection bar can widen. Fail means UC2 detection stays file-attributed until the channel is permanent.

## S6 — Can the launcher terminate TLS and attribute a credential

**Question.** Can a host-owned proxy terminate TLS with its own CA, see which credential a request carried, and leave
every consumer working?

**Method.** In order:

1. Generate a CA and a leaf per destination. Plant the CA in the guest three ways: append to `/.msb/tls/ca.pem` (the
   file agentd points `SSL_CERT_FILE`, `REQUESTS_CA_BUNDLE`, `CURL_CA_BUNDLE`, and `NODE_EXTRA_CA_CERTS` at), install
   it into the system store (`/usr/local/share/ca-certificates` + `update-ca-certificates`), and import it with
   `msb-trust java` and `msb-trust chrome`.
2. Run a CONNECT proxy on the host, first blind then terminating, and point the sandbox at it with `WithProxy`.
3. Drive a real agent turn (streaming), `git fetch` over HTTPS, `npm install`, `pip install`, and an in-VM
   `docker build` pull.
4. Repeat with a configured secret present, to observe the collision with microsandbox's own implicit interception.
5. Attempt a client known to pin certificates, if any installed harness or provider does.

**Record.** Which consumers trust the CA and which break; whether the credential-bearing header name is visible
(`authorization`, `x-api-key`, `cookie`); whether streaming survives termination; whether the runtime's interception
can be kept off or collides; whether pinning appears; and whether the CA survives a VM recreate (it is image state).

**Pass / fail.** Pass if an agent turn completes through termination and a static key's destination and header name are
visible in the log.

**Changes if it fails.** Attribution falls back to the config-derived endpoint with a warning, UC1's promise weakens
from "what your setup used" to "what your setup is configured to use", and the spec's fact 7 becomes best-effort
rather than required — which reverses the decision that made C mandatory.

## S7 — What proxying costs

**Question.** Does a real agent turn survive, and at what latency?

**Method.** One representative agent turn (streaming response, a tool call, a large diff read) direct and then
through the CONNECT proxy; measure wall time and confirm the stream is not buffered.

**Record.** Overhead per turn, whether streaming degrades, CPU on the host, and whether the proxy can run unprivileged.

**Pass / fail.** Pass if the turn completes with no behavioural difference.

**Changes if it fails.** B cannot be a default-on part of the run path; learning-session-only, or a different
transport.

## Results

See `spike/learn-mode/results.md` for the per-experiment evidence. Summary:

| Experiment | Result | Decision taken |
|---|---|---|
| S1 | partial — opencode2 only; facts 1–3 available, but the credential is a SQLite row | A must parse the agent DB |
| S2 | pass — the runtime forwards guest queries to the host resolver | DNS is usable corroboration |
| S3 | pass — HTTP CONNECT and SOCKS5 both reachable under bare `none` | proxy dial needs no widening |
| S4 | pass for TCP — no bypass; QUIC untested | learned set authoritative for TCP |
| S5 | fail — a denial leaves no launcher-readable record | UC2 detection stays file-attributed |
| S6 | pass — termination works; destination and header name visible | C is viable |
| S7 | pass — no measurable overhead; the real agent turn awaits `#149` | proxy can be the default transport |
| D (runtime interception) | destinations yes, attribution no | dropped; C ships regardless |
| S8 q1 topology | pass — the runtime intercepts, substitutes, and re-originates to our endpoint | T2; the launcher plants no guest CA |
| S8 q2 `off` | the runtime re-enables interception whenever a secret is configured | `off`-with-secrets is rejected with an actionable error |
| S8 q3 proxy composition | pass — the dial is host-side and needs no allow entry (HTTP CONNECT and SOCKS5) | no auto-allow of the proxy address |
| S8 q4 bypass under strict | fail — a bypassed-and-allowlisted host is denied under strict mode | strict hostname mode denies non-intercepted allowlisted egress |

## Closing

The durable outcome is folded into `specs/onboarding/learn-mode/observation-channel.md`: C plus A, with the runtime's own interception
dropped, `#151` demoted to optional, and invariant 3 reworded for the secrets a terminating channel handles. This plan
and the `spike/learn-mode/` harness stay until S1 is finished for the remaining six harnesses, so A's claim rests on
more than opencode2; then complete S1, update the A section of `specs/onboarding/learn-mode/observation-channel.md`, and delete the plan, the harness, and its captured
evidence. Record an ADR only if a rejected alternative needs its grounds preserved.
