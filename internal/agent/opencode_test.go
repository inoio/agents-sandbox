package agent_test

import (
	"context"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/inoio/opencode-sandbox/internal/agent"
)

func TestOpencodeAttachCommand(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	cmd, ok := agent.AsAttachRunner(a)
	if !ok {
		t.Fatal("opencode should implement AttachRunner")
	}
	got := cmd.AttachCommand("/workspace", []string{"--print-logs"})
	want := "opencode attach http://127.0.0.1:4096 --dir /workspace --print-logs"
	if got != want {
		t.Errorf("AttachCommand = %q, want %q", got, want)
	}
}

func TestOpencodeWorktreeCreateCmdUsesName(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	worktree := mustWorktree(t, a)
	got := worktree.WorktreeCreateCmd(agent.WorktreeSpec{Name: "feature/x"})
	want := "curl -sf -X POST http://127.0.0.1:4096/experimental/worktree -H 'Content-Type: application/json' -d '{\"name\":\"feature/x\"}'"
	if got != want {
		t.Errorf("WorktreeCreateCmd = %q, want %q", got, want)
	}
}

func TestOpencodeDaemonStartCmd(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	daemon := mustDaemon(t, a)
	local := daemon.DaemonStartCmd(false)
	if !strings.Contains(local, "--hostname 127.0.0.1") {
		t.Errorf("local DaemonStartCmd = %q, want hostname 127.0.0.1", local)
	}
	serve := daemon.DaemonStartCmd(true)
	if !strings.Contains(serve, "--hostname 0.0.0.0") {
		t.Errorf("serve DaemonStartCmd = %q, want hostname 0.0.0.0", serve)
	}
}

func TestOpencodeDaemonKillAndHealthCmd(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	daemon := mustDaemon(t, a)
	if daemon.DaemonKillCmd() == "" {
		t.Error("DaemonKillCmd must not be empty")
	}
	if !strings.Contains(daemon.DaemonHealthCmd(), "127.0.0.1:4096") {
		t.Errorf("DaemonHealthCmd = %q, want port 4096", daemon.DaemonHealthCmd())
	}
}

func TestOpencodeDaemonHealthParse(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	daemon := mustDaemon(t, a)
	ok, err := daemon.DaemonHealthParse(`{"healthy":true,"version":"1.0.0"}`)
	if err != nil || !ok {
		t.Errorf("DaemonHealthParse(healthy) = %v, %v, want true, nil", ok, err)
	}
	notOK, err := daemon.DaemonHealthParse(`{"healthy":false}`)
	if err != nil || notOK {
		t.Errorf("DaemonHealthParse(unhealthy) = %v, %v, want false, nil", notOK, err)
	}
	if _, err := daemon.DaemonHealthParse("not json"); err == nil {
		t.Error("DaemonHealthParse(invalid) should error")
	}
}

func TestOpencodeWorktreeListCmd(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	worktree := mustWorktree(t, a)
	if !strings.Contains(worktree.WorktreeListCmd(), "/experimental/worktree") {
		t.Errorf("WorktreeListCmd = %q", worktree.WorktreeListCmd())
	}
}

func TestOpencodeWorktreeCreateCmdWithBase(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	worktree := mustWorktree(t, a)
	got := worktree.WorktreeCreateCmd(agent.WorktreeSpec{Name: "f", Base: "main"})
	if !strings.Contains(got, `"startCommand":"git reset --hard main"`) {
		t.Errorf("WorktreeCreateCmd(with base) = %q", got)
	}
}

func TestOpencodeWorktreeParseDir(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	worktree := mustWorktree(t, a)
	dir, ok := worktree.WorktreeParseDir(`{"directory":"/home/dev/ws"}`)
	if !ok || dir != "/home/dev/ws" {
		t.Errorf("WorktreeParseDir = %q, %v, want /home/dev/ws, true", dir, ok)
	}
	if _, ok := worktree.WorktreeParseDir("not json"); ok {
		t.Error("WorktreeParseDir(invalid) should return not-ok")
	}
	if _, ok := worktree.WorktreeParseDir(`{"directory":""}`); ok {
		t.Error("WorktreeParseDir(empty) should return not-ok")
	}
}

