---
title: Alternatives
layout: default
nav_order: 15
---

# Alternatives

**Snapshot from 3 October 2026.** This page compares local tools for running
coding agents in a sandbox. It doesn't hand out a security score, and there's
no single best tool. These projects move fast, so check the upstream docs
before you decide. Hosted sandbox services are out of scope.

For agents-sandbox, the page describes v0.5.0 including
[PR #98](https://github.com/inoio/agents-sandbox/pull/98) and
[PR #103](https://github.com/inoio/agents-sandbox/pull/103): egress is denied
by default and host agent config is no longer copied into the VM by default.

## Overview

Sorted roughly by GitHub stars. The order is not a ranking.

| Tool | Stars | Boundary | Platforms | Egress default | Secrets | Workspace default | Coding agents | License |
| --- | ---: | --- | --- | --- | --- | --- | --- | --- |
| [NVIDIA OpenShell](#nvidia-openshell) | ~14k | Container + Landlock/seccomp; microVM opt-in (experimental) | Linux; macOS via Docker Desktop; WSL 2 (experimental) | Deny; L7 policy | Placeholder; supervisor injects | Inside the sandbox; host mounts opt-in | Claude Code, Copilot CLI, OpenCode; Codex with custom policy | Apache-2.0 |
| [smolvm](#smolvm) | ~6.5k | libkrun microVM | Linux, macOS, Windows | Off; host allowlist | Placeholder; host substitutes headers | n/a (runtime) | None built in | Apache-2.0 |
| [sandbox-runtime](#anthropic-sandbox-runtime-and-claude-code) | ~5.4k | Process (Seatbelt, bubblewrap + seccomp) | Linux, macOS; Windows alpha | Deny; domain proxy | No injection by default | Host directory; sensitive files protected | Claude Code; any CLI via `srt` | Apache-2.0 |
| [nono](#nono) | ~4.3k | Process (Landlock, Seatbelt) | Linux, macOS, WSL 2 | Deny; proxy allowlist | Phantom token; proxy injects | Host directory, policy-scoped | Claude Code, Codex, Pi, Copilot, OpenCode, more | Apache-2.0 |
| [container-use](#dagger-container-use) | ~4k | Container per agent (not a security boundary) | Where Dagger runs | Not documented | Not documented | Container + Git branch per agent | Any MCP-capable agent | Apache-2.0 |
| [Docker Sandboxes](#docker-sandboxes) | n/a | MicroVM | macOS (Apple Silicon), Windows 11, Linux with KVM | TCP per rule; UDP off | Proxy injects AI-service keys | Host directory read-write; `--clone` optional | Claude Code, Codex, Copilot, Gemini, OpenCode, more | Proprietary; account required |
| **[agents-sandbox](#agents-sandbox)** | ~21 | libkrun microVM (microsandbox) | Linux (KVM), macOS (Apple Silicon) | Deny; profiles and allow rules | Placeholder for `env.secret`; host config not copied | Host directory read-write; `--worktree` for opencode/opencode2 | opencode, opencode2, pi, Claude Code | GPL-3.0 |

Docker Sandboxes has no public repository, so there's no star count. It is
still one of the closest alternatives to agents-sandbox.

[microsandbox](https://microsandbox.dev/) is the VM runtime underneath
agents-sandbox. Its boundary, network and secret behavior are covered in the
agents-sandbox section; it's listed under
[baselines and building blocks](#baselines-and-building-blocks) for direct use.

## Choosing a tool

Start with the questions that rule tools out. What's left usually comes down
to workflow and taste. The [threat model](#threat-model) below explains the
risks behind these questions.

**1. Is a shared host kernel acceptable?**
The agent runs whatever it decides to run: package installs, test suites,
scripts it found in an issue. With a process or container sandbox, that code
talks to your host kernel, and a kernel or sandbox-filter bug is a host
compromise. A microVM puts a guest kernel in between.

- If that matters to you, you're down to agents-sandbox, Docker Sandboxes,
  OpenShell with its experimental microVM driver, or smolvm or microsandbox if
  you build the agent integration yourself.
- If you mainly want to stop accidents and obvious exfiltration, a process
  sandbox (sandbox-runtime, nono) is lighter: instant start, no image, no
  RAM reserved per VM.

**2. Which platform?**

- Windows: Docker Sandboxes, smolvm, nono and OpenShell via WSL 2, or the
  sandbox-runtime alpha. agents-sandbox doesn't run on Windows.
- Intel Macs: agents-sandbox and Docker Sandboxes need Apple Silicon.

**3. Which agent?**
Check the agents column in the overview. agents-sandbox ships profiles for
opencode, opencode2, pi and Claude Code. For Codex, Copilot or Gemini, look at
Docker Sandboxes, nono or OpenShell. Without a profile you can still run any
CLI in most of these tools, but you set up config and credentials yourself.

**4. How do credentials get to the agent?**
The useful question is not "does the agent see the token" but "where can the
token be used".

- agents-sandbox, smolvm, Docker Sandboxes, OpenShell and nono give the agent
  a placeholder and insert the real value on the way out.
- agents-sandbox and smolvm bind a secret to hosts. Docker Sandboxes does this
  for supported AI services.
- OpenShell and nono go further: a credential can be limited to a binary or
  tool, an HTTP method and a path. That's the only way to stop a GitHub token
  from writing to repositories outside the task.
- sandbox-runtime has no credential proxy by default; the agent holds the
  real token.

**5. May the agent write to your checkout directly?**
A read-write checkout means the agent can change Git hooks, build scripts or
editor config that later run on your host, outside any sandbox.

- Isolated by default: OpenShell (workspace inside the sandbox) and
  container-use (container plus branch per agent).
- Opt-in: Docker Sandboxes with `--clone`, agents-sandbox with `--worktree`
  (opencode and opencode2 only).
- sandbox-runtime keeps the checkout writable but protects `.git/hooks/`,
  `.git/config`, shell and IDE config.

**6. Open source, no account, no telemetry?**
That rules out Docker Sandboxes. OpenShell sends telemetry until you turn it
off.

**7. How much do you want to run?**
agents-sandbox, nono, smolvm and sandbox-runtime are a single CLI. Docker
Sandboxes needs Docker's tooling and a login. OpenShell brings a gateway,
supervisor, images and policies, which pays off when you manage many agents
or need audit logs.

### Typical situations

| Situation | Good fit | Why | Watch out for |
| --- | --- | --- | --- |
| Laptop, Claude Code, want protection with little overhead | sandbox-runtime | No VM, protects hooks and config files | Shared kernel; turn on fail-closed, or Claude Code runs commands unsandboxed when the sandbox can't start |
| Several agents, fine-grained credential rules, no VM | nono | Per-tool policies, method/path rules, keychain and password-manager integration | Shared kernel; pre-1.0 with several advisories in 2026 |
| Guest kernel, open source, no account; opencode, pi or Claude Code | agents-sandbox | MicroVM per project, egress denied, host-bound secrets, works on your checkout | Checkout writable by default; network rules are host-level only; Linux and Apple Silicon only |
| Guest kernel, many agents, Windows, closed source is fine | Docker Sandboxes | Broadest agent list, `--clone`, Docker-in-VM | Account, telemetry, Docker's terms; credential injection only for AI services |
| Team or platform, central policy and audit | OpenShell | Per-binary L7 rules, policy prover, audit events | Operational overhead; default is a container, microVM driver is experimental |
| Building sandboxing into your own tool | smolvm or microsandbox | Runtimes with network policy and credential placeholders; smolvm adds branching and Windows, microsandbox has SDKs | No agent integration |
| Several agents in parallel, each on its own branch | container-use | Reviewable branch per agent | Not a security boundary; combine with a real sandbox if that matters |

## Threat model

Assume the agent is controlled by an attacker through prompt injection: an
issue, a README in a dependency, a web page, an MCP tool response. It has the
tools, tokens and files the sandbox gives it. The question is what it can do
with them.

### Secret exfiltration through allowed channels

Most tools here keep real credentials out of the agent and add them on the way
out. That helps against accidental leaks, but an allowed endpoint is still an
open door. An agent with a GitHub token and access to `github.com` can push
your code or secrets to any repository that token can write to.

Domain filters have holes too: DNS-over-HTTPS, HTTP `CONNECT`, SOCKS hostname
mode, VPNs, domain fronting, and any service that accepts user content.
Method and path rules, per-binary rules and repository allowlists make the gap
smaller. They don't close it.

### Host compromise

A process sandbox shares the host kernel, so a kernel or filter bug reaches
the host directly. A container adds namespaces and dropped capabilities, but
still runs on the host kernel. A microVM has its own guest kernel; breaking out
needs a VMM or hypervisor bug. The VMM itself usually runs with your user's
permissions, though.

Write-back needs no bug at all. If the checkout is mounted read-write, the
agent can change Git hooks, build scripts or editor config that later run on
the host. Worktree and clone modes let you review changes first, but most tools
don't use them by default.

### Bad changes to the project

An agent can delete files, introduce subtle bugs, or make commits that look
fine. A sandbox limits where that happens, not whether it happens. You still
need review and separate branches.

### What each boundary buys you

| Boundary | Against secret exfiltration | Against host escape |
| --- | --- | --- |
| Process (Landlock, Seatbelt, bubblewrap) | Medium: proxies and network rules narrow the channel | Weak: a kernel or filter bug reaches the host |
| Container + Landlock/seccomp | Medium to strong, if policy and network fencing are set up correctly | Medium: depends on the host kernel and container runtime |
| MicroVM | Same as the others: depends on the proxy and rules in front of the VM | Strong: needs a VMM bug, but the VMM runs in your host context |

Write-back isn't in the table because it doesn't depend on the boundary type.
It depends on whether the host checkout is mounted read-write.

### Out of scope

- Prompt injection itself. No tool here stops untrusted content from
  influencing the agent.
- The model provider. Your code and prompts still go there. A sandbox controls
  where code runs, not where data goes.
- Whether the agent's work is correct.

## agents-sandbox

> **Assumption:** this section describes v0.5.0 with
> [PR #98](https://github.com/inoio/agents-sandbox/pull/98) and
> [PR #103](https://github.com/inoio/agents-sandbox/pull/103) merged, including
> safe config migration for all supported agent profiles.

agents-sandbox is a launcher on top of the microsandbox runtime. It starts one
libkrun microVM per project and agent, mounts the project at `/workspace`, and
runs opencode, opencode2, pi or Claude Code inside.

- **Boundary:** KVM on Linux, Apple's virtualization on Apple Silicon Macs.
  The guest has its own kernel and root filesystem. The VMM and runtime still
  run on the host with your permissions.
- **Guest user:** the agent runs as the non-root `dev` user; startup hooks can
  run as root. With `--dind`, the agent can use Docker inside the VM.
- **Secrets:** values from `env.secret` or `env.secret.yaml` stay on the host;
  the guest only sees placeholders. Host agent config and credential files are
  not copied by default. `provision-host-config: true` brings back the old
  copy-everything behavior. `config migrate` and the first-run guidance move an
  existing setup over: recognized credentials become placeholders, raw values
  go into the user-level secret file, and provider hosts are added to the
  allow list after you review them. Ambiguous values need manual review. None
  of this protects values in plain `env`, `home:`, mounts or project files.
- **Egress:** denied by default. Profiles and host, CIDR or suffix rules open
  it up. The policy is set when the VM is created; changing it can recreate
  the VM.
- **Workspace:** a normal session mounts the project read-write, so whatever
  the agent writes lands in your checkout. `--worktree` uses a Git worktree
  inside the VM instead; only opencode and opencode2 support it.
- **Agent state:** `/home/dev` is a persistent volume per project and agent.
  It survives VM recreation until you reset or prune it.
- **Resources:** every project and agent gets its own VM with kernel, disk and
  memory. Several projects at once need the RAM for it.
- **Maturity:** young project, small user base. Its security depends heavily
  on microsandbox and its libkrun fork.

So the agent no longer runs on your host kernel or root filesystem, but in a
normal session it still edits your checkout. The project mount, writable
mounts and the VMM process remain things to think about.

### Known limitations

- The checkout is mounted read-write by default; `--worktree` isn't available
  for pi and Claude Code.
- Network rules work on hosts, CIDRs and suffixes, not on HTTP methods or
  paths, and not per repository.
- There's no extra confinement around the VMM process on the host.

Ideas and progress are tracked in the
[issues](https://github.com/inoio/agents-sandbox/issues).

Sources (checked 3 October 2026):
[repository](https://github.com/inoio/agents-sandbox),
[releases](https://github.com/inoio/agents-sandbox/releases),
[security policy](https://github.com/inoio/agents-sandbox/blob/main/SECURITY.md),
[networking]({% link configuration/networking.md %}),
[worktree sessions]({% link branch-sessions.md %}),
[agent configuration]({% link configuration/agent.md %}).

## NVIDIA OpenShell

[OpenShell](https://github.com/NVIDIA/OpenShell) is a policy-driven runtime for
autonomous agents. It's built to manage many sandboxes, not to be a small
single-user launcher.

It has three parts:

- The **gateway** handles sandbox lifecycle, policies, providers, logs and
  access.
- The **supervisor** resolves DNS, checks policy, opens approved connections
  and injects credentials.
- The **sandbox** runs the agent and hands all network operations to the
  supervisor.

On Linux the workload runs as a non-root user without capabilities. Landlock
limits file access, and seccomp user notification routes network calls to the
supervisor. The supervisor knows which executable is calling, so rules can be
per binary. If the supervisor goes away, the agent is frozen instead of
running without controls.

### Runtimes

| Runtime | Boundary | Notes |
| --- | --- | --- |
| Docker | Container + Landlock/seccomp | Default; Docker Desktop has extra network requirements |
| Podman | Rootless container | Needs Podman 5.x and cgroups v2 |
| MicroVM | One VM per sandbox | libkrun-based; opt-in, experimental, not auto-detected |
| Kubernetes | Pod | Needs a CNI that enforces NetworkPolicy |

The microVM driver uses Apple Hypervisor on macOS and KVM on Linux. The guest
has no network device; all traffic goes through the supervisor over vsock. By
default, though, you get a hardened container, not a VM.

### Policy and credentials

Outbound traffic is denied by default. Rules can match executable,
destination, port, HTTP method, path, GraphQL operation, MCP method or tool,
and WebSocket behavior. Executables are hashed, so a changed binary can be
rejected. Private addresses and cloud metadata are blocked.

Credentials are replaced by placeholders at startup. The supervisor fills
them in only for requests whose binary, destination and credential binding are
allowed. A policy prover flags new credentialed endpoints, methods or metadata
access for review, and approved rules can be applied to a running sandbox.

The workspace lives inside the sandbox by default. Host bind mounts must be
enabled explicitly and bypass the filesystem policy. That's safer than a
read-write checkout, but working on a local checkout takes some setup.

### Platforms and caveats

Linux on amd64 and arm64, macOS on Apple Silicon via Docker Desktop, and
Windows via WSL 2 (experimental). Telemetry is on by default; set
`OPENSHELL_TELEMETRY_ENABLED=false` to turn it off. NVIDIA published security
fixes in August 2026 covering provisioning, network policy and sandbox
isolation, so stay on a current release
([bulletin](https://nvidia.custhelp.com/app/answers/detail/a_id/5872/~/security-bulletin%3A-nvidia-nemoclaw-and-openshell---august-2026)).

**Compared with agents-sandbox:** finer policy, per-binary rules, a policy
prover, audit events, and the workspace stays inside the sandbox. In exchange
it's more to run, shares the host kernel by default, and needs an image and
policy per agent. The microVM driver gets close to agents-sandbox's boundary,
but you still run the gateway and supervisor.

Sources (checked 3 October 2026):
[repository](https://github.com/NVIDIA/OpenShell),
[architecture](https://docs.nvidia.com/openshell/latest/about/architecture),
[runtimes](https://docs.nvidia.com/openshell/latest/how-it-works/sandboxes/runtimes),
[default policy](https://docs.nvidia.com/openshell/latest/how-it-works/policies/default-policy),
[network rules](https://docs.nvidia.com/openshell/latest/how-it-works/policies/network-rules),
[providers](https://docs.nvidia.com/openshell/latest/how-it-works/providers/overview),
[supported agents](https://docs.nvidia.com/openshell/latest/about/supported-agents),
[support matrix](https://docs.nvidia.com/openshell/latest/about/support-matrix).

## smolvm

[smolvm](https://github.com/smol-machines/smolvm) runs OCI images as microVMs
on Hypervisor.framework, KVM or the Windows Hypervisor Platform. It can branch
and checkpoint machines and pack them into portable files.

- **Network:** off by default. `--net` and `--allow-host` open restricted
  access. TCP and UDP work, ICMP doesn't.
- **Credentials:** `--credential NAME=ENV_VAR@HOST` gives the guest a
  placeholder; a host interceptor swaps in the real value in request headers
  for allowed hosts, optionally limited to certain HTTP methods. Credentials
  aren't stored in machine records or checkpoints. The feature is new, so test
  it with the restore and branching workflow you actually use.
- **Security model:** CLI and VMM run as your host user. The project
  recommends separate accounts or OS confinement against hostile local users.
  Releases don't have signed provenance yet.
- **Scope:** a runtime, not an agent launcher. No agent profiles, config
  provisioning or worktree workflow.

**Compared with agents-sandbox:** network off by default, Windows support,
header-only credential substitution and machine branching. No agent
integration. Both build on a libkrun fork.

Sources (checked 3 October 2026):
[repository](https://github.com/smol-machines/smolvm),
[credential substitution](https://github.com/smol-machines/smolvm/blob/main/docs/credential-substitution.md),
[security model](https://github.com/smol-machines/smolvm/blob/main/docs/security-model.md),
[releases](https://github.com/smol-machines/smolvm/releases).

## Anthropic sandbox-runtime and Claude Code

[sandbox-runtime](https://github.com/anthropics/sandbox-runtime) (`srt`) is a
process sandbox built on OS primitives. No container, no VM.

- **Boundary:** Seatbelt on macOS, bubblewrap and seccomp on Linux. The
  Windows alpha uses a separate local account and the Windows Filtering
  Platform.
- **Network:** denied by default. The process has no direct network access;
  HTTP and SOCKS5 proxies on the host enforce domain and port rules.
- **Filesystem:** reads are allowed unless denied, writes only where allowed.
  `.git/config`, `.git/hooks/`, `.gitconfig`, shell config, IDE folders and
  `.mcp.json` are write-protected by default.
- **Credentials:** no credential broker by default. Experimental TLS
  termination allows deeper filtering, but broad allowed domains are still an
  exfiltration channel.
- **Status:** beta research preview. Claude Code falls back to running
  commands unsandboxed if the sandbox can't start, unless you enable the
  fail-closed setting. Its Read, Edit and Write tools are governed separately
  from the Bash sandbox, and commands inherit the parent environment by
  default.

**Compared with agents-sandbox:** starts faster, needs no VM image, and
protects important config files out of the box. It shares the host kernel,
and Claude Code needs the fail-closed setting if a missing sandbox must never
mean an unsandboxed command.

Sources (checked 3 October 2026):
[sandbox-runtime](https://github.com/anthropics/sandbox-runtime),
[Claude Code sandboxing](https://code.claude.com/docs/en/sandboxing).

## nono

[nono](https://github.com/nolabs-ai/nono) is a process sandbox with a reverse
proxy and a policy model for agents and individual tools. It says itself that
it isn't a guest/host isolation boundary.

- **Boundary:** Landlock on Linux, Seatbelt on macOS, plus WSL 2. No daemon,
  container or VM.
- **Network:** a per-session proxy with endpoint rules that can include
  methods and paths. Link-local and other sensitive addresses are blocked after
  DNS resolution.
- **Credentials:** the agent gets a phantom token; the proxy fetches the real
  one from the OS keychain, 1Password, Bitwarden or Apple Passwords.
- **Tool policies:** `git`, `gh`, `curl` and others can get their own
  filesystem, network and credential rules, separate from the agent.
- **Limitations:** metadata calls like `stat` and `access` aren't fully
  intercepted. Pre-1.0, with several security advisories in 2026.

**Compared with agents-sandbox:** starts instantly, has more credential
sources, L7 rules, per-tool policies and agent profiles. A filter or kernel
bug reaches the host directly; in agents-sandbox the same bug stays inside the
VM unless it turns into a VM escape.

Sources (checked 3 October 2026):
[repository](https://github.com/nolabs-ai/nono),
[security model](https://nono.sh/docs/cli/internals/security-model.md),
[credential injection](https://nono.sh/docs/cli/features/credential-injection.md).

## Dagger container-use

[container-use](https://github.com/dagger/container-use) is an MCP server for
running several agents in parallel. Each agent gets a fresh Dagger container
and its own Git branch, with command history and terminal access via MCP.

It's good for parallel work and reviewable branches, but it's not a security
sandbox. The agent can still run host commands unless you restrict its tools.
Egress and secrets aren't documented as a boundary.

**Compared with agents-sandbox:** the branch-per-agent workflow is similar to
a worktree session and keeps changes off your main checkout. agents-sandbox
has the stronger execution boundary; container-use has the better workflow for
parallel agents.

Sources (checked 3 October 2026):
[repository](https://github.com/dagger/container-use).

## Docker Sandboxes

[Docker Sandboxes](https://docs.docker.com/ai/sandboxes/) is a proprietary
product and needs a Docker account. Functionally it's very close to
agents-sandbox.

- **Boundary:** each sandbox is a microVM with its own Linux kernel and its own
  Docker Engine, so Testcontainers work without exposing the host Docker
  socket.
- **Network and credentials:** outbound TCP goes through a host proxy and is
  allowed only when a rule matches; UDP is off. The proxy injects credentials
  for supported AI services.
- **Workspace:** `sbx run` mounts the current directory read-write, so the
  agent can change build scripts and Git hooks. `--clone` leaves your checkout
  alone until you fetch or push a branch.
- **Agents:** Claude Code, Codex, Copilot, Cursor, Devin, Docker Agent, Droid,
  Gemini, Kiro, OpenCode, and a plain shell.
- **Caveats:** closed source, sign-in required, usage data collected unless
  you turn it off, subject to Docker's terms.

**Compared with agents-sandbox:** currently the more complete product, if the
account, telemetry and licensing are fine for you. agents-sandbox is open
source, needs no account, and you can adapt it. Both mount the checkout
read-write unless you pick clone or worktree mode.

Sources (checked 3 October 2026):
[isolation](https://docs.docker.com/ai/sandboxes/security/isolation/),
[defaults](https://docs.docker.com/ai/sandboxes/security/defaults/),
[Git workflows](https://docs.docker.com/ai/sandboxes/workflows/git/),
[agents](https://docs.docker.com/ai/sandboxes/agents/),
[FAQ](https://docs.docker.com/ai/sandboxes/faq/),
[release repository](https://github.com/docker/sbx-releases).

## Other projects

Smaller projects in the same space. Stars as of the snapshot date.

| Project | Stars | Boundary | Notes |
| --- | ---: | --- | --- |
| [CelestoAI/SmolVM](https://github.com/CelestoAI/SmolVM) | ~770 | Firecracker, QEMU or libkrun VM | Images for Claude Code, Codex and Pi; broad network access by default |
| [matchlock](https://github.com/jingkaihe/matchlock) | ~610 | Firecracker (Linux), Virtualization.framework (macOS) | MITM proxy, allowlists, placeholder secrets; experimental |
| [tobi/wrap](https://github.com/tobi/wrap) | ~14 | microsandbox VM | Network denied except GitHub; Linux-focused |
| [wirenboard/agent-vm](https://github.com/wirenboard/agent-vm) | ~4 | libkrun VM | TLS-proxy credentials, per-launch GitHub repository allowlist |

## Baselines and building blocks

General-purpose options people often use instead. None of them ships agent
profiles, and only microsandbox has egress policy and credential placeholders
built in.

| Option | Boundary | Notes |
| --- | --- | --- |
| Running the agent directly | None | The agent has your files, credentials and network |
| [microsandbox](https://microsandbox.dev/) | libkrun microVM | The runtime under agents-sandbox; usable directly via the `msb` CLI or SDKs, with network policies and host-bound secret placeholders |
| Docker container, [Dev Containers](https://containers.dev/) | Container, host kernel | Checkout usually mounted read-write; open egress and visible secrets unless you configure otherwise |
| [gVisor](https://gvisor.dev/), [Kata Containers](https://katacontainers.io/) | User-space kernel / VM per container | Harden a container on Linux |
| [Apple container](https://github.com/apple/container) | Lightweight VM per container | macOS on Apple Silicon |
| [Lima](https://lima-vm.io/), [Colima](https://github.com/abiosoft/colima) | Full Linux VM | Usually one long-lived VM shared by everything; mounts and network are configurable |
| Built-in sandboxes of Codex CLI and Gemini CLI | Process (Seatbelt, Landlock) or container | Only cover that one agent |

## The libkrun dependency

Most open-source microVM tools here use libkrun or a fork of it:

- agents-sandbox uses the fork shipped by microsandbox;
- smolvm has its own fork;
- OpenShell's microVM driver is libkrun-based;
- `tobi/wrap` and `wirenboard/agent-vm` also build on libkrun.

matchlock and CelestoAI/SmolVM offer Firecracker instead, but they're smaller
and more experimental. Switching between libkrun-based tools may get you a
better workflow or policy model, but not a different VMM. And since the VMM
runs with your permissions, a VM escape isn't the only way things can go wrong
on the host.

## Positioning

Columns: isolation boundary. Rows: can you run a coding agent with it right
away, or do you have to build the integration yourself?

| | Process (host kernel) | Container (host kernel) | MicroVM (guest kernel) |
| --- | --- | --- | --- |
| **Ready for coding agents** | Claude Code sandbox mode, nono, Codex/Gemini CLI sandboxes | OpenShell default, container-use | **agents-sandbox**, Docker Sandboxes, OpenShell microVM driver, CelestoAI/SmolVM, matchlock |
| **Runtime or building block** | sandbox-runtime (`srt`) | Docker, Podman, gVisor | microsandbox, smolvm, Apple container, Kata Containers |

agents-sandbox sits in the top-right cell. Docker Sandboxes is the mature,
closed-source neighbor there. OpenShell only lands there with its opt-in
microVM driver.

## Where agents-sandbox stands

There aren't many tools in the top-right cell that are open source, need no
account and work with your current checkout. agents-sandbox is one of them:
simple to run, agent profiles included, placeholder secrets, and you control
the launcher and runner image. Docker Sandboxes is more complete if its terms
work for you. OpenShell's microVM driver is open source too, but experimental
and tied to the gateway.

Most of agents-sandbox's security comes from microsandbox, so keeping it safe
means following that runtime's advisories and its libkrun fork.

## Remaining risks

These tools limit the damage a hijacked agent can do. None of them makes the
agent trustworthy.

- Every allowed endpoint can be used to exfiltrate data. A GitHub token that
  works on `github.com` may push to repositories the task never meant to touch.
- Prompt injection can still come in through issues, dependencies, web pages,
  model output and MCP responses.
- A read-write host mount lets the agent plant hooks or build steps that run
  later outside the sandbox. Clone and worktree modes make that reviewable.
- Young runtimes collect advisories quickly. A microVM doesn't mean you can
  skip updates to the VMM, kernel, proxy and host OS.
- Your code and prompts still go to the model provider. If data sovereignty
  matters, pick the provider accordingly; a sandbox only controls local
  execution.

## Contributing

Missing a tool or found something outdated? Pull requests are welcome,
especially from people who maintain or use these projects. Please:

- link a source for each claim,
- update the "checked" date in the section's sources line,
- keep the structure of the existing sections, and keep table cells short.
