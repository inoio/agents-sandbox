---
title: Recipes
description: "Hands-on, self-contained how-tos for common agents-sandbox workflows, from isolated worktree sessions to connecting a desktop client."
layout: default
nav_order: 30
has_children: true
has_toc: false
---
# Recipes

Hands-on how-tos for common workflows with agents-sandbox. Each recipe is
self-contained, so you can jump straight to the one you need.

- [Worktree Sessions]({% link branch-sessions.md %})
- [Connect Opencode Desktop]({% link recipes/connect-desktop.md %})
- [Bare Checkout with Worktrees Inside]({% link recipes/bare-worktrees.md %})
- [Trust the Sandbox CA in Java]({% link recipes/java-ca-trust.md %})
- [Connect OpenChamber]({% link recipes/openchamber.md %})

## Agent context (AGENTS.md)

An `AGENTS.md` in the working directory can orient the agent on being in a sandbox VM, available tooling etc. Here is a minimal, self-explanatory example you can copy and adapt:

```markdown
## Environment

You are running inside a sandbox VM, not on the host. Filesystem layout:

- `/workspace` bind mount of the host CWD, mounted rw.
- `~/.local/share/opencode/worktree/` git worktrees of `/workspace`, created by the agent's daemon

## Toolchain

Common CLI tools are preinstalled: git, node, npm, jq, yq etc. Don't install additional tools yourself without permission.

No SSH keys in the VM, git cmds against remotes won't work.
```

When you launch from inside a git worktree or a repository subdirectory, `/workspace` is bound to the enclosing
checkout root rather than your current directory.

