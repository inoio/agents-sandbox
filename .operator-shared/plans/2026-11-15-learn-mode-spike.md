---
description: Validation spike for learn mode - seven experiments that decide whether the launcher can observe what a session actually used (home-volume read-back per harness, host DNS resolver under each profile, outbound-proxy reachability and completeness, denial observability, interception prerequisites, streaming cost); each has a pass condition and the decision it changes.
read_if: Running or revising the learn-mode spike, choosing between the passive-read-back / launcher-owned-network / interception approaches, or picking up an unverified observation-channel assumption (proxy reachability under `none`, egress bypass, denial logging, DNS coverage).
---

# Learn-mode validation spike

Status: not run. Host-only: needs `/dev/kvm`, a docker daemon and network, so it cannot run in the development VM.
Everything here is throwaway except the results, which are folded into `specs/onboarding/learn-mode.md` before this
plan is deleted.

## What it decides

Whether the launcher can establish, for one finished session, **which destinations were attempted, used, and
denied, and which credential went to which destination** — and at what cost. Attribution is required, so the pivotal
experiment is S6; the others size the rest of the channel. The contract in the spec does not depend on the answer,
but the choice of observation approach and whether UC2 can be detected at all do.

Run S6 first if there is any doubt about TLS termination being workable; the rest can follow in any order.

## S1 — What the home volume yields per harness

**Question.** After a harness has been configured and used for one short turn, what does `handle.Path()` contain,
and which of it names hosts, credentials, or both?

**Method.** For each harness in the smoke table: start a project VM with a reused root (`AGENTS_SANDBOX_SMOKE_ROOT`
via `make test-msb-reuse`), complete the harness's own setup (paste a key or run its login), send one prompt, exit.
Then read the volume from the host.

**Record.** For each harness: config file paths, credential file paths and whether values are inline or referenced,
transcript/session files and whether they contain request hosts or URLs, log files and whether they contain denial
or host strings, and whether any file is unreadable or changes while the VM is stopped.

**Pass / fail.** Pass if facts 1–3 of the analyser's list are available per harness from files alone. Fail if a
harness keeps its config outside the home volume (a socket, a keychain, a read-only image path) — that harness then
needs the observation channel for even the credential facts.

**Changes if it fails.** The copy-back source for that harness becomes the observation channel or a startup hook
export, not the home volume.

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

Fill in; one row per experiment.

| Experiment | Result | Decision taken |
|---|---|---|
| S1 | | |
| S2 | | |
| S3 | | |
| S4 | | |
| S5 | | |
| S6 | | |
| S7 | | |

## Closing

Fold the durable outcome into the spec's "Observation approaches" section, replacing the proposal with the resolved
choice and its evidence; record an ADR only if the choice rejects a real alternative on grounds a future reader would
question. Delete this plan once the spec is updated. A partial run is still useful: record which experiments were not
run and why.
