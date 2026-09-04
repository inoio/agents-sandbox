---
title: Recipes
layout: default
nav_order: 100
has_children: true
has_toc: false
---
# Recipes

Hands-on how-tos for common workflows with agents-sandbox. Each recipe is
self-contained, so you can jump straight to the one you need.

## Agent Context (AGENTS.md)

An `AGENTS.md` in the working directory can orient the agent on being in a sandbox VM, available tooling etc. Here is a minimal, self-explanatory example you can copy and adapt:

```dotenv
## Environment

You are running inside a sandbox VM, not on the host. Filesystem layout:

- `/workspace` bind mount of the host CWD, mounted rw.
- `~/.local/share/opencode/worktree/` git worktrees of `/workspace`, created by the opencode daemon

## Toolchain

Common CLI tools are preinstalled: git, node, npm, jq, yq etc. Don't install additional tools yourself without permission.

No SSH keys in the VM, git cmds against remotes won't work.
```

