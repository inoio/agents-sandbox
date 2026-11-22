# GitHub CLI in the KVM guest — implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or
> superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Install `gh` in the KVM guest and sign it in from a host-environment `GITHUB_PAT` on `make kvm-agent-up`,
so the in-guest agent can read/comment on GitHub issues and PRs and push branches / open PRs.

**Architecture:** `scripts/kvm-guest.sh`'s cloud-init `write_seed_files` installs a pinned `gh` release `.deb`; a new
`gh_auth` function, called by `agent_up` after `agent_provision` and before `agent_in`, pipes `GITHUB_PAT` over SSH
stdin into `gh auth login --with-token` + `gh auth setup-git` + SSH→HTTPS `insteadOf` rewrites. `agent_provision` stops
copying the whole host `~/.gitconfig`, writing only `user.name`/`user.email`. `make kvm-agent-up` forwards `GITHUB_PAT`.

**Tech Stack:** Bash (`scripts/kvm-guest.sh`), GNU Make (`Makefile`), the Go fixture harness `test/scripts/` (faked
`virsh`/`ssh`/`rsync`/`git` via re-exec'd handlers), Jekyll-free operator docs.

**Spec:** `.operator-shared/plans/2026-11-22-kvm-guest-gh-design.md`

## Global Constraints

- Target platform: Linux (KVM host only). Never runs in CI; `make compile-all` compiles the harness, nothing runs the
  flow. The `msb` suites need a real hypervisor, so the end-to-end result is a human accept.
- Extend `scripts/kvm-guest.sh` and `test/scripts/kvm_guest_test.go`; do not add a parallel script.
- `set -euo pipefail` is in force; every command that may legitimately fail inside a condition must sit in `if`/`||`,
  and a group feeding a pipe must not end on a possibly-false `[ … ] && …` (pipefail would fail the pipeline).
- The `agent_in` command is embedded in a single-quoted remote string; the `require_safe_agent_command` guard rejects
  `'`, `"`, `$`, backtick, `\`. The new `gh_auth` remote command must contain none of those inside its `bash -lc '…'`.
- Never name the secret `GH_TOKEN`/`GITHUB_TOKEN`: `gh auth login --with-token` refuses to store a token under either
  name (`docs/recipes/git-access.md`).
- Run `make shellcheck && make fmt` on the script, `make lint` on Go, before committing. Use `make check` before
  finalizing.
- `GH_VERSION` and the gh install mirror `.agents-sandbox/Dockerfile` (`ARG GH_VERSION=2.104.0`).

## Review Focus

The input classes or conditions most likely to bite a user of this target, and the task that pins each:

1. **A PAT carrying shell metacharacters corrupts the remote command.** It must ride SSH stdin, never argv — Task 3.
2. **`GITHUB_PAT` unset or empty.** `make kvm-agent-up` must behave exactly as before, with no gh contact — Task 3.
3. **`gh` is absent (a guest booted before this change).** Sign-in must fail loudly and name the fix, not pass or
   silently skip — Task 3.
4. **The host carries no `user.name`/`user.email`.** `agent_provision` must write nothing and keep the guest's own
   identity, not an empty/broken `~/.gitconfig` — Task 2.
5. **`agent_provision` runs after `gh_auth` and wipes gh's config.** The identity write rewrites `~/.gitconfig`, so it
   must precede the sign-in — Task 3 (order assertion).

---

### Task 1: Install `gh` in the guest

**Files:**
- Modify: `scripts/kvm-guest.sh` (header env overrides ~L45-57; version block after the `GOLANGCI_LINT_VERSION` block
  ~L108-110; `write_seed_files` `local` list ~L190 and the `runcmd` heredoc ~L241-259)
- Test: `test/scripts/kvm_guest_test.go` (`TestKvmGuestRenderWritesCloudInitAndDomain` ~L54-86)

**Interfaces:**
- Consumes: `repo_root`, `GO_ARCH`, `write_seed_files`'s existing locals.
- Produces: a `GH_VERSION` shell variable (`$GH_VERSION`) and a `gh_install` local string embedded in the seed.

- [ ] **Step 1: Write the failing test**

In `TestKvmGuestRenderWritesCloudInitAndDomain`, add `GH_VERSION` to the env and assertions. Change the env line to set
it, and add after the `golangci-lint.run/install.sh` assertion:

```go
env := kvmGuestEnv(dir)
env["GH_VERSION"] = "9.9.9"
env["KVM_IMAGE_CACHE"] = cacheDir
```

```go
	// The guest carries the GitHub CLI so the agent can work with issues and PRs.
	assertContains(t, "user-data", userData, "https://github.com/cli/cli/releases/download/v9.9.9/gh_9.9.9_linux_amd64.deb")
	assertContains(t, "user-data", userData, "dpkg -i /tmp/gh.deb")
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test -tags microsandbox_ffi_path -run KvmGuestRenderWritesCloudInitAndDomain ./test/scripts/`
Expected: FAIL — user-data has no gh download line.

- [ ] **Step 3: Derive `GH_VERSION`**

In `scripts/kvm-guest.sh`, after the `GOLANGCI_LINT_VERSION` block (~L110) add:

```bash
if [ -z "${GH_VERSION:-}" ]; then
  GH_VERSION="$(sed -n 's/^ARG GH_VERSION=//p' "$repo_root/.agents-sandbox/Dockerfile" 2>/dev/null | head -1 | tr -d '[:space:]' || true)"
  [ -n "$GH_VERSION" ] || GH_VERSION="2.104.0"
fi
```

In the header env-overrides block, add a line with the other pins:

```
#   GH_VERSION=<from .agents-sandbox/Dockerfile>  # the gh CLI installed in the guest
```

- [ ] **Step 4: Build the install fragment and add it to `runcmd`**

In `write_seed_files`, add `gh_install` to the `local` declaration line and build it next to `opencode2_install`:

```bash
  # gh: the GitHub CLI, installed from the pinned release .deb the same way the
  # dev image does, so the in-guest agent can work with issues, PRs, and pushes.
  gh_install="curl -fsSL https://github.com/cli/cli/releases/download/v$GH_VERSION/gh_${GH_VERSION}_linux-$GO_ARCH.deb -o /tmp/gh.deb && dpkg -i /tmp/gh.deb && rm -f /tmp/gh.deb"
```

In the `runcmd` heredoc, add the line after `$golangci_line`:

```
  - $gh_install
```

- [ ] **Step 5: Run the test to verify it passes**

Run: `go test -tags microsandbox_ffi_path -run KvmGuest ./test/scripts/`
Expected: PASS.

- [ ] **Step 6: Lint and commit**

Run: `make shellcheck && make fmt`
Then:
```bash
git add scripts/kvm-guest.sh test/scripts/kvm_guest_test.go
git commit -m "feat(kvm-guest): install the GitHub CLI in the guest"
```

---

### Task 2: Copy only `user.name`/`user.email` into the guest

**Files:**
- Modify: `scripts/kvm-guest.sh` (`agent_provision` git-identity block ~L551-558)
- Test: `test/scripts/kvm_guest_test.go` (replace `TestKvmGuestAgentProvisionCopiesGitIdentityAndDisablesSigning`
  ~L398-429; extend `kvmGuestSSHHandler` ~L682-689; extend `kvmGuestGitHandler` ~L726-732)

**Interfaces:**
- Consumes: `log`, `ssh`.
- Produces: the fixture knobs `SSH_STDIN`/`SSH_STDIN_MATCH` (capture a fake ssh's stdin to a file) and `GIT_USER_NAME`/
  `GIT_USER_EMAIL` (the fake git's answer to `config … user.name`/`user.email`), both reused by Task 3.

- [ ] **Step 1: Add the fakes and write the failing test**

Add `"io"` to the test file's imports. Replace `kvmGuestSSHHandler` with a version that captures stdin on demand:

```go
func kvmGuestSSHHandler(args []string) (string, string, int) {
	recordArgv("SSH_ARGV", args)
	joined := strings.Join(args, " ")
	if path := envOption("SSH_STDIN"); path != "" && envOption("SSH_STDIN_MATCH") != "" &&
		strings.Contains(joined, envOption("SSH_STDIN_MATCH")) {
		data, _ := io.ReadAll(os.Stdin)
		_ = os.WriteFile(path, data, 0o644)
	}
	if envOption("SSH_CLOUD_INIT_FAIL") != "" && strings.Contains(joined, "cloud-init status") {
		return "", "Cloud-init failed: see status --long\n", 1
	}
	return kvmGuestSSHSessionResponse(joined)
}
```

Extend `kvmGuestGitHandler` to answer the identity queries the script makes:

```go
func kvmGuestGitHandler(args []string) (string, string, int) {
	recordArgv("GIT_ARGV", args)
	if envOption("GIT_EXIT") != "" {
		return "", "", 1
	}
	joined := strings.Join(args, " ")
	switch {
	case strings.Contains(joined, "user.name"):
		if value := envOption("GIT_USER_NAME"); value != "" {
			return value + "\n", "", 0
		}
		return "", "", 1
	case strings.Contains(joined, "user.email"):
		if value := envOption("GIT_USER_EMAIL"); value != "" {
			return value + "\n", "", 0
		}
		return "", "", 1
	}
	return "", "", 0
}
```

Replace `TestKvmGuestAgentProvisionCopiesGitIdentityAndDisablesSigning` with:

```go
// TestKvmGuestAgentProvisionCopiesOnlyTheUserIdentity pins that provisioning
// gives the guest a committable identity without shipping the rest of the host's
// ~/.gitconfig (credential helpers, aliases, signing config).
func TestKvmGuestAgentProvisionCopiesOnlyTheUserIdentity(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatalf("create home: %v", err)
	}
	if err := os.WriteFile(filepath.Join(home, ".gitconfig"),
		[]byte("[user]\n\tname = Host User\n\temail = host@example.test\n[credential]\n\thelper = store\n"), 0o644); err != nil {
		t.Fatalf("write host gitconfig: %v", err)
	}
	sshLog := filepath.Join(dir, "ssh-argv")
	stdinLog := filepath.Join(dir, "ssh-stdin")
	rsyncLog := filepath.Join(dir, "rsync-argv")
	env := kvmGuestEnv(dir)
	env["HOME"] = home
	env["GIT_USER_NAME"] = "Host User"
	env["GIT_USER_EMAIL"] = "host@example.test"
	env["SSH_ARGV"] = sshLog
	env["SSH_STDIN"] = stdinLog
	env["SSH_STDIN_MATCH"] = "cat > ~/.gitconfig"
	env["RSYNC_ARGV"] = rsyncLog
	env["PATH"] = kvmGuestPath(t, "virsh", "ssh", "rsync", "git")

	result := RunScript(t, dir, env, repositoryScript(t, "kvm-guest.sh"), "agent-provision")
	assertStatus(t, result, 0)

	// The whole file is not shipped.
	assertNotContains(t, "rsync argv", readText(t, rsyncLog), ".gitconfig")
	// Only the identity is written, and via stdin (so a spaced name needs no quoting).
	identity := readText(t, stdinLog)
	assertContains(t, "git identity", identity, "name = Host User")
	assertContains(t, "git identity", identity, "email = host@example.test")
	assertNotContains(t, "git identity", identity, "credential")
	assertNotContains(t, "git identity", identity, "gpgsign")
	// The signing overrides are gone (signing is off by default now).
	assertNotContains(t, "ssh argv", readText(t, sshLog), "gpgsign")
}

