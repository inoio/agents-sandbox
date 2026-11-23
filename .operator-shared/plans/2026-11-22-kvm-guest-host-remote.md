# KVM-guest git against the host Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the host the KVM guest's git remote — `make kvm-sync` becomes a guest-side `git pull` from the host, the agent can `git push` to the host, and the destructive rsync goes.

**Architecture:** `scripts/kvm-guest.sh` gains a host-global ed25519 key (authorized on the host under `git-shell`, installed in the guest), routes the guest's single `origin` remote to the host through a `url.<host>.insteadOf <repo-url>` rewrite, and replaces `sync_in`'s rsync with a guest `git fetch`/`checkout`/`merge --ff-only`. `gh` and its sign-in stay; only its GitHub `insteadOf` rewrites go.

**Tech Stack:** Bash (`set -euo pipefail`), the Go fixture harness in `test/scripts`, GNU make.

**Spec:** `.operator-shared/plans/2026-11-22-kvm-guest-host-git-remote-design.md`

## Global Constraints

- Base is `origin/main` with PR #831 merged (it is the branch's base). #831's `gh` install, `gh auth login`, and `gh auth setup-git` stay; only the two `url.https://github.com/.insteadOf` configs in `gh_auth` are removed. Do **not** touch `agent_provision` (its `user.name`/`user.email` narrowing is already in).
- Host-only, x86_64-only; never run in CI or the development VM.
- `scripts/kvm-guest.sh` is `set -euo pipefail` and must pass `make shellcheck`.
- Gates are `make shellcheck`, `make fmt`, `make lint` — never `go vet`/`go fmt` directly; `make check` last.
- Unit tests live in `test/scripts/kvm_guest_test.go`; faked commands register in `test/scripts/main_test.go` via `Register`.
- Any test that reaches `ensure_guest_git_remote` must set `HOME` to a temp dir — it writes `$HOME/.ssh/authorized_keys`.
- Changelog fragment `changelog.d/840.added.md` (one past the highest PR, 839); rename it to the real PR number once the PR exists.

## Review Focus

- A host with no `sshd` (or one refusing the key) — `kvm-sync`/`agent-up` exits non-zero with the diagnostic and does **not** proceed to the fetch (Task 1).
- A rotated guest key — `authorized_keys` replaces the marker line instead of duplicating or skipping it (Task 1).
- Re-running `kvm-sync` — idempotent: one `authorized_keys` line, and the rewrite config does not accumulate (Tasks 1, 2).
- A diverged guest branch — sync refuses and never resets the working tree (Task 3).
- GitHub remotes are no longer rewritten — `gh_auth` sets up the credential helper but no `url.https://github.com/.insteadOf` (Task 2).

---

### Task 1: Authenticate the guest to the host

**Files:**
- Modify: `scripts/kvm-guest.sh` (globals near line 93; new `default_gateway`/`require_gateway` after `default_bridge` at 375; new `ensure_guest_git_key`/`ensure_host_authorized_key`/`probe_guest_to_host_ssh`/`ensure_guest_git_remote` before `sync_in` at 441; `sync_in` calls `ensure_guest_git_remote`; header env list)
- Modify: `test/scripts/kvm_guest_test.go`
- Modify: `test/scripts/main_test.go` (register `ssh-keygen`)

**Interfaces:**
- Consumes: `repo_root`, `require_guest_ip`, `virsh_cmd`, `VM_USER`.
- Produces:
  - globals `KVM_HOST_USER` (`${KVM_HOST_USER:-${USER:-$(id -un)}}`), `KVM_GUEST_STATE` (`${KVM_GUEST_STATE:-${XDG_STATE_HOME:-$HOME/.local/state}/agents-sandbox/kvm-guest}`), `KVM_GUEST_KEY` (`$KVM_GUEST_STATE/id_kvm_guest`).
  - `default_gateway()` → the `<ip address='…'>` of `virsh net-dumpxml default`; `require_gateway()` → the same, dying when empty.
  - `ensure_guest_git_key <ip>` → generate the key if absent, authorise it, install it in the guest.
  - `ensure_host_authorized_key()` → append/replace the marker-tagged `git-shell` line.
  - `probe_guest_to_host_ssh <ip> <gateway>` → die with a diagnostic on failure.
  - `ensure_guest_git_remote <ip>` → `require_gateway` + key + preflight (Task 2 adds the routing).
  - fake `kvmGuestSSHKeygenHandler`; the ssh fake's `SSH_FAIL_CONTAINS` knob; the virsh fake's gateway `<ip>`.
  - Test helper `kvmGuestSyncEnv(t, dir)`.

