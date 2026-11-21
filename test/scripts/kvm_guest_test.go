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

	dockerArgv := readText(t, dockerLog)
	assertContains(t, "docker argv", dockerArgv, "images --format")
	assertContains(t, "docker argv", dockerArgv, "save agents-sandbox/runner-opencode-abc:opencode-latest")

	sshArgv := readText(t, sshLog)
	assertContains(t, "ssh argv", sshArgv, "dev@192.168.122.55")
	assertContains(t, "ssh argv", sshArgv, "docker load")
	assertContains(t, "ssh argv", sshArgv, "mkdir -p '"+filepath.Join(root, "smoke", "data", "agents-sandbox")+"'")

	rsyncArgv := readText(t, rsyncLog)
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
	return "", "", 0
}

// kvmGuestRsyncHandler serves the `rsync` a script under test calls: it logs
// the invocation and succeeds.
func kvmGuestRsyncHandler(args []string) (string, string, int) {
	recordArgv("RSYNC_ARGV", args)
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
