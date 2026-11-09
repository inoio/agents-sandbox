---
name: upstream-drift
description: Check whether origin/main has moved past your branch and whether it touched the same files, before rebasing. Use when asked to check origin/main (or upstream) for updates that are relevant or potentially conflicting, when a long-lived branch runs against a fast-moving main, or immediately before a rebase.
---

# Upstream drift

`scripts/upstream-drift.sh` answers the two questions a long-lived branch keeps asking: *did
origin/main move?* and *did it touch my files?* It prints each side's commits and changed files
since their merge-base, then the overlap — the branch's likely conflict sites on the next rebase.

It never rebases and always exits 0, so it is safe to run in a loop. For the mechanics of a rebase
that is already underway, use the `rebase-status` skill instead.

```console
scripts/upstream-drift.sh
```

```
upstream drift: base=origin/main merge-base=f5a6d084d2ab
  upstream commits since merge-base: 3
  branch commits since merge-base:   12
  upstream files changed:            41
  branch files changed:              270
  files changed on both sides:       5

Likely conflict sites (changed on both sides):
  internal/sandbox/msb/testmock.go
  ...
```

- `--base <ref>` compares against another ref (default `origin/main`).
- `--repo <dir>` inspects another worktree (default the CWD); useful when your session lives in a
  worktree but you want the branch's drift.
- `--no-fetch` skips the `git fetch origin` (offline, or already fresh).
- `--quiet` prints only the counts and the overlap, dropping the per-commit lists.

Pipe it to a pager or `--quiet` when the drift is large. A non-zero overlap is the signal to rebase
soon; zero overlap means the rebase should be clean.
