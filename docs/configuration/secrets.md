---
title: Secrets
layout: default
parent: Configuration
nav_order: 20
---

# Secrets

Secrets are environment variables whose values are stored host-side only and delivered to the VM via the microsandbox
secret mechanism. They never appear in Docker images or environment dumps inside the VM.

## Format

Two file formats are supported: legacy text and structured YAML. YAML files take precedence over legacy files for
the same key.

**Legacy format** — `env.secret`

One `KEY=value@host` per line. The part after the **last** `@` is a policy tag restricting which microsandbox runtime
hosts can access the secret. Values may contain `@` — everything before the last `@` is the value. Each entry must
define a host explicitly; omitting the host part drops the secret with a warning.

```shell
# .opencode-sandbox/env.secret
GITHUB_TOKEN=ghp_xxxxxxxxxxxx@github.com
ANTHROPIC_API_KEY=sk-ant-xxxxxxxxxxxx@anthropic.com
```

**YAML format** — `env.secret.yaml`

A YAML object map from env-var name to `{ value, host?, hosts?, allow_any_host_dangerous? }`. Values may contain **any
characters** including `@`. `host` and `hosts` are optional when `allow_any_host_dangerous` is set, but otherwise
required — entries with neither hosts nor a dangerous flag are dropped with a warning. JSON is also accepted (YAML 1.2
is a JSON superset).

```yaml
# .opencode-sandbox/env.secret.yaml
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

- `~/.config/opencode-sandbox/env.secret` — user-level, legacy text format
- `~/.config/opencode-sandbox/env.secret.yaml` — user-level, structured YAML (or JSON)
- `.opencode-sandbox/env.secret` — project-level, legacy text format
- `.opencode-sandbox/env.secret.yaml` — project-level, structured YAML (or JSON)

## Accessing secrets inside the VM

Once set as a secret, the variable is available like any environment variable:

```shell
# Inside the sandbox (shell or opencode)
echo $GITHUB_TOKEN
```
