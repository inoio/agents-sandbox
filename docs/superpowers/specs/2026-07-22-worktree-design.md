# Branch-session design

## Goal

Allow `opencode-msb` to run opencode in an isolated session for a branch that is
different from the user's current checkout, while keeping the implementation
simple and VM-compatible.

## User interface

The launcher accepts a `--branch <branch>` flag. If omitted, the launcher uses the
current directory and its current branch.

```bash
opencode-msb --branch my-feature
```

## Managed directories

When `--branch` names a branch other than the current checkout, the launcher
creates or reuses an **independent git clone** under
`~/.local/share/opencode-msb/worktrees/<project>/<branch>`.

Each managed directory is a standalone git repository:

- It has its own `.git` directory.
- It works both on the host and inside the microsandbox VM.
- No absolute host paths are required inside the VM.

If the requested branch already exists in the original repository, the clone is
checked out on that branch. If it does not exist, the user is prompted whether
to create it (or `--yes` creates it from `HEAD`), and an optional base ref can
be supplied.

## Lifecycle

1. **Resolve workspace**
   - Determine the target branch.
   - If it matches the current checkout, use the current directory.
   - Otherwise call `git clone` into the managed directory and check out the
     branch.

2. **Run session**
   - Mount the managed directory as `/workspace` in the VM.
   - opencode runs inside the VM against the clone.

3. **Cleanup** (only when the launcher created the clone)
   - If there are uncommitted changes, prompt to keep, commit, or discard.
   - Then prompt to keep the clone, remove it, or merge the branch back into a
     target branch in the original repository.
   - Removing the clone simply deletes the directory.
   - Merging uses `git pull <clonePath> <branch>` from the original repository.

## Defaults with `--yes`

- Branch creation: yes, from `HEAD`.
- Uncommitted changes: keep.
- Final action: remove the managed clone and keep the branch.

## Concurrent sessions

Because managed directories are independent clones, multiple `--branch` sessions
for the same project can run concurrently without interfering with each other or
with the user's main working tree.
