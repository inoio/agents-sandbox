---
title: Letting agents-sandbox learn your setup
description: "Run a session that watches what your agent needed, then accept the config it proposes."
layout: default
parent: Get started
nav_order: 50
---

# Letting agents-sandbox learn your setup

Instead of declaring your providers, hosts, and secrets up front, you can let agents-sandbox learn them from one real
session: set up your agent inside the sandbox, use it for real work, and agents-sandbox proposes the configuration that
session needed.

> **Note:** A learning session observes your traffic to work out which services you reached and which keys carried the
> requests. It runs only when you ask for it, and it saves nothing without your acceptance.

## Quick reference

| I want to ... | Run |
| --- | --- |
| Learn from a session | `agents-sandbox run --learn` |
| Review a session whose process is gone | `agents-sandbox config learn` |
| Audit your configuration without a session | `agents-sandbox config harden` |

## Learn from a session

Start the agent with `--learn`:

```console
agents-sandbox run --learn
```

During the session, agents-sandbox routes the sandbox's network traffic through a proxy it owns and reads the sandbox
home back afterwards. The proxy sees which destinations the session reached and which credential each request carried.
The read-back finds the config files the session changed and the secrets in the sandbox home.

Set up your agent as you normally would — sign in to a provider, add an API key, reach a private endpoint — and use it.
When you exit, agents-sandbox reports what it learned and offers three choices:

| Choice | What it does |
| --- | --- |
| Review and save | Ask about each proposed change in turn |
| Save all | Write every proposed change without asking |
| Not now | Write nothing |

## What it proposes

Learn mode writes to your **per-slug user config** (`~/.config/agents-sandbox/<repo-slug>/`), keyed by the repository
so every clone of it shares the same settings. It proposes:

- a `network.egress-allow` set — the destinations the session used, added to the ones you already allow;
- a static secret for each credential it can attribute to a destination;
- a managed copy of each agent config file the session created or changed that the launcher merges, written to the same
  per-slug user config with each static secret value replaced by a placeholder. The value itself goes to
  `env.secret.yaml`; a file that holds only a sign-in, or a credential the launcher cannot attribute to a destination,
  stays in the sandbox.

It never removes a setting you wrote and never widens your network policy: the proposed allow set is your current set
plus only what the session reached, and a denied destination is reported but never allowed. The network policy applies
when the sandbox is created, so a proposed change to it takes effect at the next start, which may recreate the sandbox.

If agents-sandbox cannot attribute a secret to a destination, it leaves the secret in the sandbox home rather than
guessing where it is safe to send.

## Review a session later

If the process that started the learning session is gone — you closed the terminal, or the launcher was killed —
agents-sandbox records the session so you can review it later:

```console
agents-sandbox config learn
```

If the sandbox home has been recreated since, agents-sandbox reports that and discards the record rather than analysing
the new home against stale facts.

## Audit your configuration without a session

A learning session watches what your agent used; an audit needs no session at all. `config harden` reads the
configuration your next run would use and proposes the risky parts to tighten:

```console
agents-sandbox config harden
```

Use it when you want to check the configuration you already have — for example after copying a setup between
machines, or when you are not sure a secret or a network setting is scoped the way you intended. It reports what it
would change and asks about each change in turn, and it remembers a change you decline. It writes nothing
non-interactively or under `--dry-run`. See [Commands]({% link commands.md %}#config-harden) for exactly what it
audits.

## Non-interactive runs

A non-interactive run writes nothing. It prints one line naming `agents-sandbox run --learn`, so you know how to review
the proposals later.