// TestKvmGuestAgentProvisionKeepsTheGuestIdentityWithoutAHostIdentity pins that
// a host with no user.name/user.email writes nothing, rather than an empty or
// broken ~/.gitconfig.
func TestKvmGuestAgentProvisionKeepsTheGuestIdentityWithoutAHostIdentity(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatalf("create home: %v", err)
	}
	sshLog := filepath.Join(dir, "ssh-argv")
	env := kvmGuestEnv(dir)
	env["HOME"] = home
	env["SSH_ARGV"] = sshLog
	env["PATH"] = kvmGuestPath(t, "virsh", "ssh", "rsync", "git")

	result := RunScript(t, dir, env, repositoryScript(t, "kvm-guest.sh"), "agent-provision")
	assertStatus(t, result, 0)

	assertNotContains(t, "ssh argv", readText(t, sshLog), "cat > ~/.gitconfig")
	assertContains(t, "provision stderr", result.Stderr, "keeps its own git identity")
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test -tags microsandbox_ffi_path -run KvmGuestAgentProvision ./test/scripts/`
Expected: FAIL — provisioning still rsyncs `.gitconfig` and issues the `gpgsign` commands.

- [ ] **Step 3: Rewrite the identity block in `agent_provision`**

Replace the `if [ -f "$HOME/.gitconfig" ]; then … fi` block at the end of `agent_provision` with:

```bash
  local name email
  name="$(git config --global --get user.name 2>/dev/null || true)"
  email="$(git config --global --get user.email 2>/dev/null || true)"
  if [ -n "$name" ] || [ -n "$email" ]; then
    log "writing the host git identity into $VM_USER@$ip (name and email only)"
    {
      printf '[user]\n'
      if [ -n "$name" ]; then printf '\tname = %s\n' "$name"; fi
      if [ -n "$email" ]; then printf '\temail = %s\n' "$email"; fi
    } | ssh -o StrictHostKeyChecking=accept-new "$VM_USER@$ip" -- "bash -lc 'cat > ~/.gitconfig'"
  else
    log "no host git user.name/user.email; the guest keeps its own git identity"
  fi
