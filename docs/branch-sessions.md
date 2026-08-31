---
title: Worktree Sessions
layout: default
nav_order: 70
---
# Worktree Sessions

By default, opencode-sandbox runs in the current directory (`/workspace`). To start an isolated development session in its own worktree, use `--worktree <name>[:<base>]` (`-w`). This creates an isolated git worktree **inside the VM**, leaving the host repository untouched.

## Basic Usage

```console
opencode-sandbox                             # run in /workspace
opencode-sandbox -w bugfix-fix-thing         # run in an isolated worktree
opencode-sandbox -w bugfix-fix-thing:main -y # worktree from local base ref
```

## How It Works

When you pass `--worktree <name>`:

1. The launcher passes the name to the opencode daemon inside the running VM via its worktree API.
2. The daemon creates or reuses a worktree inside the VM at `<opencode-data>/opencode/<name>`. The host repository is **unaffected**.
3. `opencode attach` is invoked with `--dir` pointing to the worktree directory. This is where opencode reads source files and writes edits.
4. The host repository is left untouched — no host-side worktrees are created or managed.

## Name and Base Semantics

- `--worktree <name>`: opens opencode in an isolated worktree named `<name>`, reusing it if it already exists or creating it otherwise.
- `--worktree <name>:<base>`: on a **fresh create**, starts the worktree checked out at the local base ref `<base>`; on **reuse**, the base is ignored and a warning is printed. The worktree is reset to the base ref after boot via the daemon's `startCommand`.

## Validation

- **`<name>` must be a slug**: only lowercase letters, digits, and single hyphens. Names like `feature/foo` are rejected immediately.
- **`<base>` must already exist as a local ref**: msb never fetches from remotes.
- The worktree stays on its own `opencode/<name>` branch.

## When to Use

- **Feature isolation**: Develop a feature without touching the main branch's files
- **Experimentation**: Try changes in an isolated path
- **Parallel sessions**: Run multiple branches simultaneously in different worktrees