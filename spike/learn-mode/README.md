# Learn-mode observation spike — host runbook

Throwaway harness for the seven experiments in
[`.operator-shared/plans/2026-11-15-learn-mode-spike.md`](../../.operator-shared/plans/2026-11-15-learn-mode-spike.md).
Only the results survive: they are folded into
[`.operator-shared/specs/onboarding/learn-mode.md`](../../.operator-shared/specs/onboarding/learn-mode.md), then this
directory and the plan are deleted.

**Host-only.** Every experiment needs `/dev/kvm`, a docker daemon, and network, so it cannot run inside the
development VM (`/dev/kvm` is absent there). Run the whole runbook on the host, from the repository root.

Everything here drives `msb` directly (the raw runtime) plus off-the-shelf tools; it does **not** depend on the
launcher's unlanded `proxy:`/`intercept-tls:` config surfaces (#149, #151), which is the point — the spike decides
whether those surfaces are worth building. S1 is the exception: it exercises the built launcher because it is about
the launcher's home volume and harnesses.

## Prerequisites

| Tool | Why | Install (host) |
|---|---|---|
| `msb` ≥ 0.7.11 | drives every sandbox | bundled with a checkout, or `brew install superradcompany/tap/microsandbox` |
| `mitmdump` (mitmproxy ≥ 10) | the host-owned CONNECT proxy, blind and terminating | `pipx install mitmproxy` or `brew install mitmproxy` |
| `dnsmasq` | the logging DNS listener (S2) | `brew install dnsmasq` / `apt install dnsmasq` |
| `jq` | reading `msb metrics --format json` | `brew install jq` / `apt install jq` |
| `openssl` | generates the interception CA (D) | `brew install openssl` / `apt install openssl` |

Guest-side HTTP/git clients are the drivers' concern, not host prerequisites. The `public` cases `apk add` what
they need; the `none` cases use BusyBox `wget`, because installing a client there is blocked and widening the policy
to allow it would defeat the point of the case.

Confirm the host can start a VM before anything else:

```console
msb doctor
msb run alpine -- uname -a
```

## Layout

```
spike/learn-mode/
├── lib.sh                     shared helpers (source this; do not execute)
├── guest/plant-ca.sh          installs a CA PEM in the guest three ways (S6)
├── s1..s7_*.sh                one driver per experiment
├── d_runtime_interception.sh  sub-step D (runtime interception as a channel)
└── results.md                 fill this in; it becomes the spec update
```

Network policy is set with `msb` flags (`--net`, `--net-rule`, `--net-default-egress`,
`--net-strict=false`, `--dns-nameserver`), so there are no policy YAML files to keep in sync; a command that needs
JSON deny mode writes its `--conf` file into `spike-capture/` at run time.

Each driver writes raw evidence into `spike-capture/` (created on first use, git-ignored by this directory's
`.gitignore`) and prints what to record. Nothing is uploaded.

```console
export MSB_HOME=~/.local/share/agents-sandbox/msb   # only if you are driving the launcher's private runtime
export HOST_GATEWAY_IP=                             # leave unset to probe it; see below
```

`lib.sh` resolves the guest-visible host address once and caches it. Discover it by hand if you prefer:

```console
msb run alpine --net host -- getent hosts host.microsandbox.internal
```

## Amendments to the plan

Two things found while building this that change what to run:

1. **S1's target name is wrong.** The plan says `make test-msb-reuse`; that target does not exist. The standing-root
   reuse path is `make test-msb` (it sets `AGENTS_SANDBOX_SMOKE_ROOT`). But the smoke tests only drive each harness to
   its TUI and quit — they never complete a provider setup — so they cannot answer S1. S1 must run the **launcher**
   interactively per harness; `s1_home_readback.sh` then dumps the resulting volume. The launcher's private home keeps
   the runner image and tool store warm between harnesses, so run all seven before reading anything back.