- [ ] **Step 1: Extend the fakes**

In `test/scripts/kvm_guest_test.go`, replace `kvmGuestVirshHandler`'s `net-dumpxml` case:

```go
	case strings.Contains(joined, "net-dumpxml"):
		return "<network><name>default</name><bridge name='virbr0'/>" +
			"<ip address='192.168.122.1' netmask='255.255.255.0'><dhcp/></ip></network>\n", "", 0
```

In `kvmGuestSSHHandler`, add a failure knob before the cloud-init case:

```go
	if fail := envOption("SSH_FAIL_CONTAINS"); fail != "" && strings.Contains(joined, fail) {
		return "", "ssh: simulated failure\n", 255
	}
```

Add the keygen fake:

```go
// kvmGuestSSHKeygenHandler serves `ssh-keygen`: it writes the private and public
// key at the -f path so the script can read the public half, and logs argv.
func kvmGuestSSHKeygenHandler(args []string) (string, string, int) {
	recordArgv("SSH_KEYGEN_ARGV", args)
	for i, arg := range args {
		if arg == "-f" && i+1 < len(args) {
			_ = os.WriteFile(args[i+1], []byte("FAKE-PRIVATE-KEY\n"), 0o600)
			_ = os.WriteFile(args[i+1]+".pub",
				[]byte("ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIGUEST agents-sandbox-kvm-guest\n"), 0o644)
		}
	}
	return "", "", 0
}
```

In `test/scripts/main_test.go`, add `Register("ssh-keygen", kvmGuestSSHKeygenHandler)` next to the others.

- [ ] **Step 2: Write the failing tests**

Add the shared env and tests to `test/scripts/kvm_guest_test.go`:

```go
// kvmGuestSyncEnv is the environment the sync cases share: a temp HOME (sync
// writes authorized_keys), a deterministic host-global key dir, and the fakes.
func kvmGuestSyncEnv(t *testing.T, dir string) map[string]string {
	t.Helper()
	home := filepath.Join(dir, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatalf("create home: %v", err)
	}
	env := kvmGuestEnv(dir)
	env["HOME"] = home
	env["KVM_HOST_USER"] = "kvmhost"
	env["KVM_GUEST_STATE"] = filepath.Join(dir, "guest-state")
	env["SSH_ARGV"] = filepath.Join(dir, "ssh-argv")
	env["SSH_KEYGEN_ARGV"] = filepath.Join(dir, "ssh-keygen-argv")
	env["PATH"] = kvmGuestPath(t, "virsh", "ssh", "git", "ssh-keygen", "rsync")
	return env
}

// TestKvmGuestSyncGeneratesAndAuthorizesTheHostKey pins the auth chain: a key is
// generated once under the host-global state dir, its public half is authorized
// for git-shell on the host, and its private half is installed in the guest.
func TestKvmGuestSyncGeneratesAndAuthorizesTheHostKey(t *testing.T) {
	dir := t.TempDir()
	env := kvmGuestSyncEnv(t, dir)

	result := RunScript(t, dir, env, repositoryScript(t, "kvm-guest.sh"), "sync")
	assertStatus(t, result, 0)

	keygen := readText(t, filepath.Join(dir, "ssh-keygen-argv"))
	assertContains(t, "ssh-keygen argv", keygen, "-f "+filepath.Join(dir, "guest-state", "id_kvm_guest"))

	authorized := readText(t, filepath.Join(dir, "home", ".ssh", "authorized_keys"))
	assertContains(t, "authorized_keys", authorized, `command="git-shell -c \"$SSH_ORIGINAL_COMMAND\"",restrict`)
	assertContains(t, "authorized_keys", authorized, "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIGUEST")

	assertContains(t, "ssh argv", readText(t, filepath.Join(dir, "ssh-argv")), "cat > ~/.ssh/id_kvm_guest")
}

// TestKvmGuestSyncReplacesARotatedAuthorizedKey pins the guard against a key
// rotation (state dir wiped, or a moved key): a stale marker line is replaced,
// not left to make the guest's new key fail auth, and not duplicated.
func TestKvmGuestSyncReplacesARotatedAuthorizedKey(t *testing.T) {
	dir := t.TempDir()
	env := kvmGuestSyncEnv(t, dir)
	authorizedPath := filepath.Join(dir, "home", ".ssh", "authorized_keys")
	if err := os.MkdirAll(filepath.Dir(authorizedPath), 0o755); err != nil {
		t.Fatalf("create .ssh: %v", err)
	}
	if err := os.WriteFile(authorizedPath, []byte(
		`command="git-shell -c \"$SSH_ORIGINAL_COMMAND\"",restrict ssh-ed25519 AAAASTALE agents-sandbox-kvm-guest`+"\n"), 0o600); err != nil {
		t.Fatalf("seed authorized_keys: %v", err)
	}

	result := RunScript(t, dir, env, repositoryScript(t, "kvm-guest.sh"), "sync")
	assertStatus(t, result, 0)

	authorized := readText(t, authorizedPath)
	assertNotContains(t, "authorized_keys", authorized, "AAAASTALE")
	assertContains(t, "authorized_keys", authorized, "AAAAC3NzaC1lZDI1NTE5AAAAIGUEST")
	if n := strings.Count(authorized, "agents-sandbox-kvm-guest"); n != 1 {
		t.Fatalf("expected exactly one authorized key line, got %d:\n%s", n, authorized)
	}
}

// TestKvmGuestSyncKeepsOneAuthorizedKeyLineOnRerun pins idempotency.
func TestKvmGuestSyncKeepsOneAuthorizedKeyLineOnRerun(t *testing.T) {
	dir := t.TempDir()
	env := kvmGuestSyncEnv(t, dir)

	assertStatus(t, RunScript(t, dir, env, repositoryScript(t, "kvm-guest.sh"), "sync"), 0)
	assertStatus(t, RunScript(t, dir, env, repositoryScript(t, "kvm-guest.sh"), "sync"), 0)

	authorized := readText(t, filepath.Join(dir, "home", ".ssh", "authorized_keys"))
	if n := strings.Count(authorized, "agents-sandbox-kvm-guest"); n != 1 {
		t.Fatalf("re-running sync duplicated the authorized key line (%d):\n%s", n, authorized)
	}
}

// TestKvmGuestSyncFailsWhenTheGuestCannotReachTheHost pins the preflight: a guest
// that cannot ssh to the host fails with the diagnostic.
func TestKvmGuestSyncFailsWhenTheGuestCannotReachTheHost(t *testing.T) {
	dir := t.TempDir()
	env := kvmGuestSyncEnv(t, dir)
	env["SSH_FAIL_CONTAINS"] = "@192.168.122.1 true"

	result := RunScript(t, dir, env, repositoryScript(t, "kvm-guest.sh"), "sync")
	assertStatus(t, result, 1)

	assertContains(t, "stderr", result.Stderr, "cannot reach the host over SSH")
}
```

Update the existing `TestKvmGuestSyncForwardsTheCheckoutWithExcludes` (still the rsync test at this task) to add `env["HOME"] = <temp>` and `env["KVM_GUEST_STATE"] = <temp>` so the key step writes a temp home. Update `agentUpEnv` likewise: add `KVM_HOST_USER`, `KVM_GUEST_STATE`, `ssh-keygen` on `PATH`.

- [ ] **Step 3: Run the tests to verify they fail**

Run: `go test -tags microsandbox_ffi_path ./test/scripts/ -run 'TestKvmGuestSync' -count=1`
Expected: FAIL — no key is generated, no `authorized_keys`, no preflight.

- [ ] **Step 4: Implement the key, authorization, and preflight**

Add the globals next to `LIBVIRT_URI` (line 93):

