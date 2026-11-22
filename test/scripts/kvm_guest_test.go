package scripts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// kvmGuestEnv is the base environment the kvm-guest.sh cases share. It pins the
// guest name, size, and repo so the generated cloud-init and domain XML are
// asserted against literals rather than the developer's checkout, and it
// supplies an SSH key as text so the seed does not depend on the host's
// ~/.ssh.
func kvmGuestEnv(artifactDir string) map[string]string {
	return map[string]string{
		"ARTIFACT_DIR":  artifactDir,
		"VM_NAME":       "asb-kvm-test",
		"VM_VCPUS":      "4",
		"VM_MEMORY_MIB": "8192",
		"VM_DISK_GIB":   "40",
		"VM_USER":       "dev",
		"REPO_URL":      "https://example.test/agents-sandbox.git",
		"REPO_BRANCH":   "feat/793-msb-journeys",
		"GO_VERSION":    "1.27.4",
		"SSH_PUBKEY":    "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI test@host",
	}
}

// kvmGuestPath prepends the fake bin directories to the inherited PATH so the
// script under test invokes the fakes instead of the real tools.
func kvmGuestPath(t *testing.T, names ...string) string {
	t.Helper()
	bins := make([]string, 0, len(names)+1)
	for _, name := range names {
		bins = append(bins, BinDir(t, name))
	}
	bins = append(bins, envOption("PATH"))
	return strings.Join(bins, string(os.PathListSeparator))
}

func TestKvmGuestUsageWithoutSubcommand(t *testing.T) {
	result := RunScript(t, t.TempDir(), nil, repositoryScript(t, "kvm-guest.sh"))
	assertStatus(t, result, 2)
	assertContains(t, "usage", result.Stderr, "usage:")
}

func TestKvmGuestRejectsUnknownSubcommand(t *testing.T) {
	result := RunScript(t, t.TempDir(), nil, repositoryScript(t, "kvm-guest.sh"), "boot")
	assertStatus(t, result, 2)
	assertContains(t, "usage", result.Stderr, "usage:")
}

func TestKvmGuestRenderWritesCloudInitAndDomain(t *testing.T) {
	dir := t.TempDir()
	cacheDir := filepath.Join(dir, "image-cache")
	env := kvmGuestEnv(dir)
	env["KVM_IMAGE_CACHE"] = cacheDir
	result := RunScript(t, dir, env, repositoryScript(t, "kvm-guest.sh"), "render")
	assertStatus(t, result, 0)
	assertContains(t, "render stdout", result.Stdout, "resolute/current/resolute-server-cloudimg-")
	assertContains(t, "render stdout", result.Stdout, "image path: "+cacheDir)

	userData := readText(t, filepath.Join(dir, "asb-kvm-test-user-data"))
	assertContains(t, "user-data", userData, "https://example.test/agents-sandbox.git")
	assertContains(t, "user-data", userData, "feat/793-msb-journeys")
	assertContains(t, "user-data", userData, "docker.io")
	assertContains(t, "user-data", userData, "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI test@host")

	// The guest is provisioned to run an agent, not only the suites: opencode2
	// (linked onto PATH), tmux (the session `agent` attaches to), shellcheck, and
	// the Makefile-pinned golangci-lint.
	assertContains(t, "user-data", userData, "opencode.ai/v2/install")
	assertContains(t, "user-data", userData, "~/.local/bin/opencode")
	assertContains(t, "user-data", userData, "- tmux")
	assertContains(t, "user-data", userData, "- shellcheck")
	assertContains(t, "user-data", userData, "golangci-lint.run/install.sh")

	domainXML := readText(t, filepath.Join(dir, "asb-kvm-test.xml"))
	assertContains(t, "domain xml", domainXML, "<name>asb-kvm-test</name>")
	assertContains(t, "domain xml", domainXML, "<memory unit='MiB'>8192</memory>")
	assertContains(t, "domain xml", domainXML, "<vcpu>4</vcpu>")

	metadata := readText(t, filepath.Join(dir, "asb-kvm-test-meta-data"))
	assertContains(t, "meta-data", metadata, "instance-id: asb-kvm-test")
}

