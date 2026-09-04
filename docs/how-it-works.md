---
title: Architecture & Concepts
layout: default
nav_order: 105
---

# How it works

## How It Works

1. **Image build** — Builds a Docker image from `.agents-sandbox/Dockerfile` if present, or uses the base image. The image
   contains the selected coding agent, Node.js 26, and common CLI tools.
2. **Volume setup** — Creates a persistent home volume (managed by msb, name: `agents-sandbox-home-<project-slug>-<timestamp>`) for the
   project, preserving editor state, caches, and config across sessions.
3. **VM creation or reuse** — Creates a new project VM on first boot; subsequent runs connect to the existing VM (or
   restart it if it stopped).
4. **Provisioning** — Merges your agent's config snippets into a single config in the VM home (e.g. `opencode.jsonc`),
   provisions `home:` mappings, and syncs them into the VM.
5. **Agent** — Runs the agent's attach command (e.g. `opencode attach`) inside the VM, forwarding any arguments after
   `--` to the AI agent.
6. **Cleanup** — On exit, the session detaches. The VM-internal worktree is managed by the agent daemon; on subsequent runs it is reused. The host repo is untouched.

See the [Commands]({% link commands.md %}) reference for the full API and [Configuration]({% link configuration/index.md %}) for tuning
behavior.

## System Context

The following C4 container diagram shows how agents-sandbox relates the host to the microsandbox VM: your project
directory is bound into the VM as `/workspace`, persistent state lives on a home volume at `/home/dev`, secrets are
injected host-side only, and one or more clients attach to the server running inside the VM.

![agents-sandbox C4 container diagram]({% link diagrams/c4-container.svg %})

`/workspace` and the host CWD are the same files — edits inside the VM appear on the host and vice-versa. Secrets never live in
the image or in environment dumps inside the VM; they are injected at runtime and visible only as environment variables.
Multiple clients can attach to the same VM concurrently.
