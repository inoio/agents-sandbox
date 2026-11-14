# Branch and worktree cleanup

`scripts/cleanup-merged-branches.sh` (run as `make cleanup-branches ARGS=...`) removes
merged worktrees and local/origin branches, then reports everything left, fresh entries in
green and stale ones in yellow. It is repo-maintenance tooling, not part of `make check`; its
fixture test is.

## Merge detection

A branch or worktree is removed when it is provable that every change it carries is
already in a target (`origin/main` or any `origin/release/*`), regardless of how it got
there. Three checks, in order:

- the ref is an ancestor of a target (fast-forward / merge-commit);
- the ref's tip equals the head commit of a merged PR (`gh pr list`) — catches squash and
  rebase merges;
- the ref is patch-equivalent via `git cherry` — catches rebase merges offline. `git
  cherry` ignores merge commits, so the patch check is trusted only when every merge
  commit in `target..ref` is a clean auto-merge: re-merging its parents (`git merge-tree
  --write-tree`) reproduces its own tree. A merge that carries a manual conflict
  resolution leaves the ref in place.

Detached worktrees have no branch name, so they are tested by their HEAD commit against
the same three checks, and the PR check matches the tip alone. Origin branches use the
same proof, not ancestry and PR alone.

The PR check requires the tip to equal the PR head commit. Matching by branch name
alone is unsafe: a branch can gain commits after its PR merges, and deleting it would
discard them.

## Config-worktree protection

The bare-checkout recipe (`docs/recipes/bare-worktrees.md`) keeps project config in a sparse
`config/` worktree of the `agents-sandbox-config` branch, which mirrors `main` and takes no
commits of its own. Once `main` moves past it, it is an ancestor of `main` — it always looks
merged — so a naive sweep would remove the worktree and delete the branch. The script protects
it two ways:

- structurally: it reads the bare root's `.agents-sandbox/` symlinks and resolves each to the
  worktree directory it bridges config from, so the config worktree is recognised by what it
  is, whatever its directory or branch name, for both bare layouts (`<project>/.git` and
  `<name>.git`) and for absolute or relative symlink targets;
- by reserved name: a worktree whose directory is `config` and the branch
  `agents-sandbox-config` are kept even when no symlink resolves — the names the recipe already
  reserves, covering the structural check's failure mode.

Both checks sit with the `release/*` check, ahead of the age (`--older-than`), dirty/`--force`,
and `--remote-only` checks, so a protected worktree is kept regardless of age or dirtiness. A
protected worktree's branch is protected too, so the local and origin plans keep the mirror
branch. Kept entries carry the note `config mirror; protected` in the remaining inventory.

## Reporting

Whenever there is something to remove, a text run prints the named plan — `Worktrees to
remove:`, `Local branches to delete:`, `Origin branches to delete:` — before the confirmation
gate, so the prompt is approved against the exact names, not a count. `--quiet` prints that plan and the closing
summary but omits the remaining inventory. The plan states intent, not outcome: a removal that
fails on a non-dry run is still named under its plan heading — the error goes to stderr — while
the closing summary, which counts successes only, excludes it.

A kept entry that could not be proven contained carries the reason in its note: how many
of its commits are absent from `origin/main` (`<n> commit(s) not in origin/main`, or
`unmerged` when none are), and, when `gh` links the branch, the PR context (`merged PR
#N, branch moved since`, or `PR #N closed, not merged`). A kept detached worktree
prefixes `detached;`.

## Safety

- Prunable worktrees (a registered worktree whose directory is missing here) are never
  touched: the directory may still exist on the host while absent in the VM.
- Dirty worktrees require `--force`; long-lived refs (`main`, `release/*`) and the current
  branch/worktree are never removed.
- A worktree whose branch has no commits of its own (tip is an ancestor of `main`) is kept
  when its tip is the local default branch or newer, or when the worktree was created within
  `--fresh` (default 30m). This protects a just-created session worktree; `--force` does not
  override it. When the creation time cannot be established (no reflog, no birth time), the
  worktree is kept rather than assumed old.
- Noninteractive runs (stdin or stdout not a terminal) abort unless `--yes` is passed, so
  redirecting or piping the output cannot silently delete.
- `--dry-run` skips the remote refresh (`git fetch --prune`, `git remote set-head`), leaving
  remote-tracking refs untouched; the plan may therefore be stale.
- Origin branches are deleted with `--force-with-lease` pinned to the tip seen at plan time,
  so a push that lands in between fails the deletion instead of discarding it.
- Run in a host session with no agents active. The script only protects its own working
  directory, so it cannot see a worktree another host or agent is using.
