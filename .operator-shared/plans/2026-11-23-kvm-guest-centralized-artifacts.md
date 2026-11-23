# KVM-guest centralized artifacts + branch lock — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Stop `scripts/kvm-guest.sh` writing a guest disk/seed/XML into every worktree, and make the single shared guest a branch-locked resource with an explicit `pause` hand-off.

**Architecture:** Two independent changes to the host-only provisioner. (1) `ARTIFACT_DIR` defaults to a fixed, host-global, AppArmor-safe path under the libvirt images tree instead of `$PWD/.kvm-guest`, so every worktree resolves to the same guest. (2) The domain state becomes the lock — `running` is held, `paused`/absent is free — with a small owner label naming the holding branch and a new `pause` subcommand (`virsh suspend`) as the cheap, disk-preserving hand-off.

**Tech Stack:** Bash (`scripts/kvm-guest.sh`), the fixture harness in `test/scripts` (faked `virsh`/`ssh`/`rsync`, no hypervisor), GNU Make.

**Spec:** the approved design is the "Design (approved)" section below; this plan travels with it.

## Rebased onto the host-git-remote work

This branch was rebased onto main's host-git-remote work (PR #843), which rewrote `sync` and changed two things this
plan assumed. The task steps below are the original, pre-rebase record; the shipped code differs as follows:

- **`sync` is a host `git pull`, not an rsync of the checkout.** Main replaced `sync_in` with
  `ensure_guest_git_remote` + `git fetch origin --prune` / `git checkout` / `git merge --ff-only` in the guest. The
  `ARTIFACT_DIR` exclude-anchoring fix and its two exclude tests were dropped with the rsync path; `sync_in` just
  prepends `acquire_guest` (after `require_safe_branch`).
- **`guest_state` fails closed.** It proves an undefined domain from `virsh list --all --name` (not a failed
  `virsh dominfo`, which read a mis-permitted/unreachable libvirt as `absent`), and a failed state query `die`s so
  `pause`/`up` never drop the owner label for a guest that is merely out of view.
- **The paused arm mirrors the shut-off arm.** After `virsh resume`, `acquire_guest` waits for the DHCP lease and probes
  guest egress, rather than returning immediately after resuming.
- **The lock key is `lock_branch`.** An explicit `REPO_BRANCH` wins, else the live branch; a detached checkout prints
  nothing and `acquire_guest` refuses, rather than keying on the `main` fallback and colliding with a real main checkout.

## Design (approved)

- **Central artifacts.** `ARTIFACT_DIR` defaults to `$KVM_IMAGE_CACHE/guest` (`/var/lib/libvirt/images/agents-sandbox/guest`). `KVM_IMAGE_CACHE` is defined first; the env override still wins. The `guest/` subdir keeps the live disk distinct from the cached base image inside the same libvirt-allowed tree that AppArmor already permits. `VM_NAME` stays `asb-kvm` — one shared domain.
- **State-derived lock.** The domain state *is* the lock; a label file only names the owner:
  - `running` → held. `running` + this branch = the owner label → proceed; another branch → refuse, naming the owner; no owner label (a pre-lock guest) → refuse, asking for a hand-off.
  - `paused` → free; `acquire` resumes and re-labels to the current branch.
  - `shut off` / `absent` → free; `acquire` starts/creates.
  - Release is explicit: `pause` (`virsh suspend` + drop the label) or `down`. Because the held state is `running`, a dropped SSH that leaves the tmux agent alive stays locked — the gap a `flock` on the launching process would leave.
- **Guarded subcommands** (`acquire_guest`): `up`, `sync`, `prime`, `run`, `agent`, `agent-up`, `agent-provision`. **Unguarded:** `ssh`, `sync-back`, `render`, `firewall`. `pause` and `down` release.

## Global Constraints

