package agent_test

import (
	"context"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/inoio/agents-sandbox/internal/agent"
)

func TestOhMyPiLookup(t *testing.T) {
	a, ok := agent.Lookup("oh-my-pi")
	if !ok {
		t.Fatal("Lookup(\"oh-my-pi\") returned not-ok")
	}
	if a.Name() != "oh-my-pi" {
		t.Errorf("Name() = %q, want oh-my-pi", a.Name())
	}
	if a.ConfigDirName() != "oh-my-pi" {
		t.Errorf("ConfigDirName = %q, want oh-my-pi", a.ConfigDirName())
	}
	if !a.Experimental() {
		t.Error("Experimental() = false, want true")
	}
	if !slices.Contains(agent.Names(), "oh-my-pi") {
		t.Errorf("Names() = %v, want to include oh-my-pi", agent.Names())
	}
}

func TestOhMyPiImageSpec(t *testing.T) {
	a, _ := agent.Lookup("oh-my-pi")
	spec := a.ImageSpec()
	if spec.VersionArg != "OH_MY_PI_VERSION" {
		t.Errorf("VersionArg = %q, want OH_MY_PI_VERSION", spec.VersionArg)
	}
	for _, want := range []string{
		`https://github.com/can1357/oh-my-pi/releases/download/v${OH_MY_PI_VERSION}`,
		`SHA256SUMS.txt`,
		`sha256sum --check --strict`,
		`install -m 0755 "$omp_dir/$omp_asset" /opt/agents-sandbox/bin/omp`,
		`printf 'startup:\n  checkUpdate: false\n' > /opt/agents-sandbox/oh-my-pi/launcher.yml`,
		`/opt/agents-sandbox/bin/omp --config /opt/agents-sandbox/oh-my-pi/launcher.yml --version`,
	} {
		if !strings.Contains(spec.InstallCommand, want) {
			t.Errorf("InstallCommand = %q, want it to contain %q", spec.InstallCommand, want)
		}
	}
	if strings.Index(spec.InstallCommand, "sha256sum") > strings.Index(spec.InstallCommand, "install -m") {
		t.Errorf("InstallCommand = %q must verify the checksum before installing", spec.InstallCommand)
	}
	if spec.RequiresNode {
		t.Error("RequiresNode = true, want false (omp ships a standalone binary)")
	}
	if len(spec.AgentEnv) != 0 {
		t.Errorf("AgentEnv = %v, want none", spec.AgentEnv)
	}
	if len(spec.OwnedToolPaths) != 0 {
		t.Errorf("OwnedToolPaths = %v, want none (omp never writes into its install tree)", spec.OwnedToolPaths)
	}
}

// fakeOhMyPiRelease runs the install command's download-and-verify part with
// curl, uname, install, and the omp binary replaced by shell functions, so the
// architecture mapping and checksum verification are observable without a
// network or root. machine is what `uname -m` reports; checksums is the body
// the fake SHA256SUMS.txt download receives. The install step only records the
// asset it was handed.
func fakeOhMyPiRelease(t *testing.T, machine, checksums string) (string, error) {
	t.Helper()
	a, _ := agent.Lookup("oh-my-pi")
	install := a.ImageSpec().InstallCommand
	downloadDir := t.TempDir()
	install = strings.ReplaceAll(install, "/tmp/oh-my-pi", downloadDir)
	install, _, _ = strings.Cut(install, " && mkdir -p /opt/agents-sandbox/oh-my-pi")
	stubs := `uname() { echo '` + machine + `'; }; ` +
		`curl() { while [ "$1" != -o ]; do url="$1"; shift; done; out="$2"; ` +
		`case "$url" in */SHA256SUMS.txt) printf '%s' "$CHECKSUMS" > "$out" ;; *) printf 'binary' > "$out" ;; esac; }; ` +
		`install() { echo "installed $(basename "$3")"; }; `
	cmd := exec.Command("sh", "-c", stubs+install)
	cmd.Env = append(cmd.Environ(), "CHECKSUMS="+checksums, "OH_MY_PI_VERSION=18.14.0")
	output, err := cmd.CombinedOutput()
	return string(output), err
}

// binaryChecksum is the sha256 of the fake asset body "binary".
const binaryChecksum = "9a3a45d01531a20e89ac6ae10b0b0beb0492acd7216a368aa062d1a5fecaf9cd"

func TestOhMyPiInstallMapsArchitecture(t *testing.T) {
	for machine, asset := range map[string]string{
		"x86_64":  "omp-linux-x64",
		"amd64":   "omp-linux-x64",
		"aarch64": "omp-linux-arm64",
		"arm64":   "omp-linux-arm64",
	} {
		t.Run(machine, func(t *testing.T) {
			output, err := fakeOhMyPiRelease(t, machine, binaryChecksum+"  "+asset+"\n")
			if err != nil {
				t.Fatalf("install failed: %v\n%s", err, output)
			}
			if !strings.Contains(output, "installed "+asset) {
				t.Errorf("output = %q, want it to install %s", output, asset)
			}
		})
	}
}

func TestOhMyPiInstallRejectsUnsupportedArchitecture(t *testing.T) {
	output, err := fakeOhMyPiRelease(t, "riscv64", binaryChecksum+"  omp-linux-riscv64\n")
	if err == nil {
		t.Fatalf("install succeeded on riscv64, want a build failure:\n%s", output)
	}
	if !strings.Contains(output, "unsupported architecture riscv64") || strings.Contains(output, "installed") {
		t.Errorf("output = %q, want an unsupported-architecture error and no install", output)
	}
}

