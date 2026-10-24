---
title: agents-sandbox
title_category: Sandbox for Claude Code, opencode, opencode2 & pi in a microVM
layout: home
nav_order: 0
---

# agents-sandbox

agents-sandbox runs coding agents — [opencode](https://opencode.ai), [opencode2](https://opencode.ai), [pi](https://pi.dev), and [Claude Code](https://claude.com/product/claude-code) — inside a dedicated [microsandbox](https://microsandbox.dev/) [Virtual Machine](https://en.wikipedia.org/wiki/Virtual_machine) for each project, instead of directly on your machine, with your project mounted at `/workspace`.

## Isolation features

* Best same-machine isolation boundary: Run your agent inside a virtual machine, with fully separate OS/linux kernel.
* Keep secrets on the host: Supply credentials through secret files (in env file or yaml format). They reach the sandbox VM only as placeholders that agents-sandbox substitutes on-host for the hosts you allow.
* Manage the agent's network access: Set access profiles, fine-tune down to host level with allow-/denylists.

## Comparison

This comparison covers local execution models only, not hosted services. Bubblewrap/Seatbelt and Docker are policy-dependent building
blocks, so the entries summarize their usual mechanisms rather than assigning a security score.

| Category | Direct host execution | Bubblewrap / Seatbelt                                       | Docker containers                                           | Docker Sandboxes                                                   | agents-sandbox                                                  |
|---|---|-------------------------------------------------------------|-------------------------------------------------------------|--------------------------------------------------------------------|-----------------------------------------------------------------|
| **Isolation boundary** | ❌ No boundary; host kernel and user permissions. | ⚠️ Shared host kernel; policy-defined OS sandbox.           | ⚠️ Shared host kernel; policy-defined OS sandbox.           | ✅ Separate microVM and Linux kernel.                               | ✅ Separate microVM and Linux kernel.                            |
| **Secrets handling** | ❌ Host environment, files, and agent credentials are available. | ⚠️ No host-only broker; supplied values are visible.        | ⚠️ No host-only broker; supplied values are visible.        | ✅ Host proxy injects credentials; VM sees a sentinel.              | ✅ Host proxy injects credentials; VM sees a sentinel. |
| **Filesystem isolation** | ❌ Host user files are accessible. | ⚠️ Private sandbox view; configured host mounts are shared. | ⚠️ Private sandbox view; configured host mounts are shared. | ✅ Private VM; workspace sharing depends on direct/clone/mountless mode. | ✅ Private VM; only the workspace directory is read-write by default, plus opt-in host mounts. |
| **Network isolation** | ❌ Host network stack. | ⚠️ Manual firewalling.                                      | ⚠️ Manual firewalling.                                      | ✅ Policy-defined network isolation.                                                         | ✅ Policy-defined network isolation.                   |
| **Platform/OS Support** | Any OS supported by the agent. | Linux (bubblewrap); macOS (Seatbelt/App Sandbox).           | Linux Engine; Docker Desktop on macOS/Windows.              | macOS Apple silicon, Windows 11, Ubuntu 24.04+ with KVM.           | Linux with KVM, macOS Apple Silicon.       |
| **Access restrictions** | None. | None. | None. | ⚠️ Docker account sign-in required. | None. |

Sources: [bubblewrap](https://github.com/containers/bubblewrap#sandbox-security), [Apple App Sandbox](https://developer.apple.com/documentation/security/app-sandbox),
[Docker](https://docs.docker.com/engine/security/), and [Docker Sandboxes](https://docs.docker.com/ai/sandboxes/security/). Platform requirements change; check upstream docs.

Legend for the first four rows: ✅ = stronger isolation or host-side handling; ⚠️ = policy-dependent or deliberately shared; ❌ = no isolation in that category. The platform and access rows are descriptive.

## How to start using agents-sandbox

- If you already have a supported agent configured, start with **[Switch from your existing agent]({% link switch.md %})**.
- If you want reproducible configuration and explicit credential handling, start with **[Manage config in the sandbox]({% link manage-config.md %})**.
- If you have not used a supported agent before, set it up using its normal host installation first, then return here.

## Explore

Each section groups the pages by what you are trying to do.

- [Get started]({% link get-started.md %}) — install agents-sandbox and run your first agent.
- [Guides]({% link guides.md %}) — sandboxes, the runner image, and upgrading.
- [Recipes]({% link recipes.md %}) — walkthroughs for specific setups.
- [Configuration]({% link configuration/index.md %}) — config files, secrets, networking, and mounts.
- [Concepts]({% link concepts.md %}) — why the isolation is built this way.
- [Reference]({% link reference.md %}) — commands, troubleshooting, and deprecations.
