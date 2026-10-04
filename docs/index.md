---
title: Home
layout: home
nav_order: 0
---

# agents-sandbox

agents-sandbox allows you to isolate your coding agents from the rest of your dev environment. You are put in control over what parts of your host machine and network environment are available to each agent. agents-sandbox acts as a launcher for [opencode](https://opencode.ai), [opencode2](https://opencode.ai),
[pi](https://pi.dev), and [Claude Code](https://claude.com/product/claude-code). It builds or reuses a dedicated [microsandbox](https://microsandbox.dev/) VM per
project and agent, then attaches the selected agent inside that VM.

On top of that, agents-sandbox utilizes the robust secrets management of microsandbox, which significantly reduces the exposure of secrets to the agent. This way, your helpful assistant (or even a rogue agent) won't be able to accidentally publish dev secrets to a public wiki page ;)

At the same time, agents-sandbox moves out of the way as much as possible, so that you don't have to adapt to a completely new workflow.

How does it compare to other tools? The [alternatives page]({% link alternatives.md %}) covers NVIDIA OpenShell, Anthropic
sandbox-runtime, nono, smolvm, Docker Sandboxes, and others, and helps you pick the right tool.

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
