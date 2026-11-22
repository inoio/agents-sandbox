# One-command KVM-guest agent session — implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or
> superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A single `make kvm-agent-up` that ensures the KVM guest is running, waits for cloud-init, syncs the checkout,
provisions the agent config, and attaches an interactive agent session with `LITELLM_API_KEY` from the host
environment.

**Architecture:** A new `agent-up` subcommand in `scripts/kvm-guest.sh` composes the existing subcommands
(`create`/`start`, `sync_in`, `agent_provision`, `agent_in`) behind an idempotent ensure-running step and a cloud-init
gate. `make kvm-agent-up` is a thin wrapper that fails fast when the key is unset and passes the credential inline. All
lower-level `kvm-*` targets stay unchanged.

**Tech Stack:** Bash (`scripts/kvm-guest.sh`), GNU Make (`Makefile`), Go test harness (`test/scripts/`) with faked
`virsh`/`ssh`/`rsync`/`git`.

**Spec:** `.operator-shared/plans/2026-11-22-kvm-agent-up-design.md`

## Global Constraints

- Target platform: Linux (KVM host only). Never runs in CI; `make compile-all` compiles the harness, nothing runs the
  flow.
- Extend `scripts/kvm-guest.sh` and `test/scripts/kvm_guest_test.go`; do not add a parallel script.
- The command is embedded in a single-quoted remote SSH string (`agent_in`), so it must not contain a single quote.
- `set -euo pipefail` is in force; every command that may legitimately fail inside a condition must sit in `if`/`||`.
- Run `make fmt`/`golangci-lint` on Go, `make shellcheck` on the script, before committing.
- Keep the existing lower-level targets (`kvm-up`, `kvm-sync`, `kvm-agent-provision`, `kvm-agent`, …) intact.

## Review Focus

The input classes or conditions most likely to bite a user of this target, and the task that pins each:

1. **An already-running guest is recreated.** `virsh start` on a running domain fails and `check_host` refuses an
   existing domain, so the ensure step must branch on `domstate` first — Task 1.
2. **A stopped guest is destroyed and rebuilt.** A defined-but-stopped domain (after a host reboot) must be started, not
   `create`d — Task 1.
3. **A failed cloud-init is treated as ready.** Syncing into a half-provisioned guest must not happen; a non-zero
   `cloud-init status --wait` must abort before `sync_in` — Task 1.
4. **A credential with a single quote silently corrupts the remote command.** It must fail fast, before any guest
   contact — Task 2.
5. **Steps run out of order.** `cloud-init` gate must precede sync, provision, and attach — Task 1.

The undefined/fresh-guest branch is delegated unchanged to `create()`, which needs a real hypervisor (the harness cannot
fake `/dev/kvm`); it stays covered by the host accept in `guides/kvm-agent-testing.md` (T1/T6).

---

### Task 1: `agent-up` orchestration in `scripts/kvm-guest.sh`

**Files:**
- Modify: `scripts/kvm-guest.sh` (top defaults ~L84; `usage` L123-143; `agent_in` L497-504; `create` L627-641; dispatch
  L659-672; add functions near `create`)
- Test: `test/scripts/kvm_guest_test.go` (fakes at L462-525; new cases)

**Interfaces:**
- Consumes: existing `virsh_cmd`, `guest_ip`, `require_guest_ip`, `create`, `sync_in`, `agent_provision`, `agent_in`,
  `log`, `die`.
- Produces (shell functions, no return values except where noted):
  - `wait_for_lease()` — prints the guest IP on stdout, logs progress/warning to stderr; empty on timeout.
  - `ensure_running()` — create/start/no-op so the guest is running.
  - `wait_for_cloud_init()` — returns 0 when cloud-init finished, `die`s otherwise.
  - `agent_up "$@"` — runs ensure → cloud-init → sync → provision → attach; `agent_in` is last (it `exec`s).

- [ ] **Step 1: Add the fakes and write the failing tests**

In `test/scripts/kvm_guest_test.go`, add a `domstate` branch to `kvmGuestVirshHandler` (after the `dominfo` case):

```go
	case strings.Contains(joined, "domstate"):
		return envOption("VIRSH_DOMSTATE") + "\n", "", 0
```

Replace `kvmGuestSSHHandler` with a version that can fail the cloud-init probe:

```go
func kvmGuestSSHHandler(args []string) (string, string, int) {
	recordArgv("SSH_ARGV", args)
	if envOption("SSH_CLOUD_INIT_FAIL") != "" && strings.Contains(strings.Join(args, " "), "cloud-init status") {
		return "", "Cloud-init failed: see status --long\n", 1
	}
	return "", "", 0
}
```