func TestKvmGuestRenderDefaultsTheImageCacheToLibvirtDir(t *testing.T) {
	dir := t.TempDir()
	result := RunScript(t, dir, kvmGuestEnv(dir), repositoryScript(t, "kvm-guest.sh"), "render")
	assertStatus(t, result, 0)
	// The default must be a path libvirt's QEMU user can read past AppArmor,
	// so a home-directory default (0644 in 0755 or not) is not enough.
	assertContains(t, "render stdout", result.Stdout, "image path: /var/lib/libvirt/images/agents-sandbox/")
}

func TestKvmGuestFirewallAcceptsTheBridgeInInputAndDockerUser(t *testing.T) {
	dir := t.TempDir()
	argvPath := filepath.Join(dir, "nft-argv")
	env := kvmGuestEnv(dir)
	env["NFT_ARGV"] = argvPath
	env["PATH"] = kvmGuestPath(t, "virsh", "nft", "id")

	result := RunScript(t, dir, env, repositoryScript(t, "kvm-guest.sh"), "firewall")
	assertStatus(t, result, 0)

	argv := readText(t, argvPath)
	assertContains(t, "nft argv", argv, "insert rule ip filter INPUT iifname virbr0 accept")
	assertContains(t, "nft argv", argv, "insert rule ip filter DOCKER-USER iifname virbr0 accept")
	assertContains(t, "nft argv", argv, "insert rule ip filter DOCKER-USER oifname virbr0 accept")
	// FORWARD is rebuilt by Docker on every start, so an insert there is lost.
	if strings.Contains(argv, "FORWARD") {
		t.Fatalf("firewall inserted into FORWARD (Docker rebuilds it):\n%s", argv)
	}
}

func TestKvmGuestRunForwardsTheCommandOverSSH(t *testing.T) {
	dir := t.TempDir()
	argvPath := filepath.Join(dir, "ssh-argv")
	env := kvmGuestEnv(dir)
	env["SSH_ARGV"] = argvPath
	env["PATH"] = kvmGuestPath(t, "virsh", "ssh")

	result := RunScript(t, dir, env, repositoryScript(t, "kvm-guest.sh"), "run", "make", "test-msb-journeys")
	assertStatus(t, result, 0)

	argv := readText(t, argvPath)
	assertContains(t, "ssh argv", argv, "dev@192.168.122.55")
	assertContains(t, "ssh argv", argv, "bash -lc")
	assertContains(t, "ssh argv", argv, "make test-msb-journeys")
}

// TestKvmGuestAgentAllocatesATTYAndRunsInAGuestTmux pins that `agent` opens an
// interactive session: an SSH TTY (the TUI needs one) running the command inside
// a named tmux session, so a dropped connection leaves the agent running.
func TestKvmGuestAgentAllocatesATTYAndRunsInAGuestTmux(t *testing.T) {
	dir := t.TempDir()
	argvPath := filepath.Join(dir, "ssh-argv")
	env := kvmGuestEnv(dir)
	env["SSH_ARGV"] = argvPath
	env["PATH"] = kvmGuestPath(t, "virsh", "ssh")

	result := RunScript(t, dir, env, repositoryScript(t, "kvm-guest.sh"), "agent")
	assertStatus(t, result, 0)

	argv := readText(t, argvPath)
	assertContains(t, "ssh argv", argv, "-tt")
	assertContains(t, "ssh argv", argv, "dev@192.168.122.55")
	assertContains(t, "ssh argv", argv, "tmux new -A -s agents-sandbox")
	assertContains(t, "ssh argv", argv, "opencode2")
}