2. **A fourth approach needs an explicit decision.** Since SDK v0.7.15 the *runtime itself* terminates TLS with a CA
   the launcher supplies (`TLSConfig.CACert`/`CAKey`; CLI `--tls-intercept-ca-cert`/`--tls-intercept-ca-key`) and adds
   it to the guest trust store, and it already ships the `msb-trust` helper. That is approach C implemented by the
   runtime. The plan only treats runtime interception as a *collision to avoid*. `d_runtime_interception.sh` tests
   whether the runtime's interception is itself an observation channel, as two separate questions — **D1
   destinations** (does a request reveal its host?) and **D2 attribution** (does it reveal which credential went
   where?). Its source reading: the runtime logs a destination (`sni`/`dst`) only at *debug* verbosity
   (`crates/network/lib/engine/tls/proxy.rs`), and logs **only secret-policy violations**, never a successful
   substitution or a header field name (`crates/network/lib/engine/secrets/handler.rs`). So D1 is plausibly yes and
   D2 plausibly no — which makes D a certificate-free source of destinations (a B variant), not a replacement for C.
   Record both verdicts in `results.md`.

---

## S1 — What the home volume yields per harness

**Question.** After a harness is configured and used for one short turn, what does the home volume contain, and which
of it names hosts, credentials, or both?

**Method.** For each harness in `claude-code codex hermes oh-my-pi opencode opencode2 pi`:

1. Build the launcher once: `make build`.
2. Start it in a scratch project so it creates a home volume: `./agents-sandbox` (the first run configures the VM).
3. Complete the harness's own setup (paste an API key, or run its login), send one prompt, then exit.
4. Name the volume with `s1_home_readback.sh --list`, then dump it with `s1_home_readback.sh <volume-name>`.

`s1_home_readback.sh` prints the volume path via `agents-sandbox volume inspect --format json`, then lists candidate
config/credential/transcript/log files and greps them for hostnames and known credential file names. Run it once with
the VM stopped and once running, and diff the two listings.

**Record.** Per harness: config file paths; credential file paths and whether values are inline or referenced;
transcript/session files and whether they mention request hosts or URLs; log files and whether they mention denials or
hosts; any file that is unreadable or changes while the VM is stopped.

**Pass / fail.** Pass if facts 1–3 of the analyser's list are available per harness from files alone. Fail if a harness
keeps its config outside the home volume (a socket, a keychain, a read-only image path).

**Changes if it fails.** The copy-back source for that harness becomes the observation channel or a startup-hook
export, not the home volume.

---

## S2 — Is DNS worth keeping as corroboration

**Question.** With `--dns-nameserver` pointed at a listener the host owns, which queries arrive under `public`, under
`none` + an allow list, and from which processes?

**Method.** `s2_dns.sh` starts `dnsmasq --log-queries`, then under `public` and under `none` + an allow list runs:
`curl`, `apk update`, and `git ls-remote` over HTTPS, all inside one sandbox. The `docker build` pull is exercised
separately in the launcher VM (S1), which has a dockerd.

```console
./spike/learn-mode/s2_dns.sh public        # then
./spike/learn-mode/s2_dns.sh none-allow
```

**Record.** Whether the listener received every query; whether the in-VM resolver forwarded all processes' queries
(including the container engine's); what happens to a query for a host not in `egress-allow` under `none` (NXDOMAIN,
timeout, or forwarded-then-blocked); and whether queries keep arriving when the proxy is configured.

**Pass / fail.** Informational — the proxy log supersedes DNS. What matters is whether DNS catches anything the proxy
misses (UDP, IP-literal fallbacks), which would make it a cheap corroborating source.

**Changes.** Only relevant if S4 shows the proxy has blind spots.

---

## S3 — Is the outbound proxy reachable, and under which policy

**Question.** Can the sandbox dial a proxy the host owns, and does that dial itself need an egress allow rule?

**Method.** `s3_proxy_reachability.sh` starts a blind `mitmdump` and tries each combination:

- proxy address `127.0.0.1:$PROXY_PORT` (the runtime dials host-side) and `$HOST_GATEWAY_IP:$PROXY_PORT`;
- policy `none`, `none` + `allow@<proxy-ip>`, `public`, `public` + `host`;
- protocol `http` and `socks5`;

Each case creates a sandbox and probes with BusyBox `wget https://example.com` (an allowlisted host under `none`),
recording whether it succeeded and whether the proxy log saw the CONNECT. Because the runtime dials the proxy
host-side, the `allow@<proxy-ip>:tcp:<port>` rule in the `-allow-proxy` cases is almost certainly a no-op — the proxy
address never appears as a guest-visible destination; the rule is kept for the record, not because it is expected to
matter.

**Record.** Which combinations connect; whether denial is silent (guest sees a connection failure) or reported;
whether the proxy dial bypasses the policy; whether a vsock route avoids the policy (optional — needs a guest-side
vsock→TCP bridge, so record it as "not tested" unless you build one).

**Pass / fail.** Pass if the proxy is reachable under `none` without widening the policy beyond the proxy itself.

**Changes if it fails.** B's proxy requires the `host` profile or an allow rule for the proxy, a posture change during
a learning session; the spec must present that cost, or B waits for a runtime feature.

---

## S4 — Is the proxy complete

**Question.** With the proxy configured, what still bypasses it?

**Method.** `s4_proxy_completeness.sh` runs one sandbox under `public` pointed at the blind proxy and, with the proxy
log watched, tries: a fetch by IP literal, an HTTP/3 (QUIC) request, `git ls-remote` over HTTPS, `ssh` to a host, and
a package-manager install. The `docker build` pull needs a guest dockerd, so run it in the launcher VM (S1).

**Record.** Which of those appear in the proxy log and which reach the network directly.

**Pass / fail.** Pass if a policy that denies direct egress and permits only the proxy leaves no bypass.

**Changes if it fails.** The learned destination set is advisory, not authoritative; the proposal must say so.

---

## S5 — Are denials observable

**Question.** Under `none`, when a connection is denied, does the launcher learn the destination — from the system
log, from a metric, or only from the guest's error?

**Method.** `s5_denials.sh` denies a known host, attempts it, then inspects everything the launcher can read:
`msb logs --source all`, `msb metrics --format json`, and the guest's own HTTP response in JSON deny mode
(`network.http.deny_response_format: json`, wrapped because `--conf` is the whole sandbox document). Whether the
*runtime's* interception is also observable is sub-step D (`d_runtime_interception.sh`).

**Record.** Every place a denied destination appears (or does not), and its exact text/shape.

**Pass / fail.** Pass if a denied destination appears in a launcher-readable log.

**Changes.** Pass means UC2 detection needs no permanent proxy — the runtime log is enough. Fail means UC2 detection
stays file-attributed until the channel is permanent.

---

## S6 — Can the launcher terminate TLS and attribute a credential

**Question.** Can a host-owned proxy terminate TLS with its own CA, see which credential a request carried, and leave
every consumer working?

**Method.** In order (`s6_tls_termination.sh` automates 1–4):

1. Plant a CA in the guest three ways — append to `/.msb/tls/ca.pem` (the file agentd points `SSL_CERT_FILE`,
   `REQUESTS_CA_BUNDLE`, `CURL_CA_BUNDLE`, and `NODE_EXTRA_CA_CERTS` at), install it into the system store
   (`/usr/local/share/ca-certificates` + `update-ca-certificates`), and import it with `msb-trust java`/`chrome`.
   `guest/plant-ca.sh` does all three; `msb-trust` is vendored at
   `internal/sandbox/image/data/msb-trust.sh`.
2. Run a CONNECT proxy on the host, first blind then terminating, and point the sandbox at it with `--proxy`. In
   terminating mode the proxy still uses mitmproxy's CA (generated under the spike capture dir); the three-way plant
   in step 1 trusts it in the guest.
