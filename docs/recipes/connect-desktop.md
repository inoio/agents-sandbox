---
title: Connect Opencode Desktop
layout: default
parent: Recipes
nav_order: 10
---
# Connect Opencode Desktop

Serve opencode to the host and attach the Opencode Desktop client.

opencode-sandbox runs a headless `opencode serve` daemon inside the VM, and the
CLI is just one client that attaches to it. Because that server is
network-reachable, any opencode client — including Opencode Desktop — can attach
to the same VM, so Desktop connects to the sandbox with no extra plumbing.
`run --serve-only` (or `-s`) starts the project VM with the opencode port
published on the host, prints the URL for Opencode Desktop, and stays running
(no in-VM TUI) until you press `Ctrl-D`:

```console
opencode-sandbox run --serve-only
```

Connect Opencode Desktop to the printed `http://127.0.0.1:<port>` URL — the
port is dynamically allocated, so always use the URL opencode-sandbox prints.
The host port is bound only to the host loopback (never exposed on the LAN);
the in-VM `opencode serve` daemon listens on all interfaces so the published
port is reachable. To add basic auth, set `OPENCODE_SERVER_PASSWORD` (and
optionally `OPENCODE_SERVER_USERNAME`) in the project or user env before
starting.

## Why the project must live at `/workspace`

An opencode server is bound to the directory it starts in — that directory
becomes its project scope. opencode currently offers no way to re-point a
running server at a different path: `serve` has no working-directory flag (it
uses the server's startup directory), and while `attach`/`run` accept `--dir`,
that path is resolved on the client side and does not relocate the server's
project. So opencode-sandbox always runs the server with the project mounted at
`/workspace`, and any client — TUI or Desktop — must address that same path.

## Connecting Opencode Desktop to the sandbox

1. Start the sandbox with `opencode-sandbox run --serve-only`.
2. In Opencode Desktop: **File → Settings → Servers → Add server** (defaults).
3. On the host (as root), symlink the directory you want to work in to
   `/workspace`.
4. **New project** for that server, pointing at `/workspace` — the path
   Opencode Desktop sends must match the in-VM path (`/workspace`).
