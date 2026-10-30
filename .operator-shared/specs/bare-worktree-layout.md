---
description: Contract for making git worktrees a contained, host-and-VM-consistent thing (issue #145): a bare checkout whose worktrees live inside it, bound as `/workspace`, plus the minimal mount rule that makes worktrees the source of truth. Covers the pattern priority order (non-bare checkout default, non-repo folder, bare-with-worktrees esoteric), the generalized bind rule (bind the smallest enclosing checkout that contains the CWD, so a worktree session always exposes its worktree), guest-CWD derivation for subdirectories, the outside-worktree case (bind its own root, run it as its own checkout, with a shared git dir that stays unmounted — #426; identity is superseded by checkout-identity.md), the deliberately convention-only folder taxonomy (recipe + skill + AGENTS.md), and non-goals.
read_if: Implementing or reviewing issue #145 (bare checkout with worktrees inside), the `/workspace` bind-source resolution, guest-CWD derivation, or the worktree-layout recipe/skill.
---

# Contained worktrees across host and VM (issue #145)

## Problem Statement

Today worktrees are a **creature of the VM**. `--worktree <name>` asks the in-VM opencode daemon to create a worktree
under `<opencode-data>/opencode/<name>` (the opencode data dir), which lives on the **home volume**, not on the
`/workspace` bind mount. The host repository never sees them.

Two consequences motivate this work:

1. **Host and VM worktrees diverge.** A host checkout of the same repository has its own worktrees (e.g. the ones
   opencode creates on the host), and neither side can see the other. Agents have pruned worktrees that were still live
   on the other side; a worktree session cannot be inspected, continued, or cleaned up from the host.
2. **Uncommitted work is invisible and can be lost.** Because worktrees sit on the home volume, wiping/recreating the
   home volume destroys them. There is no easy host-side way to see whether a worktree still has uncommitted changes
   before deleting a home volume.

The root cause is that the worktree's *source of truth* is off the bind mount. The fix is to make worktrees live where
host and VM already agree — inside the project directory that is bind-mounted at `/workspace`.

## Goals / Non-Goals

**Goals**

- Worktrees for a project are **contained in one place** that both the host and the VM see identically (the bind mount).
- A session started in a worktree that lives **inside its checkout** (the bare layout, or a non-bare worktree nested
  under the main checkout) **always exposes that worktree** under `/workspace`, and connects to the project's shared VM.
- Dropping or recreating the home volume no longer destroys worktree state, and uncommitted work is inspectable from the
  host with ordinary git.
- **Support** a bare checkout whose worktrees live inside it, while keeping the **plain non-bare checkout the default**
  and primary pattern.

**Non-goals**

- Replacing `--worktree`. It is **kept as-is** for now; this change is an experiment in the underlying layout.
- Any `asb`/`agents-sandbox` `worktree` subcommand (create/list/remove/prune). Worktrees are managed with plain `git`
  commands; `agents-sandbox` only changes *what it binds*.
- Factoring in opencode's own daemon worktrees. They land under the opencode data dir (home volume) and are explicitly
  out of scope. Verified: `POST /experimental/worktree` creates
  `~/.local/share/opencode/worktree/<projectID>/<name>`.
- Making the bare layout the recommended/default way to use the tool.

**Anti-goals** (would make this a failure even if it "works")

- Requiring users to restructure an ordinary checkout, or silently changing behavior for plain non-repo folders and
  non-bare checkouts.
- Breaking existing VM names, home volumes, or state (no migration).
- Hiding the worktree a session is in (the bug this fixes) in any case.

## Pattern Priority Order

A project directory can be one of three shapes. Support order = priority:

1. **Plain non-bare checkout** — the default and primary pattern. Behavior must be unchanged except for the subdirectory
   rule below.
2. **Non-repo folder** — no git; bind the folder as today. Unchanged.
3. **Bare checkout with worktrees inside** — esoteric, supported.

## The Mount Rule (the only code change)

The single behavioral change in the product is *which host directory is bound at `/workspace`* and *where inside it the
agent starts*. Everything else (folder names, git config, how agents behave) is convention, not code (see below).

### Bind the smallest enclosing checkout that contains the CWD

Let:

- `cwd` = the current working directory, symlinks resolved (existing `resolveWorkspaceDir`).
- `gitCommonDir` = the resolved common git directory of `cwd`'s repository (`git rev-parse --git-common-dir`), or none
  if `cwd` is not in a repository.
- `checkoutRoot` = the directory that contains the git dir and any nested worktrees: the parent of `gitCommonDir` when
  `gitCommonDir` is named `.git`, otherwise `gitCommonDir` itself. This uniformly yields the main checkout root for a
  non-bare repo and the bare root for the `<root>/.git` bare layout.
- `worktreeRoot` = the top level of the working tree containing `cwd` (`git rev-parse --show-toplevel`), empty for a
  bare repository or a non-repo folder.

Then:

| Situation | `bindRoot` (bound at `/workspace`) |
|---|---|
| Not in a repository (non-repo folder) | `cwd` (unchanged) |
| In the main checkout, at its root or a subdirectory | `checkoutRoot` (unchanged at root; **root instead of CWD** in a subdirectory) |
| In a worktree **nested inside** the checkout (`worktreeRoot` within `checkoutRoot`) | `checkoutRoot` (so all sibling worktrees stay visible) |
| In a worktree **outside** the checkout (`worktreeRoot` not within `checkoutRoot`) | `worktreeRoot` (**root instead of CWD** in a subdirectory); scoped as its own checkout (see below) |
| At the bare root itself (no working tree) | `cwd` (== `checkoutRoot`) |

For the supported cases the unifying invariant is: **`/workspace` must always expose the worktree the session is in.**
For the bare layout the bare root contains every worktree, so all sessions share one root; for a nested non-bare worktree
the main checkout contains it.

**Outside worktrees bind their own root.** When the resolved worktree sits outside its checkout, the launcher binds the
worktree itself. That leaves its `.git` pointer aimed at a shared git dir outside the mount, so `git` commands inside the
VM may not resolve the repository.

> **Superseded by [`checkout-identity.md`](checkout-identity.md) (#144).** This section originally had the launcher also
> emit a `termio` warning that an outside worktree is unsupported. That warning no longer exists: under per-checkout
> identity an outside worktree runs as its own checkout, hashing its worktree root, with its own VM and home volume. The
> unmounted shared git dir is the remaining limitation, tracked in #426 (the recipe page states it to readers).

The `git rev-parse` phrasing above describes the *facts*, not the implementation. The derivation reuses the existing
host-side, go-git-based repository inspection (`projectSlugAt` / `resolveGitDir` in `internal/git`); a host `git`
binary is not a runtime prerequisite and must not be introduced. A linked worktree's `.git` file may record the
shared git dir **relatively** (git ≥ 2.48 with `worktree.useRelativePaths true`, which this layout requires); the
derivation resolves a relative gitdir against the worktree root, so the checkout root still resolves. (The
linked-worktree recognition that pairs with this lives in `checkout-identity.md`.)

Project config (`.agents-sandbox`) resolution is **not** part of this rule: the launcher read it from the CWD, so
in a bare layout it came from the launched worktree rather than the bare root. That gap and its `core.bare`-based
fix are owned by [project-config-root.md](project-config-root.md).

The bind *source* is a VM-creation-time property and is not part of the current reconfig change detection (only the
`/workspace` quota is compared). This change therefore takes effect on **freshly created VMs**; an already-existing VM
keeps its original bind source until it is recreated or removed. Adding bind-source-change detection to reconfig is
deliberately **not** done here (decision: scope the change to fresh VMs).

### Guest working directory

The agent must start **in the subdirectory it was launched from**, expressed relative to the bound root:

```
guestCWD = "/workspace" + <cwd relative to bindRoot>
```

At the root, `guestCWD == "/workspace"` (identical to today); a `filepath.Rel` failure between `bindRoot` and `cwd`
degrades to `/workspace` too. In a subdirectory, the full repository remains visible above the agent while it works in
the subdirectory. An outside worktree binds `worktreeRoot`, so `bindRoot == worktreeRoot`: a launch from a subdirectory
yields that subdirectory's relative `guestCWD`, exactly like any other checkout, and only a launch at the worktree root
yields `guestCWD == "/workspace"`.

How `guestCWD` reaches the agent (see Background):

- **opencode / opencode2** (`run`): pass `guestCWD` as the attach target → `opencode attach … --dir <guestCWD>` /
  `opencode2 --server … <guestCWD>` (v2 takes the directory positionally; it has no `--dir`). No server change.
- **`shell`, `pi`, `claude-code`**: attach with the SDK's `WithAttachCwd(guestCWD)` (through the msb seam's
  `AttachWith`) **unconditionally** — every attach, not only in a subdirectory — which sets the PTY working
  directory for clients that ignore the attach target.
- **`--worktree`** keeps overriding the target with the daemon-created directory (unchanged).

## VM / State Identity

> **Superseded by [`checkout-identity.md`](checkout-identity.md) (#144).** Identity is split per checkout (state, VM,
> home volume, image). The "one origin, one project VM" premise, the warn-and-bind-CWD behavior for an outside
> worktree, and the "names are byte-identical / no migration" statements this section carried are no longer true; the
> slug is the checkout-scoped slug defined there. The mount rule and guest-CWD derivation above stand.

## Conventions (deliberately not code)

The folder taxonomy is **a contract that inhabiting agents and humans follow**, not something `agents-sandbox` bakes
into code. It is delivered as (1) a user-facing recipe page under `docs/` and (2) a **1:1 copy** as a skill under
`.agents/skills/` (the skill is the machine-readable half of the same contract). The recipe and the skill keep
identical bodies.

### Layout

Inside the bound bare root:

- `.git/` — the bare git dir. Never a worktree.
- `main/` — canonical checkout of the default branch, always main; the reference point and the usual `--worktree` base.
- `IDE/` — the human's worktree; the one place branch-switching is expected (uppercase so it stands out).
- one flat directory per branch, **name derived from the branch** with `/` flattened to `--`:
  `feat/foo-does-bar` → `feat--foo-does-bar`. Agent and occasional human worktrees share this namespace.

### Required git configuration

- `worktree.useRelativePaths true` (git ≥ 2.48) so each worktree's `.git` pointer file survives the host→VM path
  remap. Set it in the bare repo's config.

### Rules and reserved names

- `main`, `IDE`, `.git` are reserved; a branch that would map to a reserved name is rejected/renamed by convention.
- Flattening is **lossy**: branches `feat/foo` and a literal `feat--foo` collide to the same directory. The convention
  documents this; users avoid literal `--` branch names.

## Background: the opencode server directory model

Verified against `opencode 1.19.0` (throwaway server + `sst/opencode` source); this is why no server relocation is
needed.

- `opencode serve` (started by the launcher with no directory argument, inheriting the exec CWD `/workspace`) has **no
  server-side directory flag**. Its startup CWD is only a **fallback**.
- The server resolves a directory **per request**:
  `directory = query("directory") || header("x-opencode-directory") || process.cwd()`, then scopes the instance to it.
- The **client** sets that directory: `attach --dir X` (v1) / `opencode2 … X` (v2, positional) does a local `process.chdir(X)`
  (passing `X` through when it does not exist locally) and sends it to the server. So the attach target is the mechanism; the
  session's working directory is client-controlled, not server-predetermined.
- `Instance.provide(directory)` resolves the git **project** by the git common dir independently of the session
  directory: a nested directory resolves the same project as the repo root, and a linked worktree resolves the same
  projectID as the main repo (worktree path preserved). This is exactly what the bare-with-worktrees layout needs.
- opencode2 uses the same directory concept via a positional directory + `--server`; the shared limitation is a local
  `chdir`, which is fine here because the directory exists inside the VM.

## `--worktree` (kept)

`--worktree` is not removed. It continues to create/reuse a daemon-side worktree under the opencode data dir and to
override the attach target. This spec changes only the bind source and default guest CWD; the `--worktree` interaction
is out of scope beyond not regressing it.

## Acceptance / Verification

- Non-repo folder and non-bare checkout at the root: **byte-identical** behavior and names to today.
- Non-bare checkout launched from a subdirectory: `/workspace` is the checkout root; the agent starts in the
  subdirectory; host/VM see the same files.
- Nested non-bare worktree and bare-with-worktrees: `/workspace` is the enclosing root; all sibling worktrees are
  visible; the session reuses the project's shared VM; the agent starts in the launched worktree.
- Outside non-bare worktree: the worktree root is bound and the worktree runs as its own checkout — its own VM and home
  volume; `git` inside the VM may not resolve the repository (#426).
- Naming: VM/volume/state/image names are **unchanged** for every case (no migration); the name parsers are untouched.
- The recipe page and the skill are present and identical in content; docs link check passes.

## Settled decisions

The questions this section used to list as open were resolved during implementation:

- **Guest-CWD plumbing**: `Session` carries the guest CWD (set in `PrepareSandbox`, exposed via a `GuestCWD()`
  accessor) and `runAttach` applies it to **every** attach through the SDK's `WithAttachCwd` — not only in a
  subdirectory — so `shell`/`pi`/`claude-code` start in the right directory; opencode/opencode2 get it through
  the attach target, and `--worktree` still overrides the target.
- **Bare git dir not named `.git`**: recognized by the same rule (the `checkoutRoot` is the common git dir
  itself), not only the documented `<root>/.git` layout.
- **Fresh-VM scope**: the bind source stays a creation-time property, so reconfig does not detect a bind-source change.
  Identity is no longer "unchanged": [checkout-identity.md](checkout-identity.md) (#144) supersedes the warn-and-bind-CWD
  behavior with a per-checkout identity.