3. Drive consumers through the terminating proxy — `curl`, `git` over HTTPS, `npm`, `pip` (the script does these).
   A real agent turn and an in-VM `docker build` pull are follow-ups: the first needs a harness configured, the second
   a guest dockerd.
4. Repeat with a configured secret present (`--secret 'KEY@api.example.com'`) to observe the collision with
   microsandbox's own implicit interception.
5. Attempt a client known to pin certificates, if any installed harness or provider does.

Sub-step D — whether the *runtime's* interception (rather than our proxy) is itself an observation channel — is a
separate driver: `d_runtime_interception.sh`, run at `--log-level debug`, drives one allowed and one disallowed
request and records two verdicts (D1 destinations, D2 attribution).

**Record.** Which consumers trust the CA and which break; whether the credential-bearing header name is visible
(`authorization`, `x-api-key`, `cookie`); whether streaming survives termination; whether the runtime's interception
can be kept off or collides; whether pinning appears; and whether the CA survives a VM recreate (it is image state).

**Pass / fail.** Pass if an agent turn completes through termination and a static key's destination and header name are
visible in the log.

**Changes if it fails.** Attribution falls back to the config-derived endpoint with a warning, UC1's promise weakens
from "what your setup used" to "what your setup is configured to use", and the spec's fact 7 becomes best-effort
rather than required.

---

## Sub-step D — Is the runtime's interception itself an observation channel

**Question.** With the launcher supplying the interception CA, does the *runtime's* own TLS interception reveal
destinations (D1) and credential attribution (D2)? If both, approach C needs no launcher-owned proxy.

**Method.** `d_runtime_interception.sh` creates a sandbox with `--log-level debug --tls-intercept
--tls-intercept-ca-cert/-key --secret 'SPIKE_TOKEN:headers=authorization@example.com' --secret-violation-action
block-and-log`, sends the placeholder to the allowed host (a successful substitution) and to a disallowed host (a
violation), then reads `msb logs --source system` and any host-side runtime log directory.

**Record.** Two verdicts: **D1 destinations** — does a `sni=`/`dst=`/`TLS intercept` line name the host? **D2
attribution** — does anything name the credential's header field and its destination?

**Reading of the source, to confirm or refute.** The runtime logs a destination only at debug
(`crates/network/lib/engine/tls/proxy.rs`), and logs only secret-policy violations, never a successful substitution
or a header field name (`crates/network/lib/engine/secrets/handler.rs`). Expect D1 yes, D2 no: D is a certificate-free
B (destinations), not a C replacement. Confirm against the captured logs.

**Changes.** If D1 holds, the spec can present the runtime's own interception as a cheap destination source; if D2
also holds, TLS-termination attribution moves into the runtime and the launcher-owned proxy loses its main reason.

---

## S7 — What proxying costs

**Question.** Does a real agent turn survive, and at what latency?

**Method.** `s7_cost.sh` times the same transport work (a streaming read, a tarball fetch, a shallow clone) direct and
then through the terminating CONNECT proxy, in two sandboxes, and reads the host CPU of the proxy process. The real
agent turn belongs to the launcher, which has no `proxy:` surface yet (#149) — run it once #149 lands.

**Record.** Overhead per turn; whether streaming degrades; CPU on the host (`mitmdump` process); whether the proxy can
run unprivileged.

**Pass / fail.** Pass if the turn completes with no behavioural difference.

**Changes if it fails.** B cannot be a default-on part of the run path; learning-session-only, or a different
transport.

---

## Closing

Fill in `results.md`, then fold the durable outcome into the spec's "Observation approaches" section (replacing the
proposal with the resolved choice and its evidence). Record an ADR only if the choice rejects a real alternative on
grounds a future reader would question. Delete this directory and the plan once the spec is updated. A partial run is
still useful: record which experiments were not run and why.
