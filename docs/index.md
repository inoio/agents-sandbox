---
title: Home
layout: home
nav_order: 0
---

# opencode-sandbox

This documentation is built from release `{{ site.data.version.release }}`.

**opencode, supercharged — safely.** Run [opencode](https://github.com/anthropics/opencode) in a near-instant, hardware-isolated VM — your project at `/workspace`, your secrets safe, your agent free to do its best work.

opencode-sandbox gives opencode a real, hardware-isolated machine to work on — full agent permissions inside a boundary that can't reach your host, and secrets the agent never gets to see.

Docker, bubblewrap, seatbelt, and bare opencode all share your kernel — a kernel bug or `sudo` is enough for an agent to reach your machine. opencode-sandbox runs a separate kernel under hypervisor isolation (KVM on Linux, Apple Silicon on macOS), so escaping takes a hypervisor-level bug: a much higher bar.

Your project is mounted at `/workspace`, read-write, so the agent works on the same files you do and edits round-trip.
Everything else on your machine — other projects, your home directory, your keys — simply isn't there, except for what
you explicitly provision into the VM's home (via `home.yaml`). Secrets are injected at runtime through the secret
mechanism as environment variables and never written into the VM, so an agent can't leak what it never possessed. Worst
case, a session is a disposable VM: wipe it, and the host is untouched.

It's also yours to shape: the VM's root is defined by a plain `Dockerfile`, so you bring your own base image and tooling like any OCI image you already use — and it's built for opencode first, with support for functionalities like worktree sessions. Just bring your own `opencode.json`. Egress and ingress stay under your control with simple profiles and allow/deny lists, from full network access to complete lockdown.

|  | Bare opencode | Bubblewrap / Seatbelt | Docker (containers) | Docker Sandboxes | **opencode-sandbox** |
|---|---|---|---|---|---|
| **Isolation boundary** | ❌ none | ⚠️ shared kernel | ⚠️ shared kernel | ✅ full VM (microVM) | **✅ full VM (hypervisor)** |
| **How hard to hide secrets?** | ❌ nearly impossible | ⚠️ complex per-project rules | ⚠️ manual per-project tweaking | ✅ built-in (proxy; login-required) | **✅ built-in mechanism** |
| **Agent edits appear in your local files instantly** | ✅ | ✅ | ✅ | ✅ rw mount (clone mode is read-only) | **✅** |
| **Failure cost vs. recovery** | ❌ high damage, hard to restore | ⚠️ potential host damage | ⚠️ potential host damage | ✅ disposable | **✅ disposable, home can persist** |
| **Ease of use** | ✅ just run it | ⚠️ craft rules | ⚠️ image + mounts | ✅ one command (Docker account login) | **✅ one command** |

> Cells give the typical story for each approach. ✅ = yes / good, ⚠️ = possible but partial / in-between, ❌ = no / poor. "Failure cost vs. recovery" weighs how much damage a rogue agent can cause against how easily you can throw the environment away and start over.

> **❓ Why not just use Docker Sandboxes?**
>
> Its microVM isolation is genuinely strong. But it's a trade: a **mandatory Docker account login** for a tool that runs locally, a **closed-source core** (VMM + policy proxy + credential injection) you're trusting as your security boundary, **org-wide controls behind a paid sales tier**, and **narrower reach** (Ubuntu 24.04+ / Apple silicon / Windows 11 only).
>
> opencode-sandbox, by contrast, is **open and account-free**, runs on **any Linux (KVM) and Apple Silicon**, and gives you **one-command disposal** — without the telemetry, login, or vendor lock-in.

- [Getting Started]({% link getting-started.md %})
- [Configuration]({% link configuration.md %})
- [Sandboxes]({% link sandboxes.md %})
- [Runner Image]({% link runner-image.md %})
- [Commands]({% link commands.md %})
- [Worktree Sessions]({% link branch-sessions.md %})
- [Recipes]({% link recipes.md %})
- [Troubleshooting]({% link troubleshooting.md %})