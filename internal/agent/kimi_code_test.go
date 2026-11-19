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

func TestKimiCodeLookup(t *testing.T) {
	a, ok := agent.Lookup("kimi-code")
	if !ok {
		t.Fatal("Lookup(\"kimi-code\") returned not-ok")
	}
	if a.Name() != "kimi-code" {
		t.Errorf("Name() = %q, want kimi-code", a.Name())
	}
	if a.ConfigDirName() != "kimi-code" {
		t.Errorf("ConfigDirName = %q, want kimi-code", a.ConfigDirName())
	}
	if !a.Experimental() {
		t.Error("Experimental() = false, want true")
	}
	if !slices.Contains(agent.Names(), "kimi-code") {
		t.Errorf("Names() = %v, want to include kimi-code", agent.Names())
	}
}

func TestKimiCodeImageSpec(t *testing.T) {
	a, _ := agent.Lookup("kimi-code")
	spec := a.ImageSpec()
	if spec.VersionArg != "KIMI_CODE_VERSION" {
		t.Errorf("VersionArg = %q, want KIMI_CODE_VERSION", spec.VersionArg)
	}
	for _, want := range []string{
		`https://github.com/MoonshotAI/kimi-code/releases/download/%40moonshot-ai%2Fkimi-code%40${KIMI_CODE_VERSION}`,
		`$kimi_asset.sha256`,
		`sha256sum --check --strict`,
		`install -m 0755 "$kimi_dir/kimi" /opt/agents-sandbox/bin/kimi`,
		`KIMI_CODE_NO_AUTO_UPDATE=1 /opt/agents-sandbox/bin/kimi --version`,
	} {
		if !strings.Contains(spec.InstallCommand, want) {
			t.Errorf("InstallCommand = %q, want it to contain %q", spec.InstallCommand, want)
		}
	}
	if strings.Index(spec.InstallCommand, "sha256sum") > strings.Index(spec.InstallCommand, "tar -xzf") {
		t.Errorf("InstallCommand = %q must verify the checksum before extracting", spec.InstallCommand)
	}
	if got := spec.AgentEnv["KIMI_CODE_NO_AUTO_UPDATE"]; got != "1" || len(spec.AgentEnv) != 1 {
		t.Errorf("AgentEnv = %v, want only KIMI_CODE_NO_AUTO_UPDATE=1", spec.AgentEnv)
	}
	if spec.RequiresNode {
		t.Error("RequiresNode = true, want false (kimi ships a Node single-executable)")
	}
	if spec.OwnsToolTree {
		t.Error("OwnsToolTree = true, want false (kimi keeps its runtime state under ~/.kimi-code)")
	}
	if want := []string{"libstdc++.so.6", "libgcc_s.so.1"}; !slices.Equal(spec.RuntimeLibs, want) {
		t.Errorf("RuntimeLibs = %v, want %v", spec.RuntimeLibs, want)
	}
}

// fakeKimiCodeRelease runs the install command's download-verify-extract part
// with curl, uname, tar, and install replaced by shell functions, so the
// architecture mapping and checksum verification are observable without a
// network or root. machine is what `uname -m` reports; checksum is the body the
// fake .sha256 download receives. tar and install only record what they were
// handed.
func fakeKimiCodeRelease(t *testing.T, machine, checksum string) (string, error) {
	t.Helper()
	a, _ := agent.Lookup("kimi-code")
	install := a.ImageSpec().InstallCommand
	install = strings.ReplaceAll(install, "/tmp/kimi-code", t.TempDir())
	install, _, _ = strings.Cut(install, " && KIMI_CODE_NO_AUTO_UPDATE=1")
	stubs := `uname() { echo '` + machine + `'; }; ` +
		`curl() { while [ "$1" != -o ]; do url="$1"; shift; done; out="$2"; ` +
		`case "$url" in *.sha256) printf '%s' "$CHECKSUM" > "$out" ;; *) printf 'binary' > "$out" ;; esac; }; ` +
		`tar() { echo "extracted $(basename "$2")"; }; ` +
		`install() { echo "installed $(basename "$3")"; }; `
	cmd := exec.Command("sh", "-c", stubs+install)
	cmd.Env = append(cmd.Environ(), "CHECKSUM="+checksum, "KIMI_CODE_VERSION=2.7.0")
	output, err := cmd.CombinedOutput()
	return string(output), err
}

