---
title: Networking
layout: default
parent: Configuration
nav_order: 30
---

# Networking

The `network:` block controls the VM's network policy. It is baked in at VM creation, so changing it recreates the VM
(see [Resource Config Application]({% link configuration/launcher.md %}#resource-config-application)). When the whole `network:` block is absent,
the VM uses the `none` profile and denies egress by default.

| Field               | Type     | Description                                                                                                          |
|---------------------|----------|----------------------------------------------------------------------------------------------------------------------|
| `profile`           | string   | `public`, `private`, `host`, or `none`. Defaults to `none` (deny-by-default) when unset.                             |
| `egress-allow`      | []string | Egress destinations to allow: `host`, a CIDR (e.g. `123.123.0.0/16`), or a `.suffix` (e.g. `.internal`).              |
| `egress-deny`       | []string | Egress carve-outs, same destination forms as `egress-allow`. Emitted **before** allow rules (deny-before-allow).     |
| `dns-servers`       | []string | DNS upstream resolvers: a bare IP (auto-appends `:53`) or `host:port`. Overrides microsandbox's default resolver.     |
| `tls`               | object   | TLS interception settings (see below).                                                                               |

- `profile: none` is an **allowlist-only** profile: egress is deny-by-default, ingress is allowed, and only the
  gateway-DNS rule plus your explicit `egress-allow`/`egress-deny` lists apply. This is how you restrict the VM to a
  specific set of hosts. The `public`/`private`/`host` profiles additionally allow their whole destination class.
- Rule order in the generated firewall: profile rules (including gateway DNS), then `egress-deny`, then `egress-allow`.
  So `egress-allow: [123.123.0.0/16]` together with `egress-deny: [123.123.123.0/24]` denies `123.123.123.5` while
  allowing `123.123.200.5` (a carve-out).

For example, to allow only a single API host:

```yaml
network:
  profile: none
  egress-allow:
    - api.example.com
```

Profile and lists can be combined, e.g. a `private` profile with an `egress-allow: [.internal]` exception.

The profile is also configurable via the `OPENCODE_SANDBOX_NETWORK_PROFILE` environment variable and the `--network`
flag on `run`/`shell` (e.g. `agents-sandbox run --network public`). Precedence: **flag > env > config > default**. The
`egress-allow`/`egress-deny` lists are config-file-only and have no env var or flag.

`dns-servers` sets custom DNS upstreams for the VM's in-VM resolver. Bare IPs (IPv4 or IPv6) get `:53` appended; a
`host:port` / `ip:port` form is used as-is. An empty entry, a host without a port, or garbage is rejected at config-load
time. It is also configurable via the `OPENCODE_SANDBOX_NETWORK_DNS_SERVERS` environment variable (comma-separated, e.g.
`1.1.1.1,8.8.8.8`) and the `--dns` flag on `run`/`shell` (comma-separated or repeated). Precedence:
**flag > env > config**. A policy that sets only `dns-servers` (no `profile`) still gets the default `none` profile.

```yaml
network:
  profile: none
  egress-allow: []          # host, CIDR, or .suffix
  egress-deny: []           # deny entries; emitted before allow rules
  dns-servers:              # custom upstream resolvers (default: microsandbox's)
    - 1.1.1.1
    - 8.8.8.8:5353
```

With `profile: none`, only the gateway DNS is auto-allowed, so a custom resolver's IP must also be listed in
`egress-allow` (e.g. `egress-allow: [1.1.1.1]`) for DNS lookups to reach it.

### TLS interception

Microsandbox's transparent HTTPS proxy intercepts outbound TLS to inspect egress traffic. The launcher generates a
machine-wide interception CA on first use (stored under `~/.local/state/agents-sandbox/tls/`) and configures the proxy
to use it, instead of relying on the runtime's default certificate. The CA certificate is baked into managed runner
images (see [Runner image](../runner-image.md#tls-interception-ca)); changing the CA (or any TLS setting) recreates
the VM because the network policy is baked in at creation.

The `network.tls` block exposes the interceptor settings:

| Field              | Type     | Description                                                                          |
|--------------------|----------|--------------------------------------------------------------------------------------|
| `bypass`           | []string | Domain patterns (`*.suffix`) to skip MITM.                                            |
| `intercepted-ports`| []int    | Ports on which TLS is intercepted (default `[443]`).                                  |
| `block-quic`       | bool     | Block QUIC/HTTP3 on intercepted ports to force TLS fallback (default `true`).         |
| `verify-upstream`  | bool     | Verify upstream TLS certificates against the system bundle (default `true`).          |

```yaml
network:
  profile: public
  tls:
    bypass:
      - "*.internal.example.com"
    intercepted-ports:
      - 443
    block-quic: true
```

The `tls` block is config-file-only; it has no env var or flag. The generated CA certificate and its private key live
on the host and never enter the VM — only the certificate is shipped into images or guests.
