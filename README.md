# opencode-sandbox

> **opencode, supercharged — safely.** Run opencode in a near-instant, hardware-isolated VM — your project at `/workspace`, your secrets safe, your agent free to do its best work.

[![License: GPL-3.0](https://img.shields.io/badge/license-GPL--3.0-blue)](https://github.com/inoio/opencode-sandbox/blob/main/LICENSE.md)
[![CI](https://github.com/inoio/opencode-sandbox/actions/workflows/ci.yml/badge.svg)](https://github.com/inoio/opencode-sandbox/actions/workflows/ci.yml)
[![Go](https://img.shields.io/github/go-mod/go-version/inoio/opencode-sandbox)](https://go.dev/)
[![Release](https://img.shields.io/github/v/release/inoio/opencode-sandbox)](https://github.com/inoio/opencode-sandbox/releases)
[![Coverage](https://codecov.io/gh/inoio/opencode-sandbox/branch/main/graph/badge.svg)](https://codecov.io/gh/inoio/opencode-sandbox)
[![Security](https://img.shields.io/badge/security-policy-purple.svg)](SECURITY.md)
[![Docs](https://img.shields.io/badge/docs-github--pages-blue)](https://inoio.github.io/opencode-sandbox/)

opencode-sandbox gives [opencode](https://github.com/anomalyco/opencode) a real, hardware-isolated machine to work on —
full agent permissions inside a boundary that can't reach your host, and secrets the agent never gets to see.

Docker, bubblewrap, seatbelt, and bare opencode all share your kernel — a kernel bug or `sudo` is enough for an agent to
reach your machine. opencode-sandbox runs a separate kernel under hypervisor isolation (KVM on Linux, Apple Silicon on
macOS), so escaping takes a hypervisor-level bug: a much higher bar.

Your project is mounted at `/workspace`, read-write, so the agent works on the same files you do and edits round-trip.
Everything else on your machine — other projects, your home directory, your keys — simply isn't there, except for what
you explicitly provision into the VM's home (via yaml-based configuration). Secrets are injected at runtime through the secret
mechanism as environment variables and never written into the VM, so an agent can't leak what it never possessed. Worst
case, a session is a disposable VM: wipe it, and the host is untouched.

It's also yours to shape: the VM's root is defined by a plain `Dockerfile`, you bring your own base image and tooling like any OCI
image you already use — and it's built for opencode first, with support for functionalities like worktree sessions. Just bring your own `opencode.json`.
Egress and ingress stay under your control with simple profiles and allow/deny lists, from full network access to complete lockdown.

|  | Bare opencode | Bubblewrap / Seatbelt | Docker (containers) | Docker Sandboxes | **opencode-sandbox** |
|---|---|---|---|---|---|
| **Isolation boundary** | ❌ none | ⚠️ shared kernel | ⚠️ shared kernel | ✅ full VM (microVM) | **✅ full VM (hypervisor)** |
| **How hard to hide secrets?** | ❌ nearly impossible | ⚠️ complex per-project rules | ⚠️ manual per-project tweaking | ✅ built-in (proxy; login-required) | **✅ built-in mechanism** |
| **Agent edits appear in your local files instantly** | ✅ | ✅ | ✅ | ✅ rw mount (clone mode is read-only) | **✅** |
| **Failure cost vs. recovery** | ❌ high damage, hard to restore | ⚠️ potential host damage | ⚠️ potential host damage | ✅ disposable | **✅ disposable, home can persist** |
| **Ease of use** | ✅ just run it | ⚠️ craft rules | ⚠️ image + mounts | ✅ one command (Docker account login) | **✅ one command** |

> Cells give the typical story for each approach. ✅ = yes / good, ⚠️ = possible but partial / in-between, ❌ = no / poor. "Failure cost vs. recovery" weighs how much damage a rogue agent can cause against how easily you can throw the environment away and start over.

> **❓ Why not just use Docker Sandboxes?**
>
> Its microVM isolation is genuinely strong. But it's a trade: a **mandatory Docker account login** for a tool that runs locally, a **closed-source core** (VMM + policy proxy + credential injection) you're trusting as your security boundary, **org-wide controls behind a paid sales tier**, and **narrower reach** (Ubuntu 24.04+ / Apple silicon / Windows 11 only).
>
> opencode-sandbox, by contrast, is **open and account-free**, runs on **any Linux (KVM) and Apple Silicon**, and gives you **one-command disposal** — without the telemetry, login, or vendor lock-in.

## Agents

opencode-sandbox is agent-aware. A `--agent <name>` flag on `run`, `build`, and the `volume` subcommands selects the
coding-agent profile to run and provision; `--agent-version` pins the agent version baked into the runner image
(replacing the older `--opencode-version`, which remains as a deprecated alias). Four agents ship as built-in profiles:

- **`opencode`** (default) — a daemon-based agent with serve/attach, worktree sessions, and GitHub-release upgrade checks.
- **`opencode2`** — opencode 2 (beta), installed from `@opencode-ai/cli@beta` on npm; daemon-based with serve/attach,
  worktree sessions, and npm beta-tag upgrade checks. It shares the `opencode` config directory (v2 reads the same files
  as v1).
- **`pi`** — the pi coding agent (`@earendil-works/pi-coding-agent`), run interactively, with upgrade checks via `pi.dev`.
- **`claude-code`** — Anthropic's Claude Code (`@anthropic-ai/claude-code`), run interactively, with upgrade checks via the
  npm registry.

`--worktree` and `--serve-only` are rejected for agents that have no daemon (pi, claude-code); they run through the
interactive TUI instead.

By default the launcher copies the active agent's config + credential files from the host into the VM. **Security note:**
this includes the opencode `auth.json` credential file. If you prefer to deliver credentials via the env-secret mechanism
(which never writes them into the VM), see the [Configuration docs](/docs/configuration.md) to opt out of the file copy.

## Documentation

There's dedicated documentation per topic. You can also browse the documentation on [GitHub Pages](https://inoio.github.io/opencode-sandbox/).

| Topic                                         | Description                                                                              |
|-----------------------------------------------|------------------------------------------------------------------------------------------|
| [Introduction](/docs/introduction.md)         | Introduction and motivation.                                                             |
| [Getting Started](/docs/getting-started.md)   | Installation, prerequisites, configuration, first run                                    |
| [Commands](/docs/commands.md)                 | Complete CLI reference                                                                   |
| [Configuration](/docs/configuration.md)       | Launcher config, env, secrets, agent snippet merge, drop-in provisioning, `home.yaml` (incl. startup hooks) |
| [Runner Image](/docs/runner-image.md)         | Base image, custom tooling                                                               |
| [Worktree Sessions](/docs/branch-sessions.md) | Isolated worktree sessions for per-feature development                                   |
| [Recipes](/docs/recipes.md)                   | Hands-on workflows (e.g. connecting Opencode Desktop)                                    |
| [Sandboxes](/docs/sandboxes.md)               | VM lifecycle, volumes, pruning                                                           |
| [Troubleshooting](/docs/troubleshooting.md)   | Common issues and fixes                                                                  |
| [Roadmap](/ROADMAP.md)                        | Public, forward-looking project roadmap                                                  |

## Contributing  

| Topic                                  | Description                                     |
|----------------------------------------|-------------------------------------------------|
| [Contributing](/CONTRIBUTING.md)       | Guidelines for contributing to opencode-sandbox |
| [Code of conduct](/CODE_OF_CONDUCT.md) | Our code of conduct                             |
| [Security](/SECURITYmd)                | Rules for submitting security issues            |
| [Roadmap](/ROADMAP.md)                 | Public, forward-looking project roadmap         |
