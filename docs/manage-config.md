---
title: Manage config in the sandbox
layout: default
nav_order: 40
---

# Manage config in the sandbox

Own the sandbox's configuration declaratively: Self-contained, reproducible, and with secrets
that are never written into the VM. Extends on **[Switch from your existing agent]({% link switch.md %})**.

> **New to coding agents?** Set up your agent (opencode, pi, or claude-code) on your host first,
> then come back here.

## 1. Turn off host-config fallback

```yaml
# ~/.config/opencode-sandbox/config.yaml
provision-host-config: false
```

## 2. Bring over your config

Copy your existing agent config into the sandbox snippet directory and adapt it:

```shell
mkdir -p ~/.config/opencode-sandbox/opencode
for f in "$HOME"/.config/opencode/* "$HOME"/.config/opencode/.[!.]*; do
  case "$(basename "$f")" in
    node_modules|package.json|bun.lock) continue ;;
  esac
  cp -R "$f" ~/.config/opencode-sandbox/opencode
done
```

(For pi / claude-code, use `~/.config/opencode-sandbox/pi/` / `.../claude/`.) Snippets matching
`opencode*.json*` are deep-merged into the VM config; see
[Agent configuration]({% link configuration/agent.md %}).

## 3. Add secrets

Deliver API keys / tokens so they never touch the VM disk — see
[Secrets]({% link configuration/secrets.md %}):

```yaml
# ~/.config/opencode-sandbox/env.secret.yaml
ANTHROPIC_API_KEY:
  value: sk-ant-xxxxxxxx
  host: provider.example
```

Then reference it in your config with `{env:ANTHROPIC_API_KEY}` (opencode) instead of the literal key.

## 4. Provision files & hooks

Map dotfiles and startup hooks into the VM home — see

## Next steps

- [Home provisioning & startup hooks]({% link configuration/home-provisioning.md %}) — Map dotfiles and startup hooks into the VM home.
- [Host mounts]({% link configuration/mounts.md %}) — additional host directories.
- [Networking]({% link configuration/networking.md %}) — egress profiles and allow/deny lists.
- [Worktree Sessions]({% link branch-sessions.md %}) — isolated sessions.
- [Configuration files & Environment variables]({% link configuration/launcher.md %})
- [Secrets]({% link configuration/secrets.md %}) — learn all about secret management.
- [Agent configuration]({% link configuration/agent.md %}) - learn all about agent configuration.