// TestKvmGuestAgentForwardsAnInlineCredentialCommand pins that `agent` passes a
// caller-supplied command through unchanged, so credentials ride in as an inline
// `VAR=value` assignment the guest shell interprets.
func TestKvmGuestAgentForwardsAnInlineCredentialCommand(t *testing.T) {
	dir := t.TempDir()
	argvPath := filepath.Join(dir, "ssh-argv")
	env := kvmGuestEnv(dir)
	env["SSH_ARGV"] = argvPath
	env["PATH"] = kvmGuestPath(t, "virsh", "ssh")

	result := RunScript(t, dir, env, repositoryScript(t, "kvm-guest.sh"),
		"agent", "LITELLM_API_KEY=secret", "opencode")
	assertStatus(t, result, 0)

	argv := readText(t, argvPath)
	assertContains(t, "ssh argv", argv, "LITELLM_API_KEY=secret opencode")
	assertContains(t, "ssh argv", argv, "tmux new -A -s agents-sandbox")
}

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

// TestKvmGuestAgentUpLeavesARunningGuestAlone pins that the ensure step must not
// recreate an existing domain: `virsh start` on a running domain fails, and
// create's check_host refuses an existing domain outright.
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

// TestKvmGuestAgentUpStartsAStoppedGuest pins that a defined-but-stopped guest
// (after a host reboot) is started, not rebuilt.
func TestKvmGuestAgentUpStartsAStoppedGuest(t *testing.T) {
	dir := t.TempDir()
	env := agentUpEnv(t, dir)
	env["VIRSH_DOMINFO_OK"] = "1"
	env["VIRSH_DOMSTATE"] = "shut off"

	result := RunScript(t, dir, env, repositoryScript(t, "kvm-guest.sh"), "agent-up", "opencode2")
	assertStatus(t, result, 0)

	virshArgv := readText(t, filepath.Join(dir, "virsh-argv"))
	assertContains(t, "virsh argv", virshArgv, "start asb-kvm-test")
	assertContains(t, "virsh argv", virshArgv, "net-info default")
	assertNotContains(t, "virsh argv", virshArgv, "define")
}

// TestKvmGuestAgentUpGatesOnCloudInitBeforeSyncing pins the order: the cloud-init
// gate precedes syncing, provisioning, and attaching.
func TestKvmGuestAgentUpGatesOnCloudInitBeforeSyncing(t *testing.T) {
	dir := t.TempDir()
	env := agentUpEnv(t, dir)
	env["VIRSH_DOMINFO_OK"] = "1"
	env["VIRSH_DOMSTATE"] = "running"
	env["RSYNC_ARGV"] = filepath.Join(dir, "rsync-argv")

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
	if cloudInit >= provision || provision >= attach {
		t.Fatalf("steps out of order (cloud-init=%d provision=%d attach=%d):\n%s",
			cloudInit, provision, attach, sshArgv)
	}
	assertContains(t, "ssh argv", sshArgv, "LITELLM_API_KEY=secret opencode2")
	// The checkout must be synced, so the guest carries `config provision`.
	assertContains(t, "rsync argv", readText(t, filepath.Join(dir, "rsync-argv")),
		"dev@192.168.122.55:~/agents-sandbox/")
}

// TestKvmGuestAgentUpFailsWhenCloudInitFails pins that a failed cloud-init
// aborts before syncing into a half-provisioned guest.
func TestKvmGuestAgentUpFailsWhenCloudInitFails(t *testing.T) {
	dir := t.TempDir()
	env := agentUpEnv(t, dir)
	env["VIRSH_DOMINFO_OK"] = "1"
	env["VIRSH_DOMSTATE"] = "running"
	env["SSH_CLOUD_INIT_FAIL"] = "1"
	env["RSYNC_ARGV"] = filepath.Join(dir, "rsync-argv")

	result := RunScript(t, dir, env, repositoryScript(t, "kvm-guest.sh"), "agent-up", "opencode2")
	assertStatus(t, result, 1)

	assertContains(t, "stderr", result.Stderr, "cloud-init failed")
	assertNotContains(t, "ssh argv", readText(t, filepath.Join(dir, "ssh-argv")), "tmux new")
	// The gate precedes sync: a regression that synced before the cloud-init
	// wait would leave an rsync log here.
	assertNotContains(t, "rsync argv", readText(t, filepath.Join(dir, "rsync-argv")), "agents-sandbox")
}