func TestOpencodeLatestVersionCancelledCtx(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	checker, ok := agent.AsUpgradeChecker(a)
	if !ok {
		t.Fatal("opencode should implement UpgradeChecker")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := checker.LatestVersion(ctx); err == nil {
		t.Error("LatestVersion with cancelled ctx should error")
	}
}

func TestOpencodeNewerThan(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	checker := mustChecker(t, a)
	gt, err := checker.NewerThan("1.2.0", "1.1.0")
	if err != nil || !gt {
		t.Errorf("NewerThan(1.2.0, 1.1.0) = %v, %v, want true, nil", gt, err)
	}
	lt, err := checker.NewerThan("1.0.0", "1.1.0")
	if err != nil || lt {
		t.Errorf("NewerThan(1.0.0, 1.1.0) = %v, %v, want false, nil", lt, err)
	}
	if _, err := checker.NewerThan("notaversion", "1.0.0"); err == nil {
		t.Error("NewerThan(invalid) should error")
	}
}

func TestOpencodeConfigMerger(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	merger, ok := agent.AsConfigMerger(a)
	if !ok {
		t.Fatal("opencode should implement ConfigMerger")
	}
	if merger.SnippetPattern() != "opencode-*.json*" {
		t.Errorf("SnippetPattern = %q", merger.SnippetPattern())
	}
	if merger.VMConfigPath("/home/user") != filepath.Join("/home/user", ".config", "opencode", "opencode.jsonc") {
		t.Errorf("VMConfigPath = %q", merger.VMConfigPath("/home/user"))
	}
}

func TestOpencodeConfigFileNames(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	merger, ok := agent.AsConfigMerger(a)
	if !ok {
		t.Fatal("opencode should implement ConfigMerger")
	}
	names := merger.ConfigFileNames()
	want := []string{
		"config.json",
		"opencode.json",
		"opencode.jsonc",
		"opencode.json5",
		"opencode.yaml",
		"opencode.yml",
	}
	if !slices.Equal(names, want) {
		t.Errorf("ConfigFileNames = %v, want %v", names, want)
	}
}

func TestOpencodeProvisionRules(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	provisioner, ok := agent.AsProvisioner(a)
	if !ok {
		t.Fatal("opencode should implement Provisioner")
	}
	rules := provisioner.ProvisionRules()
	if len(rules) == 0 {
		t.Fatal("ProvisionRules must not be empty")
	}
	found := false
	for _, r := range rules {
		if r.Dir == ".config/opencode" {
			found = true
		}
	}
	if !found {
		t.Errorf("ProvisionRules missing .config/opencode: %+v", rules)
	}
}

func mustDaemon(t *testing.T, a agent.Agent) agent.DaemonProvider {
	t.Helper()
	daemon, ok := agent.AsDaemonProvider(a)
	if !ok {
		t.Fatal("opencode should implement DaemonProvider")
	}
	return daemon
}

func mustWorktree(t *testing.T, a agent.Agent) agent.WorktreeProvider {
	t.Helper()
	worktree, ok := agent.AsWorktreeProvider(a)
	if !ok {
		t.Fatal("opencode should implement WorktreeProvider")
	}
	return worktree
}

func mustChecker(t *testing.T, a agent.Agent) agent.UpgradeChecker {
	t.Helper()
	checker, ok := agent.AsUpgradeChecker(a)
	if !ok {
		t.Fatal("opencode should implement UpgradeChecker")
	}
	return checker
}

func TestOpencodeEventStream(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	provider, ok := agent.AsEventStreamProvider(a)
	if !ok {
		t.Fatal("opencode should implement EventStreamProvider")
	}
	want := agent.EventStreamSpec{
		StreamCommand: "curl -N -s http://127.0.0.1:4096/global/event",
		BusyEvents:    []string{"message.part.updated", "session.updated"},
		AwaitingInput: []string{"permission.updated", "question.asked"},
		IdleEvents:    []string{"session.idle"},
		ErrorEvents:   []string{"session.error"},
		Name:          "opencode",
	}
	if got := provider.EventStream(); !reflect.DeepEqual(got, want) {
		t.Errorf("EventStream() = %+v, want %+v", got, want)
	}
}
