---
title: Switch from your existing agent
description: "Run your existing Claude Code, Codex, opencode, opencode2, pi, hermes, oh-my-pi, kimi-code, qwen-code, goose, grok-build or mistral-vibe setup inside an agents-sandbox microVM without writing any config."
layout: default
parent: Get started
nav_order: 30
---

# Switch from your existing agent

Use agents-sandbox with the agent setup you already have. If you haven't
installed agents-sandbox yet, [install it]({% link install/installation.md %}) first.

## Run it

In any project directory:

```console
agents-sandbox
```

Turn on host-config provisioning to copy your existing agent config (e.g. `~/.config/opencode/**`)
and credentials into the VM, so your normal agent,
models, and permissions are available immediately, now running in a hardware-isolated VM with
your project at `/workspace`.

```yaml
# ~/.config/agents-sandbox/config.yaml
provision-host-config: true
```

> **Note:** Provisioning is off by default — nothing from your host config is copied until you turn it on. Keep your
> normal workflow — because the copied config is mirrored, you can keep managing it where
> and how you already do, including managing config via the agents' TUI on the host, and it stays in sync
> with the sandbox.

## What to know

The fast path shares your host config **and** credentials (for opencode, `auth.json`) into the
VM. This is fine for trying it out or low-sensitivity work. When you want no secrets exposed to agents, 
not even the provider API keys, or you want a self-contained, reproducible setup, see
[Manage config in the sandbox]({% link manage-config.md %}) and
[Secrets]({% link configuration/secrets.md %}).

## Next steps

- [Manage config in the sandbox]({% link manage-config.md %})
- [Secrets]({% link configuration/secrets.md %})
- [Agent configuration]({% link configuration/agent.md %})