- `scripts/kvm-guest.sh` is **host-only**, x86_64-only, never run by CI or the dev VM (it has no working `/dev/kvm`); every test uses the faked `virsh`/`ssh`/`rsync` in `test/scripts` and never touches a hypervisor.
- The script keeps `set -euo pipefail` and its `log`/`die` helpers; no new runtime dependency beyond coreutils/`virsh`/`flock`-free logic.
- Follow the repo gates: `make shellcheck`, `make fmt`, `make lint`, `make check` (which includes `make brain-check`).

## Review Focus

The inputs/conditions the design implies but no task's tests naturally hit, worth pinning:

- A `running` guest with **no owner label** (created before this lock) → must refuse, never silently adopt.
- A **different worktree on the same branch** → allowed (the branch is the key, not the checkout path).
- `pause` on an already-**paused** or **absent** guest → idempotent success, not an error.
- `down` when the domain is undefined but an owner label remains → clears the label.
- An **unexpected `virsh domstate`** value (`pmsuspended`, `crashed`, …) → refuse with the state named, never mis-lock.

Each of these gets its test in the owning task below.

## File Structure

- Modify: `scripts/kvm-guest.sh` — defaults, header/usage, owner helpers, `acquire_guest`, `suspend_guest`, dispatch, `destroy`.
- Modify: `test/scripts/kvm_guest_test.go` — new fixtures and cases.
- Modify: `Makefile` — `kvm-pause` target and `.PHONY`.
- Modify: `.operator-shared/guides/kvm-guest.md` — artifact location and lock/hand-off.
- Modify: `.operator-shared/catalog.md` — plan entry and the guide entry's anchors.
- Create: `changelog.d/830.changed.md`.

---

### Task 1: Centralize the guest artifacts

**Files:**
- Modify: `scripts/kvm-guest.sh` (the defaults block at the top; the header env list)
- Modify: `.operator-shared/guides/kvm-guest.md` (the `ARTIFACT_DIR` default sentences)
- Test: `test/scripts/kvm_guest_test.go`

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces: `KVM_IMAGE_CACHE` is defined before `ARTIFACT_DIR`; `ARTIFACT_DIR` default is `$KVM_IMAGE_CACHE/guest`; a new `OWNER_PATH="$ARTIFACT_DIR/$VM_NAME.owner"` next to the other `*_PATH` definitions (used by Task 2).

- [ ] **Step 1: Write the failing test**

Add to `test/scripts/kvm_guest_test.go`, after `TestKvmGuestRenderDefaultsTheImageCacheToLibvirtDir`:

```go
// TestKvmGuestRenderDefaultsArtifactsUnderTheImageCache pins that the guest's
// disk, seed, and XML default under the image cache (a fixed, host-global,
// AppArmor-safe tree) rather than $PWD, so worktrees do not each grow a copy.
func TestKvmGuestRenderDefaultsArtifactsUnderTheImageCache(t *testing.T) {
	dir := t.TempDir()
	cacheDir := filepath.Join(dir, "image-cache")
	env := kvmGuestEnv(dir)
	env["KVM_IMAGE_CACHE"] = cacheDir
	delete(env, "ARTIFACT_DIR") // let the script derive its default

	result := RunScript(t, dir, env, repositoryScript(t, "kvm-guest.sh"), "render")
	assertStatus(t, result, 0)

	rendered := filepath.Join(cacheDir, "guest", "asb-kvm-test-user-data")
	assertContains(t, "render stdout", result.Stdout, "rendered:\n"+rendered)
	if _, err := os.Stat(rendered); err != nil {
		t.Fatalf("artifacts not written under the image cache: %v", err)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./test/scripts/ -run 'TestKvmGuestRenderDefaultsArtifactsUnderTheImageCache' -count=1`
Expected: FAIL — the script still defaults `ARTIFACT_DIR` to `$PWD/.kvm-guest`, so the rendered path is `dir/asb-kvm-test-user-data`, not `dir/image-cache/guest/asb-kvm-test-user-data`.

