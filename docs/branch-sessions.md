---
title: Worktree Sessions
description: "Start isolated agent sessions in git worktrees created inside the sandbox, leaving the host repository untouched."
layout: default
parent: Recipes
nav_order: 10
---
# Worktree Sessions

Run the agent in its own git worktree, created inside the sandbox, so your host checkout stays untouched.

By default, agents-sandbox runs in the current directory (`/workspace`). To start an isolated development session in its own worktree, use `--worktree <name>[:<base>]` (`-w`). This creates an isolated git worktree **inside the VM**, leaving the host repository untouched.

> **Note:** Requires a daemon-based agent. Worktrees are created through the agent daemon's worktree API, so `--worktree` only
> works with daemon-based agents (opencode, opencode2); it is rejected for `pi` and `claude-code`, which run through the
> interactive TUI instead.

## Basic usage

```console
agents-sandbox                             # run in /workspace
agents-sandbox -w bugfix-fix-thing         # run in an isolated worktree
agents-sandbox -w bugfix-fix-thing:main -y # worktree from local base ref
```

## How it works

When you pass `--worktree <name>`:

1. The launcher passes the name to the agent daemon inside the running VM via its worktree API.
2. The daemon creates or reuses a worktree inside the VM under the guest home volume (`~/.local/share/opencode/worktree/`); for `opencode2` (v2) the exact layout is `~/.local/share/opencode/worktree/<short-project-id>/<name>` (the id is a truncated project hash). The host repository is **unaffected**.
3. The launcher attaches the agent with the worktree directory as its working directory — `opencode attach` takes it via `--dir`, while `opencode2` takes it as a positional argument. This is where the agent reads source files and writes edits.
4. The host repository is left untouched — no host-side worktrees are created or managed.

## Name and base semantics

- `--worktree <name>`: opens the agent in an isolated worktree named `<name>`, reusing it if it already exists or creating it otherwise.
- `--worktree <name>:<base>`: on a **fresh create**, bases the worktree on the local base ref `<base>`; on **reuse**, the base is ignored and a warning is printed. The base must already resolve among the repository's local refs (no fetch is performed), and it is validated before the worktree is created — an unresolvable base fails without creating anything.
- **Checkout state depends on the agent:**
  - `opencode` (v1) leaves the worktree on its own `opencode/<name>` branch; a given base is reset into it via the daemon's `startCommand`.
  - `opencode2` (v2) leaves a **detached HEAD** at the base ref (or at the source tip without a base), so run `git switch -c <branch>` to work on a branch.

## Validation

- **`<name>` must be a slug for both agents**: only lowercase letters, digits, and single hyphens. Names like `feature/foo` are rejected immediately.
- **`<base>` must already exist as a local ref**: agents-sandbox never fetches from remotes.

## When to use

- **Feature isolation**: Develop a feature without touching the main branch's files
- **Experimentation**: Try changes in an isolated path
- **Parallel sessions**: Run multiple branches simultaneously in different worktrees