```

Also update `agent_provision`'s doc comment: it no longer copies the host `~/.gitconfig`, only the `user` block, and
signing no longer needs disabling.

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test -tags microsandbox_ffi_path -run KvmGuest ./test/scripts/`
Expected: PASS.

- [ ] **Step 5: Lint and commit**

Run: `make shellcheck && make fmt`
Then:
```bash
git add scripts/kvm-guest.sh test/scripts/kvm_guest_test.go
git commit -m "refactor(kvm-guest): copy only the git user identity into the guest"
```

---

### Task 3: Sign `gh` in from `GITHUB_PAT`

**Files:**
- Modify: `scripts/kvm-guest.sh` (add `gh_auth` after `agent_provision` ~L559; restructure `agent_up` ~L624-644;
  header usage text ~L36-38)
- Test: `test/scripts/kvm_guest_test.go` (new cases; extend `kvmGuestSSHHandler` ~L682-689)

**Interfaces:**
- Consumes: `GITHUB_PAT` (from the environment), `require_guest_ip`'s `ip`, `log`, `die`, `ssh`, and Task 2's
  `SSH_STDIN`/`SSH_STDIN_MATCH`/`GIT_USER_NAME`/`GIT_USER_EMAIL` knobs.
- Produces: `gh_auth <ip>` — returns 0 (signing in, or no-op when `GITHUB_PAT` is unset), `die`s on failure.

