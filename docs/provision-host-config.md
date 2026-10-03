---
title: Unsafe host-config drop-in
layout: default
nav_exclude: true
---

# Unsafe host-config drop-in

`provision-host-config` is an optional, unsafe compatibility path. It copies selected native agent files from the host into the
persistent VM home without requiring managed snippets. Use it only for a quick proof of concept, debugging with the exact host
configuration, or low-sensitivity work where that file copy is acceptable.

For the exact files, precedence rules, and per-agent provision manifests, see [Agent configuration]({% link configuration/agent.md %}#host-config-drop-in-provisioning).
For the recommended migration or a clean manual setup, see [Manage config in the sandbox]({% link manage-config.md %}).

## Opt in to host config

The native host-config drop-in is disabled by default. To use the existing agent setup, add this to the top-level launcher
configuration at `~/.config/agents-sandbox/config.yaml`:

```yaml
provision-host-config: true
```

Alternatively, choose **Use native host config** when the first interactive start offers the migration; agents-sandbox then sets
the option for you. A `config.json`, `config.jsonc`, or `config.json5` launcher config is rewritten as plain JSON and loses its
comments; a YAML config keeps them.

`provision-host-config: true` is intentionally not a migration mechanism. It copies native files as ordinary files and can place
raw credentials in the VM. The managed workflows keep host provisioning disabled and use snippets, placeholders, or a login inside
the persistent sandbox home instead. See [Manage config in the sandbox]({% link manage-config.md %}) for those workflows.

## Run it

In any project directory:

```console
agents-sandbox
```

With host-config provisioning enabled, agents-sandbox copies the files selected by the active agent's provision rules. OpenCode's
rules include its native settings and `auth.json`; Pi and Claude Code deliberately exclude their native credential stores. This is
therefore a file drop-in, not a complete credential migration for every agent.

> **Important:** This is provisioning, not a live mount. Changes on the host are picked up on a later provisioning/start cycle;
> an already-running agent does not automatically reload them. The VM home is persistent, so files copied by this workflow can
> remain until they are removed by reprovisioning or the home volume is reset.

## ⚠️ What to know

The opt-in path shares your host config **and** credentials (for OpenCode, `auth.json`) into the VM. Do not use it when secrets
must remain off disk in the VM or when you need a self-contained, reproducible setup. Use
[Manage config in the sandbox]({% link manage-config.md %}) and [Secrets]({% link configuration/secrets.md %}) instead.

## Technical details

Snippets, config-directory mirrors, and `home:` mappings remain active and take precedence over the drop-in. See
[Agent configuration]({% link configuration/agent.md %}#host-config-drop-in-provisioning) for the precedence rules and the files
selected for each agent.