// kimiAssetChecksum is the sha256 of the fake asset body "binary".
const kimiAssetChecksum = "9a3a45d01531a20e89ac6ae10b0b0beb0492acd7216a368aa062d1a5fecaf9cd"

func TestKimiCodeInstallMapsArchitecture(t *testing.T) {
	for machine, asset := range map[string]string{
		"x86_64":  "kimi-code-linux-x64.tar.gz",
		"amd64":   "kimi-code-linux-x64.tar.gz",
		"aarch64": "kimi-code-linux-arm64.tar.gz",
		"arm64":   "kimi-code-linux-arm64.tar.gz",
	} {
		t.Run(machine, func(t *testing.T) {
			output, err := fakeKimiCodeRelease(t, machine, kimiAssetChecksum+"  "+asset)
			if err != nil {
				t.Fatalf("install failed: %v\n%s", err, output)
			}
			if !strings.Contains(output, "extracted "+asset) || !strings.Contains(output, "installed kimi") {
				t.Errorf("output = %q, want it to extract %s and install kimi", output, asset)
			}
		})
	}
}

func TestKimiCodeInstallRejectsUnsupportedArchitecture(t *testing.T) {
	output, err := fakeKimiCodeRelease(t, "riscv64", kimiAssetChecksum+"  kimi-code-linux-riscv64.tar.gz")
	if err == nil {
		t.Fatalf("install succeeded on riscv64, want a build failure:\n%s", output)
	}
	if !strings.Contains(output, "unsupported architecture riscv64") || strings.Contains(output, "installed") {
		t.Errorf("output = %q, want an unsupported-architecture error and no install", output)
	}
}

func TestKimiCodeInstallRejectsBadChecksums(t *testing.T) {
	for name, checksum := range map[string]string{
		"mismatch":       strings.Repeat("0", 64) + "  kimi-code-linux-x64.tar.gz",
		"other asset":    kimiAssetChecksum + "  kimi-code-linux-arm64.tar.gz",
		"malformed hash": "deadbeef  kimi-code-linux-x64.tar.gz",
		"empty file":     "",
	} {
		t.Run(name, func(t *testing.T) {
			output, err := fakeKimiCodeRelease(t, "x86_64", checksum)
			if err == nil {
				t.Fatalf("install succeeded, want a build failure:\n%s", output)
			}
			if !strings.Contains(output, "kimi-code:") || strings.Contains(output, "extracted") {
				t.Errorf("output = %q, want a kimi-code checksum error before extraction", output)
			}
		})
	}
}

func TestKimiCodeAttachCommandQuotesArgs(t *testing.T) {
	a, _ := agent.Lookup("kimi-code")
	runner, ok := agent.AsAttachRunner(a)
	if !ok {
		t.Fatal("kimi-code should implement AttachRunner")
	}
	if got, want := runner.AttachCommand("/workspace", []string{"--model", "x"}), "kimi '--model' 'x'"; got != want {
		t.Errorf("AttachCommand = %q, want %q", got, want)
	}
}

func TestKimiCodeAttachCommandPreservesShellSpecialPrompts(t *testing.T) {
	a, _ := agent.Lookup("kimi-code")
	runner, _ := agent.AsAttachRunner(a)
	prompts := []string{"Explain this project's tests", "cost in $HOME; `id` && echo hi", ""}
	command := runner.AttachCommand("/workspace", prompts)
	// printf stands in for kimi so the shell's word splitting is observable.
	script := "kimi() { printf '[%s]' \"$@\"; }; " + command
	output, err := exec.Command("bash", "-c", script).Output()
	if err != nil {
		t.Fatalf("bash -c %q: %v", script, err)
	}
	if want := "[Explain this project's tests][cost in $HOME; `id` && echo hi][]"; string(output) != want {
		t.Errorf("forwarded args = %s, want %s", output, want)
	}
}

