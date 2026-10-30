---
title: Connect OpenChamber
description: "Use OpenChamber's web UI with an OpenCode 2 server running in an agents-sandbox VM."
layout: default
parent: Recipes
nav_order: 50
---
# Connect OpenChamber

Use OpenChamber's web UI as a host-side client for an OpenCode 2 server in an agents-sandbox VM. OpenChamber 2 is the
OpenChamber application release line; `opencode2` is agents-sandbox's profile for the OpenCode 2 agent. Current
OpenChamber versions require OpenCode 2 (currently 2.1.0 or newer).

This recipe runs the OpenChamber CLI/server on the host and the OpenCode agent in the VM. It does not install
OpenChamber in the VM.

## Start the sandbox server

Run this from the project checkout and keep the process open:

```console
agents-sandbox run --agent opencode2 --serve-only
```

The command prints a loopback URL such as `http://127.0.0.1:41237`. Use the exact port it prints; agents-sandbox
allocates it dynamically and keeps the host binding on `127.0.0.1`.

## Start OpenChamber

Install OpenChamber's CLI/Web server on the host using its
[installation instructions](https://github.com/openchamber/openchamber/blob/main/packages/docs/content/docs/install.mdx).
The CLI requires Node.js 24.14 or newer and an installed OpenCode CLI. Then, in another host terminal, configure it to
use the sandbox's OpenCode server:

```console
export OPENCODE_HOST=http://127.0.0.1:41237 # replace with the URL the command printed
export OPENCODE_PASSWORD=opencode
export OPENCODE_SKIP_START=true
export OPENCHAMBER_UI_PASSWORD='choose-a-strong-password'
openchamber
```

Open the local URL printed by OpenChamber, usually `http://localhost:3000`. `OPENCODE_SKIP_START=true` prevents
OpenChamber from starting a second OpenCode server. The `opencode2` profile currently uses HTTP Basic Auth with
username `opencode` and password `opencode`; `OPENCODE_PASSWORD` supplies that password to OpenChamber. The UI password
is a separate credential for signing in to OpenChamber.

## Project path and host-side features

The OpenCode server runs with the project mounted at `/workspace`. Select `/workspace` as the project directory in
OpenChamber so its requests refer to the same path. On the host, `/workspace` must resolve to the checkout. If you only
need one repository, point it at the project root as shown in [One repository](#one-repository). For several repositories that should share one VM
and one network/resource policy, use a common parent directory as the project workspace.

### One repository

On Linux, create a symlink to the project root:

```console
sudo ln -s "$PWD" /workspace
```

On macOS, the root filesystem is read-only. Add a synthetic root-level symlink pointing to the checkout, then reboot
for macOS to create it. Back up `/etc/synthetic.conf` first if it already exists; keep any existing entries and add one
tab-separated line for the current checkout:

```console
printf 'workspace\t%s\n' "${PWD#/}" | sudo tee -a /etc/synthetic.conf
sudo reboot
```

After reboot, verify that `/workspace` resolves to the checkout before starting OpenChamber. The synthetic entry is
system-wide and tied to this checkout's absolute path; update it if the checkout moves or you want to use a different
project.

### Several repositories in one sandbox

Put the repositories you want to work on under a common parent directory, then start agents-sandbox from that parent.
For example:

```text
~/work/customer-x/
├── service-a/
├── service-b/
└── shared-library/
```

Start `agents-sandbox run --agent opencode2 --serve-only` from `~/work/customer-x/`. In OpenChamber, add each repository
as a separate project using its guest path: `/workspace/service-a`, `/workspace/service-b`, and
`/workspace/shared-library`. The one OpenCode 2 server can then serve requests for each repository, and OpenChamber
keeps their sessions and project settings separate. They share the same sandbox VM, home volume, resource limits, and
network policy. This is useful when several repositories belong to one customer or need the same permitted egress.

The host-side `/workspace` mapping from [One repository](#one-repository) must point to `~/work/customer-x/`, the common parent, rather than
to one repository inside it. On macOS, use the parent directory as the target in `/etc/synthetic.conf`. OpenChamber's
host-side filesystem, terminal, and preview features can then address those same subpaths.

This layout gives the agent access to every repository mounted under the parent. Use it only when those repositories
belong to the same trust boundary. Use separate agents-sandbox VMs when projects need different network policies,
credentials, or resource limits.

OpenChamber's filesystem, terminal, and preview processes run on the host. They do not inherit the VM's isolation,
installed tools, or network policy. OpenChamber file operations can therefore access the host checkout through
`/workspace`; commands launched from its terminal run on the host.

OpenChamber's project path is host-local, and agents-sandbox does not currently translate paths between host and guest.
For host-side OpenChamber, use the main checkout(s) in the shared workspace. OpenChamber-created worktrees live on the
host, while an OpenCode server in the VM cannot see them at the same paths. Conversely, agents-sandbox `--worktree`
creates worktrees inside the VM's persistent home volume, which OpenChamber's host-side Git, filesystem, terminal, and
preview features cannot see. Multi-run with isolation disabled may run several sessions against the same mounted
checkout, but they share files and can conflict; isolated multi-run requires worktrees and is not supported by this
host-side arrangement.

Using OpenChamber worktrees and isolated multi-run with consistent Git, file, terminal, and preview access requires
OpenChamber and OpenCode to run in the same VM, which is the optional in-VM integration being evaluated in
[issue #266](https://github.com/inoio/agents-sandbox/issues/266). Until that design is implemented and tested,
keep worktree and isolated multi-run features off for the host-side setup.

## Security boundary

The VM contains the OpenCode agent, not OpenChamber. OpenChamber's file browser and Git integration run on the host;
its terminal and project actions run commands as the host user and use that user's network access. The agent's edits
appear in the shared checkout. The VM's egress policy applies only to guest processes: it does not filter network
requests made by the host-side OpenChamber web server or commands OpenChamber launches for Git, terminals, and project
actions.

In this recipe, `OPENCODE_HOST` points OpenChamber at an external server, so OpenChamber's own agent-control and
scheduling tools are unavailable. The agent has no documented tool to invoke OpenChamber's host terminal or manage its
projects; its tools, MCP servers, and plugins are those available inside the VM. The UI password and host credentials
are separate from `OPENCODE_PASSWORD` and are not passed to the agent. See OpenChamber's
[Agent Control Tool documentation](https://github.com/openchamber/openchamber/blob/main/packages/docs/content/docs/agent-control-tool.mdx).

The VM is not an air gap: ingress is always allowed, and egress defaults to the `public` profile. The published
OpenCode endpoint uses the fixed `opencode:opencode` credential. Keep that endpoint on `127.0.0.1`; do not use
`--network host`, allow VM egress to host services, or pass an OpenChamber URL or credentials into the VM without
reviewing the new access path. Any process that can reach the OpenCode endpoint can control that server.

Protect the host-capable UI too: bind OpenChamber to localhost, set a strong `OPENCHAMBER_UI_PASSWORD` if allowing
remote access, and pair only trusted devices. Inspect repository project actions and worktree setup commands before
approving them; the agent may recommend actions, but cannot execute them on the host through this external-server
connection. Treat agent-provided links and instructions as untrusted. A shared-parent workspace gives both the VM agent
and host-side OpenChamber access to every repository beneath it, so use it only within one trust boundary. Additional
MCP servers, plugins, mounts, and network rules change these assumptions and need separate review. See OpenChamber's
[Security](https://github.com/openchamber/openchamber/blob/main/packages/docs/content/docs/security.mdx) and
[Environment](https://github.com/openchamber/openchamber/blob/main/packages/docs/content/docs/environment.mdx) guides.

## Remote access

OpenChamber's web server binds to localhost by default. To connect its mobile or desktop clients, follow
OpenChamber's [device pairing guide](https://github.com/openchamber/openchamber/blob/main/packages/docs/content/docs/connect-devices.mdx).
Keep the agents-sandbox OpenCode port loopback-only; remote clients should connect to OpenChamber, not directly to the
agent server. Apply the access controls from [Security boundary](#security-boundary) before allowing connections from other machines.

Both the `agents-sandbox run --serve-only` process and the OpenChamber server must remain running while you use the UI.
When finished, stop OpenChamber and press `Ctrl-D` in the agents-sandbox terminal.
