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

func TestMistralVibeLookup(t *testing.T) {
	a, ok := agent.Lookup("mistral-vibe")
	if !ok {
		t.Fatal("Lookup(\"mistral-vibe\") returned not-ok")
	}
	if a.Name() != "mistral-vibe" {
		t.Errorf("Name() = %q, want mistral-vibe", a.Name())
	}
	if a.ConfigDirName() != "mistral-vibe" {
		t.Errorf("ConfigDirName = %q, want mistral-vibe", a.ConfigDirName())
	}
	if !a.Experimental() {
		t.Error("Experimental() = false, want true")
	}
	if !slices.Contains(agent.Names(), "mistral-vibe") {
		t.Errorf("Names() = %v, want to include mistral-vibe", agent.Names())
	}
}

func TestMistralVibeImageSpec(t *testing.T) {
	a, _ := agent.Lookup("mistral-vibe")
	spec := a.ImageSpec()
	if spec.VersionArg != "MISTRAL_VIBE_VERSION" {
		t.Errorf("VersionArg = %q, want MISTRAL_VIBE_VERSION", spec.VersionArg)
	}
	for _, want := range []string{
		`https://github.com/astral-sh/uv/releases/download/0.11.26/uv-$uv_arch-unknown-linux-gnu.tar.gz`,
		`sha256sum --check --strict`,
		`python install cpython-3.14`,
		`vibe_tag="https://raw.githubusercontent.com/mistralai/mistral-vibe/v${MISTRAL_VIBE_VERSION}"`,
		`"$vibe_tag/uv.lock"`,
		`"$vibe_tag/pyproject.toml"`,
		`export --frozen --no-dev --no-emit-project`,
		`tool install --python 3.14 --constraints "$vibe_dir/constraints.txt" "mistral-vibe==${MISTRAL_VIBE_VERSION}"`,
		`UV_SYSTEM_CERTS=true`,
		`UV_TOOL_DIR=/opt/agents-sandbox/mistral-vibe/tools`,
		`UV_TOOL_BIN_DIR=/opt/agents-sandbox/bin`,
		`UV_PYTHON_INSTALL_DIR=/opt/agents-sandbox/mistral-vibe/python`,
		`UV_PYTHON_PREFERENCE=only-managed`,
		`UV_COMPILE_BYTECODE=1`,
		`/opt/agents-sandbox/bin/vibe --version`,
	} {
		if !strings.Contains(spec.InstallCommand, want) {
			t.Errorf("InstallCommand = %q, want it to contain %q", spec.InstallCommand, want)
		}
	}
	if strings.Index(spec.InstallCommand, "sha256sum") > strings.Index(spec.InstallCommand, "tar -xzf") {
		t.Errorf("InstallCommand = %q must verify uv's checksum before extracting it", spec.InstallCommand)
	}
	if strings.Index(spec.InstallCommand, "uv.lock") > strings.Index(spec.InstallCommand, "tool install") {
		t.Errorf("InstallCommand = %q must derive the constraints before installing", spec.InstallCommand)
	}
	// Only the tool tree is copied from the agent stage into the runner image, so
	// uv's tools, Python, and launchers must all land under /opt/agents-sandbox.
	if strings.Contains(spec.InstallCommand, "/etc/") || strings.Contains(spec.InstallCommand, "/root/") {
		t.Errorf("InstallCommand = %q writes outside the tool tree, which the runner image drops", spec.InstallCommand)
	}
	wantEnv := map[string]string{"VIBE_ENABLE_UPDATE_CHECKS": "false", "VIBE_ENABLE_SYSTEM_TRUST_STORE": "true"}
	if len(spec.AgentEnv) != len(wantEnv) {
		t.Errorf("AgentEnv = %v, want %v", spec.AgentEnv, wantEnv)
	}
	for key, value := range wantEnv {
		if spec.AgentEnv[key] != value {
			t.Errorf("AgentEnv[%s] = %q, want %q", key, spec.AgentEnv[key], value)
		}
	}
	if spec.RequiresNode || spec.OwnsToolTree {
		t.Errorf("RequiresNode = %v, OwnsToolTree = %v, want both false", spec.RequiresNode, spec.OwnsToolTree)
	}
	if len(spec.RuntimeLibs) != 0 || spec.RunnerCommand != "" {
		t.Errorf("RuntimeLibs = %v, RunnerCommand = %q, want none", spec.RuntimeLibs, spec.RunnerCommand)
	}
}

