---
title: Worktree Sessions
description: "Start isolated agent sessions in git worktrees created inside the sandbox. Each worktree registers in the host checkout's git dir."
layout: default
parent: Recipes
nav_order: 10
---
# Worktree Sessions

Run the agent in its own git worktree, created inside the sandbox. The worktree files live in the VM; the host checkout's git dir gains a `worktrees/<name>` registration (`.git/worktrees/<name>` in a normal checkout).

By default, agents-sandbox runs in the current directory (`/workspace`). To start an isolated development session in its own worktree, use `--worktree <name>[:<base>]` (`-w`). The daemon creates an isolated git worktree **inside the VM**, but because the checkout bound at `/workspace` resolves to the host's git dir — contained in the bound root for a normal or bare checkout, and bind-mounted in separately for an outside worktree — the worktree's registration also appears in the host's git dir.

> **Warning:** Each worktree registers in the host checkout's git dir, and its `gitdir` file points at guest paths:
> `.git/worktrees/<name>` in a normal checkout; `<bare-root>/.git/worktrees/<name>` in a bare checkout whose bare root
> holds a `.git` directory; `<repo>.git/worktrees/<name>` in a bare checkout whose git dir is itself a `<repo>.git`
> directory. Host `git worktree list` shows these entries as prunable, but do not run `git worktree prune` on the host
> while any guest worktree exists, not only while a session is running — it would orphan the guest worktrees and their
> commits. To clean up, remove the worktree from a `shell` session inside the VM first; the host registration disappears
> with it.

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
2. The daemon creates or reuses a worktree inside the VM under the guest home volume (`~/.local/share/opencode/worktree/`); for `opencode2` (v2) the exact layout is `~/.local/share/opencode/worktree/<short-project-id>/<name>` (the id is a truncated project hash). The daemon's `git worktree add` runs against the checkout bound at `/workspace`, whose git dir is the host's — inside the bound root for a normal or bare checkout, and bind-mounted in separately for an outside worktree — so the worktree also registers on the host under `worktrees/<name>` in that git dir (`.git/worktrees/<name>` in a normal checkout).
3. The launcher attaches the agent with the worktree directory as its working directory — `opencode attach` takes it via `--dir`, while `opencode2` takes it as a positional argument. This is where the agent reads source files and writes edits.
4. The host repository's working files are not modified, but the host's git dir is not left untouched: it holds the `worktrees/<name>` registration from step 2 (`.git/worktrees/<name>` in a normal checkout). Host `git worktree list` shows the entry as prunable, but a host `git worktree prune` while any guest worktree exists would orphan them and their commits.

## Name and base semantics

- `--worktree <name>`: opens the agent in an isolated worktree named `<name>`, reusing it if it already exists or creating it otherwise.
- `--worktree <name>:<base>`: on a **fresh create**, bases the worktree on the local base ref `<base>`; on **reuse**, the base is ignored and a warning is printed. The base must already resolve among the repository's local refs (no fetch is performed), and it is validated before the worktree is created — an unresolvable base fails without creating anything.
- **Checkout state depends on the agent:**
  - `opencode` (v1) leaves the worktree on its own `opencode/<name>` branch; a given base is reset into it via the daemon's `startCommand`.
  - `opencode2` (v2) leaves a **detached HEAD** at the base ref (or at the source tip without a base), so run `git switch -c <branch>` to work on a branch.

## Validation

- **`<name>` must be a slug for both agents**: only lowercase letters, digits, and single hyphens. Names like `feature/foo` are rejected immediately.
- **`<base>` must already exist as a local ref**: agents-sandbox never fetches from remotes.

## Outside worktrees on the host

You can also launch a normal session from a git worktree you created on the host. A worktree that lives *outside* its
checkout binds its own root and gets its own identity; the checkout's shared git dir is mounted read-write at its host
path so `git` resolves inside the VM. That mount is refused when the shared git dir sits under `/workspace` or `/tmp`,
where mounting would shadow the managed mount's content. agents-sandbox then warns that
`git may not resolve inside the VM`, and `git` commands fail with `fatal: not a git repository`.

## When to use

- **Feature isolation**: Develop a feature without touching the main branch's files
- **Experimentation**: Try changes in an isolated path
- **Parallel sessions**: Run multiple branches simultaneously in different worktrees