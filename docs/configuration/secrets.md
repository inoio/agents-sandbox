---
title: Secrets
layout: default
parent: Configuration
nav_order: 20
---

# Secrets

Secrets are environment variables whose real values stay on the host and are delivered through microsandbox's secret
mechanism. The VM receives a placeholder, not the real value. The network proxy replaces that placeholder only when a
request is sent to an allowed host over a connection whose destination can be verified.

This is different from an ordinary entry in `env`: values from `env` are copied into the VM as normal environment
variables. Put credentials in `env.secret` or `env.secret.yaml`, never in `env`.

## Quick start

If you use OpenCode's `/connect` command, follow [OpenCode authentication](#opencode-authentication) below. That is the
recommended workflow for credentials, which opencode stores in `auth.json`; it uses a literal microsandbox placeholder and an explicit
`home:` mapping.

The `provision-host-config: false` setting belongs in the top-level launcher configuration in `~/.config/agents-sandbox/config.yaml`.

## Format

Two file formats are supported: legacy text and structured YAML. YAML files take precedence over legacy files for
the same key.

**Legacy format** — `env.secret`

One `KEY=value@host` per line. The part after the **last** `@` is a policy tag restricting which microsandbox runtime
hosts can access the secret. Values may contain `@` — everything before the last `@` is the value. Each entry must
define a host explicitly; omitting the host part drops the secret with a warning.

```shell
# .agents-sandbox/env.secret
GITHUB_TOKEN=ghp_xxxxxxxxxxxx@github.com
ANTHROPIC_API_KEY=sk-ant-xxxxxxxxxxxx@anthropic.com
```

**YAML format** — `env.secret.yaml`

A YAML object map from env-var name to `{ value, host?, hosts?, allow_any_host_dangerous? }`. Values may contain **any
characters** including `@`. `host` and `hosts` are optional when `allow_any_host_dangerous` is set, but otherwise
required — entries with neither hosts nor a dangerous flag are dropped with a warning. JSON is also accepted (YAML 1.2
is a JSON superset).

```yaml
# .agents-sandbox/env.secret.yaml
GITHUB_TOKEN:
  value: "ghp_xxx@corp"
  host: microsandbox
ANTHROPIC_API_KEY:
  value: sk-ant-xxxxxxxx
  hosts: [gw-a.internal, gw-b.internal]
# No hosts defined — this entry is dropped with a warning
DROPPED_KEY:
  value: secret-value
TRUSTED_KEY:
  value: secret-value
  allow_any_host_dangerous: true
```

An empty `value` is valid and will be passed through unchanged.

## Precedence

Files are merged from lowest to highest precedence per key, in this order:

1. user-level `env.secret` (legacy `KEY=value@host`)
2. project-level `env.secret` (legacy)
3. user-level `env.secret.yaml`
4. project-level `env.secret.yaml`

A YAML entry always wins over a legacy entry for the same key, even across levels — a user-level
`env.secret.yaml` overrides a project-level `env.secret`. The YAML entry **fully replaces** the legacy entry's hosts —
if a YAML entry omits `host`, `hosts`, and `allow_any_host_dangerous`, the resulting entry has no hosts and is dropped
with a warning.

## Supported files

- `~/.config/agents-sandbox/env.secret` — user-level, legacy text format
- `~/.config/agents-sandbox/env.secret.yaml` — user-level, structured YAML (or JSON)
- `.agents-sandbox/env.secret` — project-level, legacy text format
- `.agents-sandbox/env.secret.yaml` — project-level, structured YAML (or JSON)

## Placeholder values

When no custom placeholder is configured, microsandbox uses the following guest-visible value:

```text
$MSB_<SECRET_NAME>
```

For example, the guest value of `GITHUB_TOKEN` is `$MSB_GITHUB_TOKEN`.

## OpenCode authentication

OpenCode stores credentials created by `/connect` in:

```text
~/.local/share/opencode/auth.json
```

That is different from the agents-sandbox input directory:

```text
~/.config/agents-sandbox/opencode/
```

An `auth.json` placed in the latter directory is mirrored to `/home/dev/.config/opencode/auth.json`; it is not
automatically OpenCode's credential store. To use secret placeholders in OpenCode's credential store, provision the
placeholder file explicitly with the launcher's `home:` key.

For example, for GitHub Copilot:

```yaml
# ~/.config/agents-sandbox/config.yaml
provision-host-config: false
home:
  # The source is relative to ~/.config/agents-sandbox/config.yaml.
  .local/share/opencode/auth.json: opencode/auth.json
```

```json
// ~/.config/agents-sandbox/opencode/auth.json
{
  "github-copilot": {
    "type": "oauth",
    "access": "$MSB_GH_COPILOT_ACCESS_TOKEN",
    "refresh": "$MSB_GH_COPILOT_REFRESH_TOKEN",
    "expires": 0
  }
}
```

```yaml
# ~/.config/agents-sandbox/env.secret.yaml
GH_COPILOT_ACCESS_TOKEN:
  value: "<access-token>"
  hosts:
    - api.githubcopilot.com
GH_COPILOT_REFRESH_TOKEN:
  value: "<refresh-token>"
  hosts:
    - api.githubcopilot.com
```

The network policy is separate from the secret allowlist. The VM must be allowed to reach the same endpoint, for example:

```yaml
# ~/.config/agents-sandbox/config.yaml
network:
  egress-allow:
    - api.githubcopilot.com
```

Use `agents-sandbox config home` to verify the `home:` mapping. `agents-sandbox config agent` shows the agent mirror and
drop-in candidates, but does not show `home:` mappings or files already present in the persistent home volume.

> **_NOTE:_**  After running `/connect` with opencode _inside_ the agents-sandbox, you can use `!` to switch to shell mode and then run
> ```
> cat ~/.local/share/opencode/auth.json
> ```
> to see the credentials stored in the sandbox (to store them outside in `env.secret.yaml`). 