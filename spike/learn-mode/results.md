# Learn-mode observation spike — results

Fill in as experiments run. This file, not the scripts, is what feeds the spec
update (`.operator-shared/specs/onboarding/learn-mode.md`, "Observation
approaches"). Run date, host, and the resolving `msb`/SDK versions at the top.

- Run date: 2026-11-15
- Host: Linux x86_64, `/dev/kvm` writable (host session, not the dev VM), docker 29.1.3
- `msb --version`: 0.7.15
- SDK (`go.mod` `microsandbox/sdk/go`): v0.7.15
- mitmproxy version: 12.3.1
- openssl: 3.5.5

## Results

| Experiment | Result | Decision taken |
|---|---|---|
| S1 | not run (needs interactive per-harness provider setup) | |
| S2 | fail as scripted: custom upstream honoured, but no query reached the host listener | DNS dropped; D1 already yields destinations |
| S3 | pass for `http` under `none` (dial bypasses policy); gateway-IP address fails; `socks5` not truly tested | B is viable; proxy dial needs no policy widening |
| S4 | inconclusive as scripted: blind mitmproxy logs no flows; guest traffic (ip literal, git, ssh, apk) succeeded | learned set stays advisory |
| S5 | fail: denial not in any launcher-readable runtime log; only the guest's own error | UC2 detection stays guest/file-attributed |
| S6 | pass: termination works; credential header name and destination visible | C is viable; a terminating proxy sees real secrets |
| S7 | pass: no measurable overhead; streaming unbuffered; ~4% CPU, unprivileged | proxy can be default-on transport-wise |
| D (D1 destinations / D2 attribution) | D1 yes, D2 no (successful substitutions) | D = certificate-free destination source, not a C replacement |

## Sub-step D — runtime interception as an observation channel

- D1 destinations (does an intercepted request name its host?): **yes**
- D2 attribution (does it name the credential's header field and destination?): **no** for successful
  substitutions (only violations are logged, and without a header field name)
- Evidence:
  - D1: `DEBUG microsandbox_network::engine::tls::proxy: TLS intercept sni=example.com dst=104.20.23.154:443
    guest_dst=104.20.23.154:443` (and the same for the disallowed host and for `dl-cdn.alpinelinux.org`).
    Visible via `msb logs --source system` at `--log-level debug --tls-intercept`.
  - D2: the only attribution line is the violation, which names the *secret env var*, host, method, path and
    `location=header` — but not the header field name, and it exists only when a violation is blocked:
    `WARN microsandbox_network::engine::secrets::handler: secret violation: placeholder detected where
    substitution or passthrough is not permitted action=block-and-log secret_env_var=SPIKE_TOKEN
    placeholder=$MSB_SPIKE_TOKEN protocol=http/1.1 sni=www.iana.org host=www.iana.org method=GET path=/
    location=header match_form=raw guest_dst=104.18.25.232:443`.
- If D1 yes and D2 no, D is a certificate-free B; does that change the chosen approach? It reinforces D as a
  cheap destination source (no CA needed), but not an attribution source — so it does not displace C for fact 7.

## Deviations and gaps

- Experiments not run, and why:
  - S1 (home-volume read-back) needs the launcher driven interactively once per harness (provider setup /
    login); it is the only non-scriptable part.
- Places the plan/runbook was wrong (all now fixed in the drivers — see the patch):
  - `s1_home_readback` etc. leaked every sandbox: `msb remove` refuses a running sandbox without `--force`,
    so `spike_rm` failed silently. Fixed (`spike_rm` uses `--force`).
  - S3/S5/S2 ran `curl`/`git` in `alpine` without installing them; `none` cases now use BusyBox `wget`.
  - S5's deny-response YAML needed `network.http.*` under `--conf` (the earlier fix) — confirmed correct.
  - The S6 "three-way" CA plant was ineffective on Alpine: base `alpine` ships `ca-certificates-bundle`
    (the bundle file) but **not** `update-ca-certificates`, so the "system store" step was a no-op and
    `apk`/`curl` failed TLS through the terminating proxy. `guest/plant-ca.sh` now appends the PEM straight
    to `/etc/ssl/certs/ca-certificates.crt` when `update-ca-certificates` is absent.
  - S6's header grep used `^(authorization|...)`, but mitmproxy indents headers, so it always reported
    "(none)". Fixed to allow leading whitespace.
  - S6's collision case had no client and no way to trust our proxy. Fixed: install a client, add
    `--tls-upstream-ca-cert` (so the runtime trusts mitmproxy upstream), and plant the CA.
  - A socks5 dial the runtime cannot complete stalls the guest; added `SPIKE_EXEC_TIMEOUT` (applied inside
    `spike_exec`) so one wedged case cannot stall a sweep.
  - S3's `socks5` cases were never valid: `spike_proxy_up` starts mitmproxy in HTTP `regular` mode, which is
    not a SOCKS5 server, so the runtime's SOCKS5 dial cannot complete. Testing SOCKS5 needs a SOCKS5 listener
    (mitmproxy's `--mode socks5`), which the driver does not start.
  - S4's proxy log came back empty: mitmproxy's blind mode (`--ignore-hosts '.*'`) records no flows, so S4
    cannot see which traffic used the proxy. A destination-logging blind proxy (a small CONNECT logger, or the
    terminating proxy) is needed; as scripted S4 is unanswerable.
- Things the scripts could not automate here:
  - `docker build` pull (needs a guest dockerd; the launcher VM in S1 has one).
  - a client that pins certificates (none installed).
  - HTTP/3 / QUIC: the guest `curl` has no `--http3`, so QUIC bypass was untested.
  - the real agent turn (S7): needs the launcher `proxy:` surface (#149).

### Client-certificate findings (S6)

- Trusted the CA and worked through termination after the bundle fix: `curl` (200), `git` over HTTPS,
  `npm`.
- `pip` failed with Alpine's PEP 668 `externally-managed-environment` guard — a packaging policy, not a TLS
  failure; `pip --break-system-packages` (or a venv) would proceed.
- `msb-trust java` / `msb-trust chrome` were skipped (no JDK/Chrome in the base image).
- Destination visibility: the rendered flow shows the request-line IP plus the `Host:` header name.
- Credential visibility: with a static `Authorization`/`X-Api-Key`, the proxy log shows the header **names**.
  In the collision case (a configured secret, so the runtime intercepts too, with `--tls-upstream-ca-cert`
  letting it trust our proxy) the **real** substituted token value reached our proxy exactly once while the
  placeholder never appeared — i.e. the runtime substitutes in-flight and our proxy can attribute the real
  credential to its destination.
- **Security note:** a terminating proxy sees real credential values. A learning session that runs one must
  treat its flow log as secret material.

### DNS finding (S2)

`--dns-nameserver <addr>:<port>` is honoured (pointing it at a dead address breaks resolution), but queries
never arrived at a host-owned listener on `127.0.0.1:5354`, `172.16.0.61:5354` (the gateway), or the host's
primary IP: dnsmasq logged no query lines in any case (the guest saw the runtime's own proxy at
`172.16.0.5:53`). The runtime's DNS proxy evidently does not dial the configured upstream from the host
loopback, so a host-side logging resolver is not a usable corroboration channel as the runbook scripted it.
With D1 already yielding destinations from the runtime's own debug log, DNS is not needed for approach B.

## Outcome

- Observation approach chosen (A / B / C / D, and why): **C (launcher-owned terminating proxy) for
  attribution, with D as a cheap certificate-free destination source**. S6 shows termination works for the
  system-store consumers and that the credential header name and destination are visible; D shows the
  runtime's own interception reveals destinations but not the header field name for successful
  substitutions, so D cannot supply fact 7.
- Does UC2 detection need a permanent proxy, or is the runtime log enough (S5)? A permanent proxy (or an
  equivalent channel). No launcher-readable runtime log records a denied destination; under `none` a
  non-allowlisted host fails at DNS and the only trace is the guest's own error.
- Is the learned destination set authoritative or advisory (S4)? **Advisory** — the blind proxy logged no
  flows, so which traffic bypassed it remains unmeasured; anything that bypassed is invisible and the
  proposal must say so.
- Cost of the chosen channel (S7): no measurable per-operation overhead — streaming first-byte ~0.07-0.2s, a
  5.8 MB tarball comparable, `git clone --depth 1` within noise — streaming stayed unbuffered, and mitmdump
  ran unprivileged at ~4% CPU. The real agent turn is still pending #149.
