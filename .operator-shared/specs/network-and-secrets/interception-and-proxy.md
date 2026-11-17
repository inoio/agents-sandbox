---
description: The launcher's network surfaces for TLS interception and an outbound egress proxy - `network.intercept-tls` (auto/on/off) and how it interacts with secret-implied interception, `network.tls-bypass`, `network.proxy.*`/`--proxy` for SOCKS4/5 and HTTP CONNECT, the protocol/credential matrix, the proxy-reachability composition rule, and why every field is part of the network fingerprint and therefore creation-only.
read_if: Working on the launcher's TLS-interception or outbound-proxy config surface - `intercept-tls`/`tls-bypass`, secret-implied interception, a host that must skip interception (a package or image registry), `proxy.*`/`--proxy` or its credentials, the composition of a proxy with an egress policy, or why a network change recreates the VM.
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
  configured, off otherwise.
- `on`: interception is requested regardless of secrets.
- `off`: interception is requested off.

`off` while secrets are configured cannot always be expressed: the runtime may re-enable its own interception for
substitution. If `off` cannot be expressed, the launcher rejects `off`-with-secrets with an actionable error naming
the limitation rather than claiming to disable it and silently leaving interception on. (Whether the runtime
re-enables is settled by the SDK channel spike; the contract is "true off, or an actionable error".)

### `network.tls-bypass`

A list of hosts whose traffic is **not** intercepted: an exact host, or a `*.suffix` covering its subdomains.

The primary use case is a package or image registry whose client does not trust the interception CA — an in-VM
`docker build` pulling a base image. A bypass entry supersedes the earlier workaround of planting the interception CA
inside the VM for that client.

A host that is both `tls-bypass`ed and egress-allowlisted must still pass under the runtime's strict-hostname
fail-closed behaviour; whether it does is settled by the SDK channel spike and recorded here.

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

A proxy the egress policy denies silently denies **all** egress, because the sandbox cannot reach the proxy. The
launcher resolves this deterministically rather than leaving a silent failure: either the proxy address is
auto-added to the effective allow set, or the configuration fails with an actionable error. The exact rule is
settled by the SDK channel spike and recorded here. The proxy's address is host-side, so the guest never sees it as
a destination; the rule exists for the case where it is guest-visible.

## Fingerprint and creation-only

Both surfaces are part of `network.Policy.Fingerprint()`: every field that reaches the VM is in the fingerprint, so a
change to `intercept-tls`, `tls-bypass`, or any `proxy.*` value changes the fingerprint, `state.NetworkChanged`
fires, and the VM is recreated through the normal reconfiguration path. The settings are creation-only — a change
takes effect at the next VM creation, never on a running VM.