// TestKvmGuestAgentUpReattachesWithoutSyncingOverALiveSession pins the recovery
// path: re-running the command against a guest that already has the session must
// reattach, not sync_in (one-way, --delete) over the live agent's work.
func TestKvmGuestAgentUpReattachesWithoutSyncingOverALiveSession(t *testing.T) {
	dir := t.TempDir()
	env := agentUpEnv(t, dir)
	env["VIRSH_DOMINFO_OK"] = "1"
	env["VIRSH_DOMSTATE"] = "running"
	env["SSH_AGENT_SESSION"] = "1"
	env["RSYNC_ARGV"] = filepath.Join(dir, "rsync-argv")

	result := RunScript(t, dir, env, repositoryScript(t, "kvm-guest.sh"),
		"agent-up", "LITELLM_API_KEY=secret opencode2")
	assertStatus(t, result, 0)

	sshArgv := readText(t, filepath.Join(dir, "ssh-argv"))
	assertContains(t, "ssh argv", sshArgv, "tmux has-session")
	assertContains(t, "ssh argv", sshArgv, "tmux new -A -s agents-sandbox")
	assertNotContains(t, "ssh argv", sshArgv, "config provision")
	assertNotContains(t, "rsync argv", readText(t, filepath.Join(dir, "rsync-argv")), "agents-sandbox")
}

// TestKvmGuestAgentUpRefusesToSyncWhenTheSessionProbeFails pins that a probe
// that cannot reach the guest is not read as "no live session": the one-way,
// --delete sync must not run on an unknown session state.
func TestKvmGuestAgentUpRefusesToSyncWhenTheSessionProbeFails(t *testing.T) {
	dir := t.TempDir()
	env := agentUpEnv(t, dir)
	env["VIRSH_DOMINFO_OK"] = "1"
	env["VIRSH_DOMSTATE"] = "running"
	env["SSH_PROBE_FAIL"] = "1"
	env["RSYNC_ARGV"] = filepath.Join(dir, "rsync-argv")

	result := RunScript(t, dir, env, repositoryScript(t, "kvm-guest.sh"), "agent-up", "opencode2")
	assertStatus(t, result, 1)

	assertContains(t, "stderr", result.Stderr, "refusing to sync")
	assertNotContains(t, "rsync argv", readText(t, filepath.Join(dir, "rsync-argv")), "agents-sandbox")
}

