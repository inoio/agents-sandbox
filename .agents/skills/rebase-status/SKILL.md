---
name: rebase-status
description: Summarize a rebase in progress in one call - the step counter, unmerged paths, and conflict markers left in the working tree. Use while resolving rebase conflicts, after `git rebase --continue` reports another conflict, or whenever `git status`/`git log`/marker scans are being repeated across steps.
---

# Rebase status

`scripts/rebase-status.sh` is the one call that replaces the several `git status` / `git log` /
conflict-marker scans a conflict resolution otherwise needs after every step. It reports the rebase
step, the unmerged paths, and every file still carrying conflict markers, and is safe to run whether
or not a rebase is active.

```console
scripts/rebase-status.sh
```

```
rebase status: /workspace/refactor--22-test-doubles-out-of-production
  state: rebase in progress (step 7/8), onto f5a6d084  (refs/heads/refactor/22-...)
  unmerged paths: 2
    internal/sandbox/msb/msbtest/testmock.go
    internal/sandbox/msb/configmounts_test.go
  files with conflict markers: 1
    internal/sandbox/msb/msbtest/testmock.go
  working tree: 3 staged, 0 untracked
```

- `--repo <dir>` inspects another worktree (default the CWD).

Resolve the listed paths, `git add` them, then run `git rebase --continue`; repeat until the state
reads `no rebase in progress` and `files with conflict markers: 0`. The marker scan covers the whole
tree, so a file that kept markers after being staged is still caught.
