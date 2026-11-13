---
title: Worktree Sessions
description: "Start isolated agent sessions in git worktrees created inside the sandbox. Each worktree registers in the host checkout's .git directory."
layout: default
parent: Recipes
nav_order: 10
---
# Worktree Sessions

Run the agent in its own git worktree, created inside the sandbox. The worktree files live in the VM; the host checkout's `.git` directory gains a `.git/worktrees/<name>` registration.

By default, agents-sandbox runs in the current directory (`/workspace`). To start an isolated development session in its own worktree, use `--worktree <name>[:<base>]` (`-w`). The daemon creates an isolated git worktree **inside the VM**; because `/workspace` includes the checkout's `.git`, the worktree's registration also appears in the host `.git/worktrees/` directory.

> **Warning:** Each worktree registers in the host checkout's `.git/worktrees/` directory, and its `gitdir` file points at guest paths. Host `git worktree list` shows these entries as prunable, but do not run `git worktree prune` on the host while a worktree session is active — it would orphan the guest worktree and its commits. To clean up, remove the worktree from a `shell` session inside the VM first; the host registration disappears with it.

> **Note:** Requires a daemon-based agent. Worktrees are created through the agent daemon's worktree API, so `--worktree` only
> works with daemon-based agents (opencode, opencode2); it is rejected for `pi`, `claude-code`, `codex`, `hermes`, and `oh-my-pi`, which run through the
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
2. The daemon creates or reuses a worktree inside the VM under the guest home volume (`~/.local/share/opencode/worktree/`); for `opencode2` (v2) the exact layout is `~/.local/share/opencode/worktree/<short-project-id>/<name>` (the id is a truncated project hash). Because the checkout's `.git` is mounted into the VM, the worktree also registers on the host under `.git/worktrees/<name>`.
3. The launcher attaches the agent with the worktree directory as its working directory — `opencode attach` takes it via `--dir`, while `opencode2` takes it as a positional argument. This is where the agent reads source files and writes edits.
4. The host repository's working files are not modified, but the host `.git` is not left untouched: it holds the `.git/worktrees/<name>` registration from step 2. Host `git worktree list` shows the entry as prunable, but a host `git worktree prune` while the guest worktree exists would orphan it and its commits.

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