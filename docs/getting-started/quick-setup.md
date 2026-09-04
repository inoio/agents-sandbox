---
title: Quick Setup
layout: default
parent: Getting Started
nav_order: 30
---

# Quick Setup

Now that you have installed opencode-sandbox, Let's get things up and running.

## Quick Start

Navigate to any directory and run:

```shell
opencode-sandbox
```

This builds a first VM image (can take a while, mostly depending on your internet connection), starts a microsandbox VM and launches opencode within the VM.

If you want to work with the bundled free models, you're done. Otherwise, read on.

## Migrating existing OpenCode configuration

If you have been using opencode before, start by copying your existing opencode configuration:

```shell
mkdir -p ~/.config/opencode-sandbox/opencode
for f in "$HOME"/.config/opencode/* "$HOME"/.config/opencode/.[!.]*; do
  # ignore non-configuration files
  case "$(basename "$f")" in
    node_modules|package.json|bun.lock) continue ;;
  esac
  cp -R "$f" ~/.config/opencode-sandbox/opencode
done
```

### For providers with API key


While copying renders a working setup, you can set up authentication using opencode-sandbox's secret management. This completely hides the API keys from the VM and any agents.

Normally

Then, replace any API keys and secrets with a reference to an environment variable, e.g.:

```json
{
  "provider": {
    "...": {
      "options": {
        "apiKey": "{env:YOUR_PROVIDER_API_KEY}"
      }
    }
  }
}
```

All that's left to do is to define a secret in `~/.config/opencode-sandbox/env.secret.yaml`:

```yaml
YOUR_PROVIDER_API_KEY:
  value: <your-api-key>
  host: provider.example
```

See [Configuration]({% link configuration.md %}#secrets) for more details.

### FOr providers with oauth credentials

Map your existing `auth.json` into the VM(s) with the `home:` key in `~/.config/opencode-sandbox/config.yaml`:

```shell
mkdir -p ~/.config/opencode-sandbox
cat > ~/.config/opencode-sandbox/config.yaml <<'EOF'
home:
  .local/opencode/auth.json: ~/.local/opencode/auth.json
EOF
```