// fakeMistralVibeUVDownload runs the install command's resolve-download-verify
// part with curl and uname replaced by shell functions, so the architecture
// mapping and uv's checksum verification are observable without a network or
// root. machine is what `uname -m` reports and body is the downloaded uv
// archive. The command is cut before uv is extracted, and "verified" marks
// that the verification passed.
func fakeMistralVibeUVDownload(t *testing.T, machine, body string) (string, error) {
	t.Helper()
	a, _ := agent.Lookup("mistral-vibe")
	install := a.ImageSpec().InstallCommand
	install = strings.ReplaceAll(install, "/tmp/mistral-vibe", t.TempDir())
	install, _, found := strings.Cut(install, " && tar -xzf")
	if !found {
		t.Fatalf("InstallCommand = %q has no uv extraction step", a.ImageSpec().InstallCommand)
	}
	stubs := `uname() { echo '` + machine + `'; }; ` +
		`curl() { while [ "$1" != -o ]; do url="$1"; shift; done; ` +
		`echo "downloaded $(basename "$url")"; printf '%s' "$BODY" > "$2"; }; `
	cmd := exec.Command("sh", "-c", stubs+install+" && echo verified")
	cmd.Env = append(cmd.Environ(), "BODY="+body)
	output, err := cmd.CombinedOutput()
	return string(output), err
}

func TestMistralVibeInstallMapsArchitecture(t *testing.T) {
	for machine, asset := range map[string]string{
		"x86_64":  "uv-x86_64-unknown-linux-gnu.tar.gz",
		"amd64":   "uv-x86_64-unknown-linux-gnu.tar.gz",
		"aarch64": "uv-aarch64-unknown-linux-gnu.tar.gz",
		"arm64":   "uv-aarch64-unknown-linux-gnu.tar.gz",
	} {
		t.Run(machine, func(t *testing.T) {
			output, _ := fakeMistralVibeUVDownload(t, machine, "not uv")
			if !strings.Contains(output, "downloaded "+asset) {
				t.Errorf("output = %q, want it to download %s", output, asset)
			}
		})
	}
}

func TestMistralVibeInstallRejectsATamperedUV(t *testing.T) {
	output, err := fakeMistralVibeUVDownload(t, "x86_64", "not uv")
	if err == nil {
		t.Fatalf("install succeeded with a uv archive that does not match its pinned digest:\n%s", output)
	}
	if strings.Contains(output, "verified") {
		t.Errorf("output = %q, want the checksum check to stop the build", output)
	}
}

func TestMistralVibeInstallRejectsUnsupportedArchitecture(t *testing.T) {
	output, err := fakeMistralVibeUVDownload(t, "riscv64", "not uv")
	if err == nil {
		t.Fatalf("install succeeded on riscv64, want a build failure:\n%s", output)
	}
	if !strings.Contains(output, "unsupported architecture riscv64") || strings.Contains(output, "downloaded") {
		t.Errorf("output = %q, want an unsupported-architecture error and no download", output)
	}
}

func TestMistralVibeAttachCommandQuotesArgs(t *testing.T) {
	a, _ := agent.Lookup("mistral-vibe")
	runner, ok := agent.AsAttachRunner(a)
	if !ok {
		t.Fatal("mistral-vibe should implement AttachRunner")
	}
	if got, want := runner.AttachCommand(
		"/workspace",
		[]string{"--agent", "plan"},
	), "vibe '--agent' 'plan'"; got != want {
		t.Errorf("AttachCommand = %q, want %q", got, want)
	}
}

func TestMistralVibeAttachCommandPreservesShellSpecialPrompts(t *testing.T) {
	a, _ := agent.Lookup("mistral-vibe")
	runner, _ := agent.AsAttachRunner(a)
	prompts := []string{"Explain this project's tests", "cost in $HOME; `id` && echo hi", ""}
	command := runner.AttachCommand("/workspace", prompts)
	// printf stands in for vibe so the shell's word splitting is observable.
	script := "vibe() { printf '[%s]' \"$@\"; }; " + command
	output, err := exec.Command("bash", "-c", script).Output()
	if err != nil {
		t.Fatalf("bash -c %q: %v", script, err)
	}
	if want := "[Explain this project's tests][cost in $HOME; `id` && echo hi][]"; string(output) != want {
		t.Errorf("forwarded args = %s, want %s", output, want)
	}
}

