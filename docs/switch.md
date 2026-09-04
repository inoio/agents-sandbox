---
title: Switch from your existing agent
layout: default
nav_order: 30
---

# Switch from your existing agent

Use opencode-sandbox with the agent setup you already have without writing any config. If you haven't
installed opencode-sandbox yet, [install it]({% link index.md %}#install) first.

## Run it

In any project directory:

```console
opencode-sandbox
```

Opencode-sandbox copies your existing agent config (e.g. `~/.config/opencode/**`)
and credentials into the VM by default, so your normal agent,
models, and permissions are available immediately, now running in a hardware-isolated VM with
your project at `/workspace`.

> **Keep your normal workflow.** Because your config is mirrored, you can keep managing it where
> and how you already do, including managing config via the agents' TUI on the host, and it stays in sync
> with the sandbox.

## ⚠️ What to know

The fast path shares your host config **and** credentials (for opencode, `auth.json`) into the
VM. This is fine for trying it out or low-sensitivity work. When you want no secrets exposed to agents, 
not even the provider API keys, or you want a self-contained, reproducible setup, see
[Manage config in the sandbox]({% link manage-config.md %}) and
[Secrets]({% link configuration/secrets.md %}).

## Next steps

- [Manage config in the sandbox]({% link manage-config.md %})
- [Secrets]({% link configuration/secrets.md %})
- [Agent configuration]({% link configuration/agent.md %})
