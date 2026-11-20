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

func TestGooseLookup(t *testing.T) {
	a, ok := agent.Lookup("goose")
	if !ok {
		t.Fatal("Lookup(\"goose\") returned not-ok")
	}
	if a.Name() != "goose" {
		t.Errorf("Name() = %q, want goose", a.Name())
	}
	if a.ConfigDirName() != "goose" {
		t.Errorf("ConfigDirName = %q, want goose", a.ConfigDirName())
	}
	if !a.Experimental() {
		t.Error("Experimental() = false, want true")
	}
	if !slices.Contains(agent.Names(), "goose") {
		t.Errorf("Names() = %v, want to include goose", agent.Names())
	}
}

func TestGooseImageSpec(t *testing.T) {
	a, _ := agent.Lookup("goose")
	spec := a.ImageSpec()
	if spec.VersionArg != "GOOSE_VERSION" {
		t.Errorf("VersionArg = %q, want GOOSE_VERSION", spec.VersionArg)
	}
	for _, want := range []string{
		`https://github.com/aaif-goose/goose/releases/download/v${GOOSE_VERSION}/goose-$(uname -m)-unknown-linux-musl.tar.gz`,
		`tar -xz -C /opt/agents-sandbox/bin ./goose`,
		`/opt/agents-sandbox/bin/goose --version`,
	} {
		if !strings.Contains(spec.InstallCommand, want) {
			t.Errorf("InstallCommand = %q, want it to contain %q", spec.InstallCommand, want)
		}
	}
	if len(spec.AgentEnv) != 0 {
		t.Errorf("AgentEnv = %v, want none (goose runs no startup update check)", spec.AgentEnv)
	}
	if spec.RequiresNode || spec.OwnsToolTree {
		t.Errorf("RequiresNode = %v, OwnsToolTree = %v, want both false", spec.RequiresNode, spec.OwnsToolTree)
	}
	if len(spec.RuntimeLibs) != 0 {
		t.Errorf("RuntimeLibs = %v, want none (the musl build is static)", spec.RuntimeLibs)
	}
}

func TestGooseAttachCommandQuotesArgs(t *testing.T) {
	a, _ := agent.Lookup("goose")
	runner, ok := agent.AsAttachRunner(a)
	if !ok {
		t.Fatal("goose should implement AttachRunner")
	}
	if got, want := runner.AttachCommand(
		"/workspace",
		[]string{"session", "--name", "x"},
	), "goose 'session' '--name' 'x'"; got != want {
		t.Errorf("AttachCommand = %q, want %q", got, want)
	}
	if got := runner.AttachCommand("/workspace", nil); got != "goose" {
		t.Errorf("AttachCommand without args = %q, want goose", got)
	}
}

func TestGooseAttachCommandPreservesShellSpecialPrompts(t *testing.T) {
	a, _ := agent.Lookup("goose")
	runner, _ := agent.AsAttachRunner(a)
	prompts := []string{"Explain this project's tests", "cost in $HOME; `id` && echo hi", ""}
	command := runner.AttachCommand("/workspace", prompts)
	// printf stands in for goose so the shell's word splitting is observable.
	script := "goose() { printf '[%s]' \"$@\"; }; " + command
	output, err := exec.Command("bash", "-c", script).Output()
	if err != nil {
		t.Fatalf("bash -c %q: %v", script, err)
	}
	if want := "[Explain this project's tests][cost in $HOME; `id` && echo hi][]"; string(output) != want {
		t.Errorf("forwarded args = %s, want %s", output, want)
	}
}

func TestGooseConfigMerger(t *testing.T) {
	a, _ := agent.Lookup("goose")
	merger, ok := agent.AsConfigMerger(a)
	if !ok {
		t.Fatal("goose should implement ConfigMerger")
	}
	if merger.SnippetPattern() != "config*.{yml,yaml}" {
		t.Errorf("SnippetPattern = %q, want config*.{yml,yaml}", merger.SnippetPattern())
	}
	if got := merger.VMConfigPath("/home/dev"); got != filepath.Join("/home/dev", ".config", "goose", "config.yaml") {
		t.Errorf("VMConfigPath = %q", got)
	}
	if names := merger.ConfigFileNames(); !slices.Equal(names, []string{"config.yaml"}) {
		t.Errorf("ConfigFileNames = %v, want [config.yaml]", names)
	}
}

// TestGooseProvisionRulesCopyOnlyCredentialFreeFiles pins the drop-in copy to
// ~/.config/goose's credential-free, user-authored files: config.yaml (an
// extension's envs and headers can hold tokens), secrets.yaml (plain-text
// secrets without a keyring), custom provider definitions (an api_key and
// headers can sit inline), the command history, and runtime permission
// decisions stay on the host.
func TestGooseProvisionRulesCopyOnlyCredentialFreeFiles(t *testing.T) {
	a, _ := agent.Lookup("goose")
	provisioner, ok := agent.AsProvisioner(a)
	if !ok {
		t.Fatal("goose should implement Provisioner")
	}
	copied := []string{
		"permission.yaml",
		".goosehints",
		"AGENTS.md",
		"prompts/system.md",
		"recipes/review.yaml",
	}
	notCopied := []string{
		"config.yaml",
		"custom_providers/corp.json",
		"secrets.yaml",
		"history.txt",
		"permissions/tool_permissions.json",
		"settings.json",
	}
	hostHome := t.TempDir()
	for _, rel := range append(slices.Clone(copied), notCopied...) {
		path := filepath.Join(hostHome, ".config", "goose", rel)
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
			rel, _ := filepath.Rel(filepath.Join(vmHome, ".config", "goose"), dst)
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

func TestGooseVersion(t *testing.T) {
	a, _ := agent.Lookup("goose")
	provider, ok := agent.AsVersionProvider(a)
	if !ok {
		t.Fatal("goose should implement VersionProvider")
	}
	if provider.VersionCmd() != "goose --version" {
		t.Errorf("VersionCmd = %q, want goose --version", provider.VersionCmd())
	}
	got, err := provider.ParseVersion(" 1.60.1\n")
	if err != nil || got != "1.60.1" {
		t.Errorf("ParseVersion = %q, %v, want 1.60.1", got, err)
	}
}

func TestGooseUpgradeChecker(t *testing.T) {
	a, _ := agent.Lookup("goose")
	checker, ok := agent.AsUpgradeChecker(a)
	if !ok {
		t.Fatal("goose should implement UpgradeChecker")
	}
	newer, err := checker.NewerThan("1.60.1", "1.60.0")
	if err != nil || !newer {
		t.Errorf("NewerThan(1.60.1, 1.60.0) = %v, %v, want true", newer, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := checker.LatestVersion(ctx); err == nil {
		t.Error("LatestVersion with cancelled ctx should error")
	}
}

func TestGooseHasNoDaemonOrWorktree(t *testing.T) {
	a, _ := agent.Lookup("goose")
	if _, ok := agent.AsDaemonProvider(a); ok {
		t.Error("goose must not implement DaemonProvider")
	}
	if _, ok := agent.AsWorktreeProvider(a); ok {
		t.Error("goose must not implement WorktreeProvider")
	}
	if _, ok := agent.AsEventStreamProvider(a); ok {
		t.Error("goose must not implement EventStreamProvider")
	}
	if _, ok := agent.AsGuestToolRequirer(a); ok {
		t.Error("goose must not implement GuestToolRequirer (no daemon tools)")
	}
}
