# KVM-guest git identity and sync-back Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let the guest agent commit as the host user, and let the host pull the guest's branches back with `make kvm-sync-back`.

**Architecture:** Extend `scripts/kvm-guest.sh` in two places — `agent_provision` copies the host `~/.gitconfig` into the guest and disables signing, and a new `sync_back` subcommand runs `git fetch` over SSH against the guest's `~/agents-sandbox`. A `kvm-sync-back` make target wraps it. Both seam through the existing faked-command harness in `test/scripts`.

**Tech Stack:** Bash (`set -euo pipefail`), GNU make, `git fetch` over SSH, the Go fixture harness in `test/scripts`.

**Spec:** `.operator-shared/plans/2026-11-22-kvm-guest-agent-design.md` — its *Git transport*, *Git identity (added)*, and *Sync-back (added)* sections.

## Global Constraints

- Host-only, x86_64-only provisioner; never run in CI or in the development VM.
- `scripts/kvm-guest.sh` is `set -euo pipefail` and must pass `make shellcheck`.
- Gates are `make lint`, `make fmt`, `make shellcheck` — never `go vet`/`go fmt` directly; `make check` at the end.
- Unit tests live in `test/scripts/kvm_guest_test.go`; faked commands register in `test/scripts/main_test.go`.
- The guest keeps its own `.git` (`kvm-sync` excludes it); the guest never pushes.
- User-facing make interface: `BRANCH=<name>` and `FORCE=1` (as approved); the script's internal env var is `KVM_SYNC_BACK_FORCE`.

## Review Focus

- Host with no `~/.gitconfig` — provisioning must skip the copy and the signing override, not fail (Task 1).
- `BRANCH=<name>` naming a branch checked out here (or a sibling worktree) — git refuses; the command must surface that, not force (Task 2).
- A fetch failure in the guest (unreachable ref, no lease) — the script must exit non-zero under `set -e`, not swallow it (Task 2).
- A non-fast-forward guest branch under `BRANCH` — the default refspec has no leading `+`, so the fetch refuses (Task 2).
- A rebased/rewritten guest branch — reachable via the force-updated `kvm-guest/<name>` namespace even when the local-branch fetch refuses (Task 2).

---

### Task 1: Provision the host git identity into the guest

**Files:**
- Modify: `scripts/kvm-guest.sh` (`agent_provision()`)
- Modify: `test/scripts/kvm_guest_test.go`
- Modify: `.operator-shared/guides/kvm-agent-testing.md` (T2 note)

**Interfaces:**
- Consumes: the running guest and `require_guest_ip()`; the faked `ssh`/`rsync` handlers.
- Produces: `agent_provision` also rsyncs `$HOME/.gitconfig` to `$VM_USER@$ip:.gitconfig` (only when the host file exists) and then runs, in the guest, `git config --global commit.gpgsign false` and `git config --global tag.gpgsign false`.

- [ ] **Step 1: Write the failing test**

Add to `test/scripts/kvm_guest_test.go`:

```go
// TestKvmGuestAgentProvisionCopiesGitIdentityAndDisablesSigning pins that
// provisioning gives the guest a committable identity: the host's ~/.gitconfig,
// with commit and tag signing turned off (the host's key is not in the guest).
func TestKvmGuestAgentProvisionCopiesGitIdentityAndDisablesSigning(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	if err := os.MkdirAll(filepath.Join(home, ".config", "agents-sandbox"), 0o755); err != nil {
		t.Fatalf("create home config dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(home, ".gitconfig"),
		[]byte("[user]\n\tname = Host User\n\temail = host@example.test\n"), 0o644); err != nil {
		t.Fatalf("write host gitconfig: %v", err)
	}
	sshLog := filepath.Join(dir, "ssh-argv")
	rsyncLog := filepath.Join(dir, "rsync-argv")
	env := kvmGuestEnv(dir)
	env["HOME"] = home
	env["SSH_ARGV"] = sshLog
	env["RSYNC_ARGV"] = rsyncLog
	env["PATH"] = kvmGuestPath(t, "virsh", "ssh", "rsync")

	result := RunScript(t, dir, env, repositoryScript(t, "kvm-guest.sh"), "agent-provision")
	assertStatus(t, result, 0)

	rsyncArgv := readText(t, rsyncLog)
	assertContains(t, "rsync argv", rsyncArgv, filepath.Join(home, ".gitconfig"))
	assertContains(t, "rsync argv", rsyncArgv, "dev@192.168.122.55:.gitconfig")

	sshArgv := readText(t, sshLog)
	assertContains(t, "ssh argv", sshArgv, "commit.gpgsign false")
	assertContains(t, "ssh argv", sshArgv, "tag.gpgsign false")
}
```

