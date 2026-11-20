---
title: Your first agent
description: "Run an agent for the first time: install agents-sandbox, start an agent in a sandbox, and see what appears."
layout: default
parent: Get started
nav_order: 25
---

# Your first agent

This page takes you from a fresh install to a coding agent running in a sandbox, with your project mounted at
`/workspace`.

On a first run, agents-sandbox asks how to set the sandbox up: learn by using it, learn from your host setup (only when
host config exists), or configure it myself — see
[Letting agents-sandbox learn your setup]({% link learn-your-setup.md %}). Host configuration is not copied into the VM
by default, so the sandbox starts OpenCode 2 without your host setup unless you choose the host-learning path or opt in
to [drop-in provisioning]({% link configuration/agent.md %}#drop-in-provisioning-opt-in). Some agents need a login inside
the sandbox.

## Install agents-sandbox

Install it with Homebrew, or download a release binary. [Installation]({% link install/installation.md %}) covers both.

## Run an agent

Change into a project and start the default agent, OpenCode 2:

```console
cd ~/my-project
agents-sandbox
```

The first run builds the runner image and creates the sandbox; later runs reuse it. To start a different agent, pass
`--agent`: `agents-sandbox --agent opencode`, `agents-sandbox --agent pi`, `agents-sandbox --agent claude-code`, `agents-sandbox --agent codex`, `agents-sandbox --agent hermes`, `agents-sandbox --agent oh-my-pi`, `agents-sandbox --agent kimi-code`, `agents-sandbox --agent qwen-code`, `agents-sandbox --agent goose`, `agents-sandbox --agent grok-build`, or `agents-sandbox --agent mistral-vibe`.

## What you see

The agent's own interface opens inside the sandbox. The project is mounted at `/workspace`, and those are the same files
as your host checkout, so edits the agent makes are visible on the host.

Detach with the agent's own exit key (for example `Ctrl-D`). The sandbox keeps running and stops on its own when idle;
to stop it manually, run `agents-sandbox stop`, passing the same `--agent` value you started with (for example
`agents-sandbox stop --agent pi`).

Next steps:

- [Switch from your existing agent]({% link switch.md %}) — bring your existing agent config and credentials into the sandbox.
- [Manage config in the sandbox]({% link manage-config.md %}) — self-contained, reproducible configuration with secrets kept on the host.
- [Sandboxes]({% link sandboxes.md %}) — how sandboxes are named, stored, and reclaimed.
