package scripts

import (
	"io"
	"os"
	"path/filepath"
	"slices"
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
	env["GH_VERSION"] = "9.9.9"
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

	// The guest carries the GitHub CLI so the agent can work with issues and PRs.
	assertContains(
		t,
		"user-data",
		userData,
		"https://github.com/cli/cli/releases/download/v9.9.9/gh_9.9.9_linux_amd64.deb",
	)
	assertContains(t, "user-data", userData, "dpkg -i /tmp/gh.deb")

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
	lockKvmGuest(t, env)
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
	lockKvmGuest(t, env)
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
	lockKvmGuest(t, env)
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
// the flow drives and a temp HOME with no launcher config so provisioning is a
// no-op.
func agentUpEnv(t *testing.T, dir string) map[string]string {
	t.Helper()
	home := filepath.Join(dir, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatalf("create home: %v", err)
	}
	env := kvmGuestEnv(dir)
	env["HOME"] = home
	// The ambient environment may carry GITHUB_PAT (the dev VM sets it to the
	// literal secret placeholder "$MSB_GITHUB_PAT"); clear it so a case starts
	// from "no token" and opts in explicitly.
	env["GITHUB_PAT"] = ""
	env["KVM_HOST_USER"] = "kvmhost"
	env["KVM_GUEST_STATE"] = filepath.Join(dir, "guest-state")
	env["GIT_COMMON_DIR"] = "/host/agents-sandbox/.git"
	env["SSH_ARGV"] = filepath.Join(dir, "ssh-argv")
	env["SSH_KEYGEN_ARGV"] = filepath.Join(dir, "ssh-keygen-argv")
	env["RSYNC_ARGV"] = filepath.Join(dir, "rsync-argv")
	env["VIRSH_ARGV"] = filepath.Join(dir, "virsh-argv")
	env["PATH"] = kvmGuestPath(t, "virsh", "ssh", "rsync", "git", "ssh-keygen")
	lockKvmGuest(t, env)
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
	// The checkout must be pulled in the guest, so it carries `config provision`.
	assertContains(t, "ssh argv", sshArgv, "git fetch origin --prune")
}

// TestKvmGuestAgentUpFailsWhenCloudInitFails pins that a failed cloud-init
// aborts before syncing into a half-provisioned guest.
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
	// The gate precedes sync: a regression that synced before the cloud-init
	// wait would fetch here.
	assertNotContains(t, "ssh argv", readText(t, filepath.Join(dir, "ssh-argv")), "git fetch origin")
}

// TestKvmGuestAgentUpReattachesWithoutSyncingOverALiveSession pins the recovery
// path: re-running the command against a guest that already has the session must
// reattach, not sync_in over the live agent's work.
func TestKvmGuestAgentUpReattachesWithoutSyncingOverALiveSession(t *testing.T) {
	dir := t.TempDir()
	env := agentUpEnv(t, dir)
	env["VIRSH_DOMINFO_OK"] = "1"
	env["VIRSH_DOMSTATE"] = "running"
	env["SSH_AGENT_SESSION"] = "1"

	result := RunScript(t, dir, env, repositoryScript(t, "kvm-guest.sh"),
		"agent-up", "LITELLM_API_KEY=secret opencode2")
	assertStatus(t, result, 0)

	sshArgv := readText(t, filepath.Join(dir, "ssh-argv"))
	assertContains(t, "ssh argv", sshArgv, "tmux has-session")
	assertContains(t, "ssh argv", sshArgv, "tmux new -A -s agents-sandbox")
	assertNotContains(t, "ssh argv", sshArgv, "config provision")
	assertNotContains(t, "ssh argv", sshArgv, "git fetch origin")
}