- [ ] **Step 1: Add the failing test's fake and the cases**

Extend `kvmGuestSSHHandler` (added in Task 2) to fail the sign-in when asked; insert this check before the
`SSH_CLOUD_INIT_FAIL` branch:

```go
	if envOption("SSH_GH_AUTH_FAIL") != "" && strings.Contains(joined, "gh auth login") {
		return "", "error: authentication failed\n", 1
	}
```

Add the cases:

```go
// TestKvmGuestAgentUpSignsGhInFromTheHostPat pins that a host-environment
// GITHUB_PAT signs gh in inside the guest and enables push, and that the token
// rides ssh stdin, never the command line.
func TestKvmGuestAgentUpSignsGhInFromTheHostPat(t *testing.T) {
	dir := t.TempDir()
	env := agentUpEnv(t, dir)
	env["VIRSH_DOMINFO_OK"] = "1"
	env["VIRSH_DOMSTATE"] = "running"
	env["GITHUB_PAT"] = "ghp_secret"
	env["SSH_STDIN"] = filepath.Join(dir, "ssh-stdin")
	env["SSH_STDIN_MATCH"] = "gh auth login"

	result := RunScript(t, dir, env, repositoryScript(t, "kvm-guest.sh"), "agent-up", "opencode2")
	assertStatus(t, result, 0)

	sshArgv := readText(t, filepath.Join(dir, "ssh-argv"))
	assertContains(t, "ssh argv", sshArgv, "gh auth login --with-token")
	assertContains(t, "ssh argv", sshArgv, "gh auth setup-git")
	assertContains(t, "ssh argv", sshArgv, "url.https://github.com/.insteadOf git@github.com:")
	assertContains(t, "gh stdin", readText(t, filepath.Join(dir, "ssh-stdin")), "ghp_secret")
	assertNotContains(t, "ssh argv", sshArgv, "ghp_secret")
}

// TestKvmGuestAgentUpSkipsGhSignInWithoutAPat pins the no-op: an unset token
// means the flow behaves exactly as before.
func TestKvmGuestAgentUpSkipsGhSignInWithoutAPat(t *testing.T) {
	dir := t.TempDir()
	env := agentUpEnv(t, dir)
	env["VIRSH_DOMINFO_OK"] = "1"
	env["VIRSH_DOMSTATE"] = "running"

	result := RunScript(t, dir, env, repositoryScript(t, "kvm-guest.sh"), "agent-up", "opencode2")
	assertStatus(t, result, 0)

	assertNotContains(t, "ssh argv", readText(t, filepath.Join(dir, "ssh-argv")), "gh auth")
}

// TestKvmGuestAgentUpWritesTheIdentityBeforeGhSignIn pins the order: the
// identity step rewrites the guest ~/.gitconfig, so it must run before gh
// configures its credential helper there.
func TestKvmGuestAgentUpWritesTheIdentityBeforeGhSignIn(t *testing.T) {
	dir := t.TempDir()
	env := agentUpEnv(t, dir)
	env["VIRSH_DOMINFO_OK"] = "1"
	env["VIRSH_DOMSTATE"] = "running"
	env["GITHUB_PAT"] = "ghp_secret"
	env["GIT_USER_NAME"] = "Host User"
	env["GIT_USER_EMAIL"] = "host@example.test"

	result := RunScript(t, dir, env, repositoryScript(t, "kvm-guest.sh"), "agent-up", "opencode2")
	assertStatus(t, result, 0)

	sshArgv := readText(t, filepath.Join(dir, "ssh-argv"))
	identity := strings.Index(sshArgv, "cat > ~/.gitconfig")
	signIn := strings.Index(sshArgv, "gh auth login")
	if identity < 0 || signIn < 0 {
		t.Fatalf("agent-up skipped a step (identity=%d sign-in=%d):\n%s", identity, signIn, sshArgv)
	}
	if identity >= signIn {
		t.Fatalf("identity written after gh sign-in (identity=%d sign-in=%d):\n%s", identity, signIn, sshArgv)
	}
}

// TestKvmGuestAgentUpFailsWhenGhSignInFails pins that a missing or rejected gh
// is fatal, naming the rebuild.
func TestKvmGuestAgentUpFailsWhenGhSignInFails(t *testing.T) {
	dir := t.TempDir()
	env := agentUpEnv(t, dir)
	env["VIRSH_DOMINFO_OK"] = "1"
	env["VIRSH_DOMSTATE"] = "running"
	env["GITHUB_PAT"] = "ghp_secret"
	env["SSH_GH_AUTH_FAIL"] = "1"

	result := RunScript(t, dir, env, repositoryScript(t, "kvm-guest.sh"), "agent-up", "opencode2")
	assertStatus(t, result, 1)

	assertContains(t, "stderr", result.Stderr, "gh sign-in failed")
	assertNotContains(t, "ssh argv", readText(t, filepath.Join(dir, "ssh-argv")), "tmux new")
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test -tags microsandbox_ffi_path -run KvmGuestAgentUp ./test/scripts/`
Expected: FAIL — no `gh auth` call is made.

