---
title: Manage config in the sandbox
description: "Manage the sandbox's agent configuration declaratively and reproducibly, keeping raw secret values out of the VM."
layout: default
parent: Get started
nav_order: 40
---

# Manage config in the sandbox

Own the sandbox's configuration declaratively: self-contained, reproducible, and able to keep raw secret values out of the VM.
This extends **[Switch from your existing agent]({% link switch.md %})**.

> **Note:** If you are new to coding agents, set up your agent (opencode, opencode2, pi, claude-code, codex, hermes, or oh-my-pi) on your host first,
> then come back here.

## 1. Turn off host-config fallback

```yaml
# ~/.config/agents-sandbox/config.yaml
provision-host-config: false
```

## 2. Bring over your config

Copy your existing agent config into the sandbox snippet directory and adapt it:

```shell
mkdir -p ~/.config/agents-sandbox/opencode
for f in "$HOME"/.config/opencode/* "$HOME"/.config/opencode/.[!.]*; do
  case "$(basename "$f")" in
    node_modules|package.json|bun.lock) continue ;;
  esac
  cp -R "$f" ~/.config/agents-sandbox/opencode
done
```

(For opencode2 / pi / claude-code / codex / hermes / oh-my-pi, use `~/.config/agents-sandbox/opencode2/` / `.../pi/` / `.../claude/` / `.../codex/` / `.../hermes/` / `.../oh-my-pi/`. An
`opencode2` dir is separate from the `opencode` one; see the fallback note in
[Agent configuration]({% link configuration/agent.md %}).) Snippets matching
the agent's pattern (e.g. `opencode*.json*`) are deep-merged into the VM config; see
[Agent configuration]({% link configuration/agent.md %}).

## 3. Add secrets

Deliver API keys / tokens so they never touch the VM disk — see
[Secrets]({% link configuration/secrets.md %}):

```yaml
# ~/.config/agents-sandbox/env.secret.yaml
ANTHROPIC_API_KEY:
  value: sk-ant-xxxxxxxx
  host: provider.example
```

Then reference it in your config with `{env:ANTHROPIC_API_KEY}` (opencode) instead of the literal key.

## 4. Provision files and hooks

Map dotfiles and startup hooks into the VM home — see
[Home provisioning & startup hooks]({% link configuration/home-provisioning.md %}).

## Next steps

- [Home provisioning & startup hooks]({% link configuration/home-provisioning.md %}) — Map dotfiles and startup hooks into the VM home.
- [Host mounts]({% link configuration/mounts.md %}) — additional host directories.
- [Networking]({% link configuration/networking.md %}) — egress profiles and allow/deny lists.
- [Worktree Sessions]({% link branch-sessions.md %}) — isolated sessions.
- [Configuration files & Environment variables]({% link configuration/launcher.md %})
- [Secrets]({% link configuration/secrets.md %}) — learn all about secret management.
- [Agent configuration]({% link configuration/agent.md %}) - learn all about agent configuration.