- [ ] **Step 3: Implement the centralized default**

In `scripts/kvm-guest.sh`, reorder the two lines so the cache is defined first and the artifact dir derives from it:

```bash
KVM_IMAGE_CACHE="${KVM_IMAGE_CACHE:-/var/lib/libvirt/images/agents-sandbox}"
# Guest artifacts are host-global, not per-worktree: one guest is shared by every
# worktree, so its disk, seed, and XML live beside the base-image cache in the
# libvirt images tree (AppArmor-safe) rather than in $PWD.
ARTIFACT_DIR="${ARTIFACT_DIR:-$KVM_IMAGE_CACHE/guest}"
```

Then add the owner-label path next to the other artifact paths (below `DOMAIN_XML_PATH`):

```bash
OWNER_PATH="$ARTIFACT_DIR/$VM_NAME.owner"
```

Update the header env-override line from
`#   VM_USER=dev  SSH_PUBKEY=<path-or-key>  ARTIFACT_DIR=$PWD/.kvm-guest`
to
`#   VM_USER=dev  SSH_PUBKEY=<path-or-key>  ARTIFACT_DIR=$KVM_IMAGE_CACHE/guest`.

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./test/scripts/ -run 'TestKvmGuestRender|TestKvmGuestSync' -count=1`
Expected: PASS — the new default test and the existing render/sync tests (which set `ARTIFACT_DIR` explicitly) all pass.

- [ ] **Step 5: Update the guide**

In `.operator-shared/guides/kvm-guest.md`:
- Change the `render` sentence's default from `` `./.kvm-guest` `` to `` `$KVM_IMAGE_CACHE/guest` `` (the artifacts side of `ARTIFACT_DIR`).
- In the base-image-cache paragraph, note that `ARTIFACT_DIR` also defaults into this tree (as `.../agents-sandbox/guest`), so the whole guest lives under one libvirt-allowed root shared by every worktree.

- [ ] **Step 6: Commit**

```bash
git add scripts/kvm-guest.sh test/scripts/kvm_guest_test.go .operator-shared/guides/kvm-guest.md
git commit -m "feat(kvm-guest): default guest artifacts into the shared image-cache tree"
```

---

### Task 2: State-derived branch lock

**Files:**
- Modify: `scripts/kvm-guest.sh` (new helpers; `acquire_guest` replaces `ensure_running`; `agent_up`, `destroy`, dispatch, usage/header)
- Test: `test/scripts/kvm_guest_test.go`
- Modify: `Makefile` (`kvm-pause`, `.PHONY`)
- Modify: `.operator-shared/guides/kvm-guest.md`, `.operator-shared/catalog.md`
- Create: `changelog.d/830.changed.md`

**Interfaces:**
- Consumes: `ARTIFACT_DIR`, `OWNER_PATH`, `REPO_BRANCH`, and `create`/`ensure_default_network`/`wait_for_lease`/`probe_guest_egress` from the existing script (Task 1's `OWNER_PATH`).
- Produces (script functions, all no-arg): `guest_state` (prints `absent`|`running`|`paused`|`shutoff`|other); `guest_owner_branch` (prints the label's branch or empty); `write_guest_owner`; `clear_guest_owner`; `acquire_guest`; `suspend_guest`. Dispatch adds `pause|suspend`. A new `make kvm-pause` runs it.

- [ ] **Step 1: Add the shared test fixture that marks the guest running and owned by this branch**

Add to `test/scripts/kvm_guest_test.go`:

```go
// lockKvmGuest marks the faked guest running and owned by this branch, so a
// guarded subcommand proceeds through acquire_guest instead of trying to create
// a guest (which would need a hypervisor). It writes the owner label the script
// reads from ARTIFACT_DIR.
func lockKvmGuest(t *testing.T, env map[string]string) {
	t.Helper()
	artifactDir := env["ARTIFACT_DIR"]
	if err := os.MkdirAll(artifactDir, 0o755); err != nil {
		t.Fatalf("mkdir artifact dir: %v", err)
	}
	env["VIRSH_DOMINFO_OK"] = "1"
	env["VIRSH_DOMSTATE"] = "running"
	writeGuestOwnerFile(t, artifactDir, env["REPO_BRANCH"])
}

