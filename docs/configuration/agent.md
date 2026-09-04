---
title: Agent configuration
layout: default
parent: Configuration
nav_order: 60
---

# Agent configuration

opencode-sandbox is agent-aware. A `--agent <name>` flag on `run`, `shell`, `build`, `volume`, `stop`, and `kill`
selects the coding-agent profile to run, build, provision, or manage. The agent can also be selected via the `agent`
config key or the `OPENCODE_SANDBOX_AGENT` environment variable. Four agents ship as built-in profiles:

- **`opencode`** (default) — daemon-based, with serve/attach, worktree sessions, and GitHub-release upgrade checks.
- **`opencode2`** — opencode 2 (beta), installed from `@opencode-ai/cli@beta` on npm; daemon-based with serve/attach,
  worktree sessions, and npm beta-tag upgrade checks. It shares the `opencode` config directory (v2 reads the same files
  as v1).
- **`pi`** — the pi coding agent, run interactively; upgrade checks via `pi.dev`.
- **`claude-code`** — Anthropic's Claude Code, run interactively; upgrade checks via the npm registry.

`--worktree` and `--serve-only` are rejected for pi and claude-code (they have no daemon); they run through the
interactive TUI instead. Passing an unsupported `--agent` name reports the valid names in its error message.

Each agent owns its config directories, one subdir per agent under the tool's config base:

- **User:** `~/.config/opencode-sandbox/<agent>/` (e.g. `~/.config/opencode-sandbox/opencode/`)
- **Project:** `.opencode-sandbox/<agent>/` (e.g. `.opencode-sandbox/opencode/`)

