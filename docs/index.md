---
title: agents-sandbox
layout: home
nav_order: 0
---

# agents-sandbox

agents-sandbox allows you to isolate your coding agents from the rest of your dev environment. You are put in control over what parts of your host machine and network environment are available to each agent. agents-sandbox acts as a launcher for [opencode](https://opencode.ai), [opencode2](https://opencode.ai),
[pi](https://pi.dev), and [Claude Code](https://claude.com/product/claude-code). It builds or reuses a dedicated [microsandbox](https://microsandbox.dev/) VM per
project and agent, then attaches the selected agent inside that VM.

On top of that, agents-sandbox utilizes the robust secrets management of microsandbox, which significantly reduces the exposure of secrets to the agent. This way, your helpful assistant (or even a rogue agent) won't be able to accidentally publish dev secrets to a public wiki page ;)

At the same time, agents-sandbox moves out of the way as much as possible, so that you don't have to adapt to a completely new workflow.

## Comparison

The table compares local execution models, not hosted services. Bubblewrap/Seatbelt and Docker are policy-dependent building
blocks, so the entries summarize their usual mechanisms rather than assigning a security score.

| Category | Direct host execution | Bubblewrap / Seatbelt                                       | Docker containers                                           | Docker Sandboxes                                                   | agents-sandbox                                                  |
|---|---|-------------------------------------------------------------|-------------------------------------------------------------|--------------------------------------------------------------------|-----------------------------------------------------------------|
| **Isolation boundary** | ❌ No boundary; host kernel and user permissions. | ⚠️ Shared host kernel; policy-defined OS sandbox.           | ⚠️ Shared host kernel; policy-defined OS sandbox.           | ✅ Separate microVM and Linux kernel.                               | ✅ Separate microVM and Linux kernel.                            |
| **Secrets handling** | ❌ Host environment, files, and agent credentials are available. | ⚠️ No host-only broker; supplied values are visible.        | ⚠️ No host-only broker; supplied values are visible.        | ✅ Host proxy injects credentials; VM sees a sentinel.              | ✅ Host proxy injects credentials; VM sees a sentinel. |
| **Filesystem isolation** | ❌ Host user files are accessible. | ⚠️ Private sandbox view; configured host mounts are shared. | ⚠️ Private sandbox view; configured host mounts are shared. | ✅ Private VM; workspace sharing depends on direct/clone/mountless mode. | ✅ Private VM; only workspace directory is read-write by default; |
| **Network isolation** | ❌ Host network stack. | ⚠️ Manual firewalling.                                      | ⚠️ Manual firewalling.                                      | ✅ Policy-defined network isolation.                                                         | ✅ Policy-defined network isolation.                   |
| **Platform/OS Support** | Any OS supported by the agent. | Linux (bubblewrap); macOS (Seatbelt/App Sandbox).           | Linux Engine; Docker Desktop on macOS/Windows.              | macOS Apple silicon, Windows 11, Ubuntu 24.04+ with KVM.           | Linux with KVM, macOS Apple Silicon.       |
| **Access restrictions** | None. | None. | None. | ⚠️ Docker account sign-in required. | None. |

Sources: [bubblewrap](https://github.com/containers/bubblewrap#sandbox-security), [Apple App Sandbox](https://developer.apple.com/documentation/security/app-sandbox),
[Docker](https://docs.docker.com/engine/security/), and [Docker Sandboxes](https://docs.docker.com/ai/sandboxes/security/). Platform requirements change; check upstream docs.

Legend for the first four rows: ✅ = stronger isolation or host-side handling; ⚠️ = policy-dependent or deliberately shared; ❌ = no isolation in that category. The platform and access rows are descriptive.

## Start here

- If you already have a supported agent configured, start with **[Switch from your existing agent]({% link switch.md %})**.
- If you want reproducible configuration and explicit credential handling, start with **[Manage config in the sandbox]({% link manage-config.md %})**.
- If you have not used a supported agent before, set it up using its normal host installation first, then return here.

## Explore

- [Why?]({% link introduction.md %})
- [Installation]({% link install/installation.md %})
- [Switch from your existing agent]({% link switch.md %})
- [Manage config in the sandbox]({% link manage-config.md %})
- [Configuration]({% link configuration/index.md %})
- [Sandboxes]({% link sandboxes.md %})
- [Runner Image]({% link runner-image.md %})
- [Commands]({% link commands.md %})
- [Worktree Sessions]({% link branch-sessions.md %})
- [Recipes]({% link recipes.md %})
- [Architecture & Concepts]({% link how-it-works.md %})
- [Troubleshooting]({% link troubleshooting.md %})
