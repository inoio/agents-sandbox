---
title: Manage config in the sandbox
layout: default
nav_order: 40
---

# Manage config in the sandbox

This page describes the supported ways to configure an agent inside agents-sandbox. Choose the workflow that matches your
starting point:

- **Start from an existing agent config:** run `agents-sandbox` and accept the offered safe migration.
- **Manual configuration** with agent snippets, without reading or migrating native host files.
- **Manual secrets** with `env.secret` or `env.secret.yaml`.
- **Provision files and startup hooks** with the `home:` configuration key.

Native host-agent configuration is never copied into the VM unless you opt in with `provision-host-config: true` (see
[Unsafe host-config drop-in](#unsafe-host-config-drop-in)).

## Start from existing agent config

Use this workflow when the agent already works on the host and you want a managed, reviewable sandbox configuration without
copying raw host credentials into the VM.

The migration reads the native agent files, replaces credential values with microsandbox placeholders, and writes managed
copies for the sandbox. The native agent files are never modified.

### First start

When you start `agents-sandbox` interactively and native configuration for the selected agent exists, agents-sandbox asks before
the VM starts:

| Choice | Effect |
| --- | --- |
| **Migrate safely** (default) | Runs the migration described below, shows the result for review, and writes it after confirmation. |
| **Use native host config** | Sets `provision-host-config: true` and exits; rerun `agents-sandbox` to start with the [unsafe drop-in](#unsafe-host-config-drop-in). |
| **Continue without migration** | Starts without native config. The choice is remembered until the native files change; then you are asked again. |

The question is not asked when `provision-host-config: true` is set or managed files for the agent already exist. A
non-interactive start only prints a warning.

To run the same migration without starting a VM, use the host-only command (see [`config migrate`]({% link commands.md %}#config-migrate-name)):

```console
agents-sandbox config migrate --agent opencode
agents-sandbox config migrate --agent pi
agents-sandbox config migrate --agent claude-code
```

### Example

Assume an OpenCode setup with an OpenAI API key in `~/.local/share/opencode/auth.json`:

```json
{ "openai": { "type": "api", "key": "sk-proj-xxxxxxxx" } }
```

The migration writes three things on the host:

```json
// ~/.config/agents-sandbox/opencode/auth.json (managed copy; contains only a placeholder)
{ "openai": { "type": "api", "key": "$MSB_OPENCODE_OPENAI_KEY" } }
```

```yaml
# ~/.config/agents-sandbox/env.secret.yaml (raw value; stays on the host)
OPENCODE_OPENAI_KEY:
  value: sk-proj-xxxxxxxx
  hosts:
    - api.openai.com
```

```yaml
# ~/.config/agents-sandbox/config.yaml (additions)
home:
  .local/share/opencode/auth.json: opencode/auth.json
network:
  egress-allow:
    - api.openai.com
```

In the VM, OpenCode reads the managed `auth.json` and sends the placeholder. The microsandbox proxy substitutes the real key
only for requests to `api.openai.com`. Two separate policies apply: the secret's `hosts` list controls where the value may be
substituted, and `network.egress-allow` controls where the VM may connect at all. Known providers get their host automatically;
for unknown providers the migration asks for the destination host.

Credentials in the agent's settings file are handled the same way. Placeholders use the syntax the agent resolves itself, for
example `{env:OPENCODE_CONFIG_PROVIDER_CUSTOM_OPTIONS_APIKEY}` in an OpenCode config file.

### What is migrated

Managed files are written to `~/.config/agents-sandbox/<config dir>/`. Secrets are named with the agent's prefix and added to
`~/.config/agents-sandbox/env.secret.yaml`.

| Agent | Native input | Managed files | Secret prefix |
| --- | --- | --- | --- |
| OpenCode, OpenCode 2 | Config files in `~/.config/opencode/` (`opencode.json*`, `opencode.yaml`, `config.json`) and `~/.local/share/opencode/auth.json`; honors `XDG_CONFIG_HOME` and `XDG_DATA_HOME` | `opencode/opencode-migrated.jsonc`, `opencode/auth.json` | `OPENCODE_` |
| Pi | `~/.pi/agent/settings.json`, `auth.json`, and `models.json`; honors `PI_CODING_AGENT_DIR` | `pi/settings-migrated.json`, `pi/auth.json`, `pi/models.json` | `PI_` |
| Claude Code | `~/.claude/settings.json`; honors `CLAUDE_CONFIG_DIR` | `claude/settings-migrated.json` | `CLAUDE_` |

Both OpenCode profiles share the native config and auth format, so they migrate the same files. Credential stores such as
`auth.json` and Pi's `models.json` are mapped to their native VM path with a `home:` entry; the `*-migrated.json*` files are
regular [config snippets]({% link configuration/agent.md %}#config-snippet-merge). Hosts of custom provider endpoints, such as a
`baseURL`, are added to the secret's `hosts` and to `network.egress-allow`.

### Claude Code authentication choices

Claude Code migration supports two authentication paths:

- **Normal login (default):** start Claude Code in the sandbox and use its regular `/login` flow. The login is kept in the
  persistent sandbox home for later sessions. The migration adds the login hosts (`api.anthropic.com`, `platform.claude.com`,
  `claude.ai`, `claude.com`) to `network.egress-allow`.
- **Anthropic API key:** enter an endpoint and a key during migration. The generated settings use a microsandbox placeholder,
  while the raw key is stored as `CLAUDE_ENV_ANTHROPIC_API_KEY` in the host-side `env.secret.yaml`; the endpoint is added to the
  network allowlist.

Host login state such as `.credentials.json`, `~/.claude.json`, and operating-system keychain entries is not copied. See
[Secrets]({% link configuration/secrets.md %}) for the credential and endpoint details.

Without native Claude Code settings on the host, the first interactive start offers the same setup, so the login hosts are
allowed before you run `/login` in the sandbox.

### Review and apply

Before writing, the migration shows the generated paths and non-secret file contents, asks you to confirm inferred network hosts,
and warns about auth fields it does not recognize. Raw secret values are never shown. Nothing is written until you confirm.

When writing:

- `env.secret.yaml` is merged: existing entries are kept, and the file stays mode `0600`. An existing entry with the same name
  but a different value aborts the migration instead of being overwritten.
- Edits to a YAML launcher config keep its comments and key order. A `config.json`, `config.jsonc`, or `config.json5` is
  rewritten as plain JSON and loses its comments and formatting; the review warns about this beforehand.
- Progress is recorded in `~/.local/state/agents-sandbox/<agent>/config-migration.yaml`. If a write is interrupted, rerunning
  retries the migration for the current native configuration.

Afterwards, start the agent normally and inspect the result with `agents-sandbox config agent` (managed agent configuration) and
`agents-sandbox config home` (resolved `home:` mappings).

### Limitations

The migration only transforms values it understands. It aborts, without writing anything, when the native configuration contains:

- a value that references an environment variable or command, such as Pi's `"$MY_KEY"` or `"!op read ..."`;
- custom credential headers or `env` overrides that it cannot map to a secret, for example in Pi's `models.json`;
- an endpoint URL with embedded credentials, such as `https://user:pass@host` or `?api_key=...`;
- a Claude Code credential helper or refresh command (`apiKeyHelper`, `awsAuthRefresh`, `awsCredentialExport`).

Move such values to `env.secret.yaml` manually and remove them from the native file, or use the
[manual configuration](#manual-configuration-without-config-migrate) instead. Auth fields that are neither known credentials nor
known metadata are kept unchanged and reported as "review required"; check them before confirming.

### After the migration

The migration is a one-time copy. Later changes to the native agent configuration are **not** synchronized into the managed
files. Edit the managed files directly, or migrate again:

1. Delete the managed files listed in [What is migrated](#what-is-migrated) from `~/.config/agents-sandbox/<config dir>/`.
   `config migrate` refuses to run while any of them exists, so it never overwrites your edits.
2. Remove the agent's old entries (with the agent's secret prefix) from `env.secret.yaml` if their values changed.
3. Run `agents-sandbox config migrate --agent <name>` or start `agents-sandbox`.

The `home:` and `network.egress-allow` entries added earlier stay in `config.yaml` and are reused.

## Manual configuration (without config migrate)

Use this workflow when you want to build a clean configuration from scratch, or when the native configuration contains fields
that safe migration intentionally does not understand.

Create snippets in the user-level or project-level agent directory:

| Agent | User snippets | Project snippets | Matching files |
| --- | --- | --- | --- |
| OpenCode / OpenCode 2 | `~/.config/agents-sandbox/opencode/` | `.agents-sandbox/opencode/` | `opencode*.json*` |
| Pi | `~/.config/agents-sandbox/pi/` | `.agents-sandbox/pi/` | `settings*.json*` |
| Claude Code | `~/.config/agents-sandbox/claude/` | `.agents-sandbox/claude/` | `settings*.json*` |

User snippets are merged first, then project snippets. Files are deep-merged in alphabetical order within each directory. The
result is written to the agent's config path in the VM. See [Agent configuration]({% link configuration/agent.md %}) for the
mirror rules, config paths, precedence, and permissions.

Example OpenCode snippet setup:

```shell
mkdir -p ~/.config/agents-sandbox/opencode
cp opencode.jsonc ~/.config/agents-sandbox/opencode/opencode-model.jsonc
```

For Pi and Claude Code, create `settings*.json*` snippets rather than copying their native credential stores. Native
`.credentials.json`, `auth.json`, and similar files should be handled through the agent's supported secret mechanism or through
the migration flow above.

Manual configuration is useful when you want only selected settings. It also makes the configuration reproducible: snippets can
be reviewed and committed when they contain no secrets.

## Manual secrets

Put API keys and tokens in a secret file, never in ordinary `env`, a snippet, `home:`, or `/workspace`:

```yaml
# ~/.config/agents-sandbox/env.secret.yaml
ANTHROPIC_API_KEY:
  value: sk-ant-xxxxxxxx
  hosts:
    - api.anthropic.com
```

The real value remains on the host. The VM sees a placeholder and Microsandbox substitutes the value only for an allowed,
verifiable destination. See [Secrets]({% link configuration/secrets.md %}) for file formats, precedence, placeholder behavior,
and agent-specific examples.

For Claude Code, use one of the documented environment variables:

- `ANTHROPIC_API_KEY` for an Anthropic API key.
- `ANTHROPIC_AUTH_TOKEN` for a bearer token or gateway credential.
- `CLAUDE_CODE_OAUTH_TOKEN` for a token created by `claude setup-token`.

For a custom Claude gateway, add the endpoint host to both the secret's `hosts` list and `network.egress-allow`. The secret file
may already contain unrelated entries; add the Claude entry without replacing the existing map.

## Provision files and startup hooks

Use `home:` when the agent or your workflow needs ordinary dotfiles, certificates, scripts, or other non-secret files in the VM
home:

```yaml
home:
  .config/tooling/rc: tooling/rc
  .local/bin/project-login:
    source: scripts/project-login
    hook: startup
```

`home:` is also the mechanism for startup hooks. Hooks run when the VM starts, before the agent session, and may prompt for
interactive input. A long-running hook must daemonize itself. See [Home provisioning & startup hooks]({% link configuration/home-provisioning.md %})
for source resolution, permissions, hook interpreters, and reserved paths.

Do not use `home:` for API keys or native Claude/OpenCode credential stores if the value must stay off the VM. Those files are
ordinary files once provisioned.

## Unsafe host-config drop-in

For the edge case where you explicitly want native host files copied into the VM, see the [Unsafe host-config
drop-in]({% link provision-host-config.md %}). This is neither required for migration nor part of the normal manual
configuration workflow.

## Next steps

- [Agent configuration]({% link configuration/agent.md %}) — snippets, mirrors, config paths, and drop-in precedence.
- [Secrets]({% link configuration/secrets.md %}) — secret files, allowlists, and placeholder mechanics.
- [Home provisioning & startup hooks]({% link configuration/home-provisioning.md %}) — dotfiles and startup scripts.
- [Networking]({% link configuration/networking.md %}) — egress profiles and allow/deny rules.
- [Configuration files & Environment variables]({% link configuration/launcher.md %}) — all launcher settings.
