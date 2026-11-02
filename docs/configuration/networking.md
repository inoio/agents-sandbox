---
title: Networking
description: "Control the sandbox VM's network egress: composable public, private, and host profiles, the allowlist-only none profile, and explicit allow lists."
layout: default
parent: Configuration
nav_order: 30
---

# Networking

The `network:` block controls the VM's network policy. It is baked in at VM creation, so changing it recreates the VM
(see [Resource Config Application]({% link configuration/launcher.md %}#resource-config-application)). When the whole `network:` block is absent,
the VM uses the `public` profile: outbound internet is allowed so the agent can reach its APIs and package registries,
while private networks, the host, the guest's own loopback, link-local addresses, and cloud metadata endpoints stay
blocked. That default is deliberately the narrowest profile an agent usually needs; the `private` and `host` profiles are
opt-in because they reach beyond the public internet — your local network, and the machine running agents-sandbox,
respectively. The `public`, `private`, and `host` profiles compose into one policy; use `profile: none` for a
deny-by-default, allowlist-only policy.

| Field               | Type     | Description                                                                                                          |
|---------------------|----------|----------------------------------------------------------------------------------------------------------------------|
| `profile`           | string or []string | One or more of `public`, `private`, and `host`, or the terminal `none`. Defaults to `public` when unset. Accepts a scalar, a comma-separated string, or a list. |
| `egress-allow`      | []string | Egress destinations to allow: `host`, a CIDR (e.g. `123.123.0.0/16`), or a `.suffix` (e.g. `.internal`).              |
| `egress-deny`       | []string | Egress carve-outs, same destination forms as `egress-allow`. Emitted **before** the profile rules and the explicit allow rules, so a carve-out wins over a profile's broad allow. |
| `dns-servers`       | []string | DNS upstream resolvers: a bare IP (auto-appends `:53`) or `host:port`. Overrides microsandbox's default resolver.     |

- `public` (the default) allows egress to the public internet only; `private` allows the private/LAN ranges (RFC 1918,
  ULA, CGN) — your whole local network, including other machines, not just the host, and any private resources you reach
  through a VPN routed on your machine; `host` allows the host machine's gateway addresses
  (`host.microsandbox.internal`). These are disjoint destination groups, so `public` does **not** already include
  `private` or `host`, and enabling `public` never reaches your LAN, VPN, or host.
- Profiles compose: set any combination of `public`, `private`, and `host` as a list, e.g. `profile: [public, host]`. The
  VM then gets the union of their rules. Duplicate profiles are ignored.
- `profile: none` is a **terminal, allowlist-only** policy: egress is deny-by-default, ingress is allowed, and only
  your explicit `egress-allow`/`egress-deny` lists apply. DNS follows the same allow list: the VM resolves only names
  that an `egress-allow` entry matches (see [DNS under `none`](#dns-under-none)). This is how you restrict the VM to a
  specific set of hosts. `none` cannot be combined with another profile.
- **`host` opens every host port to the VM.** The group allows the whole host gateway, not a single service, so a VM
  that reaches a host MCP bridge can also reach anything else listening on the host. Combine it with `public` only when
  the VM must call a host service.
- Rule order in the generated firewall: gateway DNS (omitted under `none`), then `egress-deny`, then the profile allow
  rules, then `egress-allow`.
  microsandbox matches the first rule that applies, so a deny entry takes precedence over the profile's broad allow. With
  `profile: public` and `egress-deny: [123.123.123.0/24]`, `123.123.123.5` is blocked even though the public profile would
  otherwise allow it. Likewise `egress-allow: [123.123.0.0/16]` with `egress-deny: [123.123.123.0/24]` denies
  `123.123.123.5` while allowing `123.123.200.5` (a carve-out).

For example, to allow only a single API host:

```yaml
network:
  profile: none
  egress-allow:
    - api.example.com
```

Profile and lists can be combined, e.g. a `private` profile with an `egress-allow: [.internal]` exception.

To reach a service on the host — for example a stdio MCP bridge the host exposes at
`http://host.microsandbox.internal:<port>/mcp` — combine `host` with `public`:

```yaml
network:
  profile: [public, host]
```

Because `host` allows the whole host gateway, the bridge is reachable but so is every other port listening on the host.

The profile is also configurable via the `AGENTS_SANDBOX_NETWORK_PROFILE` environment variable and the `--network`
flag on `run`/`shell`. Both accept a comma-separated list or a repeated flag, e.g.
`agents-sandbox run --network public,host` or `--network public --network host`. Precedence:
**flag > env > config > default**. The `egress-allow`/`egress-deny` lists are config-file-only and have no env var or
flag.

`dns-servers` sets custom DNS upstreams for the VM's in-VM resolver. Bare IPs (IPv4 or IPv6) get `:53` appended; a
`host:port` / `ip:port` form is used as-is. An empty entry, a host without a port, or garbage is rejected at config-load
time. It is also configurable via the `AGENTS_SANDBOX_NETWORK_DNS_SERVERS` environment variable (comma-separated, e.g.
`1.1.1.1,8.8.8.8`) and the `--dns` flag on `run`/`shell` (comma-separated or repeated). Precedence:
**flag > env > config**. A policy that sets only `dns-servers` (no `profile`) still gets the default `public` profile.

```yaml
network:
  profile: none
  egress-allow: []          # host, CIDR, or .suffix
  egress-deny: []           # deny entries; emitted before profile and allow rules
  dns-servers:              # custom upstream resolvers (default: microsandbox's)
    - 1.1.1.1
    - 8.8.8.8:5353
```

The VM's resolver is a microsandbox DNS proxy on the gateway, and the proxy queries the upstream from the host. A custom
resolver's IP therefore does not need an `egress-allow` entry. Under `profile: none`, `dns-servers` only affects the
names the allow list lets the VM resolve.

## DNS under `none`

Under the `public`, `private`, and `host` profiles, the VM resolves any name. Under `none`, the DNS proxy answers only
names that an `egress-allow` entry matches and returns `NXDOMAIN` for every other name, without forwarding it
upstream:

| `egress-allow` entry | Resolves                                                  |
|----------------------|-----------------------------------------------------------|
| `api.example.com`    | `api.example.com` only                                    |
| `.example.com`       | `example.com` and every name under it                     |
| an IP or CIDR        | no names                                                  |
| `host`               | every name (the gateway DNS proxy is on the host gateway) |

This closes DNS as an exfiltration channel: with an empty or IP-only allow list, the VM cannot send data out by
encoding it in a query name such as `<encoded-secret>.attacker.example`. Directly queried resolvers (e.g.
`nslookup example.com 1.1.1.1`) are intercepted by the same proxy.

`profile: none` is still not an air gap. Every allowed destination is a channel the agent can send data through,
including over DNS-over-HTTPS when an allowed host serves it, and through any name under an allowed `.suffix` whose
domain someone else controls. Allow-list only hosts you trust with the data in the VM.
