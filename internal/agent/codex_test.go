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

func TestCodexLookup(t *testing.T) {
	a, ok := agent.Lookup("codex")
	if !ok {
		t.Fatal("Lookup(\"codex\") returned not-ok")
	}
	if a.Name() != "codex" {
		t.Errorf("Name() = %q, want codex", a.Name())
	}
	if a.ConfigDirName() != "codex" {
		t.Errorf("ConfigDirName = %q, want codex", a.ConfigDirName())
	}
	if !slices.Contains(agent.Names(), "codex") {
		t.Errorf("Names() = %v, want to include codex", agent.Names())
	}
}

func TestCodexImageSpec(t *testing.T) {
	a, _ := agent.Lookup("codex")
	spec := a.ImageSpec()
	if spec.VersionArg != "CODEX_VERSION" {
		t.Errorf("VersionArg = %q, want CODEX_VERSION", spec.VersionArg)
	}
	for _, want := range []string{
		`https://github.com/openai/codex/releases/download/rust-v${CODEX_VERSION}/codex-package-$(uname -m)-unknown-linux-musl.tar.gz`,
		"tar -xz -C /opt/agents-sandbox/codex",
		"ln -s ../codex/bin/codex /opt/agents-sandbox/bin/codex",
	} {
		if !strings.Contains(spec.InstallCommand, want) {
			t.Errorf("InstallCommand = %q, want it to contain %q", spec.InstallCommand, want)
		}
	}
	if spec.RequiresNode {
		t.Error("RequiresNode = true, want false (codex ships a static binary)")
	}
	if len(spec.AgentEnv) != 0 {
		t.Errorf("AgentEnv = %v, want none", spec.AgentEnv)
	}
}

func TestCodexAttachCommandDisablesUpdateCheck(t *testing.T) {
	a, _ := agent.Lookup("codex")
	runner, ok := agent.AsAttachRunner(a)
	if !ok {
		t.Fatal("codex should implement AttachRunner")
	}
	got := runner.AttachCommand("/workspace", []string{"--model", "gpt-5"})
	want := "codex -c check_for_update_on_startup=false -c 'sandbox_mode=\"danger-full-access\"' '--model' 'gpt-5'"
	if got != want {
		t.Errorf("AttachCommand = %q, want %q", got, want)
	}
}

func TestCodexProvisionRulesExcludeAuth(t *testing.T) {
	a, _ := agent.Lookup("codex")
	provisioner, ok := agent.AsProvisioner(a)
	if !ok {
		t.Fatal("codex should implement Provisioner")
	}
	rules := provisioner.ProvisionRules()
	if len(rules) != 1 || rules[0].Dir != ".codex" || !slices.Equal(rules[0].Patterns, []string{"config.toml"}) {
		t.Errorf("ProvisionRules = %+v, want [.codex: config.toml]", rules)
	}
}

func TestCodexVersion(t *testing.T) {
	a, _ := agent.Lookup("codex")
	provider, ok := agent.AsVersionProvider(a)
	if !ok {
		t.Fatal("codex should implement VersionProvider")
	}
	if provider.VersionCmd() != "codex --version" {
		t.Errorf("VersionCmd = %q", provider.VersionCmd())
	}
	got, err := provider.ParseVersion("codex-cli 0.164.0\n")
	if err != nil || got != "0.164.0" {
		t.Errorf("ParseVersion = %q, %v, want 0.164.0", got, err)
	}
}

func TestCodexUpgradeChecker(t *testing.T) {
	a, _ := agent.Lookup("codex")
	checker, ok := agent.AsUpgradeChecker(a)
	if !ok {
		t.Fatal("codex should implement UpgradeChecker")
	}
	newer, err := checker.NewerThan("0.165.0", "0.164.0")
	if err != nil || !newer {
		t.Errorf("NewerThan(0.165.0, 0.164.0) = %v, %v, want true", newer, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := checker.LatestVersion(ctx); err == nil {
		t.Error("LatestVersion with cancelled ctx should error")
	}
}

func TestCodexHasNoDaemonOrWorktree(t *testing.T) {
	a, _ := agent.Lookup("codex")
	if _, ok := agent.AsDaemonProvider(a); ok {
		t.Error("codex must not implement DaemonProvider")
	}
	if _, ok := agent.AsWorktreeProvider(a); ok {
		t.Error("codex must not implement WorktreeProvider")
	}
}

func TestCodexConfigMerger(t *testing.T) {
	a, _ := agent.Lookup("codex")
	merger, ok := agent.AsConfigMerger(a)
	if !ok {
		t.Fatal("codex should implement ConfigMerger")
	}
	if merger.SnippetPattern() != "config*.toml" {
		t.Errorf("SnippetPattern = %q, want config*.toml", merger.SnippetPattern())
	}
	if got := merger.VMConfigPath("/home/dev"); got != filepath.Join("/home/dev", ".codex", "config.toml") {
		t.Errorf("VMConfigPath = %q", got)
	}
	if names := merger.ConfigFileNames(); !slices.Equal(names, []string{"config.toml"}) {
		t.Errorf("ConfigFileNames = %v, want [config.toml]", names)
	}
}

func TestCodexAttachCommandPreservesShellSpecialPrompts(t *testing.T) {
	a, _ := agent.Lookup("codex")
	runner, _ := agent.AsAttachRunner(a)
	prompts := []string{"Explain this project's tests", "cost in $HOME; `id` && echo hi", ""}
	command := runner.AttachCommand("/workspace", prompts)
	// printf stands in for codex so the shell's word splitting is observable.
	script := "codex() { shift 4; printf '[%s]' \"$@\"; }; " + command
	output, err := exec.Command("bash", "-c", script).Output()
	if err != nil {
		t.Fatalf("bash -c %q: %v", script, err)
	}
	want := "[Explain this project's tests][cost in $HOME; `id` && echo hi][]"
	if string(output) != want {
		t.Errorf("forwarded args = %s, want %s", output, want)
	}
}