Add the shared env helper and the five cases:

```go
// agentUpEnv is the environment the agent-up cases share: fakes for the commands
// the flow drives, a temp SOURCE_DIR so sync has something to send, and a temp
// HOME with no launcher config so provisioning is a no-op.
func agentUpEnv(t *testing.T, dir string) map[string]string {
	t.Helper()
	home := filepath.Join(dir, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatalf("create home: %v", err)
	}
	env := kvmGuestEnv(dir)
	env["HOME"] = home
	env["SOURCE_DIR"] = filepath.Join(dir, "checkout")
	env["SSH_ARGV"] = filepath.Join(dir, "ssh-argv")
	env["VIRSH_ARGV"] = filepath.Join(dir, "virsh-argv")
	env["PATH"] = kvmGuestPath(t, "virsh", "ssh", "rsync", "git")
	return env
}

func TestKvmGuestAgentUpLeavesARunningGuestAlone(t *testing.T) {
	dir := t.TempDir()
	env := agentUpEnv(t, dir)
	env["VIRSH_DOMINFO_OK"] = "1"
	env["VIRSH_DOMSTATE"] = "running"

	result := RunScript(t, dir, env, repositoryScript(t, "kvm-guest.sh"), "agent-up", "opencode2")
	assertStatus(t, result, 0)

	virshArgv := readText(t, filepath.Join(dir, "virsh-argv"))
	assertNotContains(t, "virsh argv", virshArgv, "define")
	assertNotContains(t, "virsh argv", virshArgv, "start")
}

func TestKvmGuestAgentUpStartsAStoppedGuest(t *testing.T) {
	dir := t.TempDir()
	env := agentUpEnv(t, dir)
	env["VIRSH_DOMINFO_OK"] = "1"
	env["VIRSH_DOMSTATE"] = "shut off"

	result := RunScript(t, dir, env, repositoryScript(t, "kvm-guest.sh"), "agent-up", "opencode2")
	assertStatus(t, result, 0)

	virshArgv := readText(t, filepath.Join(dir, "virsh-argv"))
	assertContains(t, "virsh argv", virshArgv, "start asb-kvm-test")
	assertNotContains(t, "virsh argv", virshArgv, "define")
}

func TestKvmGuestAgentUpGatesOnCloudInitBeforeSyncing(t *testing.T) {
	dir := t.TempDir()
	env := agentUpEnv(t, dir)
	env["VIRSH_DOMINFO_OK"] = "1"
	env["VIRSH_DOMSTATE"] = "running"

	result := RunScript(t, dir, env, repositoryScript(t, "kvm-guest.sh"),
		"agent-up", "LITELLM_API_KEY=secret opencode2")
	assertStatus(t, result, 0)

	sshArgv := readText(t, filepath.Join(dir, "ssh-argv"))
	cloudInit := strings.Index(sshArgv, "cloud-init status --wait")
	provision := strings.Index(sshArgv, "config provision")
	attach := strings.Index(sshArgv, "tmux new")
	if cloudInit < 0 || provision < 0 || attach < 0 {
		t.Fatalf("agent-up skipped a step:\n%s", sshArgv)
	}
	if !(cloudInit < provision && provision < attach) {
		t.Fatalf("steps out of order (cloud-init=%d provision=%d attach=%d):\n%s",
			cloudInit, provision, attach, sshArgv)
	}
	assertContains(t, "ssh argv", sshArgv, "LITELLM_API_KEY=secret opencode2")
}

func TestKvmGuestAgentUpFailsWhenCloudInitFails(t *testing.T) {
	dir := t.TempDir()
	env := agentUpEnv(t, dir)
	env["VIRSH_DOMINFO_OK"] = "1"
	env["VIRSH_DOMSTATE"] = "running"
	env["SSH_CLOUD_INIT_FAIL"] = "1"

	result := RunScript(t, dir, env, repositoryScript(t, "kvm-guest.sh"), "agent-up", "opencode2")
	assertStatus(t, result, 1)

	assertContains(t, "stderr", result.Stderr, "cloud-init failed")
	assertNotContains(t, "ssh argv", readText(t, filepath.Join(dir, "ssh-argv")), "tmux new")
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test -tags microsandbox_ffi_path -run KvmGuestAgentUp ./test/scripts/`
Expected: FAIL — `agent-up` is an unknown subcommand (exit 2, usage printed).