func TestKimiCodeConfigMerger(t *testing.T) {
	a, _ := agent.Lookup("kimi-code")
	merger, ok := agent.AsConfigMerger(a)
	if !ok {
		t.Fatal("kimi-code should implement ConfigMerger")
	}
	if merger.SnippetPattern() != "config*.toml" {
		t.Errorf("SnippetPattern = %q, want config*.toml", merger.SnippetPattern())
	}
	if got := merger.VMConfigPath("/home/dev"); got != filepath.Join("/home/dev", ".kimi-code", "config.toml") {
		t.Errorf("VMConfigPath = %q", got)
	}
	if names := merger.ConfigFileNames(); !slices.Equal(names, []string{"config.toml"}) {
		t.Errorf("ConfigFileNames = %v, want [config.toml]", names)
	}
}

// TestKimiCodeProvisionRulesCopyNoCredentials pins that the drop-in carries
// only credential-free files: config.toml can hold a literal api_key, mcp.json
// MCP tokens, and credentials/ rotating OAuth logins.
func TestKimiCodeProvisionRulesCopyNoCredentials(t *testing.T) {
	a, _ := agent.Lookup("kimi-code")
	provisioner, ok := agent.AsProvisioner(a)
	if !ok {
		t.Fatal("kimi-code should implement Provisioner")
	}
	rules := provisioner.ProvisionRules()
	want := []string{"tui.toml", "AGENTS.md"}
	if len(rules) != 1 || rules[0].Dir != ".kimi-code" || !slices.Equal(rules[0].Patterns, want) {
		t.Errorf("ProvisionRules = %+v, want [.kimi-code: %v]", rules, want)
	}
}

func TestKimiCodeVersion(t *testing.T) {
	a, _ := agent.Lookup("kimi-code")
	provider, ok := agent.AsVersionProvider(a)
	if !ok {
		t.Fatal("kimi-code should implement VersionProvider")
	}
	if provider.VersionCmd() != "kimi --version" {
		t.Errorf("VersionCmd = %q, want kimi --version", provider.VersionCmd())
	}
	got, err := provider.ParseVersion("2.7.0\n")
	if err != nil || got != "2.7.0" {
		t.Errorf("ParseVersion = %q, %v, want 2.7.0", got, err)
	}
}

func TestKimiCodeUpgradeChecker(t *testing.T) {
	a, _ := agent.Lookup("kimi-code")
	checker, ok := agent.AsUpgradeChecker(a)
	if !ok {
		t.Fatal("kimi-code should implement UpgradeChecker")
	}
	newer, err := checker.NewerThan("2.7.1", "2.7.0")
	if err != nil || !newer {
		t.Errorf("NewerThan(2.7.1, 2.7.0) = %v, %v, want true", newer, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := checker.LatestVersion(ctx); err == nil {
		t.Error("LatestVersion with cancelled ctx should error")
	}
}

func TestKimiCodeHasNoDaemonOrWorktree(t *testing.T) {
	a, _ := agent.Lookup("kimi-code")
	if _, ok := agent.AsDaemonProvider(a); ok {
		t.Error("kimi-code must not implement DaemonProvider")
	}
	if _, ok := agent.AsWorktreeProvider(a); ok {
		t.Error("kimi-code must not implement WorktreeProvider")
	}
	if _, ok := agent.AsEventStreamProvider(a); ok {
		t.Error("kimi-code must not implement EventStreamProvider")
	}
	if _, ok := agent.AsGuestToolRequirer(a); ok {
		t.Error("kimi-code must not implement GuestToolRequirer (no daemon tools)")
	}
}
