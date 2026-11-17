---
description: The launcher's network surfaces for TLS interception and an outbound egress proxy - `network.intercept-tls` (auto/on/off, and that `off` is rejected when secrets are configured, while a configured `tls-bypass` activates interception under `auto`) and how it interacts with secret-implied interception, `network.tls-bypass`, `network.disable-strict`, `network.proxy.*`/`--proxy` for SOCKS4/5 and HTTP CONNECT, the protocol/credential matrix, the proxy-reachability composition rule (the dial is host-side and needs no allow entry), the runtime's strict-hostname limit on non-intercepted allowlisted egress, and why every field is part of the network fingerprint and therefore creation-only.
read_if: Working on the launcher's TLS-interception or outbound-proxy config surface - `intercept-tls`/`tls-bypass`/`disable-strict`, secret-implied interception, a host that must skip interception (a package or image registry), `proxy.*`/`--proxy` or its credentials, the composition of a proxy with an egress policy, or why a bypassed or `none`-allowlisted host is denied without interception (strict hostname mode); or why a network change recreates the VM.
---

# TLS interception and the outbound proxy

Two launcher network surfaces complement the egress policy and the secret-substitution layer. Both flow through the
same pipeline as the rest of the network policy (`network.Policy` → the SDK seam), so the recreate path applies them
and a stale VM cannot survive a change. The observation channel in learn mode depends on both; see
[the observation channel](../onboarding/learn-mode/observation-channel.md).

## TLS interception

microsandbox intercepts TLS implicitly whenever a secret is configured, so it is on in the normal case. The launcher
makes that explicit and controllable.

### `network.intercept-tls`

- `auto` (the default, and the empty value): interception follows the secret configuration — on when a secret is
  configured, off otherwise — **and a configured `tls-bypass` activates it too**: a non-empty bypass emits the SDK's TLS
  config, which *is* the interception switch, so `auto` with a bypass intercepts every host except the bypass.
- `on`: interception is requested regardless of secrets.
- `off`: interception is requested off.

`off` while secrets are configured **is rejected**: the runtime re-enables its own interception for substitution
whenever a secret is configured, so a nil TLS field does not disable it. The launcher therefore rejects
`off`-with-secrets with an actionable error naming the limitation rather than claiming to disable it and silently
leaving interception on. `off` without secrets is accepted.

### `network.tls-bypass`

A list of hosts whose traffic is **not** intercepted: an exact host, or a `*.suffix` covering its subdomains.

The primary use case is a package or image registry whose client does not trust the interception CA — an in-VM
`docker build` pulling a base image, for instance. A bypass entry does **not** supersede the docker workaround: a
bypassed host is denied under strict mode unless strict mode is off (see
[strict hostname mode](#strict-hostname-mode)), so the container still needs the CA added to its bundle. The workaround
is documented as the docker CA recipe (`docs/recipes/docker-ca-trust.md`).

A host that is both `tls-bypass`ed and egress-allowlisted does **not** pass under the runtime's strict-hostname
fail-closed behaviour: strict mode denies it (`TLS bypass denied by strict hostname policy`) even though the host is
allowlisted, and the request only succeeds with strict mode off. See [strict hostname mode](#strict-hostname-mode).

## The outbound proxy

A first-class outbound proxy for sandbox egress, configured as a block or overridden by a flag.

### `network.proxy.*` / `--proxy`

- `network.proxy.protocol`, `network.proxy.address`, `network.proxy.username`, `network.proxy.password-env`.
- `--proxy protocol://host:port` overrides the block for one run.

Supported protocols and their credentials:

| Protocol | Value | Credentials |
|---|---|---|
| HTTP CONNECT | `http-connect` | username + `password-env` |
| SOCKS5 | `socks5` | username + `password-env` |
| SOCKS4 | `socks4` | none — the SDK carries no credential support |

Credentials are carried for SOCKS5 and HTTP CONNECT only. A SOCKS4 configuration that supplies a username or
`password-env` fails fast **at config time**, not at VM creation, so the error names the protocol, not a runtime
failure.

### Composition with the egress policy

A proxy the egress policy denies does not silently deny all egress: the runtime dials the proxy **host-side**, so the
dial bypasses the egress policy and needs no allow entry for the proxy address. The launcher therefore neither
auto-adds the proxy address to the allow set nor fails on a policy that omits it. The guest never sees the proxy
address as a destination.

Under the runtime's strict-hostname mode the destination's own egress can still be denied before the proxy is
consulted; see [strict hostname mode](#strict-hostname-mode).

## Strict hostname mode

The runtime runs **strict hostname mode** by default (`DisableStrict` off). The launcher exposes the switch as
`network.disable-strict`: setting it true turns strict mode off.

Under strict mode (observed with microsandbox runtime `0.7.15`) a plain-domain egress-allow rule authorizes only an
**intercepted** connection:

- With a secret configured, the runtime's interception supplies the verified TLS identity and the allow matches, so
  the allowlisted host is reachable.
- Without interception for that host — no secret, or a `tls-bypass`ed host — the connection is denied even though the
  host is allowlisted (`TCP egress denied by strict hostname policy`), and the guest's TLS fails. The same egress
  succeeds with strict mode off.

Consequences:

- A learning session configures a secret, so its observed egress is in the allowed, intercepted case.
- A `tls-bypass` entry does not make a host reachable on its own; it only skips interception. Under strict mode a
  bypassed host must also be reachable another way, or strict mode must be off for that session.
- A `none`-profile allowlist (explicit egress-allow rules, no secret) is subject to the same limit.

`network.disable-strict: true` is therefore the way to reach a non-intercepted, allowlisted host (a bypassed registry,
or a `none`-profile allowlist with no secret).

## Fingerprint and creation-only

All three surfaces are part of `network.Policy.Fingerprint()`: every field that reaches the VM is in the fingerprint, so a
change that changes the emitted policy — `intercept-tls`, `tls-bypass`, `disable-strict`, or any `proxy.*` value —
changes the fingerprint, `state.NetworkChanged` fires, and the VM is recreated through the normal reconfiguration path
(an emission-equivalent flip, below, does not). The settings are creation-only — a change takes effect at the next VM
creation, never on a running VM.

The TLS-interception fields contribute fingerprint lines only when they change the emitted policy. `intercept-tls=on` is
emitted only for `on` **with no bypass** — the one case that emits a TLS config carrying an empty bypass. A non-empty
bypass contributes a `tls-bypass=<host>` line whenever the mode is not `off`, so `on`+bypass and `auto`+bypass
fingerprint alike (both emit `TLS{Bypass}`), while `auto` and `off` without a bypass contribute no TLS line and stay
digest-stable. `disable-strict=true` is emitted when set. A bypass under `off` is dropped from the emitted policy and
contributes nothing.