- [ ] **Step 3: Add the env defaults**

In `scripts/kvm-guest.sh`, after line 84 (`KVM_IMAGE_CACHE=…`) add:

```bash
# The cloud-init gate waits for sshd, then for cloud-init itself. The attempts ×
# interval bound only how long the guest may take to accept SSH; the
# `cloud-init status --wait` that follows blocks until cloud-init reports.
KVM_CLOUD_INIT_ATTEMPTS="${KVM_CLOUD_INIT_ATTEMPTS:-120}"
KVM_CLOUD_INIT_INTERVAL="${KVM_CLOUD_INIT_INTERVAL:-5}"
```

- [ ] **Step 4: Extract `wait_for_lease` from `create`**

Replace the lease-wait block in `create` (the `local timeout=… waited=0 ip=""` … through `probe_guest_egress "$ip"`)
with a call, and add the helper above `create`:

```bash
# wait_for_lease blocks until the guest has a DHCP lease, printing progress, and
# warns (printing nothing) on timeout. It is the boot gate `create` and the
# ensure-running path share.
wait_for_lease() {
  local timeout="${KVM_BOOT_TIMEOUT:-300}" waited=0 ip=""
  log "waiting up to ${timeout}s for a DHCP lease (cloud-init first boot takes a few minutes)"
  while [ "$waited" -lt "$timeout" ]; do
    ip="$(guest_ip)"
    [ -n "$ip" ] && break
    sleep 5
    waited=$((waited + 5))
    [ $((waited % 30)) -eq 0 ] && log "  ...still waiting (${waited}s)"
  done
  if [ -z "$ip" ]; then
    log "warn: no DHCP lease after ${timeout}s — the guest may still be booting"
    log "  lease:     virsh -c $LIBVIRT_URI net-dhcp-leases default"
    log "  console:   virsh -c $LIBVIRT_URI console $VM_NAME   (leave with Ctrl-])"
    log "  retry ssh: $0 ssh"
  fi
  printf '%s\n' "$ip"
}
```

In `create`, the tail becomes:

```bash
  local ip=""
  ip="$(wait_for_lease)"
  print_next_steps "$ip"
  probe_guest_egress "$ip"
```

- [ ] **Step 5: Add `ensure_running`, `wait_for_cloud_init`, `agent_up`**

Add after `agent_provision`:

```bash
# ensure_running brings the guest to a running state without recreating an
# existing domain: undefined is created, a stopped domain is started (sharing
# the boot path's lease wait), a running one is left alone. `create`'s
# check_host refuses an existing domain, so it is never called over one.
ensure_running() {
  if ! virsh_cmd dominfo "$VM_NAME" >/dev/null 2>&1; then
    create
    return
  fi
  if [ "$(virsh_cmd domstate "$VM_NAME" 2>/dev/null | tr -d '[:space:]')" = "running" ]; then
    log "guest '$VM_NAME' is running"
    return
  fi
  log "starting the stopped guest '$VM_NAME'"
  virsh_cmd start "$VM_NAME" >/dev/null
  wait_for_lease >/dev/null
}

# wait_for_cloud_init gates on the guest being provisioned: it retries the SSH
# connect (sshd accepts well after the DHCP lease appears), then runs
# `cloud-init status --wait`, failing when cloud-init reports an error.
wait_for_cloud_init() {
  local ip="$1" attempt=0 status
  while [ "$attempt" -lt "$KVM_CLOUD_INIT_ATTEMPTS" ]; do
    if ssh -o BatchMode=yes -o StrictHostKeyChecking=accept-new -o ConnectTimeout=8 \
        -o LogLevel=ERROR "$VM_USER@$ip" true >/dev/null 2>&1; then
      break
    fi
    attempt=$((attempt + 1))
    sleep "$KVM_CLOUD_INIT_INTERVAL"
  done
  [ "$attempt" -lt "$KVM_CLOUD_INIT_ATTEMPTS" ] \
    || die "guest ssh did not come up; check the console: virsh -c $LIBVIRT_URI console $VM_NAME"
  log "waiting for cloud-init to finish in '$VM_NAME'"
  if ! status="$(ssh -o BatchMode=yes -o StrictHostKeyChecking=accept-new -o ConnectTimeout=8 \
        "$VM_USER@$ip" 'cloud-init status --wait' 2>&1)"; then
    printf '%s\n' "$status" >&2
    die "cloud-init failed in '$VM_NAME'; inspect: $0 ssh -- cloud-init status --long"
  fi
  log "cloud-init: done"
}

# agent_up is the one-command bring-up: ensure the guest is running, gate on
# cloud-init, sync this checkout, materialize the agent config, and attach an
# interactive session. It composes the subcommands so the step-by-step targets
# remain available.
agent_up() {
  local ip
  ensure_running
  ip="$(require_guest_ip)"
  wait_for_cloud_init "$ip"
  sync_in
  agent_provision "${KVM_AGENT_NAME:-opencode2}"
  agent_in "$@"
}
```

