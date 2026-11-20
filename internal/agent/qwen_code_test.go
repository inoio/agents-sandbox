package agent_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/inoio/agents-sandbox/internal/agent"
)

func TestQwenCodeLookup(t *testing.T) {
	a, ok := agent.Lookup("qwen-code")
	if !ok {
		t.Fatal("Lookup(\"qwen-code\") returned not-ok")
	}
	if a.Name() != "qwen-code" {
		t.Errorf("Name() = %q, want qwen-code", a.Name())
	}
	if a.ConfigDirName() != "qwen-code" {
		t.Errorf("ConfigDirName = %q, want qwen-code", a.ConfigDirName())
	}
	if !a.Experimental() {
		t.Error("Experimental() = false, want true")
	}
	if !slices.Contains(agent.Names(), "qwen-code") {
		t.Errorf("Names() = %v, want to include qwen-code", agent.Names())
	}
}

func TestQwenCodeImageSpec(t *testing.T) {
	a, _ := agent.Lookup("qwen-code")
	spec := a.ImageSpec()
	if spec.VersionArg != "QWEN_CODE_VERSION" {
		t.Errorf("VersionArg = %q, want QWEN_CODE_VERSION", spec.VersionArg)
	}
	for _, want := range []string{
		`https://github.com/QwenLM/qwen-code/releases/download/v${QWEN_CODE_VERSION}`,
		`SHA256SUMS`,
		`sha256sum --check --strict`,
		`-C /opt/agents-sandbox/qwen-code --strip-components=1`,
		`'exec /opt/agents-sandbox/qwen-code/bin/qwen "$@"'`,
		`> /opt/agents-sandbox/bin/qwen && chmod 755 /opt/agents-sandbox/bin/qwen`,
		`'{"general":{"enableAutoUpdate":false}}' > /opt/agents-sandbox/qwen-code/system-settings.json`,
		`/opt/agents-sandbox/bin/qwen --version`,
	} {
		if !strings.Contains(spec.InstallCommand, want) {
			t.Errorf("InstallCommand = %q, want it to contain %q", spec.InstallCommand, want)
		}
	}
	if strings.Index(spec.InstallCommand, "sha256sum") > strings.Index(spec.InstallCommand, "tar -xzf") {
		t.Errorf("InstallCommand = %q must verify the checksum before extracting", spec.InstallCommand)
	}
	// The system settings file must live under the tool tree, the only path the
	// runner copies from the agent stage; a path outside it (the default
	// /etc/qwen-code/settings.json) would be dropped from the shipped image.
	// QWEN_CODE_SYSTEM_SETTINGS_PATH points qwen at the relocated file.
	if want := "/opt/agents-sandbox/qwen-code/system-settings.json"; spec.AgentEnv["QWEN_CODE_SYSTEM_SETTINGS_PATH"] != want {
		t.Errorf(
			"AgentEnv[QWEN_CODE_SYSTEM_SETTINGS_PATH] = %q, want %q",
			spec.AgentEnv["QWEN_CODE_SYSTEM_SETTINGS_PATH"],
			want,
		)
	}
	if len(spec.AgentEnv) != 1 {
		t.Errorf("AgentEnv = %v, want only QWEN_CODE_SYSTEM_SETTINGS_PATH", spec.AgentEnv)
	}
	if spec.RequiresNode {
		t.Error("RequiresNode = true, want false (the standalone release bundles its Node)")
	}
	if spec.OwnsToolTree {
		t.Error("OwnsToolTree = true, want false (qwen keeps its runtime state under ~/.qwen)")
	}
	if want := []string{"libstdc++.so.6", "libgcc_s.so.1"}; !slices.Equal(spec.RuntimeLibs, want) {
		t.Errorf("RuntimeLibs = %v, want %v", spec.RuntimeLibs, want)
	}
}

// fakeQwenCodeRelease runs the install command's resolve-download-verify part
// with curl and uname replaced by shell functions, so the architecture mapping
// and the checksum verification are observable without a network or root.
// machine is what `uname -m` reports; sums is the body the fake SHA256SUMS
// download receives. The command is cut before it extracts into the tool tree,
// and "verified" marks that the verification passed.
func fakeQwenCodeRelease(t *testing.T, machine, sums string) (string, error) {
	t.Helper()
	a, _ := agent.Lookup("qwen-code")
	install := a.ImageSpec().InstallCommand
	install = strings.ReplaceAll(install, "/tmp/qwen-code", t.TempDir())
	install, _, found := strings.Cut(install, " && mkdir -p /opt/agents-sandbox/qwen-code")
	if !found {
		t.Fatalf("InstallCommand = %q has no tool-tree extraction step", a.ImageSpec().InstallCommand)
	}
	stubs := `uname() { echo '` + machine + `'; }; ` +
		`curl() { while [ "$1" != -o ]; do url="$1"; shift; done; out="$2"; ` +
		`case "$url" in */SHA256SUMS) printf '%s' "$SUMS" > "$out" ;; *) echo "downloaded $(basename "$url")"; printf 'binary' > "$out" ;; esac; }; `
	cmd := exec.Command("sh", "-c", stubs+install+" && echo verified")
	cmd.Env = append(cmd.Environ(), "SUMS="+sums, "QWEN_CODE_VERSION=0.28.0")
	output, err := cmd.CombinedOutput()
	return string(output), err
}

