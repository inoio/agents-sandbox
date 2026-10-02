---
title: Manage config in the sandbox
layout: default
nav_order: 40
---

# Manage config in the sandbox

Own the sandbox's configuration declaratively: self-contained, reproducible, and able to keep raw secret values out of the VM.
This extends **[Switch from your existing agent]({% link switch.md %})**.

> **New to coding agents?** Set up your agent (opencode, pi, or claude-code) on your host first,
> then come back here.

## 1. Keep native host config disabled

```yaml
# ~/.config/agents-sandbox/config.yaml
# This is already the secure default; keep it explicit when overriding
# inherited configuration or cleaning up a previous opt-in.
provision-host-config: false
```

When an existing OpenCode installation is detected with native provisioning disabled, the first interactive run offers
the same safe migration automatically. You can also start it explicitly without starting a VM:

```shell
agents-sandbox config migrate
```

The migration copies supported OpenCode settings into the managed configuration, replaces recognized credential values
with `$MSB_*` placeholders, and stores the raw values in the user-level `env.secret.yaml`. It never modifies the native
OpenCode files. Provider hosts are inferred for known providers or requested interactively; an unknown host is never
allowed implicitly. Inferred hosts are added to `network.egress-allow` after confirmation. Pi and Claude Code
currently require the manual setup below.

The migration pauses for review when an auth field is not recognized as safe metadata or a credential field. Inspect the
generated auth file and submit an issue if the field should be migrated automatically. A partially applied migration is
recorded in the user state (in `~/.local/state/agents-sandbox/<agent>/config-migration.yaml`) and can be retried with `config migrate`.

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

(For pi / claude-code, use `~/.config/agents-sandbox/pi/` / `.../claude/`.) Snippets matching
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

## 4. Provision files & hooks

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
