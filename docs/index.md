---
title: agents-sandbox
title_category: Sandbox for Claude Code, Codex, opencode, opencode2, pi & hermes in a microVM
layout: home
nav_order: 0
---

# agents-sandbox

agents-sandbox runs coding agents — such as [opencode](https://opencode.ai), [Claude Code](https://claude.com/product/claude-code), [Codex](https://github.com/openai/codex), and [pi](https://pi.dev); see [all supported agents]({% link configuration/agent.md %}#built-in-agents) — inside a dedicated [microsandbox](https://microsandbox.dev/) [Virtual Machine](https://en.wikipedia.org/wiki/Virtual_machine) for each project, instead of directly on your machine, with your project mounted at `/workspace`.

## Isolation features

* Best same-machine isolation boundary: Run your agent inside a virtual machine, with fully separate OS/linux kernel.
* Keep secrets on the host: Supply credentials through secret files (in env file or yaml format). They reach the sandbox VM only as placeholders that agents-sandbox substitutes on-host for the hosts you allow.
* Manage the agent's network access: Set access profiles, fine-tune down to host level with allow-/denylists.

How does it compare to other tools? The [alternatives page]({% link alternatives.md %}) covers NVIDIA OpenShell, Anthropic
sandbox-runtime, nono, smolvm, Docker Sandboxes, and others, and helps you pick the right tool.

## How to start using agents-sandbox

- If you already have a supported agent configured, start with **[Switch from your existing agent]({% link switch.md %})**.
- If you want reproducible configuration and explicit credential handling, start with **[Manage config in the sandbox]({% link manage-config.md %})**.
- If you have not used a supported agent before, set it up using its normal host installation first, then return here.

## Explore

Each section groups the pages by what you are trying to do.

- [Get started]({% link get-started.md %}) — install agents-sandbox and run your first agent.
- [Alternatives]({% link alternatives.md %}) — how agents-sandbox compares with other local agent sandboxes and runtimes.
- [Guides]({% link guides.md %}) — sandboxes, the runner image, and upgrading.
- [Recipes]({% link recipes.md %}) — walkthroughs for specific setups.
- [Configuration]({% link configuration/index.md %}) — config files, secrets, networking, and mounts.
- [Concepts]({% link concepts.md %}) — why the isolation is built this way.
- [Reference]({% link reference.md %}) — commands, troubleshooting, and deprecations.