```bash
KVM_HOST_USER="${KVM_HOST_USER:-${USER:-$(id -un)}}"
KVM_GUEST_STATE="${KVM_GUEST_STATE:-${XDG_STATE_HOME:-$HOME/.local/state}/agents-sandbox/kvm-guest}"
KVM_GUEST_KEY="$KVM_GUEST_STATE/id_kvm_guest"
```

Add after `default_bridge()` (line 375):

```bash
# default_gateway prints the host's address on the guest's bridge: the guest
# reaches the host there, and the guest's git remote points at it.
default_gateway() {
  local gateway
  gateway="$(virsh_cmd net-dumpxml default 2>/dev/null |
    sed -n "s/.*<ip address='\([^']*\)'.*/\1/p" | head -1)"
  printf '%s\n' "$gateway"
}

require_gateway() {
  local gateway
  gateway="$(default_gateway)"
  [ -n "$gateway" ] || die "could not determine the '$VM_NAME' bridge gateway; is the libvirt 'default' network active?"
  printf '%s\n' "$gateway"
}
```

Add before `sync_in()` (line 441):

```bash
# ensure_guest_git_key gives the guest a key that authenticates it to the host for
# git: the key is generated once under the host-global state dir (so a guest
# rebuild reuses it and authorized_keys does not accumulate lines), authorized on
# the host, and installed in the guest.
ensure_guest_git_key() {
  local ip="$1"
  if [ ! -f "$KVM_GUEST_KEY" ]; then
    log "generating the guest git key at $KVM_GUEST_KEY"
    mkdir -p "$KVM_GUEST_STATE"
    chmod 700 "$KVM_GUEST_STATE"
    ssh-keygen -t ed25519 -N '' -C agents-sandbox-kvm-guest -f "$KVM_GUEST_KEY" >/dev/null
  fi
  ensure_host_authorized_key
  log "installing the guest git key in $VM_USER@$ip"
  ssh -o StrictHostKeyChecking=accept-new "$VM_USER@$ip" \
    -- "bash -lc 'install -d -m 700 ~/.ssh && cat > ~/.ssh/id_kvm_guest && chmod 600 ~/.ssh/id_kvm_guest'" \
    < "$KVM_GUEST_KEY"
}

# ensure_host_authorized_key authorizes the guest key for git on this host,
# confined to git-shell (it can run git-upload-pack/receive-pack, not a shell).
# Idempotent by the whole key: a rotated key replaces the marker-tagged line.
ensure_host_authorized_key() {
  local pubkey line auth="$HOME/.ssh/authorized_keys"
  command -v git-shell >/dev/null 2>&1 || die "git-shell not found on the host; install git"
  pubkey="$(cat "$KVM_GUEST_KEY.pub")"
  line="command=\"git-shell -c \\\"\$SSH_ORIGINAL_COMMAND\\\"\",restrict $pubkey"
  mkdir -p "$HOME/.ssh"
  touch "$auth"
  chmod 600 "$auth"
  if grep -qF "$pubkey" "$auth"; then
    return 0
  fi
  if grep -qF 'agents-sandbox-kvm-guest' "$auth"; then
    grep -vF 'agents-sandbox-kvm-guest' "$auth" > "$auth.tmp" || true
    mv "$auth.tmp" "$auth"
  fi
  printf '%s\n' "$line" >> "$auth"
}

# probe_guest_to_host_ssh checks that the guest can reach the host for git, so a
# host without sshd (or one refusing the key) fails with a diagnostic instead of
# an opaque git error. There is no fallback transport.
probe_guest_to_host_ssh() {
  local ip="$1" gateway="$2"
  if ! ssh -o BatchMode=yes -o StrictHostKeyChecking=accept-new -o ConnectTimeout=8 -o LogLevel=ERROR \
      "$VM_USER@$ip" \
      "ssh -o BatchMode=yes -o StrictHostKeyChecking=accept-new -i ~/.ssh/id_kvm_guest $KVM_HOST_USER@$gateway true" \
      >/dev/null 2>&1; then
    die "the guest cannot reach the host over SSH at $gateway for git.
    On the host: run sshd and accept the guest's key (authorized in $HOME/.ssh/authorized_keys),
    and install git-shell. Retry: $0 sync"
  fi
}

# ensure_guest_git_remote makes the host the guest's git remote. Idempotent.
ensure_guest_git_remote() {
  local ip="$1" gateway
  gateway="$(require_gateway)"
  ensure_guest_git_key "$ip"
  probe_guest_to_host_ssh "$ip" "$gateway"
}
```