// writeGuestOwnerFile writes the owner label for VM_NAME=asb-kvm-test.
func writeGuestOwnerFile(t *testing.T, artifactDir, branch string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(artifactDir, "asb-kvm-test.owner"),
		[]byte("branch="+branch+"\n"), 0o644); err != nil {
		t.Fatalf("write guest owner: %v", err)
	}
}
```

- [ ] **Step 2: Update the existing guarded-subcommand tests to lock the guest**

Call `lockKvmGuest(t, env)` in these tests, immediately after their `env := kvmGuestEnv(dir)` (or `env := agentUpEnv(t, dir)`) line: `TestKvmGuestRunForwardsTheCommandOverSSH`, `TestKvmGuestAgentAllocatesATTYAndRunsInAGuestTmux`, `TestKvmGuestAgentForwardsAnInlineCredentialCommand`, `TestKvmGuestAgentProvisionSyncsConfigAndMaterializes`, `TestKvmGuestAgentProvisionCopiesGitIdentityAndDisablesSigning`, `TestKvmGuestSyncForwardsTheCheckoutWithExcludes`, `TestKvmGuestPrimeCopiesImagesAndHome`, `TestKvmGuestPrimeWarnsWhenTheHostHasNoCache`, and inside `agentUpEnv` (so every agent-up case is owned by this branch; the `shut off` case ignores the label). Leave `TestKvmGuestRender*`, `TestKvmGuestDown*`, `TestKvmGuestFirewall*`, `TestKvmGuestSyncBack*`, and the usage/guard tests unchanged.

- [ ] **Step 3: Write the failing lock tests**

Add to `test/scripts/kvm_guest_test.go`:

```go
func TestKvmGuestSyncProceedsWhenTheOwnerBranchHoldsTheGuest(t *testing.T) {
	dir := t.TempDir()
	env := kvmGuestEnv(dir)
	lockKvmGuest(t, env)
	env["RSYNC_ARGV"] = filepath.Join(dir, "rsync-argv")
	env["PATH"] = kvmGuestPath(t, "virsh", "rsync")

	result := RunScript(t, dir, env, repositoryScript(t, "kvm-guest.sh"), "sync")
	assertStatus(t, result, 0)

	assertContains(t, "rsync argv", readText(t, filepath.Join(dir, "rsync-argv")),
		"dev@192.168.122.55:~/agents-sandbox/")
	virshArgv := readText(t, filepath.Join(dir, "virsh-argv"))
	assertNotContains(t, "virsh argv", virshArgv, "resume")
	assertNotContains(t, "virsh argv", virshArgv, "start")
}

func TestKvmGuestSyncRefusesAGuestHeldByAnotherBranch(t *testing.T) {
	dir := t.TempDir()
	env := kvmGuestEnv(dir)
	lockKvmGuest(t, env)
	writeGuestOwnerFile(t, dir, "some/other-branch")
	env["RSYNC_ARGV"] = filepath.Join(dir, "rsync-argv")
	env["PATH"] = kvmGuestPath(t, "virsh", "rsync")

	result := RunScript(t, dir, env, repositoryScript(t, "kvm-guest.sh"), "sync")
	assertStatus(t, result, 1)

	assertContains(t, "stderr", result.Stderr, "in use by branch 'some/other-branch'")
	if _, err := os.Stat(filepath.Join(dir, "rsync-argv")); err == nil {
		t.Error("sync ran despite another branch holding the guest")
	}
}