- [ ] **Step 6: Wire the dispatch and usage**

In `usage`, add to the command list and body:

```text
  agent-up [cmd]    ensure the guest is up, wait for cloud-init, sync, provision,
                    and attach an agent session (one-command bring-up)
```

In the `case`, add before `agent)`:

```bash
  agent-up)        shift; agent_up "$@" ;;
```

- [ ] **Step 7: Run the tests to verify they pass**

Run: `go test -tags microsandbox_ffi_path -run KvmGuest ./test/scripts/`
Expected: PASS (including the pre-existing `KvmGuest*` cases).

- [ ] **Step 8: Lint and commit**

Run: `make shellcheck && make fmt`
Then:
```bash
git add scripts/kvm-guest.sh test/scripts/kvm_guest_test.go
git commit -m "feat(kvm-guest): add an agent-up bring-up subcommand"
```

---

### Task 2: Reject a single quote in the embedded command

**Files:**
- Modify: `scripts/kvm-guest.sh` (`agent_in` L497-504; `agent_up` from Task 1)
- Test: `test/scripts/kvm_guest_test.go`

**Interfaces:**
- Produces: `require_no_single_quote <command>` — `die`s when the argument contains `'`.
- Consumes: `die`.

- [ ] **Step 1: Write the failing tests**

```go
func TestKvmGuestAgentUpRejectsASingleQuoteInTheCommand(t *testing.T) {
	dir := t.TempDir()
	env := agentUpEnv(t, dir)

	result := RunScript(t, dir, env, repositoryScript(t, "kvm-guest.sh"),
		"agent-up", "LITELLM_API_KEY=a'b opencode2")
	assertStatus(t, result, 1)

	assertContains(t, "stderr", result.Stderr, "single quote")
	// Rejected before any guest contact.
	assertNotContains(t, "ssh argv", readText(t, filepath.Join(dir, "ssh-argv")), "cloud-init")
}

func TestKvmGuestAgentRejectsASingleQuoteInTheCommand(t *testing.T) {
	dir := t.TempDir()
	env := kvmGuestEnv(dir)
	env["SSH_ARGV"] = filepath.Join(dir, "ssh-argv")
	env["PATH"] = kvmGuestPath(t, "virsh", "ssh")

	result := RunScript(t, dir, env, repositoryScript(t, "kvm-guest.sh"), "agent", "opencode2'")
	assertStatus(t, result, 1)

	assertContains(t, "stderr", result.Stderr, "single quote")
}
```

- [ ] **Step 2: Run to verify they fail**

Run: `go test -tags microsandbox_ffi_path -run 'KvmGuestAgent(Up)?Rejects' ./test/scripts/`
Expected: FAIL — no such guard; the quote is embedded and the fake `ssh` succeeds (exit 0).

- [ ] **Step 3: Add the helper and call it**

Add near `die`:

```bash
# require_no_single_quote rejects a command that cannot be embedded in the
# single-quoted remote string `agent_in` builds. A quote would end the string
# early and corrupt the remote command silently.
require_no_single_quote() {
  case "$1" in
    *"'"*) die "the agent command must not contain a single quote (it is embedded in a single-quoted remote command)" ;;
  esac
}
```

In `agent_in`, after `command="${*:-opencode2}"`, add:

```bash
  require_no_single_quote "$command"
```

In `agent_up`, add the same guard as the first statement of the body, so a bad credential is rejected before any guest
contact:

```bash
  require_no_single_quote "${*:-opencode2}"
```

- [ ] **Step 4: Run to verify they pass**

Run: `go test -tags microsandbox_ffi_path -run KvmGuest ./test/scripts/`
Expected: PASS.

- [ ] **Step 5: Lint and commit**

Run: `make shellcheck && make fmt`
Then:
```bash
git add scripts/kvm-guest.sh test/scripts/kvm_guest_test.go
git commit -m "fix(kvm-guest): reject a single quote in the embedded agent command"
```

---

### Task 3: `make kvm-agent-up`