Make `sync_in()` call it first (keep the rsync for now):

```bash
  ip="$(require_guest_ip)"
  ensure_guest_git_remote "$ip"
```

Add `KVM_GUEST_STATE` and `KVM_HOST_USER` to the header env-override list (lines 49-62).

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test -tags microsandbox_ffi_path ./test/scripts/ -run 'TestKvmGuest' -count=1`
Expected: PASS.

- [ ] **Step 6: Gate and commit**

```bash
make shellcheck && make fmt && make lint
git add scripts/kvm-guest.sh test/scripts/kvm_guest_test.go test/scripts/main_test.go
git commit -m "feat(kvm-guest): authenticate the guest to the host for git"
```

---

### Task 2: Route the guest's origin to the host

**Files:**
- Modify: `scripts/kvm-guest.sh` (new `host_repo_path`/`configure_guest_remote`; extend `ensure_guest_git_remote`; drop the two `insteadOf` configs from `gh_auth` at 599)
- Modify: `test/scripts/kvm_guest_test.go`

**Interfaces:**
- Consumes: `KVM_HOST_USER`, `REPO_URL`, `REPO_BRANCH`, guest `$ip`/`$gateway`; `require_gateway`.
- Produces:
  - `host_repo_path()` → `git -C "$repo_root" rev-parse --path-format=absolute --git-common-dir`.
  - `configure_guest_remote <ip> <gateway> <repo_path>` → sets the guest's `url.<host>.insteadOf`, branch upstream, and `core.sshCommand`.
  - `ensure_guest_git_remote` calls both.

- [ ] **Step 1: Write the failing tests**

```go
// TestKvmGuestSyncRoutesOriginToTheHost pins that the guest's single remote is
// routed to the host by a URL rewrite — the recorded origin URL is kept, and git
// operations go to the host repository.
func TestKvmGuestSyncRoutesOriginToTheHost(t *testing.T) {
	dir := t.TempDir()
	env := kvmGuestSyncEnv(t, dir)
	env["GIT_COMMON_DIR"] = "/host/agents-sandbox/.git"

	result := RunScript(t, dir, env, repositoryScript(t, "kvm-guest.sh"), "sync")
	assertStatus(t, result, 0)

	argv := readText(t, filepath.Join(dir, "ssh-argv"))
	assertContains(t, "ssh argv", argv,
		`git config --global url."ssh://kvmhost@192.168.122.1/host/agents-sandbox/.git".insteadOf https://example.test/agents-sandbox.git`)
	assertContains(t, "ssh argv", argv, "git config branch.feat/793-msb-journeys.remote origin")
	assertContains(t, "ssh argv", argv, "core.sshCommand")
	assertNotContains(t, "ssh argv", argv, "git remote add")
}

// TestKvmGuestAgentUpDoesNotRewriteGithubRemotes pins that #831's GitHub
// insteadOf rewrites are gone while gh's sign-in stays.
func TestKvmGuestAgentUpDoesNotRewriteGithubRemotes(t *testing.T) {
	dir := t.TempDir()
	env := agentUpEnv(t, dir)
	env["VIRSH_DOMINFO_OK"] = "1"
	env["VIRSH_DOMSTATE"] = "running"
	env["GITHUB_PAT"] = "secret"

	result := RunScript(t, dir, env, repositoryScript(t, "kvm-guest.sh"), "agent-up", "opencode2")
	assertStatus(t, result, 0)

	argv := readText(t, filepath.Join(dir, "ssh-argv"))
	assertContains(t, "ssh argv", argv, "gh auth login --with-token")
	assertNotContains(t, "ssh argv", argv, "url.https://github.com/.insteadOf")
}
```

`agentUpEnv` must set `GIT_COMMON_DIR = "/host/agents-sandbox/.git"` and `SSH_KEYGEN_ARGV`.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test -tags microsandbox_ffi_path ./test/scripts/ -run 'TestKvmGuestSyncRoutesOriginToTheHost|TestKvmGuestAgentUpDoesNotRewriteGithubRemotes' -count=1`
Expected: FAIL — no rewrite config, and `gh_auth` still sets the GitHub `insteadOf`.

