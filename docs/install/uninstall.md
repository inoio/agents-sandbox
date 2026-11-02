---
title: Uninstalling
description: "Remove agents-sandbox from your machine: its sandboxes and home volumes, its runner images, its configuration, and the binary."
layout: default
parent: Guides
nav_order: 40
---

# Uninstalling

This page removes agents-sandbox and everything it stored on your machine. Work through the steps in order: the
binary goes last, because the earlier steps use it.

> **Note:** Removing the data directory deletes every project's home volume, which holds your agents' sessions, login
> state, and shell history inside the sandbox. Copy out anything you want to keep before you remove the data directory.

## Quick reference

| What                                   | Where (default)                                  | Remove with                                                     |
|----------------------------------------|--------------------------------------------------|-----------------------------------------------------------------|
| Sandboxes, home volumes, msb images    | `~/.local/share/agents-sandbox`                  | `rm -rf ~/.local/share/agents-sandbox`                          |
| Host state (projects, locks, upgrades) | `~/.local/state/agents-sandbox`                  | `rm -rf ~/.local/state/agents-sandbox`                          |
| Cache                                  | `~/.cache/agents-sandbox`                        | `rm -rf ~/.cache/agents-sandbox`                                |
| Your configuration and secrets         | `~/.config/agents-sandbox`                       | `rm -rf ~/.config/agents-sandbox`                               |
| Runner images in Docker                | `agents-sandbox/runner-*` in your Docker daemon  | see [Remove the runner images](#remove-the-runner-images)      |
| The binary                             | where you installed it, or Homebrew              | see [Remove the binary](#remove-the-binary)                    |

When `XDG_DATA_HOME`, `XDG_STATE_HOME`, `XDG_CACHE_HOME`, or `XDG_CONFIG_HOME` is set to an absolute path, the matching
directory is `agents-sandbox` under that path instead. This applies on macOS too.

## Stop every sandbox

Leave every agent session first. Then check that no sandbox is still running:

```console
agents-sandbox list --running
```

For each sandbox the list still shows, change into its project directory and stop it, naming the agent when it is not
the default:

```console
cd /path/to/your/project
agents-sandbox stop --agent opencode
```

## Remove the runner images

agents-sandbox builds one runner image per project and agent in your Docker daemon. Remove them all:

{% raw %}
```console
docker image ls --filter 'reference=agents-sandbox/runner-*' --format '{{.Repository}}:{{.Tag}}' \
  | xargs docker image rm
```
{% endraw %}

When no runner image is left, `docker image rm` prints a usage error. You can ignore it.

## Remove the data, state, and cache directories

The data directory holds agents-sandbox's own microsandbox runtime, with every sandbox, home volume, and imported
image. The state directory records which sandbox and volume belong to which project.

```console
rm -rf ~/.local/share/agents-sandbox
rm -rf ~/.local/state/agents-sandbox
rm -rf ~/.cache/agents-sandbox
```

## Remove your configuration

Skip this step if you plan to reinstall later and want to keep your settings. The configuration directory holds your
`config.yaml`, the forwarded environment, your secrets files, and the agent configuration snippets.

```console
rm -rf ~/.config/agents-sandbox
```

A project can also carry its own configuration in `.agents-sandbox/` inside the repository. That directory is part of
the project, so leave it unless you own the project and no longer want it.

## Remove the binary

If you installed with Homebrew:

```console
brew uninstall agents-sandbox
brew untap inoio/agents-sandbox
```

If you downloaded the binary, delete it from the directory you moved it to:

```console
rm "$(command -v agents-sandbox)"
```

## Leftovers from agents-sandbox 0.5 and earlier

Up to 0.5.x, agents-sandbox stored its sandboxes and home volumes in your system-wide microsandbox home,
`~/.microsandbox`. If you used one of those versions, remove those leftovers as well:

```console
rm -rf ~/.microsandbox/volumes/agents-sandbox-home-*
rm -rf ~/.microsandbox/sandboxes/agents-sandbox-vm-*
rm -rf ~/.microsandbox/sandboxes/agents-sandbox-task-*
```

Everything else in `~/.microsandbox` belongs to your own microsandbox installation. Remove the whole folder only if you
installed microsandbox just for agents-sandbox. [Upgrading to 0.6]({% link install/upgrading.md %}#removing-the-old-microsandbox-home)
explains the old layout.
