# agents-sandbox

[![License: GPL-3.0](https://img.shields.io/badge/license-GPL--3.0-blue)](https://github.com/inoio/agents-sandbox/blob/main/LICENSE.md)
[![CI](https://github.com/inoio/agents-sandbox/actions/workflows/ci.yml/badge.svg)](https://github.com/inoio/agents-sandbox/actions/workflows/ci.yml)
[![Go](https://img.shields.io/github/go-mod/go-version/inoio/agents-sandbox)](https://go.dev/)
[![Release](https://img.shields.io/github/v/release/inoio/agents-sandbox)](https://github.com/inoio/agents-sandbox/releases)
[![Coverage](https://codecov.io/gh/inoio/agents-sandbox/branch/main/graph/badge.svg)](https://codecov.io/gh/inoio/agents-sandbox)
[![Security](https://img.shields.io/badge/security-policy-purple.svg)](SECURITY.md)
[![Docs](https://img.shields.io/badge/docs-github--pages-blue)](https://inoio.github.io/agents-sandbox/)

agents-sandbox allows you to isolate your coding agents from the rest of your dev environment. You are put in control over what parts of your host machine and network environment are available to each agent. agents-sandbox acts as a launcher for [opencode](https://opencode.ai), [opencode2](https://opencode.ai),
[pi](https://pi.dev), and [Claude Code](https://claude.com/product/claude-code). It builds or reuses a dedicated [microsandbox](https://microsandbox.dev/) VM per
project and agent, then attaches the selected agent inside that VM.

On top of that, agents-sandbox utilizes the robust secrets management of microsandbox, which significantly reduces the exposure of secrets to the agent. This way, your helpful assistant (or even a rogue agent) won't be able to accidentally publish dev secrets to a public wiki page ;)

At the same time, agents-sandbox moves out of the way as much as possible, so that you don't have to adapt to a completely new workflow.

## Comparison

The table compares local execution models, not hosted services. Bubblewrap/Seatbelt and Docker are policy-dependent building
blocks, so the entries summarize their usual mechanisms rather than assigning a security score.

| Category | Direct host execution | Bubblewrap / Seatbelt                                       | Docker containers                                           | Docker Sandboxes                                                   | agents-sandbox                                                   |
|---|---|-------------------------------------------------------------|-------------------------------------------------------------|--------------------------------------------------------------------|------------------------------------------------------------------|
| **Isolation boundary** | ❌ No boundary; host kernel and user permissions. | ⚠️ Shared host kernel; policy-defined OS sandbox.           | ⚠️ Shared host kernel; policy-defined OS sandbox.           | ✅ Separate microVM and Linux kernel.                               | ✅ Separate microVM and Linux kernel.                             |
| **Secrets handling** | ❌ Host environment, files, and agent credentials are available. | ⚠️ No host-only broker; supplied values are visible.        | ⚠️ No host-only broker; supplied values are visible.        | ✅ Host proxy injects credentials; VM sees a sentinel.              | ✅ Host proxy injects credentials; VM sees a sentinel.   |
| **Filesystem isolation** | ❌ Host user files are accessible. | ⚠️ Private sandbox view; configured host mounts are shared. | ⚠️ Private sandbox view; configured host mounts are shared. | ✅ Private VM; workspace sharing depends on direct/clone/mountless mode. | ✅ Private VM; only workspace directory is read-write by default; |
| **Network isolation** | ❌ Host network stack. | ⚠️ Manual firewalling.                                      | ⚠️ Manual firewalling.                                      | ✅ Policy-defined network isolation.                                                         | ✅ Policy-defined network isolation.                    |
| **Platform/OS Support** | Any OS supported by the agent. | Linux (bubblewrap); macOS (Seatbelt/App Sandbox).           | Linux Engine; Docker Desktop on macOS/Windows.              | macOS Apple silicon, Windows 11, Ubuntu 24.04+ with KVM.           | Linux with KVM, macOS Apple Silicon.       |
| **Access restrictions** | None. | None. | None. | ⚠️ Docker account sign-in required. | None. |

Sources: [bubblewrap](https://github.com/containers/bubblewrap#sandbox-security), [Apple App Sandbox](https://developer.apple.com/documentation/security/app-sandbox),
[Docker](https://docs.docker.com/engine/security/), and [Docker Sandboxes](https://docs.docker.com/ai/sandboxes/security/). Platform requirements change; check upstream docs.

Legend for the first four rows: ✅ = stronger isolation or host-side handling; ⚠️ = policy-dependent or deliberately shared; ❌ = no isolation in that category. The platform and access rows are descriptive.

## Boundary and tradeoffs

| Surface | Behavior |
|---------|----------|
| VM kernel and root filesystem  | Separate from the host, using the microsandbox VM boundary.                                                                                                                                                  |
| Agent home                | Stored in a persistent `/home/dev` volume scoped to the project and agent. Recreating the VM does not remove this volume unless you reset or prune it.                                                       |
| `/workspace`              | The host project directory, mounted read-write in a normal session. Some agents allow you to optionally use a  [worktree-session](docs/branch-sessions.md) for a VM-internal worktree instead. |
| Other host files          | Not visible unless they are copied, provisioned with `home:`, or exposed through an explicit host mount.                                                                                                     |
| Network                   | Egress is denied by default. Profiles and allow/deny rules can grant access; `profile: none` is deny-by-default egress, not a complete air gap.                                                              |
| Raw credentials           | `env.secret` and `env.secret.yaml` keep the real value on the host and expose a placeholder to the guest. This is separate from ordinary file provisioning. |

> **Credential warning:** Native host-agent configuration is not copied into the VM by default. To use the convenience
> workflow with an existing host setup, set `provision-host-config: true`. For opencode, that can include
> `~/.local/share/opencode/auth.json`. If credentials must not be stored in the VM, keep host-config provisioning disabled,
> use the secret mechanism, and keep credentials out of `/workspace`, `home:`, `env`, and writable mounts. See the
> [Secrets](docs/configuration/secrets.md) and [Agent configuration](docs/configuration/agent.md) documentation.

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

Install agents-sandbox using the [installation guide](https://inoio.github.io/agents-sandbox/install.html), then
check the host prerequisites and start the configured agent:

```console
agents-sandbox doctor
agents-sandbox
```

The default path does not copy native host-agent configuration. To use an existing host setup, opt in with
`provision-host-config: true`; for a self-contained setup with explicit secret handling, start with
[Manage config in the sandbox](docs/manage-config.md) instead.

## Agent selection

You can use the `--agent <name>` flag (available on `run`, `shell`, `build`, `volume`, `stop`, and `kill`) to
select the coding-agent to run, provision, or manage. This is also available as a setting in the [configuration file](configuration/launcher.md).
`--agent-version` pins the version agents-sandbox installs into the runner image; it does not replace an agent already
provided by a custom base or project Dockerfile.

Four agents ship as built-in profiles:

- **`opencode`** (default) — a daemon-based agent with serve/attach, worktree sessions, and GitHub-release upgrade checks.
- **`opencode2`** — opencode 2 (beta), installed from `@opencode-ai/cli@beta` on npm; daemon-based with serve/attach,
  worktree sessions, and npm beta-tag upgrade checks. It shares the `opencode` config directory (v2 reads the same files
  as v1).
- **`pi`** — the pi coding agent (`@earendil-works/pi-coding-agent`), run interactively, with upgrade checks via `pi.dev`.
- **`claude-code`** — Anthropic's Claude Code (`@anthropic-ai/claude-code`), run interactively, with upgrade checks via the
  npm registry.

## Documentation

There's dedicated documentation per topic. You can also browse the documentation on [GitHub Pages](https://inoio.github.io/agents-sandbox/).

| Topic                                         | Description                                                                              |
|-----------------------------------------------|------------------------------------------------------------------------------------------|
| [Why?](/docs/introduction.md)                 | Motivation, isolation boundary, shared data, and limitations.                            |
| [How it works](/docs/how-it-works.md)         | Architecture: host to VM, `/workspace`, home volume, secrets, and multi-client attach.   |
| [Install](/docs/install.md)                   | Installation and prerequisites.                                                           |
| [Switch from your existing agent](/docs/switch.md) | Opt in to using your existing agent config and credentials (host-config drop-in). |
| [Manage config in the sandbox](/docs/manage-config.md) | Declarative, self-contained config: secrets, provisioning, agent snippets.            |
| [Commands](/docs/commands.md)                 | Complete CLI reference                                                                   |
| [Configuration](/docs/configuration/)         | Split into subpages: Configuration files & Environment variables, secrets, networking, host mounts, home provisioning & startup hooks, agent configuration, notifications, self-upgrade |
| [Runner Image](/docs/runner-image.md)         | Base image, custom tooling                                                               |
| [Worktree Sessions](/docs/branch-sessions.md) | Isolated worktree sessions for per-feature development                                   |
| [Recipes](/docs/recipes.md)                   | Hands-on workflows                                  |
| [Sandboxes](/docs/sandboxes.md)               | VM lifecycle, volumes, pruning                                                           |
| [Troubleshooting](/docs/troubleshooting.md)   | Common issues and fixes                                                                  |
| [Roadmap](/ROADMAP.md)                        | Public, forward-looking project roadmap                                                  |

## Contributing  

| Topic                                  | Description                                     |
|----------------------------------------|-------------------------------------------------|
| [Contributing](/CONTRIBUTING.md)       | Guidelines for contributing to agents-sandbox |
| [Code of conduct](/CODE_OF_CONDUCT.md) | Our code of conduct                             |
| [Security](/SECURITY.md)               | Rules for submitting security issues            |
| [Roadmap](/ROADMAP.md)                 | Public, forward-looking project roadmap         |
