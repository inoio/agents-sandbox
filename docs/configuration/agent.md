---
title: Agent configuration
description: "Select and configure the coding agent run by agents-sandbox: opencode, opencode2, pi, Claude Code, Codex, Hermes or Oh My Pi."
layout: default
parent: Configuration
nav_order: 60
---

# Agent configuration

agents-sandbox is agent-aware. A `--agent <name>` flag on `run`, `shell`, `build`, `volume`, `stop`, and `kill`
selects the coding-agent profile to run, build, provision, or manage. The agent can also be selected via the `agent`
config key or the `AGENTS_SANDBOX_AGENT` environment variable. Twelve agents ship as built-in profiles:

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
- **`oh-my-pi`** — [Oh My Pi](https://github.com/can1357/oh-my-pi) (binary `omp`), installed from its standalone GitHub
  release binary after its checksum is verified, and run interactively; upgrade checks via its GitHub releases. Its
  startup update check is turned off through a launcher-owned `--config` overlay, because agents-sandbox offers upgrades
  itself. The binary is about 275 MB, so its runner image is larger than the others.
- **`kimi-code`** — [Kimi Code CLI](https://github.com/MoonshotAI/kimi-code) (binary `kimi`), installed from its
  standalone GitHub release binary after its checksum is verified, and run interactively; upgrade checks via the npm
  registry. Its update check is turned off (`KIMI_CODE_NO_AUTO_UPDATE=1`), because agents-sandbox offers upgrades
  itself. The binary is about 194 MB.
- **`qwen-code`** — [Qwen Code](https://github.com/QwenLM/qwen-code) (binary `qwen`), installed from its standalone
  GitHub release (which bundles its own Node) after its checksum is verified, and run interactively; upgrade checks via
  the npm registry. Its update check is turned off through a system settings file baked into the image
  (`QWEN_CODE_SYSTEM_SETTINGS_PATH` points at it under `/opt/agents-sandbox`), because agents-sandbox offers
  upgrades itself. The release unpacks to about 326 MB.
- **`goose`** — [Goose](https://github.com/aaif-goose/goose), installed from the static (musl) binary of its GitHub release and run
  interactively; upgrade checks via its GitHub releases, taking the newest one whose Linux archives are
  published. Goose publishes no checksums for its release assets, so the
  binary is fetched over HTTPS from the pinned release tag without a checksum check. Goose checks for updates only when
  you run `goose update`. The binary is about 159 MB.
- **`grok-build`** — xAI's [Grok Build](https://x.ai/cli) (binary `grok`), installed from the static release binary xAI hosts (with
  the storage bucket xAI's installer uses as a fallback) and run interactively; upgrade checks via the npm registry.
  xAI publishes no checksums for the binaries it hosts, so it is fetched over HTTPS by its pinned version without a checksum
  check. Its auto-updater is turned off (`GROK_DISABLE_AUTOUPDATER=1`, and `auto_update = false` pinned in
  `/etc/grok/requirements.toml`), because agents-sandbox offers upgrades itself. The binary is about 208 MB.
- **`mistral-vibe`** — Mistral's [Vibe](https://github.com/mistralai/mistral-vibe) (binary `vibe`), installed from PyPI and run
  interactively; upgrade checks via PyPI. The image fetches a pinned, checksum-verified uv, which installs a managed
  CPython 3.14 and the pinned `mistral-vibe` release under `/opt/agents-sandbox/mistral-vibe`, with its Python
  dependencies held to the versions in that release's `uv.lock`; uv itself is not shipped. Its update check is turned
  off (`VIBE_ENABLE_UPDATE_CHECKS=false`, which outranks the user's `config.toml`), because agents-sandbox offers
  upgrades itself, and it uses the system trust store (`VIBE_ENABLE_SYSTEM_TRUST_STORE=true`), which carries the
  sandbox's TLS-interception CA, so secret substitution works. The install is about 585 MB.

> **Experimental agents:** `opencode` and `opencode2` are the tested default agents. Every other built-in
> profile (`pi`, `claude-code`, `codex`, `hermes`, `oh-my-pi`, `kimi-code`, `qwen-code`, `goose`, `grok-build`, `mistral-vibe`) is **experimental** — not well tested with agents-sandbox.
> The launcher prints a notice when an experimental agent is selected.

`--worktree` and `--serve-only` are rejected for pi, claude-code, codex, hermes, oh-my-pi, kimi-code, qwen-code, goose, grok-build, and mistral-vibe (they have no daemon); they run through the
interactive TUI instead. Passing an unsupported `--agent` name reports the valid names in its error message.

> **Note:** opencode2 server auth — v2's `serve` requires HTTP basic auth on every `/api/*` route and would otherwise generate a
> random password the launcher cannot know. The runner image therefore bakes a fixed `OPENCODE_SERVER_PASSWORD`, and the
> health probe, worktree API calls, and in-VM attach client all use it. v2 hardcodes the basic-auth username to
> `opencode` (there is no `OPENCODE_SERVER_USERNAME`), so the fixed credential is `opencode:opencode` — what an
> external `--serve-only` client must present on the `/api/*` routes (see the [connect-desktop recipe]({% link recipes/connect-desktop.md %})). The
> root path and the static web UI are served without credentials; the `/api/*` routes and the `/openapi.json` document
> require the basic-auth credential.

Each agent owns its config directories, one subdir per agent under the tool's config base, in precedence order:

- **Generic user:** `~/.config/agents-sandbox/<agent>/` (e.g. `~/.config/agents-sandbox/opencode/`, `~/.config/agents-sandbox/opencode2/`)
- **Per-slug user:** `~/.config/agents-sandbox/<slug>/<agent>/` (repo slug, shared by every clone; e.g. `~/.config/agents-sandbox/myproject-<hash>/opencode2/`)
- **Project:** `.agents-sandbox/<agent>/` (e.g. `.agents-sandbox/opencode/`, `.agents-sandbox/opencode2/`)

While no dir exists in any scope (and no legacy fallback applies), every `run` and `shell` prints an info line naming the
paths, so a fresh install shows where launcher config goes. Creating one silences it.

> **Note:** Legacy fallback, migrating from an older version — the `opencode2` launcher config dir is new. While **no**
> `opencode2` dir exists in **any** scope, `opencode2` reads the old shared `opencode` dir — which the `opencode`
> agent also uses — and warns on every run while it falls back. To migrate, **copy** the files you want `opencode2` to
> read from `~/.config/agents-sandbox/opencode/` into `~/.config/agents-sandbox/opencode2/` (and the per-slug user and
> project equivalents); copy, not move, if you also use `opencode`. The three scopes switch together: as soon as an
> `opencode2` dir exists in **any** of the generic user, per-slug user, or project scope, **all three** scopes read
> their `opencode2/` dir and the warning stops — so copy **all** the files in one go, because a partial `opencode2/`
> dir disables the fallback for the other scopes too and silently ignores the leftovers. One consequence: a per-slug
> `opencode2/` dir suppresses a generic `opencode/` fallback, because the single namespace choice spans every scope.
> The guest `~/.config/opencode/` path is never affected.

VMs, home volumes, and state are also scoped per agent: the sandbox name, home volume, and state file all carry the agent, so
switching agents no longer tears down the project sandbox and multiple agents can serve the same project concurrently
(see [Sandboxes]({% link sandboxes.md %}#identity-one-sandbox-per-checkout-and-agent)).

## Config snippet merge

agents-sandbox provisions a single agent config into the VM. No embedded provider or permission config is shipped.
Instead, the agent config is assembled from **snippet files** that match the agent's snippet pattern, collected from the
agent config directories (generic user, per-slug user, project), and written to the agent's VM config path:

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
- **oh-my-pi** — snippets match `config*.{yml,yaml}` in the `oh-my-pi/` subdir (e.g. `config-model.yml`); merged to
  `/home/dev/.omp/agent/config.yml`, written as YAML. omp falls back to `config.yaml`, so both names are reserved for
  the merged output.
- **kimi-code** — snippets match `config*.toml` in the `kimi-code/` subdir (e.g. `config-provider.toml`); merged to
  `/home/dev/.kimi-code/config.toml`, written as TOML.
- **qwen-code** — snippets match `settings*.json*` in the `qwen-code/` subdir (e.g. `settings-model.json`); merged to
  `/home/dev/.qwen/settings.json`.
- **goose** — snippets match `config*.{yml,yaml}` in the `goose/` subdir (e.g. `config-provider.yaml`); merged to
  `/home/dev/.config/goose/config.yaml`, written as YAML.
- **grok-build** — snippets match `config*.toml` in the `grok-build/` subdir (e.g. `config-ui.toml`); merged to
  `/home/dev/.grok/config.toml`, written as TOML.
- **mistral-vibe** — snippets match `config*.toml` in the `mistral-vibe/` subdir (e.g. `config-model.toml`); merged to
  `/home/dev/.vibe/config.toml`, written as TOML.

Matching files are parsed and **deep-merged** into one config document. The generic user directory is merged first, then
the per-slug user directory, then the project directory; within each directory files are merged in alphabetical order,
so later files override earlier ones.
Snippet parsing supports JSON, JSONC, JSON5, YAML, and TOML, chosen by file extension (YAML only applies to agents whose
pattern includes YAML extensions, e.g. a pattern like `pi-*.{json,yaml}`). The merged config is written as TOML when the
agent's VM config path ends in `.toml` (codex, kimi-code, grok-build, mistral-vibe), as YAML when it ends in `.yaml`/`.yml` (hermes, oh-my-pi, goose), and as JSON otherwise.

If no snippet files exist, no merged config is produced.

A snippet that matches the pattern but cannot be read or parsed is skipped, not fatal: at startup the
launcher names it in a warning and leaves it out of the merge, while `agents-sandbox config agent` reports
it as an error. When every matching snippet fails to read or parse, `agents-sandbox config agent` reports
that no snippet could be merged.

Run `agents-sandbox config agent` to print the merged config that would be provisioned into the VM.

> **Note:** the merged config is written to the agent's VM config path (for opencode, `opencode.jsonc`, the last file
> opencode loads: `config.json` < `opencode.json` < `opencode.jsonc`), so it always wins the deep merge. When snippets
> exist, the config-file family (for opencode: `config.json`, `opencode.json`, `opencode.jsonc`, …) is removed from the
> VM so a host drop-in copy of any of those files cannot shadow the merged snippet config.

See the [permissions example](#example-permissions) for a concrete snippet.

## Verbatim config directory mirror

Beyond the snippet merge, the `<agent>` config directories (`~/.config/agents-sandbox/<agent>/`,
`~/.config/agents-sandbox/<slug>/<agent>/`, and `.agents-sandbox/<agent>/`) act as a **1:1 verbatim mirror** of the
agent's VM config directory. Every file and
subdirectory in them is copied verbatim into the VM, preserving its relative path (for opencode: `tui.json`, `AGENTS.md`,
`agents/`, `commands/`, `themes/`, `plugins/`, `skills/`, `tools/`, …). This makes your whole agent setup available in
the VM without extra configuration. For `opencode2`, read from its own `opencode2/` dirs rather than the shared
`opencode/` dirs.

Two families of files are *not* mirrored:

- Files matching the agent's snippet pattern (`opencode*.json*`, `settings*.json*`, `config*.toml`, …) are **deep-merged** (see [Config
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
  `~/.local/share/opencode/auth.json`. `node_modules/` is not copied because packages built on your machine do not
  run reliably in the Linux VM. When `~/.config/opencode/package.json` lists `dependencies`, `agents-sandbox run`
  warns that plugins importing them will not load; install them inside the VM as described in
  [Install Opencode Plugin Dependencies]({% link recipes/opencode-plugin-dependencies.md %}).
- **opencode2** — same drop-in copy as `opencode` (`~/.config/opencode/**` and `~/.local/share/opencode/auth.json`).
  `opencode2` reads credentials from its own database and ignores the copied `auth.json` in a new sandbox; see
  [GitHub Copilot with opencode2]({% link configuration/secrets.md %}#github-copilot-with-opencode2).
- **pi** — `~/.pi/agent/settings.json`.
- **claude-code** — `~/.claude/settings.json` (runtime state and the machine-managed `.credentials.json` are not copied).
- **codex** — `~/.codex/config.toml` (sessions, logs, and the `auth.json` login cache are not copied).
- **hermes** — `~/.hermes/config.yaml` (the rest of `~/.hermes` — `state.db`, `sessions/`, `memories/`, `skills/` — is
  runtime state and is not copied; credentials in `~/.hermes/.env` are never copied).
- **oh-my-pi** — `~/.omp/agent/config.yml` or `config.yaml` (the rest of `~/.omp` — sessions, `agent.db`, the
  extracted native addon — is runtime state and is not copied; `models.yml` and `~/.omp/agent/.env` can hold literal
  provider keys and are never copied).
- **kimi-code** — `~/.kimi-code/tui.toml` and `~/.kimi-code/AGENTS.md` only. `config.toml` is not copied, because it
  keeps literal provider keys (`api_key`, `[providers.<id>.env]`) beside its settings; nor are `mcp.json` (MCP
  headers and env can hold tokens), the `credentials/` logins, or runtime state. Put Kimi settings in `config*.toml`
  snippets instead (see [Config snippet merge](#config-snippet-merge)).
- **qwen-code** — from `~/.qwen`: `QWEN.md`, `AGENTS.md`, and the `commands/`, `skills/`, `agents/`, and `workflows/`
  directories. `settings.json` is not copied, because `/auth` saves API keys into its `env` section beside the
  settings; nor are `.env` (environment variables, typically keys), the `oauth_creds.json` and `mcp-oauth-tokens.json`
  logins, `extensions/` (each extension keeps its settings, including secret ones, in a `.env`), or runtime state. Put Qwen settings in `settings*.json` snippets instead (see
  [Config snippet merge](#config-snippet-merge)).
- **goose** — from `~/.config/goose`: `permission.yaml`, the global context files `.goosehints` and `AGENTS.md`, and the
  `prompts/` and `recipes/` directories. `config.yaml` is not copied, because an extension's `envs` and `headers` can
  hold tokens beside its settings; nor are the `custom_providers/` definitions (an inline `api_key` and headers),
  `secrets.yaml` (where goose keeps secrets as plain text when there is no system keyring), `history.txt`, or runtime
  state. Put Goose settings in `config*.yaml` snippets instead (see [Config snippet merge](#config-snippet-merge)).
- **grok-build** — the global rules files in `~/.grok/` (`AGENTS.md`, `Agents.md`, `AGENT.md`, `CLAUDE.md`,
  `Claude.md`), `~/.grok/skills/`, `~/.grok/sandbox.toml`, and `~/.grok/workflows/` only. `config.toml` is not copied,
  because a model's `api_key` and MCP server headers can sit beside its settings; nor are the `auth.json` login,
  `hooks/` (they run host commands) and `plugins/` (they can bundle MCP configs), the cloud-synced `requirements.toml`
  and `managed_config.toml`, the trust stores, or runtime state. Put Grok settings in `config*.toml`
  snippets instead (see [Config snippet merge](#config-snippet-merge)).
- **mistral-vibe** — from `~/.vibe`: `AGENTS.md` and the `agents/`, `prompts/`, and `skills/` directories. `config.toml`
  is not copied, because MCP server and provider headers and an MCP server's `env` can hold tokens beside its settings;
  nor are `.env` (where Vibe stores an API key), `hooks.toml` (hooks run host commands), the trust store, or runtime
  state. Put Vibe settings in `config*.toml` snippets instead (see [Config snippet merge](#config-snippet-merge)).

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

For pi, claude-code, codex, hermes, oh-my-pi, kimi-code, qwen-code, goose, grok-build, and mistral-vibe, the drop-in copy does
not include credential files. pi, claude-code, hermes, oh-my-pi, and goose authenticate with env secrets instead;
codex logs in inside the VM, and kimi-code, qwen-code, grok-build, and mistral-vibe do either:

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
- **oh-my-pi** — per-provider env vars, e.g. `ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `GEMINI_API_KEY`, supplied through
  `env.secret`. omp reads the process environment, so nothing needs to be written into `~/.omp/agent/.env`.
- **kimi-code** — Kimi does not read a provider key from the process environment on its own. Name the variable in a
  provider snippet with `api_key_env`, and supply it through `env.secret`; or run `/login` once inside the VM, whose
  login persists on the home volume (see [Kimi Code login](#kimi-code-login)).
- **qwen-code** — the provider's key variable (`DASHSCOPE_API_KEY`, `OPENAI_API_KEY`, …), supplied through
  `env.secret`, with the provider in a `settings*.json` snippet; or `/auth` once inside the VM (see
  [Qwen Code login](#qwen-code-login)).
- **goose** — the provider's key variable (`ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, …), supplied through `env.secret`,
  with the provider and model in a `config*.yaml` snippet (e.g. `GOOSE_PROVIDER: anthropic` and `GOOSE_MODEL: …`). Goose
  reads a secret from the process environment before its secret store, so nothing needs to be written into
  `secrets.yaml`. Keep goose's settings in the snippet, including `GOOSE_TELEMETRY_ENABLED: false` (or `true`):
  agents-sandbox owns `config.yaml` in the VM — with a snippet it is replaced by the merged document, and with none it
  is removed — so whatever `goose configure` or the first-run telemetry prompt writes there does not survive the next
  run. A key that `goose configure` stores in `secrets.yaml` on the home volume does persist, but the `env.secret`
  route keeps it out of the VM.
- **grok-build** — `XAI_API_KEY`, supplied through `env.secret` for `api.x.ai`; Grok uses it when no login is active.
  Or sign in once inside the VM with `grok login --device-auth` (or `/login` in the TUI) and finish the sign-in on your
  host; the login is stored in `~/.grok/auth.json` on the home volume and never copied from the host.
- **mistral-vibe** — `MISTRAL_API_KEY`, supplied through `env.secret` for `api.mistral.ai`; Vibe reads it from the
  process environment first. Or run `/login` once inside the VM and enter a key, which Vibe saves to `~/.vibe/.env` on
  the home volume as plain text. The Mistral-account sign-in keeps its tokens only in an OS keyring, which the VM does
  not have, so it is not available in the sandbox.
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

### Kimi Code login

**API key:** declare the key as an [env secret]({% link configuration/secrets.md %}) for the provider's API host, and
point a provider snippet at it with `api_key_env`. Kimi reads the variable on every request, and the VM only sees its
placeholder, which the network proxy replaces on requests to that host:

```yaml
# ~/.config/agents-sandbox/env.secret.yaml
MOONSHOT_API_KEY:
  value: sk-xxxxxxxx
  host: api.moonshot.ai
```

```toml
# ~/.config/agents-sandbox/kimi-code/config-provider.toml
default_model = "moonshot/kimi-k2"

[providers.moonshot]
type = "kimi"
base_url = "https://api.moonshot.ai/v1"
api_key_env = "MOONSHOT_API_KEY"

[models."moonshot/kimi-k2"]
provider = "moonshot"
model = "kimi-k2"
max_context_size = 262144
```

**Kimi Code plan:** run `/login` in the TUI (or `kimi login` in `agents-sandbox shell --agent kimi-code`) and finish the
device-code sign-in on your host. The login is stored in `~/.kimi-code/credentials/` on the home volume, so you sign in
once per project and agent; it is never copied from the host.

### Qwen Code login

**API key:** declare the key as an [env secret]({% link configuration/secrets.md %}) for the provider's API host, and
name it as the model's `envKey` in a settings snippet. Qwen reads the key from the process environment, which outranks the `env`
section of `settings.json`, so the VM only sees the placeholder, which the network proxy replaces on requests to that
host. Do not put the key into a snippet's `env` section: snippets are written into the VM as plain files.

```yaml
# ~/.config/agents-sandbox/env.secret.yaml
DASHSCOPE_API_KEY:
  value: sk-xxxxxxxx
  host: dashscope.aliyuncs.com
```

```json
// ~/.config/agents-sandbox/qwen-code/settings-provider.json
{
  "modelProviders": {
    "openai": [
      {
        "id": "qwen3-coder-plus",
        "name": "qwen3-coder-plus",
        "baseUrl": "https://dashscope.aliyuncs.com/compatible-mode/v1",
        "envKey": "DASHSCOPE_API_KEY"
      }
    ]
  },
  "security": { "auth": { "selectedType": "openai" } },
  "model": { "name": "qwen3-coder-plus" }
}
```

**In the VM:** run `/auth` in the TUI and pick a provider. A Qwen OAuth sign-in is saved into
`~/.qwen/oauth_creds.json` on the home volume, so you sign in once per project and agent; it is never copied from the
host. Qwen OAuth signs in through a browser. An API key entered through `/auth` is written to
`~/.qwen/settings.json`, but agents-sandbox owns that file through the `settings*.json` snippet merge — with a snippet
it is replaced by the merged document, and with none it is removed — so an API key entered there does not survive the
next run. Use the `envKey` + `env.secret` snippet instead for an API key that persists.

## Example: permissions

Opencode permissions are configured through opencode config snippets, which agents-sandbox merges (user-first, then
project). The snippet file name must match the agent's snippet pattern `opencode*.json*`; a file such as
`permission.json5` is not merged. Both examples in this section deny reading `.env` and `.envrc` files.

**opencode** (v1) uses the `permission` object, keyed by tool, in `.agents-sandbox/opencode/`. This example allows
everything else:

```json5
{
  // .agents-sandbox/opencode/opencode-permissions.json5
  permission: {
    "*": "allow",
    read: {
      "*": "allow",
      "*.env": "deny",
      "*.envrc": "deny",
    },
  },
}
```

**opencode2** (v2) uses the `permissions` rule list, in `.agents-sandbox/opencode2/`. Its default policy already allows
every action and only asks before reading `.env` files; your rules are appended after the defaults, and the last
matching rule wins. This example also allows directories outside the project and web search without asking, and lets
opencode pick a web search provider instead of prompting for one:

```json5
{
  // .agents-sandbox/opencode2/opencode-permissions.json5
  permissions: [
    { action: "external_directory", resource: "*", effect: "allow" },
    { action: "websearch", resource: "*", effect: "allow" },
    { action: "read", resource: "*.env", effect: "deny" },
    { action: "read", resource: "*.envrc", effect: "deny" },
  ],
  websearch: { provider: "random" },
}
```

opencode2 still reads the v1 `permission` object, but marks it deprecated. See the
[opencode v1 migration guide](https://opencode.ai/docs/migrate-v1/#permissions-and-tools).

> **Warning:** these rules are advisory for opencode's own Q&A tools. The `bash` tool executes arbitrary commands inside
> the VM and can read any file regardless of these deny rules, so they are not a security boundary — keep secrets out of
> the VM or rely on the [secret mechanism]({% link configuration/secrets.md %}) instead.

## Capability support matrix

Every agent attaches interactively in the VM and supports config snippet merge, host-config drop-in, and
version/upgrade checks. Beyond that base, coverage differs by agent:

- ✅ **Supported** — agents-sandbox implements it for this agent.
- ◐ **Supportable** — the agent exposes a surface agents-sandbox could use, but does not implement yet.
- ✖ **Unsupported** — the agent has no such surface.

| Capability | `opencode` | `opencode2` | `pi` | `claude-code` | `codex` | `hermes` | `oh-my-pi` | `kimi-code` | `qwen-code` | `goose` | `grok-build` | `mistral-vibe` |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| Interactive attach | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ |
| Config snippet merge | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ |
| Host-config drop-in | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ |
| Version + upgrade check | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ |
| Serve/attach daemon | ✅ | ✅ | ✖ | ✖ | ◐ | ◐ | ◐ | ◐ | ◐ | ◐ | ◐ | ◐ |
| Worktree sessions | ✅ | ✅ | ✖ | ✖ | ✖ | ✖ | ◐ | ✖ | ◐ | ✖ | ◐ | ◐ |
| Notify event stream | ✅ | ✅ | ✖ | ✖ | ◐ | ◐ | ◐ | ◐ | ◐ | ◐ | ◐ | ◐ |
| Session quiescence | ✅ | ✖ | ✖ | ✖ | ✖ | ✖ | ✖ | ✖ | ✖ | ✖ | ✖ | ✖ |

The ◐ entries name an upstream surface agents-sandbox has not wired up: `codex app-server` (a JSON-RPC daemon with a
turn/item event stream) for Codex, `hermes serve` / `hermes chat --format stream-json` for Hermes, and `omp --mode rpc` /
`omp acp` (stdio servers) and the `omp worktree` CLI for Oh My Pi, and `kimi web` (a REST and WebSocket server, marked
experimental upstream) for Kimi Code, `qwen serve` (HTTP with a server-sent event stream, marked experimental
upstream) and its managed session worktrees for Qwen Code, `goose serve` (HTTP and WebSocket with a `/status`
endpoint) for Goose, `grok agent stdio` (an ACP stdio server) and `grok --worktree` (sessions in git worktrees
under `~/.grok/worktrees/`) for Grok Build, and `vibe run-server` (headless runs over JSON-RPC on stdin and stdout,
hidden and marked experimental upstream), the headless `--output streaming` event stream, and `vibe --worktree`
(sessions in git worktrees under `~/.vibe/worktrees/`) for Mistral Vibe.

