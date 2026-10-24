---
title: Your first sandbox
description: "Take an agent from install to a running sandbox: install agents-sandbox, run an agent, and see what appears."
layout: default
parent: Get started
nav_order: 25
---

# Your first sandbox

By the end of this page you have a coding agent running inside a sandbox, with your project mounted at `/workspace`.

## Install agents-sandbox

Install it with Homebrew, or download a release binary. [Installation]({% link install/installation.md %}) covers both.

## Run an agent

Change into a project and start the default agent:

```console
cd ~/my-project
agents-sandbox
```

The first run builds the runner image and creates the sandbox; later runs reuse it. To start a specific agent, pass
`--agent`: `agents-sandbox --agent pi`, `agents-sandbox --agent opencode`, or `agents-sandbox --agent claude-code`.

## What you see

The agent's own interface opens inside the sandbox. The project is mounted at `/workspace`, and those are the same files
as your host checkout, so edits the agent makes are visible on the host.

Detach with the agent's own exit key (for example `Ctrl-D`). The sandbox keeps running and stops on its own when idle;
to stop it instantly, run `agents-sandbox stop`, passing the same `--agent` value you started with (for example
`agents-sandbox stop --agent pi`).

Next, make the sandbox the home for your real setup:

- [Switch from your existing agent]({% link switch.md %}) — bring your existing agent config and credentials into the sandbox.
- [Manage config in the sandbox]({% link manage-config.md %}) — self-contained, reproducible configuration with secrets kept on the host.
- [Sandboxes]({% link sandboxes.md %}) — how sandboxes are named, stored, and reclaimed.