**Files:**
- Modify: `Makefile` (`.PHONY` line L1; after the `kvm-agent-provision` target L342-343)

**Interfaces:**
- Consumes: `scripts/kvm-guest.sh agent-up <cmd>` (Task 1); the `KVM_AGENT` variable (default `opencode2`).

- [ ] **Step 1: Add the target**

Add `kvm-agent-up` to the `.PHONY` list (next to `kvm-agent`), and add the target after `kvm-agent-provision`:

```make
# One command from nothing to an interactive guest agent session: ensure the
# guest is running (idempotent — a created, stopped, or running guest all work),
# wait for cloud-init, sync this checkout, materialize the agent config, and
# attach. LITELLM_API_KEY is read from the host environment and handed to the
# agent; extend the credential set with KVM_AGENT='GITHUB_PAT=… opencode2'. The
# step-by-step targets above stay for manual control.
kvm-agent-up:
	@test -n "$$LITELLM_API_KEY" || { printf 'error: set LITELLM_API_KEY\n' >&2; exit 1; }
	scripts/kvm-guest.sh agent-up "LITELLM_API_KEY=$$LITELLM_API_KEY $(KVM_AGENT)"
```

- [ ] **Step 2: Verify the target shape**

Run: `LITELLM_API_KEY= make -n kvm-agent-up`
Expected: exit 1 with `error: set LITELLM_API_KEY` on stderr (make runs the guard; `$$LITELLM_API_KEY` is empty).
Run: `LITELLM_API_KEY=secret make -n kvm-agent-up`
Expected: prints the `scripts/kvm-guest.sh agent-up "LITELLM_API_KEY=secret opencode2"` recipe line.

- [ ] **Step 3: Commit**

```bash
git add Makefile
git commit -m "feat(kvm-guest): add make kvm-agent-up one-command bring-up"
```

---

### Task 4: Docs, changelog, brain catalog

**Files:**
- Modify: `.operator-shared/guides/kvm-guest.md` (target list L27-34; agent section L125-149)
- Modify: `.operator-shared/guides/kvm-agent-testing.md` (intro L12-13; T2/T3 preamble L76-109)
- Modify: `.operator-shared/catalog.md` (the `plans/` entries)
- Create: `changelog.d/<PR-number>.added.md`

**Interfaces:** none (documentation).

- [ ] **Step 1: Update `guides/kvm-guest.md`**

In the target list add:
```text
make kvm-agent-up           # one command: ensure up + cloud-init + sync + provision + attach (LITELLM_API_KEY)
```
In the agent section, lead with `make kvm-agent-up` as the one-command path and keep the step-by-step commands as the
manual breakdown. State that it reads `LITELLM_API_KEY` from the environment, extends via `KVM_AGENT`, and that
lower-level targets still work.

- [ ] **Step 2: Update `guides/kvm-agent-testing.md`**

Note at the top of the manual workflow that `make kvm-agent-up` is the one-command path, and that T1–T6 remain the
per-step accept detail.

- [ ] **Step 3: Add the changelog fragment**

```bash
gh pr list --state all --limit 1 --json number -q '.[0].number'
```
Create `changelog.d/<that number + 1>.added.md` (rename after the PR exists) with one bullet:
```text
Add `make kvm-agent-up`, a one-command bring-up that ensures the KVM guest is running, waits for cloud-init, syncs the checkout, provisions the agent config, and attaches an agent session with `LITELLM_API_KEY` from the environment.
```

- [ ] **Step 4: Update the brain catalog**

In `.operator-shared/catalog.md`, add under `plans/`:
- `2026-11-22-kvm-agent-up-design.md` — Description: design for the one-command KVM-guest agent bring-up
  (`make kvm-agent-up`, the `agent-up` subcommand). Read If: working on `make kvm-agent-up` or `scripts/kvm-guest.sh`'s
  `agent-up`/`ensure_running`/`wait_for_cloud_init`.
- `2026-11-22-kvm-agent-up.md` — Description: implementation plan for the same; retire when folded into
  `guides/kvm-guest.md`. Read If: executing or reviewing that plan.

- [ ] **Step 5: Verify docs and commit**

Run: `make docs-linkcheck && make check` (or `make check-local-changes`) — PASS.
Then:
```bash
git add .operator-shared/guides/kvm-guest.md .operator-shared/guides/kvm-agent-testing.md \
        .operator-shared/catalog.md changelog.d/
git commit -m "docs(kvm-guest): document make kvm-agent-up"
```