- [ ] **Step 3: Add `gh_auth`**

After `agent_provision` in `scripts/kvm-guest.sh`:

```bash
# gh_auth signs the guest's `gh` in to GitHub from the host environment's
# GITHUB_PAT, so the in-guest agent can read and comment on issues and PRs and
# push. It also points git at gh's credential helper and rewrites SSH GitHub
# remotes to HTTPS, so a push from the guest's HTTPS clone authenticates. It is a
# no-op when GITHUB_PAT is unset, and the token rides stdin, never argv. It must
# run after `agent_provision`, which rewrites the guest ~/.gitconfig.
gh_auth() {
  local ip="$1"
  if [ -z "${GITHUB_PAT:-}" ]; then
    return 0
  fi
  log "signing gh in to GitHub in '$VM_NAME'"
  if ! printf '%s\n' "$GITHUB_PAT" | ssh -o StrictHostKeyChecking=accept-new "$VM_USER@$ip" \
      -- "bash -lc 'gh auth login --with-token && gh auth setup-git && git config --global url.https://github.com/.insteadOf git@github.com: && git config --global --add url.https://github.com/.insteadOf ssh://git@github.com/'"; then
    die "gh sign-in failed in '$VM_NAME'; a guest booted before gh was installed has none — rebuild with: make kvm-down && make kvm-up"
  fi
}
```