// TestKvmGuestAgentUpRejectsUnsafeCommandCharacters pins the guard against every
// character that would end or expand the remote shell layers, not just the
// single quote.
func TestKvmGuestAgentUpRejectsUnsafeCommandCharacters(t *testing.T) {
	for _, unsafe := range []string{"'", `"`, "$", "`", `\`} {
		t.Run(unsafe, func(t *testing.T) {
			dir := t.TempDir()
			env := agentUpEnv(t, dir)

			result := RunScript(t, dir, env, repositoryScript(t, "kvm-guest.sh"),
				"agent-up", "opencode2"+unsafe)
			assertStatus(t, result, 1)

			assertContains(t, "stderr", result.Stderr, "shell string")
			assertNotContains(t, "ssh argv", readText(t, filepath.Join(dir, "ssh-argv")), "cloud-init")
		})
	}
}

// TestKvmGuestAgentUpRejectsASingleQuoteInTheCommand pins that a credential
// carrying a single quote is rejected before any guest contact: it would end the
// single-quoted remote string early and corrupt the command silently.
func TestKvmGuestAgentUpRejectsASingleQuoteInTheCommand(t *testing.T) {
	dir := t.TempDir()
	env := agentUpEnv(t, dir)

	result := RunScript(t, dir, env, repositoryScript(t, "kvm-guest.sh"),
		"agent-up", "LITELLM_API_KEY=a'b opencode2")
	assertStatus(t, result, 1)

	assertContains(t, "stderr", result.Stderr, "single quote")
	assertNotContains(t, "ssh argv", readText(t, filepath.Join(dir, "ssh-argv")), "cloud-init")
}

// TestKvmGuestAgentRejectsASingleQuoteInTheCommand pins the same guard on the
// direct `agent` subcommand.
func TestKvmGuestAgentRejectsASingleQuoteInTheCommand(t *testing.T) {
	dir := t.TempDir()
	env := kvmGuestEnv(dir)
	env["SSH_ARGV"] = filepath.Join(dir, "ssh-argv")
	env["PATH"] = kvmGuestPath(t, "virsh", "ssh")

	result := RunScript(t, dir, env, repositoryScript(t, "kvm-guest.sh"), "agent", "opencode2'")
	assertStatus(t, result, 1)

	assertContains(t, "stderr", result.Stderr, "single quote")
}

// TestKvmGuestAgentProvisionSyncsConfigAndMaterializes pins that
// `agent-provision` ships the host's launcher config to the guest and runs the
// launcher's own `config provision` there, targeting the guest home.
func TestKvmGuestAgentProvisionSyncsConfigAndMaterializes(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	if err := os.MkdirAll(filepath.Join(home, ".config", "agents-sandbox"), 0o755); err != nil {
		t.Fatalf("create home config dir: %v", err)
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
	assertContains(t, "rsync argv", rsyncArgv,
		filepath.Join(home, ".config", "agents-sandbox")+"/")
	assertContains(t, "rsync argv", rsyncArgv, "dev@192.168.122.55:.config/agents-sandbox/")
	// No host ~/.gitconfig here, so the git identity is not copied.
	assertNotContains(t, "rsync argv", rsyncArgv, ".gitconfig")

	sshArgv := readText(t, sshLog)
	// The guest config dir must exist before rsync targets it: on a fresh guest
	// ~/.config does not, and the rsync into it then fails under set -e.
	assertContains(t, "ssh argv", sshArgv, "mkdir -p ~/.config/agents-sandbox")
	assertContains(t, "ssh argv", sshArgv, "config provision")
	assertContains(t, "ssh argv", sshArgv, "--agent opencode2")
	assertContains(t, "ssh argv", sshArgv, `--target "$HOME"`)
}

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

func TestKvmGuestSyncForwardsTheCheckoutWithExcludes(t *testing.T) {
	dir := t.TempDir()
	argvPath := filepath.Join(dir, "rsync-argv")
	env := kvmGuestEnv(dir)
	env["ARTIFACT_DIR"] = filepath.Join(dir, ".kvm-guest")
	env["SOURCE_DIR"] = "/host/checkout"
	env["RSYNC_ARGV"] = argvPath
	env["PATH"] = kvmGuestPath(t, "virsh", "rsync")

	result := RunScript(t, dir, env, repositoryScript(t, "kvm-guest.sh"), "sync")
	assertStatus(t, result, 0)

	argv := readText(t, argvPath)
	assertContains(t, "rsync argv", argv, "/host/checkout/")
	assertContains(t, "rsync argv", argv, "dev@192.168.122.55:~/agents-sandbox/")
	assertContains(t, "rsync argv", argv, "--exclude .kvm-guest/")
	// The guest owns its own clone's .git. A directory-only `.git/` pattern does
	// not match a linked worktree's `.git`, which is a *file*: rsync would then
	// copy that dangling `gitdir: ../.git/...` pointer over the guest's real .git
	// directory, and every launcher command in the guest would fail to resolve
	// the project identity. Exclude the `.git` entry itself, file or directory.
	assertContains(t, "rsync argv", argv, "--exclude .git ")
	assertNotContains(t, "rsync argv", argv, "--exclude .git/")
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

// TestKvmGuestSyncBackNeverUpdatesACheckedOutBranchInPlace pins that sync-back
// has no force surface: a fetch must never update a checked-out branch in place,
// which would advance the ref while leaving the working tree at the old commit,
// so a later commit silently reverts the fetch. The checked-out branch is
// reached through the kvm-guest/<name> namespace and merged instead.
func TestKvmGuestSyncBackNeverUpdatesACheckedOutBranchInPlace(t *testing.T) {
	dir := t.TempDir()
	gitLog := filepath.Join(dir, "git-argv")
	env := kvmGuestEnv(dir)
	env["GIT_ARGV"] = gitLog
	env["KVM_SYNC_BACK_FORCE"] = "1" // ignored: the surface is gone
	env["PATH"] = kvmGuestPath(t, "virsh", "git")

	result := RunScript(t, dir, env, repositoryScript(t, "kvm-guest.sh"), "sync-back", "feature")
	assertStatus(t, result, 0)

	argv := readText(t, gitLog)
	assertNotContains(t, "git argv", argv, "--update-head-ok")
	assertNotContains(t, "git argv", argv, "+refs/heads/feature")
	assertContains(t, "git argv", argv, "refs/heads/feature:refs/heads/feature")
}

// TestKvmGuestSyncBackPropagatesAFetchFailure pins that a failing fetch is not
// swallowed: set -e must surface git's non-zero status.
func TestKvmGuestSyncBackPropagatesAFetchFailure(t *testing.T) {
	dir := t.TempDir()
	gitLog := filepath.Join(dir, "git-argv")
	env := kvmGuestEnv(dir)
	env["GIT_ARGV"] = gitLog
	env["GIT_EXIT"] = "1"
	env["PATH"] = kvmGuestPath(t, "virsh", "git")

	result := RunScript(t, dir, env, repositoryScript(t, "kvm-guest.sh"), "sync-back")
	assertContains(t, "git argv", readText(t, gitLog), "fetch")
	if result.Status == 0 {
		t.Fatalf("sync-back exited 0 despite a failing fetch:\nstderr:\n%s", result.Stderr)
	}
}

// TestKvmGuestPrimeCopiesImagesAndHome pins that `prime` copies both halves of
// the guest's cached state from the host: its runner images over docker
// save|ssh docker load, and its microsandbox home over rsync.
func TestKvmGuestPrimeCopiesImagesAndHome(t *testing.T) {
	dir := t.TempDir()
	dockerLog := filepath.Join(dir, "docker-argv")
	sshLog := filepath.Join(dir, "ssh-argv")
	rsyncLog := filepath.Join(dir, "rsync-argv")

	root := filepath.Join(dir, "root")
	home := filepath.Join(root, "smoke", "data", "agents-sandbox", "msb")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatalf("create host msb home: %v", err)
	}

	env := kvmGuestEnv(dir)
	env["KVM_PRIME_ROOT"] = root
	env["SURFACE_SWEEP_DOCKER_LOG"] = dockerLog
	env["SURFACE_SWEEP_DOCKER_IMAGES"] = "agents-sandbox/runner-opencode-abc:opencode-latest"
	env["SSH_ARGV"] = sshLog
	env["RSYNC_ARGV"] = rsyncLog
	env["PATH"] = kvmGuestPath(t, "virsh", "ssh", "rsync", "docker")

	result := RunScript(t, dir, env, repositoryScript(t, "kvm-guest.sh"), "prime")
	assertStatus(t, result, 0)

	// The image list is printed before the transfer so the (long, silent) docker
	// save stream does not look stalled; docker load then reports each image.
	assertContains(t, "prime stderr", result.Stderr, "agents-sandbox/runner-opencode-abc:opencode-latest")

	dockerArgv := readText(t, dockerLog)
	assertContains(t, "docker argv", dockerArgv, "images --format")
	assertContains(t, "docker argv", dockerArgv, "save agents-sandbox/runner-opencode-abc:opencode-latest")

	sshArgv := readText(t, sshLog)
	assertContains(t, "ssh argv", sshArgv, "dev@192.168.122.55")
	assertContains(t, "ssh argv", sshArgv, "docker load")
	assertContains(t, "ssh argv", sshArgv, "mkdir -p '"+filepath.Join(root, "smoke", "data", "agents-sandbox")+"'")

	rsyncArgv := readText(t, rsyncLog)
	assertContains(t, "rsync argv", rsyncArgv, "--info=progress2")
	assertContains(t, "rsync argv", rsyncArgv, home+"/")
	assertContains(t, "rsync argv", rsyncArgv, "dev@192.168.122.55:"+home+"/")
}

// TestKvmGuestPrimeWarnsWhenTheHostHasNoCache pins that a host with nothing
// built warns and copies nothing, rather than opening a transfer to the guest.
func TestKvmGuestPrimeWarnsWhenTheHostHasNoCache(t *testing.T) {
	dir := t.TempDir()
	sshLog := filepath.Join(dir, "ssh-argv")
	env := kvmGuestEnv(dir)
	env["KVM_PRIME_ROOT"] = filepath.Join(dir, "empty-root")
	env["SSH_ARGV"] = sshLog
	env["PATH"] = kvmGuestPath(t, "virsh", "ssh", "rsync", "docker")

	result := RunScript(t, dir, env, repositoryScript(t, "kvm-guest.sh"), "prime")
	assertStatus(t, result, 0)
	assertContains(t, "prime stderr", result.Stderr, "no host runner images")
	assertContains(t, "prime stderr", result.Stderr, "no host microsandbox home")
	if _, err := os.Stat(sshLog); err == nil {
		t.Error("prime opened an ssh connection with nothing to copy")
	}
}

func TestKvmGuestRunRejectsNoCommand(t *testing.T) {
	dir := t.TempDir()
	env := kvmGuestEnv(dir)
	env["PATH"] = kvmGuestPath(t, "virsh", "ssh")

	result := RunScript(t, dir, env, repositoryScript(t, "kvm-guest.sh"), "run")
	assertStatus(t, result, 2)
	assertContains(t, "usage", result.Stderr, "usage:")
}

func TestKvmGuestDownDestroysTheDomain(t *testing.T) {
	dir := t.TempDir()
	argvPath := filepath.Join(dir, "virsh-argv")
	env := kvmGuestEnv(dir)
	env["VIRSH_ARGV"] = argvPath
	env["VIRSH_DOMINFO_OK"] = "1"
	env["PATH"] = kvmGuestPath(t, "virsh")

	result := RunScript(t, dir, env, repositoryScript(t, "kvm-guest.sh"), "down")
	assertStatus(t, result, 0)

	argv := readText(t, argvPath)
	assertContains(t, "virsh argv", argv, "destroy asb-kvm-test")
	assertContains(t, "virsh argv", argv, "undefine asb-kvm-test")
}

func TestKvmGuestDownKeepsTheCachedBaseImage(t *testing.T) {
	dir := t.TempDir()
	cacheDir := filepath.Join(dir, "image-cache")
	imagePath := filepath.Join(cacheDir, "guest.qcow2")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		t.Fatalf("mkdir cache: %v", err)
	}
	if err := os.WriteFile(imagePath, []byte("image"), 0o644); err != nil {
		t.Fatalf("write cached image: %v", err)
	}

	env := kvmGuestEnv(dir)
	env["KVM_IMAGE_CACHE"] = cacheDir
	env["IMAGE_URL"] = "https://example.test/guest.qcow2"
	env["VIRSH_DOMINFO_OK"] = "1"
	env["PATH"] = kvmGuestPath(t, "virsh")

	result := RunScript(t, dir, env, repositoryScript(t, "kvm-guest.sh"), "down")
	assertStatus(t, result, 0)

	if _, err := os.Stat(imagePath); err != nil {
		t.Fatalf("cached base image removed by down: %v", err)
	}
}

// kvmGuestVirshHandler serves the `virsh` a script under test calls. It logs
// every invocation to VIRSH_ARGV and answers by subcommand: `domifaddr`
// returns one IPv4 lease, `dominfo` succeeds only when VIRSH_DOMINFO_OK is set
// (so `down` can exercise the destroy path), `net-info` reports an active
// network, and define/start/destroy/undefine succeed silently.
func kvmGuestVirshHandler(args []string) (string, string, int) {
	recordArgv("VIRSH_ARGV", args)
	joined := strings.Join(args, " ")
	switch {
	case strings.Contains(joined, "domifaddr"):
		return " vnet0      52:54:00:00:00:00    ipv4         192.168.122.55/24  \n", "", 0
	case strings.Contains(joined, "dominfo"):
		if envOption("VIRSH_DOMINFO_OK") != "" {
			return "Name:           asb-kvm-test\n", "", 0
		}
		return "", "error: failed to get domain 'asb-kvm-test'\n", 1
	case strings.Contains(joined, "domstate"):
		return envOption("VIRSH_DOMSTATE") + "\n", "", 0
	case strings.Contains(joined, "net-info"):
		return "Name:           default\nActive:         yes\n", "", 0
	case strings.Contains(joined, "net-dumpxml"):
		return "<network><bridge name='virbr0'/></network>\n", "", 0
	default:
		return "", "", 0
	}
}

// kvmGuestSSHHandler serves the `ssh` a script under test calls: it logs the
// invocation and succeeds, so a case can assert the guest target and the
// command the script forwarded.
func kvmGuestSSHHandler(args []string) (string, string, int) {
	recordArgv("SSH_ARGV", args)
	joined := strings.Join(args, " ")
	if envOption("SSH_CLOUD_INIT_FAIL") != "" && strings.Contains(joined, "cloud-init status") {
		return "", "Cloud-init failed: see status --long\n", 1
	}
	return kvmGuestSSHSessionResponse(joined)
}

// kvmGuestSSHSessionResponse answers the tmux session probes and lets every other
// command succeed silently. A bare probe (`agent`) encodes presence in its exit
// status; agent-up's probe prints the state, so a failed connect is distinct
// from "absent".
func kvmGuestSSHSessionResponse(joined string) (string, string, int) {
	if !strings.Contains(joined, "tmux has-session") {
		return "", "", 0
	}
	if envOption("SSH_PROBE_FAIL") != "" {
		return "", "", 255
	}
	present := envOption("SSH_AGENT_SESSION") != ""
	stateProtocol := strings.Contains(joined, "echo present")
	if stateProtocol && present {
		return "present\n", "", 0
	}
	if stateProtocol {
		return "absent\n", "", 0
	}
	if present {
		return "", "", 0
	}
	return "", "", 1
}

// kvmGuestRsyncHandler serves the `rsync` a script under test calls: it logs
// the invocation and succeeds.
func kvmGuestRsyncHandler(args []string) (string, string, int) {
	recordArgv("RSYNC_ARGV", args)
	return "", "", 0
}

// kvmGuestGitHandler serves the `git` a script under test calls: it logs the
// invocation and succeeds, or exits non-zero when GIT_EXIT is set, so a case can
// pin that a fetch failure propagates. It never touches a repository.
func kvmGuestGitHandler(args []string) (string, string, int) {
	recordArgv("GIT_ARGV", args)
	if envOption("GIT_EXIT") == "" {
		return "", "", 0
	}
	return "", "", 1
}

// kvmGuestNftHandler serves the `nft` a script under test calls: it logs the
// invocation and succeeds, so a case can assert the rules `firewall` inserts
// without touching the host ruleset.
func kvmGuestNftHandler(args []string) (string, string, int) {
	recordArgv("NFT_ARGV", args)
	return "", "", 0
}

// kvmGuestIDHandler reports uid 0 so `firewall` takes the no-sudo path and
// calls the faked `nft` directly.
func kvmGuestIDHandler(_ []string) (string, string, int) {
	return "0\n", "", 0
}

// recordArgv appends the faked command's argv to the file named by envVar, when
// the harness set it. The fake runs in a re-executed process, so it reads its
// knobs from the environment rather than a closure.
func recordArgv(envVar string, args []string) {
	path := envOption(envVar)
	if path == "" {
		return
	}
	_ = appendLine(path, strings.Join(args, " "))
}
