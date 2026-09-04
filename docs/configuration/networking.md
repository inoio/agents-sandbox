---
title: Networking
layout: default
parent: Configuration
nav_order: 30
---

# Networking

The `network:` block controls the VM's network policy. It is baked in at VM creation, so changing it recreates the VM
(see [Resource Config Application]({% link configuration/launcher.md %}#resource-config-application)). When the whole `network:` block is absent, the VM gets
microsandbox's default (public) — no behavior change for existing users.

| Field               | Type     | Description                                                                                                          |
|---------------------|----------|----------------------------------------------------------------------------------------------------------------------|
| `profile`           | string   | `public`, `private`, `host`, or `none`. Defaults to `public` (microsandbox's default) when unset.                    |
| `egress-allow`      | []string | Egress destinations to allow: `host`, a CIDR (e.g. `123.123.0.0/16`), or a `.suffix` (e.g. `.internal`).              |
| `egress-deny`       | []string | Egress carve-outs, same destination forms as `egress-allow`. Emitted **before** allow rules (deny-before-allow).     |

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
flag on `run`/`shell` (e.g. `opencode-sandbox run --network none`). Precedence: **flag > env > config > default**. The
`egress-allow`/`egress-deny` lists are config-file-only and have no env var or flag.

```yaml
network:
  profile: public
  egress-allow: []          # host, CIDR, or .suffix
  egress-deny: []           # carve-outs; emitted before allow rules
```