Add to the script header (near the credentials note ~L36-38) one line: gh is signed in from `GITHUB_PAT` when set.

- [ ] **Step 4: Restructure `agent_up`**

Replace the body of `agent_up` with a single `gh_auth` on both paths:

```bash
agent_up() {
  local ip session_state
  require_safe_agent_command "${*:-opencode2}"
  ensure_running
  ip="$(require_guest_ip)"
  wait_for_cloud_init "$ip"
  session_state="$(guest_agent_session_state "$ip" || true)"
  case "$session_state" in
    present)
      log "agent session '${KVM_AGENT_SESSION:-agents-sandbox}' is already running; reattaching without syncing"
      ;;
    absent)
      sync_in
      agent_provision "${KVM_AGENT_NAME:-opencode2}"
      ;;
    *)
      die "could not determine whether an agent session is running in '$VM_NAME' (probe returned '$session_state'); refusing to sync over it"
      ;;
  esac
  gh_auth "$ip"
  agent_in "$@"
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test -tags microsandbox_ffi_path -run KvmGuest ./test/scripts/`
Expected: PASS (including the pre-existing `KvmGuest*` agent-up cases).

- [ ] **Step 6: Lint and commit**

Run: `make shellcheck && make fmt`
Then:
```bash
git add scripts/kvm-guest.sh test/scripts/kvm_guest_test.go
git commit -m "feat(kvm-guest): sign gh in from GITHUB_PAT on agent-up"
```

---

### Task 4: Forward `GITHUB_PAT` from `make kvm-agent-up`

**Files:**
- Modify: `Makefile` (`kvm-agent-up` target and its comment ~L345-355)

**Interfaces:**
- Consumes: `scripts/kvm-guest.sh agent-up <cmd>` (Task 3), which reads `GITHUB_PAT` from its environment.
- Produces: nothing consumed by later tasks.

- [ ] **Step 1: Update the target**

```make
# One command from nothing to an interactive guest agent session: ensure the
# guest is running (idempotent — a created, stopped, or running guest all work),
# wait for cloud-init, sync this checkout, materialize the agent config, and
# attach. LITELLM_API_KEY is read from the host environment and handed to the
# agent; extend the credential set with KVM_AGENT='GITHUB_PAT=… opencode2'. A
# host-environment GITHUB_PAT is forwarded to the guest so gh signs in there (the
# agent can then work with issues and PRs and push); it is optional. If
# KVM_AGENT runs a different binary, set KVM_AGENT_NAME to the profile to
# materialize (default opencode2), so the config matches the agent attached. The
# step-by-step targets above stay for manual control.
kvm-agent-up:
	@test -n "$$LITELLM_API_KEY" || { printf 'error: set LITELLM_API_KEY\n' >&2; exit 1; }
	GITHUB_PAT="$$GITHUB_PAT" KVM_AGENT_NAME="$(KVM_AGENT_NAME)" scripts/kvm-guest.sh agent-up "LITELLM_API_KEY=$$LITELLM_API_KEY $(KVM_AGENT)"
```