func TestKvmGuestSyncRefusesARunningGuestWithoutAnOwnerLabel(t *testing.T) {
	dir := t.TempDir()
	env := kvmGuestEnv(dir)
	env["VIRSH_DOMINFO_OK"] = "1"
	env["VIRSH_DOMSTATE"] = "running"
	env["RSYNC_ARGV"] = filepath.Join(dir, "rsync-argv")
	env["PATH"] = kvmGuestPath(t, "virsh", "rsync")

	result := RunScript(t, dir, env, repositoryScript(t, "kvm-guest.sh"), "sync")
	assertStatus(t, result, 1)

	assertContains(t, "stderr", result.Stderr, "without an owner label")
}

func TestKvmGuestRunResumesAPausedGuest(t *testing.T) {
	dir := t.TempDir()
	env := kvmGuestEnv(dir)
	env["VIRSH_DOMINFO_OK"] = "1"
	env["VIRSH_DOMSTATE"] = "paused"
	env["SSH_ARGV"] = filepath.Join(dir, "ssh-argv")
	env["PATH"] = kvmGuestPath(t, "virsh", "ssh")

	result := RunScript(t, dir, env, repositoryScript(t, "kvm-guest.sh"), "run", "true")
	assertStatus(t, result, 0)

	assertContains(t, "virsh argv", readText(t, filepath.Join(dir, "virsh-argv")), "resume asb-kvm-test")
	owner := readText(t, filepath.Join(dir, "asb-kvm-test.owner"))
	assertContains(t, "owner", owner, "branch=feat/793-msb-journeys")
}

func TestKvmGuestRunStartsAStoppedGuest(t *testing.T) {
	dir := t.TempDir()
	env := kvmGuestEnv(dir)
	env["VIRSH_DOMINFO_OK"] = "1"
	env["VIRSH_DOMSTATE"] = "shut off"
	env["SSH_ARGV"] = filepath.Join(dir, "ssh-argv")
	env["PATH"] = kvmGuestPath(t, "virsh", "ssh")

	result := RunScript(t, dir, env, repositoryScript(t, "kvm-guest.sh"), "run", "true")
	assertStatus(t, result, 0)

	assertContains(t, "virsh argv", readText(t, filepath.Join(dir, "virsh-argv")), "start asb-kvm-test")
}

func TestKvmGuestPauseSuspendsTheGuestAndReleasesTheLock(t *testing.T) {
	dir := t.TempDir()
	env := kvmGuestEnv(dir)
	lockKvmGuest(t, env)
	env["PATH"] = kvmGuestPath(t, "virsh")

	result := RunScript(t, dir, env, repositoryScript(t, "kvm-guest.sh"), "pause")
	assertStatus(t, result, 0)

	assertContains(t, "virsh argv", readText(t, filepath.Join(dir, "virsh-argv")), "suspend asb-kvm-test")
	if _, err := os.Stat(filepath.Join(dir, "asb-kvm-test.owner")); err == nil {
		t.Error("pause left the owner label behind")
	}
}
```

In `TestKvmGuestDownDestroysTheDomain`, before `RunScript`, add `writeGuestOwnerFile(t, dir, "feat/793-msb-journeys")`, and after `assertStatus` add:

```go
	if _, err := os.Stat(filepath.Join(dir, "asb-kvm-test.owner")); err == nil {
		t.Error("down left the owner label behind")
	}
```

- [ ] **Step 4: Run the tests to verify they fail**

Run: `go test ./test/scripts/ -run 'TestKvmGuest' -count=1`
Expected: FAIL — `pause` is an unknown subcommand (exit 2), the lock tests' `sync`/`run` try to create a guest (dying on the missing `/dev/kvm`), and the running/same and paused cases do not yet resume or refuse.

- [ ] **Step 5: Implement the lock**

In `scripts/kvm-guest.sh`, add these helpers (below `ensure_default_network`/`guest_ip`, before `ssh_in`):

```bash
# The shared guest is locked by its domain state: running means one branch holds
# it, paused or absent means it is free. The label names the holding branch so a
# competing branch can be told who has it; it is a record, not the lock.
guest_owner_branch() {
  sed -n 's/^branch=//p' "$OWNER_PATH" 2>/dev/null | head -1
}

