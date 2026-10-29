---
title: Agent configuration
description: "Select and configure the coding agent run by agents-sandbox: opencode, opencode2, pi, Claude Code or Codex."
layout: default
parent: Configuration
nav_order: 60
---

# Agent configuration

agents-sandbox is agent-aware. A `--agent <name>` flag on `run`, `shell`, `build`, `volume`, `stop`, and `kill`
selects the coding-agent profile to run, build, provision, or manage. The agent can also be selected via the `agent`
config key or the `AGENTS_SANDBOX_AGENT` environment variable. Six agents ship as built-in profiles:

- **`opencode`** — daemon-based, with serve/attach, worktree sessions, and GitHub-release upgrade checks.
- **`opencode2`** (default) — opencode 2 (beta), installed from its upstream installer (`https://opencode.ai/v2/install`); the
  opencode update API's `latest` channel is used for upgrade checks and unpinned builds. Daemon-based with serve/attach, worktree sessions. It owns its own launcher
  config namespace
  (`~/.config/agents-sandbox/opencode2/` and `.agents-sandbox/opencode2/`), separate from `opencode`, because v2's config
  schema (e.g. structured `providers.*.models.*.capabilities`) differs from v1's — so the two can now diverge. Its VM
  config path and host drop-in stay `~/.config/opencode/` (v2 reads the same files as v1).
- **`pi`** — the pi coding agent, run interactively; upgrade checks via `pi.dev`.
- **`claude-code`** — Anthropic's Claude Code, run interactively; upgrade checks via the npm registry.
- **`codex`** — OpenAI's Codex CLI, run interactively; upgrade checks via GitHub releases. Its startup update check is
  turned off because agents-sandbox offers upgrades itself. Codex's own command sandbox is turned off too
  (`sandbox_mode = "danger-full-access"`), because the VM already isolates it. This overrides `sandbox_mode` in `config.toml`; to choose another mode, pass it as an agent
  argument, e.g. `agents-sandbox --agent codex -- --sandbox workspace-write`. Codex still asks for approval according
  to its `approval_policy`.
- **`hermes`** — Hermes Agent, installed from its official installer at a pinned release tag and run interactively;
  upgrade checks via its GitHub releases.

`--worktree` and `--serve-only` are rejected for pi, claude-code, codex, and hermes (they have no daemon); they run through the
interactive TUI instead. Passing an unsupported `--agent` name reports the valid names in its error message.

> **Note:** opencode2 server auth — v2's `serve` requires HTTP basic auth on every `/api/*` route and would otherwise generate a
> random password the launcher cannot know. The runner image therefore bakes a fixed `OPENCODE_SERVER_PASSWORD`, and the
> health probe, worktree API calls, and in-VM attach client all use it. v2 hardcodes the basic-auth username to
> `opencode` (there is no `OPENCODE_SERVER_USERNAME`), so the fixed credential is `opencode:opencode` — the value an
> external `--serve-only` client must use to connect (see the [connect-desktop recipe]({% link recipes/connect-desktop.md %})).

Each agent owns its config directories, one subdir per agent under the tool's config base:

- **User:** `~/.config/agents-sandbox/<agent>/` (e.g. `~/.config/agents-sandbox/opencode/`, `~/.config/agents-sandbox/opencode2/`)
- **Project:** `.agents-sandbox/<agent>/` (e.g. `.agents-sandbox/opencode/`, `.agents-sandbox/opencode2/`)

While neither dir exists (and no legacy fallback applies), every `run` and `shell` prints an info line naming both
paths, so a fresh install shows where launcher config goes. Creating either dir silences it.

> **Note:** Legacy fallback, migrating from an older version — the `opencode2` launcher config dir is new. While **no**
> `opencode2` dir exists in **either** scope, `opencode2` reads the old shared `opencode` dir — which the `opencode`
> agent also uses — and warns on every run while it falls back. To migrate, **copy** the files you want `opencode2` to
> read from `~/.config/agents-sandbox/opencode/` into `~/.config/agents-sandbox/opencode2/` (and the project
> equivalent); copy, not move, if you also use `opencode`. The two scopes switch together: as soon as an `opencode2`
> dir exists in **either** the user or the project scope, **both** scopes read their `opencode2/` dir and the warning
> stops — so copy **all** the files in one go, because a partial `opencode2/` dir disables the fallback for the other
> scope too and silently ignores the leftovers. The guest `~/.config/opencode/` path is never affected.

