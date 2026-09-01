package agent_test

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/inoio/opencode-sandbox/internal/agent"
)

func TestClaudeCodeLookup(t *testing.T) {
	a, ok := agent.Lookup("claude-code")
	if !ok {
		t.Fatal("Lookup(\"claude-code\") returned not-ok")
	}
	if a.Name() != "claude-code" {
		t.Errorf("Lookup(\"claude-code\").Name() = %q, want claude-code", a.Name())
	}
	if a.ConfigDirName() != "claude" {
		t.Errorf("ConfigDirName = %q, want claude", a.ConfigDirName())
	}
}

func TestClaudeCodeNamesIncludesClaudeCode(t *testing.T) {
	if !slices.Contains(agent.Names(), "claude-code") {
		t.Errorf("Names() = %v, want to include claude-code", agent.Names())
	}
}

func TestClaudeCodeImageSpec(t *testing.T) {
	a, _ := agent.Lookup("claude-code")
	spec := a.ImageSpec()
	if spec.VersionArg != "CLAUDE_CODE_VERSION" {
		t.Errorf("VersionArg = %q, want CLAUDE_CODE_VERSION", spec.VersionArg)
	}
	if spec.VersionLabel != "org.opencode-sandbox.claude-code-version" {
		t.Errorf("VersionLabel = %q, want org.opencode-sandbox.claude-code-version", spec.VersionLabel)
	}
	if _, ok := spec.AgentEnv["DISABLE_AUTOUPDATER"]; !ok {
		t.Errorf("AgentEnv = %v, want DISABLE_AUTOUPDATER key", spec.AgentEnv)
	}
	if !strings.Contains(spec.InstallCommand, "@anthropic-ai/claude-code") {
		t.Errorf("InstallCommand = %q, want claude-code npm install", spec.InstallCommand)
	}
}

func TestClaudeCodeAttachCommand(t *testing.T) {
	a, _ := agent.Lookup("claude-code")
	runner, ok := agent.AsAttachRunner(a)
	if !ok {
		t.Fatal("claude-code should implement AttachRunner")
	}
	got := runner.AttachCommand("/workspace", []string{"--dangerously-skip-permissions"})
	want := "claude --dangerously-skip-permissions"
	if got != want {
		t.Errorf("AttachCommand = %q, want %q", got, want)
	}
}

func TestClaudeCodeConfigMerger(t *testing.T) {
	a, _ := agent.Lookup("claude-code")
	merger, ok := agent.AsConfigMerger(a)
	if !ok {
		t.Fatal("claude-code should implement ConfigMerger")
	}
	if merger.SnippetPattern() != "settings*.json*" {
		t.Errorf("SnippetPattern = %q, want settings*.json*", merger.SnippetPattern())
	}
	if got := merger.VMConfigPath("/home/user"); got != filepath.Join("/home/user", ".claude", "settings.json") {
		t.Errorf("VMConfigPath = %q", got)
	}
	if names := merger.ConfigFileNames(); len(names) != 1 || names[0] != "settings.json" {
		t.Errorf("ConfigFileNames = %v, want [settings.json]", names)
	}
}

func TestClaudeCodeProvisionRules(t *testing.T) {
	a, _ := agent.Lookup("claude-code")
	provisioner, ok := agent.AsProvisioner(a)
	if !ok {
		t.Fatal("claude-code should implement Provisioner")
	}
	found := false
	for _, r := range provisioner.ProvisionRules() {
		if r.Dir == ".claude" {
			found = true
		}
	}
	if !found {
		t.Errorf("ProvisionRules missing .claude: %+v", provisioner.ProvisionRules())
	}
}

func TestClaudeCodeHasUpgradeCheckerNoDaemonNoWorktree(t *testing.T) {
	a, _ := agent.Lookup("claude-code")
	if _, ok := agent.AsUpgradeChecker(a); !ok {
		t.Error("claude-code should implement UpgradeChecker")
	}
	if _, ok := agent.AsDaemonProvider(a); ok {
		t.Error("claude-code must not implement DaemonProvider")
	}
	if _, ok := agent.AsWorktreeProvider(a); ok {
		t.Error("claude-code must not implement WorktreeProvider")
	}
}