write_guest_owner() {
  mkdir -p "$ARTIFACT_DIR"
  printf 'branch=%s\npid=%s\ncwd=%s\n' "$REPO_BRANCH" "$$" "$PWD" > "$OWNER_PATH"
}

clear_guest_owner() { rm -f "$OWNER_PATH"; }

# guest_state prints "absent" when the domain is not defined, else libvirt's
# state name with whitespace removed ("running", "paused", "shutoff", ...).
guest_state() {
  if ! virsh_cmd dominfo "$VM_NAME" >/dev/null 2>&1; then
    printf 'absent\n'
    return
  fi
  virsh_cmd domstate "$VM_NAME" 2>/dev/null | tr -d '[:space:]'
}

# acquire_guest brings the shared guest up for this branch and records it as the
# holder. Idempotent for the branch that already holds it; a guest held by
# another branch (or a pre-lock guest with no label) is refused by name.
acquire_guest() {
  local state owner ip
  state="$(guest_state)"
  case "$state" in
    running)
      owner="$(guest_owner_branch)"
      [ "$owner" = "$REPO_BRANCH" ] && return 0
      if [ -n "$owner" ]; then
        die "guest '$VM_NAME' is in use by branch '$owner'. Hand it over with '$0 pause' (keeps the disk) or '$0 down' (destroys it)."
      fi
      die "guest '$VM_NAME' is running without an owner label (created before the lock). Hand it over with '$0 pause' or '$0 down'."
      ;;
    paused)
      log "resuming guest '$VM_NAME'"
      virsh_cmd resume "$VM_NAME" >/dev/null
      ;;
    shutoff)
      ensure_default_network
      log "starting the stopped guest '$VM_NAME'"
      virsh_cmd start "$VM_NAME" >/dev/null
      ip="$(wait_for_lease)"
      probe_guest_egress "$ip"
      ;;
    absent)
      create
      ;;
    *)
      die "guest '$VM_NAME' is in state '$state'; expected running, paused, shut off, or absent"
      ;;
  esac
  write_guest_owner
}

# suspend_guest pauses the guest and releases the lock, keeping the disk so the
# next branch resumes rather than rebuilds. Hand-off is explicit: a running guest
# stays held until it is paused or destroyed.
suspend_guest() {
  local state
  state="$(guest_state)"
  case "$state" in
    running)
      log "pausing guest '$VM_NAME' (disk kept; '$0 up' resumes it)"
      virsh_cmd suspend "$VM_NAME" >/dev/null
      ;;
    paused) log "guest '$VM_NAME' is already paused" ;;
    absent|shutoff) log "guest '$VM_NAME' is not running" ;;
    *) die "guest '$VM_NAME' is in an unexpected state '$state'" ;;
  esac
  clear_guest_owner
}
```

Delete `ensure_running` (now replaced by `acquire_guest`) and change `agent_up`'s first call from `ensure_running` to `acquire_guest`. In `destroy`, add `clear_guest_owner` after the `rm -f "$DISK_PATH" ...` line.

Replace the dispatch `case` with:

```bash
case "${1:-}" in
  up|create)       acquire_guest ;;
  render)          render ;;
  ssh)             ssh_in ;;
  sync)            acquire_guest; sync_in ;;
  prime)           acquire_guest; prime ;;
  run)             shift; acquire_guest; run_in "$@" ;;
  agent)           shift; acquire_guest; agent_in "$@" ;;
  agent-up)        shift; agent_up "$@" ;;
  agent-provision) shift; acquire_guest; agent_provision "$@" ;;
  sync-back)       sync_back "$@" ;;
  pause|suspend)   suspend_guest ;;
  firewall|fix-net) fix_firewall ;;
  down|destroy)    destroy ;;
  *) usage >&2; exit 2 ;;