- [ ] **Step 3: Implement the resolution and routing**

Add after `require_gateway()`:

```bash
# host_repo_path prints the host repository the guest's origin is routed to.
# --git-common-dir is the bare repository in the bare-worktree layout and the
# .git gitdir in a plain clone; git serves both as a remote path.
host_repo_path() {
  local path
  path="$(git -C "$repo_root" rev-parse --path-format=absolute --git-common-dir 2>/dev/null || true)"
  [ -n "$path" ] || die "cannot resolve the host repository path from $repo_root"
  printf '%s\n' "$path"
}
```

Add before `ensure_guest_git_key()`:

```bash
# configure_guest_remote routes the guest checkout's single origin remote to the
# host: a url.<host>.insteadOf rewrite sends every git operation on origin to the
# host repository, while origin keeps the URL the boot clone recorded. The branch
# upstream is pinned to origin and the guest's git uses the dedicated key.
configure_guest_remote() {
  local ip="$1" gateway="$2" repo_path="$3" host_url branch
  branch="${REPO_BRANCH:-main}"
  host_url="ssh://$KVM_HOST_USER@$gateway$repo_path"
  log "routing the guest origin to $host_url"
  ssh -o StrictHostKeyChecking=accept-new "$VM_USER@$ip" \
    -- "bash -lc 'set -e && cd ~/agents-sandbox && git config --global url.\"$host_url\".insteadOf $REPO_URL && git config branch.$branch.remote origin && git config branch.$branch.merge refs/heads/$branch && git config core.sshCommand \"ssh -i \$HOME/.ssh/id_kvm_guest -o StrictHostKeyChecking=accept-new\"'"
}
```

Extend `ensure_guest_git_remote`:

```bash
ensure_guest_git_remote() {
  local ip="$1" gateway repo_path
  gateway="$(require_gateway)"
  repo_path="$(host_repo_path)"
  ensure_guest_git_key "$ip"
  configure_guest_remote "$ip" "$gateway" "$repo_path"
  probe_guest_to_host_ssh "$ip" "$gateway"
}
```

In `gh_auth` (line 599), keep `gh auth login --with-token && gh auth setup-git` and drop the two `git config --global … url.https://github.com/.insteadOf …` clauses. Update `gh_auth`'s comment (lines 584-591) to say the guest's git targets the host, so no GitHub remote rewrite is needed.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -tags microsandbox_ffi_path ./test/scripts/ -run 'TestKvmGuest' -count=1`
Expected: PASS.

- [ ] **Step 5: Gate and commit**

```bash
make shellcheck && make fmt && make lint
git add scripts/kvm-guest.sh test/scripts/kvm_guest_test.go
git commit -m "feat(kvm-guest): route the guest's origin to the host"
```

---

### Task 3: `kvm-sync` pulls from the host instead of rsyncing

**Files:**
- Modify: `scripts/kvm-guest.sh` (`sync_in()` 441-455; header usage line 22 and the `usage()` `sync` line 161; drop `SOURCE_DIR` from the env list 52)
- Modify: `test/scripts/kvm_guest_test.go` (replace the rsync sync test; update the agent-up assertions)
- Modify: `Makefile:317-318` (comment)

**Interfaces:**
- Consumes: `ensure_guest_git_remote`, `require_guest_ip`, `REPO_BRANCH`.
- Produces: `sync_in()` → ensure the remote, then the guest `git fetch origin --prune && git checkout <branch> && git merge --ff-only origin/<branch>`.

- [ ] **Step 1: Write the failing tests**

Replace `TestKvmGuestSyncForwardsTheCheckoutWithExcludes` (lines ~431-454):

```go
// TestKvmGuestSyncPullsFromTheHostInsteadOfRsyncing pins that `sync` pulls the
// host's branch in the guest, fast-forward only — not an rsync that would
// clobber the agent's tree.
func TestKvmGuestSyncPullsFromTheHostInsteadOfRsyncing(t *testing.T) {
	dir := t.TempDir()
	env := kvmGuestSyncEnv(t, dir)
	env["GIT_COMMON_DIR"] = "/host/agents-sandbox/.git"
	env["RSYNC_ARGV"] = filepath.Join(dir, "rsync-argv")

	result := RunScript(t, dir, env, repositoryScript(t, "kvm-guest.sh"), "sync")
	assertStatus(t, result, 0)

	argv := readText(t, filepath.Join(dir, "ssh-argv"))
	assertContains(t, "ssh argv", argv, "git fetch origin --prune")
	assertContains(t, "ssh argv", argv, "git checkout feat/793-msb-journeys")
	assertContains(t, "ssh argv", argv, "git merge --ff-only origin/feat/793-msb-journeys")
	assertNotContains(t, "rsync argv", readText(t, filepath.Join(dir, "rsync-argv")), "agents-sandbox")
}

