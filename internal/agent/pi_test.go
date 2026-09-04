package agent_test

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/inoio/agents-sandbox/internal/agent"
)

func TestPILookup(t *testing.T) {
	a, ok := agent.Lookup("pi")
	if !ok {
		t.Fatal("Lookup(\"pi\") returned not-ok")
	}
	if a.Name() != "pi" {
		t.Errorf("Lookup(\"pi\").Name() = %q, want pi", a.Name())
	}
	if a.ConfigDirName() != "pi" {
		t.Errorf("ConfigDirName = %q, want pi", a.ConfigDirName())
	}
}

func TestPINamesIncludesPi(t *testing.T) {
	if !slices.Contains(agent.Names(), "pi") {
		t.Errorf("Names() = %v, want to include pi", agent.Names())
	}
}

func TestPIImageSpec(t *testing.T) {
	a, _ := agent.Lookup("pi")
	spec := a.ImageSpec()
	if spec.VersionArg != "PI_VERSION" {
		t.Errorf("VersionArg = %q, want PI_VERSION", spec.VersionArg)
	}
	if _, ok := spec.AgentEnv["PI_SKIP_VERSION_CHECK"]; !ok {
		t.Errorf("AgentEnv = %v, want PI_SKIP_VERSION_CHECK key", spec.AgentEnv)
	}
	if !strings.Contains(spec.InstallCommand, "@earendil-works/pi-coding-agent") {
		t.Errorf("InstallCommand = %q, want pi npm install", spec.InstallCommand)
	}
}

func TestPIAttachCommand(t *testing.T) {
	a, _ := agent.Lookup("pi")
	runner, ok := agent.AsAttachRunner(a)
	if !ok {
		t.Fatal("pi should implement AttachRunner")
	}
	got := runner.AttachCommand("/workspace", []string{"--model", "claude"})
	want := "pi --model claude"
	if got != want {
		t.Errorf("AttachCommand = %q, want %q", got, want)
	}
}

func TestPIConfigMerger(t *testing.T) {
	a, _ := agent.Lookup("pi")
	merger, ok := agent.AsConfigMerger(a)
	if !ok {
		t.Fatal("pi should implement ConfigMerger")
	}
	if merger.SnippetPattern() != "settings*.json*" {
		t.Errorf("SnippetPattern = %q, want settings*.json*", merger.SnippetPattern())
	}
	if got := merger.VMConfigPath("/home/user"); got != filepath.Join("/home/user", ".pi", "agent", "settings.json") {
		t.Errorf("VMConfigPath = %q", got)
	}
	if names := merger.ConfigFileNames(); len(names) != 1 || names[0] != "settings.json" {
		t.Errorf("ConfigFileNames = %v, want [settings.json]", names)
	}
}

func TestPIProvisionRules(t *testing.T) {
	a, _ := agent.Lookup("pi")
	provisioner, ok := agent.AsProvisioner(a)
	if !ok {
		t.Fatal("pi should implement Provisioner")
	}
	found := false
	for _, r := range provisioner.ProvisionRules() {
		if r.Dir == ".pi/agent" {
			found = true
		}
	}
	if !found {
		t.Errorf("ProvisionRules missing .pi/agent: %+v", provisioner.ProvisionRules())
	}
}

func TestPILatestVersionCancelledCtx(t *testing.T) {
	a, _ := agent.Lookup("pi")
	checker, ok := agent.AsUpgradeChecker(a)
	if !ok {
		t.Fatal("pi should implement UpgradeChecker")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := checker.LatestVersion(ctx); err == nil {
		t.Error("LatestVersion with cancelled ctx should error")
	}
}

func TestPINewerThan(t *testing.T) {
	a, _ := agent.Lookup("pi")
	checker, ok := agent.AsUpgradeChecker(a)
	if !ok {
		t.Fatal("pi should implement UpgradeChecker")
	}
	gt, err := checker.NewerThan("v1.2.0", "1.1.0")
	if err != nil || !gt {
		t.Errorf("NewerThan(v1.2.0, 1.1.0) = %v, %v, want true, nil", gt, err)
	}
	lt, err := checker.NewerThan("1.0.0", "1.1.0")
	if err != nil || lt {
		t.Errorf("NewerThan(1.0.0, 1.1.0) = %v, %v, want false, nil", lt, err)
	}
	if _, err := checker.NewerThan("notaversion", "1.0.0"); err == nil {
		t.Error("NewerThan(invalid) should error")
	}
}

func TestPIHasUpgradeCheckerNoDaemonNoWorktree(t *testing.T) {
	a, _ := agent.Lookup("pi")
	if _, ok := agent.AsUpgradeChecker(a); !ok {
		t.Error("pi should implement UpgradeChecker")
	}
	if _, ok := agent.AsDaemonProvider(a); ok {
		t.Error("pi must not implement DaemonProvider")
	}
	if _, ok := agent.AsWorktreeProvider(a); ok {
		t.Error("pi must not implement WorktreeProvider")
	}
}