func TestOhMyPiInstallRejectsBadChecksums(t *testing.T) {
	otherChecksum := strings.Repeat("0", 64)
	for name, checksums := range map[string]string{
		"mismatch":        otherChecksum + "  omp-linux-x64\n",
		"missing entry":   binaryChecksum + "  omp-linux-arm64\n",
		"malformed entry": "deadbeef  omp-linux-x64\n",
		"empty file":      "",
	} {
		t.Run(name, func(t *testing.T) {
			output, err := fakeOhMyPiRelease(t, "x86_64", checksums)
			if err == nil {
				t.Fatalf("install succeeded, want a build failure:\n%s", output)
			}
			if !strings.Contains(output, "oh-my-pi:") || strings.Contains(output, "installed") {
				t.Errorf("output = %q, want an oh-my-pi checksum error and no install", output)
			}
		})
	}
}

func TestOhMyPiAttachCommandUsesLauncherOverlay(t *testing.T) {
	a, _ := agent.Lookup("oh-my-pi")
	runner, ok := agent.AsAttachRunner(a)
	if !ok {
		t.Fatal("oh-my-pi should implement AttachRunner")
	}
	got := runner.AttachCommand("/workspace", []string{"--model", "x"})
	want := "omp --config /opt/agents-sandbox/oh-my-pi/launcher.yml '--model' 'x'"
	if got != want {
		t.Errorf("AttachCommand = %q, want %q", got, want)
	}
}

func TestOhMyPiAttachCommandPreservesShellSpecialPrompts(t *testing.T) {
	a, _ := agent.Lookup("oh-my-pi")
	runner, _ := agent.AsAttachRunner(a)
	prompts := []string{"Explain this project's tests", "cost in $HOME; `id` && echo hi", ""}
	command := runner.AttachCommand("/workspace", prompts)
	// printf stands in for omp so the shell's word splitting is observable.
	script := "omp() { shift 2; printf '[%s]' \"$@\"; }; " + command
	output, err := exec.Command("bash", "-c", script).Output()
	if err != nil {
		t.Fatalf("bash -c %q: %v", script, err)
	}
	want := "[Explain this project's tests][cost in $HOME; `id` && echo hi][]"
	if string(output) != want {
		t.Errorf("forwarded args = %s, want %s", output, want)
	}
}

func TestOhMyPiConfigMerger(t *testing.T) {
	a, _ := agent.Lookup("oh-my-pi")
	merger, ok := agent.AsConfigMerger(a)
	if !ok {
		t.Fatal("oh-my-pi should implement ConfigMerger")
	}
	if merger.SnippetPattern() != "config*.{yml,yaml}" {
		t.Errorf("SnippetPattern = %q, want config*.{yml,yaml}", merger.SnippetPattern())
	}
	if got := merger.VMConfigPath("/home/dev"); got != filepath.Join("/home/dev", ".omp", "agent", "config.yml") {
		t.Errorf("VMConfigPath = %q", got)
	}
	if names := merger.ConfigFileNames(); !slices.Equal(names, []string{"config.yml", "config.yaml"}) {
		t.Errorf("ConfigFileNames = %v, want [config.yml config.yaml]", names)
	}
}

func TestOhMyPiProvisionRulesCopyOnlyConfig(t *testing.T) {
	a, _ := agent.Lookup("oh-my-pi")
	provisioner, ok := agent.AsProvisioner(a)
	if !ok {
		t.Fatal("oh-my-pi should implement Provisioner")
	}
	rules := provisioner.ProvisionRules()
	want := []string{"config.yml", "config.yaml"}
	if len(rules) != 1 || rules[0].Dir != ".omp/agent" || !slices.Equal(rules[0].Patterns, want) {
		t.Errorf("ProvisionRules = %+v, want [.omp/agent: %v]", rules, want)
	}
}

func TestOhMyPiVersion(t *testing.T) {
	a, _ := agent.Lookup("oh-my-pi")
	provider, ok := agent.AsVersionProvider(a)
	if !ok {
		t.Fatal("oh-my-pi should implement VersionProvider")
	}
	if provider.VersionCmd() != "omp --version" {
		t.Errorf("VersionCmd = %q, want omp --version", provider.VersionCmd())
	}
	got, err := provider.ParseVersion("omp/18.14.0\n")
	if err != nil || got != "18.14.0" {
		t.Errorf("ParseVersion = %q, %v, want 18.14.0", got, err)
	}
}

func TestOhMyPiUpgradeChecker(t *testing.T) {
	a, _ := agent.Lookup("oh-my-pi")
	checker, ok := agent.AsUpgradeChecker(a)
	if !ok {
		t.Fatal("oh-my-pi should implement UpgradeChecker")
	}
	newer, err := checker.NewerThan("18.15.0", "18.14.0")
	if err != nil || !newer {
		t.Errorf("NewerThan(18.15.0, 18.14.0) = %v, %v, want true", newer, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := checker.LatestVersion(ctx); err == nil {
		t.Error("LatestVersion with cancelled ctx should error")
	}
}

func TestOhMyPiHasNoDaemonOrWorktree(t *testing.T) {
	a, _ := agent.Lookup("oh-my-pi")
	if _, ok := agent.AsDaemonProvider(a); ok {
		t.Error("oh-my-pi must not implement DaemonProvider")
	}
	if _, ok := agent.AsWorktreeProvider(a); ok {
		t.Error("oh-my-pi must not implement WorktreeProvider")
	}
	if _, ok := agent.AsEventStreamProvider(a); ok {
		t.Error("oh-my-pi must not implement EventStreamProvider")
	}
	if _, ok := agent.AsGuestToolRequirer(a); ok {
		t.Error("oh-my-pi must not implement GuestToolRequirer (no daemon tools)")
	}
}