esac
```

Add to `usage()`: the synopsis gains `|pause`, and a line `  pause             suspend the guest and release the shared-guest lock (disk kept)`.

- [ ] **Step 6: Run the tests to verify they pass**

Run: `go test ./test/scripts/ -run 'TestKvmGuest' -count=1`
Expected: PASS.

- [ ] **Step 7: Lint the script**

Run: `make shellcheck`
Expected: PASS.

- [ ] **Step 8: Add the Make target**

In `Makefile`, add `kvm-pause` to the `.PHONY` list on line 1, and add after the `kvm-down` target:

```make
# Pause the shared guest and release it for another branch without destroying the
# disk; the next `make kvm-up` (or any guarded target) resumes it.
kvm-pause:
	scripts/kvm-guest.sh pause
```

- [ ] **Step 9: Update the guide, catalog, and changelog**

In `.operator-shared/guides/kvm-guest.md`:
- Add `make kvm-pause` to the workflow block (next to `make kvm-down`).
- Add a short subsection, "Sharing the guest across worktrees", stating: one guest is shared by all worktrees; its artifacts live under `ARTIFACT_DIR` (`$KVM_IMAGE_CACHE/guest`); the domain state is the lock (running = held, paused/down = free); the holder is named in `$ARTIFACT_DIR/$VM_NAME.owner`; a competing branch is refused by name and hands over with `make kvm-pause` (keeps the disk) or `make kvm-down`; `pause` freezes any running session, including a live tmux agent.

In `.operator-shared/catalog.md`:
- Add the guide's new anchors: `make kvm-pause`, "shared guest", "owner label", `ARTIFACT_DIR` default — extend the `guides/kvm-guest.md` `Read If`.

Create `changelog.d/830.changed.md` (rename to the real PR number after opening the PR):

```markdown
The KVM-guest provisioner now keeps its guest disk, seed, and domain XML under the libvirt images tree instead of the working directory, so worktrees no longer each grow a guest. The one shared guest is locked to the branch using it (running = held, paused/down = free); hand it to another branch with `make kvm-pause`.
```

- [ ] **Step 10: Run the gates**

Run: `make shellcheck && go test ./test/scripts/ -count=1 && make brain-check`
Expected: PASS. Then run `make check` (or `make check-local-changes` while iterating) and fix anything it reports; run `make fmt` if it reports formatting.

- [ ] **Step 11: Commit**

```bash
git add scripts/kvm-guest.sh test/scripts/kvm_guest_test.go Makefile \
  .operator-shared/guides/kvm-guest.md .operator-shared/catalog.md changelog.d/830.changed.md
git commit -m "feat(kvm-guest): lock the shared guest by domain state with a pause hand-off"
```

---

## Self-review

- **Spec coverage:** centralized default (Task 1); state lock, owner label, `pause`, guarded/unguarded split, `down` release, docs, Make target (Task 2). No gaps.
- **Review Focus:** no-label refusal (Step 3 `...WithoutAnOwnerLabel`); same-branch-other-worktree (branch is the key — `...WhenTheOwnerBranchHoldsTheGuest`); `pause` idempotence (Step 5's `paused`/`absent|shutoff` arms — no dedicated test, covered by the `running` test's assertion set; a dedicated case is optional); `down` clears a stale label (Step 3's `TestKvmGuestDown...` addition); unexpected `domstate` (Step 5's `*)` arm — no dedicated test, since the fake only emits configured states).
- **Type/name consistency:** `guest_state`, `guest_owner_branch`, `write_guest_owner`, `clear_guest_owner`, `acquire_guest`, `suspend_guest`, `OWNER_PATH` are used with the same names in the script and tests.
- **Proportion:** two tasks; code blocks are the new shell functions and their tests, not a transcript.
