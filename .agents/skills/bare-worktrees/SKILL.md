---
name: bare-worktrees
description: Work in a bare checkout whose git worktrees are nested inside it, so the host and the VM share one contained set of worktrees. Use when setting up or working in a repository laid out as a bare `.git` with worktree subdirectories, or when you need host/VM-consistent worktrees that survive a home-volume reset.
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
```

`myproject/` is now the bare checkout root; `myproject/main` is a live checkout of `main`. That directory's name is
yours — `main`, `IDE`, or another; what matters is that one worktree holds `main` for reference, and that it is the
checkout the project config points at (see [Project config in git](#project-config-in-git)).

`worktree.useRelativePaths` matters because a linked worktree's `.git` pointer must survive the host-to-VM path
mapping: inside the VM the same directory is at `/workspace`, not at your host path. Relative pointers keep the
worktree usable at either path.

## Layout

```text
myproject/
├── .git/               bare git dir — never a worktree
├── main/               live checkout of main; the stable reference (this name is yours)
├── config/             sparse worktree of the config mirror; only .agents-sandbox/
├── .agents-sandbox/    the bare root's project config — symlinks into config/ (see below)
├── feat--foo/          one directory per branch (feat/foo → feat--foo)
└── fix--bug-123/       agent or human worktrees share the same namespace
```

- Branch names map to directory names with `/` flattened to `--`: `feat/foo-does-bar` → `feat--foo-does-bar`.
- `.git` and the config worktree's name (`config`) are reserved. If you name the stable checkout `main` or `IDE`,
  reserve that name too.
- Flattening is lossy: a branch `feat/foo` and a literal branch `feat--foo` both map to `feat--foo`. Avoid literal
  `--` in branch names.
- Agent worktrees and occasional human worktrees share the same flat namespace; nothing distinguishes them but who
  created them.

## Project config in git

Project config (`.agents-sandbox/`: `Dockerfile`, `config.yaml`, `env`, `env.secret`, home sources, agent config) is
read from the **bare root**, not from the worktree you launch from, so one copy serves every worktree. Because the
bare root is not itself a worktree, the config is kept in git on `main` and bridged to the bare root with file-level
relative symlinks.

Commit the config on `main`, ignoring only the secret files:

```gitignore
/.agents-sandbox/env.secret
/.agents-sandbox/env.secret.yaml
```

Add a sparse config worktree and point the bare root at it:

```console
git --git-dir=.git branch agents-sandbox-config main
git --git-dir=.git worktree add config agents-sandbox-config
git -C config sparse-checkout set --no-cone '/.agents-sandbox/'

mkdir -p .agents-sandbox
ln -s ../config/.agents-sandbox/Dockerfile      .agents-sandbox/Dockerfile
ln -s ../config/.agents-sandbox/config.yaml     .agents-sandbox/config.yaml
ln -s ../config/.agents-sandbox/github-auth.sh  .agents-sandbox/github-auth.sh
ln -s ../config/.agents-sandbox/opencode2       .agents-sandbox/opencode2
```

- The symlinks are **relative**, so they resolve both on the host and at `/workspace` inside the VM, and
  **file-level**, so you can drop untracked files (a personal `env.secret`) in the same directory.
- `agents-sandbox-config` is a **mirror** of `main`: it takes no commits of its own. Keep it at main's HEAD by
  fast-forwarding it after a fetch — `git -C config merge --ff-only main` — or the bare root reads a stale config.
- If the config worktree can be the *only* checkout of `main`, skip the mirror branch: `git --git-dir=.git worktree
  add config main` and symlink into `config/` directly.

## Working with it

Add a worktree for a branch, from the bare root:

```console
git --git-dir=.git worktree add feat--my-work -b feat/my-work main
```

Start a session from any worktree:

```console
cd myproject/main
agents-sandbox
```

- agents-sandbox detects the enclosing bare checkout and binds **the whole checkout root** at `/workspace`, so the
  agent sees `main/`, `config/`, `.agents-sandbox/`, and every worktree at once, and starts in the worktree you
  launched from.
- Sessions launched from any worktree of the project connect to the **same shared project VM**, each attaching to its
  own directory.
- Project config is read from the **bare root**, next to `.git` — not from the worktree you launched from. A
  `.agents-sandbox/` inside a worktree is ignored, and agents-sandbox warns and names it. With the config tracked on
  `main`, every worktree carries one, so that warning is expected.
- A worktree that lives *outside* its checkout runs as its own checkout: agents-sandbox binds the worktree directory and
  gives it its own project VM and home volume rather than sharing the checkout's. The checkout's shared git dir sits outside
  that bind, so agents-sandbox also mounts it read-write at the same path inside the VM, where the worktree's `.git`
  file points. The sandbox can therefore write to the whole shared git dir, including the refs of every other worktree.
  If that mount is impossible — the `.git` file records a relative path (`worktree.useRelativePaths`), or the path lies
  under `/home/dev`, `/workspace`, or `/tmp` — agents-sandbox warns, and git commands inside the VM fail to resolve the
  repository.

Your IDE can attach to the stable checkout, to any worktree, or hop between them; that is a local choice and does not
change the layout.

## Checking for uncommitted work

From the host, inspect each worktree directly:

```console
for wt in main feat--foo; do
  echo "== $wt =="
  git -C "$wt" status -s
done
```

Skip the sparse `config/` worktree — it mirrors `.agents-sandbox/` and takes no work of its own. Because the remaining
worktrees are on the bind mount, the loop reports exactly the state the agent sees, and it survives deleting the VM or
its home volume.

## Compatibility

- Plain non-bare checkouts and non-repo folders behave exactly as before.
- Two things change for a bare checkout: which host directory is bound at `/workspace` (the enclosing checkout root,
  with the agent starting in the subdirectory you launched from) and where project config is read from (the bare root
  next to `.git`, not the worktree). A `.agents-sandbox/` inside a launched worktree is ignored, with a warning naming
  it.
- A bare clone into a directory named `<name>.git` (instead of `.git` inside the project dir) is recognized as a bare
  checkout too: the `<name>.git` directory is the bare root and binds at `/workspace`.