// qwenAssetChecksum is the sha256 of the fake asset body "binary".
const qwenAssetChecksum = "9a3a45d01531a20e89ac6ae10b0b0beb0492acd7216a368aa062d1a5fecaf9cd"

// qwenSums renders a SHA256SUMS body that lists the preview build beside the
// asset, as the real release does, so the verification must pick the exact
// asset line.
func qwenSums(assetHash, asset string) string {
	return strings.Repeat("1", 64) + "  qwen-code-linux-x64-opentui-preview.tar.gz\n" +
		assetHash + "  " + asset + "\n"
}

func TestQwenCodeInstallMapsArchitecture(t *testing.T) {
	for machine, asset := range map[string]string{
		"x86_64":  "qwen-code-linux-x64.tar.gz",
		"amd64":   "qwen-code-linux-x64.tar.gz",
		"aarch64": "qwen-code-linux-arm64.tar.gz",
		"arm64":   "qwen-code-linux-arm64.tar.gz",
	} {
		t.Run(machine, func(t *testing.T) {
			output, err := fakeQwenCodeRelease(t, machine, qwenSums(qwenAssetChecksum, asset))
			if err != nil {
				t.Fatalf("install failed: %v\n%s", err, output)
			}
			if !strings.Contains(output, "downloaded "+asset) || !strings.Contains(output, "verified") {
				t.Errorf("output = %q, want it to download and verify %s", output, asset)
			}
		})
	}
}

func TestQwenCodeInstallRejectsUnsupportedArchitecture(t *testing.T) {
	output, err := fakeQwenCodeRelease(t, "riscv64", qwenSums(qwenAssetChecksum, "qwen-code-linux-riscv64.tar.gz"))
	if err == nil {
		t.Fatalf("install succeeded on riscv64, want a build failure:\n%s", output)
	}
	if !strings.Contains(output, "unsupported architecture riscv64") || strings.Contains(output, "downloaded") {
		t.Errorf("output = %q, want an unsupported-architecture error and no download", output)
	}
}

func TestQwenCodeInstallRejectsBadChecksums(t *testing.T) {
	for name, sums := range map[string]string{
		"mismatch":       qwenSums(strings.Repeat("0", 64), "qwen-code-linux-x64.tar.gz"),
		"asset missing":  qwenSums(qwenAssetChecksum, "qwen-code-linux-arm64.tar.gz"),
		"malformed hash": qwenSums("deadbeef", "qwen-code-linux-x64.tar.gz"),
		"empty file":     "",
	} {
		t.Run(name, func(t *testing.T) {
			output, err := fakeQwenCodeRelease(t, "x86_64", sums)
			if err == nil {
				t.Fatalf("install succeeded, want a build failure:\n%s", output)
			}
			if !strings.Contains(output, "qwen-code:") || strings.Contains(output, "verified") {
				t.Errorf("output = %q, want a qwen-code checksum error", output)
			}
		})
	}
}

func TestQwenCodeAttachCommandQuotesArgs(t *testing.T) {
	a, _ := agent.Lookup("qwen-code")
	runner, ok := agent.AsAttachRunner(a)
	if !ok {
		t.Fatal("qwen-code should implement AttachRunner")
	}
	if got, want := runner.AttachCommand("/workspace", []string{"--model", "x"}), "qwen '--model' 'x'"; got != want {
		t.Errorf("AttachCommand = %q, want %q", got, want)
	}
}

func TestQwenCodeAttachCommandPreservesShellSpecialPrompts(t *testing.T) {
	a, _ := agent.Lookup("qwen-code")
	runner, _ := agent.AsAttachRunner(a)
	prompts := []string{"Explain this project's tests", "cost in $HOME; `id` && echo hi", ""}
	command := runner.AttachCommand("/workspace", prompts)
	// printf stands in for qwen so the shell's word splitting is observable.
	script := "qwen() { printf '[%s]' \"$@\"; }; " + command
	output, err := exec.Command("bash", "-c", script).Output()
	if err != nil {
		t.Fatalf("bash -c %q: %v", script, err)
	}
	if want := "[Explain this project's tests][cost in $HOME; `id` && echo hi][]"; string(output) != want {
		t.Errorf("forwarded args = %s, want %s", output, want)
	}
}

