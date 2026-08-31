---
title: Getting Started
layout: default
nav_order: 20
has_children: true
has_toc: false
---

# Getting Started

## Quick Start


See [Configuration]({% link configuration.md %}#secrets) for more details.

## How It Works

1. **Image build** — Builds a Docker image from `.opencode-sandbox/Dockerfile` if present, or uses the base image. The image
   contains opencode, Node.js 26, and common CLI tools.
2. **Volume setup** — Creates a persistent home volume (managed by msb, name: `opencode-sandbox-home-<project-slug>-<timestamp>`) for the
   project, preserving editor state, caches, and config across sessions.
3. **VM creation or reuse** — Creates a new project VM on first boot; subsequent runs connect to the existing VM (or
   restart it if it stopped).
4. **Provisioning** — Merges your opencode config snippets into a single `opencode.json` in the VM home, provisions
   `home.yaml` mappings, and syncs them into the VM.
5. **Opencode** — Runs `opencode attach` inside the VM, forwarding any arguments after `--` to the AI agent.
6. **Cleanup** — On exit, the session detaches. The VM-internal worktree is managed by opencode; on subsequent runs it is reused. The host repo is untouched.

See the [Commands]({% link commands.md %}) reference for the full API and [Configuration]({% link configuration.md %}) for tuning
behavior.

### System Context

The following C4 container diagram shows how opencode-sandbox relates the host to the microsandbox VM: your project
directory is bound into the VM as `/workspace`, persistent state lives on a home volume at `/home/dev`, secrets are
injected host-side only, and one or more opencode clients attach to the server running inside the VM.

![opencode-sandbox C4 container diagram]({% link diagrams/c4-container.svg %})

`/workspace` and the host CWD are the same files — edits inside the VM appear on the host and vice-versa. Secrets never live in
the image or in environment dumps inside the VM; they are injected at runtime and visible only as environment variables.
Multiple clients can attach to the same VM concurrently.

## Agent Context (AGENTS.md)

An `AGENTS.md` in the working directory can orient the agent on being in a sandbox VM, available tooling etc. Here is a minimal, self-explanatory example you can copy and adapt:

```dotenv
## Environment

You are running inside a sandbox VM, not on the host. Filesystem layout:

- `/workspace` bind mount of the host CWD, mounted rw.
- `~/.local/share/opencode/worktree/` git worktrees of `/workspace`, created by opencode

## Toolchain

Common CLI tools are preinstalled: git, node, npm, jq, yq etc. Don't install additional tools yourself without permission.

No SSH keys in the VM, git cmds against remotes won't work.
```

## Example: Permissions

Opencode permissions are configured through opencode config snippets, which opencode-sandbox merges (user-first, then
project; see [Configuration]({% link configuration.md %}#opencode-configuration)). Place a snippet in your project, e.g.
`.opencode-sandbox/opencode/permission.json5`.

**Quasi-auto:** allow everything except what is explicitly denied:

```json5
{
  // .opencode-sandbox/opencode/permission.json5
  permission: {
    "*": "allow",
  },
}
```

**Protect secrets:** deny reads of `.env` and `.envrc` files:

```json5
{
  // .opencode-sandbox/opencode/permission.json5
  permission: {
    denylist: [
      { tool: "read", files: [".env", ".envrc"] },
    ],
  },
}
```

> **Caveat:** these rules are advisory for opencode's own Q&A tools. The `bash` tool executes arbitrary commands inside
> the VM and can read any file regardless of these deny rules, so they are not a security boundary — keep secrets out of
> the VM or rely on the [secret mechanism]({% link configuration.md %}#secrets) instead.

## Next Steps

- Read the [Commands]({% link commands.md %}) reference for the complete CLI API.
- Learn about [Configuration]({% link configuration.md %}) for setting defaults and secrets.
- See how to [Extend the Runner Image]({% link runner-image.md %}) with project-specific tooling.
- Explore [Worktree Sessions]({% link branch-sessions.md %}) for isolated worktree sessions.
- Browse [Recipes]({% link recipes.md %}) for hands-on workflows like connecting Opencode Desktop.
- Check [Troubleshooting]({% link troubleshooting.md %}) for common issues.