// TestKvmGuestSyncRefusesWhenTheGuestCannotFastForward pins that a diverged guest
// branch aborts sync — never a reset that would clobber the agent's work.
func TestKvmGuestSyncRefusesWhenTheGuestCannotFastForward(t *testing.T) {
	dir := t.TempDir()
	env := kvmGuestSyncEnv(t, dir)
	env["GIT_COMMON_DIR"] = "/host/agents-sandbox/.git"
	env["SSH_FAIL_CONTAINS"] = "git merge --ff-only"

	result := RunScript(t, dir, env, repositoryScript(t, "kvm-guest.sh"), "sync")
	if result.Status == 0 {
		t.Fatalf("sync exited 0 despite a failing guest pull:\nstderr:\n%s", result.Stderr)
	}
}
```

Update the four agent-up tests that assert the rsync sync (`GatesOnCloudInitBeforeSyncing` ~249, `FailsWhenCloudInitFails` ~271, `ReattachesWithoutSyncing` ~293, `RefusesToSyncWhenTheSessionProbeFails` ~311) to assert the guest pull over the faked `ssh`:

```go
	assertContains(t, "ssh argv", sshArgv, "git fetch origin --prune")
```
```go
	assertNotContains(t, "ssh argv", readText(t, filepath.Join(dir, "ssh-argv")), "git fetch origin")
```

`agentUpEnv` must set `RSYNC_ARGV = filepath.Join(dir, "rsync-argv")`.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test -tags microsandbox_ffi_path ./test/scripts/ -run 'TestKvmGuestSync|TestKvmGuestAgentUp' -count=1`
Expected: FAIL — sync still rsyncs, so the ssh log has no fetch.

- [ ] **Step 3: Rewrite `sync_in()`**

Replace lines 441-455 (the `local ip source` … rsync block) with:

```bash
# sync_in brings the guest checkout to the host's branch: it ensures the host is
# the guest's git remote, then, in the guest, fetches the host, switches to the
# branch, and fast-forwards. A diverged branch refuses (never a reset), so the
# agent's work is not clobbered; only committed host work moves — uncommitted
# changes are no longer copied.
sync_in() {
  local ip branch
  ip="$(require_guest_ip)"
  branch="${REPO_BRANCH:-main}"
  ensure_guest_git_remote "$ip"
  log "pulling $branch from the host into $VM_USER@$ip:~/agents-sandbox"
  ssh -o StrictHostKeyChecking=accept-new "$VM_USER@$ip" \
    -- "bash -lc 'set -e && cd ~/agents-sandbox && git fetch origin --prune && git checkout $branch && git merge --ff-only origin/$branch'"
}
```

Update the header usage (line 22) and the `usage()` `sync` line (line 161) to "point the guest's git at the host and pull its branch", and drop `SOURCE_DIR` from the header env-override list (line 52). Update `Makefile:317-318`'s comment likewise.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -tags microsandbox_ffi_path ./test/scripts/ -run 'TestKvmGuest' -count=1`
Expected: PASS.

