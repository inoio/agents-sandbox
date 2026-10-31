---
title: Install Opencode Plugin Dependencies
description: "Make local opencode plugins, custom tools, and MCP servers that need npm packages work in the sandbox VM by installing the packages inside it."
layout: default
parent: Recipes
nav_order: 45
---

# Install Opencode Plugin Dependencies

This recipe applies to `opencode` and `opencode2` when a local plugin or custom tool in `~/.config/opencode` imports
an npm package, or an MCP server runs from `~/.config/opencode/node_modules`. Without it, those plugins load on your
machine but not in the sandbox, and the only trace is a `failed to load plugin ... Cannot find package` line in the
opencode log inside the VM.

The launcher copies your `~/.config/opencode` into the VM, but not its `node_modules/` or `package*.json` (see
[Default drop-in provisioning]({% link configuration/agent.md %}#default-drop-in-provisioning)). Copying them would
not help: your machine and the Linux VM are different platforms, so a package with native code would be built for the
wrong one, and a copy goes stale silently after you change the packages on your machine. opencode does not install the
packages of a local plugin either. Instead, you install them inside the VM, where they are built for Linux and kept in
the sandbox's home volume.

While the packages are missing, `agents-sandbox run` warns:

```text
/Users/you/.config/opencode/package.json declares dependencies, but node_modules is not copied into the VM ...
```

## Declare every import

The VM install only knows the packages listed in `~/.config/opencode/package.json`. A package your plugin imports but
the file does not list is missing in the VM, even when it happens to be installed on your machine. The warning cannot
detect that case, so check it yourself: every package named in an `import ... from "<package>"` line of your
plugins and tools must appear under `dependencies`.

```json
{
  "dependencies": {
    "@opencode-ai/plugin": "1.0.2",
    "zod": "4.1.8"
  }
}
```

Then create or update the lockfile on your machine, so the VM installs exactly the versions you tested:

```console
npm install --prefix ~/.config/opencode
```

This writes `~/.config/opencode/package-lock.json`. Keep it next to `package.json`; the install in
[Install the dependencies in the VM](#install-the-dependencies-in-the-vm) refuses to run without it. Run the command
again after every change to `package.json`. A lockfile that no longer matches `package.json` fails the install with
`npm ci can only install packages when your package.json and package-lock.json ... are in sync`. This happens when
another tool, such as `bun`, manages the directory and updates only its own lockfile.

## Add node to the image

The opencode images contain no node or npm. Add them in your project's `.agents-sandbox/Dockerfile`:

```dockerfile
# .agents-sandbox/Dockerfile
FROM agents-sandbox/runner-base:latest
RUN apt-get update && apt-get install -y --no-install-recommends nodejs npm && rm -rf /var/lib/apt/lists/*
```

Every project whose sandbox should load the plugins needs this. See [Runner Image]({% link runner-image.md %}) for how
the project Dockerfile is built.

## Install the dependencies in the VM

A startup hook runs a script in the VM each time the sandbox starts (see
[Home provisioning & startup hooks]({% link configuration/home-provisioning.md %})). Save this script as
`~/.config/agents-sandbox/install-opencode-deps.sh`:

```sh
#!/bin/sh
# Installs the npm packages of ~/.config/opencode inside the VM.
set -eu
cd "$HOME/.config/opencode"
if ! command -v npm >/dev/null 2>&1; then
  echo "install-opencode-deps: npm is not installed in this image, skipping" >&2
  exit 0
fi
# Reinstall only when the lockfile or the node version changed.
installed_from="node_modules/.installed-from"
wanted="$(node --version) $(sha256sum package-lock.json | cut -d ' ' -f 1)"
if [ -f "$installed_from" ] && [ "$(cat "$installed_from")" = "$wanted" ]; then
  exit 0
fi
npm ci --no-audit --no-fund
echo "$wanted" > "$installed_from"
```

Then add the package files and the hook to `~/.config/agents-sandbox/config.yaml`:

```yaml
home:
  .config/opencode/package.json: ~/.config/opencode/package.json
  .config/opencode/package-lock.json: ~/.config/opencode/package-lock.json
  .local/share/agents-sandbox/install-opencode-deps.sh:
    source: install-opencode-deps.sh
    hook: startup
```

The `home:` entries copy the package files into the VM, and the hook runs `npm ci` from them. The first start installs
the packages; later starts skip the install until you change the lockfile or the image's node version. A failed
install, for example while the npm registry is unreachable, prints a warning and the sandbox starts anyway.

Once `package.json` reaches the VM this way, the warning from `agents-sandbox run` no longer appears.

The install needs to reach `registry.npmjs.org`. The default `public` network profile allows it; with `profile: none`,
add the host to `network.egress-allow` (see [Networking]({% link configuration/networking.md %})). Without it, `npm ci`
fails with `403 Forbidden`.

## MCP servers from the config directory

An MCP server whose command points into `~/.config/opencode/node_modules` needs two more things in the VM: `node` on
the `PATH`, which [Add node to the image](#add-node-to-the-image) installs, and a path that exists in the VM. An
absolute path from your machine, such as `/Users/you/.config/opencode/node_modules/...`, does not. Write the path
with opencode's `{env:HOME}` variable instead, so it resolves to your home directory on your machine and to
`/home/dev` in the VM:

```json
{
  "mcp": {
    "mobile": {
      "type": "local",
      "command": ["node", "{env:HOME}/.config/opencode/node_modules/@mobilenext/mobile-mcp/lib/index.js"]
    }
  }
}
```

List the server's package under `dependencies` in `package.json` so the hook installs it.

## Check the result

Restart the sandbox so the hook runs:

```console
agents-sandbox stop
agents-sandbox run
```

The startup output shows the hook running `npm ci` on the first start. opencode loads plugins when a session opens
the project, so start a session before you check its log. Then open a shell in the sandbox from another terminal:

```console
agents-sandbox shell
```

In that shell, `ls ~/.config/opencode/node_modules` lists the installed packages. If a plugin still does not load,
search the opencode log for the package it cannot find, and add that package to `package.json`:

```console
grep -h 'failed to load plugin' ~/.local/share/opencode/log/*.log
```

## Mounting the packages from your machine instead

A [mount]({% link configuration/mounts.md %}) can make your machine's `node_modules` visible in the VM, and it also
silences the warning:

```yaml
mounts:
  /home/dev/node_modules:
    source: ~/.config/opencode/node_modules
    readonly: true
```

This only works when none of the packages contains native code, because the files are built for your machine, not for
the Linux VM. Prefer [Install the dependencies in the VM](#install-the-dependencies-in-the-vm).
