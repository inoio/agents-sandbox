---
title: Switch from your existing agent
layout: default
nav_order: 30
---

# Switch from your existing agent

Use opencode-sandbox with the agent setup you already have — no config to write. If you haven't
installed it yet, [install it]({% link index.md %}#install) first.

## Run it

In any project directory:

```console
opencode-sandbox
```

That's it. opencode-sandbox copies your existing agent config (e.g. `~/.config/opencode/**`)
and credentials into the VM by default (`provision-host-config: true`), so your normal agent,
models, and permissions are available immediately — now running in a hardware-isolated VM with
your project at `/workspace`.

> **Keep your normal workflow.** Because your config is mirrored, you can keep managing it the
> way you already do — including interactive config editing on the host — and it stays in sync
> with the sandbox.

## What to know

The fast path shares your host config **and** credentials (for opencode, `auth.json`) into the
VM. This is fine for trying it out or low-sensitivity work. When you want secrets that are never
written into the VM — or a self-contained, reproducible setup — see
[Manage config in the sandbox]({% link manage-config.md %}) and
[Secrets]({% link configuration/secrets.md %}).

## Next steps

- [Manage config in the sandbox]({% link manage-config.md %})
- [Secrets]({% link configuration/secrets.md %})
- [Agent configuration]({% link configuration/agent.md %})
- [provision-host-config]({% link configuration/launcher.md %})
