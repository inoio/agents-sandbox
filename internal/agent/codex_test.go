package agent_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"

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

func TestCodexAttachCommandRunsWithoutTheBackgroundServer(t *testing.T) {
	a, _ := agent.Lookup("codex")
	runner, ok := agent.AsAttachRunner(a)
	if !ok {
		t.Fatal("codex should implement AttachRunner")
	}
	got := runner.AttachCommand("/workspace", []string{"--model", "gpt-5"})
	want := "codex --no-daemon '--model' 'gpt-5'"
	if got != want {
		t.Errorf("AttachCommand = %q, want %q", got, want)
	}
}

// TestCodexRunnerCommandBakesTheSystemConfig runs the runner-stage command with
// /etc redirected into a temp dir and decodes the file it writes, so a quoting
// mistake in the printf surfaces as a TOML error rather than in an image build.
func TestCodexRunnerCommandBakesTheSystemConfig(t *testing.T) {
	a, _ := agent.Lookup("codex")
	command := a.ImageSpec().RunnerCommand
	if !strings.Contains(command, "/etc/codex/config.toml") {
		t.Fatalf("RunnerCommand = %q, want it to write /etc/codex/config.toml", command)
	}
	root := t.TempDir()
	redirected := strings.ReplaceAll(command, "/etc/codex", filepath.Join(root, "etc", "codex"))
	if output, err := exec.Command("sh", "-c", redirected).CombinedOutput(); err != nil {
		t.Fatalf("run RunnerCommand: %v\n%s", err, output)
	}
	content, err := os.ReadFile(filepath.Join(root, "etc", "codex", "config.toml"))
	if err != nil {
		t.Fatalf("read baked config: %v", err)
	}
	var baked struct {
		CheckForUpdateOnStartup *bool  `toml:"check_for_update_on_startup"`
		SandboxMode             string `toml:"sandbox_mode"`
		Projects                map[string]struct {
			TrustLevel string `toml:"trust_level"`
		} `toml:"projects"`
	}
	if err := toml.Unmarshal(content, &baked); err != nil {
		t.Fatalf("baked config is not valid TOML: %v\n%s", err, content)
	}
	if baked.CheckForUpdateOnStartup == nil || *baked.CheckForUpdateOnStartup {
		t.Errorf("check_for_update_on_startup = %v, want false", baked.CheckForUpdateOnStartup)
	}
	if baked.SandboxMode != "danger-full-access" {
		t.Errorf("sandbox_mode = %q, want danger-full-access", baked.SandboxMode)
	}
	if got := baked.Projects["/workspace"].TrustLevel; got != "trusted" {
		t.Errorf(`projects."/workspace".trust_level = %q, want trusted`, got)
	}
}

func TestCodexProvisionRulesCopyConfigAndInstructions(t *testing.T) {
	assertProvisionedSet(t, "codex", ".codex",
		[]string{"config.toml", "AGENTS.md", "AGENTS.override.md", "skills/tdd/SKILL.md"},
		[]string{"auth.json", "sessions/2026/x.jsonl", "log/codex-tui.log", "history.jsonl"},
	)
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
	// printf stands in for codex (after its --no-daemon flag) so the shell's word
	// splitting is observable.
	script := "codex() { shift 1; printf '[%s]' \"$@\"; }; " + command
	output, err := exec.Command("bash", "-c", script).Output()
	if err != nil {
		t.Fatalf("bash -c %q: %v", script, err)
	}
	want := "[Explain this project's tests][cost in $HOME; `id` && echo hi][]"
	if string(output) != want {
		t.Errorf("forwarded args = %s, want %s", output, want)
	}
}