- [ ] **Step 5: Gate and commit**

```bash
make shellcheck && make fmt && make lint
git add scripts/kvm-guest.sh test/scripts/kvm_guest_test.go Makefile
git commit -m "feat(kvm-guest): pull the guest checkout from the host over git"
```

---

### Task 4: Update the guides, catalog, and changelog

**Files:**
- Modify: `.operator-shared/guides/kvm-guest.md`
- Modify: `.operator-shared/guides/kvm-agent-testing.md`
- Modify: `.operator-shared/catalog.md`
- Modify: `CONTRIBUTING.md`
- Create: `changelog.d/840.added.md`

**Interfaces:** none (documentation).

- [ ] **Step 1: `.operator-shared/guides/kvm-guest.md`**

- Workflow block: replace the `make kvm-sync # rsync this checkout into ~/agents-sandbox` line.
- Add a "Git transport" subsection: the guest's single `origin` is routed to the host repository at the bridge gateway by a `url.<host>.insteadOf` rewrite over the guest→host SSH that already works; the host key is generated once at `$KVM_GUEST_STATE/id_kvm_guest` and authorized under `git-shell`; `make kvm-sync` pulls, and the agent **pushes with plain `git push`** (a branch checked out in a host worktree is refused). `gh` stays signed in for issues and PRs, but the guest does not push to GitHub, and `gh`'s repo inference sees the host — use `--repo`/`GH_REPO`.
- Note the host prerequisites: `sshd` reachable from the bridge and `git-shell`. Env overrides: drop `SOURCE_DIR`; add `KVM_GUEST_STATE`, `KVM_HOST_USER`.
- Known limitation: the `git-shell` key confines the command but not the path.
- `make kvm-sync-back` paragraph: the guest can now push; keep it as the fallback.

- [ ] **Step 2: `.operator-shared/guides/kvm-agent-testing.md`**

- T5: make the guest push the primary path (commit in the guest, `git push` from the session, observe the branch in the host repository), keeping `make kvm-sync-back` as the fallback; note the guest cannot push to GitHub.
- Known limitations: the `git-shell` path caveat and the `gh`/`--repo` note.

- [ ] **Step 3: `.operator-shared/catalog.md` and `CONTRIBUTING.md`**

- Catalog: extend the `guides/kvm-guest.md` Description/Read If to name the host git remote and guest push.
- `CONTRIBUTING.md` (the kvm-guest sentence, ~line 158-163): `make kvm-sync` now pulls the checkout from the host over git, and the guest pushes its branches to the host.

- [ ] **Step 4: Changelog fragment**

```bash
cat > changelog.d/840.added.md <<'EOF'
Make the host the KVM guest's git remote: `make kvm-sync` now pulls the guest's checkout from the host
over git (replacing the one-way, destructive rsync), and the in-guest agent pushes its branches straight
to the host. The guest's `origin` is routed to the host by a URL rewrite; `gh` stays signed in for issues
and PRs, but the guest no longer pushes to GitHub.
EOF
```

- [ ] **Step 5: Gate and commit**

```bash
make brain-check && make shellcheck
git add .operator-shared/guides/kvm-guest.md .operator-shared/guides/kvm-agent-testing.md \
  .operator-shared/catalog.md CONTRIBUTING.md changelog.d/840.added.md
git commit -m "docs(kvm-guest): document the host git remote"
```

---

### Task 5: Whole-change verification

**Files:** none (verification only).

- [ ] **Step 1: Run the full gate**

Run: `make check`
Expected: every target PASS. `changelog-presence` passes because `changelog.d/840.added.md` is in the branch diff.

- [ ] **Step 2: Confirm the usage surface (read-only)**

Run: `./scripts/kvm-guest.sh 2>&1 | grep -c SOURCE_DIR`
Expected: `0`.

- [ ] **Step 3: Record what needs a host**

`make check` cannot exercise the transport (needs a real hypervisor and a host `sshd`). The manual acceptance (`.operator-shared/guides/kvm-agent-testing.md` T5) is handed to the user on a KVM host before merging.