func TestMistralVibeConfigMerger(t *testing.T) {
	a, _ := agent.Lookup("mistral-vibe")
	merger, ok := agent.AsConfigMerger(a)
	if !ok {
		t.Fatal("mistral-vibe should implement ConfigMerger")
	}
	if merger.SnippetPattern() != "config*.toml" {
		t.Errorf("SnippetPattern = %q, want config*.toml", merger.SnippetPattern())
	}
	if got := merger.VMConfigPath("/home/dev"); got != filepath.Join("/home/dev", ".vibe", "config.toml") {
		t.Errorf("VMConfigPath = %q", got)
	}
	if names := merger.ConfigFileNames(); !slices.Equal(names, []string{"config.toml"}) {
		t.Errorf("ConfigFileNames = %v, want [config.toml]", names)
	}
}

// TestMistralVibeProvisionRulesCopyOnlyCredentialFreeFiles pins the drop-in
// copy to ~/.vibe's user-authored, credential-free files: config.toml (MCP
// headers, provider headers, and stdio env can hold tokens), the .env key
// store, hooks.toml (host commands), the trust store, and runtime state stay on
// the host.
func TestMistralVibeProvisionRulesCopyOnlyCredentialFreeFiles(t *testing.T) {
	a, _ := agent.Lookup("mistral-vibe")
	provisioner, ok := agent.AsProvisioner(a)
	if !ok {
		t.Fatal("mistral-vibe should implement Provisioner")
	}
	copied := []string{
		"AGENTS.md",
		"agents/reviewer.toml",
		"prompts/review.md",
		"skills/fix/SKILL.md",
	}
	notCopied := []string{
		"config.toml",
		".env",
		"hooks.toml",
		"trusted_folders.toml",
		"vibehistory",
		"logs/vibe.log",
		"plugins/local/plugin.toml",
		"tools/custom.py",
		"sessions/abc/AGENTS.md",
	}
	hostHome := t.TempDir()
	for _, rel := range append(slices.Clone(copied), notCopied...) {
		path := filepath.Join(hostHome, ".vibe", rel)
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
			rel, _ := filepath.Rel(filepath.Join(vmHome, ".vibe"), dst)
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

func TestMistralVibeVersion(t *testing.T) {
	a, _ := agent.Lookup("mistral-vibe")
	provider, ok := agent.AsVersionProvider(a)
	if !ok {
		t.Fatal("mistral-vibe should implement VersionProvider")
	}
	if provider.VersionCmd() != "vibe --version" {
		t.Errorf("VersionCmd = %q, want vibe --version", provider.VersionCmd())
	}
	got, err := provider.ParseVersion("vibe 3.1.3\n")
	if err != nil || got != "3.1.3" {
		t.Errorf("ParseVersion = %q, %v, want 3.1.3", got, err)
	}
}

func TestMistralVibeUpgradeChecker(t *testing.T) {
	a, _ := agent.Lookup("mistral-vibe")
	checker, ok := agent.AsUpgradeChecker(a)
	if !ok {
		t.Fatal("mistral-vibe should implement UpgradeChecker")
	}
	newer, err := checker.NewerThan("3.1.4", "3.1.3")
	if err != nil || !newer {
		t.Errorf("NewerThan(3.1.4, 3.1.3) = %v, %v, want true", newer, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := checker.LatestVersion(ctx); err == nil {
		t.Error("LatestVersion with cancelled ctx should error")
	}
}

func TestMistralVibeHasNoDaemonOrWorktree(t *testing.T) {
	a, _ := agent.Lookup("mistral-vibe")
	if _, ok := agent.AsDaemonProvider(a); ok {
		t.Error("mistral-vibe must not implement DaemonProvider")
	}
	if _, ok := agent.AsWorktreeProvider(a); ok {
		t.Error("mistral-vibe must not implement WorktreeProvider")
	}
	if _, ok := agent.AsEventStreamProvider(a); ok {
		t.Error("mistral-vibe must not implement EventStreamProvider")
	}
	if _, ok := agent.AsGuestToolRequirer(a); ok {
		t.Error("mistral-vibe must not implement GuestToolRequirer (no daemon tools)")
	}
}
