# agents-sandbox

[![License: GPL-3.0](https://img.shields.io/badge/license-GPL--3.0-blue)](https://github.com/inoio/agents-sandbox/blob/main/LICENSE.md)
[![CI](https://github.com/inoio/agents-sandbox/actions/workflows/ci.yml/badge.svg)](https://github.com/inoio/agents-sandbox/actions/workflows/ci.yml)
[![Go](https://img.shields.io/github/go-mod/go-version/inoio/agents-sandbox)](https://go.dev/)
[![Release](https://img.shields.io/github/v/release/inoio/agents-sandbox)](https://github.com/inoio/agents-sandbox/releases)
[![Coverage](https://codecov.io/gh/inoio/agents-sandbox/branch/main/graph/badge.svg)](https://codecov.io/gh/inoio/agents-sandbox)
[![Security](https://img.shields.io/badge/security-policy-purple.svg)](SECURITY.md)
[![Docs](https://img.shields.io/badge/docs-github--pages-blue)](https://inoio.github.io/agents-sandbox/)

agents-sandbox allows you to isolate your coding agents from the rest of your dev environment. You are put in control over what parts of your host machine and network environment are available to each agent. agents-sandbox acts as a launcher for [opencode](https://opencode.ai), [opencode2](https://opencode.ai),
[pi](https://pi.dev), [Claude Code](https://claude.com/product/claude-code), [Codex](https://github.com/openai/codex), [Hermes Agent](https://hermes-agent.nousresearch.com/), [Oh My Pi](https://github.com/can1357/oh-my-pi), [Kimi Code](https://github.com/MoonshotAI/kimi-code), [Qwen Code](https://github.com/QwenLM/qwen-code), [Goose](https://github.com/aaif-goose/goose), [Grok Build](https://x.ai/cli), and [Mistral Vibe](https://github.com/mistralai/mistral-vibe). It builds or reuses a dedicated [microsandbox](https://microsandbox.dev/) VM per
project and agent, then attaches the selected agent inside that VM. A system-wide microsandbox installation is not
required: agents-sandbox provisions its own isolated runtime under its data directory, never uses `~/.microsandbox` as
its runtime, and never writes to it (the one-time migration reads it only to copy your old volumes).

On top of that, agents-sandbox utilizes the robust secrets management of microsandbox, which significantly reduces the exposure of secrets to the agent. This way, your helpful assistant (or even a rogue agent) won't be able to accidentally publish dev secrets to a public wiki page ;)

At the same time, agents-sandbox moves out of the way as much as possible, so that you don't have to adapt to a completely new workflow.

## Comparison

In short, agents-sandbox:

- runs each agent in its own microVM with a separate kernel, not just a container or process sandbox,
- is open source and needs no account,
- denies network egress by default and keeps secrets on the host,
- runs on Linux (KVM) and Apple Silicon Macs.

The [alternatives page](https://inoio.github.io/agents-sandbox/alternatives.html) compares it with NVIDIA OpenShell, Anthropic sandbox-runtime,
nono, smolvm, Docker Sandboxes, and others, and helps you pick the right tool.

## Boundary and tradeoffs

| Surface | Behavior |
|---------|----------|
| VM kernel and root filesystem  | Separate from the host, using the microsandbox VM boundary.                                                                                                                                                  |
| Agent home                | Stored in a persistent `/home/dev` volume scoped to the project and agent. Recreating the VM does not remove this volume; it is deleted only by an explicit volume reset/removal, `volume prune` (once orphaned), or `prune --purge` (a whole project).                                                       |
| `/workspace`              | The host project directory, mounted read-write in a normal session. Some agents allow you to optionally use a  [worktree-session](https://inoio.github.io/agents-sandbox/branch-sessions.html) for a VM-internal worktree instead. |
| Other host files          | Not visible unless they are copied, provisioned with `home:`, or exposed through an explicit host mount.                                                                                                     |
| Network                   | Egress uses the `public` profile by default; the composable `private` and `host` profiles extend it, and the allowlist-only `none` profile replaces it, with allow/deny rules refining either. `none` is deny-by-default egress, not a complete air gap.                                                              |
| Links                     | A link the agent opens in the VM is handed to the attached launcher through the home volume and opened in the host's browser, if its scheme is allowlisted (`http`, `https`, `mailto`, editor/app links; never `file:`). While a browser login is open, the launcher listens on its callback port on the host's loopback and passes the callback into the VM. Turn it off with `open-links: false`; see [Opening links](https://inoio.github.io/agents-sandbox/configuration/opening-links.html). |
| Project config            | `.agents-sandbox/` sits in the checkout the agent can edit. A run uses it only after you approve its content for the checkout, and asks again whenever it changes; see [Approving project config changes](https://inoio.github.io/agents-sandbox/configuration/launcher.html#approving-project-config-changes). |
| Raw credentials           | `env.secret` and `env.secret.yaml` keep the real value on the host and expose a placeholder to the guest. This is separate from ordinary file provisioning. |

> **Credential warning:** Host agent configuration is not copied into the VM by default. Opt in with
> `provision-host-config: true` to copy the active agent's host configuration; for
> opencode, that can include `~/.local/share/opencode/auth.json`. Because the default is off, previously drop-in-copied
> host config and credentials are removed from existing sandboxes on the next run; opt in again to keep the drop-in
> copy. If credentials must not be stored in the VM, leave
> provisioning off and use the secret mechanism, and keep credentials out of `/workspace`, `home:`, `env`, and
> writable mounts. See the [Secrets](https://inoio.github.io/agents-sandbox/configuration/secrets.html) and [Agent configuration](https://inoio.github.io/agents-sandbox/configuration/agent.html)
> documentation.

## Why this model?

The useful distinction is not that an agent becomes safe. It is that the agent process no longer runs in the host's kernel or
root filesystem. A VM boundary limits what the agent can see and modify to the guest and to the host paths you deliberately
share. That is a stronger boundary than running the agent directly on the host or relying only on a shared-kernel container,
while preserving the practical workflow of editing the current checkout.

Use agents-sandbox when you already use a supported coding agent and want:

- a separate VM kernel and root filesystem around agent execution;
- direct read-write access to the current project, or an isolated VM-internal worktree;
- persistent agent state without exposing your whole host home directory; and
- a runner image and network policy that you can configure for the project.

## Quick start

Install agents-sandbox using the [Get started guide](https://inoio.github.io/agents-sandbox/get-started.html), then
start the configured agent (OpenCode 2 by default):

```console
agents-sandbox
```

On a first run, agents-sandbox asks how to set the sandbox up — learn by using it, learn from your host setup (when
your host has agent config), or configure it myself (see
[Letting agents-sandbox learn your setup](https://inoio.github.io/agents-sandbox/learn-your-setup.html)).
Learn mode can also move an API key saved by Codex or Claude Code's login into a host secret after you accept the proposal.
For a self-contained setup with explicit secret handling, start with
[Manage config in the sandbox](https://inoio.github.io/agents-sandbox/manage-config.html) instead.

## Agent selection

You can use the `--agent <name>` flag (available on `run`, `shell`, `build`, `volume`, `stop`, and `kill`) to
select the coding-agent to run, provision, or manage. This is also available as a setting in the [configuration file](https://inoio.github.io/agents-sandbox/configuration/launcher.html).
`--agent-version` pins the version agents-sandbox installs into the runner image; it does not replace an agent already
provided by a custom base or project Dockerfile.

Twelve agents ship as built-in profiles:

- **`opencode`** — a daemon-based agent with serve/attach, worktree sessions, and GitHub-release upgrade checks.
- **`opencode2`** (default) — opencode 2 (beta), installed from its native installer; daemon-based with serve/attach, worktree
  sessions, and upgrade checks via the opencode update API's `latest` channel. It owns its own launcher config dir (`~/.config/agents-sandbox/opencode2/`,
  project equivalent `.agents-sandbox/opencode2/`), while its VM config path and host drop-in stay `~/.config/opencode/`.
  While no `opencode2/` dir exists it falls back to the shared `opencode/` dir with a warning (see the agent docs).
- **`pi`** — the pi coding agent (`@earendil-works/pi-coding-agent`, an npm-installed node bundle — the only agent that
  needs node), run interactively, with upgrade checks via `pi.dev`.
- **`claude-code`** — Anthropic's Claude Code, installed from its native installer, run interactively, with upgrade
  checks via the npm registry.
- **`codex`** — OpenAI's Codex CLI, installed from its GitHub release package, run interactively, with upgrade checks via
  GitHub releases.
- **`hermes`** — [Hermes Agent](https://hermes-agent.nousresearch.com/), installed from its official installer at a
  pinned release tag, run interactively, with upgrade checks via its GitHub releases.
- **`oh-my-pi`** — [Oh My Pi](https://github.com/can1357/oh-my-pi) (binary `omp`), installed from its checksum-verified
  GitHub release binary (about 275 MB), run interactively, with upgrade checks via its GitHub releases.
- **`kimi-code`** — [Kimi Code CLI](https://github.com/MoonshotAI/kimi-code) (binary `kimi`), installed from its
  checksum-verified GitHub release binary (about 194 MB), run interactively, with upgrade checks via the npm registry.
- **`qwen-code`** — [Qwen Code](https://github.com/QwenLM/qwen-code) (binary `qwen`), installed from its checksum-verified
  standalone GitHub release, which bundles its own Node (about 326 MB unpacked), run interactively, with upgrade checks
  via the npm registry.
- **`goose`** — [Goose](https://github.com/aaif-goose/goose), installed from its static GitHub release binary (about 159 MB), run
  interactively, with upgrade checks via its GitHub releases.
- **`grok-build`** — xAI's [Grok Build](https://x.ai/cli) (binary `grok`), installed from its static release binary (about 208 MB),
  run interactively, with upgrade checks via the npm registry.
- **`mistral-vibe`** — Mistral's [Vibe](https://github.com/mistralai/mistral-vibe) (binary `vibe`), installed from PyPI with a
  checksum-verified uv into its own managed Python (about 585 MB), run interactively, with upgrade checks via PyPI.

`opencode`, `opencode2`, and `pi` are the tested agents; the other nine are experimental — not well tested with
agents-sandbox.

## Documentation

There's dedicated documentation per topic. You can also browse the documentation on [GitHub Pages](https://inoio.github.io/agents-sandbox/).

| Topic                                         | Description                                                                              |
|-----------------------------------------------|------------------------------------------------------------------------------------------|
| [Why?](https://inoio.github.io/agents-sandbox/introduction.html)                 | Motivation, isolation boundary, shared data, and limitations.                            |
| [Alternatives](https://inoio.github.io/agents-sandbox/alternatives.html)         | Comparison of agents-sandbox with other local agent sandboxes and runtimes.              |
| [How it works](https://inoio.github.io/agents-sandbox/how-it-works.html)         | Architecture: host to VM, `/workspace`, home volume, secrets, and multi-client attach.   |
| [Get started](https://inoio.github.io/agents-sandbox/get-started.html)         | Installation, system requirements, and first run.                                        |
| [Switch from your existing agent](https://inoio.github.io/agents-sandbox/switch.html) | Use agents-sandbox with your existing agent config and credentials (host-config drop-in). |
| [Manage config in the sandbox](https://inoio.github.io/agents-sandbox/manage-config.html) | Declarative, self-contained config: secrets, provisioning, agent snippets.            |
| [Letting agents-sandbox learn your setup](https://inoio.github.io/agents-sandbox/learn-your-setup.html) | Learn the config a session needed, then accept the proposal.            |
| [Commands](https://inoio.github.io/agents-sandbox/commands.html)                 | Complete CLI reference                                                                   |
| [Configuration](https://inoio.github.io/agents-sandbox/configuration/)         | Split into subpages: Configuration files & Environment variables, secrets, networking, host mounts, home provisioning & startup hooks, agent configuration, notifications, self-upgrade |
| [Runner Image](https://inoio.github.io/agents-sandbox/runner-image.html)         | Base image, custom tooling                                                               |
| [Worktree Sessions](https://inoio.github.io/agents-sandbox/branch-sessions.html) | Isolated worktree sessions for per-feature development                                   |
| [Recipes](https://inoio.github.io/agents-sandbox/recipes.html)                   | Hands-on workflows                                  |
| [Sandboxes](https://inoio.github.io/agents-sandbox/sandboxes.html)               | VM lifecycle, volumes, pruning                                                           |
| [Troubleshooting](https://inoio.github.io/agents-sandbox/troubleshooting.html)   | Common issues and fixes                                                                  |
| [Roadmap](ROADMAP.md)                        | Public, forward-looking project roadmap                                                  |

## Contributing  

| Topic                                  | Description                                     |
|----------------------------------------|-------------------------------------------------|
| [Contributing](CONTRIBUTING.md)       | Guidelines for contributing to agents-sandbox |
| [Code of conduct](CODE_OF_CONDUCT.md) | Our code of conduct                             |
| [Security](SECURITY.md)               | Rules for submitting security issues            |
| [Roadmap](ROADMAP.md)                 | Public, forward-looking project roadmap         |
