---
description: Contract for the `/workspace` bind-source rule (#145) - bind the smallest enclosing checkout containing the CWD, so a session in a nested or bare-layout worktree always exposes its worktree, with guest-CWD derivation for subdirectories and the outside-worktree case (bind its own root, run it as its own checkout with its shared git dir mounted). The bare-checkout folder taxonomy and recipe are the tracked `bare-worktrees` skill; per-checkout identity is `../identity/checkout-identity.md`.
read_if: Changing the `/workspace` bind-source resolution, guest-CWD derivation, the worktree mount rule, or the outside-worktree shared-git-dir mount (`prepare.withSharedGitDirMount`); or diagnosing a session that hides the worktree it was launched from or a `git` command that cannot resolve inside the VM.
---

# Contained worktrees: the `/workspace` bind rule

Worktrees must live where the host and the VM already agree — inside the project directory bind-mounted at `/workspace` — so both sides see the same set, dropping or recreating the home volume cannot destroy worktree state, and uncommitted work is inspectable from the host with ordinary git. The single behavioral change is *which host directory is bound at `/workspace`* and *where inside it the agent starts*; the folder taxonomy and setup recipe are convention, delivered as the tracked `bare-worktrees` skill, not code.

## Pattern priority order

1. **Plain non-bare checkout** — the default and primary pattern. Behavior is unchanged except for the subdirectory rule below.
2. **Non-repo folder** — no git; bind the folder as today. Unchanged.
3. **Bare checkout with worktrees inside** — esoteric, supported.

Anti-goals: requiring users to restructure an ordinary checkout, silently changing behavior for plain non-repo folders and non-bare checkouts, breaking existing VM names/home volumes/state (no migration), and hiding the worktree a session is in (the bug this fixes).

## The mount rule

Let `cwd` be the current directory (symlinks resolved), `gitCommonDir` the resolved common git dir (`git rev-parse --git-common-dir`) or none, `checkoutRoot` the directory containing the git dir and any nested worktrees (the parent of `gitCommonDir` when it is named `.git`, else `gitCommonDir` itself), and `worktreeRoot` the working-tree top level (`git rev-parse --show-toplevel`), empty for a bare repo or non-repo folder.

| Situation | `bindRoot` (bound at `/workspace`) |
|---|---|
| Not in a repository (non-repo folder) | `cwd` (unchanged) |
| In the main checkout, root or subdirectory | `checkoutRoot` (unchanged at root; **root instead of CWD** in a subdirectory) |
| In a worktree **nested inside** the checkout | `checkoutRoot` (so all sibling worktrees stay visible) |
| In a worktree **outside** the checkout | `worktreeRoot` (**root instead of CWD** in a subdirectory), scoped as its own checkout |
| At the bare root itself (no working tree) | `cwd` (== `checkoutRoot`) |

Invariant: **`/workspace` must always expose the worktree the session is in.** For the bare layout the bare root contains every worktree, so all sessions share one root; for a nested non-bare worktree the main checkout contains it.

The `git rev-parse` phrasing describes the facts, not the implementation: the derivation reuses the host-side, go-git-based inspection (`projectSlugAt` / `resolveGitDir` in `internal/git`). A host `git` binary is not a runtime prerequisite and must not be introduced. A linked worktree's `.git` file may record the shared git dir relatively (git ≥ 2.48 with `worktree.useRelativePaths true`, which this layout requires); the derivation resolves a relative gitdir against the worktree root.

Project config (`.agents-sandbox`) resolution is not part of this rule: the launcher read it from the CWD, so in a bare layout it came from the launched worktree rather than the bare root. That gap and its `core.bare`-based fix are owned by `configuration/project-config-root.md`.

The bind source is a VM-creation-time property, not part of reconfig change detection (only the `/workspace` quota is compared). The change therefore takes effect on freshly created VMs; an existing VM keeps its original bind source until recreated or removed. Adding bind-source-change detection to reconfig is deliberately not done.

### Outside worktrees bind their own root

When the resolved worktree sits outside its checkout, the launcher binds the worktree itself and runs it as its own checkout (hashing its worktree root, with its own VM and home volume — `../identity/checkout-identity.md`). Its `.git` pointer aims at a shared git dir outside the mount, so the launcher also bind-mounts that git dir at the path the worktree's `.git` file records (`prepare.withSharedGitDirMount`, from `git.Identity.SharedGitDir`/`RecordedSharedGitDir`), and git resolves inside the VM.

### Guest working directory

The agent starts in the subdirectory it was launched from, expressed relative to the bound root:

```
guestCWD = "/workspace" + <cwd relative to bindRoot>
```

At the root, `guestCWD == "/workspace"`; a `filepath.Rel` failure between `bindRoot` and `cwd` degrades to `/workspace`. An outside worktree binds `worktreeRoot`, so a launch from a subdirectory yields that subdirectory's relative `guestCWD`, like any other checkout.

`Session` carries the guest CWD (set in `PrepareSandbox`, exposed via `GuestCWD()`) and `runAttach` applies it to **every** attach through the SDK's `WithAttachCwd` — not only in a subdirectory:

- **opencode / opencode2** (`run`): the guest CWD is the attach target (`opencode attach … --dir <guestCWD>` / `opencode2 --server … <guestCWD>`; v2 takes the directory positionally, it has no `--dir`). No server change.
- **`shell`, `pi`, `claude-code`**: attach with `WithAttachCwd(guestCWD)` unconditionally, which sets the PTY working directory for clients that ignore the attach target.
- **`--worktree`** keeps overriding the target with the daemon-created directory.

## Background: why no server relocation

Verified against `opencode 1.19.0`: `opencode serve` has no server-side directory flag; its startup CWD is only a fallback. The server resolves a directory per request (`directory = query("directory") || header("x-opencode-directory") || process.cwd()`), and the client sets it (`attach --dir X` / `opencode2 … X` does a local `process.chdir` and sends it). `Instance.provide` resolves the git project by the git common dir independently of the session directory, so a nested directory resolves the same project as the repo root and a linked worktree resolves the same projectID as the main repo. opencode2 uses the same directory concept via a positional directory + `--server`.

## `--worktree` (kept)

`--worktree` is not removed. It continues to create/reuse a daemon-side worktree under the opencode data dir (home volume, explicitly out of scope) and to override the attach target. This contract changes only the bind source and default guest CWD.

## Acceptance

- Non-repo folder and non-bare checkout at the root: byte-identical behavior and names.
- Non-bare checkout from a subdirectory: `/workspace` is the checkout root; the agent starts in the subdirectory; host and VM see the same files.
- Nested non-bare worktree and bare-with-worktrees: `/workspace` is the enclosing root; sibling worktrees are visible; the session reuses the project's shared VM; the agent starts in the launched worktree.
- Outside non-bare worktree: the worktree root is bound and runs as its own checkout (own VM and home volume) with its shared git dir mounted.
- Naming: VM/volume/state/image names are unchanged for the non-bare and bare-layout cases; the outside-worktree case gets its own checkout identity. Name parsers are untouched.

Bare git dir not named `.git` is recognized by the same rule (the `checkoutRoot` is the common git dir itself), not only the `<root>/.git` layout.