func TestQwenCodeConfigMerger(t *testing.T) {
	a, _ := agent.Lookup("qwen-code")
	merger, ok := agent.AsConfigMerger(a)
	if !ok {
		t.Fatal("qwen-code should implement ConfigMerger")
	}
	if merger.SnippetPattern() != "settings*.json*" {
		t.Errorf("SnippetPattern = %q, want settings*.json*", merger.SnippetPattern())
	}
	if got := merger.VMConfigPath("/home/dev"); got != filepath.Join("/home/dev", ".qwen", "settings.json") {
		t.Errorf("VMConfigPath = %q", got)
	}
	if names := merger.ConfigFileNames(); !slices.Equal(names, []string{"settings.json"}) {
		t.Errorf("ConfigFileNames = %v, want [settings.json]", names)
	}
}

// TestQwenCodeProvisionRulesCopyOnlyCredentialFreeFiles pins the drop-in copy
// to ~/.qwen's user-authored, credential-free files: settings.json (/auth writes
// literal keys into its env), the OAuth and MCP token stores, extensions (each
// keeps its settings in a .env), and runtime state stay on the host.
func TestQwenCodeProvisionRulesCopyOnlyCredentialFreeFiles(t *testing.T) {
	a, _ := agent.Lookup("qwen-code")
	provisioner, ok := agent.AsProvisioner(a)
	if !ok {
		t.Fatal("qwen-code should implement Provisioner")
	}
	copied := []string{
		"QWEN.md",
		"AGENTS.md",
		"commands/review.toml",
		"skills/fix/SKILL.md",
		"agents/reviewer.md",
		"workflows/release.js",
	}
	notCopied := []string{
		"settings.json",
		".env",
		"oauth_creds.json",
		"mcp-oauth-tokens.json",
		"extensions/notify/.env",
		"extensions/notify/qwen-extension.json",
		"installation_id",
		"trustedFolders.json",
		"tmp/session/chat.json",
		"skills.json",
	}
	hostHome := t.TempDir()
	for _, rel := range append(slices.Clone(copied), notCopied...) {
		path := filepath.Join(hostHome, ".qwen", rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	vmHome := "/home/dev"
	var got []string
	_, err := agent.EvalProvisionRules(provisioner.ProvisionRules(), hostHome, vmHome,
		func(dst string, _ []byte, _ os.FileMode) error {
			rel, _ := filepath.Rel(filepath.Join(vmHome, ".qwen"), dst)
			got = append(got, filepath.ToSlash(rel))
			return nil
		})
	if err != nil {
		t.Fatalf("EvalProvisionRules: %v", err)
	}
	slices.Sort(got)
	want := slices.Sorted(slices.Values(copied))
	if !slices.Equal(got, want) {
		t.Errorf("copied %v, want %v", got, want)
	}
}

func TestQwenCodeVersion(t *testing.T) {
	a, _ := agent.Lookup("qwen-code")
	provider, ok := agent.AsVersionProvider(a)
	if !ok {
		t.Fatal("qwen-code should implement VersionProvider")
	}
	if provider.VersionCmd() != "qwen --version" {
		t.Errorf("VersionCmd = %q, want qwen --version", provider.VersionCmd())
	}
	got, err := provider.ParseVersion("0.28.0\n")
	if err != nil || got != "0.28.0" {
		t.Errorf("ParseVersion = %q, %v, want 0.28.0", got, err)
	}
}

func TestQwenCodeUpgradeChecker(t *testing.T) {
	a, _ := agent.Lookup("qwen-code")
	checker, ok := agent.AsUpgradeChecker(a)
	if !ok {
		t.Fatal("qwen-code should implement UpgradeChecker")
	}
	newer, err := checker.NewerThan("0.28.1", "0.28.0")
	if err != nil || !newer {
		t.Errorf("NewerThan(0.28.1, 0.28.0) = %v, %v, want true", newer, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := checker.LatestVersion(ctx); err == nil {
		t.Error("LatestVersion with cancelled ctx should error")
	}
}

func TestQwenCodeHasNoDaemonOrWorktree(t *testing.T) {
	a, _ := agent.Lookup("qwen-code")
	if _, ok := agent.AsDaemonProvider(a); ok {
		t.Error("qwen-code must not implement DaemonProvider")
	}
	if _, ok := agent.AsWorktreeProvider(a); ok {
		t.Error("qwen-code must not implement WorktreeProvider")
	}
	if _, ok := agent.AsEventStreamProvider(a); ok {
		t.Error("qwen-code must not implement EventStreamProvider")
	}
	if _, ok := agent.AsGuestToolRequirer(a); ok {
		t.Error("qwen-code must not implement GuestToolRequirer (no daemon tools)")
	}
}
