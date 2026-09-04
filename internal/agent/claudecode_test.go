package agent_test

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/inoio/agents-sandbox/internal/agent"
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

func TestClaudeCodeLatestVersionCancelledCtx(t *testing.T) {
	a, _ := agent.Lookup("claude-code")
	checker, ok := agent.AsUpgradeChecker(a)
	if !ok {
		t.Fatal("claude-code should implement UpgradeChecker")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := checker.LatestVersion(ctx); err == nil {
		t.Error("LatestVersion with cancelled ctx should error")
	}
}

func TestClaudeCodeNewerThan(t *testing.T) {
	a, _ := agent.Lookup("claude-code")
	checker, ok := agent.AsUpgradeChecker(a)
	if !ok {
		t.Fatal("claude-code should implement UpgradeChecker")
	}
	gt, err := checker.NewerThan("v2.0.0", "1.9.9")
	if err != nil || !gt {
		t.Errorf("NewerThan(v2.0.0, 1.9.9) = %v, %v, want true, nil", gt, err)
	}
	lt, err := checker.NewerThan("1.0.0", "2.0.0")
	if err != nil || lt {
		t.Errorf("NewerThan(1.0.0, 2.0.0) = %v, %v, want false, nil", lt, err)
	}
	if _, err := checker.NewerThan("notaversion", "1.0.0"); err == nil {
		t.Error("NewerThan(invalid) should error")
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
