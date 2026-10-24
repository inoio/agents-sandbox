---
title: Bare Checkout with Worktrees Inside
description: "Keep git worktrees host-and-VM-consistent by nesting them inside a bare checkout, so the host and the agent share one contained set of worktrees."
layout: default
parent: Recipes
nav_order: 30
---
# Bare Checkout with Worktrees Inside

> **Beta** — the bare-checkout layout and its configuration rules may still change.

Put every worktree for a project in one place that the host and the sandbox VM see identically: a bare checkout whose
worktrees are subdirectories. That directory is bind-mounted at `/workspace`, so the host and the agent operate on the
same worktrees. Nothing lives on the VM's home volume, so dropping the home volume cannot lose work, and you can
inspect uncommitted changes from the host with ordinary git.

This is an opt-in pattern. A plain (non-bare) checkout is still the normal way to use agents-sandbox; this pattern
matters only when you want host/VM-consistent, contained worktrees.

## When to use it

- You switch between several worktrees and want the *same* set visible on the host and inside the agent.
- You are tired of not knowing whether a worktree still has uncommitted work before deleting a home volume.
- You want agents and humans to share one contained set of worktrees.

## When not to

- You only ever work on one branch — use an ordinary checkout.
- You do not want to change how your repository is laid out.

## Setup

Requires git ≥ 2.48 (for `worktree.useRelativePaths`).

```console
mkdir myproject
cd myproject
git clone --bare <repo-url> .git
git --git-dir=.git config worktree.useRelativePaths true
git --git-dir=.git worktree add main main
git --git-dir=.git worktree add IDE -b IDE main
```

`myproject/` is now the bare checkout root; `myproject/main` and `myproject/IDE` are worktrees inside it.

`worktree.useRelativePaths` matters because a linked worktree's `.git` pointer must survive the host-to-VM path
mapping: inside the VM the same directory is at `/workspace`, not at your host path. Relative pointers keep the
worktree usable at either path.

## Layout

```text
myproject/
├── .git/            bare git dir — never a worktree
├── main/            always checked out to main
├── IDE/             the human's worktree; switch branches here
├── feat--foo/       one directory per branch (feat/foo → feat--foo)
└── fix--bug-123/    agent or human worktrees share the same namespace
```

- Branch names map to directory names with `/` flattened to `--`: `feat/foo-does-bar` → `feat--foo-does-bar`.
- `main`, `IDE`, and `.git` are reserved names.
- Flattening is lossy: a branch `feat/foo` and a literal branch `feat--foo` both map to `feat--foo`. Avoid literal
  `--` in branch names.
- Agent worktrees and occasional human worktrees share the same flat namespace; nothing distinguishes them but who
  created them.

## Working with it

Add a worktree for a branch, from the bare root:

```console
git --git-dir=.git worktree add feat--my-work -b feat/my-work main
```

Start a session from any worktree:

```console
cd myproject/IDE
agents-sandbox
```

- agents-sandbox detects the enclosing bare checkout and binds **the whole checkout root** at `/workspace`, so the
  agent sees `main/`, `IDE/`, and every worktree at once, and starts in the worktree you launched from.
- Sessions launched from any worktree of the project connect to the **same shared project VM**, each attaching to its
  own directory.
- Project config (`.agents-sandbox/`: `Dockerfile`, `config.yaml`, `env`, `env.secret`, home sources, agent config) is
  read from the **bare root**, next to `.git` — not from the worktree you launched from. A `.agents-sandbox/` inside a
  worktree is ignored, and agents-sandbox warns and names it.
- A worktree that lives *outside* its checkout is **not supported**: agents-sandbox keeps its current behavior and warns
  that the shared git dir is not reachable inside the VM. Keep worktrees inside the checkout.

## Checking for uncommitted work

From the host, inspect each worktree directly:

```console
for wt in main IDE feat--foo; do
  echo "== $wt =="
  git -C "$wt" status -s
done
```

Because the worktrees are on the bind mount, this reports exactly the state the agent sees, and it survives deleting
the VM or its home volume.

## Compatibility

- Plain non-bare checkouts and non-repo folders behave exactly as before.
- Two things change for a bare checkout: which host directory is bound at `/workspace` (the enclosing checkout root,
  with the agent starting in the subdirectory you launched from) and where project config is read from (the bare root
  next to `.git`, not the worktree). A `.agents-sandbox/` inside a launched worktree is ignored, with a warning naming
  it.