- [ ] **Step 2: Verify the target shape**

Run: `LITELLM_API_KEY=secret GITHUB_PAT=ghp_x make -n kvm-agent-up`
Expected: prints a recipe line containing `GITHUB_PAT="ghp_x"` and `scripts/kvm-guest.sh agent-up "LITELLM_API_KEY=secret opencode2"`.

- [ ] **Step 3: Commit**

```bash
git add Makefile
git commit -m "feat(kvm-guest): forward GITHUB_PAT on make kvm-agent-up"
```

---

### Task 5: Docs, changelog, brain catalog

**Files:**
- Modify: `.operator-shared/guides/kvm-guest.md` (workflow target list ~L35; credentials and never-pushes passages
  ~L150-175)
- Modify: `.operator-shared/guides/kvm-agent-testing.md` (`T1` ~L49-60; `T2` ~L103-105; `T3` ~L110-125; `T5` ~L152-154;
  known limitations ~L180)
- Modify: `.operator-shared/plans/2026-11-22-kvm-guest-agent-design.md` (the "No GitHub/PR delivery in v1" non-goal
  ~L38-39)
- Modify: `.operator-shared/catalog.md` (the `plans/` entries)
- Create: `changelog.d/<PR>.added.md`

**Interfaces:** none (documentation).

- [ ] **Step 1: Determine the changelog number**

Run: `gh pr list --state all --limit 1 --json number -q '.[0].number'`
Create `changelog.d/<that number + 1>.added.md` (rename after the PR exists) with:

```text
Install the GitHub CLI (`gh`) in the KVM guest and sign it in from a host-environment `GITHUB_PAT` on `make kvm-agent-up`, so the in-guest agent can work with issues and PRs and push; `agent_provision` now copies only the host's git `user.name`/`user.email` into the guest.
```

- [ ] **Step 2: Update `guides/kvm-guest.md`**

In the workflow block, note that `make kvm-agent-up` reads `GITHUB_PAT` to sign `gh` in. In the agent section, add that
the guest ships `gh`, that `kvm-agent-up` signs it in when `GITHUB_PAT` is set (and configures git for HTTPS push and PR
creation), and rewrite the "Credentials are not provisioned" and "The guest never pushes" passages: the guest can now
push and open PRs when a PAT was supplied, while `kvm-sync-back` stays the no-push return path. Describe the identity
copy as name/email only.

- [ ] **Step 3: Update `guides/kvm-agent-testing.md`**

Add `gh --version` to `T1`. Adjust `T2` to say the host's `user.name`/`user.email` are written (the whole file is
not). Update `T3` to mention `GITHUB_PAT` signs `gh` in, `T5` to say the guest can now push/open PRs when a PAT was
supplied, and remove the "`gh` is not installed on the guest" limitation.

- [ ] **Step 4: Note the superseded non-goal**

In `.operator-shared/plans/2026-11-22-kvm-guest-agent-design.md`, amend the "No GitHub/PR delivery in v1" non-goal to
note it is superseded by `2026-11-22-kvm-guest-gh-design.md` (the guest now signs `gh` in and can push).

- [ ] **Step 5: Update the brain catalog**

In `.operator-shared/catalog.md`, add under `plans/`:
- `2026-11-22-kvm-guest-gh-design.md` — Description: design for `gh` in the KVM guest and its `GITHUB_PAT` sign-in on
  `make kvm-agent-up`. Read If: working on gh provisioning or `kvm-agent-up`'s sign-in.
- `2026-11-22-kvm-guest-gh.md` — Description: implementation plan for the same; retire when folded into
  `guides/kvm-guest.md`. Read If: executing or reviewing that plan.

- [ ] **Step 6: Verify docs and commit**

Run: `make check` — PASS.
Then:
```bash
git add .operator-shared/guides/kvm-guest.md .operator-shared/guides/kvm-agent-testing.md \
        .operator-shared/plans/2026-11-22-kvm-guest-agent-design.md .operator-shared/catalog.md changelog.d/
git commit -m "docs(kvm-guest): document gh and the GITHUB_PAT sign-in"
```
