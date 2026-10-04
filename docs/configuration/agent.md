---
title: Agent configuration
layout: default
parent: Configuration
nav_order: 60
---

# Agent configuration

agents-sandbox is agent-aware. A `--agent <name>` flag on `run`, `shell`, `build`, `volume`, `stop`, and `kill`
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

- **User:** `~/.config/agents-sandbox/<agent>/` (e.g. `~/.config/agents-sandbox/opencode/`)
- **Project:** `.agents-sandbox/<agent>/` (e.g. `.agents-sandbox/opencode/`)

VMs, home volumes, and state are also scoped per agent: the VM name, home volume, and state file all carry the agent, so
switching agents no longer tears down the project sandbox and multiple agents can serve the same project concurrently
(see [Sandboxes]({% link sandboxes.md %}#vm-identity)).

## Config snippet merge

agents-sandbox provisions a single agent config into the VM. No embedded provider or permission config is shipped.
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

Run `agents-sandbox config agent` to print the merged config that would be provisioned into the VM.

> **Note:** the merged config is written to the agent's VM config path (for opencode, `opencode.jsonc`, the last file
> opencode loads: `config.json` < `opencode.json` < `opencode.jsonc`), so it always wins the deep merge. When snippets
> exist, the config-file family (for opencode: `config.json`, `opencode.json`, `opencode.jsonc`, …) is removed from the
> VM so a host drop-in copy of any of those files cannot shadow the merged snippet config.

See the [permissions example](#example-permissions) for a concrete snippet.

## Verbatim config directory mirror

Beyond the snippet merge, the `<agent>` config directories (`~/.config/agents-sandbox/<agent>/` and
`.agents-sandbox/<agent>/`) act as a **1:1 verbatim mirror** of the agent's VM config directory. Every file and
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

> **File modes:** Home mappings, the verbatim mirror, and the host-config drop-in
> copy preserve ordinary Unix permission bits, including executable bits.
> Launcher scripts therefore remain directly executable in the VM.

The mirror is **always active**, independent of `provision-host-config`. Stale mirrored files (deleted from the host)
are left in place in the VM. Run `agents-sandbox config agent` to list the mirror files, each shown as its host source
path → VM path. Previously non-pattern files in `<agent>/` were silently ignored; they are now mirrored verbatim.

## Host-config drop-in provisioning

This section is the technical reference for the per-agent provision rules. The rules are only evaluated when
`provision-host-config: true`; the setting is disabled by default. For when to use this opt-in, how to enable it, and its security
tradeoff, see [Unsafe host-config drop-in]({% link provision-host-config.md %}).

The drop-in copy is limited to the native paths below; files outside these paths are not copied:

- **opencode** — `~/.config/opencode/**` (excluding `node_modules/`, `package*.json`, and `.gitignore`) plus
  `~/.local/share/opencode/auth.json`.
- **opencode2** — same drop-in copy as `opencode` (`~/.config/opencode/**` and `~/.local/share/opencode/auth.json`).
- **pi** — files under `~/.pi/agent/` except `auth.json` and `models.json`.
- **claude-code** — `~/.claude/settings.json`; the host's machine-managed `.credentials.json` is not copied. A user can
  either log in inside the persistent sandbox home or use secret-backed environment credentials.

Precedence is, from strongest to weakest: `home:` mappings, merged snippet config, the verbatim config-directory mirror, and the
host-config drop-in.

When snippets exist, the drop-in copy of the config-file family is skipped entirely (see the note above) so host config
cannot override the merged snippets. Non-config files — e.g. plugins, custom commands, themes — are still copied.

When disabled, no host-config files are copied; snippet merging, the mirror, and `home:` mappings remain active. Because the VM
home is persistent, files copied by an earlier drop-in can remain. On start, copies that still match the host file are removed;
files that differ, such as an `auth.json` written by a login inside the VM, are kept until you delete them or reset the home
volume.

### Credential boundaries

> **Security note:** when host-config drop-in provisioning is enabled, the OpenCode `auth.json` credential file is copied into
> the VM. To deliver credentials exclusively through the env-secret mechanism (which never writes them into the VM, see
> [Secrets]({% link configuration/secrets.md %})), leave host-config provisioning disabled. The env-secret channel remains
> fully supported and unchanged; this does not replace it.

If the drop-in is enabled, exclude OpenCode's `auth.json` by placing a `home:` entry that overrides the provisioned path (see
[Home provisioning & startup hooks]({% link configuration/home-provisioning.md %})), or remove the credential file from the host
before running. The launcher does not inject host secrets in any other way; the env-secret mechanism is the supported channel for
secrets you do not want on disk in the VM.

Pi's `auth.json` and `models.json` are excluded from the drop-in. Copies of these files left in the sandbox home by an earlier
drop-in are removed on start when they still match the host file; files created inside the VM, such as by a Pi login, are
kept. Claude Code's host `.credentials.json`, `~/.claude.json`, and
operating-system keychain state are also excluded; use Claude Code's normal login inside the persistent sandbox home or the secret
mechanism instead. See [Secrets]({% link configuration/secrets.md %}) for the supported secret formats and agent-specific
authentication details.

## Example: Permissions

Opencode permissions are configured through opencode config snippets, which agents-sandbox merges (user-first, then
project). Place a snippet in your project, e.g. `.agents-sandbox/opencode/permission.json5`.

**Quasi-auto:** allow everything except what is explicitly denied:

```json5
{
  // .agents-sandbox/opencode/permission.json5
  permission: {
    "*": "allow",
  },
}
```

**Protect secrets:** deny reads of `.env` and `.envrc` files:

```json5
{
  // .agents-sandbox/opencode/permission.json5
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