VMs, home volumes, and state are also scoped per agent: the sandbox name, home volume, and state file all carry the agent, so
switching agents no longer tears down the project sandbox and multiple agents can serve the same project concurrently
(see [Sandboxes]({% link sandboxes.md %}#identity-one-sandbox-per-checkout-and-agent)).

## Config snippet merge

agents-sandbox provisions a single agent config into the VM. No embedded provider or permission config is shipped.
Instead, the agent config is assembled from **snippet files** that match the agent's snippet pattern, collected from the
user and project directories, and written to the agent's VM config path:

- **opencode** — snippets match `opencode*.json*` (e.g. `opencode-model.json`, `opencode-permissions.jsonc`); merged to
  `/home/dev/.config/opencode/opencode.jsonc`. A file named exactly `opencode.json` no longer merges by default.
- **opencode2** — same snippet pattern and merged config path as `opencode` (v2 reads the same files as v1), but read
  from its own `opencode2/` config dirs instead of the shared `opencode/` dirs.
- **pi** — snippets match `settings*.json*` in the `pi/` subdir; merged to `/home/dev/.pi/agent/settings.json`.
- **claude-code** — snippets match `settings*.json*` in the `claude/` subdir; merged to `/home/dev/.claude/settings.json`.
- **codex** — snippets match `config*.toml` in the `codex/` subdir (e.g. `config-model.toml`); merged to
  `/home/dev/.codex/config.toml`, written as TOML.
- **hermes** — snippets match `config*.yaml` in the `hermes/` subdir; merged to `/home/dev/.hermes/config.yaml`,
  written as YAML.

Matching files are parsed and **deep-merged** into one config document. The user directory is merged first, then the
project directory; within each directory files are merged in alphabetical order, so later files override earlier ones.
Snippet parsing supports JSON, JSONC, JSON5, YAML, and TOML, chosen by file extension (YAML only applies to agents whose
pattern includes YAML extensions, e.g. a pattern like `pi-*.{json,yaml}`). The merged config is written as TOML when the
agent's VM config path ends in `.toml` (codex), as YAML when it ends in `.yaml`/`.yml` (hermes), and as JSON otherwise.

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
the VM without extra configuration. For `opencode2`, read from its own `opencode2/` dirs rather than the shared
`opencode/` dirs.

Two families of files are *not* mirrored:

- Files matching the agent's snippet pattern (`opencode*.json*`, `settings*.json*`, `config*.toml`) are **deep-merged** (see [Config
  snippet merge](#config-snippet-merge)), never mirrored.
- The agent's config-file family (for opencode: `config.json`, `opencode.json`, `opencode.jsonc`, …) is **reserved for
  the merged output** and never mirrored, so a mirror copy cannot shadow the merged snippet config.

Precedence when the same VM path is reachable from multiple sources:

`home:` > merged snippet config > verbatim mirror > drop-in provisioning

> **Note:** File modes — home mappings, the verbatim mirror, and the default drop-in
> copy preserve ordinary Unix permission bits, including executable bits.
> Launcher scripts therefore remain directly executable in the VM.

The mirror is **always active**, independent of `provision-host-config`. Stale mirrored files (deleted from the host)
are left in place in the VM. Run `agents-sandbox config agent` to list the mirror files, each shown as its host source
path → VM path. Previously non-pattern files in `<agent>/` were silently ignored; they are now mirrored verbatim.

## Default drop-in provisioning

Beyond the snippet merge, when running the launcher now **copies the active agent's config + credential files from the
host into the VM by default**, driven by a per-agent gitignore-style include-list manifest (provision rules). This means
your normal agent setup (e.g. an existing opencode config) works without extra configuration.

The drop-in copy is scoped to the agent's settings and, for opencode, its credential file. Runtime state is not copied:

- **opencode** — `~/.config/opencode/**` (excluding `node_modules/`, `package*.json`, and `.gitignore`) plus
  `~/.local/share/opencode/auth.json`.
- **opencode2** — same drop-in copy as `opencode` (`~/.config/opencode/**` and `~/.local/share/opencode/auth.json`).
- **pi** — `~/.pi/agent/settings.json`.
- **claude-code** — `~/.claude/settings.json` (runtime state and the machine-managed `.credentials.json` are not copied).
- **codex** — `~/.codex/config.toml` (sessions, logs, and the `auth.json` login cache are not copied).
- **hermes** — `~/.hermes/config.yaml` (the rest of `~/.hermes` — `state.db`, `sessions/`, `memories/`, `skills/` — is
  runtime state and is not copied; credentials in `~/.hermes/.env` are never copied).

Precedence: the merged snippet config and any `home:` mappings override the drop-in copy for the same VM path.

When snippets exist, the drop-in copy of the config-file family is skipped entirely (see [Config snippet merge](#config-snippet-merge)) so host config
cannot override the merged snippets. Non-config files — e.g. plugins, custom commands, themes — are still copied.

To switch from your agent's own config to `agents-sandbox/<agent>` snippet provisioning, create snippet files in the
user or project snippet directories (see [Config snippet merge](#config-snippet-merge)). Once a snippet matching the
agent's pattern exists, it wins over the host config for the merged config path. To stop the native-config drop-in
entirely so only the snippet merge and `home:` mappings apply, set `provision-host-config: false`.

To turn off the drop-in copy altogether (config **and** credentials), set `provision-host-config: false` in the launcher
config:

```yaml
provision-host-config: false
```

This skips the whole host-config copy (for opencode: `~/.config/opencode/**` and `auth.json`), while the snippet merge
and `home:` mappings keep working. On the next run, previously drop-in-copied config and credential files are removed
from existing home volumes so they cannot linger.

### Authentication: file copy vs. env-secret

> **Warning:** because of the drop-in provisioning, the opencode `auth.json` credential file is now copied
> into the VM by default. If you prefer to deliver credentials exclusively through the env-secret mechanism (which never
> writes them into the VM, see [Secrets]({% link configuration/secrets.md %})), you can opt out of the credential file copy. The env-secret channel
> remains fully supported and unchanged; this does not replace it.

To opt out, exclude `auth.json` from the drop-in copy by placing a `home:` entry that overrides the provisioned path
(see [Home provisioning & startup hooks]({% link configuration/home-provisioning.md %})), or remove the credential file from the host before running. The launcher does not inject
host secrets in any other way; the env-secret mechanism is the supported channel for secrets you do not want on disk in
the VM.

For pi, claude-code, codex, and hermes, the drop-in copy does not include credential files. pi, claude-code, and hermes
authenticate with env secrets instead; codex logs in inside the VM:

- **pi** — per-provider env vars, e.g. `ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `GEMINI_API_KEY` (see pi's docs for the
  full list). Put them in an `env.secret` / `env.secret.yaml` file.
- **claude-code** — `ANTHROPIC_AUTH_TOKEN`, `ANTHROPIC_API_KEY`, or `CLAUDE_CODE_OAUTH_TOKEN`. Claude's
  `.credentials.json` is machine-managed and not hand-provisioned, so env vars are the supported channel here.
- **codex** — the interactive Codex CLI reads its login from `~/.codex/auth.json`, not from an env var, so you log in
  once inside the VM (see [Codex login](#codex-login)). The host `auth.json` is not copied because a ChatGPT login
  rotates its refresh token, and a second copy using it would sign the host out.
- **hermes** — the provider key Hermes is configured to use (`OPENAI_API_KEY`, `ANTHROPIC_API_KEY`,
  `OPENROUTER_API_KEY`, …), supplied through `env.secret`. Hermes reads the process environment, so nothing needs to be
  written into its `.env`.
- **opencode** — `OPENCODE_API_KEY`.

### Codex login

The default `public` network profile already lets Codex reach OpenAI. With `profile: none`, add these hosts to
`network.egress-allow` (see [Networking]({% link configuration/networking.md %})); the network policy applies when the
VM is created, so restart the sandbox afterwards:

```yaml
network:
  egress-allow:
    - auth.openai.com # ChatGPT login
    - chatgpt.com     # ChatGPT backend: workspace routing and the model API for ChatGPT logins
    - api.openai.com  # model API for API-key logins
```

Without `chatgpt.com`, a ChatGPT login completes in the browser but Codex then reports
`workspace routing discovery failed`.

**ChatGPT plan:** choose **Sign in with ChatGPT**. Codex opens the sign-in page in your host browser,
and agents-sandbox passes the browser's return to `http://127.0.0.1:1455/auth/callback` on to Codex in the sandbox (see
[Opening links]({% link configuration/opening-links.md %}#logging-in-to-an-agent)).

If that doesn't work, for example because port 1455 is in use on your computer or `open-links` is off, choose **Sign in
with Device Code** (or run `codex login --device-auth`), open the link on your host, and enter the one-time code. Device
code login is off by default: enable it in your ChatGPT security settings (personal account) or ask a workspace admin.
While it is off, Codex fails with `device code request failed with status 403 Forbidden`.

**API key:** declare `OPENAI_API_KEY` as an [env secret]({% link configuration/secrets.md %}) for `api.openai.com`, then
run `printenv OPENAI_API_KEY | codex login --with-api-key` in `agents-sandbox shell --agent codex`. The VM only sees the
secret's placeholder, so `auth.json` stores the placeholder and the network proxy replaces it on requests to
`api.openai.com`.

The login is stored in `~/.codex/auth.json` on the home volume, so you sign in once per project and agent.

## Example: permissions

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

> **Warning:** these rules are advisory for opencode's own Q&A tools. The `bash` tool executes arbitrary commands inside
> the VM and can read any file regardless of these deny rules, so they are not a security boundary — keep secrets out of
> the VM or rely on the [secret mechanism]({% link configuration/secrets.md %}) instead.
