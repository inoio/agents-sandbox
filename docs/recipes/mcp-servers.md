---
title: MCP servers in the sandbox
layout: default
parent: Recipes
nav_order: 20
---
# MCP servers in the sandbox

Local (`stdio`) MCP servers declared in your agent config also start **inside the VM** — a Debian Linux environment with its own filesystem and toolchain, not your host. A server that works on the host can therefore still fail in the sandbox. Four rules cover most cases.

## 1. Never reference host-absolute paths

`/Users/<you>/...` (macOS) or `/home/<you>/...` (Linux) do not exist in the VM; the VM home is `/home/dev`. The agent config directory is mirrored **1:1 under the VM home**, so a file at `~/.config/opencode/bin/tool` on the host is at `~/.config/opencode/bin/tool` in the VM as well — only `$HOME` differs. Reference it in a way that resolves on both:

```jsonc
// opencode.jsonc — works on the host and in the VM
"command": ["sh", "-c", "exec bash \"$HOME/.config/opencode/bin/tool\""]
```

> **Exec bit:** executables copied over by the host-config drop-in may arrive without the exec bit (see [#69](https://github.com/inoio/agents-sandbox/issues/69)). Launching through the interpreter (as above) sidesteps this entirely.

## 2. Install the server's toolchain in the VM image

Launcher binaries are not copied from the host — and could not run there anyway (different OS, often different architecture). A macOS `uvx`/`npx`/native module does not exist in a `linux-arm64` VM. Add the Linux equivalents to your `.agents-sandbox/Dockerfile` (see [Runner Image]({% link runner-image.md %})) instead:

```dockerfile
USER root
# uv/uvx for Python-based MCP servers
RUN curl -LsSf https://astral.sh/uv/install.sh \
    | env UV_INSTALL_DIR=/usr/local/bin INSTALLER_NO_MODIFY_PATH=1 sh
# example: Chromium runtime for a browser-automation MCP server
RUN apt-get update \
 && apt-get install -y --no-install-recommends chromium fonts-liberation fonts-dejavu-core \
 && rm -rf /var/lib/apt/lists/*
```

Have launcher scripts resolve tools via `PATH` rather than a hard-coded per-user location: `UVX="$(command -v uvx || echo "$HOME/.local/bin/uvx")"`.

## 3. Keep host-integrated servers on the host

MCP servers that drive the host itself — USB/adb device control, the OS keychain, a service on `localhost` — have no counterpart inside the hardware-isolated VM. Disable them in the VM-side snippet config rather than trying to make them work.

## 4. Share session state via `mounts`

Servers that keep login sessions or browser profiles (e.g. `mcp-server-linkedin` under `~/.linkedin-mcp`) can reuse the host's state by mounting that directory (see [Host mounts]({% link configuration/mounts.md %})). Writable mounts let sandbox processes modify host files, so mount only directories whose contents the sandboxed tools may change; host and VM should not drive the same state directory concurrently:

```yaml
# ~/.config/agents-sandbox/config.yaml
mounts:
  /home/dev/.linkedin-mcp: ~/.linkedin-mcp
```