Then extend the existing `TestKvmGuestAgentProvisionSyncsConfigAndMaterializes` (its HOME has no `.gitconfig`) to pin the skip:

```go
	assertNotContains(t, "rsync argv", rsyncArgv, ".gitconfig")
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test -tags microsandbox_ffi_path ./test/scripts/ -run 'TestKvmGuestAgentProvision' -count=1 -v`
Expected: `TestKvmGuestAgentProvisionCopiesGitIdentityAndDisablesSigning` FAILs — the current `agent_provision` puts no `.gitconfig` in the rsync argv. The existing test's new `assertNotContains` already passes (nothing is copied today).

- [ ] **Step 3: Implement the copy and the override in `agent_provision()`**

At the end of `agent_provision` in `scripts/kvm-guest.sh` (after the `config provision` ssh), add:

```bash
  if [ -f "$HOME/.gitconfig" ]; then
    log "syncing $HOME/.gitconfig → $VM_USER@$ip (signing disabled in the guest)"
    rsync -az "$HOME/.gitconfig" "$VM_USER@$ip:.gitconfig"
    ssh -o StrictHostKeyChecking=accept-new "$VM_USER@$ip" \
      -- "bash -lc 'git config --global commit.gpgsign false && git config --global tag.gpgsign false'"
  else
    log "no $HOME/.gitconfig on this host; the guest keeps its own git identity"
  fi
```

The copy runs after `config provision` so the explicit host config wins if the launcher's `home:` manifest also maps `.gitconfig`; the signing override follows so a copied `commit.gpgsign = true` cannot make a guest commit fail.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -tags microsandbox_ffi_path ./test/scripts/ -run 'TestKvmGuestAgentProvision' -count=1 -v`
Expected: PASS.

- [ ] **Step 5: Update the host-accept guide**

In `.operator-shared/guides/kvm-agent-testing.md`, under T2, add that provisioning also copies the host `~/.gitconfig` and disables signing in the guest, and that a host without `~/.gitconfig` keeps the guest's own identity.

- [ ] **Step 6: Gate and commit**

```bash
make shellcheck
git add scripts/kvm-guest.sh test/scripts/kvm_guest_test.go .operator-shared/guides/kvm-agent-testing.md
git commit -m "feat(kvm-guest): provision the host git identity into the guest"
```

---

### Task 2: `make kvm-sync-back` fetches the guest's branches

**Files:**
- Modify: `scripts/kvm-guest.sh` (new `sync_back()` after `sync_in()`; `usage()`; the `case` dispatch; the header usage and env-override comments)
- Modify: `Makefile` (`.PHONY`; a `kvm-sync-back` target near `kvm-agent-provision`)
- Modify: `test/scripts/kvm_guest_test.go` (faked `git`, three tests)
- Modify: `test/scripts/main_test.go` (register the `git` fake)
- Modify: `.operator-shared/guides/kvm-guest.md`, `.operator-shared/guides/kvm-agent-testing.md` (T5), `.operator-shared/catalog.md`, `CONTRIBUTING.md`, `changelog.d/823.added.md`

**Interfaces:**
- Consumes: `require_guest_ip()`, `repo_root`, `VM_USER`; the faked `git`/`virsh` handlers.
- Produces:
  - script subcommand `sync-back [branch]`; env `KVM_SYNC_BACK_FORCE` (non-empty adds `--update-head-ok`).
  - make target `kvm-sync-back` with make vars `BRANCH` (empty = all) and `FORCE` (mapped to `KVM_SYNC_BACK_FORCE`).
  - test fake `kvmGuestGitHandler` registered as `git`, logging argv to `GIT_ARGV` and honoring `GIT_EXIT`.

- [ ] **Step 1: Register the faked git and write the failing tests**

In `test/scripts/main_test.go`, add to `TestMain` (next to the other `Register` calls):

```go
	Register("git", kvmGuestGitHandler)
```

In `test/scripts/kvm_guest_test.go`, add the handler and tests:

```go
// kvmGuestGitHandler serves the `git` a script under test calls: it logs the
// invocation and succeeds, or exits with GIT_EXIT so a case can pin that a
// fetch failure propagates. It never touches a repository.
func kvmGuestGitHandler(args []string) (string, string, int) {
	recordArgv("GIT_ARGV", args)
	return "", "", gitExitStatus()
}

func gitExitStatus() int {
	if envOption("GIT_EXIT") == "" {
		return 0
	}
	return 1
}

// TestKvmGuestSyncBackFetchesEveryGuestBranchIntoTheNamespace pins the default:
// every guest branch lands under refs/remotes/kvm-guest/*, leaving local
// branches untouched.
func TestKvmGuestSyncBackFetchesEveryGuestBranchIntoTheNamespace(t *testing.T) {
	dir := t.TempDir()
	gitLog := filepath.Join(dir, "git-argv")
	env := kvmGuestEnv(dir)
	env["GIT_ARGV"] = gitLog
	env["PATH"] = kvmGuestPath(t, "virsh", "git")

	result := RunScript(t, dir, env, repositoryScript(t, "kvm-guest.sh"), "sync-back")
	assertStatus(t, result, 0)

	argv := readText(t, gitLog)
	assertContains(t, "git argv", argv, "fetch")
	assertContains(t, "git argv", argv, "--prune")
	assertContains(t, "git argv", argv, "dev@192.168.122.55:~/agents-sandbox")
	assertContains(t, "git argv", argv, "+refs/heads/*:refs/remotes/kvm-guest/*")
	assertNotContains(t, "git argv", argv, ":refs/heads/")
}

// TestKvmGuestSyncBackFetchesOneBranchIntoALocalBranch pins BRANCH=<name>:
// only that branch is fetched, into the local branch of the same name, without
// force or --update-head-ok.
func TestKvmGuestSyncBackFetchesOneBranchIntoALocalBranch(t *testing.T) {
	dir := t.TempDir()
	gitLog := filepath.Join(dir, "git-argv")
	env := kvmGuestEnv(dir)
	env["GIT_ARGV"] = gitLog
	env["PATH"] = kvmGuestPath(t, "virsh", "git")

	result := RunScript(t, dir, env, repositoryScript(t, "kvm-guest.sh"), "sync-back", "feature")
	assertStatus(t, result, 0)

	argv := readText(t, gitLog)
	assertContains(t, "git argv", argv, "dev@192.168.122.55:~/agents-sandbox")
	assertContains(t, "git argv", argv, "refs/heads/feature:refs/heads/feature")
	assertNotContains(t, "git argv", argv, "+refs/heads/feature")
	assertNotContains(t, "git argv", argv, "--prune")
	assertNotContains(t, "git argv", argv, "--update-head-ok")
}

// TestKvmGuestSyncBackForceAllowsUpdatingTheCheckedOutBranch pins FORCE=1:
// --update-head-ok lets the fetch update a branch checked out here.
func TestKvmGuestSyncBackForceAllowsUpdatingTheCheckedOutBranch(t *testing.T) {
	dir := t.TempDir()
	gitLog := filepath.Join(dir, "git-argv")
	env := kvmGuestEnv(dir)
	env["GIT_ARGV"] = gitLog
	env["KVM_SYNC_BACK_FORCE"] = "1"
	env["PATH"] = kvmGuestPath(t, "virsh", "git")

	result := RunScript(t, dir, env, repositoryScript(t, "kvm-guest.sh"), "sync-back", "feature")
	assertStatus(t, result, 0)

	argv := readText(t, gitLog)
	assertContains(t, "git argv", argv, "--update-head-ok")
	assertContains(t, "git argv", argv, "refs/heads/feature:refs/heads/feature")
}

// TestKvmGuestSyncBackPropagatesAFetchFailure pins that a failing fetch is not
// swallowed: set -e must surface git's non-zero status.
func TestKvmGuestSyncBackPropagatesAFetchFailure(t *testing.T) {
	dir := t.TempDir()
	env := kvmGuestEnv(dir)
	env["GIT_ARGV"] = filepath.Join(dir, "git-argv")
	env["GIT_EXIT"] = "1"
	env["PATH"] = kvmGuestPath(t, "virsh", "git")

	result := RunScript(t, dir, env, repositoryScript(t, "kvm-guest.sh"), "sync-back")
	if result.Status == 0 {
		t.Fatalf("sync-back exited 0 despite a failing fetch:\nstderr:\n%s", result.Stderr)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test -tags microsandbox_ffi_path ./test/scripts/ -run 'TestKvmGuestSyncBack' -count=1 -v`
Expected: FAIL — `sync-back` is an unknown subcommand (exit 2), so every assertion above fails.

- [ ] **Step 3: Implement `sync_back()`**

In `scripts/kvm-guest.sh`, after `sync_in()`, add:

```bash
# sync_back fetches the guest checkout's branches back to the host: by default
# every branch into the refs/remotes/kvm-guest/* namespace (reachable locally
# without touching any local branch), or one branch named by the argument into
# the local branch of the same name (fast-forward only; KVM_SYNC_BACK_FORCE=1
# adds --update-head-ok for a branch checked out here, where git otherwise
# refuses). The guest keeps its own .git (sync excludes it), so this fetch is
# the return path; the guest never pushes.
sync_back() {
  local ip branch
  local -a force=()
  branch="${1:-}"
  ip="$(require_guest_ip)"
  [ -z "${KVM_SYNC_BACK_FORCE:-}" ] || force=(--update-head-ok)
  if [ -z "$branch" ]; then
    log "fetching every guest branch into refs/remotes/kvm-guest/*"
    git -C "$repo_root" fetch --prune "${force[@]}" \
      "$VM_USER@$ip:~/agents-sandbox" '+refs/heads/*:refs/remotes/kvm-guest/*'
    return
  fi
  log "fetching guest branch '$branch' into local '$branch'"
  git -C "$repo_root" fetch "${force[@]}" \
    "$VM_USER@$ip:~/agents-sandbox" "refs/heads/$branch:refs/heads/$branch"
}
```

The namespace refspec keeps its leading `+` (remote-tracking refs are meant to be force-updated); the single-branch refspec has none, so a non-fast-forward refuses. `FORCE` adds only `--update-head-ok`, which unlocks a branch checked out as this checkout's HEAD but not a sibling worktree's.

- [ ] **Step 4: Wire the subcommand into the dispatcher and usage**

In `scripts/kvm-guest.sh`:
- Add to the top-of-file `Usage (on the host):` list: `./scripts/kvm-guest.sh sync-back [branch]   # fetch the guest's branches back to the host`.
- Add to the env-override list: `KVM_SYNC_BACK_FORCE=1`.
- Extend the `usage()` heredoc's first line with `sync-back [branch]` and add its description:

```
  sync-back [branch]
                    fetch the guest's branches back (all into
                    refs/remotes/kvm-guest/*, or one into a local branch)
```

- Add to the `case` dispatch, next to `agent-provision)`: `sync-back) shift; sync_back "$@" ;;`.

- [ ] **Step 5: Add the make target**

In `Makefile`, append `kvm-sync-back` to the `.PHONY` list, and add next to `kvm-agent-provision`:

```make
# Fetch the guest agent's branches back to the host. Default: every branch into
# refs/remotes/kvm-guest/*. BRANCH=<name> fetches just that branch into the
# local branch of the same name (FORCE=1 for a branch checked out here).
kvm-sync-back:
	KVM_SYNC_BACK_FORCE='$(FORCE)' scripts/kvm-guest.sh sync-back $(BRANCH)
```

- [ ] **Step 6: Run the tests to verify they pass**

Run: `go test -tags microsandbox_ffi_path ./test/scripts/ -run 'TestKvmGuest' -count=1`
Expected: PASS.

- [ ] **Step 7: Update the docs and changelog**

- `.operator-shared/guides/kvm-guest.md` ("Running an agent in the guest"): document `make kvm-sync-back` (defaults to all branches under `kvm-guest/<name>`; `BRANCH=<name>` into a local branch) and that identity comes from the copied host `~/.gitconfig`.
- `.operator-shared/guides/kvm-agent-testing.md` T5: replace the manual `git fetch dev@$ip:/home/dev/agents-sandbox <branch>` recipe with `make kvm-sync-back BRANCH=<branch>` (and note the all-branches default).
- `.operator-shared/catalog.md`: add `kvm-sync-back` to the `guides/kvm-guest.md` description.
- `CONTRIBUTING.md`: mention `make kvm-sync-back` beside the `make kvm-agent` sentence.
- `changelog.d/823.added.md`: extend the fragment to name the guest git identity and `make kvm-sync-back`.

- [ ] **Step 8: Gate and commit**

```bash
make shellcheck
make fmt
make lint
git add scripts/kvm-guest.sh Makefile test/scripts/kvm_guest_test.go test/scripts/main_test.go \
  .operator-shared/guides/kvm-guest.md .operator-shared/guides/kvm-agent-testing.md \
  .operator-shared/catalog.md CONTRIBUTING.md changelog.d/823.added.md
git commit -m "feat(kvm-guest): add make kvm-sync-back to fetch guest branches"
```

---

### Task 3: Whole-change verification

**Files:** none (verification only).

- [ ] **Step 1: Run the full gate**

Run: `make check`
Expected: every target PASS. `changelog-presence` passes because `changelog.d/823.added.md` is in the branch diff; `kvm-guest-test`, `shellcheck`, and `lint` cover the new script and tests.

- [ ] **Step 2: Confirm the manual command shapes (read-only)**

Run: `./scripts/kvm-guest.sh 2>&1 | grep -A2 sync-back`
Expected: the new `sync-back [branch]` usage line is present.
