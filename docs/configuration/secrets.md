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

For the complete configuration decision tree, including `config migrate`, manual snippets, Claude Code login/API-key choices,
and `home:` provisioning, see [Manage config in the sandbox]({% link manage-config.md %}). This page focuses on the secret
file formats and the runtime behavior shared by all agents.

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

## Credential kinds

Migration classifies every native credential by a single concept, independent of agent:

- **Static** — an inline, non-refreshable secret that a placeholder can replace: API keys (`type: "api"` / `"api_key"`),
  well-known tokens (`type: "wellknown"`), and bearer tokens. These **are migrated**: the generated file contains a
  microsandbox placeholder, the raw value is written to `env.secret.yaml`, and the host is allowed for egress.
- **Login** — refreshable OAuth (`type: "oauth"`) or machine-bound login state that agents-sandbox cannot read (Claude
  Code's `.credentials.json`, `~/.claude.json`, or the OS keychain). These are **not copied**. Migration drops the entry
  from the generated files, allows the login hosts, and leaves you to authenticate inside the persistent sandbox home.

A refreshable OAuth token is refreshed by the agent and rewritten inside the VM, so treating it as a static secret would
either materialize real tokens in the VM or depend on fragile proxy-mediated refresh. That is why OAuth is never
placeholder-migrated. When a provider offers both a login and an API key, migration recommends the API-key path (a
host-side secret) while still allowing the login fallback.

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
automatically OpenCode's credential store.

Migration writes a managed `auth.json` containing placeholders for **static** credentials only. Refreshable OAuth
providers (for example GitHub Copilot, whose `type` is `"oauth"`) are omitted: run `/connect` inside the sandbox and the
login is kept in the persistent sandbox home. The migration allows that provider's login hosts so the in-sandbox flow can
reach them. See [Manage config in the sandbox]({% link manage-config.md %}#start-from-existing-agent-config).

To provision a static placeholder file explicitly instead of migrating, use the launcher's `home:` key:

```yaml
# ~/.config/agents-sandbox/config.yaml
# Native host config is disabled by default; keep this explicit when needed.
provision-host-config: false
home:
  # The source is relative to ~/.config/agents-sandbox/config.yaml.
  .local/share/opencode/auth.json: opencode/auth.json
```

```json
// ~/.config/agents-sandbox/opencode/auth.json
{
  "openai": { "type": "api", "key": "$MSB_OPENCODE_OPENAI_KEY" }
}
```

```yaml
# ~/.config/agents-sandbox/env.secret.yaml
OPENCODE_OPENAI_KEY:
  value: sk-proj-xxxxxxxx
  hosts:
    - api.openai.com
```

The network policy is separate from the secret allowlist. The VM must be allowed to reach the same endpoint, for example:

```yaml
# ~/.config/agents-sandbox/config.yaml
network:
  egress-allow:
    - api.openai.com
```

Use `agents-sandbox config home` to verify the `home:` mapping. `agents-sandbox config agent` shows the agent mirror and
drop-in candidates, but does not show `home:` mappings or files already present in the persistent home volume.

> **_NOTE:_**  After running `/connect` with OpenCode _inside_ the agents-sandbox, you can use `!` to switch to shell mode and then run
> ```
> cat ~/.local/share/opencode/auth.json
> ```
> to see the credentials stored in the sandbox. Login credentials must stay inside the persistent sandbox home rather than
> being copied back into host secret files; static keys you want to manage explicitly belong in `env.secret.yaml`.

## Pi authentication

Pi stores user settings and credentials in `~/.pi/agent/settings.json` and `~/.pi/agent/auth.json`; custom providers and
endpoints are stored in `~/.pi/agent/models.json`. Use `agents-sandbox config migrate --agent pi` to create managed copies
of these files. **Static** credential values are replaced with `$MSB_PI_*` placeholders and raw values are written only to
the user-level `env.secret.yaml`; custom endpoint hosts are added to the network policy after review. **Login** entries
(for example GitHub Copilot's `"oauth"` credentials) are dropped from the generated files and their login hosts are
allowed so you can authenticate inside the persistent sandbox home.

The migration aborts when `auth.json` values reference an environment variable or shell command (`"$MY_KEY"`,
`"!op read ..."`), or when `models.json` contains raw custom credential headers. Move those values to `env.secret.yaml`
manually first (see [Limitations]({% link manage-config.md %}#limitations)).

## Claude Code authentication

Claude Code can consume credentials through secret environment variables:

```yaml
# ~/.config/agents-sandbox/env.secret.yaml
CLAUDE_ENV_ANTHROPIC_API_KEY:
  value: sk-ant-xxxxxxxx
  hosts:
    - api.anthropic.com
```

The migration creates this kind of entry in API-key mode. Claude Code is the login-only case: it declares no migratable
credential file (`NativeCredential` is empty), and its login hosts (`api.anthropic.com`, `platform.claude.com`,
`claude.ai`, `claude.com`) are pre-allowed so a normal login can be performed inside the persistent sandbox home. The
host's `.credentials.json`, `~/.claude.json`, and operating-system keychain are not copied: they are
machine-bound/runtime-managed state, not a portable credential interface, and copying them would turn credential material
into ordinary files inside the VM. See [Manage config in the sandbox]({% link manage-config.md %}) for the complete migration
workflow and its authentication choices.
