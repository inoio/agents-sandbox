# Branch Sessions

By default, opencode-msb runs in the current directory (`/workspace`). To start an isolated development session for a different branch, use `--branch` (`-b`). This creates an isolated opencode-managed worktree **inside the VM**, leaving the host repository untouched.

## Basic Usage

```console
opencode-msb                             # run in the current branch
opencode-msb -b feature/my-feature       # run in an isolated worktree
opencode-msb -b hotfix-123 -y            # skip prompts (if any)
```

## How It Works

When you pass `--branch <name>`:

1. The launcher passes the branch name to the opencode daemon inside the running VM via its experimental worktree API.
2. The daemon creates or reuses a worktree at an internal path (e.g., `/workspace/branch-<name>`).
3. `opencode attach` is invoked with `--dir` pointing to the worktree directory. This is where opencode reads source files and writes edits.
4. Your project's directory on the host (`/workspace` on the host) is **unaffected**.

If no branch is specified, opencode runs in `/workspace` (the host project directory).

## Host Cleanup

On session exit, the launcher runs `git worktree prune` on the host repository to clean up any stale worktree entries. This is a host-side safety measure and does not affect the in-VM worktree (which is managed by opencode).

## Limitations

- Host-side git worktrees don't work reliably with microsandbox VMs. Branch sessions use the VM-internal opencode worktree mechanism instead, bypassing this limitation.
- Branch sessions require opencode's experimental workspaces feature to be enabled.
- The worktree path inside the VM is managed by opencode — you can't control the exact pathname.

## When to Use

- **Feature isolation**: Develop a feature without touching the main branch's files
- **Experimentation**: Try changes in an isolated path
- **Parallel sessions**: Run multiple branches simultaneously in different VMs