// TestKvmGuestAgentUpRefusesToSyncWhenTheSessionProbeFails pins that a probe
// that cannot reach the guest is not read as "no live session": sync must not
// run on an unknown session state.
func TestKvmGuestAgentUpRefusesToSyncWhenTheSessionProbeFails(t *testing.T) {
	dir := t.TempDir()
	env := agentUpEnv(t, dir)
	env["VIRSH_DOMINFO_OK"] = "1"
	env["VIRSH_DOMSTATE"] = "running"
	env["SSH_PROBE_FAIL"] = "1"

	result := RunScript(t, dir, env, repositoryScript(t, "kvm-guest.sh"), "agent-up", "opencode2")
	assertStatus(t, result, 1)

	assertContains(t, "stderr", result.Stderr, "refusing to sync")
	assertNotContains(t, "ssh argv", readText(t, filepath.Join(dir, "ssh-argv")), "git fetch origin")
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

// TestKvmGuestAgentUpSignsGhInFromTheHostPat pins that a host-environment
// GITHUB_PAT signs gh in inside the guest, and that the token rides ssh stdin,
// never the command line. The guest's git targets the host (ensure_guest_git_remote),
// not GitHub, so no GitHub insteadOf rewrite is set up here.
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
	assertNotContains(t, "ssh argv", sshArgv, "url.https://github.com/.insteadOf")
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
	// The hint must cover a rejected token, not only a pre-gh guest.
	assertContains(t, "stderr", result.Stderr, "token")
	assertNotContains(t, "ssh argv", readText(t, filepath.Join(dir, "ssh-argv")), "tmux new")
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
	lockKvmGuest(t, env)
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

// TestKvmGuestAgentProvisionCopiesOnlyTheUserIdentity pins that provisioning
// gives the guest a committable identity without shipping the rest of the host's
// ~/.gitconfig (credential helpers, aliases, signing config).
func TestKvmGuestAgentProvisionCopiesOnlyTheUserIdentity(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatalf("create home: %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(home, ".gitconfig"),
		[]byte(
			"[user]\n\tname = Host User\n\temail = host@example.test\n[credential]\n\thelper = store\n",
		),
		0o644,
	); err != nil {
		t.Fatalf("write host gitconfig: %v", err)
	}
	sshLog := filepath.Join(dir, "ssh-argv")
	stdinLog := filepath.Join(dir, "ssh-stdin")
	rsyncLog := filepath.Join(dir, "rsync-argv")
	env := kvmGuestEnv(dir)
	lockKvmGuest(t, env)
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
// a host whose gitconfig carries no user.name/user.email writes nothing, rather
// than shipping the rest of the file over the guest's own identity.
func TestKvmGuestAgentProvisionKeepsTheGuestIdentityWithoutAHostIdentity(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatalf("create home: %v", err)
	}
	if err := os.WriteFile(filepath.Join(home, ".gitconfig"),
		[]byte("[credential]\n\thelper = store\n"), 0o644); err != nil {
		t.Fatalf("write host gitconfig: %v", err)
	}
	sshLog := filepath.Join(dir, "ssh-argv")
	rsyncLog := filepath.Join(dir, "rsync-argv")
	env := kvmGuestEnv(dir)
	lockKvmGuest(t, env)
	env["HOME"] = home
	env["SSH_ARGV"] = sshLog
	env["RSYNC_ARGV"] = rsyncLog
	env["PATH"] = kvmGuestPath(t, "virsh", "ssh", "rsync", "git")

	result := RunScript(t, dir, env, repositoryScript(t, "kvm-guest.sh"), "agent-provision")
	assertStatus(t, result, 0)

	assertNotContains(t, "ssh argv", readText(t, sshLog), "cat > ~/.gitconfig")
	assertNotContains(t, "rsync argv", readText(t, rsyncLog), ".gitconfig")
	assertContains(t, "provision stderr", result.Stderr, "keeps its own git identity")
}

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
	env["GIT_COMMON_DIR"] = "/host/agents-sandbox/.git"
	env["SSH_ARGV"] = filepath.Join(dir, "ssh-argv")
	env["SSH_KEYGEN_ARGV"] = filepath.Join(dir, "ssh-keygen-argv")
	env["PATH"] = kvmGuestPath(t, "virsh", "ssh", "git", "ssh-keygen", "rsync")
	lockKvmGuest(t, env)
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
		`command="git-shell -c \"$SSH_ORIGINAL_COMMAND\"",restrict ssh-ed25519 AAAASTALE agents-sandbox-kvm-guest`+"\n",
	), 0o600); err != nil {
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

// TestKvmGuestSyncPreservesForeignGitShellKeys pins the guard against collateral
// damage: a foreign git-shell key shares the options prefix this script matches
// but carries a different comment, so the rewrite must leave it intact rather
// than silently revoke another admin's (or tool's) git access.
func TestKvmGuestSyncPreservesForeignGitShellKeys(t *testing.T) {
	dir := t.TempDir()
	env := kvmGuestSyncEnv(t, dir)
	authorizedPath := filepath.Join(dir, "home", ".ssh", "authorized_keys")
	if err := os.MkdirAll(filepath.Dir(authorizedPath), 0o755); err != nil {
		t.Fatalf("create .ssh: %v", err)
	}
	if err := os.WriteFile(authorizedPath, []byte(
		`command="git-shell -c \"$SSH_ORIGINAL_COMMAND\"",no-port-forwarding ssh-rsa AAAAFOREIGN other-admin-key`+"\n",
	), 0o600); err != nil {
		t.Fatalf("seed authorized_keys: %v", err)
	}

	result := RunScript(t, dir, env, repositoryScript(t, "kvm-guest.sh"), "sync")
	assertStatus(t, result, 0)

	authorized := readText(t, authorizedPath)
	assertContains(t, "authorized_keys", authorized, "AAAAFOREIGN")
	assertContains(t, "authorized_keys", authorized, "other-admin-key")
	if n := strings.Count(authorized, "agents-sandbox-kvm-guest"); n != 1 {
		t.Fatalf("expected exactly one agents-sandbox-kvm-guest line, got %d:\n%s", n, authorized)
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

// TestKvmGuestSyncAppendsTheKeyAfterAMissingTrailingNewline pins the append: an
// existing authorized_keys whose last line has no trailing newline must not have
// the key line merged onto it — the merged line would both break the existing key
// and hide the appended key's options from sshd.
func TestKvmGuestSyncAppendsTheKeyAfterAMissingTrailingNewline(t *testing.T) {
	dir := t.TempDir()
	env := kvmGuestSyncEnv(t, dir)
	authorizedPath := filepath.Join(dir, "home", ".ssh", "authorized_keys")
	if err := os.MkdirAll(filepath.Dir(authorizedPath), 0o755); err != nil {
		t.Fatalf("create .ssh: %v", err)
	}
	existing := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIEXISTING existing@host"
	if err := os.WriteFile(authorizedPath, []byte(existing), 0o600); err != nil {
		t.Fatalf("seed authorized_keys: %v", err)
	}

	assertStatus(t, RunScript(t, dir, env, repositoryScript(t, "kvm-guest.sh"), "sync"), 0)

	authorized := readText(t, authorizedPath)
	// The existing key must stand alone on its line, terminated by a newline, so
	// the guest key's options start a new line rather than extend its comment.
	assertContains(t, "authorized_keys", authorized, existing+"\n")
	assertContains(t, "authorized_keys", authorized, `command="git-shell -c \"$SSH_ORIGINAL_COMMAND\"",restrict`)
}

// TestKvmGuestSyncRepairsAPreviouslyMergedKeyLine pins the repair path: a line a
// pre-fix run merged the guest options onto (the appended-without-newline bug)
// does not begin with the options prefix, so it must be kept — deleting it would
// drop the host key it was merged onto — while a clean guest line is appended so
// the guest key is authorized. A substring idempotency check would mistake the
// merged line for the whole line and skip the repair.
func TestKvmGuestSyncRepairsAPreviouslyMergedKeyLine(t *testing.T) {
	dir := t.TempDir()
	env := kvmGuestSyncEnv(t, dir)
	authorizedPath := filepath.Join(dir, "home", ".ssh", "authorized_keys")
	if err := os.MkdirAll(filepath.Dir(authorizedPath), 0o755); err != nil {
		t.Fatalf("create .ssh: %v", err)
	}
	existing := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIEXISTING existing@host"
	merged := existing + `command="git-shell -c \"$SSH_ORIGINAL_COMMAND\"",restrict ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIGUEST agents-sandbox-kvm-guest`
	if err := os.WriteFile(authorizedPath, []byte(merged+"\n"), 0o600); err != nil {
		t.Fatalf("seed authorized_keys: %v", err)
	}

	assertStatus(t, RunScript(t, dir, env, repositoryScript(t, "kvm-guest.sh"), "sync"), 0)

	authorized := readText(t, authorizedPath)
	assertContains(t, "authorized_keys", authorized, existing)
	assertContains(
		t,
		"authorized_keys",
		authorized,
		"\ncommand=\"git-shell -c \\\"$SSH_ORIGINAL_COMMAND\\\"\",restrict ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIGUEST agents-sandbox-kvm-guest\n",
	)
}

// TestKvmGuestSyncFailsWhenTheGuestCannotReachTheHost pins the preflight: a guest
// that cannot complete a git exchange with the host fails with the diagnostic and
// never proceeds to the fetch. The probe must be a real git handshake, not a plain
// command: the key is forced through `git-shell`, which rejects anything but
// git-upload-pack/git-receive-pack, so `ssh … true` would always fail on a
// correctly configured host.
func TestKvmGuestSyncFailsWhenTheGuestCannotReachTheHost(t *testing.T) {
	dir := t.TempDir()
	env := kvmGuestSyncEnv(t, dir)
	env["SSH_FAIL_CONTAINS"] = "git ls-remote origin"

	result := RunScript(t, dir, env, repositoryScript(t, "kvm-guest.sh"), "sync")
	assertStatus(t, result, 1)

	assertContains(t, "stderr", result.Stderr, "cannot reach the host over SSH")
	assertContains(t, "ssh argv", readText(t, filepath.Join(dir, "ssh-argv")), "git ls-remote origin")
	assertNotContains(t, "ssh argv", readText(t, filepath.Join(dir, "ssh-argv")), "git fetch origin")
}

// TestKvmGuestSyncRoutesOriginToTheHost pins that the guest's single remote is
// routed to the host by a repository-local URL rewrite — the recorded origin URL
// is kept, and git operations go to the host repository. The rewrite is
// repository-local (not `--global`): `agent-provision` truncates the guest's
// ~/.gitconfig, which would wipe a global rewrite and send pushes back to GitHub.
func TestKvmGuestSyncRoutesOriginToTheHost(t *testing.T) {
	dir := t.TempDir()
	env := kvmGuestSyncEnv(t, dir)

	result := RunScript(t, dir, env, repositoryScript(t, "kvm-guest.sh"), "sync")
	assertStatus(t, result, 0)

	argv := readText(t, filepath.Join(dir, "ssh-argv"))
	assertContains(
		t,
		"ssh argv",
		argv,
		`git config url."ssh://kvmhost@192.168.122.1/host/agents-sandbox/.git".insteadOf "https://example.test/agents-sandbox.git"`,
	)
	assertContains(t, "ssh argv", argv, `git config branch."feat/793-msb-journeys".remote origin`)
	assertContains(t, "ssh argv", argv, "core.sshCommand")
	assertContains(t, "ssh argv", argv, "git config push.autoSetupRemote true")
	assertNotContains(t, "ssh argv", argv, "git config --global")
	assertNotContains(t, "ssh argv", argv, "git remote add")
}

// TestKvmGuestSyncReplacesAStaleRewriteOnAChangedHost pins that re-pointing the
// guest at a different host URL removes the previous url.<host> rewrite first:
// two insteadOf rules for the same origin URL would let the stale one shadow the
// new one, so a moved host would stick.
func TestKvmGuestSyncReplacesAStaleRewriteOnAChangedHost(t *testing.T) {
	dir := t.TempDir()
	env := kvmGuestSyncEnv(t, dir)

	result := RunScript(t, dir, env, repositoryScript(t, "kvm-guest.sh"), "sync")
	assertStatus(t, result, 0)

	argv := readText(t, filepath.Join(dir, "ssh-argv"))
	assertContains(t, "ssh argv", argv, "git config kvmguest.hostUrl")
	assertContains(t, "ssh argv", argv, "git config --remove-section")
}

// TestKvmGuestSyncPreservesAuthorizedKeysWhenTheRewriteFails pins the guard in
// ensure_host_authorized_key: when the grep that filters the old guest line fails
// with a read error (not the no-match exit 1), the partial temp file must not be
// moved over the host's authorized_keys — that would drop the user's other keys
// and can lock them out of the host.
func TestKvmGuestSyncPreservesAuthorizedKeysWhenTheRewriteFails(t *testing.T) {
	dir := t.TempDir()
	env := kvmGuestSyncEnv(t, dir)
	env["GREP_READ_ERROR"] = "1"
	env["PATH"] = kvmGuestPath(t, "virsh", "ssh", "git", "ssh-keygen", "rsync", "grep")

	home := filepath.Join(dir, "home")
	authorizedPath := filepath.Join(home, ".ssh", "authorized_keys")
	if err := os.MkdirAll(filepath.Dir(authorizedPath), 0o755); err != nil {
		t.Fatalf("create .ssh: %v", err)
	}
	seed := `command="git-shell -c \"$SSH_ORIGINAL_COMMAND\"",restrict ssh-ed25519 AAAASTALE agents-sandbox-kvm-guest` + "\n"
	if err := os.WriteFile(authorizedPath, []byte(seed), 0o600); err != nil {
		t.Fatalf("seed authorized_keys: %v", err)
	}

	result := RunScript(t, dir, env, repositoryScript(t, "kvm-guest.sh"), "sync")
	if result.Status == 0 {
		t.Fatalf("sync exited 0 despite a failing authorized_keys rewrite:\nstderr:\n%s", result.Stderr)
	}
	assertContains(t, "stderr", result.Stderr, "could not rewrite")
	if got := readText(t, authorizedPath); got != seed {
		t.Fatalf("authorized_keys was modified after a failed rewrite:\n%s", got)
	}
}

// TestKvmGuestSyncPullsFromTheHostInsteadOfRsyncing pins that `sync` pulls the
// host's branch in the guest, fast-forward only — not an rsync that would
// clobber the agent's tree.
func TestKvmGuestSyncPullsFromTheHostInsteadOfRsyncing(t *testing.T) {
	dir := t.TempDir()
	env := kvmGuestSyncEnv(t, dir)
	env["RSYNC_ARGV"] = filepath.Join(dir, "rsync-argv")

	result := RunScript(t, dir, env, repositoryScript(t, "kvm-guest.sh"), "sync")
	assertStatus(t, result, 0)

	argv := readText(t, filepath.Join(dir, "ssh-argv"))
	assertContains(t, "ssh argv", argv, "git fetch origin --prune")
	assertContains(t, "ssh argv", argv, `git checkout "feat/793-msb-journeys"`)
	assertContains(t, "ssh argv", argv, `git merge --ff-only origin/"feat/793-msb-journeys"`)
	assertNotContains(t, "rsync argv", readText(t, filepath.Join(dir, "rsync-argv")), "agents-sandbox")
}

// TestKvmGuestSyncQuotesAShellMetacharacterBranch pins that a branch name with
// the shell metacharacters git allows (`;`, `&`, `|`) is quoted in the remote
// script, not left to split the command: a name like `feat/x;id` must reach git
// as one argument, not run `id`.
func TestKvmGuestSyncQuotesAShellMetacharacterBranch(t *testing.T) {
	dir := t.TempDir()
	env := kvmGuestSyncEnv(t, dir)
	env["REPO_BRANCH"] = "feat/x;id"
	lockKvmGuest(t, env)

	result := RunScript(t, dir, env, repositoryScript(t, "kvm-guest.sh"), "sync")
	assertStatus(t, result, 0)

	argv := readText(t, filepath.Join(dir, "ssh-argv"))
	assertContains(t, "ssh argv", argv, `git checkout "feat/x;id" && git merge --ff-only origin/"feat/x;id"`)
}

// TestKvmGuestSyncRejectsABranchNameThatCannotBeQuoted pins the guard: a branch
// name carrying a double quote, backslash, dollar sign, or backtick cannot be
// embedded safely in the remote `bash -lc` script even when double-quoted, so
// sync refuses it before touching the host or the guest.
func TestKvmGuestSyncRejectsABranchNameThatCannotBeQuoted(t *testing.T) {
	dir := t.TempDir()
	env := kvmGuestSyncEnv(t, dir)
	env["REPO_BRANCH"] = "feat/$(id)"

	result := RunScript(t, dir, env, repositoryScript(t, "kvm-guest.sh"), "sync")
	assertStatus(t, result, 1)

	assertContains(t, "stderr", result.Stderr, "branch name must not contain")
}

// TestKvmGuestSyncRejectsABranchNameWithASingleQuote pins the guard's single
// quote: the branch is embedded in a single-quoted remote script, so a name like
// `fix/it's` must be refused, not left to end the script early.
func TestKvmGuestSyncRejectsABranchNameWithASingleQuote(t *testing.T) {
	dir := t.TempDir()
	env := kvmGuestSyncEnv(t, dir)
	env["REPO_BRANCH"] = "fix/it's"

	result := RunScript(t, dir, env, repositoryScript(t, "kvm-guest.sh"), "sync")
	assertStatus(t, result, 1)

	assertContains(t, "stderr", result.Stderr, "branch name must not contain")
}

// TestKvmGuestSyncRefusesWhenTheGuestCannotFastForward pins that a diverged guest
// branch aborts sync — never a reset that would clobber the agent's work.
func TestKvmGuestSyncRefusesWhenTheGuestCannotFastForward(t *testing.T) {
	dir := t.TempDir()
	env := kvmGuestSyncEnv(t, dir)
	env["SSH_FAIL_CONTAINS"] = "git merge --ff-only"

	result := RunScript(t, dir, env, repositoryScript(t, "kvm-guest.sh"), "sync")
	if result.Status == 0 {
		t.Fatalf("sync exited 0 despite a failing guest pull:\nstderr:\n%s", result.Stderr)
	}
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
	lockKvmGuest(t, env)
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
	lockKvmGuest(t, env)
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

// TestKvmGuestSyncProceedsWhenTheOwnerBranchHoldsTheGuest pins that the branch
// that already holds the guest proceeds without resuming or recreating it.
func TestKvmGuestSyncProceedsWhenTheOwnerBranchHoldsTheGuest(t *testing.T) {
	dir := t.TempDir()
	// kvmGuestSyncEnv records this branch as the guest's holder and fakes the
	// guest as running, so acquire_guest is a no-op.
	env := kvmGuestSyncEnv(t, dir)
	env["VIRSH_ARGV"] = filepath.Join(dir, "virsh-argv")

	result := RunScript(t, dir, env, repositoryScript(t, "kvm-guest.sh"), "sync")
	assertStatus(t, result, 0)

	assertContains(t, "ssh argv", readText(t, filepath.Join(dir, "ssh-argv")), "git fetch origin --prune")
	virshArgv := readText(t, filepath.Join(dir, "virsh-argv"))
	// The log must be wired: the acquire probe ran (the state query), so the
	// absence of a resume/start below is meaningful rather than an assertion
	// against "".
	assertContains(t, "virsh argv", virshArgv, "list --all --name")
	assertNotContains(t, "virsh argv", virshArgv, "resume")
	assertNotContains(t, "virsh argv", virshArgv, "start")
}

// TestKvmGuestSyncRefusesAGuestHeldByAnotherBranch pins the cross-branch guard:
// a running guest owned by another branch is refused, by name, before any sync.
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

// TestKvmGuestSyncRefusesARunningGuestWithoutAnOwnerLabel pins the pre-lock case:
// a running guest with no label is refused, never silently adopted.
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

// assertOwnerLabeledAtLeaseProbe pins the ordering the lock depends on: the
// owner label exists when wait_for_lease first polls the guest for its address,
// not after the boot-long wait. It fails when the captured content lacks the
// branch — including an empty capture, an absent label.
func assertOwnerLabeledAtLeaseProbe(t *testing.T, capturePath string) {
	t.Helper()
	assertContains(t, "owner at the lease probe", readText(t, capturePath), "branch=feat/793-msb-journeys")
}

// TestKvmGuestRunLabelsAPausedGuestBeforeWaitingForItsLease pins the hand-off
// ordering: the branch takes the guest the moment it resumes it, before
// wait_for_lease. A competing branch that reads a running guest with no label
// reads it as a pre-lock guest and is invited to take it over, so a label
// written only after the wait leaves exactly that window open.
func TestKvmGuestRunLabelsAPausedGuestBeforeWaitingForItsLease(t *testing.T) {
	dir := t.TempDir()
	env := kvmGuestEnv(dir)
	env["VIRSH_DOMINFO_OK"] = "1"
	env["VIRSH_DOMSTATE"] = "paused"
	env["SSH_ARGV"] = filepath.Join(dir, "ssh-argv")
	env["VIRSH_ARGV"] = filepath.Join(dir, "virsh-argv")
	env["VIRSH_OWNER_AT_PROBE"] = filepath.Join(dir, "owner-at-lease")
	env["KVM_BOOT_TIMEOUT"] = "5" // one lease poll, no 300s wait
	env["PATH"] = kvmGuestPath(t, "virsh", "ssh")

	result := RunScript(t, dir, env, repositoryScript(t, "kvm-guest.sh"), "run", "true")
	assertStatus(t, result, 0)

	assertContains(t, "virsh argv", readText(t, filepath.Join(dir, "virsh-argv")), "resume asb-kvm-test")
	assertContains(t, "virsh argv", readText(t, filepath.Join(dir, "virsh-argv")), "domifaddr asb-kvm-test")
	assertOwnerLabeledAtLeaseProbe(t, filepath.Join(dir, "owner-at-lease"))
}

// TestKvmGuestRunLabelsAStoppedGuestBeforeWaitingForItsLease pins the same
// ordering on the start arm, which re-ensures the default network first.
func TestKvmGuestRunLabelsAStoppedGuestBeforeWaitingForItsLease(t *testing.T) {
	dir := t.TempDir()
	env := kvmGuestEnv(dir)
	env["VIRSH_DOMINFO_OK"] = "1"
	env["VIRSH_DOMSTATE"] = "shut off"
	env["SSH_ARGV"] = filepath.Join(dir, "ssh-argv")
	env["VIRSH_ARGV"] = filepath.Join(dir, "virsh-argv")
	env["VIRSH_OWNER_AT_PROBE"] = filepath.Join(dir, "owner-at-lease")
	env["KVM_BOOT_TIMEOUT"] = "5" // one lease poll, no 300s wait
	env["PATH"] = kvmGuestPath(t, "virsh", "ssh")

	result := RunScript(t, dir, env, repositoryScript(t, "kvm-guest.sh"), "run", "true")
	assertStatus(t, result, 0)

	assertContains(t, "virsh argv", readText(t, filepath.Join(dir, "virsh-argv")), "start asb-kvm-test")
	assertContains(t, "virsh argv", readText(t, filepath.Join(dir, "virsh-argv")), "net-info default")
	assertOwnerLabeledAtLeaseProbe(t, filepath.Join(dir, "owner-at-lease"))
}

// TestKvmGuestRunRefusesAnUnexpectedState pins that a state the lock does not
// model is refused by name rather than mis-locked.
func TestKvmGuestRunRefusesAnUnexpectedState(t *testing.T) {
	dir := t.TempDir()
	env := kvmGuestEnv(dir)
	env["VIRSH_DOMINFO_OK"] = "1"
	env["VIRSH_DOMSTATE"] = "crashed"
	env["PATH"] = kvmGuestPath(t, "virsh", "ssh")

	result := RunScript(t, dir, env, repositoryScript(t, "kvm-guest.sh"), "run", "true")
	assertStatus(t, result, 1)

	assertContains(t, "stderr", result.Stderr, "'crashed'")
}

// TestKvmGuestPauseSuspendsTheGuestAndReleasesTheLock pins the hand-off: pause
// suspends the running guest and drops the owner label, keeping the disk.
func TestKvmGuestPauseSuspendsTheGuestAndReleasesTheLock(t *testing.T) {
	dir := t.TempDir()
	env := kvmGuestEnv(dir)
	lockKvmGuest(t, env)
	env["VIRSH_ARGV"] = filepath.Join(dir, "virsh-argv")
	env["PATH"] = kvmGuestPath(t, "virsh")

	result := RunScript(t, dir, env, repositoryScript(t, "kvm-guest.sh"), "pause")
	assertStatus(t, result, 0)

	assertContains(t, "virsh argv", readText(t, filepath.Join(dir, "virsh-argv")), "suspend asb-kvm-test")
	if _, err := os.Stat(filepath.Join(dir, "asb-kvm-test.owner")); err == nil {
		t.Error("pause left the owner label behind")
	}
}

// TestKvmGuestPauseOnAnAbsentGuestIsANoOp pins idempotence: pausing a guest that
// is not there succeeds without touching libvirt.
func TestKvmGuestPauseOnAnAbsentGuestIsANoOp(t *testing.T) {
	dir := t.TempDir()
	env := kvmGuestEnv(dir)
	env["VIRSH_ARGV"] = filepath.Join(dir, "virsh-argv")
	env["PATH"] = kvmGuestPath(t, "virsh")

	result := RunScript(t, dir, env, repositoryScript(t, "kvm-guest.sh"), "pause")
	assertStatus(t, result, 0)

	// The state probe (list) ran, and nothing beyond it: no suspend, start, or
	// destroy. Without VIRSH_ARGV this case could not fail.
	virshArgv := readText(t, filepath.Join(dir, "virsh-argv"))
	assertContains(t, "virsh argv", virshArgv, "list --all --name")
	assertNotContains(t, "virsh argv", virshArgv, "suspend")
	assertNotContains(t, "virsh argv", virshArgv, "start")
	assertNotContains(t, "virsh argv", virshArgv, "destroy")
}

// TestKvmGuestPauseFailsWhenTheStateQueryFails pins that an unreachable or
// mis-permitted libvirt is not read as an absent guest: pause dies, and the
// owner label is left for the real holder rather than cleared.
func TestKvmGuestPauseFailsWhenTheStateQueryFails(t *testing.T) {
	dir := t.TempDir()
	env := kvmGuestEnv(dir)
	writeGuestOwnerFile(t, dir, "feat/793-msb-journeys")
	env["VIRSH_ARGV"] = filepath.Join(dir, "virsh-argv")
	env["VIRSH_LIST_FAIL"] = "1"
	env["PATH"] = kvmGuestPath(t, "virsh")

	result := RunScript(t, dir, env, repositoryScript(t, "kvm-guest.sh"), "pause")
	assertStatus(t, result, 1)

	assertContains(t, "stderr", result.Stderr, "cannot query libvirt")
	assertNotContains(t, "virsh argv", readText(t, filepath.Join(dir, "virsh-argv")), "suspend")
	if _, err := os.Stat(filepath.Join(dir, "asb-kvm-test.owner")); err != nil {
		t.Errorf("pause cleared the owner label after a failed state query: %v", err)
	}
}

// TestKvmGuestPauseFailsWhenTheStateReadFails pins the second fail-closed arm:
// the domain is defined (the list query succeeds) but its state cannot be read,
// so pause dies and leaves the owner label rather than treating the guest as
// free and handoff-able.
func TestKvmGuestPauseFailsWhenTheStateReadFails(t *testing.T) {
	dir := t.TempDir()
	env := kvmGuestEnv(dir)
	writeGuestOwnerFile(t, dir, "feat/793-msb-journeys")
	env["VIRSH_ARGV"] = filepath.Join(dir, "virsh-argv")
	env["VIRSH_DOMINFO_OK"] = "1"
	env["VIRSH_DOMSTATE_FAIL"] = "1"
	env["PATH"] = kvmGuestPath(t, "virsh")

	result := RunScript(t, dir, env, repositoryScript(t, "kvm-guest.sh"), "pause")
	assertStatus(t, result, 1)

	assertContains(t, "stderr", result.Stderr, "cannot read the state of guest")
	assertNotContains(t, "virsh argv", readText(t, filepath.Join(dir, "virsh-argv")), "suspend")
	if _, err := os.Stat(filepath.Join(dir, "asb-kvm-test.owner")); err != nil {
		t.Errorf("pause cleared the owner label after a failed state read: %v", err)
	}
}

// TestKvmGuestUpOnAHeldGuestIsANoOp pins that the `up` dispatch routes through
// acquire_guest: the branch that already holds the guest is a no-op, so the
// create path (define/start, which need a hypervisor) is never reached.
func TestKvmGuestUpOnAHeldGuestIsANoOp(t *testing.T) {
	dir := t.TempDir()
	env := kvmGuestEnv(dir)
	lockKvmGuest(t, env)
	env["VIRSH_ARGV"] = filepath.Join(dir, "virsh-argv")
	env["PATH"] = kvmGuestPath(t, "virsh")

	result := RunScript(t, dir, env, repositoryScript(t, "kvm-guest.sh"), "up")
	assertStatus(t, result, 0)

	virshArgv := readText(t, filepath.Join(dir, "virsh-argv"))
	// The state probe ran (list), so the absence of define/start below is
	// meaningful rather than an assertion against "".
	assertContains(t, "virsh argv", virshArgv, "list --all --name")
	assertNotContains(t, "virsh argv", virshArgv, "define")
	assertNotContains(t, "virsh argv", virshArgv, "start")
	assertNotContains(t, "stderr", result.Stderr, "qemu-img")
}

// TestKvmGuestUpOnAnAbsentGuestLabelsItBeforeAnyReadinessWait covers the create
// arm's ordering. create defines and starts the domain and only then waits for a
// lease, so the label must be written before create is called; the case gives its
// fake virsh no domifaddr answer (VIRSH_LEASE_NONE), which leaves create still
// booting — the state a competing branch used to see as an unlabeled guest — and
// asserts the label was already there when create polled for the lease.
func TestKvmGuestUpOnAnAbsentGuestLabelsItBeforeAnyReadinessWait(t *testing.T) {
	dir := t.TempDir()
	env := kvmGuestEnv(dir)
	env["VIRSH_ARGV"] = filepath.Join(dir, "virsh-argv")
	env["VIRSH_OWNER_AT_PROBE"] = filepath.Join(dir, "owner-at-lease")
	env["KVM_BOOT_TIMEOUT"] = "5" // one lease poll, no 300s wait
	env["VIRSH_LEASE_NONE"] = "1"
	imageCache := filepath.Join(dir, "image-cache")
	if err := os.MkdirAll(imageCache, 0o755); err != nil {
		t.Fatalf("create image cache: %v", err)
	}
	env["KVM_IMAGE_CACHE"] = imageCache
	// State the host's /dev/kvm answer, so the case drives create's remaining
	// gates without a real KVM device (see kvm_device_writable). The faked curl
	// writes the base image, so create builds from it instead of downloading.
	env["KVM_FAKE_DEVICE_WRITABLE"] = "1"
	env["PATH"] = kvmGuestPath(t, "virsh", "qemu-img", "cloud-localds", "curl")

	assertStatus(t, RunScript(t, dir, env, repositoryScript(t, "kvm-guest.sh"), "up"), 0)

	virshArgv := readText(t, filepath.Join(dir, "virsh-argv"))
	assertContains(t, "virsh argv", virshArgv, "define")
	assertContains(t, "virsh argv", virshArgv, "start asb-kvm-test")
	assertContains(t, "virsh argv", virshArgv, "domifaddr asb-kvm-test")
	assertOwnerLabeledAtLeaseProbe(t, filepath.Join(dir, "owner-at-lease"))
}

// TestKvmGuestSyncRefusesADetachedCheckout pins fail-closed on an unresolved
// branch: without a branch name the lock key would fall back to `main` and let a
// detached checkout impersonate main's guest.
func TestKvmGuestSyncRefusesADetachedCheckout(t *testing.T) {
	dir := t.TempDir()
	env := kvmGuestEnv(dir)
	delete(env, "REPO_BRANCH") // resolve the branch from git (the faked git prints nothing)
	env["VIRSH_DOMINFO_OK"] = "1"
	env["VIRSH_DOMSTATE"] = "running"
	env["RSYNC_ARGV"] = filepath.Join(dir, "rsync-argv")
	env["PATH"] = kvmGuestPath(t, "virsh", "rsync", "git")

	result := RunScript(t, dir, env, repositoryScript(t, "kvm-guest.sh"), "sync")
	assertStatus(t, result, 1)

	assertContains(t, "stderr", result.Stderr, "not on a branch")
	if _, err := os.Stat(filepath.Join(dir, "rsync-argv")); err == nil {
		t.Error("sync ran from a detached checkout")
	}
}

// TestKvmGuestPauseOnAPausedGuestIsANoOp pins idempotence for the already-paused
// arm: pause succeeds, does not re-suspend, and still drops the label.
func TestKvmGuestPauseOnAPausedGuestIsANoOp(t *testing.T) {
	dir := t.TempDir()
	env := kvmGuestEnv(dir)
	env["VIRSH_DOMINFO_OK"] = "1"
	env["VIRSH_DOMSTATE"] = "paused"
	writeGuestOwnerFile(t, dir, "feat/793-msb-journeys")
	env["VIRSH_ARGV"] = filepath.Join(dir, "virsh-argv")
	env["PATH"] = kvmGuestPath(t, "virsh")

	result := RunScript(t, dir, env, repositoryScript(t, "kvm-guest.sh"), "pause")
	assertStatus(t, result, 0)
	assertNotContains(t, "virsh argv", readText(t, filepath.Join(dir, "virsh-argv")), "suspend")
	if _, err := os.Stat(filepath.Join(dir, "asb-kvm-test.owner")); err == nil {
		t.Error("pause left the owner label behind")
	}
}

// TestKvmGuestDownClearsTheLabelWhenTheDomainIsUndefined pins Review Focus item
// 4: down clears a stale label even when the domain is already gone.
func TestKvmGuestDownClearsTheLabelWhenTheDomainIsUndefined(t *testing.T) {
	dir := t.TempDir()
	env := kvmGuestEnv(dir)
	writeGuestOwnerFile(t, dir, "feat/793-msb-journeys")
	env["PATH"] = kvmGuestPath(t, "virsh")

	result := RunScript(t, dir, env, repositoryScript(t, "kvm-guest.sh"), "down")
	assertStatus(t, result, 0)
	if _, err := os.Stat(filepath.Join(dir, "asb-kvm-test.owner")); err == nil {
		t.Error("down left the owner label behind")
	}
}

// TestKvmGuestDownFailsWhenTheStateQueryFails pins the fail-closed destroy: an
// unreachable or mis-permitted libvirt is not read as an undefined domain, so
// down dies without deleting the disk or clearing the owner label.
func TestKvmGuestDownFailsWhenTheStateQueryFails(t *testing.T) {
	dir := t.TempDir()
	diskPath := filepath.Join(dir, "asb-kvm-test.qcow2")
	if err := os.WriteFile(diskPath, []byte("disk"), 0o644); err != nil {
		t.Fatalf("write disk: %v", err)
	}
	env := kvmGuestEnv(dir)
	writeGuestOwnerFile(t, dir, "feat/793-msb-journeys")
	env["VIRSH_ARGV"] = filepath.Join(dir, "virsh-argv")
	env["VIRSH_LIST_FAIL"] = "1"
	env["PATH"] = kvmGuestPath(t, "virsh")

	result := RunScript(t, dir, env, repositoryScript(t, "kvm-guest.sh"), "down")
	assertStatus(t, result, 1)

	assertContains(t, "stderr", result.Stderr, "cannot query libvirt")
	if _, err := os.Stat(diskPath); err != nil {
		t.Fatalf("down removed the disk after a failed state query: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "asb-kvm-test.owner")); err != nil {
		t.Errorf("down cleared the owner label after a failed state query: %v", err)
	}
	virshArgv := readText(t, filepath.Join(dir, "virsh-argv"))
	assertNotContains(t, "virsh argv", virshArgv, "destroy")
	assertNotContains(t, "virsh argv", virshArgv, "undefine")
}

// TestKvmGuestDownRefusesAnUnexpectedState pins that a state the lock does not
// model is refused by name, leaving the disk of a guest in an unknown state
// alone rather than removing it.
func TestKvmGuestDownRefusesAnUnexpectedState(t *testing.T) {
	dir := t.TempDir()
	diskPath := filepath.Join(dir, "asb-kvm-test.qcow2")
	if err := os.WriteFile(diskPath, []byte("disk"), 0o644); err != nil {
		t.Fatalf("write disk: %v", err)
	}
	env := kvmGuestEnv(dir)
	writeGuestOwnerFile(t, dir, "feat/793-msb-journeys")
	env["VIRSH_ARGV"] = filepath.Join(dir, "virsh-argv")
	env["VIRSH_DOMINFO_OK"] = "1"
	env["VIRSH_DOMSTATE"] = "crashed"
	env["PATH"] = kvmGuestPath(t, "virsh")

	result := RunScript(t, dir, env, repositoryScript(t, "kvm-guest.sh"), "down")
	assertStatus(t, result, 1)

	assertContains(t, "stderr", result.Stderr, "'crashed'")
	if _, err := os.Stat(diskPath); err != nil {
		t.Fatalf("down removed the disk of a guest in an unexpected state: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "asb-kvm-test.owner")); err != nil {
		t.Errorf("down cleared the owner label of a guest in an unexpected state: %v", err)
	}
	assertNotContains(t, "virsh argv", readText(t, filepath.Join(dir, "virsh-argv")), "destroy")
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
	env["VIRSH_DOMSTATE"] = "running"
	env["PATH"] = kvmGuestPath(t, "virsh")
	writeGuestOwnerFile(t, dir, "feat/793-msb-journeys")

	result := RunScript(t, dir, env, repositoryScript(t, "kvm-guest.sh"), "down")
	assertStatus(t, result, 0)

	argv := readText(t, argvPath)
	assertContains(t, "virsh argv", argv, "destroy asb-kvm-test")
	assertContains(t, "virsh argv", argv, "undefine asb-kvm-test")
	if _, err := os.Stat(filepath.Join(dir, "asb-kvm-test.owner")); err == nil {
		t.Error("down left the owner label behind")
	}
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
	env["VIRSH_DOMSTATE"] = "running"
	env["PATH"] = kvmGuestPath(t, "virsh")

	result := RunScript(t, dir, env, repositoryScript(t, "kvm-guest.sh"), "down")
	assertStatus(t, result, 0)

	if _, err := os.Stat(imagePath); err != nil {
		t.Fatalf("cached base image removed by down: %v", err)
	}
}

// kvmGuestVirshHandler serves the `virsh` a script under test calls. It logs
// every invocation to VIRSH_ARGV and answers by subcommand: `domifaddr`
// returns one IPv4 lease (none when VIRSH_LEASE_NONE is set, so a case can leave
// a boot still waiting for one), `dominfo` succeeds only when VIRSH_DOMINFO_OK is
// set (so `down` can exercise the destroy path), `net-info` reports an active
// network, and define/start/destroy/undefine succeed silently.
func kvmGuestVirshHandler(args []string) (string, string, int) {
	recordArgv("VIRSH_ARGV", args)
	joined := strings.Join(args, " ")
	switch {
	case strings.Contains(joined, "domifaddr"):
		recordOwnerAtLeaseProbe()
		if envOption("VIRSH_LEASE_NONE") != "" {
			return "", "", 0
		}
		return " vnet0      52:54:00:00:00:00    ipv4         192.168.122.55/24  \n", "", 0
	case strings.Contains(joined, "dominfo"):
		if envOption("VIRSH_DOMINFO_OK") != "" {
			return "Name:           asb-kvm-test\n", "", 0
		}
		return "", "error: failed to get domain 'asb-kvm-test'\n", 1
	case strings.Contains(joined, "list"):
		// `virsh list --all --name` proves a domain is defined. VIRSH_LIST_FAIL
		// models an unreachable/mis-permitted libvirt so a case can pin that the
		// state query failing is fatal, not read as absent.
		if envOption("VIRSH_LIST_FAIL") != "" {
			return "", "error: failed to connect to the hypervisor\n", 1
		}
		if envOption("VIRSH_DOMINFO_OK") != "" {
			return "asb-kvm-test\n", "", 0
		}
		return "", "", 0
	case strings.Contains(joined, "domstate"):
		if envOption("VIRSH_DOMSTATE_FAIL") != "" {
			return "", "error: failed to get domain 'asb-kvm-test'\n", 1
		}
		return envOption("VIRSH_DOMSTATE") + "\n", "", 0
	case strings.Contains(joined, "net-info"):
		return "Name:           default\nActive:         yes\n", "", 0
	case strings.Contains(joined, "net-dumpxml"):
		return "<network><name>default</name><bridge name='virbr0'/>" +
			"<ip address='192.168.122.1' netmask='255.255.255.0'><dhcp/></ip></network>\n", "", 0
	default:
		return "", "", 0
	}
}

// recordOwnerAtLeaseProbe captures the owner label's content the first time the
// guest is polled for an address — the wait_for_lease gate — into the file named
// by VIRSH_OWNER_AT_PROBE. A case seeds a small KVM_BOOT_TIMEOUT so wait_for_lease
// polls a bounded number of times whether or not it gets a lease.
//
// The guest enters the lock the moment it is resumed/started/defined, so the
// label must already exist when that first poll runs: assertOwnerLabeledAtLeaseProbe
// reads this capture, and a regression that moved the write back after the wait
// would leave the file empty (readText returns "" for an absent file) and fail
// the case.
func recordOwnerAtLeaseProbe() {
	path := envOption("VIRSH_OWNER_AT_PROBE")
	if path == "" {
		return
	}
	if _, err := os.Stat(path); err == nil {
		return // first poll only: record the label as it stood at the lease wait
	}
	owner := filepath.Join(envOption("ARTIFACT_DIR"), envOption("VM_NAME")+".owner")
	data, _ := os.ReadFile(owner)
	_ = os.WriteFile(path, data, 0o600)
}

// kvmGuestSSHHandler serves the `ssh` a script under test calls: it logs the
// invocation and succeeds, so a case can assert the guest target and the
// command the script forwarded.
func kvmGuestSSHHandler(args []string) (string, string, int) {
	recordArgv("SSH_ARGV", args)
	joined := strings.Join(args, " ")
	if path := envOption("SSH_STDIN"); path != "" && envOption("SSH_STDIN_MATCH") != "" &&
		strings.Contains(joined, envOption("SSH_STDIN_MATCH")) {
		data, _ := io.ReadAll(os.Stdin)
		_ = os.WriteFile(path, data, 0o644)
	}
	if envOption("SSH_GH_AUTH_FAIL") != "" && strings.Contains(joined, "gh auth login") {
		return "", "error: authentication failed\n", 1
	}
	if fail := envOption("SSH_FAIL_CONTAINS"); fail != "" && strings.Contains(joined, fail) {
		return "", "ssh: simulated failure\n", 255
	}
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

// kvmGuestQemuImgHandler serves `qemu-img`: create's check_host requires it, and
// a case that drives the create path fakes it to write the overlay image at the
// path create names, so the build proceeds without a real qemu-img.
func kvmGuestQemuImgHandler(args []string) (string, string, int) {
	if len(args) > 0 {
		_ = os.WriteFile(args[len(args)-1], []byte("FAKE-OVERLAY\n"), 0o644)
	}
	return "", "", 0
}

// kvmGuestCloudLocaldsHandler serves `cloud-localds`, which create's check_host
// requires and build_seed runs; it writes the seed image it is asked to produce.
func kvmGuestCloudLocaldsHandler(args []string) (string, string, int) {
	if len(args) > 0 {
		_ = os.WriteFile(args[0], []byte("FAKE-SEED\n"), 0o644)
	}
	return "", "", 0
}

// kvmGuestCurlHandler serves `curl`: it writes the output named by -o, so a case
// can drive create's base-image fetch without a download.
func kvmGuestCurlHandler(args []string) (string, string, int) {
	for i, arg := range args {
		if arg == "-o" && i+1 < len(args) {
			_ = os.WriteFile(args[i+1], []byte("FAKE-IMAGE\n"), 0o644)
		}
	}
	return "", "", 0
}

// rewrite's `-v <pattern> <file>` prints the lines that do not match, modelling
// only a leading `^` anchor and a literal substring, not `.*`/`$`: the one case
// that puts this fake on PATH sets GREP_READ_ERROR and short-circuits before
// matching, so the partial matcher is never reached. GREP_READ_ERROR makes the
// rewrite fail with grep's read-error status (2), so a case can pin that a failed
// rewrite does not clobber the host's authorized_keys. A `-qxF <name>` call
// (guest_state's defined-domain probe) reads the domain list on stdin and matches
// a whole line. The other authorized_keys cases run the real grep (their PATH has
// no grep fake), so the real pattern is exercised there.
func kvmGuestGrepHandler(args []string) (string, string, int) {
	recordArgv("GREP_ARGV", args)
	if len(args) == 2 && args[0] == "-qxF" {
		data, _ := io.ReadAll(os.Stdin)
		lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
		if slices.Contains(lines, args[1]) {
			return "", "", 0
		}
		return "", "", 1
	}
	if len(args) != 3 || args[0] != "-v" {
		return "", "grep fake: unexpected arguments\n", 2
	}
	pattern, path := args[1], args[2]
	anchored := strings.HasPrefix(pattern, "^")
	pattern = strings.TrimPrefix(pattern, "^")
	data, err := os.ReadFile(path)
	if err != nil {
		return "", "grep: " + err.Error() + "\n", 2
	}
	if envOption("GREP_READ_ERROR") != "" {
		return "", "grep: simulated read error\n", 2
	}
	var lines []string
	if len(data) > 0 {
		lines = strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	}
	var out []string
	for _, line := range lines {
		matches := strings.Contains(line, pattern)
		if anchored {
			matches = strings.HasPrefix(line, pattern)
		}
		if !matches {
			out = append(out, line)
		}
	}
	if len(out) == 0 {
		return "", "", 1
	}
	return strings.Join(out, "\n") + "\n", "", 0
}

// kvmGuestGitHandler serves the `git` a script under test calls: it logs the
// invocation and succeeds, or exits non-zero when GIT_EXIT is set, so a case can
// pin that a fetch failure propagates. It never touches a repository.
func kvmGuestGitHandler(args []string) (string, string, int) {
	recordArgv("GIT_ARGV", args)
	if envOption("GIT_EXIT") != "" {
		return "", "", 1
	}
	joined := strings.Join(args, " ")
	switch {
	case strings.Contains(joined, "--git-common-dir"):
		return envOption("GIT_COMMON_DIR") + "\n", "", 0
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

// kvmGuestUnameHandler reports x86_64 so the script's host-arch gate passes on a
// PATH that carries only fakes (the arch case runs at startup, before dispatch).
func kvmGuestUnameHandler(_ []string) (string, string, int) {
	return "x86_64\n", "", 0
}

// kvmGuestDirnameHandler and kvmGuestBasenameHandler stand in for the two
// coreutils the script resolves its own paths with, so a fake-only PATH reaches
// acquire_guest instead of aborting during startup.
func kvmGuestDirnameHandler(args []string) (string, string, int) {
	return filepath.Dir(strings.Join(args, " ")) + "\n", "", 0
}

func kvmGuestBasenameHandler(args []string) (string, string, int) {
	return filepath.Base(strings.Join(args, " ")) + "\n", "", 0
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
