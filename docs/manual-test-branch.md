# Manual testing guide: `--branch` clone-based sessions

Run these from the project root. Each test assumes Docker + microsandbox runtime
are available (`opencode-msb doctor` passes).

## Prerequisites

```bash
go run ./cmd/opencode-msb doctor
```

## 1. No `--branch` (baseline)

```bash
go run ./cmd/opencode-msb
```

Expected: runs in the current directory. No managed clone created. After exit,
no cleanup prompt.

## 2. `--branch` matching current branch

```bash
go run ./cmd/opencode-msb --branch $(git branch --show-current)
```

Expected: uses the current directory. No managed clone created.

## 3. `--branch` for an existing branch

```bash
git branch existing-test 2>/dev/null || true
go run ./cmd/opencode-msb --branch existing-test
```

Expected:
- Creates a managed clone under `~/.local/share/opencode-msb/worktrees/<project>/existing-test`
- Mounts the clone as `/workspace` in the VM
- `git status` works inside the VM (this was the bug that prompted the clone fix)
- After exit, prompts: keep / remove / merge
- Choose "remove" → clone directory gone, branch still exists

## 4. `--branch` for a new branch (interactive)

```bash
go run ./cmd/opencode-msb --branch totally-new-branch
```

Expected:
- Prompt: `Branch 'totally-new-branch' does not exist. Create it?`
- After "yes", prompt: `Base ref for new branch 'totally-new-branch'` (default `HEAD`)
- Creates clone and branch

## 5. `--yes` with a new branch

```bash
go run ./cmd/opencode-msb --yes --branch another-new-branch
```

Expected:
- No prompts
- Branch created from HEAD, clone created
- After session, clone removed by default, branch kept
- Default actions logged to stderr

## 6. Uncommitted changes — default keep

```bash
go run ./cmd/opencode-msb --yes --branch dirty-branch
# inside the VM, make a change but don't commit, then exit
```

Expected:
- Cleanup detects uncommitted changes
- Default "keep" chosen (warning logged)
- Clone remains with changes intact

Verify:
```bash
ls ~/.local/share/opencode-msb/worktrees/*/<project>/dirty-branch
git -C ~/.local/share/opencode-msb/worktrees/*/<project>/dirty-branch status
```

## 7. Uncommitted changes — interactive discard + remove

```bash
go run ./cmd/opencode-msb --branch dirty-branch-2
# inside the VM, make a change but don't commit, then exit
```

Expected:
- Prompt for uncommitted changes → choose "d" (discard)
- Prompt for cleanup → choose "r" (remove)
- Clone removed, branch kept

## 8. Merge-back success

```bash
go run ./cmd/opencode-msb --branch merge-test
# inside the VM, make and commit a change, then exit
```

Expected:
- Prompt for cleanup → choose "m" (merge)
- Default target is the original CWD branch
- Clone's branch merged into target, clone removed
- Original checkout back on its starting branch

Verify:
```bash
git log --oneline -3   # should show the merged commit
```

## 9. Merge conflict

Set up conflicting changes on both branches first:

```bash
git checkout -b conflict-target
echo "host change" > conflict-file.txt
git add conflict-file.txt && git commit -m "host change"
git checkout -        # back to original branch

go run ./cmd/opencode-msb --branch conflict-target
# inside the VM, edit conflict-file.txt with different content, commit, then exit
```

Expected:
- Prompt for cleanup → choose "m" (merge)
- Merge fails, `git merge --abort` runs
- Original checkout restored to starting branch
- Clone removed
- Error: branch was not merged

## 10. Branch slug collision

```bash
git branch feature/foo
git branch feature-foo
go run ./cmd/opencode-msb --yes --branch feature/foo
go run ./cmd/opencode-msb --yes --branch feature-foo
```

Expected: two distinct managed clone directories, no accidental deletion.

## 11. `--branch` outside a git repo

```bash
cd /tmp
go run /workspace/cmd/opencode-msb --branch foo
```

Expected: clear error that `--branch` requires a git repository.

## Cleanup between tests

```bash
rm -rf ~/.local/share/opencode-msb/worktrees
git branch -D existing-test totally-new-branch another-new-branch \
  dirty-branch dirty-branch-2 merge-test conflict-target \
  feature/foo feature-foo 2>/dev/null || true
```
