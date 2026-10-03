---
title: Why?
layout: default
nav_order: 10
---

# Why?

Coding agents can inspect a codebase, execute shell commands, install tools, and access network services. That breadth is what
makes them useful, but it also makes running one directly on a developer workstation a significant trust decision: the agent
shares the host kernel and can discover whatever the host user can access.

agents-sandbox runs the agent in a microsandbox VM with a separate guest kernel and root filesystem. On Linux the VM is backed by
KVM; on macOS it uses Apple Silicon virtualization. This is a VM boundary rather than a shared-kernel process or container
boundary. It limits the guest's view of the host, while accepting the residual risk of the VM runtime and hypervisor.

## The deliberate boundary

In a normal run, the host project directory is mounted read-write at `/workspace`. The agent therefore edits the host checkout;
recreating the VM does not undo those edits. Additional host mounts and files provisioned with `home:` are also deliberate shared
surfaces. A daemon-based agent's `--worktree` mode moves the working tree inside the VM when the host checkout must remain
untouched.

The agent's `/home/dev` is backed by a persistent volume scoped to the project and agent. It can be kept across VM rebuilds or
reset separately. The VM root is replaceable, but the project checkout and writable host mounts have their own lifecycles.

## Credentials and network access

The VM boundary does not make credentials safe by itself. With `env.secret` or `env.secret.yaml`, the real value stays on the
host and the guest receives a placeholder; the microsandbox proxy can substitute the value only for an allowed, verifiable
destination. Ordinary `env`, `home:`, mounts, and files in `/workspace` are not protected by that mechanism.

Native host-agent configuration stays out of the VM by default. When an existing supported setup is detected, the first
interactive start offers a safe, reviewable migration before the VM starts. Where credentials are migrated, the VM gets
microsandbox placeholders and the raw values remain in the host-side secret file; machine-bound login state is not copied. See
[Manage config in the sandbox]({% link manage-config.md %}) for this workflow and for manual configuration.

The explicit `provision-host-config: true` option is an unsafe compatibility exception, not the default migration path. It copies
selected native files as ordinary files into the persistent VM home. For OpenCode, this may include
`~/.local/share/opencode/auth.json`, so credentials can then be present in the VM. Project `.env` files are also visible because
`/workspace` is shared; they are not hidden by the VM boundary.

Network egress is denied by default. The `network:` configuration can grant access through an explicit profile or allow list, and
`profile: none` provides deny-by-default egress with explicit allow rules. It is not a complete air gap, and network policy does
not change which files are shared with the guest.

## What this is for

Use agents-sandbox when you want VM-level isolation around local agent execution without moving your project out of the host
checkout. Use the normal run mode when direct edits are useful; use `--worktree` when the checkout itself must remain untouched.

The project is not a multi-tenant service, a guarantee that shared host paths cannot be changed, or a replacement for deciding
which files and credentials an agent should be allowed to access.