VMs, home volumes, and state are also scoped per agent: the VM name, home volume, and state file all carry the agent, so
switching agents no longer tears down the project sandbox and multiple agents can serve the same project concurrently
(see [Sandboxes]({% link sandboxes.md %}#vm-identity)).

## Config snippet merge

opencode-sandbox provisions a single agent config into the VM. No embedded provider or permission config is shipped.
Instead, the agent config is assembled from **snippet files** that match the agent's snippet pattern, collected from the
user and project directories, and written to the agent's VM config path:

- **opencode** — snippets match `opencode*.json*` (e.g. `opencode-model.json`, `opencode-permissions.jsonc`); merged to
  `/home/dev/.config/opencode/opencode.jsonc`. A file named exactly `opencode.json` no longer merges by default.
- **opencode2** — same snippet pattern and merged config path as `opencode` (v2 reads the same files as v1).
- **pi** — snippets match `settings*.json*` in the `pi/` subdir; merged to `/home/dev/.pi/agent/settings.json`.
- **claude-code** — snippets match `settings*.json*` in the `claude/` subdir; merged to `/home/dev/.claude/settings.json`.

Matching files are parsed and **deep-merged** into one config document. The user directory is merged first, then the
project directory; within each directory files are merged in alphabetical order, so later files override earlier ones.
Snippet parsing supports JSON, JSONC, JSON5, and YAML (agents whose pattern includes YAML extensions, e.g. a pattern
like `pi-*.{json,yaml}`). The built-in patterns above match JSON-family extensions.

If no snippet files exist, no merged config is produced.

Run `opencode-sandbox config agent` to print the merged config that would be provisioned into the VM.

> **Note:** the merged config is written to the agent's VM config path (for opencode, `opencode.jsonc`, the last file
> opencode loads: `config.json` < `opencode.json` < `opencode.jsonc`), so it always wins the deep merge. When snippets
> exist, the config-file family (for opencode: `config.json`, `opencode.json`, `opencode.jsonc`, …) is removed from the
> VM so a host drop-in copy of any of those files cannot shadow the merged snippet config.

See the [permissions example](#example-permissions) for a concrete snippet.

## Verbatim config directory mirror

Beyond the snippet merge, the `<agent>` config directories (`~/.config/opencode-sandbox/<agent>/` and
`.opencode-sandbox/<agent>/`) act as a **1:1 verbatim mirror** of the agent's VM config directory. Every file and
subdirectory in them is copied verbatim into the VM, preserving its relative path (for opencode: `tui.json`, `AGENTS.md`,
`agents/`, `commands/`, `themes/`, `plugins/`, `skills/`, `tools/`, …). This makes your whole agent setup available in
the VM without extra configuration.

Two families of files are *not* mirrored:

- Files matching the agent's snippet pattern (`opencode*.json*`, `settings*.json*`) are **deep-merged** (see [Config
  snippet merge](#config-snippet-merge)), never mirrored.
- The agent's config-file family (for opencode: `config.json`, `opencode.json`, `opencode.jsonc`, …) is **reserved for
  the merged output** and never mirrored, so a mirror copy cannot shadow the merged snippet config.

Precedence when the same VM path is reachable from multiple sources:

`home:` > merged snippet config > verbatim mirror > drop-in provisioning

The mirror is **always active**, independent of `provision-host-config`. Stale mirrored files (deleted from the host)
are left in place in the VM. Run `opencode-sandbox config agent` to list the mirror files, each shown as its host source
path → VM path. Previously non-pattern files in `<agent>/` were silently ignored; they are now mirrored verbatim.

## Default drop-in provisioning

Beyond the snippet merge, when running the launcher now **copies the active agent's config + credential files from the
host into the VM by default**, driven by a per-agent gitignore-style include-list manifest (provision rules). This means
your normal agent setup (e.g. an existing opencode config) works without extra configuration.

The drop-in copy is scoped to the agent's settings file, not its runtime state or credentials:

- **opencode** — `~/.config/opencode/**` (excluding `node_modules/`, `package*.json`, and `.gitignore`) plus
  `~/.local/share/opencode/auth.json`.
- **opencode2** — same drop-in copy as `opencode` (`~/.config/opencode/**` and `~/.local/share/opencode/auth.json`).
- **pi** — `~/.pi/agent/settings.json`.
- **claude-code** — `~/.claude/settings.json` (runtime state and the machine-managed `.credentials.json` are not copied).

Precedence: the merged snippet config and any `home:` mappings override the drop-in copy for the same VM path.

When snippets exist, the drop-in copy of the config-file family is skipped entirely (see the note above) so host config
cannot override the merged snippets. Non-config files — e.g. plugins, custom commands, themes — are still copied.

To switch from your agent's own config to `opencode-sandbox/<agent>` snippet provisioning, create snippet files in the
user or project snippet directories (see [Config snippet merge](#config-snippet-merge)). Once a snippet matching the
agent's pattern exists, it wins over the host config for the merged config path. To stop the native-config drop-in
entirely so only the snippet merge and `home:` mappings apply, set `provision-host-config: false` (below).

To turn off the drop-in copy altogether (config **and** credentials), set `provision-host-config: false` in the launcher
config:

```yaml
provision-host-config: false
```

This skips the whole host-config copy (for opencode: `~/.config/opencode/**` and `auth.json`), while the snippet merge
and `home:` mappings keep working. On the next run, previously drop-in-copied config and credential files are removed
from existing home volumes so they cannot linger.

### Authentication: file copy vs. env-secret

> **Security note:** because of the drop-in provisioning above, the opencode `auth.json` credential file is now copied
> into the VM by default. If you prefer to deliver credentials exclusively through the env-secret mechanism (which never
> writes them into the VM, see [Secrets]({% link configuration/secrets.md %})), you can opt out of the credential file copy. The env-secret channel
> remains fully supported and unchanged; this does not replace it.

To opt out, exclude `auth.json` from the drop-in copy by placing a `home:` entry that overrides the provisioned path
(see [Home provisioning & startup hooks]({% link configuration/home-provisioning.md %})), or remove the credential file from the host before running. The launcher does not inject
host secrets in any other way; the env-secret mechanism is the supported channel for secrets you do not want on disk in
the VM.

For pi and claude-code, the drop-in copy does not include credential files; authenticate them with env secrets instead:

- **pi** — per-provider env vars, e.g. `ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `GEMINI_API_KEY` (see pi's docs for the
  full list). Put them in an `env.secret` / `env.secret.yaml` file (below).
- **claude-code** — `ANTHROPIC_AUTH_TOKEN`, `ANTHROPIC_API_KEY`, or `CLAUDE_CODE_OAUTH_TOKEN`. Claude's
  `.credentials.json` is machine-managed and not hand-provisioned, so env vars are the supported channel here.
- **opencode** — `OPENCODE_API_KEY`.

## Example: Permissions

Opencode permissions are configured through opencode config snippets, which opencode-sandbox merges (user-first, then
project). Place a snippet in your project, e.g. `.opencode-sandbox/opencode/permission.json5`.

**Quasi-auto:** allow everything except what is explicitly denied:

```json5
{
  // .opencode-sandbox/opencode/permission.json5
  permission: {
    "*": "allow",
  },
}
```

**Protect secrets:** deny reads of `.env` and `.envrc` files:

```json5
{
  // .opencode-sandbox/opencode/permission.json5
  permission: {
    denylist: [
      { tool: "read", files: [".env", ".envrc"] },
    ],
  },
}
```

> **Caveat:** these rules are advisory for opencode's own Q&A tools. The `bash` tool executes arbitrary commands inside
> the VM and can read any file regardless of these deny rules, so they are not a security boundary — keep secrets out of
> the VM or rely on the [secret mechanism]({% link configuration/secrets.md %}) instead.
